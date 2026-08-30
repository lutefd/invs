"""Strict local input loading for the v0.5 backtest engine.

The loader is deliberately independent of provider clients and PostgreSQL.  A
backtest can only see bytes named by its experiment specification, and every
row is checked before it reaches the simulator.
"""

from __future__ import annotations

import json
import re
from collections.abc import Mapping
from dataclasses import dataclass
from datetime import UTC, date, datetime
from decimal import Decimal, InvalidOperation
from pathlib import Path
from typing import Any, Final
from uuid import UUID

from .experiments import BacktestSpecError, sha256_file, validate_experiment_spec

INPUT_SCHEMA_VERSION: Final[str] = "1.0.0"

_UTC_TIMESTAMP = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?Z$")
_DATE = re.compile(r"^\d{4}-\d{2}-\d{2}$")
_UUID = re.compile(
    r"^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"
)
_DECIMAL = re.compile(r"^(0|[1-9][0-9]*)(\.[0-9]+)?$")
_SIGNED_DECIMAL = re.compile(r"^-?(0|[1-9][0-9]*)(\.[0-9]+)?$")

_ENVELOPE_FIELDS = frozenset({"schema_version", "artifact_kind", "artifact_id", "available_at", "rows"})
_PRICE_FIELDS = frozenset(
    {
        "security_id",
        "session_date",
        "observed_at",
        "available_at",
        "currency",
        "price_basis",
        "open",
        "high",
        "low",
        "close",
        "volume",
        "has_volume",
        "source_record_id",
    }
)
_CALENDAR_FIELDS = frozenset({"session_date", "open_at", "close_at", "available_at", "session_status"})
_MEMBERSHIP_FIELDS = frozenset(
    {"security_id", "valid_from", "valid_until", "member", "available_at", "revision"}
)
_ACTION_FIELDS = frozenset(
    {
        "id",
        "security_id",
        "action_type",
        "effective_date",
        "payment_date",
        "available_at",
        "currency",
        "ratio_numerator",
        "ratio_denominator",
        "cash_amount",
        "settlement_price",
        "target_security_id",
    }
)
_FX_FIELDS = frozenset(
    {"fixing_at", "base_currency", "quote_currency", "rate", "available_at"}
)
_OBSERVATION_FIELDS = frozenset({"observation_id", "observed_at", "available_at", "value", "revision"})
_KINDS = frozenset(
    {"prices", "calendar", "membership", "corporate_actions", "fx", "feature", "macro", "risk_free"}
)


class BacktestInputError(BacktestSpecError):
    """Raised when a pinned backtest input is malformed or unavailable."""


@dataclass(frozen=True)
class LoadedInputArtifact:
    kind: str
    artifact_id: str
    path: Path
    sha256: str
    available_at: datetime
    rows: tuple[dict[str, Any], ...]


@dataclass(frozen=True)
class BacktestInputs:
    """All input rows indexed in deterministic order for one experiment."""

    root: Path
    artifacts: dict[str, LoadedInputArtifact]
    prices: tuple[dict[str, Any], ...]
    calendar: tuple[dict[str, Any], ...]
    membership: tuple[dict[str, Any], ...]
    corporate_actions: tuple[dict[str, Any], ...]
    fx: tuple[dict[str, Any], ...]
    risk_free: tuple[dict[str, Any], ...]
    observations: dict[str, tuple[dict[str, Any], ...]]

    def artifact(self, kind: str) -> LoadedInputArtifact | None:
        return self.artifacts.get(kind)


