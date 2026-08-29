"""Shared immutable artifact IO for the reviewed multi-dataset feature sets."""

from __future__ import annotations

import json
import re
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from datetime import datetime, timedelta
from decimal import Decimal, InvalidOperation
from pathlib import Path
from typing import Any
from uuid import UUID, uuid5

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
    _decimal_string,
    _read_json,
    _sha256_bytes,
    _sha256_file,
    _validate_decimal_string,
    _validate_delay,
    _validate_relative_path,
    _validate_sha256,
    _write_exclusive,
    compute_input_fingerprint,
)

_PART_PATTERN = re.compile(r"^part-([0-9a-f]{64})\.parquet$")
_GIT_COMMIT_PATTERN = re.compile(r"^[0-9a-f]{40}$")
_DERIVED_MANIFEST_FIELDS = frozenset(
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
_DERIVED_OBSERVATION_FIELDS = frozenset(
    {
        "schema_version",
        "feature_set",
        "feature_set_version",
        "decision_at",
        "input_available_at",
        "computation_delay_seconds",
        "available_at",
        "input_fingerprint",
        "artifact",
        "features",
    }
)
_ARTIFACT_FIELDS = frozenset(
    {"artifact_id", "artifact_version", "generator_version", "git_commit", "created_at"}
)
_LINEAGE_FIELDS = frozenset({"path", "sha256"})
_PART_FIELDS = frozenset({"path", "sha256", "row_count"})


@dataclass(frozen=True)
class DerivedArtifactContract:
    """Closed identity and feature fields for one derived artifact family."""

    feature_set: str
    feature_set_version: str
    feature_names: tuple[str, ...]
    identity_fields: tuple[str, ...]
    namespace: UUID
    directory_name: str
    mapping_field: str | None = None


def _artifact_id(
    contract: DerivedArtifactContract,
    identity: Mapping[str, str],
    decision_at: str,
    input_fingerprint: str,
) -> str:
    identity_text = ":".join(identity[field] for field in contract.identity_fields)
    return str(
        uuid5(
            contract.namespace,
            f"{contract.feature_set}:{contract.feature_set_version}:{ARTIFACT_VERSION}:"
            f"{identity_text}:{decision_at}:{input_fingerprint}",
        )
    )


def _artifact_directory(
    features_root: Path, contract: DerivedArtifactContract, artifact_id: str
) -> Path:
    return (
        features_root
        / contract.directory_name
        / contract.feature_set_version
        / f"artifact-{artifact_id}"
    )


def _validate_identity(
    contract: DerivedArtifactContract,
    value: Mapping[str, Any],
    *,
    label: str,
) -> dict[str, str]:
    if set(value) != set(contract.identity_fields):
        raise FeatureArtifactValidationError(f"{label} has missing or unknown identity fields")
    result: dict[str, str] = {}
    for field in contract.identity_fields:
        item = value[field]
        if not isinstance(item, str) or not item:
            raise FeatureArtifactValidationError(f"{label}.{field} must be non-empty")
        if field in {"security_id", "issuer_id"}:
            try:
                canonical = _canonical_uuid(item, field=f"{label}.{field}")
            except FeatureArtifactError as error:
                raise FeatureArtifactValidationError(str(error)) from error
            if item != canonical:
                raise FeatureArtifactValidationError(f"{label}.{field} is not a canonical UUID")
        result[field] = item
    return result


def _validate_artifact(value: Any, *, label: str) -> dict[str, Any]:
    if not isinstance(value, dict) or set(value) != _ARTIFACT_FIELDS:
        raise FeatureArtifactValidationError(f"{label} has missing or unknown fields")
    try:
        artifact_id = _canonical_uuid(value["artifact_id"], field=f"{label}.artifact_id")
    except FeatureArtifactError as error:
        raise FeatureArtifactValidationError(str(error)) from error
    if value["artifact_id"] != artifact_id:
        raise FeatureArtifactValidationError(f"{label}.artifact_id is not canonical")
    if value["artifact_version"] != ARTIFACT_VERSION:
        raise FeatureArtifactValidationError(f"{label}.artifact_version is unsupported")
    if not isinstance(value["generator_version"], str) or not value["generator_version"]:
        raise FeatureArtifactValidationError(f"{label}.generator_version must be non-empty")
    if not isinstance(value["git_commit"], str) or not (
        value["git_commit"] == "unknown" or _GIT_COMMIT_PATTERN.fullmatch(value["git_commit"])
    ):
        raise FeatureArtifactValidationError(f"{label}.git_commit is invalid")
    try:
        created_at, _ = _canonical_timestamp(value["created_at"], field=f"{label}.created_at")
    except FeatureArtifactError as error:
        raise FeatureArtifactValidationError(str(error)) from error
    if value["created_at"] != created_at:
        raise FeatureArtifactValidationError(f"{label}.created_at is not canonical UTC")
    return value


def _validate_lineage(
    value: Any,
    *,
    label: str,
    parts: bool,
) -> list[dict[str, Any]]:
    if not isinstance(value, list) or not value:
        raise FeatureArtifactValidationError(f"{label} must be a non-empty array")
    result: list[dict[str, Any]] = []
    seen: set[tuple[str, str]] = set()
    for index, item in enumerate(value):
        expected = _PART_FIELDS if parts else _LINEAGE_FIELDS
        if not isinstance(item, dict) or set(item) != expected:
            raise FeatureArtifactValidationError(f"{label}[{index}] has invalid fields")
        path = item["path"]
        if parts:
            if not isinstance(path, str) or _PART_PATTERN.fullmatch(path) is None:
                raise FeatureArtifactValidationError(f"{label}[{index}].path is not content-named")
            row_count = item["row_count"]
            if not isinstance(row_count, int) or isinstance(row_count, bool) or row_count < 0:
                raise FeatureArtifactValidationError(f"{label}[{index}].row_count is invalid")
        else:
            _validate_relative_path(path, field=f"{label}[{index}].path")
        sha256 = _validate_sha256(item["sha256"], field=f"{label}[{index}].sha256")
        if parts and path != f"part-{sha256}.parquet":
            raise FeatureArtifactValidationError(f"{label}[{index}] path does not match its hash")
        key = (path, sha256)
        if key in seen:
            raise FeatureArtifactValidationError(f"{label} contains duplicate entries")
        seen.add(key)
        result.append(item)
    return result


def _pyarrow_schema(contract: DerivedArtifactContract) -> Any:
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
    identity_type = [pa.field(field, pa.string()) for field in contract.identity_fields]
    fixed_type = [
        pa.field("schema_version", pa.string()),
        pa.field("feature_set", pa.string()),
        pa.field("feature_set_version", pa.string()),
        *identity_type,
        pa.field("decision_at", pa.string()),
        pa.field("input_available_at", pa.string()),
        pa.field("computation_delay_seconds", pa.int64()),
        pa.field("available_at", pa.string()),
        pa.field("input_fingerprint", pa.string()),
    ]
    if contract.mapping_field is not None:
        fixed_type.append(pa.field(contract.mapping_field, pa.string()))
    fixed_type.extend(
        [
            pa.field("artifact", artifact_type),
            pa.field(
                "features",
                pa.struct([pa.field(name, pa.string()) for name in contract.feature_names]),
            ),
        ]
    )
    return pa.schema(fixed_type)


def _parquet_bytes(
    contract: DerivedArtifactContract,
    observation: Mapping[str, Any],
) -> bytes:
    import pyarrow as pa
    from pyarrow import parquet

    table = pa.Table.from_pylist([dict(observation)], schema=_pyarrow_schema(contract))
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


def _validate_manifest(
    document: dict[str, Any],
    *,
    contract: DerivedArtifactContract,
    manifest_path: Path,
) -> dict[str, Any]:
    expected_fields = set(_DERIVED_MANIFEST_FIELDS) | set(contract.identity_fields)
    if contract.mapping_field is not None:
        expected_fields.add(contract.mapping_field)
    if set(document) != expected_fields:
        raise FeatureArtifactValidationError(
            f"manifest {manifest_path} has missing or unknown fields"
        )
    if document["schema_version"] != SCHEMA_VERSION:
        raise FeatureArtifactValidationError("derived manifest schema_version is unsupported")
    if document["manifest_version"] != MANIFEST_VERSION:
        raise FeatureArtifactValidationError("derived manifest manifest_version is unsupported")
    if document["feature_set"] != contract.feature_set:
        raise FeatureArtifactValidationError("derived manifest feature_set is unsupported")
    if document["feature_set_version"] != contract.feature_set_version:
        raise FeatureArtifactValidationError("derived manifest feature_set_version is unsupported")
    if document["feature_names"] != list(contract.feature_names):
        raise FeatureArtifactValidationError("derived manifest feature_names differ from contract")
    _validate_identity(
        contract,
        {field: document[field] for field in contract.identity_fields},
        label="manifest identity",
    )
    if contract.mapping_field is not None:
        _validate_sha256(document[contract.mapping_field], field=f"manifest.{contract.mapping_field}")
    _validate_artifact(document["artifact"], label="manifest artifact")
    timestamps: dict[str, Any] = {}
    for field in ("decision_at", "input_available_at", "available_at"):
        try:
            canonical, parsed = _canonical_timestamp(document[field], field=f"manifest.{field}")
        except FeatureArtifactError as error:
            raise FeatureArtifactValidationError(str(error)) from error
        if document[field] != canonical:
            raise FeatureArtifactValidationError(f"manifest.{field} is not canonical UTC")
        timestamps[field] = parsed
    try:
        calendar = _canonical_calendar_pin(
            document["calendar_pin"], decision_at=timestamps["decision_at"], field="manifest.calendar_pin"
        )
    except FeatureArtifactError as error:
        raise FeatureArtifactValidationError(str(error)) from error
    if document["calendar_pin"] != calendar:
        raise FeatureArtifactValidationError("manifest.calendar_pin is not canonical")
    try:
        delay = _validate_delay(document["computation_delay_seconds"])
    except FeatureArtifactError as error:
        raise FeatureArtifactValidationError(str(error)) from error
    _, calendar_available_at = _canonical_timestamp(
        calendar["calendar_available_at"], field="manifest.calendar_pin.calendar_available_at"
    )
    if timestamps["input_available_at"] < calendar_available_at:
        raise FeatureArtifactValidationError("manifest input availability precedes calendar availability")
    if timestamps["input_available_at"] > timestamps["decision_at"]:
        raise FeatureArtifactValidationError("manifest input availability is after decision_at")
    if timestamps["available_at"] != timestamps["input_available_at"] + timedelta(seconds=delay):
        raise FeatureArtifactValidationError("manifest available_at violates timing contract")

    _validate_sha256(document["input_fingerprint"], field="manifest.input_fingerprint")
    _validate_lineage(document["selected_input_manifests"], label="selected_input_manifests", parts=False)
    _validate_lineage(document["selected_input_parts"], label="selected_input_parts", parts=False)
    expected_fingerprint = compute_input_fingerprint(document)
    if document["input_fingerprint"] != expected_fingerprint:
        raise FeatureArtifactValidationError("derived manifest input_fingerprint is invalid")

    row_count = document["row_count"]
    if not isinstance(row_count, int) or isinstance(row_count, bool) or row_count < 0:
        raise FeatureArtifactValidationError("derived manifest row_count is invalid")
    parts = _validate_lineage(document["parts"], label="manifest.parts", parts=True)
    if row_count != sum(item["row_count"] for item in parts):
        raise FeatureArtifactValidationError("derived manifest row_count does not match parts")
    return document


def _validate_part_table(
    path: Path,
    *,
    manifest: Mapping[str, Any],
    contract: DerivedArtifactContract,
) -> list[dict[str, Any]]:
    from pyarrow import parquet

    try:
        table = parquet.read_table(path)
    except Exception as error:
        raise FeatureArtifactValidationError(f"cannot read derived feature part {path}: {error}") from error
    expected_schema = _pyarrow_schema(contract)
    if table.column_names != expected_schema.names:
        raise FeatureArtifactValidationError(f"derived feature part {path} has an unsupported column set")
    for actual, expected in zip(table.schema, expected_schema, strict=True):
        if actual.type != expected.type:
            raise FeatureArtifactValidationError(
                f"derived feature part {path} field {actual.name!r} has type {actual.type}; "
                f"expected {expected.type}"
            )
    rows = table.to_pylist()
    expected_fields = set(_DERIVED_OBSERVATION_FIELDS) | set(contract.identity_fields)
    if contract.mapping_field is not None:
        expected_fields.add(contract.mapping_field)
    result: list[dict[str, Any]] = []
    for index, row in enumerate(rows):
        if not isinstance(row, dict) or set(row) != expected_fields:
            raise FeatureArtifactValidationError(f"derived feature part {path} row {index} has unknown fields")
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
                    f"derived feature part {path} row {index} {field} disagrees with manifest"
                )
        if row["computation_delay_seconds"] != manifest["computation_delay_seconds"]:
            raise FeatureArtifactValidationError(f"derived feature part {path} row {index} delay disagrees with manifest")
        for field in contract.identity_fields:
            if row[field] != manifest[field]:
                raise FeatureArtifactValidationError(
                    f"derived feature part {path} row {index} {field} disagrees with manifest"
                )
        if contract.mapping_field is not None and row[contract.mapping_field] != manifest[contract.mapping_field]:
            raise FeatureArtifactValidationError(
                f"derived feature part {path} row {index} mapping fingerprint disagrees with manifest"
            )
        _validate_artifact(row["artifact"], label=f"derived feature part {path} row {index} artifact")
        if row["artifact"] != manifest["artifact"]:
            raise FeatureArtifactValidationError(f"derived feature part {path} row {index} artifact disagrees with manifest")
        features = row["features"]
        if not isinstance(features, dict) or set(features) != set(contract.feature_names):
            raise FeatureArtifactValidationError(f"derived feature part {path} row {index} has unknown features")
        for name, value in features.items():
            if value is not None:
                _validate_decimal_string(value, field=f"derived feature part {path} row {index}.{name}")
        for field in ("decision_at", "input_available_at", "available_at"):
            try:
                canonical, _ = _canonical_timestamp(row[field], field=f"observation.{field}")
            except FeatureArtifactError as error:
                raise FeatureArtifactValidationError(str(error)) from error
            if canonical != row[field]:
                raise FeatureArtifactValidationError(f"derived feature part {path} row {index} {field} is not canonical")
        result.append(row)
    return result


