"""Retrospective price and stock-discovery backfills.

These artifacts use the latest installed normalized price vintage to replay
historical closes. They are labeled explicitly and never enter the operational
``discovery_indexes`` catalog used for genuine daily discovery runs.
"""

from __future__ import annotations

import hashlib
import json
from collections import defaultdict
from datetime import UTC, date, datetime, timedelta
from decimal import Decimal
from pathlib import Path
from typing import Any
from uuid import UUID, uuid5
from zoneinfo import ZoneInfo

from .backfill_artifacts import (
    BackfillArtifactError,
    publish_retrospective_artifact_tree,
    validate_retrospective_artifact_tree,
)
from .catalog import ResearchCatalog
from .discovery import (
    MODEL_NAME,
    MODEL_VERSION,
    _REQUIRED_FEATURES,
    _WEIGHTS,
    _fmt,
    _load_profile,
    _percentiles,
    _sha256,
)
from .experiments import canonical_json
from .market_momentum import _compute_features

OBSERVATION_MODE = "retrospective_current_universe"
UNIVERSE_BASIS = "current_profile_membership_snapshot"
INPUT_FITNESS = "installation_replay_only"
AVAILABILITY_POLICY = "current_vintage_with_preserved_receipt_times"
GENERATOR_VERSION = "python-retrospective-discovery-1.0.0"
_NAMESPACE = UUID("dfd74827-a6cb-5a1a-821d-22bbd8a06d5e")
_NEW_YORK = ZoneInfo("America/New_York")


class RetrospectiveBackfillError(ValueError):
    """Raised when a retrospective backfill cannot be reconstructed safely."""


def _timestamp(value: Any, *, field: str) -> datetime:
    if isinstance(value, datetime):
        parsed = value
    elif isinstance(value, str):
        parsed = datetime.fromisoformat(value)
    else:
        raise RetrospectiveBackfillError(f"{field} must be an ISO timestamp")
    if parsed.tzinfo is None or parsed.utcoffset() is None:
        raise RetrospectiveBackfillError(f"{field} must include a timezone")
    return parsed.astimezone(UTC)


def _iso(value: datetime) -> str:
    return value.astimezone(UTC).isoformat(timespec="microseconds").replace("+00:00", "Z")


def _go_rfc3339nano(value: datetime) -> str:
    canonical = value.astimezone(UTC)
    rendered = canonical.strftime("%Y-%m-%dT%H:%M:%S")
    if canonical.microsecond:
        rendered += "." + f"{canonical.microsecond:06d}".rstrip("0")
    return rendered + "Z"


def _date(value: Any, *, field: str) -> date:
    try:
        return date.fromisoformat(str(value))
    except ValueError as error:
        raise RetrospectiveBackfillError(f"{field} must be an ISO date") from error


def _subtract_months(value: date, months: int) -> date:
    if months <= 0:
        raise RetrospectiveBackfillError("months must be positive")
    absolute_month = value.year * 12 + value.month - 1 - months
    year, month_zero = divmod(absolute_month, 12)
    month = month_zero + 1
    next_month = date(year + (month == 12), 1 if month == 12 else month + 1, 1)
    last_day = (next_month - timedelta(days=1)).day
    return date(year, month, min(value.day, last_day))


def _calendar_session_fingerprint(sessions: list[dict[str, Any]]) -> str:
    canonical: list[dict[str, Any]] = []
    sortable: list[tuple[tuple[str, ...], dict[str, Any]]] = []
    for item in sessions:
        open_at = None if item.get("open_at") is None else _go_rfc3339nano(
            _timestamp(item["open_at"], field="session.open_at")
        )
        close_at = None if item.get("close_at") is None else _go_rfc3339nano(
            _timestamp(item["close_at"], field="session.close_at")
        )
        record = {
            "close_at": close_at,
            "exchange_timezone": item["exchange_timezone"],
            "is_early_close": bool(item["is_early_close"]),
            "mic": item["mic"],
            "open_at": open_at,
            "session_date": _date(item["session_date"], field="session_date").isoformat(),
            "session_status": item["session_status"],
        }
        sort_key = (
            record["mic"],
            record["exchange_timezone"],
            record["session_date"],
            record["session_status"],
            record["open_at"] if open_at is not None else "<nil>",
            record["close_at"] if close_at is not None else "<nil>",
            str(record["is_early_close"]).lower(),
        )
        sortable.append((sort_key, record))
    canonical.extend(record for _, record in sorted(sortable, key=lambda item: item[0]))
    encoded = json.dumps(canonical, ensure_ascii=True, separators=(",", ":")).encode("utf-8")
    return hashlib.sha256(encoded).hexdigest()


