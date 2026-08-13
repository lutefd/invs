"""Deterministic ``market-basic`` feature artifacts.

This module deliberately owns only the first, closed feature registry.  Input
selection remains the responsibility of :class:`ResearchCatalog`; this slice
uses its point-in-time input API and only reads the selected rows' lineage for
the high, low, and volume values needed by the registry.
"""

from __future__ import annotations

import hashlib
import json
import re
from collections.abc import Mapping
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from decimal import Decimal, InvalidOperation, localcontext
from pathlib import Path
from typing import Any, Final
from uuid import UUID, uuid5

from .catalog import ResearchCatalog

FEATURE_SET: Final[str] = "market-basic"
FEATURE_SET_VERSION: Final[str] = "1.0.0"
SCHEMA_VERSION: Final[str] = "1.0.0"
MANIFEST_VERSION: Final[str] = "1.0.0"
FEATURE_NAMES: Final[tuple[str, ...]] = ("close", "return_1d", "range_1d", "volume")
DEFAULT_GENERATOR_VERSION: Final[str] = "python-market-basic-1.0.0"

_DEFAULT_FEATURE_ROOT = Path("data/features")
_ARTIFACT_NAMESPACE = UUID("2e9fcd7f-7ed1-5c65-bdf3-49b8f0b19c72")
_SHA256_PATTERN = re.compile(r"^[0-9a-f]{64}$")
_DECIMAL_PATTERN = re.compile(r"^-?(0|[1-9][0-9]*)(\.[0-9]+)?$")
_PART_PATTERN = re.compile(r"^part-([0-9a-f]{64})\.parquet$")
_UTC_TIMESTAMP_PATTERN = re.compile(
    r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?Z$"
)

_MANIFEST_FIELDS = frozenset(
    {
        "schema_version",
        "manifest_version",
        "feature_set",
        "feature_set_version",
        "feature_names",
        "artifact",
        "decision_at",
        "input_available_at",
        "computation_delay_seconds",
        "available_at",
        "input_fingerprint",
        "selected_input_manifests",
        "selected_input_parts",
        "row_count",
        "parts",
    }
)
_ARTIFACT_FIELDS = frozenset(
    {"artifact_id", "artifact_version", "generator_version", "git_commit", "created_at"}
)
_SELECTED_MANIFEST_FIELDS = frozenset({"path", "sha256"})
_SELECTED_PART_FIELDS = frozenset({"path", "sha256"})
_OUTPUT_PART_FIELDS = frozenset({"path", "sha256", "row_count"})
_OBSERVATION_FIELDS = frozenset(
    {
        "schema_version",
        "feature_set",
        "feature_set_version",
        "security_id",
        "decision_at",
        "input_available_at",
        "computation_delay_seconds",
        "available_at",
        "input_fingerprint",
        "artifact",
        "features",
    }
)


class FeatureArtifactError(ValueError):
    """Base error for invalid, unsupported, or unsafe feature artifacts."""


class FeatureArtifactConflictError(FeatureArtifactError):
    """Raised when an immutable artifact identity already has other content."""


class FeatureArtifactValidationError(FeatureArtifactError):
    """Raised when a manifest or one of its listed parts fails validation."""


@dataclass(frozen=True)
class ValidatedFeatureArtifact:
    """A validated manifest and the observations from its listed parts."""

    manifest_path: Path
    manifest: dict[str, Any]
    observations: tuple[dict[str, Any], ...]
    part_paths: tuple[Path, ...]

    @property
    def row_count(self) -> int:
        return len(self.observations)

    def __getitem__(self, key: str) -> Any:
        """Offer a small mapping-like convenience for callers and tests."""
        if key == "manifest":
            return self.manifest
        if key in {"observations", "rows"}:
            return self.observations
        if key == "manifest_path":
            return self.manifest_path
        raise KeyError(key)


def _reject_json_constant(value: str) -> Any:
    raise ValueError(f"invalid JSON constant {value}")


def _json_object_without_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    document: dict[str, Any] = {}
    for key, value in pairs:
        if key in document:
            raise ValueError(f"duplicate JSON key {key!r}")
        document[key] = value
    return document


def _read_json(path: Path, *, label: str) -> dict[str, Any]:
    try:
        document = json.loads(
            path.read_text(encoding="utf-8"),
            object_pairs_hook=_json_object_without_duplicates,
            parse_constant=_reject_json_constant,
        )
    except (OSError, UnicodeError, ValueError) as error:
        raise FeatureArtifactValidationError(f"invalid {label} {path}: {error}") from error
    if not isinstance(document, dict):
        raise FeatureArtifactValidationError(f"invalid {label} {path}: expected a JSON object")
    return document


def _sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def _sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    try:
        with path.open("rb") as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(chunk)
    except OSError as error:
        raise FeatureArtifactError(f"cannot read input file {path}: {error}") from error
    return digest.hexdigest()


