"""Capture and validate hash-pinned wall-clock paper evidence."""

from __future__ import annotations

import hashlib
import json
import os
import re
from collections.abc import Mapping, Sequence
from datetime import UTC, date, datetime, timedelta
from pathlib import Path
from typing import Any
from uuid import UUID, uuid4

from .paper import LedgerStore, PaperError, reconcile_paper_account, validate_paper_account

FORWARD_SCHEMA_VERSION = "1.0.0"
FORWARD_SCHEMA = "../schemas/paper-forward-record.schema.json"
CAPTURE_METHOD = "invs-paper-forward-capture"
MAX_SESSION_AGE_DAYS = 7

_SHA256 = re.compile(r"^[0-9a-f]{64}$")
_UUID = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")
_DATE = re.compile(r"^[0-9]{4}-[0-9]{2}-[0-9]{2}$")
_NON_NEGATIVE_DECIMAL = re.compile(r"^(0|[1-9][0-9]*)(\.[0-9]+)?$")
_UTC_TIMESTAMP = re.compile(
    r"^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,6})?Z$"
)


class ForwardRecordError(ValueError):
    """Raised when forward evidence is missing, stale, or inconsistent."""


class ForwardRecordConflictError(ForwardRecordError):
    """Raised when a capture output already contains different evidence."""


def canonical_json(value: Any) -> bytes:
    return json.dumps(
        value,
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
        allow_nan=False,
    ).encode("utf-8")


def sha256_file(path: str | Path) -> str:
    digest = hashlib.sha256()
    try:
        with Path(path).open("rb") as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(chunk)
    except OSError as error:
        raise ForwardRecordError(f"cannot hash forward evidence file {path}: {error}") from error
    return digest.hexdigest()


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
            parse_constant=lambda constant: (_ for _ in ()).throw(
                ValueError(f"invalid JSON constant {constant}")
            ),
        )
    except (OSError, UnicodeError, ValueError) as error:
        raise ForwardRecordError(f"invalid forward evidence JSON {path}: {error}") from error


