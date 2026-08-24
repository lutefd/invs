"""Immutable point-in-time bias-audit artifacts for bounded research slices."""

from __future__ import annotations

import hashlib
import json
import re
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Any, Final
from uuid import UUID, uuid5

SCHEMA_VERSION: Final[str] = "1.0.0"
GENERATOR_VERSION: Final[str] = "python-bias-audit-1.0.0"

_ARTIFACT_NAMESPACE = UUID("4582dfbe-b785-5ed7-8a65-e5cc1251bf95")
_SHA256 = re.compile(r"^[0-9a-f]{64}$")
_REGIONS = frozenset({"US", "BR"})
_CLASSIFICATIONS = frozenset(
    {"backtest_safe", "current_research_only", "installation_replay_only", "unsupported"}
)
_KINDS = frozenset(
    {
        "availability_transition",
        "blocked_before_receipt",
        "removal_transition",
        "revision_transition",
        "unsupported_blocks",
    }
)
_REQUIRED_CATEGORIES = {
    "US": frozenset(
        {
            "identity",
            "membership",
            "calendar",
            "price",
            "macro_vintage",
            "corporate_action",
            "filing",
        }
    ),
    "BR": frozenset({"identity", "membership", "calendar", "price", "corporate_action", "fx"}),
}
_EXPECTED_STATES = {
    "availability_transition": ((False, "absent"), (True, "eligible"), (True, "eligible")),
    "blocked_before_receipt": ((False, "absent"), (True, "eligible"), (True, "eligible")),
    "removal_transition": ((True, "eligible"), (False, "absent"), (False, "absent")),
    "revision_transition": (
        (True, "prior_revision"),
        (True, "eligible"),
        (True, "eligible"),
    ),
    "unsupported_blocks": ((False, "absent"), (False, "unsupported"), (False, "unsupported")),
}


class BiasAuditError(ValueError):
    """Raised when a bias audit cannot be published or validated safely."""


class BiasAuditConflictError(BiasAuditError):
    """Raised when an immutable artifact identity already contains other bytes."""


class BiasAuditValidationError(BiasAuditError):
    """Raised when an audit specification or artifact is invalid."""


@dataclass(frozen=True)
class ValidatedBiasAudit:
    manifest_path: Path
    manifest: dict[str, Any]


def _json_object_without_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise BiasAuditValidationError(f"duplicate JSON key {key!r}")
        result[key] = value
    return result


def _read_json(path: Path, *, label: str) -> dict[str, Any]:
    try:
        document = json.loads(
            path.read_text(encoding="utf-8"),
            object_pairs_hook=_json_object_without_duplicates,
            parse_constant=lambda value: (_ for _ in ()).throw(
                BiasAuditValidationError(f"invalid JSON constant {value}")
            ),
        )
    except (OSError, UnicodeError, ValueError) as error:
        raise BiasAuditValidationError(f"invalid {label} {path}: {error}") from error
    if not isinstance(document, dict):
        raise BiasAuditValidationError(f"invalid {label} {path}: expected object")
    return document


def _canonical_json(value: Any) -> bytes:
    return json.dumps(
        value,
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
        allow_nan=False,
    ).encode("utf-8")


def _sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def _sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    try:
        with path.open("rb") as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(chunk)
    except OSError as error:
        raise BiasAuditValidationError(f"cannot hash evidence artifact {path}: {error}") from error
    return digest.hexdigest()


def _exact_fields(value: dict[str, Any], expected: frozenset[str], *, label: str) -> None:
    actual = frozenset(value)
    if actual != expected:
        missing = sorted(expected - actual)
        extra = sorted(actual - expected)
        raise BiasAuditValidationError(f"{label} fields mismatch: missing={missing}, extra={extra}")


def _timestamp(value: Any, *, field: str) -> tuple[str, datetime]:
    if not isinstance(value, str):
        raise BiasAuditValidationError(f"{field} must be an RFC 3339 UTC string")
    try:
        parsed = datetime.fromisoformat(value[:-1] + "+00:00" if value.endswith("Z") else value)
    except ValueError as error:
        raise BiasAuditValidationError(f"{field} must be an RFC 3339 UTC string") from error
    if parsed.tzinfo is None or parsed.utcoffset() is None:
        raise BiasAuditValidationError(f"{field} must include a timezone")
    utc = parsed.astimezone(UTC)
    fraction = f".{utc.microsecond:06d}".rstrip("0") if utc.microsecond else ""
    return utc.strftime("%Y-%m-%dT%H:%M:%S") + fraction + "Z", utc


