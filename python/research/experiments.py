"""Strict, deterministic v0.5 backtest experiment specifications."""

from __future__ import annotations

import hashlib
import json
import re
from collections.abc import Mapping
from dataclasses import dataclass
from datetime import UTC, date, datetime
from decimal import Decimal, InvalidOperation
from itertools import pairwise
from pathlib import Path
from typing import Any, Final
from uuid import UUID, uuid5

SCHEMA_VERSION: Final[str] = "1.0.0"
ENGINE_VERSION: Final[str] = "python-backtest-1.0.0"

_EXPERIMENT_NAMESPACE = UUID("9e3cf3f7-9f54-5d0e-8e31-3db5e1d3f7f1")
_SHA256 = re.compile(r"^[0-9a-f]{64}$")
_SEMVER = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+$")
_GIT_COMMIT = re.compile(r"^[0-9a-f]{40}$")
_DECIMAL = re.compile(r"^-?(0|[1-9][0-9]*)(\.[0-9]+)?$")
_NON_NEGATIVE_DECIMAL = re.compile(r"^(0|[1-9][0-9]*)(\.[0-9]+)?$")
_UTC_TIMESTAMP = re.compile(
    r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?Z$"
)
_DATE = re.compile(r"^\d{4}-\d{2}-\d{2}$")

_SPEC_FIELDS = frozenset(
    {
        "schema_version",
        "experiment_id",
        "strategy",
        "period",
        "universe",
        "inputs",
        "benchmark",
        "decision_policy",
        "accounting_policy",
        "cost_policy",
        "risk_policy",
        "partitions",
        "walk_forward_windows",
        "missing_data_policy",
        "random_seed",
    }
)
_ARTIFACT_REF_FIELDS = frozenset(
    {"kind", "artifact_id", "path", "sha256", "available_at", "historical_fitness"}
)
_SUPPORTED_INPUT_KINDS = frozenset(
    {"prices", "calendar", "membership", "corporate_actions", "fx", "feature", "macro"}
)
_PARTITION_KINDS = ("development", "validation", "holdout")


class BacktestSpecError(ValueError):
    """Raised when a backtest experiment specification is unsafe or malformed."""


class BacktestSpecConflictError(BacktestSpecError):
    """Raised when an immutable experiment identity contains different bytes."""


class BacktestSpecValidationError(BacktestSpecError):
    """Raised when a published experiment specification fails validation."""


@dataclass(frozen=True)
class ValidatedExperimentSpec:
    path: Path
    spec: dict[str, Any]
    sha256: str


def canonical_json(value: Any) -> bytes:
    return json.dumps(
        value,
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
        allow_nan=False,
    ).encode("utf-8")


def sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def sha256_file(path: str | Path) -> str:
    digest = hashlib.sha256()
    try:
        with Path(path).open("rb") as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(chunk)
    except OSError as error:
        raise BacktestSpecValidationError(f"cannot hash experiment file {path}: {error}") from error
    return digest.hexdigest()


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
        raise BacktestSpecValidationError(f"invalid experiment JSON {path}: {error}") from error
    if not isinstance(document, dict):
        raise BacktestSpecValidationError(f"experiment JSON {path} must be an object")
    return document


