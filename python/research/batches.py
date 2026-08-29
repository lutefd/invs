"""Deterministic, resumable dataset-level feature batches."""

from __future__ import annotations

import hashlib
import json
import re
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Final
from uuid import UUID, uuid5

from .features import (
    FeatureArtifactConflictError,
    FeatureArtifactError,
    FeatureArtifactValidationError,
    _canonical_timestamp,
    _canonical_uuid,
    publish_feature_artifact,
    read_feature_artifact,
)
from .registry import FeatureRegistry, FeatureRegistryError

BATCH_SCHEMA_VERSION: Final[str] = "1.0.0"
BATCH_MANIFEST_VERSION: Final[str] = "1.0.0"
BATCH_ARTIFACT_VERSION: Final[str] = "1.0.0"
_DEFAULT_FEATURE_ROOT = Path("data/features")
_BATCH_NAMESPACE = UUID("8a88ba32-1a82-5f10-9e27-1fe0d06bf67e")
_SHA256_PATTERN = re.compile(r"^[0-9a-f]{64}$")
_PART_PATTERN = re.compile(r"^part-[0-9a-f]{64}\.parquet$")
_GIT_COMMIT_PATTERN = re.compile(r"^[0-9a-f]{40}$")
_BATCH_FIELDS = frozenset(
    {
        "schema_version",
        "manifest_version",
        "feature_set",
        "feature_set_version",
        "registry_sha256",
        "batch",
        "calendar_pin",
        "decision_schedule",
        "universe",
        "computation_delay_seconds",
        "input_fitness",
        "input_fingerprint",
        "selected_input_manifests",
        "selected_input_parts",
        "row_count",
        "parts",
        "rejected",
        "run_summary",
    }
)
_BATCH_METADATA_FIELDS = frozenset(
    {"batch_id", "artifact_version", "generator_version", "git_commit", "created_at"}
)
_CALENDAR_FIELDS = frozenset(
    {
        "data_source_id",
        "mic",
        "calendar_version",
        "session_fingerprint",
        "calendar_available_at",
        "decision_clock_policy",
    }
)
_UNIVERSE_FIELDS = frozenset({"kind", "security_ids", "fingerprint"})
_FITNESS_FIELDS = frozenset({"dataset", "historical_fitness", "availability_policy"})
_INPUT_MANIFEST_FIELDS = frozenset({"path", "sha256"})
_INPUT_PART_FIELDS = frozenset({"path", "sha256"})
_PART_FIELDS = frozenset(
    {
        "security_id",
        "decision_at",
        "artifact_id",
        "manifest_path",
        "manifest_sha256",
        "part_path",
        "part_sha256",
        "row_count",
    }
)
_REJECT_FIELDS = frozenset({"security_id", "decision_at", "reason", "detail"})
_SUMMARY_FIELDS = frozenset(
    {
        "requested_partitions",
        "accepted_partitions",
        "rejected_partitions",
        "row_count",
        "status",
    }
)


class FeatureBatchError(ValueError):
    """Base error for invalid, unsupported, or conflicting feature batches."""


class FeatureBatchConflictError(FeatureBatchError):
    """Raised when an immutable batch identity already contains other content."""


class FeatureBatchValidationError(FeatureBatchError):
    """Raised when a batch manifest or referenced artifact fails validation."""


@dataclass(frozen=True)
class ValidatedFeatureBatch:
    """A validated batch manifest and its child artifact observations."""

    manifest_path: Path
    manifest: dict[str, Any]
    observations: tuple[dict[str, Any], ...]

    @property
    def row_count(self) -> int:
        return len(self.observations)


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
        raise FeatureBatchValidationError(f"invalid {label} {path}: {error}") from error
    if not isinstance(document, dict):
        raise FeatureBatchValidationError(f"invalid {label} {path}: expected a JSON object")
    return document


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
        raise FeatureBatchError(f"cannot canonicalize feature batch envelope: {error}") from error


def _sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def _sha256_file(path: Path, *, label: str) -> str:
    digest = hashlib.sha256()
    try:
        with path.open("rb") as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(chunk)
    except OSError as error:
        raise FeatureBatchValidationError(f"cannot read {label} {path}: {error}") from error
    return digest.hexdigest()


def _relative_path(path: Path, *, root: Path, field: str) -> str:
    try:
        relative = path.resolve().relative_to(root.resolve())
    except ValueError as error:
        raise FeatureBatchError(f"{field} {path} is outside root {root}") from error
    if not relative.parts or any(part in {"", ".", ".."} for part in relative.parts):
        raise FeatureBatchError(f"{field} {path} is not safely relative")
    return relative.as_posix()