def _load_calendar_snapshot(path: str | Path) -> tuple[dict[str, Any], list[dict[str, Any]], str]:
    source = Path(path)
    raw = source.read_bytes()
    document = json.loads(raw)
    if not isinstance(document, dict) or set(document) != {"manifest", "sessions"}:
        raise RetrospectiveBackfillError("calendar snapshot must contain manifest and sessions")
    manifest = document["manifest"]
    expected_manifest = {
        "data_source_id", "mic", "exchange_timezone", "calendar_version", "session_fingerprint",
        "session_count", "available_at",
    }
    if not isinstance(manifest, dict) or not expected_manifest <= set(manifest):
        raise RetrospectiveBackfillError("calendar snapshot manifest is incomplete")
    if manifest["mic"] != "XNAS" or manifest["exchange_timezone"] != "America/New_York":
        raise RetrospectiveBackfillError("retrospective starter-universe backfill requires XNAS")
    UUID(str(manifest["data_source_id"]))
    session_count = manifest["session_count"]
    if not isinstance(session_count, int) or isinstance(session_count, bool) or session_count <= 0:
        raise RetrospectiveBackfillError("calendar.session_count must be a positive integer")
    if len(document["sessions"]) != session_count:
        raise RetrospectiveBackfillError(
            f"calendar exported {len(document['sessions'])} rows but manifest declares {session_count}"
        )
    _timestamp(manifest["available_at"], field="calendar.available_at")
    if not isinstance(document["sessions"], list):
        raise RetrospectiveBackfillError("calendar sessions must be an array")
    latest: dict[date, dict[str, Any]] = {}
    for item in document["sessions"]:
        if not isinstance(item, dict):
            raise RetrospectiveBackfillError("calendar session must be an object")
        session_date = _date(item.get("session_date"), field="session_date")
        status = item.get("session_status")
        if status not in {"open", "closed"}:
            raise RetrospectiveBackfillError("calendar session_status is invalid")
        normalized = dict(item)
        if item.get("mic") != manifest["mic"] or item.get("exchange_timezone") != manifest["exchange_timezone"]:
            raise RetrospectiveBackfillError("calendar session identity differs from its manifest")
        normalized["_session_date"] = session_date
        normalized["_revision"] = int(item.get("revision", 0))
        if normalized["_revision"] < 0:
            raise RetrospectiveBackfillError("calendar revision cannot be negative")
        normalized["_available_at"] = _timestamp(
            item.get("available_at", manifest["available_at"]),
            field="session.available_at",
        )
        if status == "open":
            normalized["_open_at"] = _timestamp(item.get("open_at"), field="session.open_at")
            normalized["_close_at"] = _timestamp(item.get("close_at"), field="session.close_at")
            if normalized["_open_at"] >= normalized["_close_at"]:
                raise RetrospectiveBackfillError("open XNAS session must close after it opens")
        else:
            normalized["_open_at"] = None
            normalized["_close_at"] = None
        previous = latest.get(session_date)
        order = (normalized["_revision"], normalized["_available_at"])
        if previous is None:
            latest[session_date] = normalized
            continue
        previous_order = (previous["_revision"], previous["_available_at"])
        if order == previous_order and normalized != previous:
            raise RetrospectiveBackfillError(
                f"calendar has conflicting revisions for {session_date.isoformat()}"
            )
        if order > previous_order:
            latest[session_date] = normalized
    selected = sorted(latest.values(), key=lambda item: item["_session_date"])
    fingerprint = _calendar_session_fingerprint(document["sessions"])
    if fingerprint != manifest["session_fingerprint"]:
        raise RetrospectiveBackfillError("calendar snapshot session fingerprint does not match its manifest")
    return manifest, selected, hashlib.sha256(raw).hexdigest()


def _data_relative_path(value: Any, *, data_root: Path, field: str) -> str:
    path = Path(str(value))
    if not path.is_absolute():
        absolute = (data_root / path).resolve()
    else:
        absolute = path.resolve()
    try:
        return absolute.relative_to(data_root).as_posix()
    except ValueError as error:
        raise RetrospectiveBackfillError(f"{field} escapes data_root") from error


