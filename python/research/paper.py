"""Deterministic forward paper accounts and their append-only ledger."""

from __future__ import annotations

import hashlib
import json
import os
import re
from collections import defaultdict
from collections.abc import Mapping, Sequence
from dataclasses import replace
from datetime import UTC, date, datetime
from decimal import Decimal
from pathlib import Path
from typing import Any
from uuid import UUID, uuid5

from .backtest_inputs import BacktestInputError
from .experiments import canonical_json
from .portfolio import (
    PAPER_SCHEMA_VERSION,
    PaperMissingDataError,
    PortfolioError,
    PortfolioState,
    build_target,
    cash_base,
    convert,
    decimal,
    decimal_string,
    input_fingerprint,
    load_paper_inputs,
    positions_value,
    price_row,
    sessions_for_account,
    target_orders,
    timestamp,
)
from .risk import assess_target

PAPER_ENGINE_VERSION = "python-paper-1.0.0"

_ZERO = Decimal(0)
_ONE = Decimal(1)
_BPS = Decimal(10_000)
_SHA256 = re.compile(r"^[0-9a-f]{64}$")
_SEMVER = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+$")
_DATE = re.compile(r"^[0-9]{4}-[0-9]{2}-[0-9]{2}$")
_UTC_TIMESTAMP = re.compile(r"^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,6})?Z$")
_UUID = re.compile(
    r"^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"
)

_EVENT_NAMESPACE = UUID("45c1dfd4-9a04-5fd9-b4e9-8c2250bfc2a4")
_DECISION_NAMESPACE = UUID("2ec02277-a80f-5dd2-8b54-9639e9f2fe7b")
_REPORT_NAMESPACE = UUID("73c7f89f-a2f8-5a06-a68b-5b6fd75f6d5b")

_EVENT_TYPES = frozenset(
    {
        "cash_deposit",
        "decision",
        "target_published",
        "risk_approved",
        "risk_rejected",
        "halt",
        "no_op",
        "approval",
        "manual_rejection",
        "order",
        "fill",
        "fee",
        "tax",
        "dividend",
        "split",
        "delisting",
        "valuation",
        "reconciliation",
        "fx_conversion",
    }
)


class PaperError(ValueError):
    """Base error for paper account validation, persistence, and operation."""


class PaperSpecError(PaperError):
    """Raised when a paper account specification is unsafe or malformed."""


class PaperLedgerError(PaperError):
    """Raised when an append-only ledger is missing, tampered, or inconsistent."""


class PaperConflictError(PaperLedgerError):
    """Raised when an immutable identity is reused with different bytes."""


class PaperReconciliationError(PaperLedgerError):
    """Raised when a rebuild cannot prove the account ledger invariant."""


def _strict_json(path: Path) -> Any:
    def no_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for key, value in pairs:
            if key in result:
                raise ValueError(f"duplicate JSON key {key!r}")
            result[key] = value
        return result

    try:
        return json.loads(
            path.read_text(encoding="utf-8"),
            object_pairs_hook=no_duplicates,
            parse_constant=lambda value: (_ for _ in ()).throw(
                ValueError(f"invalid JSON constant {value}")
            ),
        )
    except (OSError, UnicodeError, ValueError) as error:
        raise PaperLedgerError(f"invalid JSON {path}: {error}") from error


