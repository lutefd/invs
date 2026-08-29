"""Deterministic ``market-momentum`` feature artifacts.

The feature set is deliberately small and source-native: it consumes one
point-in-time daily price series and publishes exact decimal strings for
multi-horizon returns and two trailing risk diagnostics.  Artifact identity,
lineage, and validation follow the existing feature artifact contract while
using a feature-set-specific schema and UUID namespace.
"""

from __future__ import annotations

import json
import re
from collections.abc import Mapping
from dataclasses import dataclass
from datetime import datetime, timedelta
from decimal import Decimal, localcontext
from pathlib import Path
from typing import Any, Final
from uuid import UUID, uuid5

from .catalog import ResearchCatalog
from .features import (
    ARTIFACT_VERSION,
    MANIFEST_VERSION,
    SCHEMA_VERSION,
    FeatureArtifactConflictError,
    FeatureArtifactError,
    FeatureArtifactValidationError,
    ValidatedFeatureArtifact,
    _canonical_calendar_pin,
    _canonical_timestamp,
    _canonical_uuid,
    _decimal,
    _decimal_string,
    _duckdb_timestamp,
    _read_json,
    _record_value,
    _selected_lineage,
    _sha256_bytes,
    _sha256_file,
    _strict_frame_records,
    _validate_artifact_metadata,
    _validate_decimal_string,
    _validate_delay,
    _validate_relative_path,
    _validate_sha256,
    _write_exclusive,
    compute_input_fingerprint,
)

FEATURE_SET: Final[str] = "market-momentum"
FEATURE_SET_VERSION: Final[str] = "1.0.0"
FEATURE_NAMES: Final[tuple[str, ...]] = (
    "return_1m",
    "return_3m",
    "return_6m",
    "return_12m",
    "realized_volatility_1m",
    "max_drawdown_1m",
)
DEFAULT_GENERATOR_VERSION: Final[str] = "python-market-momentum-1.0.0"