def _load_installed_prices(
    catalog: ResearchCatalog,
    *,
    security_ids: list[str],
    source: str,
    price_basis: str,
    observed_through: datetime,
    available_through: datetime,
) -> list[dict[str, Any]]:
    if not security_ids:
        raise RetrospectiveBackfillError("universe has no securities")
    placeholders = ", ".join(f"${index}" for index in range(1, len(security_ids) + 1))
    source_slot = len(security_ids) + 1
    basis_slot = len(security_ids) + 2
    available_slot = len(security_ids) + 3
    observed_slot = len(security_ids) + 4
    query = f"""
        WITH eligible AS (
            SELECT *
            FROM _prices_lineage
            WHERE security_id IN ({placeholders})
              AND source = ${source_slot}
              AND interval = '1d'
              AND price_basis = ${basis_slot}
              AND available_at IS NOT NULL
              AND observed_at IS NOT NULL
              AND available_at <= ${available_slot}
              AND observed_at <= ${observed_slot}
        )
        SELECT *
        FROM eligible
        QUALIFY row_number() OVER (
            PARTITION BY source, security_id, interval, price_basis, observed_at
            ORDER BY available_at DESC, ingested_at DESC,
                     raw_payload_hash DESC NULLS LAST, manifest_path DESC, part_path DESC
        ) = 1
        ORDER BY security_id, observed_at, source, price_basis, available_at,
                 ingested_at, raw_payload_hash NULLS LAST, manifest_path, part_path
    """
    params: list[Any] = [*security_ids, source, price_basis, available_through, observed_through]
    cursor = catalog.connection.execute(query, params)
    columns = [item[0] for item in cursor.description]
    return [dict(zip(columns, row, strict=True)) for row in cursor.fetchall()]


def _refs_for_prices(rows: list[dict[str, Any]], *, data_root: Path) -> list[dict[str, Any]]:
    parts_by_manifest: dict[str, dict[str, str]] = defaultdict(dict)
    for row in rows:
        manifest_path = _data_relative_path(row["manifest_path"], data_root=data_root, field="manifest_path")
        part_path = _data_relative_path(row["part_path"], data_root=data_root, field="part_path")
        manifest_file = data_root / manifest_path
        if not manifest_file.is_file():
            raise RetrospectiveBackfillError(f"selected price manifest is missing: {manifest_path}")
        parts_by_manifest[manifest_path][part_path] = str(row["part_sha256"])
    result: list[dict[str, Any]] = []
    for manifest_path, parts in sorted(parts_by_manifest.items()):
        manifest_file = data_root / manifest_path
        manifest_sha256 = _sha256(manifest_file)
        manifest = json.loads(manifest_file.read_text(encoding="utf-8"))
        listed = {part["path"]: part["sha256"] for part in manifest.get("parts", [])}
        for part_path, part_sha in parts.items():
            # Catalog registration has verified bytes; this check also makes the
            # exact row-to-part lineage explicit in the backfill envelope.
            relative_in_manifest = Path(part_path).relative_to(Path(manifest_path).parent).as_posix()
            if listed.get(relative_in_manifest) != part_sha:
                raise RetrospectiveBackfillError(
                    f"price row part reference differs from manifest: {part_path}"
                )
        pinned_parts: list[dict[str, str]] = []
        for part in manifest.get("parts", []):
            source_part = (Path(manifest_path).parent / part["path"]).as_posix()
            source_part_file = data_root / source_part
            if not source_part_file.is_file() or _sha256(source_part_file) != part["sha256"]:
                raise RetrospectiveBackfillError(
                    f"price part changed after catalog validation: {source_part}"
                )
            pinned_parts.append({"source_path": source_part, "sha256": part["sha256"]})
        result.append(
            {
                "source_path": manifest_path,
                "sha256": manifest_sha256,
                "parts": sorted(pinned_parts, key=lambda item: item["source_path"]),
            }
        )
    return result


def _price_record(row: dict[str, Any]) -> dict[str, Any]:
    close = row["close"]
    close_text = None if close is None else format(Decimal(str(close)), "f")
    return {
        "source": row["source"],
        "interval": row["interval"],
        "price_basis": row["price_basis"],
        "currency": row["currency"],
        "observed_at": row["observed_at"],
        "available_at": row["available_at"],
        "close_value": close_text,
        "raw_payload_hash": row["raw_payload_hash"],
        "raw_record_locator": row["raw_record_locator"],
    }


def _session_fingerprint(
    *,
    session_date: date,
    close_at: datetime,
    records_by_security: dict[str, list[dict[str, Any]]],
    data_root: Path,
) -> str:
    lineage: list[dict[str, Any]] = []
    for security_id, records in sorted(records_by_security.items()):
        eligible = [
            row for row in records
            if _timestamp(row["observed_at"], field="price.observed_at") <= close_at
        ]
        lineage.extend(
            {
                "security_id": security_id,
                "observed_at": _iso(_timestamp(row["observed_at"], field="price.observed_at")),
                "available_at": _iso(_timestamp(row["available_at"], field="price.available_at")),
                "raw_payload_hash": row["raw_payload_hash"],
                "manifest_path": _data_relative_path(row["manifest_path"], data_root=data_root, field="manifest_path"),
                "part_path": _data_relative_path(row["part_path"], data_root=data_root, field="part_path"),
                "part_sha256": row["part_sha256"],
            }
            for row in eligible
        )
    return hashlib.sha256(
        canonical_json(
            {
                "market_session": session_date.isoformat(),
                "decision_at": _iso(close_at),
                "selection": "latest_installed_revision_observed_by_session_close",
                "price_rows": lineage,
            }
        )
    ).hexdigest()