def _canonical_timestamp(value: str | datetime | Any, *, field: str) -> tuple[str, datetime]:
    """Return the contract timestamp and its aware UTC datetime."""
    candidate = value
    if hasattr(candidate, "to_pydatetime"):
        candidate = candidate.to_pydatetime(warn=False)
    if isinstance(candidate, datetime):
        parsed = candidate
    elif isinstance(candidate, str):
        text = candidate.strip()
        if text.endswith("Z"):
            text = text[:-1] + "+00:00"
        try:
            parsed = datetime.fromisoformat(text)
        except ValueError as error:
            raise FeatureArtifactError(f"{field} must be an RFC 3339 UTC timestamp") from error
    else:
        raise FeatureArtifactError(f"{field} must be an RFC 3339 UTC timestamp")
    if parsed.tzinfo is None or parsed.utcoffset() is None:
        raise FeatureArtifactError(f"{field} must include an explicit timezone")
    utc_value = parsed.astimezone(UTC)
    if utc_value.microsecond:
        fraction = f".{utc_value.microsecond:06d}".rstrip("0")
    else:
        fraction = ""
    canonical = utc_value.strftime("%Y-%m-%dT%H:%M:%S") + fraction + "Z"
    if not _UTC_TIMESTAMP_PATTERN.fullmatch(canonical):
        raise FeatureArtifactError(f"{field} is outside the supported timestamp contract")
    return canonical, utc_value


def _canonical_uuid(value: str | UUID, *, field: str) -> str:
    if isinstance(value, UUID):
        return str(value)
    if not isinstance(value, str) or not value:
        raise FeatureArtifactError(f"{field} must be a UUID string")
    try:
        parsed = UUID(value)
    except (ValueError, AttributeError) as error:
        raise FeatureArtifactError(f"{field} must be a UUID string") from error
    return str(parsed)


