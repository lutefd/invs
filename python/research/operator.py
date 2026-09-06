"""Materialize deterministic operator inputs from canonical market data."""

from __future__ import annotations

import hashlib
import json
from collections.abc import Mapping
from datetime import UTC, date, datetime
from pathlib import Path
from typing import Any
from uuid import UUID, uuid5

import yaml

from .catalog import ResearchCatalog
from .experiments import canonical_json

_ARTIFACT_NAMESPACE = UUID("9197e3f6-1a28-5ea8-a068-2b1c14f28964")
_ACCOUNT_NAMESPACE = UUID("99a14327-e8cd-50a0-a2ac-d985eb9edac7")


class OperatorMaterializationError(ValueError):
    """Raised when a live operator snapshot cannot be built safely."""


def _timestamp(value: datetime | str) -> str:
    parsed = value if isinstance(value, datetime) else datetime.fromisoformat(value)
    if parsed.tzinfo is None or parsed.utcoffset() is None:
        raise OperatorMaterializationError("timestamps must include an offset")
    parsed = parsed.astimezone(UTC)
    fraction = f".{parsed.microsecond:06d}".rstrip("0") if parsed.microsecond else ""
    return parsed.strftime("%Y-%m-%dT%H:%M:%S") + fraction + "Z"


def _load_yaml(path: str | Path, *, label: str) -> dict[str, Any]:
    document = yaml.safe_load(Path(path).read_text(encoding="utf-8"))
    if not isinstance(document, dict):
        raise OperatorMaterializationError(f"{label} must be a YAML object")
    return document


def _load_json(path: str | Path, *, label: str) -> dict[str, Any]:
    document = json.loads(Path(path).read_text(encoding="utf-8"))
    if not isinstance(document, dict):
        raise OperatorMaterializationError(f"{label} must be a JSON object")
    return document


def _write_immutable(path: Path, document: Mapping[str, Any]) -> None:
    content = canonical_json(document) + b"\n"
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.exists():
        if path.read_bytes() != content:
            raise OperatorMaterializationError(f"refusing to replace immutable operator file {path}")
        return
    temporary = path.with_name(f".{path.name}.tmp")
    temporary.write_bytes(content)
    temporary.replace(path)


def _artifact(kind: str, rows: list[dict[str, Any]]) -> dict[str, Any]:
    if not rows:
        raise OperatorMaterializationError(f"cannot publish empty {kind} artifact")
    available_at = max(row["available_at"] for row in rows)
    identity = hashlib.sha256(canonical_json({"kind": kind, "rows": rows})).hexdigest()
    return {
        "schema_version": "1.0.0",
        "artifact_kind": kind,
        "artifact_id": str(uuid5(_ARTIFACT_NAMESPACE, identity)),
        "available_at": available_at,
        "rows": rows,
    }


def _reference(kind: str, path: Path, *, data_root: Path) -> dict[str, Any]:
    document = json.loads(path.read_text(encoding="utf-8"))
    try:
        relative = path.relative_to(data_root).as_posix()
    except ValueError as error:
        raise OperatorMaterializationError("operator output must be below data_root") from error
    return {
        "kind": kind,
        "artifact_id": document["artifact_id"],
        "path": relative,
        "sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
        "available_at": document["available_at"],
        "fitness": "current_research_only",
    }


def _price_rows(
    catalog: ResearchCatalog,
    security_ids: list[str],
    *,
    decision_at: str,
) -> list[dict[str, Any]]:
    rows = catalog.connection.execute(
        """
        WITH eligible AS (
          SELECT *
          FROM _prices_lineage
          WHERE security_id IN (SELECT unnest($security_ids::VARCHAR[]))
            AND interval = '1d'
            AND available_at <= CAST($decision_at AS TIMESTAMPTZ)
            AND observed_at <= CAST($decision_at AS TIMESTAMPTZ)
            AND price_basis IN ('raw', 'split_adjusted')
        ), selected AS (
          SELECT *
          FROM eligible
          QUALIFY row_number() OVER (
            PARTITION BY security_id, CAST(observed_at AS DATE)
            ORDER BY available_at DESC, ingested_at DESC, raw_payload_hash DESC,
                     manifest_path DESC, part_path DESC
          ) = 1
        )
        SELECT security_id, CAST(observed_at AS DATE), observed_at, available_at,
               currency, price_basis, open, high, low, close, volume, has_volume,
               raw_record_locator
        FROM selected
        ORDER BY CAST(observed_at AS DATE), security_id
        """,
        {"security_ids": security_ids, "decision_at": decision_at},
    ).fetchall()
    result: list[dict[str, Any]] = []
    for row in rows:
        result.append(
            {
                "security_id": row[0],
                "session_date": row[1].isoformat(),
                "observed_at": _timestamp(row[2]),
                "available_at": _timestamp(row[3]),
                "currency": row[4],
                "price_basis": row[5],
                "open": row[6],
                "high": row[7],
                "low": row[8],
                "close": row[9],
                "volume": row[10] if row[11] and row[10] else "0",
                "has_volume": bool(row[11]),
                "source_record_id": row[12],
            }
        )
    return result