_DEFAULT_FEATURE_ROOT = Path("data/features")
_ARTIFACT_NAMESPACE = UUID("7ca5a2d6-b6c1-5d83-94aa-a9e11901d428")
_CALCULATION_PRECISION: Final[int] = 256
_ANNUALIZATION_FACTOR: Final[Decimal] = Decimal(252)
_RETURN_HORIZONS: Final[tuple[tuple[str, int], ...]] = (
    ("return_1m", 21),
    ("return_3m", 63),
    ("return_6m", 126),
    ("return_12m", 252),
)
_REALIZED_VOLATILITY_WINDOW: Final[int] = 21
_MAX_DRAWDOWN_WINDOW: Final[int] = 21
_PART_PATTERN = re.compile(r"^part-([0-9a-f]{64})\.parquet$")
_MANIFEST_FIELDS = frozenset(
    {
        "schema_version",
        "manifest_version",
        "feature_set",
        "feature_set_version",
        "feature_names",
        "artifact",
        "calendar_pin",
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


def _artifact_id(security_id: str, decision_at: str, input_fingerprint: str) -> str:
    return str(
        uuid5(
            _ARTIFACT_NAMESPACE,
            f"{FEATURE_SET}:{FEATURE_SET_VERSION}:{ARTIFACT_VERSION}:{security_id}:"
            f"{decision_at}:{input_fingerprint}",
        )
    )


def _artifact_directory(features_root: Path, artifact_id: str) -> Path:
    return features_root / FEATURE_SET / FEATURE_SET_VERSION / f"artifact-{artifact_id}"


def _require_complete_window(
    values: list[Decimal | None],
    *,
    length: int,
) -> list[Decimal] | None:
    if len(values) < length or any(value is None for value in values[-length:]):
        return None
    return [value for value in values[-length:] if value is not None]


def _simple_return(
    values: list[Decimal | None],
    *,
    horizon: int,
    field: str,
) -> str | None:
    if len(values) <= horizon:
        return None
    current = values[-1]
    previous = values[-(horizon + 1)]
    if current is None or previous is None:
        return None
    if previous.is_zero():
        raise FeatureArtifactError(f"{field} has a zero previous close")
    return _decimal_string(current / previous - Decimal(1), field=field)


def _realized_volatility(values: list[Decimal | None]) -> str | None:
    window = _require_complete_window(
        values,
        length=_REALIZED_VOLATILITY_WINDOW + 1,
    )
    if window is None:
        return None
    returns: list[Decimal] = []
    for index in range(1, len(window)):
        previous = window[index - 1]
        if previous.is_zero():
            raise FeatureArtifactError("realized_volatility_1m has a zero previous close")
        returns.append(window[index] / previous - Decimal(1))
    mean = sum(returns, Decimal(0)) / Decimal(len(returns))
    variance = sum((value - mean) ** 2 for value in returns) / Decimal(len(returns) - 1)
    return _decimal_string(
        variance.sqrt() * _ANNUALIZATION_FACTOR.sqrt(),
        field="realized_volatility_1m",
    )


def _max_drawdown(values: list[Decimal | None]) -> str | None:
    window = _require_complete_window(values, length=_MAX_DRAWDOWN_WINDOW)
    if window is None:
        return None
    peak: Decimal | None = None
    worst: Decimal | None = None
    for close in window:
        if close < 0:
            raise FeatureArtifactError("max_drawdown_1m requires non-negative closes")
        if peak is None or close > peak:
            peak = close
        if peak.is_zero():
            raise FeatureArtifactError("max_drawdown_1m has a zero prior peak")
        drawdown = close / peak - Decimal(1)
        worst = drawdown if worst is None else min(worst, drawdown)
    if worst is None:
        raise FeatureArtifactError("max_drawdown_1m could not compute a drawdown")
    return _decimal_string(worst, field="max_drawdown_1m")


def _compute_features(records: list[Mapping[str, Any]]) -> dict[str, str | None]:
    if not records:
        raise FeatureArtifactError("no price rows are eligible at the requested decision_at")
    for index, record in enumerate(records):
        interval = _record_value(record, "interval")
        if interval != "1d":
            raise FeatureArtifactError(
                f"market-momentum requires daily price bars; row {index} has interval {interval!r}"
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
            "market-momentum requires one deterministic source/interval/price-basis/currency series"
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
    values: list[Decimal | None] = []
    for index, record in enumerate(ordered):
        raw_close = _record_value(record, "close_value")
        close = None if raw_close is None else _decimal(raw_close, field=f"close[{index}]")
        if close is not None and close < 0:
            raise FeatureArtifactError("market-momentum requires non-negative closes")
        values.append(close)

    with localcontext() as context:
        context.prec = _CALCULATION_PRECISION
        features = {
            field: _simple_return(values, horizon=horizon, field=field)
            for field, horizon in _RETURN_HORIZONS
        }
        features["realized_volatility_1m"] = _realized_volatility(values)
        features["max_drawdown_1m"] = _max_drawdown(values)
    return {name: features[name] for name in FEATURE_NAMES}


@dataclass(frozen=True)
class _Selection:
    decision_at: str
    security_id: str
    calendar_pin: dict[str, str]
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
    calendar_pin: Mapping[str, Any] | None,
) -> _Selection:
    if feature_set != FEATURE_SET:
        raise FeatureArtifactError(f"unsupported feature_set {feature_set!r}")
    if feature_set_version != FEATURE_SET_VERSION:
        raise FeatureArtifactError(f"unsupported feature_set_version {feature_set_version!r}")
    if calendar_pin is None:
        raise FeatureArtifactError("calendar_pin is required for market-momentum")
    delay = _validate_delay(computation_delay_seconds)
    canonical_decision, decision_datetime = _canonical_timestamp(decision_at, field="decision_at")
    canonical_security_id = _canonical_uuid(security_id, field="security_id")
    canonical_calendar_pin = _canonical_calendar_pin(
        calendar_pin,
        decision_at=decision_datetime,
    )
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
    _, calendar_available_datetime = _canonical_timestamp(
        canonical_calendar_pin["calendar_available_at"],
        field="calendar_pin.calendar_available_at",
    )
    input_available_datetime = max(input_available_datetime, calendar_available_datetime)
    input_available_at = _canonical_timestamp(
        input_available_datetime,
        field="input_available_at",
    )[0]
    available_datetime = input_available_datetime + timedelta(seconds=delay)
    available_at = _canonical_timestamp(available_datetime, field="available_at")[0]
    selected_input_manifests, selected_input_parts = _selected_lineage(catalog, records)
    fingerprint_envelope = {
        "calendar_pin": canonical_calendar_pin,
        "decision_at": canonical_decision,
        "feature_set": FEATURE_SET,
        "feature_set_version": FEATURE_SET_VERSION,
        "selected_input_manifests": selected_input_manifests,
        "selected_input_parts": selected_input_parts,
    }
    input_fingerprint = compute_input_fingerprint(fingerprint_envelope)
    return _Selection(
        decision_at=canonical_decision,
        security_id=canonical_security_id,
        calendar_pin=canonical_calendar_pin,
        input_available_at=input_available_at,
        available_at=available_at,
        computation_delay_seconds=delay,
        input_fingerprint=input_fingerprint,
        selected_input_manifests=selected_input_manifests,
        selected_input_parts=selected_input_parts,
        features=_compute_features(records),
    )


def compute_market_momentum_features(
    catalog: ResearchCatalog,
    *,
    decision_at: str | datetime,
    security_id: str | UUID,
    calendar_pin: Mapping[str, Any],
) -> dict[str, str | None]:
    """Compute the reviewed market-momentum registry for one point-in-time security."""
    return _select(
        catalog,
        decision_at=decision_at,
        security_id=security_id,
        computation_delay_seconds=0,
        feature_set=FEATURE_SET,
        feature_set_version=FEATURE_SET_VERSION,
        calendar_pin=calendar_pin,
    ).features


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
    try:
        calendar_pin = _canonical_calendar_pin(
            document["calendar_pin"],
            decision_at=timestamps["decision_at"],
            field="manifest.calendar_pin",
        )
    except FeatureArtifactError as error:
        raise FeatureArtifactValidationError(str(error)) from error
    if document["calendar_pin"] != calendar_pin:
        raise FeatureArtifactValidationError("manifest.calendar_pin is not canonical")
    _, calendar_available_at = _canonical_timestamp(
        calendar_pin["calendar_available_at"],
        field="manifest.calendar_pin.calendar_available_at",
    )
    if timestamps["input_available_at"] < calendar_available_at:
        raise FeatureArtifactValidationError(
            "manifest input_available_at precedes calendar availability"
        )
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
            item["path"],
            field=f"selected_input_manifests[{index}].path",
        )
        sha256 = _validate_sha256(
            item["sha256"],
            field=f"selected_input_manifests[{index}].sha256",
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
        sha256 = _validate_sha256(
            item["sha256"],
            field=f"selected_input_parts[{index}].sha256",
        )
        match = _PART_PATTERN.fullmatch(path) if isinstance(path, str) else None
        if match is None or match.group(1) != sha256:
            raise FeatureArtifactValidationError(
                f"selected_input_parts[{index}] is not content-named"
            )
        key = (path, sha256)
        if key in part_keys:
            raise FeatureArtifactValidationError("manifest selected input parts are not unique")
        part_keys.add(key)

    if document["input_fingerprint"] != compute_input_fingerprint(document):
        raise FeatureArtifactValidationError("manifest input_fingerprint does not match ADR 0005")

    row_count = document["row_count"]
    if not isinstance(row_count, int) or isinstance(row_count, bool) or row_count < 0:
        raise FeatureArtifactValidationError("manifest row_count is invalid")
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
        part_row_count = item["row_count"]
        if (
            not isinstance(part_row_count, int)
            or isinstance(part_row_count, bool)
            or part_row_count < 0
        ):
            raise FeatureArtifactValidationError(f"manifest.parts[{index}].row_count is invalid")
        if path in output_names:
            raise FeatureArtifactValidationError("manifest output parts are not unique")
        output_names.add(path)
        output_rows += part_row_count
    if row_count != output_rows:
        raise FeatureArtifactValidationError("manifest row_count does not equal output part rows")
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


def _validate_part_table(path: Path, *, manifest: Mapping[str, Any]) -> list[dict[str, Any]]:
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
            raise FeatureArtifactValidationError(
                f"feature part {path} row {index} has non-canonical security_id"
            )
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


def read_market_momentum_artifact(manifest_path: str | Path) -> ValidatedFeatureArtifact:
    """Read and validate one market-momentum manifest and its listed parts."""
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
        raise FeatureArtifactValidationError(
            f"cannot inspect feature artifact {directory}: {error}"
        ) from error
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


def validate_market_momentum_artifact(manifest_path: str | Path) -> ValidatedFeatureArtifact:
    """Validate and return one market-momentum artifact."""
    return read_market_momentum_artifact(manifest_path)


def publish_market_momentum(
    catalog: ResearchCatalog,
    *,
    decision_at: str | datetime,
    security_id: str | UUID,
    calendar_pin: Mapping[str, Any],
    features_root: str | Path = _DEFAULT_FEATURE_ROOT,
    computation_delay_seconds: int = 0,
    artifact_id: str | UUID | None = None,
    generator_version: str = DEFAULT_GENERATOR_VERSION,
    git_commit: str = "unknown",
    created_at: str | datetime | None = None,
    feature_set: str = FEATURE_SET,
    feature_set_version: str = FEATURE_SET_VERSION,
) -> Path:
    """Publish one immutable deterministic market-momentum artifact."""
    selection = _select(
        catalog,
        decision_at=decision_at,
        security_id=security_id,
        computation_delay_seconds=computation_delay_seconds,
        feature_set=feature_set,
        feature_set_version=feature_set_version,
        calendar_pin=calendar_pin,
    )
    canonical_artifact_id = (
        _canonical_uuid(artifact_id, field="artifact_id")
        if artifact_id is not None
        else _artifact_id(
            selection.security_id,
            selection.decision_at,
            selection.input_fingerprint,
        )
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
        "artifact_version": ARTIFACT_VERSION,
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
        "calendar_pin": selection.calendar_pin,
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
        existing = read_market_momentum_artifact(manifest_path)
        if existing.manifest != manifest:
            raise FeatureArtifactConflictError(
                f"immutable artifact {canonical_artifact_id} conflicts with existing content"
            )
        return manifest_path

    try:
        directory.mkdir(parents=True, exist_ok=False)
    except FileExistsError as error:
        raise FeatureArtifactConflictError(
            f"feature artifact identity appeared during publication: {directory}"
        ) from error
    except OSError as error:
        raise FeatureArtifactError(f"cannot create feature artifact directory {directory}: {error}") from error

    part_path = directory / part_name
    _write_exclusive(part_path, part_bytes, label="feature part")
    manifest_bytes = (
        json.dumps(manifest, ensure_ascii=False, indent=2, sort_keys=False, allow_nan=False) + "\n"
    ).encode("utf-8")
    _write_exclusive(manifest_path, manifest_bytes, label="feature manifest")
    return manifest_path


def build_market_momentum_artifact(*args: Any, **kwargs: Any) -> Path:
    """Alias for callers that name the operation as an artifact build."""
    return publish_market_momentum(*args, **kwargs)


__all__ = [
    "DEFAULT_GENERATOR_VERSION",
    "FEATURE_NAMES",
    "FEATURE_SET",
    "FEATURE_SET_VERSION",
    "build_market_momentum_artifact",
    "compute_market_momentum_features",
    "publish_market_momentum",
    "read_market_momentum_artifact",
    "validate_market_momentum_artifact",
]