def _nonempty(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise BacktestSpecError(f"{field} must be a non-empty string")
    return value.strip()


def _uuid(value: Any, *, field: str) -> str:
    if not isinstance(value, str):
        raise BacktestSpecError(f"{field} must be a canonical UUID")
    try:
        parsed = UUID(value)
    except ValueError as error:
        raise BacktestSpecError(f"{field} must be a canonical UUID") from error
    canonical = str(parsed)
    if value != canonical:
        raise BacktestSpecError(f"{field} must be a canonical UUID")
    return canonical


def _sha256(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not _SHA256.fullmatch(value):
        raise BacktestSpecError(f"{field} must be a lower-case SHA-256")
    return value


def _semver(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not _SEMVER.fullmatch(value):
        raise BacktestSpecError(f"{field} must be semantic version x.y.z")
    return value


def _git_commit(value: Any, *, field: str) -> str:
    if value == "unknown":
        return value
    if not isinstance(value, str) or not _GIT_COMMIT.fullmatch(value):
        raise BacktestSpecError(f"{field} must be a lower-case Git SHA or unknown")
    return value


def _currency(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not re.fullmatch(r"^[A-Z]{3}$", value):
        raise BacktestSpecError(f"{field} must be an ISO-4217 uppercase currency")
    return value


def _decimal(value: Any, *, field: str, non_negative: bool = False, positive: bool = False) -> str:
    pattern = _NON_NEGATIVE_DECIMAL if non_negative else _DECIMAL
    if not isinstance(value, str) or not pattern.fullmatch(value):
        label = "non-negative decimal" if non_negative else "decimal"
        raise BacktestSpecError(f"{field} must be a canonical {label} string")
    try:
        parsed = Decimal(value)
    except InvalidOperation as error:
        raise BacktestSpecError(f"{field} must be a decimal string") from error
    if positive and parsed <= 0:
        raise BacktestSpecError(f"{field} must be positive")
    if non_negative and parsed < 0:
        raise BacktestSpecError(f"{field} must be non-negative")
    return value


def _timestamp(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not _UTC_TIMESTAMP.fullmatch(value):
        raise BacktestSpecError(f"{field} must be a canonical UTC timestamp")
    try:
        parsed = datetime.fromisoformat(value)
    except ValueError as error:
        raise BacktestSpecError(f"{field} must be a canonical UTC timestamp") from error
    if parsed.tzinfo is None or parsed.utcoffset() is None or parsed.astimezone(UTC) != parsed:
        raise BacktestSpecError(f"{field} must be a canonical UTC timestamp")
    return value


def _date(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not _DATE.fullmatch(value):
        raise BacktestSpecError(f"{field} must be an ISO date")
    try:
        date.fromisoformat(value)
    except ValueError as error:
        raise BacktestSpecError(f"{field} must be an ISO date") from error
    return value


def _date_range(value: Any, *, field: str) -> dict[str, str]:
    if not isinstance(value, Mapping) or set(value) != {"start_date", "end_date"}:
        raise BacktestSpecError(f"{field} must contain only start_date and end_date")
    start = _date(value["start_date"], field=f"{field}.start_date")
    end = _date(value["end_date"], field=f"{field}.end_date")
    if start > end:
        raise BacktestSpecError(f"{field} start_date must not be after end_date")
    return {"start_date": start, "end_date": end}


def _safe_relative_path(value: Any, *, field: str) -> str:
    path = _nonempty(value, field=field)
    if path.startswith(("/", "\\")) or "\x00" in path:
        raise BacktestSpecError(f"{field} must be a safe relative path")
    parts = path.replace("\\", "/").split("/")
    if any(part == ".." for part in parts):
        raise BacktestSpecError(f"{field} must not contain parent traversal")
    return path


def _artifact_ref(value: Any, *, field: str) -> dict[str, Any]:
    if not isinstance(value, Mapping) or not set(value).issubset(_ARTIFACT_REF_FIELDS):
        raise BacktestSpecError(f"{field} contains unknown fields")
    required = {"kind", "artifact_id", "path", "sha256", "available_at"}
    if not required.issubset(value):
        missing = sorted(required - set(value))
        raise BacktestSpecError(f"{field} is missing fields: {', '.join(missing)}")
    kind = _nonempty(value["kind"], field=f"{field}.kind")
    if kind not in _SUPPORTED_INPUT_KINDS:
        raise BacktestSpecError(f"{field}.kind is unsupported")
    result: dict[str, Any] = {
        "kind": kind,
        "artifact_id": _uuid(value["artifact_id"], field=f"{field}.artifact_id"),
        "path": _safe_relative_path(value["path"], field=f"{field}.path"),
        "sha256": _sha256(value["sha256"], field=f"{field}.sha256"),
        "available_at": _timestamp(value["available_at"], field=f"{field}.available_at"),
        "historical_fitness": value.get("historical_fitness", "backtest_safe"),
    }
    if result["historical_fitness"] not in {"backtest_safe", "installation_replay_only", "unsupported"}:
        raise BacktestSpecError(f"{field}.historical_fitness is unsupported")
    if result["historical_fitness"] != "backtest_safe":
        raise BacktestSpecError(f"{field} is not admitted as backtest_safe")
    return result


def _strategy(value: Any) -> dict[str, Any]:
    field = "strategy"
    if not isinstance(value, Mapping) or set(value) != {"name", "version", "git_commit", "parameters"}:
        raise BacktestSpecError(f"{field} must contain name, version, git_commit, and parameters")
    name = value["name"]
    if name not in {"buy_and_hold", "equal_weight", "momentum_12_1"}:
        raise BacktestSpecError("strategy.name is unsupported")
    parameters = value["parameters"]
    if not isinstance(parameters, Mapping):
        raise BacktestSpecError("strategy.parameters must be an object")
    params = dict(parameters)
    if name == "buy_and_hold" and params:
        raise BacktestSpecError("buy_and_hold does not accept parameters")
    if name == "equal_weight" and (
        set(params) != {"rebalance_frequency"}
        or params["rebalance_frequency"] not in {"daily", "monthly"}
    ):
        raise BacktestSpecError("equal_weight requires daily or monthly rebalance_frequency")
    if name == "momentum_12_1":
        expected = {"lookback_sessions", "skip_sessions", "top_k", "rebalance_frequency"}
        if set(params) != expected or params["rebalance_frequency"] not in {"daily", "monthly"}:
            raise BacktestSpecError("momentum_12_1 has invalid parameters")
        for key in ("lookback_sessions", "skip_sessions", "top_k"):
            if not isinstance(params[key], int) or isinstance(params[key], bool) or params[key] < 1:
                raise BacktestSpecError(f"strategy.parameters.{key} must be a positive integer")
        if params["skip_sessions"] >= params["lookback_sessions"]:
            raise BacktestSpecError("momentum_12_1 skip_sessions must be less than lookback_sessions")
    return {
        "name": name,
        "version": _semver(value["version"], field="strategy.version"),
        "git_commit": _git_commit(value["git_commit"], field="strategy.git_commit"),
        "parameters": params,
    }


def _universe(value: Any) -> dict[str, Any]:
    if not isinstance(value, Mapping) or set(value) != {"universe_id", "version", "security_ids", "membership_fingerprint"}:
        raise BacktestSpecError("universe has an invalid field set")
    security_ids = value["security_ids"]
    if not isinstance(security_ids, list) or not security_ids:
        raise BacktestSpecError("universe.security_ids must be a non-empty list")
    normalized = [_uuid(item, field=f"universe.security_ids[{index}]") for index, item in enumerate(security_ids)]
    if len(set(normalized)) != len(normalized):
        raise BacktestSpecError("universe.security_ids must be unique")
    return {
        "universe_id": _uuid(value["universe_id"], field="universe.universe_id"),
        "version": _semver(value["version"], field="universe.version"),
        "security_ids": sorted(normalized),
        "membership_fingerprint": _sha256(value["membership_fingerprint"], field="universe.membership_fingerprint"),
    }


def _policy(value: Any, *, name: str, required: set[str]) -> dict[str, Any]:
    if not isinstance(value, Mapping) or set(value) != required:
        raise BacktestSpecError(f"{name} has an invalid field set")
    return dict(value)


def _decision_policy(value: Any) -> dict[str, Any]:
    result = _policy(
        value,
        name="decision_policy",
        required={"name", "frequency", "signal_delay_sessions", "execution_price"},
    )
    if result != {
        "name": "after_close_next_session_open",
        "frequency": "daily",
        "signal_delay_sessions": 1,
        "execution_price": "open",
    }:
        raise BacktestSpecError("only after_close_next_session_open is supported")
    return result


def _accounting_policy(value: Any) -> dict[str, Any]:
    result = _policy(
        value,
        name="accounting_policy",
        required={"base_currency", "reporting_currency", "initial_cash", "fractional_shares", "rebalance_frequency"},
    )
    result["base_currency"] = _currency(result["base_currency"], field="accounting_policy.base_currency")
    if result["reporting_currency"] is not None:
        result["reporting_currency"] = _currency(result["reporting_currency"], field="accounting_policy.reporting_currency")
    result["initial_cash"] = _decimal(result["initial_cash"], field="accounting_policy.initial_cash", positive=True)
    if not isinstance(result["fractional_shares"], bool):
        raise BacktestSpecError("accounting_policy.fractional_shares must be boolean")
    if result["rebalance_frequency"] not in {"once", "daily", "monthly"}:
        raise BacktestSpecError("accounting_policy.rebalance_frequency is unsupported")
    return result


def _cost_policy(value: Any) -> dict[str, Any]:
    result = _policy(
        value,
        name="cost_policy",
        required={"version", "commission_bps", "fixed_fee", "minimum_fee", "spread_bps", "slippage_bps", "tax_bps"},
    )
    result["version"] = _semver(result["version"], field="cost_policy.version")
    for key in ("commission_bps", "fixed_fee", "minimum_fee", "spread_bps", "slippage_bps", "tax_bps"):
        result[key] = _decimal(result[key], field=f"cost_policy.{key}", non_negative=True)
    return result


def _risk_policy(value: Any) -> dict[str, Any]:
    result = _policy(
        value,
        name="risk_policy",
        required={"max_gross_exposure", "max_position_weight", "max_participation"},
    )
    for key in result:
        result[key] = _decimal(result[key], field=f"risk_policy.{key}", non_negative=True)
    if Decimal(result["max_gross_exposure"]) <= 0 or Decimal(result["max_position_weight"]) <= 0:
        raise BacktestSpecError("risk exposure limits must be positive")
    if Decimal(result["max_position_weight"]) > Decimal(result["max_gross_exposure"]):
        raise BacktestSpecError("risk_policy.max_position_weight exceeds max_gross_exposure")
    if Decimal(result["max_participation"]) > 1:
        raise BacktestSpecError("risk_policy.max_participation must not exceed 1")
    return result


def _partitions(value: Any, *, period: Mapping[str, str]) -> list[dict[str, str]]:
    if not isinstance(value, list) or len(value) < 3:
        raise BacktestSpecError("partitions must contain development, validation, and holdout")
    result: list[dict[str, str]] = []
    names: set[str] = set()
    kinds: set[str] = set()
    for index, item in enumerate(value):
        if not isinstance(item, Mapping) or set(item) != {"name", "kind", "start_date", "end_date"}:
            raise BacktestSpecError(f"partitions[{index}] has an invalid field set")
        name = _nonempty(item["name"], field=f"partitions[{index}].name")
        if not re.fullmatch(r"^[a-z][a-z0-9_-]{1,63}$", name) or name in names:
            raise BacktestSpecError(f"partitions[{index}].name is invalid or duplicated")
        kind = item["kind"]
        if kind not in _PARTITION_KINDS or kind in kinds:
            raise BacktestSpecError(f"partitions[{index}].kind must be unique and supported")
        current = _date_range(
            {"start_date": item["start_date"], "end_date": item["end_date"]},
            field=f"partitions[{index}]",
        )
        if current["start_date"] < period["start_date"] or current["end_date"] > period["end_date"]:
            raise BacktestSpecError(f"partitions[{index}] must lie inside period")
        names.add(name)
        kinds.add(kind)
        result.append({"name": name, "kind": kind, **current})
    if kinds != set(_PARTITION_KINDS):
        raise BacktestSpecError("partitions must include each development, validation, and holdout kind")
    ordered = sorted(result, key=lambda item: item["start_date"])
    for previous, current in pairwise(ordered):
        if current["start_date"] <= previous["end_date"]:
            raise BacktestSpecError("partitions must not overlap")
    kind_order = {kind: index for index, kind in enumerate(_PARTITION_KINDS)}
    if [kind_order[item["kind"]] for item in ordered] != sorted(kind_order[item["kind"]] for item in ordered):
        raise BacktestSpecError("partitions must progress development, validation, then holdout")
    return ordered


def _walk_forward_windows(value: Any, *, period: Mapping[str, str]) -> list[dict[str, Any]]:
    if value is None:
        return []
    if not isinstance(value, list):
        raise BacktestSpecError("walk_forward_windows must be a list")
    result: list[dict[str, Any]] = []
    names: set[str] = set()
    for index, item in enumerate(value):
        if not isinstance(item, Mapping) or set(item) != {"name", "train", "calibration", "test"}:
            raise BacktestSpecError(f"walk_forward_windows[{index}] has an invalid field set")
        name = _nonempty(item["name"], field=f"walk_forward_windows[{index}].name")
        if not re.fullmatch(r"^[a-z][a-z0-9_-]{1,63}$", name) or name in names:
            raise BacktestSpecError(f"walk_forward_windows[{index}].name is invalid or duplicated")
        train = _date_range(item["train"], field=f"walk_forward_windows[{index}].train")
        calibration = _date_range(item["calibration"], field=f"walk_forward_windows[{index}].calibration")
        test = _date_range(item["test"], field=f"walk_forward_windows[{index}].test")
        if not (train["end_date"] < calibration["start_date"] <= calibration["end_date"] < test["start_date"]):
            raise BacktestSpecError(f"walk_forward_windows[{index}] intervals must be chronological and disjoint")
        if train["start_date"] < period["start_date"] or test["end_date"] > period["end_date"]:
            raise BacktestSpecError(f"walk_forward_windows[{index}] must lie inside period")
        names.add(name)
        result.append({"name": name, "train": train, "calibration": calibration, "test": test})
    return result


def normalize_experiment_spec(value: Mapping[str, Any], *, require_id: bool = False) -> dict[str, Any]:
    if not isinstance(value, Mapping):
        raise BacktestSpecError("experiment specification must be an object")
    keys = set(value)
    allowed = _SPEC_FIELDS if require_id or "experiment_id" in keys else _SPEC_FIELDS - {"experiment_id"}
    if not keys.issubset(allowed):
        unknown = sorted(keys - allowed)
        raise BacktestSpecError(f"experiment specification has unknown fields: {', '.join(unknown)}")
    required = _SPEC_FIELDS - {"experiment_id", "walk_forward_windows", "random_seed"}
    missing = sorted(required - keys)
    if missing:
        raise BacktestSpecError(f"experiment specification is missing fields: {', '.join(missing)}")
    period = _date_range(value["period"], field="period")
    inputs = value["inputs"]
    if not isinstance(inputs, list) or len(inputs) < 3:
        raise BacktestSpecError("inputs must contain at least prices, calendar, and membership")
    normalized_inputs = [_artifact_ref(item, field=f"inputs[{index}]") for index, item in enumerate(inputs)]
    input_kinds = [item["kind"] for item in normalized_inputs]
    if len(set(input_kinds)) != len(input_kinds):
        raise BacktestSpecError("inputs must contain one artifact per kind")
    if not {"prices", "calendar", "membership"}.issubset(input_kinds):
        raise BacktestSpecError("inputs must include prices, calendar, and membership artifacts")
    normalized_inputs.sort(key=lambda item: (item["kind"], item["artifact_id"]))
    benchmark = _policy(value["benchmark"], name="benchmark", required={"security_id", "currency"})
    benchmark = {
        "security_id": _uuid(benchmark["security_id"], field="benchmark.security_id"),
        "currency": _currency(benchmark["currency"], field="benchmark.currency"),
    }
    normalized: dict[str, Any] = {
        "schema_version": value.get("schema_version", SCHEMA_VERSION),
        "strategy": _strategy(value["strategy"]),
        "period": period,
        "universe": _universe(value["universe"]),
        "inputs": normalized_inputs,
        "benchmark": benchmark,
        "decision_policy": _decision_policy(value["decision_policy"]),
        "accounting_policy": _accounting_policy(value["accounting_policy"]),
        "cost_policy": _cost_policy(value["cost_policy"]),
        "risk_policy": _risk_policy(value["risk_policy"]),
        "partitions": _partitions(value["partitions"], period=period),
        "walk_forward_windows": _walk_forward_windows(value.get("walk_forward_windows"), period=period),
        "missing_data_policy": value["missing_data_policy"],
        "random_seed": value.get("random_seed"),
    }
    if normalized["schema_version"] != SCHEMA_VERSION:
        raise BacktestSpecError(f"unsupported experiment schema_version {normalized['schema_version']!r}")
    if normalized["missing_data_policy"] not in {"reject_trade", "halt_experiment"}:
        raise BacktestSpecError("missing_data_policy is unsupported")
    seed = normalized["random_seed"]
    if seed is not None and (not isinstance(seed, int) or isinstance(seed, bool) or seed < 0):
        raise BacktestSpecError("random_seed must be a non-negative integer or null")
    if "experiment_id" in value:
        normalized["experiment_id"] = _uuid(value["experiment_id"], field="experiment_id")
    elif require_id:
        raise BacktestSpecError("experiment_id is required")
    identity = dict(normalized)
    identity.pop("experiment_id", None)
    expected_id = str(uuid5(_EXPERIMENT_NAMESPACE, canonical_json(identity).decode("utf-8")))
    if "experiment_id" in normalized and normalized["experiment_id"] != expected_id:
        raise BacktestSpecError("experiment_id does not match the canonical specification")
    normalized["experiment_id"] = expected_id
    return normalized


def build_experiment_spec(value: Mapping[str, Any]) -> dict[str, Any]:
    """Normalize and deterministically identify one experiment specification."""

    return normalize_experiment_spec(value)


def validate_experiment_spec(value: Mapping[str, Any]) -> dict[str, Any]:
    """Validate a persisted specification, including its deterministic identity."""

    try:
        return normalize_experiment_spec(value, require_id=True)
    except BacktestSpecError as error:
        raise BacktestSpecValidationError(str(error)) from error


def experiment_sha256(spec: Mapping[str, Any]) -> str:
    normalized = validate_experiment_spec(spec)
    return sha256_bytes(canonical_json(normalized) + b"\n")


def input_fingerprint(spec: Mapping[str, Any]) -> str:
    normalized = validate_experiment_spec(spec)
    return sha256_bytes(canonical_json(normalized["inputs"]))


def _write_immutable(path: Path, content: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.exists():
        try:
            existing = path.read_bytes()
        except OSError as error:
            raise BacktestSpecConflictError(f"cannot read existing experiment {path}: {error}") from error
        if existing != content:
            raise BacktestSpecConflictError(f"experiment identity already contains different bytes: {path}")
        return
    try:
        path.write_bytes(content)
    except OSError as error:
        raise BacktestSpecError(f"cannot publish experiment {path}: {error}") from error


def write_experiment_spec(spec: Mapping[str, Any], *, experiments_root: str | Path) -> Path:
    normalized = validate_experiment_spec(spec)
    path = Path(experiments_root).expanduser().resolve() / f"experiment-{normalized['experiment_id']}" / "spec.json"
    _write_immutable(path, canonical_json(normalized) + b"\n")
    return path


def read_experiment_spec(path: str | Path) -> ValidatedExperimentSpec:
    manifest_path = Path(path).expanduser().resolve()
    try:
        spec = validate_experiment_spec(_strict_json(manifest_path))
    except BacktestSpecError as error:
        if isinstance(error, BacktestSpecValidationError):
            raise
        raise BacktestSpecValidationError(str(error)) from error
    return ValidatedExperimentSpec(manifest_path, spec, sha256_file(manifest_path))


__all__ = [
    "ENGINE_VERSION",
    "SCHEMA_VERSION",
    "BacktestSpecConflictError",
    "BacktestSpecError",
    "BacktestSpecValidationError",
    "ValidatedExperimentSpec",
    "build_experiment_spec",
    "canonical_json",
    "experiment_sha256",
    "input_fingerprint",
    "normalize_experiment_spec",
    "read_experiment_spec",
    "sha256_bytes",
    "sha256_file",
    "validate_experiment_spec",
    "write_experiment_spec",
]