def _rank_session(
    *,
    members: dict[str, dict[str, Any]],
    records_by_security: dict[str, list[dict[str, Any]]],
    session_date: date,
    close_at: datetime,
    exact_bars: dict[str, dict[str, Any]],
    candidate_count: int,
) -> tuple[list[dict[str, Any]], int, int]:
    values: dict[str, dict[str, Decimal]] = {}
    reasons: dict[str, list[str]] = {}
    for security_id in sorted(members):
        if security_id not in exact_bars:
            reasons[security_id] = ["missing_price_bar_for_market_session"]
            continue
        prior = [
            row for row in records_by_security.get(security_id, [])
            if _timestamp(row["observed_at"], field="price.observed_at") <= close_at
        ]
        features = _compute_features([_price_record(row) for row in prior])
        missing = [name for name in _REQUIRED_FEATURES if features.get(name) is None]
        if missing:
            reasons[security_id] = [f"null_feature:{name}" for name in missing]
            continue
        values[security_id] = {name: Decimal(str(features[name])) for name in _REQUIRED_FEATURES}
    if not values:
        raise RetrospectiveBackfillError(
            f"no securities have complete features on {session_date.isoformat()}"
        )
    components = {
        "return_3m": _percentiles({key: row["return_3m"] for key, row in values.items()}),
        "return_6m": _percentiles({key: row["return_6m"] for key, row in values.items()}),
        "return_12m": _percentiles({key: row["return_12m"] for key, row in values.items()}),
        "low_volatility_1m": _percentiles(
            {key: row["realized_volatility_1m"] for key, row in values.items()},
            higher_is_better=False,
        ),
        "shallow_drawdown_1m": _percentiles(
            {key: row["max_drawdown_1m"] for key, row in values.items()}
        ),
    }
    scores = {
        security_id: sum(
            (components[name][security_id] * weight for name, weight in _WEIGHTS.items()),
            Decimal(0),
        ) * 100
        for security_id in values
    }
    ordered = sorted(values, key=lambda item: (-scores[item], members[item]["ticker"], item))
    rows: list[dict[str, Any]] = []
    for rank, security_id in enumerate(ordered, start=1):
        member = members[security_id]
        features = values[security_id]
        rows.append(
            {
                "rank": rank,
                "security_id": security_id,
                "ticker": member["ticker"],
                "sector": member["sector"],
                "candidate": rank <= candidate_count,
                "score": _fmt(scores[security_id], "0.000001"),
                "features": {name: _fmt(features[name]) for name in _REQUIRED_FEATURES},
                "eligibility": {"status": "eligible", "reasons": []},
            }
        )
    for security_id in sorted(reasons, key=lambda item: members[item]["ticker"]):
        member = members[security_id]
        rows.append(
            {
                "rank": None,
                "security_id": security_id,
                "ticker": member["ticker"],
                "sector": member["sector"],
                "candidate": False,
                "score": None,
                "features": {name: None for name in _REQUIRED_FEATURES},
                "eligibility": {"status": "rejected", "reasons": reasons[security_id]},
            }
        )
    return rows, len(values), len(reasons)


def _price_bar(row: dict[str, Any], *, member: dict[str, Any], data_root: Path, manifest_refs: dict[str, dict[str, Any]]) -> dict[str, Any]:
    source_manifest_path = _data_relative_path(row["manifest_path"], data_root=data_root, field="manifest_path")
    part_path = _data_relative_path(row["part_path"], data_root=data_root, field="part_path")
    manifest_ref = manifest_refs[source_manifest_path]
    part_refs = {part["source_path"]: part for part in manifest_ref["parts"]}
    part_ref = part_refs[part_path]
    return {
        "security_id": row["security_id"],
        "ticker": member["ticker"],
        "observed_at": _iso(_timestamp(row["observed_at"], field="price.observed_at")),
        "available_at": _iso(_timestamp(row["available_at"], field="price.available_at")),
        "ingested_at": _iso(_timestamp(row["ingested_at"], field="price.ingested_at")),
        "source": row["source"],
        "interval": row["interval"],
        "price_basis": row["price_basis"],
        "currency": row["currency"],
        "open": row["open"],
        "high": row["high"],
        "low": row["low"],
        "close": row["close"],
        "volume": row["volume"],
        "raw_payload_hash": row["raw_payload_hash"],
        "data_source_id": row["data_source_id"],
        "ingestion_run_id": row["ingestion_run_id"],
        "raw_record_locator": row["raw_record_locator"],
        "manifest_path": manifest_ref["path"],
        "source_manifest_path": source_manifest_path,
        "manifest_sha256": manifest_ref["sha256"],
        "part_path": part_ref["path"],
        "source_part_path": part_path,
        "part_sha256": part_ref["sha256"],
    }