def _safe_relative_path(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not value or Path(value).is_absolute():
        raise FeatureBatchValidationError(f"{field} must be a non-empty relative path")
    if "\\" in value or "\x00" in value or any(part in {"", ".", ".."} for part in Path(value).parts):
        raise FeatureBatchValidationError(f"{field} contains an unsafe path")
    return value


def _validate_sha256(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or _SHA256_PATTERN.fullmatch(value) is None:
        raise FeatureBatchValidationError(f"{field} must be a lower-case SHA-256")
    return value


def _canonical_schedule(value: Sequence[Any], *, field: str) -> list[str]:
    if not isinstance(value, Sequence) or isinstance(value, (str, bytes)) or not value:
        raise FeatureBatchError(f"{field} must be a non-empty timestamp list")
    result: list[str] = []
    for index, item in enumerate(value):
        try:
            canonical, _ = _canonical_timestamp(item, field=f"{field}[{index}]")
        except FeatureArtifactError as error:
            raise FeatureBatchError(str(error)) from error
        if item != canonical:
            raise FeatureBatchError(f"{field}[{index}] must be canonical UTC")
        result.append(canonical)
    if len(set(result)) != len(result):
        raise FeatureBatchError(f"{field} must contain unique timestamps")
    return sorted(result)


def _canonical_security_ids(value: Sequence[Any]) -> list[str]:
    if not isinstance(value, Sequence) or isinstance(value, (str, bytes)) or not value:
        raise FeatureBatchError("security_ids must be a non-empty list")
    result: list[str] = []
    for index, item in enumerate(value):
        try:
            canonical = _canonical_uuid(item, field=f"security_ids[{index}]")
        except FeatureArtifactError as error:
            raise FeatureBatchError(str(error)) from error
        if item != canonical:
            raise FeatureBatchError(f"security_ids[{index}] must be canonical UUID")
        result.append(canonical)
    if len(set(result)) != len(result):
        raise FeatureBatchError("security_ids must contain unique UUIDs")
    return sorted(result)


def _canonical_security_mappings(
    value: Mapping[str, Any] | Sequence[Any] | None,
    *,
    security_ids: Sequence[str],
) -> dict[str, str]:
    """Normalize an explicit security-to-issuer input for fundamental batches."""
    if value is None:
        return {}
    if isinstance(value, Mapping):
        items = value.items()
    elif isinstance(value, Sequence) and not isinstance(value, (str, bytes)):
        normalized_items: list[tuple[Any, Any]] = []
        for index, item in enumerate(value):
            if isinstance(item, Mapping):
                if set(item) != {"security_id", "issuer_id"}:
                    raise FeatureBatchError(
                        f"security_mappings[{index}] must contain security_id and issuer_id"
                    )
                normalized_items.append((item["security_id"], item["issuer_id"]))
                continue
            try:
                normalized_items.append((item.security_id, item.issuer_id))
            except AttributeError as error:
                raise FeatureBatchError(
                    f"security_mappings[{index}] must expose security_id and issuer_id"
                ) from error
        items = normalized_items
    else:
        raise FeatureBatchError("security_mappings must be an object or a list")

    result: dict[str, str] = {}
    universe = set(security_ids)
    for raw_security_id, raw_issuer_id in items:
        try:
            security_id = _canonical_uuid(raw_security_id, field="security_mappings.security_id")
            issuer_id = _canonical_uuid(raw_issuer_id, field="security_mappings.issuer_id")
        except FeatureArtifactError as error:
            raise FeatureBatchError(str(error)) from error
        if security_id not in universe:
            raise FeatureBatchError(
                f"security_mappings contains security outside the explicit universe: {security_id}"
            )
        if security_id in result:
            raise FeatureBatchError(f"security_mappings contains duplicate security_id {security_id}")
        result[security_id] = issuer_id
    return result


def _calendar_pin(value: Mapping[str, Any], *, decision_at: str) -> dict[str, str]:
    if not isinstance(value, Mapping) or set(value) != _CALENDAR_FIELDS:
        raise FeatureBatchError("calendar_pin has an unsupported field set")
    data_source_id = value["data_source_id"]
    mic = value["mic"]
    version = value["calendar_version"]
    fingerprint = value["session_fingerprint"]
    available = value["calendar_available_at"]
    policy = value["decision_clock_policy"]
    try:
        canonical_source = _canonical_uuid(data_source_id, field="calendar_pin.data_source_id")
        canonical_available, available_datetime = _canonical_timestamp(
            available, field="calendar_pin.calendar_available_at"
        )
        _, decision_datetime = _canonical_timestamp(decision_at, field="decision_at")
    except FeatureArtifactError as error:
        raise FeatureBatchError(str(error)) from error
    if data_source_id != canonical_source or available != canonical_available:
        raise FeatureBatchError("calendar_pin must use canonical UUID and UTC values")
    if not isinstance(mic, str) or not re.fullmatch(r"[A-Z0-9]{4}", mic):
        raise FeatureBatchError("calendar_pin.mic must be a canonical MIC")
    if not isinstance(version, str) or re.fullmatch(r"[a-z][a-z0-9_-]{1,63}", version) is None:
        raise FeatureBatchError("calendar_pin.calendar_version is invalid")
    if not isinstance(fingerprint, str) or _SHA256_PATTERN.fullmatch(fingerprint) is None:
        raise FeatureBatchError("calendar_pin.session_fingerprint must be a lower-case SHA-256")
    if policy != "after_close_next_session":
        raise FeatureBatchError("calendar_pin.decision_clock_policy is unsupported")
    if available_datetime > decision_datetime:
        raise FeatureBatchError("calendar_pin.calendar_available_at is after decision_at")
    return {
        "data_source_id": canonical_source,
        "mic": mic,
        "calendar_version": version,
        "session_fingerprint": fingerprint,
        "calendar_available_at": canonical_available,
        "decision_clock_policy": policy,
    }


def _universe_fingerprint(security_ids: Sequence[str]) -> str:
    return _sha256_bytes(
        _canonical_json({"kind": "explicit_security_list", "security_ids": list(security_ids)})
    )


def _input_fingerprint(document: Mapping[str, Any]) -> str:
    envelope = {
        "calendar_pin": document["calendar_pin"],
        "computation_delay_seconds": document["computation_delay_seconds"],
        "decision_schedule": document["decision_schedule"],
        "feature_set": document["feature_set"],
        "feature_set_version": document["feature_set_version"],
        "registry_sha256": document["registry_sha256"],
        "selected_input_manifests": sorted(
            document["selected_input_manifests"], key=lambda item: (item["path"], item["sha256"])
        ),
        "selected_input_parts": sorted(
            document["selected_input_parts"], key=lambda item: (item["path"], item["sha256"])
        ),
        "universe": document["universe"],
    }
    return _sha256_bytes(_canonical_json(envelope))


def _batch_id(input_fingerprint: str) -> str:
    return str(uuid5(_BATCH_NAMESPACE, input_fingerprint))


def _batch_directory(features_root: Path, feature_set: str, version: str, batch_id: str) -> Path:
    return features_root / "batches" / feature_set / version / f"batch-{batch_id}"


def _read_child_input_lineage(child_manifest: Mapping[str, Any]) -> tuple[list[dict[str, str]], list[dict[str, str]]]:
    manifests = [
        {"path": item["path"], "sha256": item["sha256"]}
        for item in child_manifest["selected_input_manifests"]
    ]
    parts = [
        {"path": item["path"], "sha256": item["sha256"]}
        for item in child_manifest["selected_input_parts"]
    ]
    return manifests, parts


def _validate_batch_metadata(value: Any) -> dict[str, Any]:
    if not isinstance(value, dict) or set(value) != _BATCH_METADATA_FIELDS:
        raise FeatureBatchValidationError("batch metadata has an unsupported field set")
    try:
        batch_id = _canonical_uuid(value["batch_id"], field="batch.batch_id")
    except FeatureBatchError as error:
        raise FeatureBatchValidationError(str(error)) from error
    if value["batch_id"] != batch_id:
        raise FeatureBatchValidationError("batch.batch_id must be canonical UUID")
    if value["artifact_version"] != BATCH_ARTIFACT_VERSION:
        raise FeatureBatchValidationError("batch.artifact_version is unsupported")
    if not isinstance(value["generator_version"], str) or not value["generator_version"]:
        raise FeatureBatchValidationError("batch.generator_version must be non-empty")
    if not isinstance(value["git_commit"], str) or not (
        value["git_commit"] == "unknown" or _GIT_COMMIT_PATTERN.fullmatch(value["git_commit"])
    ):
        raise FeatureBatchValidationError("batch.git_commit is invalid")
    try:
        canonical_created, _ = _canonical_timestamp(value["created_at"], field="batch.created_at")
    except FeatureArtifactError as error:
        raise FeatureBatchValidationError(str(error)) from error
    if value["created_at"] != canonical_created:
        raise FeatureBatchValidationError("batch.created_at is not canonical UTC")
    return value


def _validate_batch_manifest(document: dict[str, Any], *, path: Path) -> dict[str, Any]:
    if set(document) != _BATCH_FIELDS:
        raise FeatureBatchValidationError(f"batch manifest {path} has missing or unknown fields")
    if document["schema_version"] != BATCH_SCHEMA_VERSION:
        raise FeatureBatchValidationError("batch schema_version is unsupported")
    if document["manifest_version"] != BATCH_MANIFEST_VERSION:
        raise FeatureBatchValidationError("batch manifest_version is unsupported")
    for field in ("feature_set", "feature_set_version"):
        if not isinstance(document[field], str) or not document[field]:
            raise FeatureBatchValidationError(f"batch {field} must be non-empty")
    _validate_sha256(document["registry_sha256"], field="batch.registry_sha256")
    _validate_batch_metadata(document["batch"])
    if not isinstance(document["calendar_pin"], dict) or set(document["calendar_pin"]) != _CALENDAR_FIELDS:
        raise FeatureBatchValidationError("batch calendar_pin is invalid")

    schedule = document["decision_schedule"]
    if not isinstance(schedule, list) or not schedule:
        raise FeatureBatchValidationError("batch decision_schedule must not be empty")
    if schedule != sorted(schedule) or len(set(schedule)) != len(schedule):
        raise FeatureBatchValidationError("batch decision_schedule must be sorted and unique")
    for index, value in enumerate(schedule):
        try:
            canonical, _ = _canonical_timestamp(value, field=f"batch.decision_schedule[{index}]")
        except FeatureArtifactError as error:
            raise FeatureBatchValidationError(str(error)) from error
        if value != canonical:
            raise FeatureBatchValidationError("batch decision_schedule contains non-canonical UTC")

    universe = document["universe"]
    if not isinstance(universe, dict) or set(universe) != _UNIVERSE_FIELDS:
        raise FeatureBatchValidationError("batch universe is invalid")
    if universe["kind"] != "explicit_security_list":
        raise FeatureBatchValidationError("batch universe kind is unsupported")
    security_ids = universe["security_ids"]
    if not isinstance(security_ids, list) or not security_ids or security_ids != sorted(security_ids):
        raise FeatureBatchValidationError("batch universe security_ids must be sorted and non-empty")
    if len(set(security_ids)) != len(security_ids):
        raise FeatureBatchValidationError("batch universe security_ids must be unique")
    for value in security_ids:
        try:
            canonical = _canonical_uuid(value, field="batch.universe.security_ids")
        except FeatureArtifactError as error:
            raise FeatureBatchValidationError(str(error)) from error
        if value != canonical:
            raise FeatureBatchValidationError("batch universe security_id is not canonical")
    _validate_sha256(universe["fingerprint"], field="batch.universe.fingerprint")
    if universe["fingerprint"] != _universe_fingerprint(security_ids):
        raise FeatureBatchValidationError("batch universe fingerprint is invalid")

    delay = document["computation_delay_seconds"]
    if not isinstance(delay, int) or isinstance(delay, bool) or delay < 0:
        raise FeatureBatchValidationError("batch computation delay is invalid")

    fitness = document["input_fitness"]
    if not isinstance(fitness, list) or not fitness:
        raise FeatureBatchValidationError("batch input_fitness must not be empty")
    seen_fitness: set[str] = set()
    for index, item in enumerate(fitness):
        if not isinstance(item, dict) or set(item) != _FITNESS_FIELDS:
            raise FeatureBatchValidationError(f"batch input_fitness[{index}] is invalid")
        dataset = item["dataset"]
        if not isinstance(dataset, str) or re.fullmatch(r"[a-z][a-z0-9_-]{1,63}", dataset) is None:
            raise FeatureBatchValidationError(f"batch input_fitness[{index}].dataset is invalid")
        if dataset in seen_fitness:
            raise FeatureBatchValidationError("batch input_fitness datasets must be unique")
        seen_fitness.add(dataset)
        if item["historical_fitness"] not in {
            "backtest_safe",
            "current_research_only",
            "installation_replay_only",
            "unsupported",
        }:
            raise FeatureBatchValidationError(f"batch input_fitness[{index}].historical_fitness is invalid")
        if item["availability_policy"] not in {
            "exact_publication",
            "source_declared",
            "conservative_receipt_time",
            "current_snapshot",
            "unknown",
        }:
            raise FeatureBatchValidationError(f"batch input_fitness[{index}].availability_policy is invalid")

    _validate_sha256(document["input_fingerprint"], field="batch.input_fingerprint")
    for field, item_fields in (
        ("selected_input_manifests", _INPUT_MANIFEST_FIELDS),
        ("selected_input_parts", _INPUT_PART_FIELDS),
    ):
        values = document[field]
        if not isinstance(values, list):
            raise FeatureBatchValidationError(f"batch {field} must be an array")
        seen: set[tuple[str, str]] = set()
        for index, item in enumerate(values):
            if not isinstance(item, dict) or set(item) != item_fields:
                raise FeatureBatchValidationError(f"batch {field}[{index}] is invalid")
            item_path = _safe_relative_path(item["path"], field=f"batch {field}[{index}].path")
            if field == "selected_input_parts" and _PART_PATTERN.fullmatch(item_path) is None:
                raise FeatureBatchValidationError(f"batch {field}[{index}].path is invalid")
            item_sha = _validate_sha256(item["sha256"], field=f"batch {field}[{index}].sha256")
            key = (item_path, item_sha)
            if key in seen:
                raise FeatureBatchValidationError(f"batch {field} contains duplicates")
            seen.add(key)

    parts = document["parts"]
    if not isinstance(parts, list):
        raise FeatureBatchValidationError("batch parts must be an array")
    seen_partitions: set[tuple[str, str]] = set()
    output_rows = 0
    for index, item in enumerate(parts):
        if not isinstance(item, dict) or set(item) != _PART_FIELDS:
            raise FeatureBatchValidationError(f"batch parts[{index}] is invalid")
        try:
            security_id = _canonical_uuid(item["security_id"], field=f"batch parts[{index}].security_id")
            decision_at, _ = _canonical_timestamp(item["decision_at"], field=f"batch parts[{index}].decision_at")
            artifact_id = _canonical_uuid(item["artifact_id"], field=f"batch parts[{index}].artifact_id")
        except FeatureArtifactError as error:
            raise FeatureBatchValidationError(str(error)) from error
        if item["security_id"] != security_id or item["decision_at"] != decision_at or item["artifact_id"] != artifact_id:
            raise FeatureBatchValidationError(f"batch parts[{index}] contains non-canonical identity")
        partition = (decision_at, security_id)
        if partition in seen_partitions:
            raise FeatureBatchValidationError("batch parts contain duplicate partitions")
        seen_partitions.add(partition)
        for field in ("manifest_path", "part_path"):
            _safe_relative_path(item[field], field=f"batch parts[{index}].{field}")
        _validate_sha256(item["manifest_sha256"], field=f"batch parts[{index}].manifest_sha256")
        _validate_sha256(item["part_sha256"], field=f"batch parts[{index}].part_sha256")
        row_count = item["row_count"]
        if not isinstance(row_count, int) or isinstance(row_count, bool) or row_count < 0:
            raise FeatureBatchValidationError(f"batch parts[{index}].row_count is invalid")
        output_rows += row_count
    if document["row_count"] != output_rows:
        raise FeatureBatchValidationError("batch row_count does not equal part row counts")
    if not isinstance(document["row_count"], int) or isinstance(document["row_count"], bool):
        raise FeatureBatchValidationError("batch row_count is invalid")

    rejected = document["rejected"]
    if not isinstance(rejected, list):
        raise FeatureBatchValidationError("batch rejected must be an array")
    for index, item in enumerate(rejected):
        if not isinstance(item, dict) or set(item) != _REJECT_FIELDS:
            raise FeatureBatchValidationError(f"batch rejected[{index}] is invalid")
        try:
            security_id = _canonical_uuid(item["security_id"], field=f"batch rejected[{index}].security_id")
            decision_at, _ = _canonical_timestamp(item["decision_at"], field=f"batch rejected[{index}].decision_at")
        except FeatureArtifactError as error:
            raise FeatureBatchValidationError(str(error)) from error
        if item["security_id"] != security_id or item["decision_at"] != decision_at:
            raise FeatureBatchValidationError(f"batch rejected[{index}] contains non-canonical identity")
        if not isinstance(item["reason"], str) or re.fullmatch(r"[a-z][a-z0-9_-]{1,63}", item["reason"]) is None:
            raise FeatureBatchValidationError(f"batch rejected[{index}].reason is invalid")
        if not isinstance(item["detail"], str) or not item["detail"]:
            raise FeatureBatchValidationError(f"batch rejected[{index}].detail is invalid")
    if len({(item["decision_at"], item["security_id"]) for item in rejected}) != len(rejected):
        raise FeatureBatchValidationError("batch rejected contains duplicate partitions")

    summary = document["run_summary"]
    if not isinstance(summary, dict) or set(summary) != _SUMMARY_FIELDS:
        raise FeatureBatchValidationError("batch run_summary is invalid")
    for field in ("requested_partitions", "accepted_partitions", "rejected_partitions", "row_count"):
        if not isinstance(summary[field], int) or isinstance(summary[field], bool) or summary[field] < 0:
            raise FeatureBatchValidationError(f"batch run_summary.{field} is invalid")
    if summary["status"] != "completed":
        raise FeatureBatchValidationError("batch run_summary.status is unsupported")
    if summary["requested_partitions"] != len(parts) + len(rejected):
        raise FeatureBatchValidationError("batch run_summary partition counts are inconsistent")
    if summary["accepted_partitions"] != len(parts) or summary["rejected_partitions"] != len(rejected):
        raise FeatureBatchValidationError("batch run_summary accepted/rejected counts are inconsistent")
    if summary["row_count"] != document["row_count"]:
        raise FeatureBatchValidationError("batch run_summary row_count is inconsistent")
    if document["input_fingerprint"] != _input_fingerprint(document):
        raise FeatureBatchValidationError("batch input_fingerprint is invalid")
    return document


def read_feature_batch(
    manifest_path: str | Path,
    *,
    features_root: str | Path,
    registry: FeatureRegistry | None = None,
) -> ValidatedFeatureBatch:
    """Validate one batch manifest and every immutable child artifact it references."""
    path = Path(manifest_path).expanduser().resolve()
    if path.name != "manifest.json" or not path.is_file():
        raise FeatureBatchValidationError(f"feature batch manifest {path} does not exist")
    document = _validate_batch_manifest(_read_json(path, label="feature batch manifest"), path=path)
    if registry is not None:
        if registry.registry_sha256 != document["registry_sha256"]:
            raise FeatureBatchValidationError("batch registry fingerprint differs from supplied registry")
        try:
            definition = registry.resolve(document["feature_set"], document["feature_set_version"])
        except FeatureRegistryError as error:
            raise FeatureBatchValidationError(str(error)) from error
        expected_fitness = [
            {
                "dataset": item.dataset,
                "historical_fitness": item.historical_fitness,
                "availability_policy": item.availability_policy,
            }
            for item in definition.inputs
        ]
        if document["input_fitness"] != expected_fitness:
            raise FeatureBatchValidationError("batch input_fitness differs from registry")
        if document["computation_delay_seconds"] != definition.computation.delay_seconds:
            raise FeatureBatchValidationError("batch computation delay differs from registry")

    root = Path(features_root).expanduser().resolve()
    try:
        unexpected = sorted(
            item.name for item in path.parent.iterdir() if item.name != "manifest.json"
        )
    except OSError as error:
        raise FeatureBatchValidationError(f"cannot inspect feature batch directory {path.parent}: {error}") from error
    if unexpected:
        raise FeatureBatchValidationError(
            f"feature batch contains unlisted file(s): {', '.join(unexpected)}"
        )

    observations: list[dict[str, Any]] = []
    manifests: set[tuple[str, str]] = set()
    parts: set[tuple[str, str]] = set()
    schedule = set(document["decision_schedule"])
    universe = set(document["universe"]["security_ids"])
    for index, item in enumerate(document["parts"]):
        manifest_relative = _safe_relative_path(item["manifest_path"], field=f"batch parts[{index}].manifest_path")
        child_path = (root / manifest_relative).resolve()
        try:
            child_path.relative_to(root)
        except ValueError as error:
            raise FeatureBatchValidationError(f"batch child manifest escapes features root: {manifest_relative}") from error
        if _sha256_file(child_path, label="child feature manifest") != item["manifest_sha256"]:
            raise FeatureBatchValidationError(f"batch child manifest hash mismatch: {manifest_relative}")
        try:
            child = read_feature_artifact(child_path)
        except FeatureArtifactError as error:
            raise FeatureBatchValidationError(
                f"batch child artifact {manifest_relative} is invalid: {error}"
            ) from error
        child_manifest = child.manifest
        if (
            child_manifest["feature_set"] != document["feature_set"]
            or child_manifest["feature_set_version"] != document["feature_set_version"]
        ):
            raise FeatureBatchValidationError(f"batch part {index} feature contract differs from child manifest")
        if child_manifest["artifact"]["artifact_id"] != item["artifact_id"]:
            raise FeatureBatchValidationError(f"batch part {index} artifact id differs from child manifest")
        if len(child.observations) != item["row_count"]:
            raise FeatureBatchValidationError(f"batch part {index} row count differs from child artifact")
        if not child.observations:
            raise FeatureBatchValidationError(f"batch part {index} child artifact is empty")
        for observation in child.observations:
            if observation["security_id"] != item["security_id"] or observation["decision_at"] != item["decision_at"]:
                raise FeatureBatchValidationError(f"batch part {index} identity differs from child observation")
        child_parts = child_manifest["parts"]
        if len(child_parts) != 1:
            raise FeatureBatchValidationError(f"batch part {index} must reference one child output part")
        child_part = child_parts[0]
        part_relative = _safe_relative_path(item["part_path"], field=f"batch parts[{index}].part_path")
        expected_part = _relative_path(child.part_paths[0], root=root, field="child part")
        if part_relative != expected_part or item["part_sha256"] != child_part["sha256"]:
            raise FeatureBatchValidationError(f"batch part {index} output part differs from child manifest")
        manifests.update((entry["path"], entry["sha256"]) for entry in child_manifest["selected_input_manifests"])
        parts.update((entry["path"], entry["sha256"]) for entry in child_manifest["selected_input_parts"])
        observations.extend(child.observations)
        if item["decision_at"] not in schedule or item["security_id"] not in universe:
            raise FeatureBatchValidationError(f"batch part {index} is outside its declared inputs")

    actual_manifests = {(item["path"], item["sha256"]) for item in document["selected_input_manifests"]}
    actual_parts = {(item["path"], item["sha256"]) for item in document["selected_input_parts"]}
    if manifests != actual_manifests or parts != actual_parts:
        raise FeatureBatchValidationError("batch input lineage differs from child artifacts")
    if len(observations) != document["row_count"]:
        raise FeatureBatchValidationError("batch row_count does not match child observations")
    expected_batch_id = _batch_id(document["input_fingerprint"])
    if document["batch"]["batch_id"] != expected_batch_id:
        raise FeatureBatchValidationError("batch identity does not match its input fingerprint")
    return ValidatedFeatureBatch(path, document, tuple(observations))


def validate_feature_batch(
    manifest_path: str | Path,
    *,
    features_root: str | Path,
    registry: FeatureRegistry | None = None,
) -> ValidatedFeatureBatch:
    """Validate and return one feature batch."""
    return read_feature_batch(manifest_path, features_root=features_root, registry=registry)


def _write_exclusive(path: Path, content: bytes, *, label: str) -> None:
    try:
        with path.open("xb") as stream:
            stream.write(content)
    except FileExistsError as error:
        raise FeatureBatchConflictError(f"{label} already exists: {path}") from error
    except OSError as error:
        raise FeatureBatchError(f"cannot write {label} {path}: {error}") from error


def publish_feature_batch(
    catalog: Any,
    *,
    registry: FeatureRegistry,
    security_ids: Sequence[str],
    decision_ats: Sequence[str],
    calendar_pin: Mapping[str, Any],
    features_root: str | Path = _DEFAULT_FEATURE_ROOT,
    feature_set: str = "market-basic",
    feature_set_version: str = "1.0.0",
    git_commit: str = "unknown",
    security_mappings: Mapping[str, Any] | Sequence[Any] | None = None,
    taxonomy_registry: Any = None,
    macro_source: str | None = None,
    macro_series_id: str | None = None,
    macro_geography: str | None = None,
    macro_unit: str | None = None,
    macro_frequency: str | None = None,
) -> Path:
    """Publish or resume a deterministic dataset-level batch.

    Each security/decision partition is an immutable child artifact. The batch
    manifest is installed only after every accepted child has been validated, so a
    rerun can safely reuse children left behind by an interrupted process.
    """
    try:
        definition = registry.resolve(feature_set, feature_set_version)
    except FeatureRegistryError as error:
        raise FeatureBatchError(str(error)) from error
    supported_datasets = {"prices", "fundamentals", "macroeconomics"}
    unsupported_datasets = set(definition.required_datasets) - supported_datasets
    if unsupported_datasets:
        raise FeatureBatchError(
            f"{feature_set} batch requires unsupported input dataset(s): "
            f"{', '.join(sorted(unsupported_datasets))}"
        )
    if not isinstance(git_commit, str) or not (git_commit == "unknown" or _GIT_COMMIT_PATTERN.fullmatch(git_commit)):
        raise FeatureBatchError("git_commit must be a lower-case SHA-1 or 'unknown'")

    canonical_security_ids = _canonical_security_ids(security_ids)
    canonical_mappings = _canonical_security_mappings(
        security_mappings,
        security_ids=canonical_security_ids,
    )
    canonical_schedule = _canonical_schedule(decision_ats, field="decision_ats")
    canonical_calendar = _calendar_pin(calendar_pin, decision_at=canonical_schedule[0])
    for decision_at in canonical_schedule[1:]:
        _calendar_pin(canonical_calendar, decision_at=decision_at)

    accepted: list[dict[str, Any]] = []
    rejected: list[dict[str, str]] = []
    input_manifests: dict[tuple[str, str], dict[str, str]] = {}
    input_parts: dict[tuple[str, str], dict[str, str]] = {}
    root = Path(features_root).expanduser().resolve()
    for decision_at in canonical_schedule:
        for security_id in canonical_security_ids:
            if "fundamentals" in definition.required_datasets:
                issuer_id = canonical_mappings.get(security_id)
                if issuer_id is None:
                    rejected.append(
                        {
                            "security_id": security_id,
                            "decision_at": decision_at,
                            "reason": "missing_security_mapping",
                            "detail": "fundamental-growth requires an explicit security-to-issuer mapping",
                        }
                    )
                    continue
            else:
                issuer_id = None
            try:
                child_path = publish_feature_artifact(
                    catalog,
                    decision_at=decision_at,
                    security_id=security_id,
                    calendar_pin=canonical_calendar,
                    features_root=root,
                    computation_delay_seconds=definition.computation.delay_seconds,
                    generator_version=definition.generator.version,
                    git_commit=git_commit,
                    feature_set=feature_set,
                    feature_set_version=feature_set_version,
                    issuer_id=issuer_id,
                    taxonomy_registry=taxonomy_registry,
                    macro_source=macro_source,
                    macro_series_id=macro_series_id,
                    macro_geography=macro_geography,
                    macro_unit=macro_unit,
                    macro_frequency=macro_frequency,
                )
                child = read_feature_artifact(child_path)
            except (FeatureArtifactConflictError, FeatureArtifactValidationError):
                raise
            except FeatureArtifactError as error:
                rejected.append(
                    {
                        "security_id": security_id,
                        "decision_at": decision_at,
                        "reason": "input_rejected",
                        "detail": str(error),
                    }
                )
                continue

            child_manifest = child.manifest
            child_part = child_manifest["parts"]
            if len(child_part) != 1:
                raise FeatureBatchError(
                    f"{feature_set} child artifact must contain one output part"
                )
            part = child_part[0]
            manifest_relative = _relative_path(child_path, root=root, field="child manifest")
            part_relative = _relative_path(child.part_paths[0], root=root, field="child part")
            accepted.append(
                {
                    "security_id": security_id,
                    "decision_at": decision_at,
                    "artifact_id": child_manifest["artifact"]["artifact_id"],
                    "manifest_path": manifest_relative,
                    "manifest_sha256": _sha256_file(child_path, label="child feature manifest"),
                    "part_path": part_relative,
                    "part_sha256": part["sha256"],
                    "row_count": int(part["row_count"]),
                }
            )
            for item in child_manifest["selected_input_manifests"]:
                key = (item["path"], item["sha256"])
                input_manifests[key] = {"path": item["path"], "sha256": item["sha256"]}
            for item in child_manifest["selected_input_parts"]:
                key = (item["path"], item["sha256"])
                input_parts[key] = {"path": item["path"], "sha256": item["sha256"]}

    accepted.sort(key=lambda item: (item["decision_at"], item["security_id"]))
    rejected.sort(key=lambda item: (item["decision_at"], item["security_id"]))
    selected_manifests = sorted(input_manifests.values(), key=lambda item: (item["path"], item["sha256"]))
    selected_parts = sorted(input_parts.values(), key=lambda item: (item["path"], item["sha256"]))
    input_fitness = [
        {
            "dataset": item.dataset,
            "historical_fitness": item.historical_fitness,
            "availability_policy": item.availability_policy,
        }
        for item in definition.inputs
    ]
    universe = {
        "kind": "explicit_security_list",
        "security_ids": canonical_security_ids,
        "fingerprint": _universe_fingerprint(canonical_security_ids),
    }
    fingerprint_document = {
        "calendar_pin": canonical_calendar,
        "computation_delay_seconds": definition.computation.delay_seconds,
        "decision_schedule": canonical_schedule,
        "feature_set": feature_set,
        "feature_set_version": feature_set_version,
        "registry_sha256": registry.registry_sha256,
        "selected_input_manifests": selected_manifests,
        "selected_input_parts": selected_parts,
        "universe": universe,
    }
    input_fingerprint = _sha256_bytes(_canonical_json(fingerprint_document))
    batch_id = _batch_id(input_fingerprint)
    availability_candidates = [canonical_calendar["calendar_available_at"]]
    availability_candidates.extend(item["decision_at"] for item in accepted)
    created_at = max(availability_candidates)
    manifest = {
        "schema_version": BATCH_SCHEMA_VERSION,
        "manifest_version": BATCH_MANIFEST_VERSION,
        "feature_set": feature_set,
        "feature_set_version": feature_set_version,
        "registry_sha256": registry.registry_sha256,
        "batch": {
            "batch_id": batch_id,
            "artifact_version": BATCH_ARTIFACT_VERSION,
            "generator_version": definition.generator.version,
            "git_commit": git_commit,
            "created_at": created_at,
        },
        "calendar_pin": canonical_calendar,
        "decision_schedule": canonical_schedule,
        "universe": universe,
        "computation_delay_seconds": definition.computation.delay_seconds,
        "input_fitness": input_fitness,
        "input_fingerprint": input_fingerprint,
        "selected_input_manifests": selected_manifests,
        "selected_input_parts": selected_parts,
        "row_count": sum(item["row_count"] for item in accepted),
        "parts": accepted,
        "rejected": rejected,
        "run_summary": {
            "requested_partitions": len(canonical_security_ids) * len(canonical_schedule),
            "accepted_partitions": len(accepted),
            "rejected_partitions": len(rejected),
            "row_count": sum(item["row_count"] for item in accepted),
            "status": "completed",
        },
    }
    directory = _batch_directory(root, feature_set, feature_set_version, batch_id)
    manifest_path = directory / "manifest.json"
    if directory.exists():
        if not directory.is_dir() or not manifest_path.is_file():
            raise FeatureBatchConflictError(f"feature batch directory is incomplete: {directory}")
        existing = read_feature_batch(manifest_path, features_root=root, registry=registry)
        if existing.manifest != manifest:
            raise FeatureBatchConflictError(f"immutable feature batch {batch_id} conflicts with existing content")
        return manifest_path
    try:
        directory.mkdir(parents=True, exist_ok=False)
    except FileExistsError as error:
        raise FeatureBatchConflictError(f"feature batch identity appeared during publication: {directory}") from error
    except OSError as error:
        raise FeatureBatchError(f"cannot create feature batch directory {directory}: {error}") from error
    manifest_bytes = (
        json.dumps(manifest, ensure_ascii=False, indent=2, sort_keys=False, allow_nan=False) + "\n"
    ).encode("utf-8")
    _write_exclusive(manifest_path, manifest_bytes, label="feature batch manifest")
    return manifest_path


__all__ = [
    "BATCH_ARTIFACT_VERSION",
    "BATCH_MANIFEST_VERSION",
    "BATCH_SCHEMA_VERSION",
    "FeatureBatchConflictError",
    "FeatureBatchError",
    "FeatureBatchValidationError",
    "ValidatedFeatureBatch",
    "publish_feature_batch",
    "read_feature_batch",
    "validate_feature_batch",
]