def read_derived_artifact(
    manifest_path: str | Path,
    *,
    contract: DerivedArtifactContract,
) -> ValidatedFeatureArtifact:
    """Validate one derived manifest and exactly its listed immutable part(s)."""
    path = Path(manifest_path).expanduser().resolve()
    if path.name != "manifest.json" or not path.is_file():
        raise FeatureArtifactValidationError(f"derived feature manifest {path} does not exist")
    document = _validate_manifest(_read_json(path, label="derived feature manifest"), contract=contract, manifest_path=path)
    directory = path.parent.resolve()
    listed_names = {"manifest.json", *(item["path"] for item in document["parts"])}
    try:
        unexpected = sorted(entry.name for entry in directory.iterdir() if entry.name not in listed_names)
    except OSError as error:
        raise FeatureArtifactValidationError(f"cannot inspect derived artifact {directory}: {error}") from error
    if unexpected:
        raise FeatureArtifactValidationError(f"derived artifact contains unlisted file(s): {', '.join(unexpected)}")
    observations: list[dict[str, Any]] = []
    part_paths: list[Path] = []
    for item in document["parts"]:
        part_path = (directory / item["path"]).resolve()
        try:
            part_path.relative_to(directory)
        except ValueError as error:
            raise FeatureArtifactValidationError(f"derived feature part {item['path']!r} escapes artifact") from error
        if not part_path.is_file():
            raise FeatureArtifactValidationError(f"listed derived feature part {item['path']!r} is missing")
        actual_sha = _sha256_file(part_path)
        if actual_sha != item["sha256"]:
            raise FeatureArtifactValidationError(
                f"derived feature part {item['path']!r} hash mismatch: expected {item['sha256']}, got {actual_sha}"
            )
        observations.extend(_validate_part_table(part_path, manifest=document, contract=contract))
        part_paths.append(part_path)
    if len(observations) != document["row_count"]:
        raise FeatureArtifactValidationError("derived manifest row_count does not match Parquet rows")
    return ValidatedFeatureArtifact(path, document, tuple(observations), tuple(part_paths))