def _date_window(*, months: int, start_date: str | None, end_date: str | None, local_today: date) -> tuple[date, date]:
    end = _date(end_date, field="end_date") if end_date else local_today
    start = _date(start_date, field="start_date") if start_date else _subtract_months(end, months)
    if start > end:
        raise RetrospectiveBackfillError("start_date must not be after end_date")
    return start, end


def _session_rows(
    sessions: list[dict[str, Any]], *, start: date, end: date, cutoff: datetime
) -> list[tuple[date, datetime]]:
    covered_dates = [item["_session_date"] for item in sessions]
    if not covered_dates or covered_dates[0] > start or covered_dates[-1] < end:
        raise RetrospectiveBackfillError(
            "XNAS calendar snapshot does not cover both requested window boundaries"
        )
    result = [
        (item["_session_date"], item["_close_at"])
        for item in sessions
        if item["session_status"] == "open"
        and start <= item["_session_date"] <= end
        and item["_close_at"] <= cutoff
    ]
    if not result:
        raise RetrospectiveBackfillError("calendar contains no completed XNAS sessions in the requested window")
    return result


def validate_retrospective_backfill(path: str | Path) -> dict[str, Any]:
    """Validate one immutable retrospective run manifest."""

    manifest_path = Path(path).resolve()
    try:
        document = validate_retrospective_artifact_tree(manifest_path)
    except (BackfillArtifactError, OSError, json.JSONDecodeError) as error:
        raise RetrospectiveBackfillError(f"cannot validate backfill artifact tree: {error}") from error
    expected = {
        "schema_version", "reconstruction_run_id", "observation_mode", "universe_basis",
        "reconstructed_at", "input_fingerprint", "generator_version", "profile", "model", "range", "calendar",
        "price_inputs", "sessions",
    }
    if not isinstance(document, dict) or set(document) != expected:
        raise RetrospectiveBackfillError("backfill manifest fields are invalid")
    if document["schema_version"] != "1.0.0":
        raise RetrospectiveBackfillError("unsupported retrospective backfill schema")
    if document["generator_version"] != GENERATOR_VERSION:
        raise RetrospectiveBackfillError("unsupported retrospective backfill generator version")
    if document["observation_mode"] != OBSERVATION_MODE or document["universe_basis"] != UNIVERSE_BASIS:
        raise RetrospectiveBackfillError("retrospective backfill labels are invalid")
    UUID(document["reconstruction_run_id"])
    _timestamp(document["reconstructed_at"], field="reconstructed_at")
    if not isinstance(document["input_fingerprint"], str) or len(document["input_fingerprint"]) != 64:
        raise RetrospectiveBackfillError("input_fingerprint is invalid")
    profile = document["profile"]
    if not isinstance(profile, dict) or set(profile) != {
        "universe_id", "name", "membership_as_of", "fingerprint", "profile_sha256", "size",
        "snapshot_path",
    }:
        raise RetrospectiveBackfillError("backfill profile fields are invalid")
    UUID(profile["universe_id"])
    _date(profile["membership_as_of"], field="profile.membership_as_of")
    calendar = document["calendar"]
    if not isinstance(calendar, dict) or set(calendar) != {
        "data_source_id", "mic", "calendar_version", "session_fingerprint", "session_count",
        "available_at", "snapshot_sha256", "snapshot_path",
    } or calendar["mic"] != "XNAS":
        raise RetrospectiveBackfillError("backfill calendar fields are invalid")
    UUID(calendar["data_source_id"])
    if not isinstance(calendar["session_count"], int) or calendar["session_count"] <= 0:
        raise RetrospectiveBackfillError("backfill calendar session_count is invalid")
    _timestamp(calendar["available_at"], field="calendar.available_at")
    price_inputs = document["price_inputs"]
    if not isinstance(price_inputs, dict) or set(price_inputs) != {
        "source", "interval", "price_basis", "currency", "historical_fitness",
        "availability_policy", "input_fingerprint", "selected_row_count", "manifests",
    }:
        raise RetrospectiveBackfillError("backfill price_inputs fields are invalid")
    if (
        price_inputs["historical_fitness"] != INPUT_FITNESS
        or price_inputs["availability_policy"] != AVAILABILITY_POLICY
    ):
        raise RetrospectiveBackfillError("backfill price fitness labels are invalid")
    if not isinstance(document["sessions"], list) or not document["sessions"]:
        raise RetrospectiveBackfillError("backfill sessions must be a non-empty array")
    session_dates: list[str] = []
    for session in document["sessions"]:
        if not isinstance(session, dict) or set(session) != {
            "index_id", "market_session", "decision_at", "candidate_count", "eligible_count",
            "rejected_count", "price_input_fingerprint", "rankings", "price_bars",
        }:
            raise RetrospectiveBackfillError("backfill session fields are invalid")
        UUID(session["index_id"])
        market_session = _date(session["market_session"], field="market_session")
        session_dates.append(market_session.isoformat())
        _timestamp(session["decision_at"], field="session.decision_at")
        ranks: list[int] = []
        security_ids: set[str] = set()
        for row in session["rankings"]:
            UUID(row["security_id"])
            if row["security_id"] in security_ids:
                raise RetrospectiveBackfillError("backfill session has duplicate securities")
            security_ids.add(row["security_id"])
            if row["eligibility"]["status"] == "eligible":
                ranks.append(row["rank"])
            elif row["eligibility"]["status"] != "rejected":
                raise RetrospectiveBackfillError("backfill eligibility status is invalid")
        if sorted(ranks) != list(range(1, len(ranks) + 1)):
            raise RetrospectiveBackfillError("backfill ranks are not contiguous")
        if len(session["rankings"]) != session["eligible_count"] + session["rejected_count"]:
            raise RetrospectiveBackfillError("backfill counts differ from ranking rows")
        if sum(bool(row["candidate"]) for row in session["rankings"]) != session["candidate_count"]:
            raise RetrospectiveBackfillError("backfill candidate count differs from rows")
    if session_dates != sorted(set(session_dates)):
        raise RetrospectiveBackfillError("backfill sessions must be unique and date ordered")
    expected_run_id = str(uuid5(_NAMESPACE, document["input_fingerprint"]))
    if document["reconstruction_run_id"] != expected_run_id:
        raise RetrospectiveBackfillError("backfill id does not match its input fingerprint")
    return document