def parse_utc(value: Any, *, field: str) -> datetime:
    if not isinstance(value, str) or not _UTC_TIMESTAMP.fullmatch(value):
        raise BacktestInputError(f"{field} must be a canonical UTC timestamp")
    try:
        parsed = datetime.fromisoformat(value)
    except ValueError as error:
        raise BacktestInputError(f"{field} must be a canonical UTC timestamp") from error
    if parsed.tzinfo is None or parsed.utcoffset() is None or parsed.astimezone(UTC) != parsed:
        raise BacktestInputError(f"{field} must be a canonical UTC timestamp")
    fraction = f".{parsed.microsecond:06d}".rstrip("0") if parsed.microsecond else ""
    canonical = parsed.strftime("%Y-%m-%dT%H:%M:%S") + fraction + "Z"
    if value != canonical:
        raise BacktestInputError(f"{field} must be a canonical UTC timestamp")
    return parsed


def parse_date(value: Any, *, field: str) -> date:
    if not isinstance(value, str) or not _DATE.fullmatch(value):
        raise BacktestInputError(f"{field} must be an ISO date")
    try:
        return date.fromisoformat(value)
    except ValueError as error:
        raise BacktestInputError(f"{field} must be an ISO date") from error


def decimal(value: Any, *, field: str, non_negative: bool = False, positive: bool = False) -> Decimal:
    pattern = _DECIMAL if non_negative else _SIGNED_DECIMAL
    if not isinstance(value, str) or not pattern.fullmatch(value):
        label = "non-negative" if non_negative else "signed"
        raise BacktestInputError(f"{field} must be a canonical {label} decimal string")
    try:
        parsed = Decimal(value)
    except InvalidOperation as error:
        raise BacktestInputError(f"{field} must be a decimal string") from error
    if non_negative and parsed < 0:
        raise BacktestInputError(f"{field} must be non-negative")
    if positive and parsed <= 0:
        raise BacktestInputError(f"{field} must be positive")
    return parsed