def _nonempty_string(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise BiasAuditValidationError(f"{field} must be a non-empty string")
    return value


def _validate_spec(spec_path: Path) -> tuple[dict[str, Any], list[dict[str, str]], str]:
    spec = _read_json(spec_path, label="bias-audit specification")
    _exact_fields(
        spec,
        frozenset({"schema_version", "audit_id", "git_commit", "datasets", "artifacts", "probes"}),
        label="specification",
    )
    if spec["schema_version"] != SCHEMA_VERSION:
        raise BiasAuditValidationError(f"unsupported bias-audit schema {spec['schema_version']!r}")
    _nonempty_string(spec["audit_id"], field="audit_id")
    git_commit = _nonempty_string(spec["git_commit"], field="git_commit")
    if not re.fullmatch(r"[0-9a-f]{40}", git_commit):
        raise BiasAuditValidationError("git_commit must be a lowercase full commit SHA")

    datasets = spec["datasets"]
    if not isinstance(datasets, list) or not datasets:
        raise BiasAuditValidationError("datasets must be a non-empty array")
    dataset_by_id: dict[str, dict[str, Any]] = {}
    for index, dataset in enumerate(datasets):
        if not isinstance(dataset, dict):
            raise BiasAuditValidationError(f"datasets[{index}] must be an object")
        _exact_fields(
            dataset,
            frozenset({"id", "classification", "availability_policy", "scope_decision"}),
            label=f"datasets[{index}]",
        )
        dataset_id = _nonempty_string(dataset["id"], field=f"datasets[{index}].id")
        if dataset_id in dataset_by_id:
            raise BiasAuditValidationError(f"duplicate dataset id {dataset_id!r}")
        classification = dataset["classification"]
        if classification not in _CLASSIFICATIONS:
            raise BiasAuditValidationError(f"dataset {dataset_id!r} has invalid classification")
        _nonempty_string(
            dataset["availability_policy"], field=f"dataset {dataset_id}.availability_policy"
        )
        _nonempty_string(dataset["scope_decision"], field=f"dataset {dataset_id}.scope_decision")
        dataset_by_id[dataset_id] = dataset

    artifacts = spec["artifacts"]
    if not isinstance(artifacts, list) or not artifacts:
        raise BiasAuditValidationError("artifacts must be a non-empty array")
    artifact_by_id: dict[str, dict[str, Any]] = {}
    verified_artifacts: list[dict[str, str]] = []
    for index, artifact in enumerate(artifacts):
        if not isinstance(artifact, dict):
            raise BiasAuditValidationError(f"artifacts[{index}] must be an object")
        _exact_fields(
            artifact,
            frozenset({"id", "path", "sha256"}),
            label=f"artifacts[{index}]",
        )
        artifact_id = _nonempty_string(artifact["id"], field=f"artifacts[{index}].id")
        if artifact_id in artifact_by_id:
            raise BiasAuditValidationError(f"duplicate artifact id {artifact_id!r}")
        path_text = _nonempty_string(artifact["path"], field=f"artifact {artifact_id}.path")
        expected_hash = artifact["sha256"]
        if not isinstance(expected_hash, str) or not _SHA256.fullmatch(expected_hash):
            raise BiasAuditValidationError(f"artifact {artifact_id!r} has invalid SHA-256")
        candidate = Path(path_text).expanduser()
        path = candidate if candidate.is_absolute() else spec_path.parent / candidate
        path = path.resolve()
        actual_hash = _sha256_file(path)
        if actual_hash != expected_hash:
            raise BiasAuditValidationError(
                f"artifact {artifact_id!r} hash mismatch: expected {expected_hash}, got {actual_hash}"
            )
        artifact_by_id[artifact_id] = artifact
        verified_artifacts.append({"id": artifact_id, "path": str(path), "sha256": actual_hash})

    probes = spec["probes"]
    if not isinstance(probes, list) or not probes:
        raise BiasAuditValidationError("probes must be a non-empty array")
    probe_ids: set[str] = set()
    observed_categories = {region: set() for region in _REGIONS}
    used_datasets: set[str] = set()
    for index, probe in enumerate(probes):
        if not isinstance(probe, dict):
            raise BiasAuditValidationError(f"probes[{index}] must be an object")
        _exact_fields(
            probe,
            frozenset(
                {
                    "id",
                    "region",
                    "category",
                    "dataset_id",
                    "kind",
                    "boundary_at",
                    "before",
                    "at",
                    "after",
                    "evidence_artifact_ids",
                }
            ),
            label=f"probes[{index}]",
        )
        probe_id = _nonempty_string(probe["id"], field=f"probes[{index}].id")
        if probe_id in probe_ids:
            raise BiasAuditValidationError(f"duplicate probe id {probe_id!r}")
        probe_ids.add(probe_id)
        region = probe["region"]
        if region not in _REGIONS:
            raise BiasAuditValidationError(f"probe {probe_id!r} has invalid region")
        category = _nonempty_string(probe["category"], field=f"probe {probe_id}.category")
        if category not in _REQUIRED_CATEGORIES[region]:
            raise BiasAuditValidationError(
                f"probe {probe_id!r} has unexpected category {category!r}"
            )
        if category in observed_categories[region]:
            raise BiasAuditValidationError(f"duplicate {region} category probe {category!r}")
        observed_categories[region].add(category)
        dataset_id = probe["dataset_id"]
        if dataset_id not in dataset_by_id:
            raise BiasAuditValidationError(f"probe {probe_id!r} references unknown dataset")
        used_datasets.add(dataset_id)
        kind = probe["kind"]
        if kind not in _KINDS:
            raise BiasAuditValidationError(f"probe {probe_id!r} has invalid kind")
        classification = dataset_by_id[dataset_id]["classification"]
        if kind == "blocked_before_receipt" and classification != "installation_replay_only":
            raise BiasAuditValidationError(
                f"probe {probe_id!r} must use an installation_replay_only dataset"
            )
        if kind == "unsupported_blocks" and classification not in {
            "installation_replay_only",
            "unsupported",
        }:
            raise BiasAuditValidationError(f"probe {probe_id!r} must use a blocking dataset")
        if kind in {"availability_transition", "removal_transition", "revision_transition"} and (
            classification != "backtest_safe"
        ):
            raise BiasAuditValidationError(f"probe {probe_id!r} cannot claim a safe transition")

        boundary_text, boundary = _timestamp(
            probe["boundary_at"], field=f"probe {probe_id}.boundary_at"
        )
        expected_times = (
            boundary - timedelta(microseconds=1),
            boundary,
            boundary + timedelta(microseconds=1),
        )
        for position, field in enumerate(("before", "at", "after")):
            result = probe[field]
            if not isinstance(result, dict):
                raise BiasAuditValidationError(f"probe {probe_id}.{field} must be an object")
            _exact_fields(
                result,
                frozenset({"decision_at", "eligible", "state"}),
                label=f"probe {probe_id}.{field}",
            )
            timestamp_text, timestamp = _timestamp(
                result["decision_at"], field=f"probe {probe_id}.{field}.decision_at"
            )
            if timestamp != expected_times[position]:
                raise BiasAuditValidationError(
                    f"probe {probe_id}.{field} must be exactly one microsecond from {boundary_text}"
                )
            expected_eligible, expected_state = _EXPECTED_STATES[kind][position]
            if result["eligible"] is not expected_eligible or result["state"] != expected_state:
                raise BiasAuditValidationError(
                    f"probe {probe_id}.{field} must be eligible={expected_eligible} state={expected_state!r}"
                )
            if timestamp_text != result["decision_at"]:
                raise BiasAuditValidationError(
                    f"probe {probe_id}.{field}.decision_at must be canonical UTC"
                )

        evidence_ids = probe["evidence_artifact_ids"]
        if not isinstance(evidence_ids, list) or not evidence_ids:
            raise BiasAuditValidationError(f"probe {probe_id!r} must pin evidence artifacts")
        if len(evidence_ids) != len(set(evidence_ids)):
            raise BiasAuditValidationError(f"probe {probe_id!r} repeats an evidence artifact")
        unknown = [item for item in evidence_ids if item not in artifact_by_id]
        if unknown:
            raise BiasAuditValidationError(
                f"probe {probe_id!r} references unknown artifacts {unknown}"
            )

    for region, required in _REQUIRED_CATEGORIES.items():
        missing = sorted(required - observed_categories[region])
        if missing:
            raise BiasAuditValidationError(
                f"{region} audit is missing required categories {missing}"
            )
    unused_datasets = sorted(set(dataset_by_id) - used_datasets)
    if unused_datasets:
        raise BiasAuditValidationError(f"unreferenced datasets {unused_datasets}")

    spec_hash = _sha256_bytes(_canonical_json(spec))
    return spec, verified_artifacts, spec_hash


def publish_bias_audit(
    spec: str | Path,
    *,
    audits_root: str | Path = "data/audits/point-in-time",
) -> Path:
    """Validate a complete bounded audit and publish an immutable result manifest."""

    spec_path = Path(spec).expanduser().resolve()
    document, artifacts, spec_hash = _validate_spec(spec_path)
    artifact_id = str(uuid5(_ARTIFACT_NAMESPACE, spec_hash))
    manifest = {
        "schema_version": SCHEMA_VERSION,
        "generator_version": GENERATOR_VERSION,
        "artifact_id": artifact_id,
        "audit_id": document["audit_id"],
        "git_commit": document["git_commit"],
        "spec_path": str(spec_path),
        "spec_sha256": spec_hash,
        "status": "passed",
        "regions": sorted(_REGIONS),
        "dataset_classifications": {
            item["id"]: item["classification"] for item in document["datasets"]
        },
        "probe_count": len(document["probes"]),
        "probes": [item["id"] for item in document["probes"]],
        "verified_artifacts": artifacts,
    }
    output = (
        Path(audits_root).expanduser().resolve() / f"artifact_id={artifact_id}" / "manifest.json"
    )
    encoded = json.dumps(manifest, indent=2, sort_keys=True, ensure_ascii=False) + "\n"
    if output.exists():
        if output.read_text(encoding="utf-8") != encoded:
            raise BiasAuditConflictError(f"immutable bias-audit artifact conflict: {artifact_id}")
        validate_bias_audit(output)
        return output
    output.parent.mkdir(parents=True, exist_ok=False)
    output.write_text(encoded, encoding="utf-8")
    validate_bias_audit(output)
    return output


def validate_bias_audit(path: str | Path) -> ValidatedBiasAudit:
    """Validate an immutable audit manifest and all of its pinned source evidence."""

    manifest_path = Path(path).expanduser().resolve()
    manifest = _read_json(manifest_path, label="bias-audit manifest")
    _exact_fields(
        manifest,
        frozenset(
            {
                "schema_version",
                "generator_version",
                "artifact_id",
                "audit_id",
                "git_commit",
                "spec_path",
                "spec_sha256",
                "status",
                "regions",
                "dataset_classifications",
                "probe_count",
                "probes",
                "verified_artifacts",
            }
        ),
        label="bias-audit manifest",
    )
    if (
        manifest["schema_version"] != SCHEMA_VERSION
        or manifest["generator_version"] != GENERATOR_VERSION
    ):
        raise BiasAuditValidationError("unsupported bias-audit manifest version")
    if manifest["status"] != "passed" or manifest["regions"] != sorted(_REGIONS):
        raise BiasAuditValidationError("bias-audit manifest does not record a complete pass")
    spec_path = Path(_nonempty_string(manifest["spec_path"], field="spec_path"))
    document, artifacts, spec_hash = _validate_spec(spec_path)
    expected_id = str(uuid5(_ARTIFACT_NAMESPACE, spec_hash))
    if manifest["artifact_id"] != expected_id or manifest["spec_sha256"] != spec_hash:
        raise BiasAuditValidationError("bias-audit identity does not match its specification")
    expected = {
        "audit_id": document["audit_id"],
        "git_commit": document["git_commit"],
        "dataset_classifications": {
            item["id"]: item["classification"] for item in document["datasets"]
        },
        "probe_count": len(document["probes"]),
        "probes": [item["id"] for item in document["probes"]],
        "verified_artifacts": artifacts,
    }
    for field, value in expected.items():
        if manifest[field] != value:
            raise BiasAuditValidationError(f"bias-audit manifest field {field!r} does not match")
    return ValidatedBiasAudit(manifest_path=manifest_path, manifest=manifest)