def _exact(value: Any, expected: set[str], *, field: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise ForwardRecordError(f"{field} must be an object")
    actual = set(value)
    missing = sorted(expected - actual)
    unknown = sorted(actual - expected)
    if missing:
        raise ForwardRecordError(f"{field} is missing fields: {', '.join(missing)}")
    if unknown:
        raise ForwardRecordError(f"{field} contains unknown fields: {', '.join(unknown)}")
    return value


def _string(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not value:
        raise ForwardRecordError(f"{field} must be a non-empty string")
    return value


def _uuid(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not _UUID.fullmatch(value):
        raise ForwardRecordError(f"{field} must be a canonical UUID")
    if str(UUID(value)) != value:
        raise ForwardRecordError(f"{field} must be a canonical UUID")
    return value


def _sha(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not _SHA256.fullmatch(value):
        raise ForwardRecordError(f"{field} must be a lower-case SHA-256")
    return value


def _relative(value: Any, *, field: str) -> str:
    path = _string(value, field=field).replace("\\", "/")
    if path.startswith("/") or "\x00" in path or any(part in {"", ".", ".."} for part in path.split("/")):
        raise ForwardRecordError(f"{field} must be a safe relative path")
    return path


def _date_value(value: Any, *, field: str) -> date:
    value = _string(value, field=field)
    if not _DATE.fullmatch(value):
        raise ForwardRecordError(f"{field} must be an ISO date")
    try:
        return date.fromisoformat(value)
    except ValueError as error:
        raise ForwardRecordError(f"{field} must be an ISO date") from error


def _timestamp_value(value: Any, *, field: str) -> datetime:
    value = _string(value, field=field)
    if not _UTC_TIMESTAMP.fullmatch(value):
        raise ForwardRecordError(f"{field} must be a canonical UTC timestamp")
    try:
        parsed = datetime.fromisoformat(value)
    except ValueError as error:
        raise ForwardRecordError(f"{field} must be a canonical UTC timestamp") from error
    if parsed.tzinfo is None or parsed.utcoffset() is None or parsed.astimezone(UTC) != parsed:
        raise ForwardRecordError(f"{field} must be a canonical UTC timestamp")
    return parsed


def _resolve_file(root: Path, value: Any, *, field: str) -> tuple[Path, str]:
    relative = _relative(value, field=field)
    declared_path = root / relative
    if declared_path.is_symlink():
        raise ForwardRecordError(f"{field} must identify a regular file: {relative}")
    path = declared_path.resolve()
    try:
        path.relative_to(root)
    except ValueError as error:
        raise ForwardRecordError(f"{field} escapes the repository root") from error
    if path.is_symlink() or not path.is_file():
        raise ForwardRecordError(f"{field} must identify a regular file: {relative}")
    return path, relative


def _relative_path(root: Path, path: Path, *, field: str) -> str:
    declared_path = path.expanduser()
    if declared_path.is_symlink() or not declared_path.is_file():
        raise ForwardRecordError(f"{field} must identify a regular file: {path}")
    resolved = declared_path.resolve()
    try:
        return resolved.relative_to(root).as_posix()
    except ValueError as error:
        raise ForwardRecordError(f"{field} must be inside the repository root") from error


def _account_fitness(account: Mapping[str, Any], *, field: str) -> str:
    inputs = account.get("inputs")
    if not isinstance(inputs, list) or not inputs or any(not isinstance(item, Mapping) for item in inputs):
        raise ForwardRecordError(f"{field}.inputs must be a non-empty array")
    fitnesses = {item.get("fitness") for item in inputs}
    if not fitnesses or not fitnesses.issubset({"backtest_safe", "current_research_only"}):
        raise ForwardRecordError(f"{field}.inputs contain an unsupported fitness label")
    return "current_research_only" if "current_research_only" in fitnesses else "backtest_safe"


def _non_negative_decimal(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not _NON_NEGATIVE_DECIMAL.fullmatch(value):
        raise ForwardRecordError(f"{field} must be a canonical non-negative decimal")
    return value


def _string_array(value: Any, *, field: str) -> None:
    if not isinstance(value, list) or any(not isinstance(item, str) or not item for item in value):
        raise ForwardRecordError(f"{field} must be an array of non-empty strings")


def _validate_paper_order(
    raw: Mapping[str, Any], *, account_id: str, decision_id: str, field: str
) -> None:
    order = _exact(
        raw,
        {
            "schema_version",
            "order_id",
            "client_order_id",
            "account_id",
            "target_id",
            "decision_id",
            "target_revision",
            "decision_session",
            "execution_session",
            "security_id",
            "side",
            "quantity",
            "reference_price",
            "currency",
            "expected_notional_base",
            "expected_cost_base",
            "reason",
            "status",
        },
        field=field,
    )
    if order["schema_version"] != FORWARD_SCHEMA_VERSION:
        raise ForwardRecordError(f"{field}.schema_version is unsupported")
    for key in ("order_id", "client_order_id", "target_id", "security_id"):
        _uuid(order[key], field=f"{field}.{key}")
    if order["account_id"] != account_id or order["decision_id"] != decision_id:
        raise ForwardRecordError(f"{field} identity does not match its report")
    if (
        not isinstance(order["target_revision"], int)
        or isinstance(order["target_revision"], bool)
        or order["target_revision"] < 1
    ):
        raise ForwardRecordError(f"{field}.target_revision must be a positive integer")
    _date_value(order["decision_session"], field=f"{field}.decision_session")
    _date_value(order["execution_session"], field=f"{field}.execution_session")
    if order["side"] not in {"buy", "sell"}:
        raise ForwardRecordError(f"{field}.side is unsupported")
    for key in ("quantity", "reference_price", "expected_notional_base", "expected_cost_base"):
        _non_negative_decimal(order[key], field=f"{field}.{key}")
    if not isinstance(order["currency"], str) or not re.fullmatch(r"^[A-Z]{3}$", order["currency"]):
        raise ForwardRecordError(f"{field}.currency is invalid")
    _string(order["reason"], field=f"{field}.reason")
    if order["status"] not in {"proposed", "approved", "rejected", "cancelled", "filled"}:
        raise ForwardRecordError(f"{field}.status is unsupported")


def _validate_paper_report(
    raw: Mapping[str, Any], *, account_id: str, session: date, start: int, end: int
) -> str:
    report = _exact(
        raw,
        {
            "schema_version",
            "report_id",
            "account_id",
            "session_date",
            "decision_id",
            "input_fingerprint",
            "decision_status",
            "risk",
            "approval",
            "orders",
            "nav_base",
            "cash_base",
            "positions_value_base",
            "gross_exposure",
            "drawdown",
            "ledger_sequence_start",
            "ledger_sequence_end",
            "reconciled",
            "warnings",
        }
        if "warnings" in raw
        else {
            "schema_version",
            "report_id",
            "account_id",
            "session_date",
            "decision_id",
            "input_fingerprint",
            "decision_status",
            "risk",
            "approval",
            "orders",
            "nav_base",
            "cash_base",
            "positions_value_base",
            "gross_exposure",
            "drawdown",
            "ledger_sequence_start",
            "ledger_sequence_end",
            "reconciled",
        },
        field="observation report",
    )
    if report["schema_version"] != FORWARD_SCHEMA_VERSION:
        raise ForwardRecordError("observation report schema_version is unsupported")
    report_id = _uuid(report["report_id"], field="observation report_id")
    if report["account_id"] != account_id or report["session_date"] != session.isoformat():
        raise ForwardRecordError("observation report identity does not match the observation")
    decision_id = _uuid(report["decision_id"], field="observation report.decision_id")
    _sha(report["input_fingerprint"], field="observation report.input_fingerprint")
    if report["decision_status"] not in {"proposed", "approved", "rejected", "halted", "no_op", "settled"}:
        raise ForwardRecordError("observation report.decision_status is unsupported")
    risk = _exact(
        report["risk"],
        {"status", "policy_version", "codes", "reasons", "checked_at"},
        field="observation report.risk",
    )
    if risk["status"] not in {"approved", "rejected", "halted"}:
        raise ForwardRecordError("observation report.risk.status is unsupported")
    _string(risk["policy_version"], field="observation report.risk.policy_version")
    _string_array(risk["codes"], field="observation report.risk.codes")
    _string_array(risk["reasons"], field="observation report.risk.reasons")
    _timestamp_value(risk["checked_at"], field="observation report.risk.checked_at")
    if report["approval"] not in {"pending", "approved", "rejected", "auto_approved", "not_required"}:
        raise ForwardRecordError("observation report.approval is unsupported")
    orders = report["orders"]
    if not isinstance(orders, list):
        raise ForwardRecordError("observation report.orders must be an array")
    for index, order in enumerate(orders):
        _validate_paper_order(
            order,
            account_id=account_id,
            decision_id=decision_id,
            field=f"observation report.orders[{index}]",
        )
    for key in ("nav_base", "cash_base", "positions_value_base", "gross_exposure", "drawdown"):
        _non_negative_decimal(report[key], field=f"observation report.{key}")
    if (
        not isinstance(report["ledger_sequence_start"], int)
        or isinstance(report["ledger_sequence_start"], bool)
        or not isinstance(report["ledger_sequence_end"], int)
        or isinstance(report["ledger_sequence_end"], bool)
        or report["ledger_sequence_start"] < 1
        or report["ledger_sequence_start"] > report["ledger_sequence_end"]
        or report["ledger_sequence_end"] < 1
    ):
        raise ForwardRecordError("observation report ledger sequence bounds are invalid")
    if report["ledger_sequence_start"] != start or report["ledger_sequence_end"] != end:
        raise ForwardRecordError("observation ledger sequence does not match the report")
    if report["reconciled"] is not True:
        raise ForwardRecordError("observation report is not reconciled")
    if "warnings" in report:
        _string_array(report["warnings"], field="observation report.warnings")
    return report_id


def _validate_observation(
    raw: Mapping[str, Any],
    *,
    root: Path,
    account_ids: set[str],
    session_dates: set[date],
    top_fitness: str | None,
) -> tuple[dict[str, Any], str]:
    observation = _exact(
        raw,
        {
            "account_id",
            "session_date",
            "account_path",
            "account_sha256",
            "report_path",
            "report_sha256",
            "report_id",
            "ledger_manifest_path",
            "ledger_manifest_sha256",
            "ledger_sequence_start",
            "ledger_sequence_end",
            "reconciled",
        },
        field="observations[]",
    )
    account_id = _uuid(observation["account_id"], field="observation.account_id")
    if account_id not in account_ids:
        raise ForwardRecordError(f"observation.account_id is not listed in account_ids: {account_id}")
    session = _date_value(observation["session_date"], field="observation.session_date")
    if session not in session_dates:
        raise ForwardRecordError("observation.session_date is not listed in session_dates")
    if observation["reconciled"] is not True:
        raise ForwardRecordError("observation.reconciled must be true")
    start = observation["ledger_sequence_start"]
    end = observation["ledger_sequence_end"]
    if any(not isinstance(value, int) or isinstance(value, bool) or value < 1 for value in (start, end)):
        raise ForwardRecordError("observation ledger sequence bounds must be positive integers")
    if start > end:
        raise ForwardRecordError("observation ledger sequence start exceeds its end")

    account_path, account_relative = _resolve_file(root, observation["account_path"], field="observation.account_path")
    report_path, report_relative = _resolve_file(root, observation["report_path"], field="observation.report_path")
    manifest_path, manifest_relative = _resolve_file(
        root, observation["ledger_manifest_path"], field="observation.ledger_manifest_path"
    )
    account_sha = _sha(observation["account_sha256"], field="observation.account_sha256")
    report_sha = _sha(observation["report_sha256"], field="observation.report_sha256")
    manifest_sha = _sha(observation["ledger_manifest_sha256"], field="observation.ledger_manifest_sha256")
    if sha256_file(account_path) != account_sha:
        raise ForwardRecordError(f"observation.account_sha256 does not match {account_relative}")
    if sha256_file(report_path) != report_sha:
        raise ForwardRecordError(f"observation.report_sha256 does not match {report_relative}")
    if sha256_file(manifest_path) != manifest_sha:
        raise ForwardRecordError(f"observation.ledger_manifest_sha256 does not match {manifest_relative}")

    account_dir = account_path.parent
    if (
        account_path.name != "account.json"
        or account_dir.name != account_id
        or account_dir.parent.name != "accounts"
    ):
        raise ForwardRecordError("observation account path is not in the canonical ledger layout")
    if manifest_path != account_dir / "ledger-manifest.json":
        raise ForwardRecordError("observation ledger manifest is not adjacent to its account")
    expected_report = account_dir / "reports" / f"report-{session.isoformat()}.json"
    if report_path != expected_report:
        raise ForwardRecordError("observation report path does not match its account and session")

    account = _strict_json(account_path)
    if not isinstance(account, Mapping):
        raise ForwardRecordError(f"observation account is not an object: {account_relative}")
    try:
        normalized_account = validate_paper_account(account)
    except PaperError as error:
        raise ForwardRecordError(f"observation account is not a valid paper account: {error}") from error
    if normalized_account != account or account_path.read_bytes() != canonical_json(normalized_account) + b"\n":
        raise ForwardRecordError("observation account is not canonically serialized")
    if normalized_account["account_id"] != account_id:
        raise ForwardRecordError("observation account file identity does not match account_id")
    fitness = _account_fitness(normalized_account, field="observation account")
    if top_fitness is not None and fitness != top_fitness:
        raise ForwardRecordError("all observations must use one aggregate fitness classification")

    ledger_root = account_dir.parent.parent
    store = LedgerStore(ledger_root, account_id)
    try:
        actual_account = store.load_account()
        events = store.events()
        reconciliation = reconcile_paper_account(store)
    except (PaperError, OSError, TypeError, ValueError) as error:
        raise ForwardRecordError(f"observation ledger is invalid: {error}") from error
    if actual_account != normalized_account or store.account_path != account_path:
        raise ForwardRecordError("observation ledger account identity does not match its path")
    if reconciliation["status"] != "passed":
        raise ForwardRecordError("observation ledger is not reconciled")
    if end > len(events):
        raise ForwardRecordError("observation ledger does not cover the report sequence")

    report = _strict_json(report_path)
    if not isinstance(report, Mapping):
        raise ForwardRecordError(f"observation report is not an object: {report_relative}")
    report_id = _validate_paper_report(report, account_id=account_id, session=session, start=start, end=end)
    if report_id != observation["report_id"]:
        raise ForwardRecordError("observation report_id does not match the report file")

    manifest = _strict_json(manifest_path)
    if not isinstance(manifest, Mapping):
        raise ForwardRecordError("observation ledger manifest is not an object")
    if manifest.get("schema_version") != FORWARD_SCHEMA_VERSION or manifest.get("account_id") != account_id:
        raise ForwardRecordError("observation ledger manifest identity is invalid")
    event_count = manifest.get("event_count")
    last_sequence = manifest.get("last_sequence")
    if (
        not isinstance(event_count, int)
        or isinstance(event_count, bool)
        or not isinstance(last_sequence, int)
        or isinstance(last_sequence, bool)
        or event_count != last_sequence
        or last_sequence < end
    ):
        raise ForwardRecordError("observation ledger manifest does not cover the report sequence")
    if not isinstance(manifest.get("events"), list) or len(manifest["events"]) != event_count:
        raise ForwardRecordError("observation ledger manifest events are inconsistent")

    return {
        "account_id": account_id,
        "session_date": session.isoformat(),
        "account_path": account_relative,
        "account_sha256": account_sha,
        "report_path": report_relative,
        "report_sha256": report_sha,
        "report_id": report_id,
        "ledger_manifest_path": manifest_relative,
        "ledger_manifest_sha256": manifest_sha,
        "ledger_sequence_start": start,
        "ledger_sequence_end": end,
        "reconciled": True,
    }, fitness


def validate_forward_record(value: Mapping[str, Any], *, repo_root: str | Path) -> dict[str, Any]:
    """Validate the capture contract and all hash-pinned paper observations."""

    root = Path(repo_root).expanduser().resolve()
    raw = _exact(
        value,
        {
            "$schema",
            "schema_version",
            "capture_method",
            "record_id",
            "account_ids",
            "session_dates",
            "sessions",
            "wall_clock",
            "started_at",
            "captured_at",
            "fitness",
            "observations",
        },
        field="forward record",
    )
    if raw["$schema"] != FORWARD_SCHEMA:
        raise ForwardRecordError("forward record.$schema is unsupported")
    if raw["schema_version"] != FORWARD_SCHEMA_VERSION:
        raise ForwardRecordError("forward record.schema_version is unsupported")
    if raw["capture_method"] != CAPTURE_METHOD:
        raise ForwardRecordError("forward record.capture_method is unsupported")
    record_id = _uuid(raw["record_id"], field="record_id")

    account_ids = raw["account_ids"]
    if not isinstance(account_ids, list) or not account_ids:
        raise ForwardRecordError("account_ids must be a non-empty array")
    normalized_accounts = [_uuid(item, field=f"account_ids[{index}]") for index, item in enumerate(account_ids)]
    if normalized_accounts != sorted(set(normalized_accounts)):
        raise ForwardRecordError("account_ids must be unique and sorted")

    raw_dates = raw["session_dates"]
    if not isinstance(raw_dates, list) or not raw_dates:
        raise ForwardRecordError("session_dates must be a non-empty array")
    parsed_dates = [_date_value(item, field=f"session_dates[{index}]") for index, item in enumerate(raw_dates)]
    if parsed_dates != sorted(set(parsed_dates)):
        raise ForwardRecordError("session_dates must be unique and sorted")
    if not isinstance(raw["sessions"], int) or isinstance(raw["sessions"], bool):
        raise ForwardRecordError("sessions must be a positive integer")
    if raw["sessions"] != len(parsed_dates):
        raise ForwardRecordError("sessions must equal the number of unique session_dates")
    if raw["wall_clock"] is not True:
        raise ForwardRecordError("wall_clock must be true for a genuine forward record")
    started_at = _timestamp_value(raw["started_at"], field="started_at")
    captured_at = _timestamp_value(raw["captured_at"], field="captured_at")
    if started_at > captured_at:
        raise ForwardRecordError("started_at must not be after captured_at")
    if captured_at > datetime.now(UTC) + timedelta(minutes=5):
        raise ForwardRecordError("captured_at is too far in the future")
    latest_session = max(parsed_dates)
    if latest_session > captured_at.date():
        raise ForwardRecordError("forward record contains a session after captured_at")
    if captured_at.date() - latest_session > timedelta(days=MAX_SESSION_AGE_DAYS):
        raise ForwardRecordError(
            f"latest paper session is older than {MAX_SESSION_AGE_DAYS} calendar days at capture"
        )
    fitness = raw["fitness"]
    if fitness not in {"backtest_safe", "current_research_only"}:
        raise ForwardRecordError("forward record.fitness is unsupported")

    observations = raw["observations"]
    if not isinstance(observations, list) or not observations:
        raise ForwardRecordError("observations must be a non-empty array")
    normalized_observations: list[dict[str, Any]] = []
    seen_pairs: set[tuple[str, date]] = set()
    observed_accounts: set[str] = set()
    observed_fitness: str | None = None
    for raw_observation in observations:
        normalized, observation_fitness = _validate_observation(
            raw_observation,
            root=root,
            account_ids=set(normalized_accounts),
            session_dates=set(parsed_dates),
            top_fitness=fitness,
        )
        pair = (normalized["account_id"], date.fromisoformat(normalized["session_date"]))
        if pair in seen_pairs:
            raise ForwardRecordError("observations contains a duplicate account/session pair")
        seen_pairs.add(pair)
        observed_accounts.add(normalized["account_id"])
        if observed_fitness is not None and observed_fitness != observation_fitness:
            raise ForwardRecordError("observations use inconsistent fitness classifications")
        observed_fitness = observation_fitness
        normalized_observations.append(normalized)
    if observed_accounts != set(normalized_accounts):
        raise ForwardRecordError("observations must cover every account_id")
    if observed_fitness != fitness:
        raise ForwardRecordError("forward record fitness does not match the account inputs")
    normalized_observations.sort(key=lambda item: (item["account_id"], item["session_date"]))

    return {
        "$schema": FORWARD_SCHEMA,
        "schema_version": FORWARD_SCHEMA_VERSION,
        "capture_method": CAPTURE_METHOD,
        "record_id": record_id,
        "account_ids": normalized_accounts,
        "session_dates": [item.isoformat() for item in parsed_dates],
        "sessions": len(parsed_dates),
        "wall_clock": True,
        "started_at": started_at.isoformat().replace("+00:00", "Z"),
        "captured_at": captured_at.isoformat().replace("+00:00", "Z"),
        "fitness": fitness,
        "observations": normalized_observations,
    }


def load_forward_record(path: str | Path, *, repo_root: str | Path) -> dict[str, Any]:
    root = Path(repo_root).expanduser().resolve()
    declared_path = Path(path).expanduser()
    if declared_path.is_symlink() or not declared_path.is_file():
        raise ForwardRecordError(f"forward record must identify a regular file: {path}")
    record_path = declared_path.resolve()
    try:
        record_path.relative_to(root)
    except ValueError as error:
        raise ForwardRecordError("forward record must be inside the repository root") from error
    value = _strict_json(record_path)
    if not isinstance(value, Mapping):
        raise ForwardRecordError(f"forward record {path} must be an object")
    return validate_forward_record(value, repo_root=repo_root)


def _timestamp_now() -> str:
    return datetime.now(UTC).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def _write_immutable(path: Path, value: Mapping[str, Any]) -> None:
    content = canonical_json(value) + b"\n"
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.exists():
        if path.read_bytes() != content:
            raise ForwardRecordConflictError(f"forward record output already contains different bytes: {path}")
        return
    temporary = path.with_name(f".{path.name}.{os.getpid()}.tmp")
    try:
        temporary.write_bytes(content)
        os.replace(temporary, path)
    finally:
        if temporary.exists():
            temporary.unlink()


def capture_forward_record(
    *,
    repo_root: str | Path,
    ledger_root: str | Path,
    account_ids: Sequence[str],
    output: str | Path,
) -> Path:
    """Capture the latest recent reconciled report for each requested account."""

    root = Path(repo_root).expanduser().resolve()
    ledger = Path(ledger_root).expanduser().resolve()
    try:
        ledger.relative_to(root)
    except ValueError as error:
        raise ForwardRecordError("ledger_root must be inside the repository root") from error
    normalized_accounts = sorted({_uuid(item, field="account_id") for item in account_ids})
    if not normalized_accounts:
        raise ForwardRecordError("at least one account_id is required")
    started_at = _timestamp_now()
    observations: list[dict[str, Any]] = []
    fitnesses: set[str] = set()
    for account_id in normalized_accounts:
        try:
            store = LedgerStore(ledger, account_id)
            account = store.load_account()
            store.events()
            reconciliation = reconcile_paper_account(store)
        except (PaperError, OSError, TypeError, ValueError) as error:
            raise ForwardRecordError(f"cannot capture account {account_id}: {error}") from error
        if reconciliation["status"] != "passed":
            raise ForwardRecordError(f"account {account_id} is not reconciled")
        report_candidates = []
        for report_path in store.reports_dir.glob("report-*.json"):
            if report_path.is_symlink() or not report_path.is_file():
                continue
            match = re.fullmatch(r"report-([0-9]{4}-[0-9]{2}-[0-9]{2})\.json", report_path.name)
            if match is None:
                continue
            try:
                report_date = date.fromisoformat(match.group(1))
            except ValueError:
                continue
            report_candidates.append((report_date, report_path))
        if not report_candidates:
            raise ForwardRecordError(f"account {account_id} has no paper session report")
        session_date, report_path = max(report_candidates, key=lambda item: item[0])
        try:
            report = store.read_report(session_date)
        except (PaperError, OSError, TypeError, ValueError) as error:
            raise ForwardRecordError(f"cannot read account {account_id} latest report: {error}") from error
        if not isinstance(report, Mapping):
            raise ForwardRecordError(f"account {account_id} latest report is not reconciled")
        sequence_start = report.get("ledger_sequence_start")
        sequence_end = report.get("ledger_sequence_end")
        if (
            not isinstance(sequence_start, int)
            or isinstance(sequence_start, bool)
            or not isinstance(sequence_end, int)
            or isinstance(sequence_end, bool)
            or sequence_start < 1
            or sequence_end < sequence_start
        ):
            raise ForwardRecordError(f"account {account_id} latest report has invalid ledger sequence")
        try:
            report_id = _validate_paper_report(
                report,
                account_id=account_id,
                session=session_date,
                start=sequence_start,
                end=sequence_end,
            )
        except ForwardRecordError as error:
            raise ForwardRecordError(f"account {account_id} latest report is invalid: {error}") from error
        fitness = _account_fitness(account, field=f"account {account_id}")
        fitnesses.add(fitness)
        account_path = store.account_path
        manifest_path = store.manifest_path
        observations.append(
            {
                "account_id": account_id,
                "session_date": session_date.isoformat(),
                "account_path": _relative_path(root, account_path, field="account_path"),
                "account_sha256": sha256_file(account_path),
                "report_path": _relative_path(root, report_path, field="report_path"),
                "report_sha256": sha256_file(report_path),
                "report_id": report_id,
                "ledger_manifest_path": _relative_path(root, manifest_path, field="ledger_manifest_path"),
                "ledger_manifest_sha256": sha256_file(manifest_path),
                "ledger_sequence_start": sequence_start,
                "ledger_sequence_end": sequence_end,
                "reconciled": True,
            }
        )
    captured_at = _timestamp_now()
    record = {
        "$schema": FORWARD_SCHEMA,
        "schema_version": FORWARD_SCHEMA_VERSION,
        "capture_method": CAPTURE_METHOD,
        "record_id": str(uuid4()),
        "account_ids": normalized_accounts,
        "session_dates": sorted({item["session_date"] for item in observations}),
        "sessions": len({item["session_date"] for item in observations}),
        "wall_clock": True,
        "started_at": started_at,
        "captured_at": captured_at,
        "fitness": "current_research_only" if "current_research_only" in fitnesses else "backtest_safe",
        "observations": sorted(observations, key=lambda item: (item["account_id"], item["session_date"])),
    }
    normalized = validate_forward_record(record, repo_root=root)
    declared_output = Path(output).expanduser()
    if declared_output.is_symlink():
        raise ForwardRecordError("output must not be a symlink")
    output_path = declared_output.resolve()
    try:
        output_path.relative_to(root)
    except ValueError as error:
        raise ForwardRecordError("output must be inside the repository root") from error
    _write_immutable(output_path, normalized)
    return output_path


__all__ = [
    "CAPTURE_METHOD",
    "FORWARD_SCHEMA",
    "FORWARD_SCHEMA_VERSION",
    "ForwardRecordConflictError",
    "ForwardRecordError",
    "capture_forward_record",
    "load_forward_record",
    "validate_forward_record",
]