def _uuid(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not _UUID.fullmatch(value):
        raise BacktestInputError(f"{field} must be a canonical UUID")
    try:
        parsed = UUID(value)
    except ValueError as error:
        raise BacktestInputError(f"{field} must be a canonical UUID") from error
    if str(parsed) != value:
        raise BacktestInputError(f"{field} must be a canonical UUID")
    return value


def _currency(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not re.fullmatch(r"^[A-Z]{3}$", value):
        raise BacktestInputError(f"{field} must be an uppercase ISO-4217 currency")
    return value


def _strict_json(path: Path) -> dict[str, Any]:
    def no_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for key, value in pairs:
            if key in result:
                raise ValueError(f"duplicate JSON key {key!r}")
            result[key] = value
        return result

    try:
        document = json.loads(
            path.read_text(encoding="utf-8"),
            object_pairs_hook=no_duplicates,
            parse_constant=lambda value: (_ for _ in ()).throw(
                ValueError(f"invalid JSON constant {value}")
            ),
        )
    except (OSError, UnicodeError, ValueError) as error:
        raise BacktestInputError(f"invalid input JSON {path}: {error}") from error
    if not isinstance(document, dict):
        raise BacktestInputError(f"input JSON {path} must be an object")
    return document


def _row_error(kind: str, index: int, message: str) -> BacktestInputError:
    return BacktestInputError(f"{kind} row {index}: {message}")


def _fields(row: Any, *, expected: frozenset[str], kind: str, index: int) -> dict[str, Any]:
    if not isinstance(row, Mapping):
        raise _row_error(kind, index, "must be an object")
    if not set(row).issubset(expected):
        unknown = sorted(set(row) - expected)
        raise _row_error(kind, index, f"contains unknown fields: {', '.join(unknown)}")
    return dict(row)


def _price_rows(rows: list[Any]) -> tuple[dict[str, Any], ...]:
    result: list[dict[str, Any]] = []
    identities: set[tuple[str, date]] = set()
    for index, raw in enumerate(rows):
        row = _fields(raw, expected=_PRICE_FIELDS, kind="prices", index=index)
        required = _PRICE_FIELDS - {"source_record_id"}
        if not required.issubset(row):
            raise _row_error("prices", index, "is missing required fields")
        security_id = _uuid(row["security_id"], field=f"prices row {index}.security_id")
        session_date = parse_date(row["session_date"], field=f"prices row {index}.session_date")
        observed_at = parse_utc(row["observed_at"], field=f"prices row {index}.observed_at")
        available_at = parse_utc(row["available_at"], field=f"prices row {index}.available_at")
        if available_at < observed_at:
            raise _row_error("prices", index, "available_at must not precede observed_at")
        currency = _currency(row["currency"], field=f"prices row {index}.currency")
        if row["price_basis"] != "raw":
            raise _row_error("prices", index, "price_basis must be raw")
        prices = {
            key: decimal(row[key], field=f"prices row {index}.{key}", non_negative=True, positive=True)
            for key in ("open", "high", "low", "close")
        }
        if prices["high"] < max(prices["open"], prices["close"]) or prices["low"] > min(
            prices["open"], prices["close"]
        ):
            raise _row_error("prices", index, "high/low do not contain open and close")
        volume = decimal(row["volume"], field=f"prices row {index}.volume", non_negative=True)
        if not isinstance(row["has_volume"], bool):
            raise _row_error("prices", index, "has_volume must be boolean")
        identity = (security_id, session_date)
        if identity in identities:
            raise _row_error("prices", index, "duplicate security/session observation")
        identities.add(identity)
        normalized = {
            "security_id": security_id,
            "session_date": session_date,
            "observed_at": observed_at,
            "available_at": available_at,
            "currency": currency,
            "price_basis": "raw",
            **prices,
            "volume": volume,
            "has_volume": row["has_volume"],
        }
        if "source_record_id" in row:
            if not isinstance(row["source_record_id"], str) or not row["source_record_id"]:
                raise _row_error("prices", index, "source_record_id must be non-empty")
            normalized["source_record_id"] = row["source_record_id"]
        result.append(normalized)
    return tuple(sorted(result, key=lambda item: (item["session_date"], item["security_id"])))


def _calendar_rows(rows: list[Any]) -> tuple[dict[str, Any], ...]:
    result: list[dict[str, Any]] = []
    identities: set[date] = set()
    for index, raw in enumerate(rows):
        row = _fields(raw, expected=_CALENDAR_FIELDS, kind="calendar", index=index)
        required = _CALENDAR_FIELDS - {"session_status"}
        if not required.issubset(row):
            raise _row_error("calendar", index, "is missing required fields")
        session_date = parse_date(row["session_date"], field=f"calendar row {index}.session_date")
        open_at = parse_utc(row["open_at"], field=f"calendar row {index}.open_at")
        close_at = parse_utc(row["close_at"], field=f"calendar row {index}.close_at")
        available_at = parse_utc(row["available_at"], field=f"calendar row {index}.available_at")
        if not open_at < close_at:
            raise _row_error("calendar", index, "open_at must precede close_at")
        if session_date in identities:
            raise _row_error("calendar", index, "duplicate session date")
        status = row.get("session_status", "open")
        if status not in {"open", "early_close"}:
            raise _row_error("calendar", index, "session_status is unsupported")
        identities.add(session_date)
        result.append(
            {
                "session_date": session_date,
                "open_at": open_at,
                "close_at": close_at,
                "available_at": available_at,
                "session_status": status,
            }
        )
    return tuple(sorted(result, key=lambda item: item["session_date"]))


def _membership_rows(rows: list[Any]) -> tuple[dict[str, Any], ...]:
    result: list[dict[str, Any]] = []
    identities: set[tuple[str, date, date | None, int]] = set()
    for index, raw in enumerate(rows):
        row = _fields(raw, expected=_MEMBERSHIP_FIELDS, kind="membership", index=index)
        required = _MEMBERSHIP_FIELDS - {"revision"}
        if not required.issubset(row):
            raise _row_error("membership", index, "is missing required fields")
        security_id = _uuid(row["security_id"], field=f"membership row {index}.security_id")
        valid_from = parse_date(row["valid_from"], field=f"membership row {index}.valid_from")
        valid_until = (
            parse_date(row["valid_until"], field=f"membership row {index}.valid_until")
            if row["valid_until"] is not None
            else None
        )
        if valid_until is not None and valid_until <= valid_from:
            raise _row_error("membership", index, "valid_until must follow valid_from")
        available_at = parse_utc(row["available_at"], field=f"membership row {index}.available_at")
        if not isinstance(row["member"], bool):
            raise _row_error("membership", index, "member must be boolean")
        revision = row.get("revision", 0)
        if not isinstance(revision, int) or isinstance(revision, bool) or revision < 0:
            raise _row_error("membership", index, "revision must be a non-negative integer")
        identity = (security_id, valid_from, valid_until, revision)
        if identity in identities:
            raise _row_error("membership", index, "duplicate membership assertion")
        identities.add(identity)
        result.append(
            {
                "security_id": security_id,
                "valid_from": valid_from,
                "valid_until": valid_until,
                "member": row["member"],
                "available_at": available_at,
                "revision": revision,
            }
        )
    return tuple(
        sorted(
            result,
            key=lambda item: (item["security_id"], item["valid_from"], item["available_at"], item["revision"]),
        )
    )


def _action_rows(rows: list[Any]) -> tuple[dict[str, Any], ...]:
    result: list[dict[str, Any]] = []
    identities: set[str] = set()
    for index, raw in enumerate(rows):
        row = _fields(raw, expected=_ACTION_FIELDS, kind="corporate_actions", index=index)
        if not _ACTION_FIELDS.issubset(row):
            raise _row_error("corporate_actions", index, "is missing required fields")
        action_id = _uuid(row["id"], field=f"corporate_actions row {index}.id")
        if action_id in identities:
            raise _row_error("corporate_actions", index, "duplicate action id")
        identities.add(action_id)
        security_id = _uuid(row["security_id"], field=f"corporate_actions row {index}.security_id")
        action_type = row["action_type"]
        if action_type not in {"split", "cash_dividend", "delisting"}:
            raise _row_error("corporate_actions", index, "action_type is unsupported")
        effective_date = parse_date(
            row["effective_date"], field=f"corporate_actions row {index}.effective_date"
        )
        payment_date = (
            parse_date(row["payment_date"], field=f"corporate_actions row {index}.payment_date")
            if row["payment_date"] is not None
            else None
        )
        if payment_date is not None and payment_date < effective_date:
            raise _row_error("corporate_actions", index, "payment_date must not precede effective_date")
        available_at = parse_utc(row["available_at"], field=f"corporate_actions row {index}.available_at")
        currency = _currency(row["currency"], field=f"corporate_actions row {index}.currency")
        numerator = decimal(
            row["ratio_numerator"],
            field=f"corporate_actions row {index}.ratio_numerator",
            non_negative=True,
        )
        denominator = decimal(
            row["ratio_denominator"],
            field=f"corporate_actions row {index}.ratio_denominator",
            non_negative=True,
        )
        cash_amount = decimal(
            row["cash_amount"], field=f"corporate_actions row {index}.cash_amount", non_negative=True
        )
        settlement_price = (
            decimal(
                row["settlement_price"],
                field=f"corporate_actions row {index}.settlement_price",
                non_negative=True,
                positive=True,
            )
            if row["settlement_price"] is not None
            else None
        )
        target_security_id = (
            _uuid(row["target_security_id"], field=f"corporate_actions row {index}.target_security_id")
            if row["target_security_id"] is not None
            else None
        )
        if action_type == "split" and (numerator <= 0 or denominator <= 0):
            raise _row_error("corporate_actions", index, "split ratio must be positive")
        if action_type == "cash_dividend" and (payment_date is None or cash_amount <= 0):
            raise _row_error("corporate_actions", index, "cash dividend needs payment_date and positive cash_amount")
        if action_type == "delisting" and target_security_id is not None:
            raise _row_error("corporate_actions", index, "delisting cannot specify target_security_id")
        result.append(
            {
                "id": action_id,
                "security_id": security_id,
                "action_type": action_type,
                "effective_date": effective_date,
                "payment_date": payment_date,
                "available_at": available_at,
                "currency": currency,
                "ratio_numerator": numerator,
                "ratio_denominator": denominator,
                "cash_amount": cash_amount,
                "settlement_price": settlement_price,
                "target_security_id": target_security_id,
            }
        )
    return tuple(sorted(result, key=lambda item: (item["effective_date"], item["id"])))


def _fx_rows(rows: list[Any]) -> tuple[dict[str, Any], ...]:
    result: list[dict[str, Any]] = []
    identities: set[tuple[datetime, str, str]] = set()
    for index, raw in enumerate(rows):
        row = _fields(raw, expected=_FX_FIELDS, kind="fx", index=index)
        if not _FX_FIELDS.issubset(row):
            raise _row_error("fx", index, "is missing required fields")
        fixing_at = parse_utc(row["fixing_at"], field=f"fx row {index}.fixing_at")
        available_at = parse_utc(row["available_at"], field=f"fx row {index}.available_at")
        base_currency = _currency(row["base_currency"], field=f"fx row {index}.base_currency")
        quote_currency = _currency(row["quote_currency"], field=f"fx row {index}.quote_currency")
        if base_currency == quote_currency:
            raise _row_error("fx", index, "base_currency and quote_currency must differ")
        rate = decimal(row["rate"], field=f"fx row {index}.rate", non_negative=True, positive=True)
        identity = (fixing_at, base_currency, quote_currency)
        if identity in identities:
            raise _row_error("fx", index, "duplicate fixing for currency pair")
        identities.add(identity)
        result.append(
            {
                "fixing_at": fixing_at,
                "base_currency": base_currency,
                "quote_currency": quote_currency,
                "rate": rate,
                "available_at": available_at,
            }
        )
    return tuple(
        sorted(
            result,
            key=lambda item: (
                item["fixing_at"],
                item["base_currency"],
                item["quote_currency"],
            ),
        )
    )


def _observation_rows(rows: list[Any], *, kind: str) -> tuple[dict[str, Any], ...]:
    result: list[dict[str, Any]] = []
    identities: set[tuple[str, datetime, int]] = set()
    for index, raw in enumerate(rows):
        row = _fields(raw, expected=_OBSERVATION_FIELDS, kind=kind, index=index)
        if not {"observation_id", "observed_at", "available_at", "value"}.issubset(row):
            raise _row_error(kind, index, "is missing required fields")
        observation_id = row["observation_id"]
        if not isinstance(observation_id, str) or not observation_id:
            raise _row_error(kind, index, "observation_id must be non-empty")
        observed_at = parse_utc(row["observed_at"], field=f"{kind} row {index}.observed_at")
        available_at = parse_utc(row["available_at"], field=f"{kind} row {index}.available_at")
        value = decimal(row["value"], field=f"{kind} row {index}.value")
        revision = row.get("revision", 0)
        if not isinstance(revision, int) or isinstance(revision, bool) or revision < 0:
            raise _row_error(kind, index, "revision must be a non-negative integer")
        identity = (observation_id, observed_at, revision)
        if identity in identities:
            raise _row_error(kind, index, "duplicate observation revision")
        identities.add(identity)
        result.append(
            {
                "observation_id": observation_id,
                "observed_at": observed_at,
                "available_at": available_at,
                "value": value,
                "revision": revision,
            }
        )
    return tuple(sorted(result, key=lambda item: (item["observed_at"], item["observation_id"], item["revision"])))


def _load_artifact(path: Path, *, expected: Mapping[str, Any]) -> LoadedInputArtifact:
    if not path.is_file():
        raise BacktestInputError(f"pinned input artifact does not exist: {path}")
    actual_sha256 = sha256_file(path)
    if actual_sha256 != expected["sha256"]:
        raise BacktestInputError(f"pinned input artifact hash mismatch: {path}")
    document = _strict_json(path)
    if set(document) != _ENVELOPE_FIELDS:
        raise BacktestInputError(f"input artifact {path} has an invalid envelope")
    if document["schema_version"] != INPUT_SCHEMA_VERSION:
        raise BacktestInputError(f"input artifact {path} has an unsupported schema_version")
    if document["artifact_kind"] != expected["kind"] or document["artifact_kind"] not in _KINDS:
        raise BacktestInputError(f"input artifact {path} kind does not match its experiment reference")
    artifact_id = _uuid(document["artifact_id"], field=f"{path}.artifact_id")
    if artifact_id != expected["artifact_id"]:
        raise BacktestInputError(f"input artifact {path} artifact_id does not match its experiment reference")
    available_at = parse_utc(document["available_at"], field=f"{path}.available_at")
    expected_available_at = parse_utc(expected["available_at"], field="experiment input available_at")
    if available_at != expected_available_at:
        raise BacktestInputError(f"input artifact {path} available_at does not match its experiment reference")
    if not isinstance(document["rows"], list):
        raise BacktestInputError(f"input artifact {path}.rows must be an array")
    kind = expected["kind"]
    if kind == "prices":
        rows = _price_rows(document["rows"])
    elif kind == "calendar":
        rows = _calendar_rows(document["rows"])
    elif kind == "membership":
        rows = _membership_rows(document["rows"])
    elif kind == "corporate_actions":
        rows = _action_rows(document["rows"])
    elif kind == "fx":
        rows = _fx_rows(document["rows"])
    else:
        rows = _observation_rows(document["rows"], kind=kind)
    for index, row in enumerate(rows):
        row_available_at = row["available_at"]
        if row_available_at > available_at:
            raise BacktestInputError(f"{kind} row {index} is available after its artifact envelope")
    return LoadedInputArtifact(kind, artifact_id, path, actual_sha256, available_at, rows)


def _resolve_under_root(root: Path, relative_path: str) -> Path:
    root = root.expanduser().resolve()
    path = (root / relative_path).resolve()
    try:
        path.relative_to(root)
    except ValueError as error:
        raise BacktestInputError(f"input path escapes data root: {relative_path}") from error
    return path


def load_backtest_inputs(spec: Mapping[str, Any], *, data_root: str | Path) -> BacktestInputs:
    """Verify and load every local artifact referenced by a validated spec."""

    normalized = validate_experiment_spec(spec)
    root = Path(data_root).expanduser().resolve()
    artifacts: dict[str, LoadedInputArtifact] = {}
    for reference in normalized["inputs"]:
        path = _resolve_under_root(root, reference["path"])
        artifacts[reference["kind"]] = _load_artifact(path, expected=reference)
    corporate_actions = artifacts.get("corporate_actions")
    fx = artifacts.get("fx")
    risk_free = artifacts.get("risk_free")
    return BacktestInputs(
        root=root,
        artifacts=artifacts,
        prices=artifacts["prices"].rows,
        calendar=artifacts["calendar"].rows,
        membership=artifacts["membership"].rows,
        corporate_actions=corporate_actions.rows if corporate_actions is not None else (),
        fx=fx.rows if fx is not None else (),
        risk_free=risk_free.rows if risk_free is not None else (),
        observations={
            kind: artifact.rows
            for kind, artifact in artifacts.items()
            if kind in {"feature", "macro"}
        },
    )


__all__ = [
    "INPUT_SCHEMA_VERSION",
    "BacktestInputError",
    "BacktestInputs",
    "LoadedInputArtifact",
    "decimal",
    "load_backtest_inputs",
    "parse_date",
    "parse_utc",
]