def publish_retrospective_backfill(
    *,
    profile_path: str | Path,
    calendar_snapshot_path: str | Path,
    data_root: str | Path,
    output_root: str | Path,
    months: int = 3,
    start_date: str | None = None,
    end_date: str | None = None,
    source: str = "yahoo",
    price_basis: str = "split_adjusted",
    currency: str = "USD",
    candidate_count: int = 5,
    reconstructed_at: str | datetime | None = None,
    git_commit: str = "unknown",
) -> Path:
    """Publish one deterministic retrospective daily price/ranking backfill."""

    profile_source = Path(profile_path).resolve()
    profile_sha256 = _sha256(profile_source)
    profile = _load_profile(profile_source)
    manifest, calendar_sessions, calendar_snapshot_sha256 = _load_calendar_snapshot(calendar_snapshot_path)
    captured_at = (
        _timestamp(reconstructed_at, field="reconstructed_at")
        if reconstructed_at is not None
        else datetime.now(UTC)
    )
    local_today = captured_at.astimezone(_NEW_YORK).date()
    start, end = _date_window(
        months=months, start_date=start_date, end_date=end_date, local_today=local_today
    )
    sessions = _session_rows(calendar_sessions, start=start, end=end, cutoff=captured_at)
    max_close = max(close for _, close in sessions)
    members = {str(member["security_id"]): member for member in profile["members"]}
    if candidate_count <= 0 or candidate_count > len(members):
        raise RetrospectiveBackfillError("candidate_count must be within the current profile size")
    if currency != "USD" or source != "yahoo" or price_basis != "split_adjusted":
        raise RetrospectiveBackfillError("Nasdaq starter backfill requires Yahoo USD split-adjusted prices")

    root = Path(data_root).expanduser().resolve()
    catalog = ResearchCatalog(root).register()
    prices = _load_installed_prices(
        catalog,
        security_ids=sorted(members),
        source=source,
        price_basis=price_basis,
        observed_through=max_close,
        available_through=captured_at,
    )
    catalog.connection.close()
    for row in prices:
        row["manifest_path"] = _data_relative_path(row["manifest_path"], data_root=root, field="manifest_path")
        row["part_path"] = _data_relative_path(row["part_path"], data_root=root, field="part_path")
        if row["currency"] != currency or row["interval"] != "1d":
            raise RetrospectiveBackfillError("price inputs include an unexpected currency or interval")
    present_series = {
        (row["source"], row["interval"], row["price_basis"], row["currency"])
        for row in prices
    }
    if present_series - {(source, "1d", price_basis, currency)}:
        raise RetrospectiveBackfillError("price inputs mix incompatible price series")
    by_security: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for row in prices:
        by_security[row["security_id"]].append(row)
    if set(by_security) != set(members):
        missing = sorted(set(members) - set(by_security))
        raise RetrospectiveBackfillError(
            "current price catalog is missing profile securities: " + ", ".join(missing)
        )
    for security_id, rows in by_security.items():
        rows.sort(key=lambda row: _timestamp(row["observed_at"], field="price.observed_at"))
        local_dates: set[date] = set()
        for row in rows:
            local_date = _timestamp(row["observed_at"], field="price.observed_at").astimezone(_NEW_YORK).date()
            if local_date in local_dates:
                raise RetrospectiveBackfillError(
                    f"multiple daily bars for {members[security_id]['ticker']} on {local_date}"
                )
            local_dates.add(local_date)

    source_refs = _refs_for_prices(prices, data_root=root)
    canonical_profile = dict(profile)
    canonical_profile["membership_as_of"] = _date(
        profile["membership_as_of"], field="profile.membership_as_of"
    ).isoformat()
    price_rows_fingerprint = [
        {
            "security_id": row["security_id"],
            "observed_at": _iso(_timestamp(row["observed_at"], field="price.observed_at")),
            "available_at": _iso(_timestamp(row["available_at"], field="price.available_at")),
            "close": row["close"],
            "raw_payload_hash": row["raw_payload_hash"],
            "manifest_path": row["manifest_path"],
            "part_path": row["part_path"],
            "part_sha256": row["part_sha256"],
        }
        for row in prices
    ]
    price_input_fingerprint = hashlib.sha256(canonical_json(price_rows_fingerprint)).hexdigest()
    profile_record = {
        "universe_id": str(profile["universe_id"]),
        "name": profile["name"],
        "membership_as_of": str(profile["membership_as_of"]),
        "fingerprint": hashlib.sha256(canonical_json(canonical_profile)).hexdigest(),
        "profile_sha256": profile_sha256,
        "size": len(members),
    }
    calendar_record = {
        "data_source_id": str(manifest["data_source_id"]),
        "mic": "XNAS",
        "calendar_version": str(manifest["calendar_version"]),
        "session_fingerprint": str(manifest["session_fingerprint"]),
        "session_count": int(manifest["session_count"]),
        "available_at": _iso(_timestamp(manifest["available_at"], field="calendar.available_at")),
        "snapshot_sha256": calendar_snapshot_sha256,
    }
    model = {
        "name": MODEL_NAME,
        "version": MODEL_VERSION,
        "git_commit": git_commit,
        "weights": {name: _fmt(weight) for name, weight in _WEIGHTS.items()},
    }
    model_identity = {key: value for key, value in model.items() if key != "git_commit"}
    input_fingerprint_envelope = {
        "profile": profile_record,
        "calendar": calendar_record,
        "model": model_identity,
        "generator_version": GENERATOR_VERSION,
        "period_start": start.isoformat(),
        "period_end": end.isoformat(),
        "sessions": [
            {"market_session": day.isoformat(), "decision_at": _iso(close)}
            for day, close in sessions
        ],
        "candidate_count": candidate_count,
        "source": source,
        "interval": "1d",
        "price_basis": price_basis,
        "currency": currency,
        "historical_fitness": INPUT_FITNESS,
        "availability_policy": AVAILABILITY_POLICY,
        "price_input_fingerprint": price_input_fingerprint,
        "manifest_refs": source_refs,
    }
    input_fingerprint = hashlib.sha256(canonical_json(input_fingerprint_envelope)).hexdigest()
    reconstruction_run_id = str(uuid5(_NAMESPACE, input_fingerprint))
    base = Path(output_root).expanduser().resolve()
    directory = base / "backfills" / reconstruction_run_id
    pinned_profile_path = f"backfills/{reconstruction_run_id}/inputs/profile.yaml"
    pinned_calendar_path = f"backfills/{reconstruction_run_id}/inputs/calendar-snapshot.json"
    profile_record["snapshot_path"] = pinned_profile_path
    calendar_record["snapshot_path"] = pinned_calendar_path
    selected_refs = [
        {
            "path": f"backfills/{reconstruction_run_id}/inputs/{item['source_path']}",
            "source_path": item["source_path"],
            "sha256": item["sha256"],
            "parts": [
                {
                    "path": f"backfills/{reconstruction_run_id}/inputs/{part['source_path']}",
                    "source_path": part["source_path"],
                    "sha256": part["sha256"],
                }
                for part in item["parts"]
            ],
        }
        for item in source_refs
    ]
    manifest_ref_by_source_path = {item["source_path"]: item for item in selected_refs}
    daily: list[dict[str, Any]] = []
    for market_session, close_at in sessions:
        exact_bars: dict[str, dict[str, Any]] = {}
        for security_id, rows in by_security.items():
            matching = [
                row for row in rows
                if _timestamp(row["observed_at"], field="price.observed_at").astimezone(_NEW_YORK).date()
                == market_session
                and _timestamp(row["observed_at"], field="price.observed_at") <= close_at
            ]
            if matching:
                exact_bars[security_id] = matching[-1]
        if set(exact_bars) != set(members):
            missing = sorted(members[key]["ticker"] for key in set(members) - set(exact_bars))
            raise RetrospectiveBackfillError(
                f"{market_session.isoformat()} lacks exact close bars for: {', '.join(missing)}"
            )
        ranking_rows, eligible_count, rejected_count = _rank_session(
            members=members,
            records_by_security=by_security,
            session_date=market_session,
            close_at=close_at,
            exact_bars=exact_bars,
            candidate_count=candidate_count,
        )
        session_id = str(uuid5(UUID(reconstruction_run_id), market_session.isoformat()))
        daily.append(
            {
                "index_id": session_id,
                "market_session": market_session.isoformat(),
                "decision_at": _iso(close_at),
                "candidate_count": min(candidate_count, eligible_count),
                "eligible_count": eligible_count,
                "rejected_count": rejected_count,
                "price_input_fingerprint": _session_fingerprint(
                    session_date=market_session,
                    close_at=close_at,
                    records_by_security=by_security,
                    data_root=root,
                ),
                "rankings": ranking_rows,
                "price_bars": [
                    _price_bar(
                        row,
                        member=members[security_id],
                        data_root=root,
                        manifest_refs=manifest_ref_by_source_path,
                    )
                    for security_id, row in sorted(exact_bars.items())
                ],
            }
        )
    document = {
        "schema_version": "1.0.0",
        "reconstruction_run_id": reconstruction_run_id,
        "observation_mode": OBSERVATION_MODE,
        "universe_basis": UNIVERSE_BASIS,
        "reconstructed_at": _iso(captured_at),
        "input_fingerprint": input_fingerprint,
        "generator_version": GENERATOR_VERSION,
        "profile": profile_record,
        "model": model,
        "range": {"start_date": start.isoformat(), "end_date": end.isoformat()},
        "calendar": calendar_record,
        "price_inputs": {
            "source": source,
            "interval": "1d",
            "price_basis": price_basis,
            "currency": currency,
            "historical_fitness": INPUT_FITNESS,
            "availability_policy": AVAILABILITY_POLICY,
            "input_fingerprint": price_input_fingerprint,
            "selected_row_count": len(prices),
            "manifests": selected_refs,
        },
        "sessions": daily,
    }
    pinned_files = {
        "inputs/profile.yaml": profile_source.read_bytes(),
        "inputs/calendar-snapshot.json": Path(calendar_snapshot_path).read_bytes(),
    }
    run_prefix = f"backfills/{reconstruction_run_id}/"
    for ref in selected_refs:
        if not ref["path"].startswith(run_prefix):
            raise RetrospectiveBackfillError("archived manifest path escapes its run directory")
        pinned_files[ref["path"][len(run_prefix) :]] = (
            root / ref["source_path"]
        ).read_bytes()
        for part in ref["parts"]:
            if not part["path"].startswith(run_prefix):
                raise RetrospectiveBackfillError("archived price part path escapes its run directory")
            pinned_files[part["path"][len(run_prefix) :]] = (
                root / part["source_path"]
            ).read_bytes()
    try:
        manifest_path = publish_retrospective_artifact_tree(
            run_directory=directory,
            manifest=document,
            pinned_files=pinned_files,
        )
    except BackfillArtifactError as error:
        raise RetrospectiveBackfillError(str(error)) from error
    validate_retrospective_backfill(manifest_path)
    return manifest_path


__all__ = [
    "AVAILABILITY_POLICY",
    "GENERATOR_VERSION",
    "INPUT_FITNESS",
    "OBSERVATION_MODE",
    "UNIVERSE_BASIS",
    "RetrospectiveBackfillError",
    "publish_retrospective_backfill",
    "validate_retrospective_backfill",
]