def _validate_sha256(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not _SHA256_PATTERN.fullmatch(value):
        raise FeatureArtifactValidationError(f"{field} must be a lower-case SHA-256")
    return value


def _validate_decimal_string(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not _DECIMAL_PATTERN.fullmatch(value):
        raise FeatureArtifactValidationError(
            f"{field} must be an exact canonical decimal string or null"
        )
    return value


def _decimal(value: Any, *, field: str) -> Decimal:
    if not isinstance(value, str) or not _DECIMAL_PATTERN.fullmatch(value):
        raise FeatureArtifactError(f"{field} must be a canonical decimal string")
    try:
        return Decimal(value)
    except InvalidOperation as error:
        raise FeatureArtifactError(f"{field} is not a valid decimal") from error


def _decimal_string(value: Decimal, *, field: str) -> str:
    if not value.is_finite():
        raise FeatureArtifactError(f"{field} produced a non-finite decimal")
    if value.is_zero():
        return "0"
    result = format(value, "f")
    if not _DECIMAL_PATTERN.fullmatch(result):
        raise FeatureArtifactError(f"{field} produced a non-canonical decimal {result!r}")
    return result


def _canonical_json(value: Mapping[str, Any]) -> bytes:
    try:
        return json.dumps(
            value,
            ensure_ascii=False,
            sort_keys=True,
            separators=(",", ":"),
            allow_nan=False,
        ).encode("utf-8")
    except (TypeError, ValueError) as error:
        raise FeatureArtifactError(f"cannot canonicalize feature input envelope: {error}") from error


def compute_input_fingerprint(manifest: Mapping[str, Any]) -> str:
    """Compute the ADR 0005 SHA-256 input-selection fingerprint.

    Only the six fields in the ADR envelope participate.  The two lineage
    arrays are sorted by ``(path, sha256)`` before canonical JSON encoding.
    """
    required = {
        "decision_at",
        "feature_set",
        "feature_set_version",
        "selected_input_manifests",
        "selected_input_parts",
    }
    missing = sorted(required - manifest.keys())
    if missing:
        raise FeatureArtifactError(
            f"input fingerprint envelope is missing field(s): {', '.join(missing)}"
        )
    envelope = {
        "decision_at": manifest["decision_at"],
        "feature_set": manifest["feature_set"],
        "feature_set_version": manifest["feature_set_version"],
        "selected_input_manifests": sorted(
            manifest["selected_input_manifests"],
            key=lambda item: (item["path"], item["sha256"]),
        ),
        "selected_input_parts": sorted(
            manifest["selected_input_parts"],
            key=lambda item: (item["path"], item["sha256"]),
        ),
    }
    return _sha256_bytes(_canonical_json(envelope))


def input_fingerprint(manifest: Mapping[str, Any]) -> str:
    """Compatibility spelling for :func:`compute_input_fingerprint`."""
    return compute_input_fingerprint(manifest)


def _validate_delay(value: Any) -> int:
    if not isinstance(value, int) or isinstance(value, bool) or value < 0:
        raise FeatureArtifactError("computation_delay_seconds must be a non-negative integer")
    return value


def _strict_frame_records(frame: Any) -> list[dict[str, Any]]:
    if hasattr(frame, "to_dict"):
        try:
            records = frame.to_dict(orient="records")
        except TypeError:
            records = frame.to_dict("records")
    elif isinstance(frame, list):
        records = frame
    else:
        raise FeatureArtifactError("point_in_time_inputs returned an unsupported frame")
    if not isinstance(records, list) or any(not isinstance(item, dict) for item in records):
        raise FeatureArtifactError("point_in_time_inputs returned invalid rows")
    return records


def _record_value(record: Mapping[str, Any], field: str) -> Any:
    if field not in record:
        raise FeatureArtifactError(f"point_in_time_inputs row is missing {field!r}")
    value = record[field]
    if value is None:
        return None
    return value


def _duckdb_timestamp(value: Any, *, field: str) -> datetime:
    _, parsed = _canonical_timestamp(value, field=field)
    return parsed


def _relative_input_path(path: Path, *, data_root: Path) -> str:
    resolved_root = data_root.resolve()
    resolved_path = path.resolve()
    try:
        relative = resolved_path.relative_to(resolved_root)
    except ValueError as error:
        raise FeatureArtifactError(
            f"selected input manifest {path} is outside catalog data root {data_root}"
        ) from error
    if not relative.parts or any(part in {"", ".", ".."} for part in relative.parts):
        raise FeatureArtifactError(f"selected input manifest {path} is not safely relative")
    return relative.as_posix()


def _validate_selected_manifest_membership(
    manifest_path: Path,
    selected_parts: set[tuple[str, str]],
) -> None:
    document = _read_json(manifest_path, label="selected input manifest")
    parts = document.get("parts")
    if not isinstance(parts, list):
        raise FeatureArtifactError(f"selected input manifest {manifest_path} has no parts list")
    listed = set()
    for item in parts:
        if not isinstance(item, dict):
            raise FeatureArtifactError(f"selected input manifest {manifest_path} has an invalid part")
        part_path = item.get("path")
        part_sha = item.get("sha256")
        if isinstance(part_path, str) and isinstance(part_sha, str):
            listed.add((part_path, part_sha))
    if not selected_parts <= listed:
        missing = sorted(selected_parts - listed)
        raise FeatureArtifactError(
            f"selected input manifest {manifest_path} does not list selected part(s): {missing}"
        )


def _selected_lineage(
    catalog: ResearchCatalog,
    records: list[Mapping[str, Any]],
) -> tuple[list[dict[str, str]], list[dict[str, str]]]:
    data_root = Path(catalog.data_root).resolve()
    manifest_records: dict[str, dict[str, str]] = {}
    part_records: dict[tuple[str, str], dict[str, str]] = {}
    parts_by_manifest: dict[Path, set[tuple[str, str]]] = {}

    for index, record in enumerate(records):
        manifest_raw = _record_value(record, "manifest_path")
        part_raw = _record_value(record, "part_path")
        part_sha = _record_value(record, "part_sha256")
        if not all(isinstance(value, str) for value in (manifest_raw, part_raw, part_sha)):
            raise FeatureArtifactError(f"selected input row {index} has invalid lineage")
        manifest_path = Path(manifest_raw).resolve()
        part_path = Path(part_raw).resolve()
        if not manifest_path.is_file() or not part_path.is_file():
            raise FeatureArtifactError(f"selected input row {index} points to a missing lineage file")
        if part_path.parent != manifest_path.parent:
            raise FeatureArtifactError(
                f"selected input row {index} part is not beside its manifest"
            )
        _validate_sha256(part_sha, field=f"selected input row {index}.part_sha256")
        actual_part_sha = _sha256_file(part_path)
        if actual_part_sha != part_sha:
            raise FeatureArtifactError(
                f"selected input part {part_path} hash mismatch: expected {part_sha}, "
                f"got {actual_part_sha}"
            )
        part_name = part_path.name
        match = _PART_PATTERN.fullmatch(part_name)
        if match is None or match.group(1) != part_sha:
            raise FeatureArtifactError(f"selected input part {part_path} is not content-named")

        manifest_name = _relative_input_path(manifest_path, data_root=data_root)
        manifest_sha = _sha256_file(manifest_path)
        manifest_records[manifest_name] = {"path": manifest_name, "sha256": manifest_sha}
        part_key = (part_name, part_sha)
        part_records[part_key] = {"path": part_name, "sha256": part_sha}
        parts_by_manifest.setdefault(manifest_path, set()).add(part_key)

    for manifest_path, selected_parts in parts_by_manifest.items():
        _validate_selected_manifest_membership(manifest_path, selected_parts)

    selected_manifests = sorted(
        manifest_records.values(), key=lambda item: (item["path"], item["sha256"])
    )
    selected_parts = sorted(part_records.values(), key=lambda item: (item["path"], item["sha256"]))
    if not selected_manifests or not selected_parts:
        raise FeatureArtifactError("an artifact requires at least one selected input manifest and part")
    return selected_manifests, selected_parts


def _price_details(
    catalog: ResearchCatalog, record: Mapping[str, Any]
) -> tuple[Any, Any, Any, bool]:
    """Read high/low/volume for one row already selected by the catalog."""
    if {"high", "low", "volume", "has_volume"} <= record.keys():
        high = _record_value(record, "high")
        low = _record_value(record, "low")
        volume = _record_value(record, "volume")
        has_volume = _record_value(record, "has_volume")
        if not isinstance(has_volume, bool):
            raise FeatureArtifactError("selected price lineage has an invalid has_volume flag")
        return high, low, volume, has_volume
    fields = (
        "source",
        "security_id",
        "interval",
        "price_basis",
        "observed_at",
        "available_at",
        "ingested_at",
        "raw_payload_hash",
        "raw_record_locator",
        "manifest_path",
        "part_path",
        "part_sha256",
    )
    values = [_record_value(record, field) for field in fields]
    query = """
        SELECT high, low, volume, has_volume
        FROM "_prices_lineage"
        WHERE source = ?
          AND security_id = ?
          AND interval = ?
          AND price_basis = ?
          AND observed_at = ?
          AND available_at = ?
          AND ingested_at IS NOT DISTINCT FROM ?
          AND raw_payload_hash IS NOT DISTINCT FROM ?
          AND raw_record_locator IS NOT DISTINCT FROM ?
          AND manifest_path = ?
          AND part_path = ?
          AND part_sha256 = ?
    """
    parameters = values[:4]
    parameters.extend(
        [
            _duckdb_timestamp(values[4], field="observed_at"),
            _duckdb_timestamp(values[5], field="available_at"),
            None if values[6] is None else _duckdb_timestamp(values[6], field="ingested_at"),
            values[7],
            values[8],
            values[9],
            values[10],
            values[11],
        ]
    )
    try:
        rows = catalog.connection.execute(query, parameters).fetchall()
    except Exception as error:
        raise FeatureArtifactError(f"cannot read selected price lineage: {error}") from error
    if len(rows) != 1:
        raise FeatureArtifactError(
            f"selected price lineage resolved to {len(rows)} rows; expected exactly one"
        )
    high, low, volume, has_volume = rows[0]
    if not isinstance(has_volume, bool):
        raise FeatureArtifactError("selected price lineage has an invalid has_volume flag")
    return high, low, volume, has_volume


def _compute_features(
    catalog: ResearchCatalog,
    records: list[Mapping[str, Any]],
) -> dict[str, str | None]:
    if not records:
        raise FeatureArtifactError("no price rows are eligible at the requested decision_at")
    for index, record in enumerate(records):
        interval = _record_value(record, "interval")
        if interval != "1d":
            raise FeatureArtifactError(
                f"market-basic requires daily price bars; row {index} has interval {interval!r}"
            )

    series_keys = {
        (
            _record_value(record, "source"),
            _record_value(record, "interval"),
            _record_value(record, "price_basis"),
            _record_value(record, "currency"),
        )
        for record in records
    }
    if len(series_keys) != 1:
        raise FeatureArtifactError(
            "market-basic requires one deterministic source/interval/price-basis/currency series"
        )
    ordered = sorted(
        records,
        key=lambda record: (
            _duckdb_timestamp(_record_value(record, "observed_at"), field="observed_at"),
            str(_record_value(record, "source")),
            str(_record_value(record, "price_basis")),
            _duckdb_timestamp(_record_value(record, "available_at"), field="available_at"),
            str(_record_value(record, "raw_payload_hash")),
            str(_record_value(record, "raw_record_locator")),
        ),
    )
    current = ordered[-1]
    previous = ordered[-2] if len(ordered) >= 2 else None

    current_close = _decimal(_record_value(current, "close_value"), field="close")
    current_high, current_low, current_volume, has_volume = _price_details(catalog, current)
    range_value: str | None
    if current_high is None or current_low is None:
        range_value = None
    else:
        high = _decimal(current_high, field="high")
        low = _decimal(current_low, field="low")
        if high < low:
            raise FeatureArtifactError("selected price bar has high below low")
        range_value = _decimal_string(high - low, field="range_1d")

    return_value: str | None = None
    if previous is not None:
        previous_close_raw = _record_value(previous, "close_value")
        if previous_close_raw is not None:
            previous_close = _decimal(previous_close_raw, field="previous close")
            if previous_close.is_zero():
                raise FeatureArtifactError("return_1d has a zero previous close")
            with localcontext() as context:
                context.prec = max(128, len(current_close.as_tuple().digits) * 2 + 32)
                return_value = _decimal_string(
                    current_close / previous_close - Decimal(1), field="return_1d"
                )

    volume_value: str | None
    if not has_volume or current_volume is None:
        volume_value = None
    else:
        volume_value = _decimal_string(_decimal(current_volume, field="volume"), field="volume")

    return {
        "close": _decimal_string(current_close, field="close"),
        "return_1d": return_value,
        "range_1d": range_value,
        "volume": volume_value,
    }


@dataclass(frozen=True)
class _Selection:
    decision_at: str
    security_id: str
    input_available_at: str
    available_at: str
    computation_delay_seconds: int
    input_fingerprint: str
    selected_input_manifests: list[dict[str, str]]
    selected_input_parts: list[dict[str, str]]
    features: dict[str, str | None]


def _select(
    catalog: ResearchCatalog,
    *,
    decision_at: str | datetime,
    security_id: str | UUID,
    computation_delay_seconds: int,
    feature_set: str,
    feature_set_version: str,
) -> _Selection:
    if feature_set != FEATURE_SET:
        raise FeatureArtifactError(f"unsupported feature_set {feature_set!r}")
    if feature_set_version != FEATURE_SET_VERSION:
        raise FeatureArtifactError(f"unsupported feature_set_version {feature_set_version!r}")
    delay = _validate_delay(computation_delay_seconds)
    canonical_decision, decision_datetime = _canonical_timestamp(decision_at, field="decision_at")
    canonical_security_id = _canonical_uuid(security_id, field="security_id")
    try:
        inputs = catalog.point_in_time_inputs(
            decision_at=canonical_decision,
            security_id=canonical_security_id,
            dataset="prices",
        )
    except Exception as error:
        if isinstance(error, FeatureArtifactError):
            raise
        raise FeatureArtifactError(f"point-in-time price selection failed: {error}") from error
    records = _strict_frame_records(inputs.frame)
    if not records:
        raise FeatureArtifactError("no price rows are eligible at the requested decision_at")
    available_datetimes = [
        _duckdb_timestamp(_record_value(record, "available_at"), field="available_at")
        for record in records
    ]
    observed_datetimes = [
        _duckdb_timestamp(_record_value(record, "observed_at"), field="observed_at")
        for record in records
    ]
    if any(value > decision_datetime for value in available_datetimes):
        raise FeatureArtifactError("point-in-time input selection returned a future available_at")
    if any(value > decision_datetime for value in observed_datetimes):
        raise FeatureArtifactError("point-in-time input selection returned a future observed_at")
    input_available_datetime = max(available_datetimes)
    input_available_at = _canonical_timestamp(
        input_available_datetime, field="input_available_at"
    )[0]
    available_datetime = input_available_datetime + timedelta(seconds=delay)
    available_at = _canonical_timestamp(available_datetime, field="available_at")[0]
    selected_input_manifests, selected_input_parts = _selected_lineage(catalog, records)
    fingerprint_envelope = {
        "decision_at": canonical_decision,
        "feature_set": FEATURE_SET,
        "feature_set_version": FEATURE_SET_VERSION,
        "selected_input_manifests": selected_input_manifests,
        "selected_input_parts": selected_input_parts,
    }
    fingerprint = compute_input_fingerprint(fingerprint_envelope)
    return _Selection(
        decision_at=canonical_decision,
        security_id=canonical_security_id,
        input_available_at=input_available_at,
        available_at=available_at,
        computation_delay_seconds=delay,
        input_fingerprint=fingerprint,
        selected_input_manifests=selected_input_manifests,
        selected_input_parts=selected_input_parts,
        features=_compute_features(catalog, records),
    )


def compute_market_basic_features(
    catalog: ResearchCatalog,
    *,
    decision_at: str | datetime,
    security_id: str | UUID,
) -> dict[str, str | None]:
    """Compute the closed market-basic registry for one point-in-time security."""
    return _select(
        catalog,
        decision_at=decision_at,
        security_id=security_id,
        computation_delay_seconds=0,
        feature_set=FEATURE_SET,
        feature_set_version=FEATURE_SET_VERSION,
    ).features


def _artifact_id(security_id: str, decision_at: str) -> str:
    return str(
        uuid5(_ARTIFACT_NAMESPACE, f"{FEATURE_SET}:{FEATURE_SET_VERSION}:{security_id}:{decision_at}")
    )


def _artifact_directory(features_root: Path, artifact_id: str) -> Path:
    return features_root / FEATURE_SET / FEATURE_SET_VERSION / f"artifact-{artifact_id}"


def _validate_artifact_metadata(artifact: Any, *, label: str) -> dict[str, Any]:
    if not isinstance(artifact, dict):
        raise FeatureArtifactValidationError(f"{label} must be an object")
    if set(artifact) != _ARTIFACT_FIELDS:
        raise FeatureArtifactValidationError(f"{label} has an unsupported field set")
    artifact_id = artifact["artifact_id"]
    try:
        canonical_id = _canonical_uuid(artifact_id, field=f"{label}.artifact_id")
    except FeatureArtifactError as error:
        raise FeatureArtifactValidationError(str(error)) from error
    if artifact_id != canonical_id:
        raise FeatureArtifactValidationError(f"{label}.artifact_id must be lower-case canonical UUID")
    if artifact["artifact_version"] != FEATURE_SET_VERSION:
        raise FeatureArtifactValidationError(
            f"{label}.artifact_version must be {FEATURE_SET_VERSION!r}"
        )
    if not isinstance(artifact["generator_version"], str) or not artifact["generator_version"]:
        raise FeatureArtifactValidationError(f"{label}.generator_version must be non-empty")
    git_commit = artifact["git_commit"]
    if not isinstance(git_commit, str) or not (
        git_commit == "unknown" or re.fullmatch(r"[0-9a-f]{40}", git_commit)
    ):
        raise FeatureArtifactValidationError(f"{label}.git_commit is invalid")
    created_at, _ = _canonical_timestamp(artifact["created_at"], field=f"{label}.created_at")
    if artifact["created_at"] != created_at:
        raise FeatureArtifactValidationError(f"{label}.created_at is not canonical UTC")
    return artifact


def _validate_relative_path(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not value:
        raise FeatureArtifactValidationError(f"{field} must be a non-empty relative path")
    path = Path(value)
    if path.is_absolute() or "\\" in value or "\x00" in value:
        raise FeatureArtifactValidationError(f"{field} must be a relative path")
    if any(part in {"", ".", ".."} for part in path.parts):
        raise FeatureArtifactValidationError(f"{field} contains an unsafe path component")
    return value


def _validate_manifest(document: dict[str, Any], *, manifest_path: Path) -> dict[str, Any]:
    if set(document) != _MANIFEST_FIELDS:
        raise FeatureArtifactValidationError(
            f"manifest {manifest_path} has missing or unknown top-level fields"
        )
    if document["schema_version"] != SCHEMA_VERSION:
        raise FeatureArtifactValidationError(
            f"manifest schema_version must be {SCHEMA_VERSION!r}"
        )
    if document["manifest_version"] != MANIFEST_VERSION:
        raise FeatureArtifactValidationError(
            f"manifest manifest_version must be {MANIFEST_VERSION!r}"
        )
    if document["feature_set"] != FEATURE_SET:
        raise FeatureArtifactValidationError(f"unsupported feature_set {document['feature_set']!r}")
    if document["feature_set_version"] != FEATURE_SET_VERSION:
        raise FeatureArtifactValidationError(
            f"unsupported feature_set_version {document['feature_set_version']!r}"
        )
    if document["feature_names"] != list(FEATURE_NAMES):
        raise FeatureArtifactValidationError("manifest feature_names are not the closed registry")
    _validate_artifact_metadata(document["artifact"], label="manifest artifact")

    timestamps: dict[str, datetime] = {}
    for field in ("decision_at", "input_available_at", "available_at"):
        canonical, parsed = _canonical_timestamp(document[field], field=f"manifest.{field}")
        if document[field] != canonical:
            raise FeatureArtifactValidationError(f"manifest.{field} is not canonical UTC")
        timestamps[field] = parsed
    if timestamps["input_available_at"] > timestamps["decision_at"]:
        raise FeatureArtifactValidationError("manifest input_available_at is after decision_at")
    delay = document["computation_delay_seconds"]
    if not isinstance(delay, int) or isinstance(delay, bool) or delay < 0:
        raise FeatureArtifactValidationError("manifest computation_delay_seconds is invalid")
    if timestamps["available_at"] != timestamps["input_available_at"] + timedelta(seconds=delay):
        raise FeatureArtifactValidationError("manifest available_at violates the timing contract")
    _validate_sha256(document["input_fingerprint"], field="manifest.input_fingerprint")

    selected_manifests = document["selected_input_manifests"]
    if not isinstance(selected_manifests, list) or not selected_manifests:
        raise FeatureArtifactValidationError("manifest selected_input_manifests must not be empty")
    manifest_keys: set[tuple[str, str]] = set()
    for index, item in enumerate(selected_manifests):
        if not isinstance(item, dict) or set(item) != _SELECTED_MANIFEST_FIELDS:
            raise FeatureArtifactValidationError(f"selected_input_manifests[{index}] is invalid")
        path = _validate_relative_path(
            item["path"], field=f"selected_input_manifests[{index}].path"
        )
        sha256 = _validate_sha256(
            item["sha256"], field=f"selected_input_manifests[{index}].sha256"
        )
        key = (path, sha256)
        if key in manifest_keys:
            raise FeatureArtifactValidationError("manifest selected input manifests are not unique")
        manifest_keys.add(key)

    selected_parts = document["selected_input_parts"]
    if not isinstance(selected_parts, list) or not selected_parts:
        raise FeatureArtifactValidationError("manifest selected_input_parts must not be empty")
    part_keys: set[tuple[str, str]] = set()
    for index, item in enumerate(selected_parts):
        if not isinstance(item, dict) or set(item) != _SELECTED_PART_FIELDS:
            raise FeatureArtifactValidationError(f"selected_input_parts[{index}] is invalid")
        path = item["path"]
        sha256 = _validate_sha256(item["sha256"], field=f"selected_input_parts[{index}].sha256")
        if not isinstance(path, str) or _PART_PATTERN.fullmatch(path) is None:
            raise FeatureArtifactValidationError(f"selected_input_parts[{index}].path is invalid")
        if _PART_PATTERN.fullmatch(path).group(1) != sha256:
            raise FeatureArtifactValidationError(
                f"selected_input_parts[{index}] is not content-named"
            )
        key = (path, sha256)
        if key in part_keys:
            raise FeatureArtifactValidationError("manifest selected input parts are not unique")
        part_keys.add(key)

    if document["input_fingerprint"] != compute_input_fingerprint(document):
        raise FeatureArtifactValidationError("manifest input_fingerprint does not match ADR 0005")

    output_parts = document["parts"]
    if not isinstance(output_parts, list) or not output_parts:
        raise FeatureArtifactValidationError("manifest parts must not be empty")
    output_names: set[str] = set()
    output_rows = 0
    for index, item in enumerate(output_parts):
        if not isinstance(item, dict) or set(item) != _OUTPUT_PART_FIELDS:
            raise FeatureArtifactValidationError(f"manifest parts[{index}] is invalid")
        path = item["path"]
        sha256 = _validate_sha256(item["sha256"], field=f"manifest.parts[{index}].sha256")
        match = _PART_PATTERN.fullmatch(path) if isinstance(path, str) else None
        if match is None or match.group(1) != sha256:
            raise FeatureArtifactValidationError(f"manifest parts[{index}] is not content-named")
        row_count = item["row_count"]
        if not isinstance(row_count, int) or isinstance(row_count, bool) or row_count < 0:
            raise FeatureArtifactValidationError(f"manifest parts[{index}].row_count is invalid")
        if path in output_names:
            raise FeatureArtifactValidationError("manifest output parts are not unique")
        output_names.add(path)
        output_rows += row_count
    if document["row_count"] != output_rows:
        raise FeatureArtifactValidationError("manifest row_count does not equal output part rows")
    if not isinstance(document["row_count"], int) or isinstance(document["row_count"], bool):
        raise FeatureArtifactValidationError("manifest row_count is invalid")
    return document


def _pyarrow_schema() -> Any:
    import pyarrow as pa

    artifact_type = pa.struct(
        [
            pa.field("artifact_id", pa.string()),
            pa.field("artifact_version", pa.string()),
            pa.field("generator_version", pa.string()),
            pa.field("git_commit", pa.string()),
            pa.field("created_at", pa.string()),
        ]
    )
    feature_type = pa.struct([pa.field(name, pa.string()) for name in FEATURE_NAMES])
    return pa.schema(
        [
            pa.field("schema_version", pa.string()),
            pa.field("feature_set", pa.string()),
            pa.field("feature_set_version", pa.string()),
            pa.field("security_id", pa.string()),
            pa.field("decision_at", pa.string()),
            pa.field("input_available_at", pa.string()),
            pa.field("computation_delay_seconds", pa.int64()),
            pa.field("available_at", pa.string()),
            pa.field("input_fingerprint", pa.string()),
            pa.field("artifact", artifact_type),
            pa.field("features", feature_type),
        ]
    )


def _parquet_bytes(observation: dict[str, Any]) -> bytes:
    import pyarrow as pa
    from pyarrow import parquet

    table = pa.Table.from_pylist([observation], schema=_pyarrow_schema())
    sink = pa.BufferOutputStream()
    parquet.write_table(
        table,
        sink,
        compression="NONE",
        data_page_version="1.0",
        use_dictionary=False,
        write_statistics=False,
        version="2.6",
    )
    return sink.getvalue().to_pybytes()


def _validate_part_table(
    path: Path,
    *,
    manifest: Mapping[str, Any],
) -> list[dict[str, Any]]:
    from pyarrow import parquet

    try:
        table = parquet.read_table(path)
    except Exception as error:
        raise FeatureArtifactValidationError(f"cannot read feature part {path}: {error}") from error
    expected_schema = _pyarrow_schema()
    if table.column_names != expected_schema.names:
        raise FeatureArtifactValidationError(
            f"feature part {path} has an unsupported column set; DOUBLE or extra columns are not allowed"
        )
    for actual, expected in zip(table.schema, expected_schema, strict=True):
        if actual.type != expected.type:
            raise FeatureArtifactValidationError(
                f"feature part {path} field {actual.name!r} has type {actual.type}; "
                f"expected {expected.type}"
            )
    rows = table.to_pylist()
    result: list[dict[str, Any]] = []
    for index, row in enumerate(rows):
        if not isinstance(row, dict) or set(row) != _OBSERVATION_FIELDS:
            raise FeatureArtifactValidationError(f"feature part {path} row {index} has unknown fields")
        for field in (
            "schema_version",
            "feature_set",
            "feature_set_version",
            "decision_at",
            "input_available_at",
            "available_at",
            "input_fingerprint",
        ):
            if row[field] != manifest[field]:
                raise FeatureArtifactValidationError(
                    f"feature part {path} row {index} {field} disagrees with manifest"
                )
        if row["computation_delay_seconds"] != manifest["computation_delay_seconds"]:
            raise FeatureArtifactValidationError(
                f"feature part {path} row {index} computation delay disagrees with manifest"
            )
        try:
            row_security_id = _canonical_uuid(row["security_id"], field="observation.security_id")
        except FeatureArtifactError as error:
            raise FeatureArtifactValidationError(str(error)) from error
        if row["security_id"] != row_security_id:
            raise FeatureArtifactValidationError(f"feature part {path} row {index} has non-canonical security_id")
        if row["artifact"] != manifest["artifact"]:
            raise FeatureArtifactValidationError(
                f"feature part {path} row {index} artifact metadata disagrees with manifest"
            )
        features = row["features"]
        if not isinstance(features, dict) or set(features) != set(FEATURE_NAMES):
            raise FeatureArtifactValidationError(f"feature part {path} row {index} has unknown features")
        for name, value in features.items():
            if value is not None:
                _validate_decimal_string(value, field=f"feature part {path} row {index}.{name}")
        for field in ("decision_at", "input_available_at", "available_at"):
            canonical, _ = _canonical_timestamp(row[field], field=f"observation.{field}")
            if canonical != row[field]:
                raise FeatureArtifactValidationError(
                    f"feature part {path} row {index} {field} is not canonical UTC"
                )
        result.append(row)
    return result


def read_feature_artifact(manifest_path: str | Path) -> ValidatedFeatureArtifact:
    """Read and validate one manifest and exactly its listed Parquet parts.

    The reader never recursively discovers parts.  It also rejects unexpected
    files in the artifact directory so an unlisted part cannot silently become
    part of a later publication or validation result.
    """
    path = Path(manifest_path).expanduser().resolve()
    if path.name != "manifest.json" or not path.is_file():
        raise FeatureArtifactValidationError(f"feature manifest {path} does not exist")
    document = _validate_manifest(_read_json(path, label="feature manifest"), manifest_path=path)
    directory = path.parent.resolve()
    listed_names = {"manifest.json"}
    for item in document["parts"]:
        listed_names.add(item["path"])
    try:
        unexpected = sorted(
            entry.name for entry in directory.iterdir() if entry.name not in listed_names
        )
    except OSError as error:
        raise FeatureArtifactValidationError(f"cannot inspect feature artifact {directory}: {error}") from error
    if unexpected:
        raise FeatureArtifactValidationError(
            f"feature artifact contains unlisted file(s): {', '.join(unexpected)}"
        )

    observations: list[dict[str, Any]] = []
    part_paths: list[Path] = []
    for item in document["parts"]:
        part_path = (directory / item["path"]).resolve()
        try:
            part_path.relative_to(directory)
        except ValueError as error:
            raise FeatureArtifactValidationError(
                f"feature part {item['path']!r} escapes its artifact directory"
            ) from error
        if not part_path.is_file():
            raise FeatureArtifactValidationError(f"listed feature part {item['path']!r} is missing")
        actual_sha = _sha256_file(part_path)
        if actual_sha != item["sha256"]:
            raise FeatureArtifactValidationError(
                f"feature part {item['path']!r} hash mismatch: expected {item['sha256']}, got {actual_sha}"
            )
        observations.extend(_validate_part_table(part_path, manifest=document))
        part_paths.append(part_path)
    if len(observations) != document["row_count"]:
        raise FeatureArtifactValidationError(
            f"feature manifest row_count {document['row_count']} does not match Parquet rows {len(observations)}"
        )
    return ValidatedFeatureArtifact(path, document, tuple(observations), tuple(part_paths))


def validate_feature_artifact(manifest_path: str | Path) -> ValidatedFeatureArtifact:
    """Validate and return one feature artifact."""
    return read_feature_artifact(manifest_path)


def _write_exclusive(path: Path, content: bytes, *, label: str) -> None:
    try:
        with path.open("xb") as stream:
            stream.write(content)
    except FileExistsError as error:
        raise FeatureArtifactConflictError(f"{label} already exists: {path}") from error
    except OSError as error:
        raise FeatureArtifactError(f"cannot write {label} {path}: {error}") from error


def publish_market_basic(
    catalog: ResearchCatalog,
    *,
    decision_at: str | datetime,
    security_id: str | UUID,
    features_root: str | Path = _DEFAULT_FEATURE_ROOT,
    computation_delay_seconds: int = 0,
    artifact_id: str | UUID | None = None,
    generator_version: str = DEFAULT_GENERATOR_VERSION,
    git_commit: str = "unknown",
    created_at: str | datetime | None = None,
    feature_set: str = FEATURE_SET,
    feature_set_version: str = FEATURE_SET_VERSION,
) -> Path:
    """Publish one immutable deterministic market-basic feature artifact.

    The default artifact identity is UUID5-derived from the feature set,
    security, and canonical decision timestamp.  Supplying ``artifact_id`` is
    useful when an external workflow owns identity, but the same immutable
    conflict checks still apply.
    """
    selection = _select(
        catalog,
        decision_at=decision_at,
        security_id=security_id,
        computation_delay_seconds=computation_delay_seconds,
        feature_set=feature_set,
        feature_set_version=feature_set_version,
    )
    canonical_artifact_id = (
        _canonical_uuid(artifact_id, field="artifact_id")
        if artifact_id is not None
        else _artifact_id(selection.security_id, selection.decision_at)
    )
    if not isinstance(generator_version, str) or not generator_version:
        raise FeatureArtifactError("generator_version must be a non-empty string")
    if not isinstance(git_commit, str) or not (
        git_commit == "unknown" or re.fullmatch(r"[0-9a-f]{40}", git_commit)
    ):
        raise FeatureArtifactError("git_commit must be a lower-case SHA-1 or 'unknown'")
    canonical_created_at = (
        _canonical_timestamp(created_at, field="created_at")[0]
        if created_at is not None
        else selection.available_at
    )
    artifact = {
        "artifact_id": canonical_artifact_id,
        "artifact_version": FEATURE_SET_VERSION,
        "generator_version": generator_version,
        "git_commit": git_commit,
        "created_at": canonical_created_at,
    }
    observation = {
        "schema_version": SCHEMA_VERSION,
        "feature_set": FEATURE_SET,
        "feature_set_version": FEATURE_SET_VERSION,
        "security_id": selection.security_id,
        "decision_at": selection.decision_at,
        "input_available_at": selection.input_available_at,
        "computation_delay_seconds": selection.computation_delay_seconds,
        "available_at": selection.available_at,
        "input_fingerprint": selection.input_fingerprint,
        "artifact": artifact,
        "features": dict(selection.features),
    }
    part_bytes = _parquet_bytes(observation)
    part_sha = _sha256_bytes(part_bytes)
    part_name = f"part-{part_sha}.parquet"
    manifest = {
        "schema_version": SCHEMA_VERSION,
        "manifest_version": MANIFEST_VERSION,
        "feature_set": FEATURE_SET,
        "feature_set_version": FEATURE_SET_VERSION,
        "feature_names": list(FEATURE_NAMES),
        "artifact": artifact,
        "decision_at": selection.decision_at,
        "input_available_at": selection.input_available_at,
        "computation_delay_seconds": selection.computation_delay_seconds,
        "available_at": selection.available_at,
        "input_fingerprint": selection.input_fingerprint,
        "selected_input_manifests": selection.selected_input_manifests,
        "selected_input_parts": selection.selected_input_parts,
        "row_count": 1,
        "parts": [{"path": part_name, "sha256": part_sha, "row_count": 1}],
    }

    root = Path(features_root).expanduser().resolve()
    directory = _artifact_directory(root, canonical_artifact_id)
    manifest_path = directory / "manifest.json"
    if directory.exists():
        if not directory.is_dir():
            raise FeatureArtifactConflictError(f"feature artifact path is not a directory: {directory}")
        if not manifest_path.is_file():
            raise FeatureArtifactConflictError(
                f"feature artifact directory exists without a manifest: {directory}"
            )
        existing = read_feature_artifact(manifest_path)
        if existing.manifest != manifest:
            raise FeatureArtifactConflictError(
                f"immutable artifact {canonical_artifact_id} conflicts with existing content"
            )
        return manifest_path

    try:
        directory.mkdir(parents=True, exist_ok=False)
    except FileExistsError as error:
        raise FeatureArtifactConflictError(f"feature artifact identity appeared during publication: {directory}") from error
    except OSError as error:
        raise FeatureArtifactError(f"cannot create feature artifact directory {directory}: {error}") from error

    part_path = directory / part_name
    _write_exclusive(part_path, part_bytes, label="feature part")
    manifest_bytes = (
        json.dumps(manifest, ensure_ascii=False, indent=2, sort_keys=False, allow_nan=False) + "\n"
    ).encode("utf-8")
    _write_exclusive(manifest_path, manifest_bytes, label="feature manifest")
    return manifest_path


def build_market_basic_artifact(*args: Any, **kwargs: Any) -> Path:
    """Alias for callers that name the operation as an artifact build."""
    return publish_market_basic(*args, **kwargs)


__all__ = [
    "DEFAULT_GENERATOR_VERSION",
    "FEATURE_NAMES",
    "FEATURE_SET",
    "FEATURE_SET_VERSION",
    "MANIFEST_VERSION",
    "SCHEMA_VERSION",
    "FeatureArtifactConflictError",
    "FeatureArtifactError",
    "FeatureArtifactValidationError",
    "ValidatedFeatureArtifact",
    "build_market_basic_artifact",
    "compute_input_fingerprint",
    "compute_market_basic_features",
    "input_fingerprint",
    "publish_market_basic",
    "read_feature_artifact",
    "validate_feature_artifact",
]