def materialize_operator_snapshot(
    *,
    profile_path: str | Path,
    calendar_snapshot_path: str | Path,
    data_root: str | Path,
    output_root: str | Path,
    decision_at: str,
    not_before: str,
    git_commit: str = "unknown",
) -> dict[str, Any]:
    """Create feature inputs and, once forward-eligible, paper session inputs."""

    profile = _load_yaml(profile_path, label="universe profile")
    calendar = _load_json(calendar_snapshot_path, label="calendar snapshot")
    members = profile.get("members")
    if not isinstance(members, list) or not members:
        raise OperatorMaterializationError("universe profile members must be non-empty")
    security_ids = sorted(str(member["security_id"]) for member in members)
    if len(set(security_ids)) != len(security_ids):
        raise OperatorMaterializationError("universe profile security IDs must be unique")
    member_by_security = {str(member["security_id"]): member for member in members}

    cutoff = _timestamp(decision_at)
    activation = _timestamp(not_before)
    root = Path(data_root).resolve()
    output = Path(output_root).resolve()
    try:
        output.relative_to(root)
    except ValueError as error:
        raise OperatorMaterializationError("output_root must be below data_root") from error

    catalog = ResearchCatalog(root).register()
    prices = _price_rows(catalog, security_ids, decision_at=cutoff)
    dates_by_security = {
        security_id: {row["session_date"] for row in prices if row["security_id"] == security_id}
        for security_id in security_ids
    }
    common_dates = set.intersection(*(value for value in dates_by_security.values()))
    sessions = calendar.get("sessions")
    manifest = calendar.get("manifest")
    if not isinstance(sessions, list) or not isinstance(manifest, dict):
        raise OperatorMaterializationError("calendar snapshot requires manifest and sessions")
    open_sessions = {
        str(row["session_date"]): row
        for row in sessions
        if row.get("session_status") == "open"
        and _timestamp(row["close_at"]) <= cutoff
    }
    candidates = sorted(common_dates & set(open_sessions))
    if not candidates:
        missing = [member_by_security[item]["ticker"] for item, dates in dates_by_security.items() if not dates]
        raise OperatorMaterializationError(
            "no common closed XNAS session is available for the universe"
            + (f"; missing prices for {', '.join(missing)}" if missing else "")
        )
    session_date = candidates[-1]
    selected_session = open_sessions[session_date]
    snapshot_prices = [row for row in prices if row["session_date"] <= session_date]
    selected_ids = {row["security_id"] for row in snapshot_prices if row["session_date"] == session_date}
    if selected_ids != set(security_ids):
        raise OperatorMaterializationError("latest common session is missing a configured security")

    identity = hashlib.sha256(
        canonical_json(
            {
                "universe": security_ids,
                "session_date": session_date,
                "price_rows": snapshot_prices,
                "calendar_version": manifest["calendar_version"],
                "decision_at": cutoff,
            }
        )
    ).hexdigest()
    snapshot_root = output / "snapshots" / f"{session_date}-{identity[:12]}"
    feature_root = snapshot_root / "feature"
    feature_documents = {
        "universe.json": {"security_ids": security_ids},
        "schedule.json": {"decision_ats": [cutoff]},
        "calendar-pin.json": {
            "data_source_id": manifest["data_source_id"],
            "mic": manifest["mic"],
            "calendar_version": manifest["calendar_version"],
            "session_fingerprint": manifest["session_fingerprint"],
            "calendar_available_at": _timestamp(manifest["available_at"]),
            "decision_clock_policy": "after_close_next_session",
        },
        "security-mappings.json": {
            "mappings": sorted(
                (
                    {"security_id": item, "issuer_id": str(member_by_security[item]["issuer_id"])}
                    for item in security_ids
                ),
                key=lambda row: row["security_id"],
            )
        },
    }
    for name, document in feature_documents.items():
        _write_immutable(feature_root / name, document)

    latest_rows = [
        {
            "ticker": member_by_security[row["security_id"]]["ticker"],
            "security_id": row["security_id"],
            "session_date": row["session_date"],
            "close": row["close"],
            "currency": row["currency"],
            "price_basis": row["price_basis"],
            "available_at": row["available_at"],
            "source_record_id": row["source_record_id"],
        }
        for row in snapshot_prices
        if row["session_date"] == session_date
    ]
    latest_rows.sort(key=lambda row: row["ticker"])
    market_snapshot_path = snapshot_root / "market-snapshot.json"
    _write_immutable(
        market_snapshot_path,
        {
            "schema_version": "1.0.0",
            "session_date": session_date,
            "decision_at": cutoff,
            "historical_fitness": "installation_replay_only",
            "rows": latest_rows,
        },
    )

    result: dict[str, Any] = {
        "status": "feature_ready",
        "session_date": session_date,
        "decision_at": cutoff,
        "common_security_count": len(selected_ids),
        "market_snapshot": str(market_snapshot_path),
        "feature": {name.removesuffix(".json").replace("-", "_"): str(feature_root / name) for name in feature_documents},
        "paper": None,
    }
    membership_date = date.fromisoformat(str(profile["membership_as_of"]))
    activation_at = datetime.fromisoformat(activation)
    session_close = datetime.fromisoformat(_timestamp(selected_session["close_at"]))
    if date.fromisoformat(session_date) < membership_date or session_close < activation_at:
        result["status"] = "awaiting_forward_session"
        result["paper_reason"] = "latest complete close predates operator activation or membership admission"
        return result

    paper_root = snapshot_root / "paper"
    input_root = paper_root / "inputs"
    calendar_rows = [
        {
            "session_date": str(row["session_date"]),
            "open_at": _timestamp(row["open_at"]),
            "close_at": _timestamp(row["close_at"]),
            "available_at": _timestamp(row["available_at"]),
            "session_status": "early_close" if row.get("is_early_close") else "open",
        }
        for row in sessions
        if row.get("session_status") == "open"
        and date.fromisoformat(str(row["session_date"])) >= membership_date
    ]
    membership_available = max(activation, f"{membership_date.isoformat()}T00:00:00Z")
    membership_rows = [
        {
            "security_id": security_id,
            "valid_from": membership_date.isoformat(),
            "valid_until": None,
            "member": True,
            "available_at": membership_available,
            "revision": 0,
        }
        for security_id in security_ids
    ]
    artifacts = {
        "prices": _artifact("prices", snapshot_prices),
        "calendar": _artifact("calendar", calendar_rows),
        "membership": _artifact("membership", membership_rows),
    }
    references: list[dict[str, Any]] = []
    for kind, document in artifacts.items():
        path = input_root / f"{kind}.json"
        _write_immutable(path, document)
        references.append(_reference(kind, path, data_root=root))
    references.sort(key=lambda row: row["kind"])
    input_bundle = {"inputs": references}
    bundle_path = paper_root / "session-inputs.json"
    _write_immutable(bundle_path, input_bundle)

    membership_fingerprint = hashlib.sha256(canonical_json(membership_rows)).hexdigest()
    account_id = str(uuid5(_ACCOUNT_NAMESPACE, f"{profile['universe_id']}:equal-weight-v1"))
    paper_spec = {
        "schema_version": "1.0.0",
        "account_id": account_id,
        "name": "Nasdaq-100 starter equal-weight forward paper account",
        "strategy": {
            "name": "equal_weight",
            "version": "1.0.0",
            "git_commit": git_commit,
            "parameters": {"rebalance_frequency": "weekly"},
        },
        "period": {
            "start_date": membership_date.isoformat(),
            "end_date": max(str(row["session_date"]) for row in calendar_rows),
        },
        "universe": {
            "universe_id": profile["universe_id"],
            "version": "1.0.0",
            "security_ids": security_ids,
            "membership_fingerprint": membership_fingerprint,
        },
        "security_metadata": [
            {
                "security_id": security_id,
                "country": "US",
                "sector": member_by_security[security_id]["sector"],
                "themes": member_by_security[security_id].get("themes", []),
            }
            for security_id in security_ids
        ],
        "inputs": references,
        "benchmark": {"security_id": security_ids[0], "currency": "USD"},
        "decision_policy": {
            "frequency": "daily",
            "decision_at": "close",
            "signal_delay_sessions": 1,
            "execution_price": "open",
            "stale_after_sessions": 3,
            "max_price_gap": "0.25",
        },
        "accounting_policy": {
            "base_currency": "USD",
            "initial_cash": "100000",
            "fractional_shares": True,
            "rebalance_frequency": "weekly",
        },
        "cost_policy": {
            "version": "1.0.0",
            "commission_bps": "0",
            "fixed_fee": "0",
            "minimum_fee": "0",
            "spread_bps": "2",
            "slippage_bps": "3",
            "tax_bps": "0",
        },
        "risk_policy": {
            "version": "1.0.0",
            "max_gross_exposure": "0.96",
            "max_position_weight": "0.08",
            "max_sector_exposure": "0.8",
            "max_country_exposure": "1",
            "max_currency_exposure": "1",
            "max_theme_exposure": "0.8",
            "minimum_cash_reserve": "0.04",
            "max_turnover": "1",
            "max_daily_notional": "100000",
            "max_price_gap": "0.25",
            "max_stale_sessions": 3,
            "max_participation": "0.01",
            "max_drawdown": "0.25",
            "prohibited_security_ids": [],
        },
        "approval_policy": {"mode": "auto"},
        "missing_data_policy": "halt_decision",
        "promoted_backtest": None,
    }
    # Keep the paper engine out of the market-feature import path. Besides
    # preserving that architectural boundary, the deferred import means a
    # feature-only materialization does not load paper/backtest dependencies.
    from .paper import validate_paper_account

    paper_spec = validate_paper_account(paper_spec)
    spec_path = paper_root / "account.json"
    _write_immutable(spec_path, paper_spec)
    result["status"] = "paper_ready"
    result["paper"] = {
        "account_id": account_id,
        "spec": str(spec_path),
        "inputs": str(bundle_path),
        "ledger_root": str(output / "ledger"),
    }
    return result