def publish_derived_artifact(
    *,
    contract: DerivedArtifactContract,
    identity: Mapping[str, str],
    decision_at: str,
    calendar_pin: Mapping[str, str],
    input_available_at: str,
    available_at: str,
    computation_delay_seconds: int,
    input_fingerprint: str,
    selected_input_manifests: Sequence[Mapping[str, str]],
    selected_input_parts: Sequence[Mapping[str, str]],
    features: Mapping[str, str | None],
    features_root: str | Path,
    generator_version: str,
    git_commit: str = "unknown",
    artifact_id: str | UUID | None = None,
    created_at: str | None = None,
    mapping_sha256: str | None = None,
) -> Path:
    """Write one deterministic derived artifact with exclusive publication."""
    canonical_identity = _validate_identity(contract, identity, label="artifact identity")
    if set(features) != set(contract.feature_names):
        raise FeatureArtifactError("derived features do not match the closed contract")
    canonical_features: dict[str, str | None] = {}
    for name in contract.feature_names:
        value = features[name]
        if value is None:
            canonical_features[name] = None
        else:
            try:
                decimal_value = Decimal(value)
            except (InvalidOperation, ValueError) as error:
                raise FeatureArtifactError(f"{name} must be a valid decimal string") from error
            canonical_features[name] = _decimal_string(decimal_value, field=name)
    if contract.mapping_field is not None:
        if mapping_sha256 is None:
            raise FeatureArtifactError(f"{contract.mapping_field} is required")
        _validate_sha256(mapping_sha256, field=contract.mapping_field)
    elif mapping_sha256 is not None:
        raise FeatureArtifactError("mapping_sha256 is not supported by this derived contract")
    _validate_sha256(input_fingerprint, field="input_fingerprint")
    delay = _validate_delay(computation_delay_seconds)
    canonical_decision, decision_datetime = _canonical_timestamp(decision_at, field="decision_at")
    canonical_input_available, input_available_datetime = _canonical_timestamp(
        input_available_at, field="input_available_at"
    )
    canonical_available, available_datetime = _canonical_timestamp(available_at, field="available_at")
    if input_available_datetime > decision_datetime:
        raise FeatureArtifactError("input_available_at is after decision_at")
    if available_datetime != input_available_datetime + timedelta(seconds=delay):
        raise FeatureArtifactError("available_at violates timing contract")
    canonical_calendar = _canonical_calendar_pin(
        calendar_pin, decision_at=decision_datetime, field="calendar_pin"
    )
    calendar_available_datetime = datetime.fromisoformat(canonical_calendar["calendar_available_at"])
    if input_available_datetime < calendar_available_datetime:
        raise FeatureArtifactError("input_available_at precedes calendar availability")
    selected_manifests = [dict(item) for item in selected_input_manifests]
    selected_parts = [dict(item) for item in selected_input_parts]
    if not selected_manifests or not selected_parts:
        raise FeatureArtifactError("derived artifact requires selected input lineage")
    artifact_identifier = (
        _canonical_uuid(artifact_id, field="artifact_id")
        if artifact_id is not None
        else _artifact_id(contract, canonical_identity, canonical_decision, input_fingerprint)
    )
    if not isinstance(generator_version, str) or not generator_version:
        raise FeatureArtifactError("generator_version must be non-empty")
    if not isinstance(git_commit, str) or not (git_commit == "unknown" or _GIT_COMMIT_PATTERN.fullmatch(git_commit)):
        raise FeatureArtifactError("git_commit must be a lower-case SHA-1 or 'unknown'")
    canonical_created_at = canonical_available if created_at is None else _canonical_timestamp(created_at, field="created_at")[0]
    artifact = {
        "artifact_id": artifact_identifier,
        "artifact_version": ARTIFACT_VERSION,
        "generator_version": generator_version,
        "git_commit": git_commit,
        "created_at": canonical_created_at,
    }
    observation: dict[str, Any] = {
        "schema_version": SCHEMA_VERSION,
        "feature_set": contract.feature_set,
        "feature_set_version": contract.feature_set_version,
        **canonical_identity,
        "decision_at": canonical_decision,
        "input_available_at": canonical_input_available,
        "computation_delay_seconds": delay,
        "available_at": canonical_available,
        "input_fingerprint": input_fingerprint,
    }
    if contract.mapping_field is not None:
        observation[contract.mapping_field] = mapping_sha256
    observation.update({"artifact": artifact, "features": canonical_features})
    part_bytes = _parquet_bytes(contract, observation)
    part_sha256 = _sha256_bytes(part_bytes)
    part_name = f"part-{part_sha256}.parquet"
    manifest: dict[str, Any] = {
        "schema_version": SCHEMA_VERSION,
        "manifest_version": MANIFEST_VERSION,
        "feature_set": contract.feature_set,
        "feature_set_version": contract.feature_set_version,
        "feature_names": list(contract.feature_names),
        **canonical_identity,
        "artifact": artifact,
        "calendar_pin": canonical_calendar,
        "decision_at": canonical_decision,
        "input_available_at": canonical_input_available,
        "computation_delay_seconds": delay,
        "available_at": canonical_available,
        "input_fingerprint": input_fingerprint,
        "selected_input_manifests": selected_manifests,
        "selected_input_parts": selected_parts,
        "row_count": 1,
        "parts": [{"path": part_name, "sha256": part_sha256, "row_count": 1}],
    }
    if contract.mapping_field is not None:
        manifest[contract.mapping_field] = mapping_sha256
    root = Path(features_root).expanduser().resolve()
    directory = _artifact_directory(root, contract, artifact_identifier)
    manifest_path = directory / "manifest.json"
    if directory.exists():
        if not directory.is_dir() or not manifest_path.is_file():
            raise FeatureArtifactConflictError(f"derived artifact directory is incomplete: {directory}")
        existing = read_derived_artifact(manifest_path, contract=contract)
        if existing.manifest != manifest:
            raise FeatureArtifactConflictError(f"immutable derived artifact {artifact_identifier} conflicts with existing content")
        return manifest_path
    try:
        directory.mkdir(parents=True, exist_ok=False)
    except FileExistsError as error:
        raise FeatureArtifactConflictError(f"derived artifact identity appeared during publication: {directory}") from error
    except OSError as error:
        raise FeatureArtifactError(f"cannot create derived artifact directory {directory}: {error}") from error
    _write_exclusive(directory / part_name, part_bytes, label="derived feature part")
    manifest_bytes = (json.dumps(manifest, ensure_ascii=False, indent=2, sort_keys=False, allow_nan=False) + "\n").encode("utf-8")
    _write_exclusive(manifest_path, manifest_bytes, label="derived feature manifest")
    return manifest_path


__all__ = [
    "DerivedArtifactContract",
    "publish_derived_artifact",
    "read_derived_artifact",
]