def _uuid(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not _UUID.fullmatch(value):
        raise PaperSpecError(f"{field} must be a canonical UUID")
    try:
        parsed = UUID(value)
    except ValueError as error:
        raise PaperSpecError(f"{field} must be a canonical UUID") from error
    if str(parsed) != value:
        raise PaperSpecError(f"{field} must be a canonical UUID")
    return value


def _nonempty(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise PaperSpecError(f"{field} must be a non-empty string")
    return value.strip()


def _semver(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not _SEMVER.fullmatch(value):
        raise PaperSpecError(f"{field} must be semantic version x.y.z")
    return value


def _sha(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not _SHA256.fullmatch(value):
        raise PaperSpecError(f"{field} must be a lower-case SHA-256")
    return value


def _currency(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not re.fullmatch(r"^[A-Z]{3}$", value):
        raise PaperSpecError(f"{field} must be an uppercase ISO-4217 currency")
    return value


def _country(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not re.fullmatch(r"^[A-Z]{2}$", value):
        raise PaperSpecError(f"{field} must be an uppercase ISO-3166 country code")
    return value


def _slug(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not re.fullmatch(r"^[a-z][a-z0-9_-]{1,63}$", value):
        raise PaperSpecError(f"{field} must be a lower-case slug")
    return value


def _date(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not _DATE.fullmatch(value):
        raise PaperSpecError(f"{field} must be an ISO date")
    try:
        date.fromisoformat(value)
    except ValueError as error:
        raise PaperSpecError(f"{field} must be an ISO date") from error
    return value


def _utc(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not _UTC_TIMESTAMP.fullmatch(value):
        raise PaperSpecError(f"{field} must be a canonical UTC timestamp")
    try:
        parsed = datetime.fromisoformat(value)
    except ValueError as error:
        raise PaperSpecError(f"{field} must be a canonical UTC timestamp") from error
    if parsed.tzinfo is None or parsed.astimezone(UTC) != parsed:
        raise PaperSpecError(f"{field} must be a canonical UTC timestamp")
    return value


def _relative_path(value: Any, *, field: str) -> str:
    path = _nonempty(value, field=field)
    if path.startswith(("/", "\\")) or "\x00" in path:
        raise PaperSpecError(f"{field} must be a safe relative path")
    if any(part == ".." for part in path.replace("\\", "/").split("/")):
        raise PaperSpecError(f"{field} must not contain parent traversal")
    return path


def _exact(value: Any, fields: set[str], *, field: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping) or set(value) != fields:
        raise PaperSpecError(f"{field} has an invalid field set")
    return value


def _strategy(value: Any) -> dict[str, Any]:
    raw = _exact(value, {"name", "version", "git_commit", "parameters"}, field="strategy")
    name = raw["name"]
    if name not in {"buy_and_hold", "equal_weight", "momentum_12_1"}:
        raise PaperSpecError("strategy.name is unsupported")
    if raw["git_commit"] != "unknown" and (
        not isinstance(raw["git_commit"], str)
        or not re.fullmatch(r"^[0-9a-f]{40}$", raw["git_commit"])
    ):
        raise PaperSpecError("strategy.git_commit must be a lower-case Git SHA or unknown")
    parameters = raw["parameters"]
    if not isinstance(parameters, Mapping):
        raise PaperSpecError("strategy.parameters must be an object")
    parameters = dict(parameters)
    if name == "buy_and_hold" and parameters:
        raise PaperSpecError("buy_and_hold does not accept parameters")
    if name == "equal_weight" and (
        set(parameters) != {"rebalance_frequency"}
        or parameters["rebalance_frequency"] not in {"daily", "weekly", "monthly"}
    ):
        raise PaperSpecError("equal_weight requires a daily, weekly, or monthly rebalance_frequency")
    if name == "momentum_12_1":
        expected = {"lookback_sessions", "skip_sessions", "top_k", "rebalance_frequency"}
        if set(parameters) != expected or parameters["rebalance_frequency"] not in {
            "daily",
            "weekly",
            "monthly",
        }:
            raise PaperSpecError("momentum_12_1 has invalid parameters")
        for key in ("lookback_sessions", "skip_sessions", "top_k"):
            if not isinstance(parameters[key], int) or isinstance(parameters[key], bool) or parameters[key] < 1:
                raise PaperSpecError(f"strategy.parameters.{key} must be a positive integer")
        if parameters["skip_sessions"] >= parameters["lookback_sessions"]:
            raise PaperSpecError("momentum_12_1 skip_sessions must be less than lookback_sessions")
    return {
        "name": name,
        "version": _semver(raw["version"], field="strategy.version"),
        "git_commit": raw["git_commit"],
        "parameters": parameters,
    }


def validate_paper_account(value: Mapping[str, Any]) -> dict[str, Any]:
    """Validate and canonically normalize a v0.6 paper account specification."""

    allowed = {
        "schema_version",
        "account_id",
        "name",
        "strategy",
        "period",
        "universe",
        "security_metadata",
        "inputs",
        "benchmark",
        "decision_policy",
        "accounting_policy",
        "cost_policy",
        "risk_policy",
        "approval_policy",
        "missing_data_policy",
        "promoted_backtest",
    }
    if not isinstance(value, Mapping) or not set(value).issubset(allowed):
        raise PaperSpecError("paper account contains unknown fields")
    required = allowed - {"promoted_backtest"}
    if not required.issubset(value):
        raise PaperSpecError(
            f"paper account is missing fields: {', '.join(sorted(required - set(value)))}"
        )
    if value["schema_version"] != PAPER_SCHEMA_VERSION:
        raise PaperSpecError("paper account schema_version is unsupported")
    account_id = _uuid(value["account_id"], field="account_id")
    name = _nonempty(value["name"], field="name")
    strategy = _strategy(value["strategy"])
    period = _exact(value["period"], {"start_date", "end_date"}, field="period")
    period = {
        "start_date": _date(period["start_date"], field="period.start_date"),
        "end_date": _date(period["end_date"], field="period.end_date"),
    }
    if period["start_date"] > period["end_date"]:
        raise PaperSpecError("period.start_date must not be after period.end_date")

    universe = _exact(
        value["universe"],
        {"universe_id", "version", "security_ids", "membership_fingerprint"},
        field="universe",
    )
    security_ids = universe["security_ids"]
    if not isinstance(security_ids, list) or not security_ids:
        raise PaperSpecError("universe.security_ids must be a non-empty list")
    security_ids = sorted(
        _uuid(item, field=f"universe.security_ids[{index}]")
        for index, item in enumerate(security_ids)
    )
    if len(set(security_ids)) != len(security_ids):
        raise PaperSpecError("universe.security_ids must be unique")
    universe = {
        "universe_id": _uuid(universe["universe_id"], field="universe.universe_id"),
        "version": _semver(universe["version"], field="universe.version"),
        "security_ids": security_ids,
        "membership_fingerprint": _sha(
            universe["membership_fingerprint"], field="universe.membership_fingerprint"
        ),
    }

    metadata = value["security_metadata"]
    if not isinstance(metadata, list) or not metadata:
        raise PaperSpecError("security_metadata must be a non-empty list")
    normalized_metadata: list[dict[str, Any]] = []
    for index, raw in enumerate(metadata):
        row = _exact(raw, {"security_id", "country", "sector", "themes"}, field=f"security_metadata[{index}]")
        themes = row["themes"]
        if not isinstance(themes, list) or any(not isinstance(theme, str) for theme in themes):
            raise PaperSpecError(f"security_metadata[{index}].themes must be a string array")
        normalized_metadata.append(
            {
                "security_id": _uuid(row["security_id"], field=f"security_metadata[{index}].security_id"),
                "country": _country(row["country"], field=f"security_metadata[{index}].country"),
                "sector": _slug(row["sector"], field=f"security_metadata[{index}].sector"),
                "themes": sorted(
                    {_slug(theme, field=f"security_metadata[{index}].themes") for theme in themes}
                ),
            }
        )
    normalized_metadata.sort(key=lambda row: row["security_id"])
    metadata_ids = [row["security_id"] for row in normalized_metadata]
    if len(set(metadata_ids)) != len(metadata_ids) or set(metadata_ids) != set(security_ids):
        raise PaperSpecError("security_metadata must contain exactly the universe security IDs")

    inputs = value["inputs"]
    if not isinstance(inputs, list) or len(inputs) < 3:
        raise PaperSpecError("inputs must contain at least prices, calendar, and membership")
    normalized_inputs: list[dict[str, Any]] = []
    for index, raw in enumerate(inputs):
        row = _exact(
            raw,
            {"kind", "artifact_id", "path", "sha256", "available_at", "fitness"},
            field=f"inputs[{index}]",
        )
        if row["kind"] not in {
            "prices",
            "calendar",
            "membership",
            "corporate_actions",
            "fx",
            "feature",
            "macro",
            "risk_free",
        }:
            raise PaperSpecError(f"inputs[{index}].kind is unsupported")
        if row["fitness"] not in {"current_research_only", "backtest_safe"}:
            raise PaperSpecError(f"inputs[{index}].fitness is not admitted for paper")
        normalized_inputs.append(
            {
                "kind": row["kind"],
                "artifact_id": _uuid(row["artifact_id"], field=f"inputs[{index}].artifact_id"),
                "path": _relative_path(row["path"], field=f"inputs[{index}].path"),
                "sha256": _sha(row["sha256"], field=f"inputs[{index}].sha256"),
                "available_at": _utc(row["available_at"], field=f"inputs[{index}].available_at"),
                "fitness": row["fitness"],
            }
        )
    normalized_inputs.sort(key=lambda row: row["kind"])
    input_kinds = [row["kind"] for row in normalized_inputs]
    if len(set(input_kinds)) != len(input_kinds):
        raise PaperSpecError("inputs must contain one artifact per kind")
    for required_kind in ("prices", "calendar", "membership"):
        if required_kind not in input_kinds:
            raise PaperSpecError(f"inputs must include {required_kind}")

    benchmark = _exact(value["benchmark"], {"security_id", "currency"}, field="benchmark")
    benchmark = {
        "security_id": _uuid(benchmark["security_id"], field="benchmark.security_id"),
        "currency": _currency(benchmark["currency"], field="benchmark.currency"),
    }
    if benchmark["security_id"] not in security_ids:
        raise PaperSpecError("benchmark.security_id must belong to the account universe")

    decision = _exact(
        value["decision_policy"],
        {"frequency", "decision_at", "signal_delay_sessions", "execution_price", "stale_after_sessions", "max_price_gap"},
        field="decision_policy",
    )
    if decision["frequency"] != "daily" or decision["decision_at"] != "close" or decision["signal_delay_sessions"] != 1 or decision["execution_price"] != "open":
        raise PaperSpecError("only daily close to next-session open decisions are supported")
    if not isinstance(decision["signal_delay_sessions"], int) or isinstance(decision["signal_delay_sessions"], bool):
        raise PaperSpecError("decision_policy.signal_delay_sessions must be one")
    if not isinstance(decision["stale_after_sessions"], int) or isinstance(decision["stale_after_sessions"], bool) or decision["stale_after_sessions"] < 0:
        raise PaperSpecError("decision_policy.stale_after_sessions must be non-negative")
    decision = {
        "frequency": "daily",
        "decision_at": "close",
        "signal_delay_sessions": 1,
        "execution_price": "open",
        "stale_after_sessions": decision["stale_after_sessions"],
        "max_price_gap": decimal_string(
            decimal(decision["max_price_gap"], field="decision_policy.max_price_gap", non_negative=True)
        ),
    }

    accounting = _exact(
        value["accounting_policy"],
        {"base_currency", "initial_cash", "fractional_shares", "rebalance_frequency"},
        field="accounting_policy",
    )
    if accounting["rebalance_frequency"] not in {"once", "daily", "weekly", "monthly"}:
        raise PaperSpecError("accounting_policy.rebalance_frequency is unsupported")
    if not isinstance(accounting["fractional_shares"], bool):
        raise PaperSpecError("accounting_policy.fractional_shares must be boolean")
    accounting = {
        "base_currency": _currency(accounting["base_currency"], field="accounting_policy.base_currency"),
        "initial_cash": decimal_string(
            decimal(accounting["initial_cash"], field="accounting_policy.initial_cash", positive=True)
        ),
        "fractional_shares": accounting["fractional_shares"],
        "rebalance_frequency": accounting["rebalance_frequency"],
    }

    cost = _exact(
        value["cost_policy"],
        {"version", "commission_bps", "fixed_fee", "minimum_fee", "spread_bps", "slippage_bps", "tax_bps"},
        field="cost_policy",
    )
    cost = {"version": _semver(cost["version"], field="cost_policy.version")}
    for field in ("commission_bps", "fixed_fee", "minimum_fee", "spread_bps", "slippage_bps", "tax_bps"):
        cost[field] = decimal_string(
            decimal(value["cost_policy"][field], field=f"cost_policy.{field}", non_negative=True)
        )

    risk = _exact(
        value["risk_policy"],
        {
            "version",
            "max_gross_exposure",
            "max_position_weight",
            "max_sector_exposure",
            "max_country_exposure",
            "max_currency_exposure",
            "max_theme_exposure",
            "minimum_cash_reserve",
            "max_turnover",
            "max_daily_notional",
            "max_price_gap",
            "max_stale_sessions",
            "max_participation",
            "max_drawdown",
            "prohibited_security_ids",
        },
        field="risk_policy",
    )
    normalized_risk: dict[str, Any] = {"version": _semver(risk["version"], field="risk_policy.version")}
    for field in (
        "max_gross_exposure",
        "max_position_weight",
        "max_sector_exposure",
        "max_country_exposure",
        "max_currency_exposure",
        "max_theme_exposure",
        "minimum_cash_reserve",
        "max_turnover",
        "max_daily_notional",
        "max_price_gap",
        "max_participation",
        "max_drawdown",
    ):
        parsed = decimal(risk[field], field=f"risk_policy.{field}", non_negative=True)
        if field in {
            "max_gross_exposure",
            "max_position_weight",
            "max_sector_exposure",
            "max_country_exposure",
            "max_currency_exposure",
            "max_theme_exposure",
            "minimum_cash_reserve",
            "max_participation",
            "max_drawdown",
        } and parsed > 1:
            raise PaperSpecError(f"risk_policy.{field} must not exceed one")
        normalized_risk[field] = decimal_string(parsed)
    if not isinstance(risk["max_stale_sessions"], int) or isinstance(risk["max_stale_sessions"], bool) or risk["max_stale_sessions"] < 0:
        raise PaperSpecError("risk_policy.max_stale_sessions must be non-negative")
    normalized_risk["max_stale_sessions"] = risk["max_stale_sessions"]
    prohibited = risk["prohibited_security_ids"]
    if not isinstance(prohibited, list):
        raise PaperSpecError("risk_policy.prohibited_security_ids must be an array")
    normalized_risk["prohibited_security_ids"] = sorted(
        {_uuid(item, field="risk_policy.prohibited_security_ids") for item in prohibited}
    )

    approval = _exact(value["approval_policy"], {"mode"}, field="approval_policy")
    if approval["mode"] not in {"manual", "auto"}:
        raise PaperSpecError("approval_policy.mode is unsupported")
    missing_data_policy = value["missing_data_policy"]
    if missing_data_policy not in {"halt_account", "halt_decision", "reject_trade"}:
        raise PaperSpecError("missing_data_policy is unsupported")

    result: dict[str, Any] = {
        "schema_version": PAPER_SCHEMA_VERSION,
        "account_id": account_id,
        "name": name,
        "strategy": strategy,
        "period": period,
        "universe": universe,
        "security_metadata": normalized_metadata,
        "inputs": normalized_inputs,
        "benchmark": benchmark,
        "decision_policy": decision,
        "accounting_policy": accounting,
        "cost_policy": cost,
        "risk_policy": normalized_risk,
        "approval_policy": {"mode": approval["mode"]},
        "missing_data_policy": missing_data_policy,
    }
    if "promoted_backtest" in value:
        promoted = value["promoted_backtest"]
        if promoted is not None:
            promoted = _exact(
                promoted,
                {"experiment_id", "manifest_path", "manifest_sha256"},
                field="promoted_backtest",
            )
            promoted = {
                "experiment_id": _uuid(promoted["experiment_id"], field="promoted_backtest.experiment_id"),
                "manifest_path": _relative_path(promoted["manifest_path"], field="promoted_backtest.manifest_path"),
                "manifest_sha256": _sha(promoted["manifest_sha256"], field="promoted_backtest.manifest_sha256"),
            }
        result["promoted_backtest"] = promoted
    return result


def paper_account_sha256(account: Mapping[str, Any]) -> str:
    return hashlib.sha256(canonical_json(validate_paper_account(account))).hexdigest()


def _write_immutable(path: Path, document: Mapping[str, Any]) -> None:
    content = canonical_json(document) + b"\n"
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.exists():
        try:
            existing = path.read_bytes()
        except OSError as error:
            raise PaperLedgerError(f"cannot read immutable file {path}: {error}") from error
        if existing != content:
            raise PaperConflictError(f"immutable file already contains different bytes: {path}")
        return
    temporary = path.with_name(f".{path.name}.{os.getpid()}.tmp")
    try:
        temporary.write_bytes(content)
        temporary.replace(path)
    except OSError as error:
        raise PaperLedgerError(f"cannot publish immutable file {path}: {error}") from error
    finally:
        if temporary.exists():
            temporary.unlink()


def _write_atomic(path: Path, document: Mapping[str, Any]) -> None:
    content = canonical_json(document) + b"\n"
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f".{path.name}.{os.getpid()}.tmp")
    try:
        temporary.write_bytes(content)
        temporary.replace(path)
    except OSError as error:
        raise PaperLedgerError(f"cannot publish file {path}: {error}") from error
    finally:
        if temporary.exists():
            temporary.unlink()


def _event_payload(event: Mapping[str, Any]) -> dict[str, Any]:
    return {key: value for key, value in event.items() if key != "record_hash"}


def _event_hash(event: Mapping[str, Any]) -> str:
    return hashlib.sha256(canonical_json(_event_payload(event))).hexdigest()


def _event_id(account_id: str, idempotency_key: str) -> str:
    return str(uuid5(_EVENT_NAMESPACE, f"{account_id}:{idempotency_key}"))


class LedgerStore:
    """Filesystem-backed immutable account specification and event stream."""

    def __init__(self, ledger_root: str | Path, account_id: str):
        self.root = Path(ledger_root).expanduser().resolve()
        self.account_id = _uuid(account_id, field="account_id")
        self.account_dir = self.root / "accounts" / self.account_id
        self.events_dir = self.account_dir / "events"
        self.targets_dir = self.account_dir / "targets"
        self.orders_dir = self.account_dir / "orders"
        self.reports_dir = self.account_dir / "reports"
        self.account_path = self.account_dir / "account.json"
        self.manifest_path = self.account_dir / "ledger-manifest.json"

    def initialize(self, account: Mapping[str, Any]) -> dict[str, Any]:
        normalized = validate_paper_account(account)
        if normalized["account_id"] != self.account_id:
            raise PaperSpecError("account_id does not match ledger path")
        if self.account_path.exists():
            existing = validate_paper_account(_strict_json(self.account_path))
            if existing != normalized:
                raise PaperConflictError("paper account already exists with different specification")
            return existing
        if self.account_dir.exists() and any(self.account_dir.iterdir()):
            raise PaperLedgerError(f"ledger directory is not an unused account directory: {self.account_dir}")
        self.events_dir.mkdir(parents=True, exist_ok=True)
        self.targets_dir.mkdir(parents=True, exist_ok=True)
        self.orders_dir.mkdir(parents=True, exist_ok=True)
        self.reports_dir.mkdir(parents=True, exist_ok=True)
        _write_immutable(self.account_path, normalized)
        _write_atomic(
            self.manifest_path,
            {"schema_version": PAPER_SCHEMA_VERSION, "account_id": self.account_id, "event_count": 0, "last_sequence": 0, "events": []},
        )
        self.append_event(
            idempotency_key=f"cash-deposit:{self.account_id}",
            session_date=date.fromisoformat(normalized["period"]["start_date"]),
            event_at=datetime.fromisoformat(normalized["period"]["start_date"] + "T00:00:00+00:00"),
            event_type="cash_deposit",
            currency=normalized["accounting_policy"]["base_currency"],
            amount_local_delta=decimal(normalized["accounting_policy"]["initial_cash"], field="initial_cash"),
            amount_base_delta=decimal(normalized["accounting_policy"]["initial_cash"], field="initial_cash"),
            details={"reason": "initial_cash", "account_sha256": paper_account_sha256(normalized)},
            nav_base=normalized["accounting_policy"]["initial_cash"],
            cash_base=normalized["accounting_policy"]["initial_cash"],
            positions_value_base="0",
        )
        return normalized

    def load_account(self) -> dict[str, Any]:
        if not self.account_path.is_file():
            raise PaperLedgerError(f"paper account does not exist: {self.account_path}")
        account = validate_paper_account(_strict_json(self.account_path))
        if account["account_id"] != self.account_id:
            raise PaperLedgerError("paper account file identity does not match its path")
        return account

    def _manifest(self) -> dict[str, Any]:
        if not self.manifest_path.is_file():
            raise PaperLedgerError(f"ledger manifest does not exist: {self.manifest_path}")
        manifest = _strict_json(self.manifest_path)
        if not isinstance(manifest, Mapping) or set(manifest) != {
            "schema_version",
            "account_id",
            "event_count",
            "last_sequence",
            "events",
        }:
            raise PaperLedgerError("ledger manifest has an invalid field set")
        if manifest["schema_version"] != PAPER_SCHEMA_VERSION or manifest["account_id"] != self.account_id:
            raise PaperLedgerError("ledger manifest identity is invalid")
        if not isinstance(manifest["events"], list):
            raise PaperLedgerError("ledger manifest events must be an array")
        if manifest["event_count"] != len(manifest["events"]) or manifest["last_sequence"] != len(manifest["events"]):
            raise PaperLedgerError("ledger manifest counts are inconsistent")
        return dict(manifest)

    def _validate_event(self, event: Mapping[str, Any], expected_sequence: int) -> dict[str, Any]:
        required = {
            "schema_version",
            "event_id",
            "idempotency_key",
            "account_id",
            "sequence",
            "event_at",
            "session_date",
            "event_type",
            "decision_id",
            "target_id",
            "order_id",
            "security_id",
            "currency",
            "quantity_delta",
            "amount_local_delta",
            "amount_base_delta",
            "nav_base",
            "cash_base",
            "positions_value_base",
            "details",
            "record_hash",
        }
        if set(event) != required:
            raise PaperLedgerError("paper event has an invalid field set")
        if event["schema_version"] != PAPER_SCHEMA_VERSION or event["account_id"] != self.account_id:
            raise PaperLedgerError("paper event identity is invalid")
        if event["sequence"] != expected_sequence:
            raise PaperLedgerError("paper event sequence is not contiguous")
        _uuid(event["event_id"], field="event_id")
        if not isinstance(event["idempotency_key"], str) or not event["idempotency_key"]:
            raise PaperLedgerError("paper event idempotency_key is invalid")
        if _event_id(self.account_id, event["idempotency_key"]) != event["event_id"]:
            raise PaperLedgerError("paper event id does not match its idempotency key")
        if not isinstance(event["event_type"], str) or event["event_type"] not in _EVENT_TYPES:
            raise PaperLedgerError("paper event type is unsupported")
        if not isinstance(event["currency"], str) or not re.fullmatch(r"^[A-Z]{3}$", event["currency"]):
            raise PaperLedgerError("paper event currency is invalid")
        try:
            datetime.fromisoformat(event["event_at"])
            date.fromisoformat(event["session_date"])
        except (TypeError, ValueError) as error:
            raise PaperLedgerError("paper event date or timestamp is invalid") from error
        if not isinstance(event["details"], Mapping):
            raise PaperLedgerError("paper event details must be an object")
        if _event_hash(event) != event["record_hash"] or not _SHA256.fullmatch(event["record_hash"]):
            raise PaperLedgerError("paper event record_hash does not match its payload")
        for field in ("quantity_delta", "amount_local_delta", "amount_base_delta"):
            if not isinstance(event[field], str) or not re.fullmatch(r"^-?(0|[1-9][0-9]*)(\.[0-9]+)?$", event[field]):
                raise PaperLedgerError(f"paper event {field} is not a canonical decimal")
        for field in ("nav_base", "cash_base", "positions_value_base"):
            if event[field] is not None and (
                not isinstance(event[field], str)
                or not re.fullmatch(r"^(0|[1-9][0-9]*)(\.[0-9]+)?$", event[field])
            ):
                raise PaperLedgerError(f"paper event {field} is not a canonical non-negative decimal")
        return dict(event)

    def events(self) -> tuple[dict[str, Any], ...]:
        manifest = self._manifest()
        files = sorted(self.events_dir.glob("event-*.json")) if self.events_dir.is_dir() else []
        if len(files) != manifest["event_count"]:
            raise PaperLedgerError("ledger event files do not match manifest count")
        result: list[dict[str, Any]] = []
        for sequence, path in enumerate(files, start=1):
            if path.name != f"event-{sequence:06d}.json":
                raise PaperLedgerError(f"ledger event filename is invalid: {path.name}")
            result.append(self._validate_event(_strict_json(path), sequence))
        for expected, event in zip(manifest["events"], result, strict=True):
            if expected != {
                "sequence": event["sequence"],
                "event_id": event["event_id"],
                "record_hash": event["record_hash"],
            }:
                raise PaperLedgerError("ledger manifest does not match event files")
        return tuple(result)

    def event_for_idempotency(self, idempotency_key: str) -> dict[str, Any] | None:
        return next((event for event in self.events() if event["idempotency_key"] == idempotency_key), None)

    def append_event(
        self,
        *,
        idempotency_key: str,
        session_date: date,
        event_at: datetime,
        event_type: str,
        currency: str,
        quantity_delta: Decimal = _ZERO,
        amount_local_delta: Decimal = _ZERO,
        amount_base_delta: Decimal = _ZERO,
        details: Mapping[str, Any] | None = None,
        decision_id: str | None = None,
        target_id: str | None = None,
        order_id: str | None = None,
        security_id: str | None = None,
        nav_base: Decimal | str | None = None,
        cash_base: Decimal | str | None = None,
        positions_value_base: Decimal | str | None = None,
    ) -> dict[str, Any]:
        if not isinstance(idempotency_key, str) or not idempotency_key:
            raise PaperLedgerError("idempotency_key must be non-empty")
        if event_type not in _EVENT_TYPES:
            raise PaperLedgerError(f"unsupported paper event type {event_type}")
        if details is None:
            details = {}
        if not isinstance(details, Mapping):
            raise PaperLedgerError("event details must be an object")
        existing = self.event_for_idempotency(idempotency_key)
        event_id = _event_id(self.account_id, idempotency_key)
        sequence = existing["sequence"] if existing is not None else len(self.events()) + 1
        event = {
            "schema_version": PAPER_SCHEMA_VERSION,
            "event_id": event_id,
            "idempotency_key": idempotency_key,
            "account_id": self.account_id,
            "sequence": sequence,
            "event_at": timestamp(event_at),
            "session_date": session_date.isoformat(),
            "event_type": event_type,
            "decision_id": decision_id,
            "target_id": target_id,
            "order_id": order_id,
            "security_id": security_id,
            "currency": currency,
            "quantity_delta": decimal_string(quantity_delta),
            "amount_local_delta": decimal_string(amount_local_delta),
            "amount_base_delta": decimal_string(amount_base_delta),
            "nav_base": decimal_string(nav_base) if nav_base is not None else None,
            "cash_base": decimal_string(cash_base) if cash_base is not None else None,
            "positions_value_base": decimal_string(positions_value_base) if positions_value_base is not None else None,
            "details": dict(details),
        }
        event["record_hash"] = _event_hash(event)
        if existing is not None:
            if _event_payload(existing) != _event_payload(event) or existing["record_hash"] != event["record_hash"]:
                raise PaperConflictError(f"idempotency key already contains different event: {idempotency_key}")
            return existing
        path = self.events_dir / f"event-{sequence:06d}.json"
        _write_immutable(path, event)
        old_manifest = self._manifest()
        manifest = {
            "schema_version": PAPER_SCHEMA_VERSION,
            "account_id": self.account_id,
            "event_count": sequence,
            "last_sequence": sequence,
            "events": [
                *old_manifest["events"],
                {"sequence": sequence, "event_id": event_id, "record_hash": event["record_hash"]},
            ],
        }
        _write_atomic(self.manifest_path, manifest)
        return event

    def rebuild_state(self) -> PortfolioState:
        events = self.events()
        cash: dict[str, Decimal] = defaultdict(Decimal)
        quantities: dict[str, Decimal] = defaultdict(Decimal)
        security_currency: dict[str, str] = {}
        nav = _ZERO
        cash_value = _ZERO
        positions_value = _ZERO
        peak = _ZERO
        last_session: date | None = None
        last_decision: date | None = None
        for event in events:
            currency = event["currency"]
            cash[currency] += Decimal(event["amount_local_delta"])
            security_id = event["security_id"]
            if security_id is not None:
                quantities[security_id] += Decimal(event["quantity_delta"])
                security_currency[security_id] = currency
                if quantities[security_id] < 0:
                    raise PaperReconciliationError(f"negative holding after event {event['event_id']}")
            if event["event_type"] == "decision":
                last_decision = date.fromisoformat(event["session_date"])
            if event["nav_base"] is not None:
                nav = Decimal(event["nav_base"])
                cash_value = Decimal(event["cash_base"] or "0")
                positions_value = Decimal(event["positions_value_base"] or "0")
                if nav < 0 or cash_value < 0 or positions_value < 0:
                    raise PaperReconciliationError(f"negative valuation in event {event['event_id']}")
                peak = max(peak, nav)
            current_session = date.fromisoformat(event["session_date"])
            last_session = max(last_session, current_session) if last_session else current_session
        if not events:
            raise PaperLedgerError("paper ledger has no events")
        return PortfolioState(
            cash_by_currency=dict(cash),
            quantities={key: value for key, value in quantities.items() if value != 0},
            security_currency=security_currency,
            nav_base=nav,
            cash_base=cash_value,
            positions_value_base=positions_value,
            peak_nav_base=peak,
            last_session=last_session,
            last_decision_session=last_decision,
        )

    def write_target(self, target: Mapping[str, Any]) -> tuple[Path, str]:
        path = self.targets_dir / f"target-{target['target_id']}.json"
        _write_immutable(path, target)
        return path, hashlib.sha256(path.read_bytes()).hexdigest()

    def write_order(self, order: Mapping[str, Any]) -> tuple[Path, str]:
        path = self.orders_dir / f"order-{order['order_id']}.json"
        _write_immutable(path, order)
        return path, hashlib.sha256(path.read_bytes()).hexdigest()

    def write_report(self, report: Mapping[str, Any]) -> Path:
        path = self.reports_dir / f"report-{report['session_date']}.json"
        _write_immutable(path, report)
        return path

    def read_report(self, session_date: date | str) -> dict[str, Any] | None:
        value = session_date.isoformat() if isinstance(session_date, date) else session_date
        path = self.reports_dir / f"report-{value}.json"
        return dict(_strict_json(path)) if path.is_file() else None


def create_paper_account(account: Mapping[str, Any], *, ledger_root: str | Path) -> dict[str, Any]:
    normalized = validate_paper_account(account)
    store = LedgerStore(ledger_root, normalized["account_id"])
    store.initialize(normalized)
    return {
        "account_id": normalized["account_id"],
        "account_sha256": paper_account_sha256(normalized),
        "ledger_path": str(store.account_dir),
        "event_count": len(store.events()),
    }


def _state_after_mark(
    account: Mapping[str, Any],
    inputs: Any,
    state: PortfolioState,
    session: Mapping[str, Any],
) -> PortfolioState:
    position_value, currencies = positions_value(account, inputs, state, session)
    available_cash = cash_base(account, inputs, state, session["close_at"])
    nav = available_cash + position_value
    if nav <= 0:
        raise PaperReconciliationError(f"paper NAV is not positive on {session['session_date']}")
    return replace(
        state,
        security_currency=currencies,
        nav_base=nav,
        cash_base=available_cash,
        positions_value_base=position_value,
        peak_nav_base=max(state.peak_nav_base, nav),
        last_session=session["session_date"],
    )


def _append_valuation(
    store: LedgerStore,
    account: Mapping[str, Any],
    inputs: Any,
    state: PortfolioState,
    session: Mapping[str, Any],
    decision_id: str,
) -> PortfolioState:
    marked = _state_after_mark(account, inputs, state, session)
    holdings = []
    for security_id, quantity in sorted(marked.quantities.items()):
        if quantity <= 0:
            continue
        row = price_row(inputs, security_id, session["session_date"], session["close_at"])
        if row is None:
            raise PaperMissingDataError(f"missing close for held security {security_id}")
        value = convert(
            inputs,
            quantity * row["close"],
            row["currency"],
            account["accounting_policy"]["base_currency"],
            session["close_at"],
        )
        holdings.append(
            {
                "security_id": security_id,
                "quantity": decimal_string(quantity),
                "price": decimal_string(row["close"]),
                "currency": row["currency"],
                "market_value_base": decimal_string(value),
            }
        )
    existing = store.event_for_idempotency(f"valuation:{session['session_date']}")
    if existing is not None:
        return store.rebuild_state()
    store.append_event(
        idempotency_key=f"valuation:{session['session_date']}",
        session_date=session["session_date"],
        event_at=session["close_at"],
        event_type="valuation",
        currency=account["accounting_policy"]["base_currency"],
        details={"decision_id": decision_id, "cash_by_currency": {key: decimal_string(value) for key, value in sorted(marked.cash_by_currency.items())}, "holdings": holdings},
        decision_id=decision_id,
        nav_base=marked.nav_base,
        cash_base=marked.cash_base,
        positions_value_base=marked.positions_value_base,
    )
    return store.rebuild_state()


def _action_events(inputs: Any, session_date: date) -> list[dict[str, Any]]:
    result = []
    for action in inputs.corporate_actions:
        if (
            action["action_type"] == "cash_dividend" and action["payment_date"] == session_date
        ) or (
            action["action_type"] != "cash_dividend" and action["effective_date"] == session_date
        ):
            result.append(action)
    return sorted(result, key=lambda action: action["id"])


def _apply_actions(
    store: LedgerStore,
    account: Mapping[str, Any],
    inputs: Any,
    session: Mapping[str, Any],
) -> None:
    state = store.rebuild_state()
    for action in _action_events(inputs, session["session_date"]):
        if store.event_for_idempotency(f"action:{action['id']}") is not None:
            continue
        quantity = state.quantities.get(action["security_id"], _ZERO)
        if action["available_at"] > session["open_at"] and quantity > 0:
            raise PaperMissingDataError(
                f"corporate action {action['id']} is not available by its economic event"
            )
        amount = _ZERO
        quantity_delta = _ZERO
        price: Decimal | None = None
        if quantity > 0 and action["action_type"] == "split":
            factor = action["ratio_numerator"] / action["ratio_denominator"]
            quantity_delta = quantity * (factor - _ONE)
            price = factor
        elif quantity > 0 and action["action_type"] == "cash_dividend":
            amount = quantity * action["cash_amount"]
            price = action["cash_amount"]
        elif quantity > 0 and action["action_type"] == "delisting":
            if action["settlement_price"] is None:
                raise PaperMissingDataError(f"delisting {action['id']} has no settlement price")
            amount = quantity * action["settlement_price"]
            quantity_delta = -quantity
            price = action["settlement_price"]
        amount_base = convert(
            inputs,
            amount,
            action["currency"],
            account["accounting_policy"]["base_currency"],
            session["open_at"],
        )
        event_type = {
            "split": "split",
            "cash_dividend": "dividend",
            "delisting": "delisting",
        }[action["action_type"]]
        store.append_event(
            idempotency_key=f"action:{action['id']}",
            session_date=session["session_date"],
            event_at=session["open_at"],
            event_type=event_type,
            currency=action["currency"],
            quantity_delta=quantity_delta,
            amount_local_delta=amount,
            amount_base_delta=amount_base,
            details={
                "action_id": action["id"],
                "action_type": action["action_type"],
                "ignored_no_position": quantity <= 0,
                "price": decimal_string(price) if price is not None else None,
            },
            security_id=action["security_id"],
        )
        state = store.rebuild_state()


def _fill_terms(account: Mapping[str, Any], row: Mapping[str, Any], side: str, quantity: Decimal) -> tuple[Decimal, Decimal, Decimal, Decimal, Decimal]:
    policy = account["cost_policy"]
    gross = row["open"] * quantity
    commission = gross * Decimal(policy["commission_bps"]) / _BPS + Decimal(policy["fixed_fee"])
    commission = max(commission, Decimal(policy["minimum_fee"]))
    spread = gross * Decimal(policy["spread_bps"]) / (_BPS * 2)
    tax = gross * Decimal(policy["tax_bps"]) / _BPS
    direction = _ONE if side == "buy" else -_ONE
    fill_price = row["open"] * (_ONE + direction * (Decimal(policy["spread_bps"]) / (_BPS * 2) + Decimal(policy["slippage_bps"]) / _BPS))
    return fill_price, fill_price * quantity, commission, spread, tax


def _approved_by_decision(events: Sequence[Mapping[str, Any]]) -> dict[str, bool]:
    result: dict[str, bool] = {}
    for event in events:
        if event["event_type"] == "approval" and event["decision_id"]:
            result[event["decision_id"]] = bool(event["details"].get("approved"))
        elif event["event_type"] == "manual_rejection" and event["decision_id"]:
            result[event["decision_id"]] = False
    return result


def _settle_orders(
    store: LedgerStore,
    account: Mapping[str, Any],
    inputs: Any,
    session: Mapping[str, Any],
) -> None:
    events = store.events()
    approvals = _approved_by_decision(events)
    for event in events:
        if event["event_type"] != "order":
            continue
        order = event["details"].get("order")
        if not isinstance(order, Mapping) or order["execution_session"] != session["session_date"].isoformat():
            continue
        if order["decision_id"] not in approvals or not approvals[order["decision_id"]]:
            continue
        if store.event_for_idempotency(f"fill:{order['order_id']}") is not None:
            continue
        row = price_row(inputs, order["security_id"], session["session_date"], session["open_at"])
        if row is None or row["currency"] != order["currency"]:
            raise PaperMissingDataError(f"missing executable open for {order['security_id']}")
        quantity = Decimal(order["quantity"])
        state = store.rebuild_state()
        if order["side"] == "sell" and state.quantities.get(order["security_id"], _ZERO) < quantity:
            raise PaperReconciliationError(f"order {order['order_id']} exceeds the available holding")
        fill_price, trade_notional, commission, spread, tax = _fill_terms(account, row, order["side"], quantity)
        required_local = trade_notional + commission + tax
        if order["side"] == "buy":
            local_cash = state.cash_by_currency.get(order["currency"], _ZERO)
            if order["currency"] != account["accounting_policy"]["base_currency"]:
                local_cash += convert(
                    inputs,
                    state.cash_by_currency.get(account["accounting_policy"]["base_currency"], _ZERO),
                    account["accounting_policy"]["base_currency"],
                    order["currency"],
                    session["open_at"],
                )
            if local_cash < required_local:
                raise PaperReconciliationError(f"insufficient cash for approved order {order['order_id']}")
            if order["currency"] != account["accounting_policy"]["base_currency"] and state.cash_by_currency.get(order["currency"], _ZERO) < required_local:
                shortfall = required_local - state.cash_by_currency.get(order["currency"], _ZERO)
                base_amount = convert(
                    inputs,
                    shortfall,
                    order["currency"],
                    account["accounting_policy"]["base_currency"],
                    session["open_at"],
                )
                store.append_event(
                    idempotency_key=f"fx:{order['order_id']}",
                    session_date=session["session_date"],
                    event_at=session["open_at"],
                    event_type="fx_conversion",
                    currency=account["accounting_policy"]["base_currency"],
                    amount_local_delta=-base_amount,
                    amount_base_delta=-base_amount,
                    details={"order_id": order["order_id"], "from": account["accounting_policy"]["base_currency"], "to": order["currency"]},
                )
                store.append_event(
                    idempotency_key=f"fx-credit:{order['order_id']}",
                    session_date=session["session_date"],
                    event_at=session["open_at"],
                    event_type="fx_conversion",
                    currency=order["currency"],
                    amount_local_delta=shortfall,
                    amount_base_delta=base_amount,
                    details={"order_id": order["order_id"], "from": account["accounting_policy"]["base_currency"], "to": order["currency"]},
                )
        sign = _ONE if order["side"] == "sell" else -_ONE
        base_trade = convert(
            inputs,
            trade_notional,
            order["currency"],
            account["accounting_policy"]["base_currency"],
            session["open_at"],
        )
        store.append_event(
            idempotency_key=f"fill:{order['order_id']}",
            session_date=session["session_date"],
            event_at=session["open_at"],
            event_type="fill",
            currency=order["currency"],
            quantity_delta=quantity if order["side"] == "buy" else -quantity,
            amount_local_delta=sign * trade_notional,
            amount_base_delta=sign * base_trade,
            details={
                "order_id": order["order_id"],
                "client_order_id": order["client_order_id"],
                "side": order["side"],
                "quantity": decimal_string(quantity),
                "reference_price": order["reference_price"],
                "fill_price": decimal_string(fill_price),
                "gross_notional": decimal_string(row["open"] * quantity),
                "spread_cost": decimal_string(spread),
                "slippage_cost": decimal_string(abs(fill_price - row["open"]) * quantity - spread),
            },
            order_id=order["order_id"],
            security_id=order["security_id"],
        )
        store.append_event(
            idempotency_key=f"fee:{order['order_id']}",
            session_date=session["session_date"],
            event_at=session["open_at"],
            event_type="fee",
            currency=order["currency"],
            amount_local_delta=-commission,
            amount_base_delta=-convert(inputs, commission, order["currency"], account["accounting_policy"]["base_currency"], session["open_at"]),
            details={"order_id": order["order_id"], "commission": decimal_string(commission)},
            order_id=order["order_id"],
            security_id=order["security_id"],
        )
        store.append_event(
            idempotency_key=f"tax:{order['order_id']}",
            session_date=session["session_date"],
            event_at=session["open_at"],
            event_type="tax",
            currency=order["currency"],
            amount_local_delta=-tax,
            amount_base_delta=-convert(inputs, tax, order["currency"], account["accounting_policy"]["base_currency"], session["open_at"]),
            details={"order_id": order["order_id"], "tax": decimal_string(tax)},
            order_id=order["order_id"],
            security_id=order["security_id"],
        )


def _decision_id(account_id: str, session_date: date, fingerprint: str) -> str:
    return str(uuid5(_DECISION_NAMESPACE, f"{account_id}:{session_date.isoformat()}:{fingerprint}"))


def _decision_events(events: Sequence[Mapping[str, Any]], decision_id: str) -> tuple[Mapping[str, Any], ...]:
    return tuple(event for event in events if event["decision_id"] == decision_id)


def _append_halt(
    store: LedgerStore,
    account: Mapping[str, Any],
    session: Mapping[str, Any],
    decision_id: str,
    fingerprint: str,
    reason: str,
) -> None:
    if store.event_for_idempotency(f"decision:{decision_id}") is None:
        store.append_event(
            idempotency_key=f"decision:{decision_id}",
            session_date=session["session_date"],
            event_at=session["close_at"],
            event_type="decision",
            currency=account["accounting_policy"]["base_currency"],
            details={"status": "halted", "input_fingerprint": fingerprint, "reason": reason},
            decision_id=decision_id,
        )
    store.append_event(
        idempotency_key=f"halt:{decision_id}",
        session_date=session["session_date"],
        event_at=session["close_at"],
        event_type="halt",
        currency=account["accounting_policy"]["base_currency"],
        details={"status": "halted", "reason": reason},
        decision_id=decision_id,
    )


def _report_from_events(
    store: LedgerStore,
    account: Mapping[str, Any],
    session: Mapping[str, Any],
    decision_id: str,
    fingerprint: str,
    *,
    risk: Mapping[str, Any] | None = None,
    warnings: Sequence[str] = (),
) -> dict[str, Any]:
    events = store.events()
    decision_events = _decision_events(events, decision_id)
    decision = next((event for event in decision_events if event["event_type"] == "decision"), None)
    status = decision["details"].get("status", "halted") if decision else "halted"
    current_orders = [
        event["details"]["order"]
        for event in decision_events
        if event["event_type"] == "order" and isinstance(event["details"].get("order"), Mapping)
    ]
    approvals = _approved_by_decision(events)
    fills = {event["order_id"] for event in events if event["event_type"] == "fill" and event["order_id"]}
    rendered_orders: list[dict[str, Any]] = []
    for raw in current_orders:
        order = dict(raw)
        if order["order_id"] in fills:
            order["status"] = "filled"
        elif raw["decision_id"] in approvals:
            order["status"] = "approved" if approvals[raw["decision_id"]] else "rejected"
        rendered_orders.append(order)
    state = store.rebuild_state()
    derived_risk = next(
        (
            event["details"].get("risk")
            for event in decision_events
            if event["event_type"] == "target_published"
            and isinstance(event["details"].get("risk"), Mapping)
        ),
        None,
    )
    if risk is not None:
        report_risk = dict(risk)
    elif derived_risk is not None:
        report_risk = dict(derived_risk)
    elif status == "no_op":
        report_risk = {
            "status": "approved",
            "policy_version": account["risk_policy"]["version"],
            "codes": [],
            "reasons": [],
            "checked_at": timestamp(session["close_at"]),
        }
    else:
        report_risk = {
            "status": "halted",
            "policy_version": account["risk_policy"]["version"],
            "codes": ["halted"],
            "reasons": list(warnings) or ["decision halted"],
            "checked_at": timestamp(session["close_at"]),
        }
    if rendered_orders and status == "approved" and any(order["status"] == "proposed" for order in rendered_orders):
        status = "proposed"
    approval = "not_required"
    if rendered_orders:
        if approvals.get(decision_id) is True:
            approval = "auto_approved" if account["approval_policy"]["mode"] == "auto" else "approved"
        elif decision_id in approvals:
            approval = "rejected"
        else:
            approval = "pending"
    if status == "approved" and not rendered_orders:
        status = "no_op"
    session_sequences = [event["sequence"] for event in events if event["session_date"] == session["session_date"].isoformat()]
    if not session_sequences:
        session_sequences = [events[-1]["sequence"]]
    report_id = str(uuid5(_REPORT_NAMESPACE, f"{account['account_id']}:{session['session_date']}:{fingerprint}"))
    gross = state.positions_value_base / state.nav_base if state.nav_base else _ZERO
    drawdown = (
        max(_ZERO, (state.peak_nav_base - state.nav_base) / state.peak_nav_base)
        if state.peak_nav_base
        else _ZERO
    )
    return {
        "schema_version": PAPER_SCHEMA_VERSION,
        "report_id": report_id,
        "account_id": account["account_id"],
        "session_date": session["session_date"].isoformat(),
        "decision_id": decision_id,
        "input_fingerprint": fingerprint,
        "decision_status": status,
        "risk": report_risk,
        "approval": approval,
        "orders": rendered_orders,
        "nav_base": decimal_string(state.nav_base),
        "cash_base": decimal_string(state.cash_base),
        "positions_value_base": decimal_string(state.positions_value_base),
        "gross_exposure": decimal_string(gross),
        "drawdown": decimal_string(drawdown),
        "ledger_sequence_start": min(session_sequences),
        "ledger_sequence_end": max(session_sequences),
        "reconciled": reconcile_paper_account(store)["status"] == "passed",
        **({"warnings": list(warnings)} if warnings else {}),
    }


def run_paper_session(
    account: Mapping[str, Any],
    *,
    data_root: str | Path,
    ledger_root: str | Path,
    session_date: str | date,
) -> dict[str, Any]:
    """Run one close decision and next-open settlement cycle idempotently."""

    normalized = validate_paper_account(account)
    store = LedgerStore(ledger_root, normalized["account_id"])
    persisted = store.load_account()
    if persisted != normalized:
        raise PaperConflictError("supplied paper account differs from immutable ledger account")
    current_date = date.fromisoformat(session_date) if isinstance(session_date, str) else session_date
    inputs = load_paper_inputs(normalized["inputs"], data_root=data_root)
    sessions = sessions_for_account(normalized, inputs)
    try:
        session_index = next(index for index, row in enumerate(sessions) if row["session_date"] == current_date)
    except StopIteration as error:
        raise PaperSpecError(f"session {current_date} is outside the paper account calendar") from error
    existing_report = store.read_report(current_date)
    if existing_report is not None:
        return existing_report
    fingerprint = input_fingerprint(normalized["inputs"])
    decision_id = _decision_id(normalized["account_id"], current_date, fingerprint)
    session = sessions[session_index]
    warnings: list[str] = []
    try:
        _apply_actions(store, normalized, inputs, session)
        _settle_orders(store, normalized, inputs, session)
        state = store.rebuild_state()
        state = _append_valuation(store, normalized, inputs, state, session, decision_id)
        due = (
            state.last_decision_session is None
            or normalized["strategy"]["name"] == "buy_and_hold"
            or normalized["strategy"]["parameters"].get("rebalance_frequency", normalized["accounting_policy"]["rebalance_frequency"]) == "daily"
            or (normalized["strategy"]["parameters"].get("rebalance_frequency") == "weekly" and (not state.last_decision_session or current_date.isocalendar()[:2] != state.last_decision_session.isocalendar()[:2]))
            or (normalized["strategy"]["parameters"].get("rebalance_frequency") == "monthly" and (not state.last_decision_session or current_date.month != state.last_decision_session.month or current_date.year != state.last_decision_session.year))
        )
        if store.event_for_idempotency(f"decision:{decision_id}") is None:
            if not due:
                store.append_event(
                    idempotency_key=f"decision:{decision_id}",
                    session_date=current_date,
                    event_at=session["close_at"],
                    event_type="decision",
                    currency=normalized["accounting_policy"]["base_currency"],
                    details={"status": "no_op", "input_fingerprint": fingerprint, "rebalance_due": False},
                    decision_id=decision_id,
                )
                store.append_event(
                    idempotency_key=f"no-op:{decision_id}",
                    session_date=current_date,
                    event_at=session["close_at"],
                    event_type="no_op",
                    currency=normalized["accounting_policy"]["base_currency"],
                    details={"reason": "rebalance_not_due"},
                    decision_id=decision_id,
                )
            else:
                target = build_target(
                    normalized,
                    inputs,
                    sessions,
                    session_index,
                    state,
                    decision_id=decision_id,
                )
                orders = target_orders(normalized, inputs, sessions, session_index, target, state)
                risk = assess_target(normalized, inputs, sessions, session_index, state, target, orders)
                target = dict(target)
                target["risk"] = risk
                target_path, target_hash = store.write_target(target)
                store.append_event(
                    idempotency_key=f"decision:{decision_id}",
                    session_date=current_date,
                    event_at=session["close_at"],
                    event_type="decision",
                    currency=normalized["accounting_policy"]["base_currency"],
                    details={"status": "approved" if risk["status"] == "approved" else "rejected", "input_fingerprint": fingerprint, "target_id": target["target_id"], "target_sha256": target_hash},
                    decision_id=decision_id,
                    target_id=target["target_id"],
                )
                store.append_event(
                    idempotency_key=f"target:{target['target_id']}",
                    session_date=current_date,
                    event_at=session["close_at"],
                    event_type="target_published",
                    currency=normalized["accounting_policy"]["base_currency"],
                    details={"path": str(target_path.relative_to(store.root)), "sha256": target_hash, "risk": risk},
                    decision_id=decision_id,
                    target_id=target["target_id"],
                )
                if risk["status"] == "approved":
                    store.append_event(
                        idempotency_key=f"risk:{decision_id}",
                        session_date=current_date,
                        event_at=session["close_at"],
                        event_type="risk_approved",
                        currency=normalized["accounting_policy"]["base_currency"],
                        details=risk,
                        decision_id=decision_id,
                        target_id=target["target_id"],
                    )
                    for order in orders:
                        order_path, order_hash = store.write_order(order)
                        store.append_event(
                            idempotency_key=f"order:{order['order_id']}",
                            session_date=current_date,
                            event_at=session["close_at"],
                            event_type="order",
                            currency=order["currency"],
                            details={"order": order, "path": str(order_path.relative_to(store.root)), "sha256": order_hash},
                            decision_id=decision_id,
                            target_id=target["target_id"],
                            order_id=order["order_id"],
                            security_id=order["security_id"],
                        )
                    if not orders:
                        store.append_event(
                            idempotency_key=f"no-op:{decision_id}",
                            session_date=current_date,
                            event_at=session["close_at"],
                            event_type="no_op",
                            currency=normalized["accounting_policy"]["base_currency"],
                            details={"reason": "target_matches_projection"},
                            decision_id=decision_id,
                            target_id=target["target_id"],
                        )
                    elif normalized["approval_policy"]["mode"] == "auto":
                        approve_paper_decision(
                            normalized["account_id"],
                            ledger_root=ledger_root,
                            decision_id=decision_id,
                            approved=True,
                            event_at=session["close_at"],
                        )
                else:
                    store.append_event(
                        idempotency_key=f"risk:{decision_id}",
                        session_date=current_date,
                        event_at=session["close_at"],
                        event_type="risk_rejected",
                        currency=normalized["accounting_policy"]["base_currency"],
                        details=risk,
                        decision_id=decision_id,
                        target_id=target["target_id"],
                    )
        return _publish_report(store, normalized, session, decision_id, fingerprint, warnings)
    except (PaperMissingDataError, BacktestInputError, PortfolioError) as error:
        warnings.append(str(error))
        _append_halt(store, normalized, session, decision_id, fingerprint, str(error))
        return _publish_report(store, normalized, session, decision_id, fingerprint, warnings)


def _publish_report(
    store: LedgerStore,
    account: Mapping[str, Any],
    session: Mapping[str, Any],
    decision_id: str,
    fingerprint: str,
    warnings: Sequence[str],
) -> dict[str, Any]:
    report = _report_from_events(
        store,
        account,
        session,
        decision_id,
        fingerprint,
        warnings=warnings,
    )
    store.write_report(report)
    return report


def approve_paper_decision(
    account_id: str,
    *,
    ledger_root: str | Path,
    decision_id: str,
    approved: bool,
    event_at: datetime | None = None,
) -> dict[str, Any]:
    store = LedgerStore(ledger_root, account_id)
    account = store.load_account()
    events = store.events()
    decision = next((event for event in events if event["decision_id"] == decision_id and event["event_type"] == "decision"), None)
    if decision is None:
        raise PaperLedgerError(f"decision does not exist: {decision_id}")
    session_date = date.fromisoformat(decision["session_date"])
    event_at = event_at or datetime.fromisoformat(decision["event_at"])
    event_type = "approval" if approved else "manual_rejection"
    return store.append_event(
        idempotency_key=f"approval:{decision_id}",
        session_date=session_date,
        event_at=event_at,
        event_type=event_type,
        currency=account["accounting_policy"]["base_currency"],
        details={"approved": approved, "operator": "manual"},
        decision_id=decision_id,
    )


def rebuild_paper_account(account_id: str, *, ledger_root: str | Path) -> dict[str, Any]:
    store = LedgerStore(ledger_root, account_id)
    store.load_account()
    state = store.rebuild_state()
    return {
        "account_id": account_id,
        "event_count": len(store.events()),
        "last_sequence": len(store.events()),
        "cash_by_currency": {key: decimal_string(value) for key, value in sorted(state.cash_by_currency.items()) if value != 0},
        "quantities": {key: decimal_string(value) for key, value in sorted(state.quantities.items()) if value != 0},
        "nav_base": decimal_string(state.nav_base),
        "cash_base": decimal_string(state.cash_base),
        "positions_value_base": decimal_string(state.positions_value_base),
        "peak_nav_base": decimal_string(state.peak_nav_base),
    }


def reconcile_paper_account(store: LedgerStore) -> dict[str, Any]:
    events = store.events()
    state = store.rebuild_state()
    issues: list[str] = []
    if state.nav_base != state.cash_base + state.positions_value_base:
        issues.append("latest valuation does not balance cash and positions")
    valuation_count = 0
    for event in events:
        if event["event_type"] != "valuation":
            continue
        valuation_count += 1
        if event["nav_base"] != decimal_string(Decimal(event["cash_base"] or "0") + Decimal(event["positions_value_base"] or "0")):
            issues.append(f"valuation event {event['event_id']} does not balance")
    return {
        "status": "passed" if not issues else "failed",
        "account_id": store.account_id,
        "event_count": len(events),
        "valuation_count": valuation_count,
        "issues": issues,
    }


def read_paper_report(
    account_id: str, session_date: str | date, *, ledger_root: str | Path
) -> dict[str, Any]:
    store = LedgerStore(ledger_root, account_id)
    report = store.read_report(session_date)
    if report is None:
        raise PaperLedgerError(f"paper report does not exist for {session_date}")
    return report


__all__ = [
    "PAPER_ENGINE_VERSION",
    "LedgerStore",
    "PaperConflictError",
    "PaperError",
    "PaperLedgerError",
    "PaperReconciliationError",
    "PaperSpecError",
    "approve_paper_decision",
    "create_paper_account",
    "paper_account_sha256",
    "read_paper_report",
    "rebuild_paper_account",
    "reconcile_paper_account",
    "run_paper_session",
    "validate_paper_account",
]
