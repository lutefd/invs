"""Atomic publication and validation for immutable retrospective backfills."""

from __future__ import annotations

import fcntl
import hashlib
import json
import os
import re
import shutil
import tempfile
from collections.abc import Mapping
from pathlib import Path, PurePosixPath
from typing import Any
from uuid import UUID

from .experiments import canonical_json

_SHA256 = re.compile(r"^[0-9a-f]{64}$")
_MANIFEST_NAME = "manifest.json"


class BackfillArtifactError(ValueError):
    """Raised when a retrospective artifact tree cannot be published safely."""


def _sha256(content: bytes) -> str:
    return hashlib.sha256(content).hexdigest()


def _safe_relative(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not value or "\x00" in value:
        raise BackfillArtifactError(f"{field} must be a safe relative path")
    if value.startswith(("/", "\\")) or "\\" in value:
        raise BackfillArtifactError(f"{field} must be a safe relative path")
    path = PurePosixPath(value)
    if path.is_absolute() or any(part in {"", ".", ".."} for part in value.split("/")):
        raise BackfillArtifactError(f"{field} must be a safe relative path")
    return path.as_posix()


def _run_id(manifest: Mapping[str, Any]) -> str:
    try:
        value = manifest["reconstruction_run_id"]
        canonical = str(UUID(value))
    except (KeyError, TypeError, ValueError, AttributeError) as error:
        raise BackfillArtifactError("manifest reconstruction_run_id is invalid") from error
    if value != canonical:
        raise BackfillArtifactError("manifest reconstruction_run_id must be a canonical UUID")
    fingerprint = manifest.get("input_fingerprint")
    if not isinstance(fingerprint, str) or not _SHA256.fullmatch(fingerprint):
        raise BackfillArtifactError("manifest input_fingerprint must be a lower-case SHA-256")
    return canonical


def _expected_pins(manifest: Mapping[str, Any], *, run_id: str) -> dict[str, str]:
    """Return run-relative pinned file paths and hashes declared by the manifest."""

    prefix = f"backfills/{run_id}/"

    def add(path_value: Any, sha_value: Any, *, field: str) -> None:
        path = _safe_relative(path_value, field=f"{field}.path")
        if not path.startswith(prefix):
            raise BackfillArtifactError(f"{field}.path must refer to this backfill run")
        relative = _safe_relative(path[len(prefix) :], field=f"{field}.path")
        if not relative.startswith("inputs/") or relative == "inputs/":
            raise BackfillArtifactError(f"{field}.path must refer to a pinned input")
        if not isinstance(sha_value, str) or not _SHA256.fullmatch(sha_value):
            raise BackfillArtifactError(f"{field}.sha256 must be a lower-case SHA-256")
        previous = pins.get(relative)
        if previous is not None and previous != sha_value:
            raise BackfillArtifactError(f"manifest has conflicting digests for {relative}")
        pins[relative] = sha_value

    pins: dict[str, str] = {}
    try:
        profile = manifest["profile"]
        calendar = manifest["calendar"]
        price_inputs = manifest["price_inputs"]
        add(profile["snapshot_path"], profile["profile_sha256"], field="profile")
        add(calendar["snapshot_path"], calendar["snapshot_sha256"], field="calendar")
        manifests = price_inputs["manifests"]
        if not isinstance(manifests, list):
            raise BackfillArtifactError("price_inputs.manifests must be an array")
        for index, item in enumerate(manifests):
            if not isinstance(item, Mapping):
                raise BackfillArtifactError(f"price_inputs.manifests[{index}] must be an object")
            add(item["path"], item["sha256"], field=f"price_inputs.manifests[{index}]")
            parts = item["parts"]
            if not isinstance(parts, list):
                raise BackfillArtifactError(f"price_inputs.manifests[{index}].parts must be an array")
            for part_index, part in enumerate(parts):
                if not isinstance(part, Mapping):
                    raise BackfillArtifactError(
                        f"price_inputs.manifests[{index}].parts[{part_index}] must be an object"
                    )
                add(
                    part["path"],
                    part["sha256"],
                    field=f"price_inputs.manifests[{index}].parts[{part_index}]",
                )
    except (KeyError, TypeError) as error:
        raise BackfillArtifactError("manifest pinned input references are incomplete") from error
    if not pins:
        raise BackfillArtifactError("manifest declares no pinned input files")
    if _MANIFEST_NAME in pins:
        raise BackfillArtifactError("manifest cannot pin itself as an input")
    return pins


def _validated_manifest_document(manifest_path: Path) -> tuple[dict[str, Any], dict[str, str]]:
    if manifest_path.is_symlink() or not manifest_path.is_file():
        raise BackfillArtifactError(f"backfill manifest is missing or unsafe: {manifest_path}")
    try:
        document = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as error:
        raise BackfillArtifactError(f"cannot read backfill manifest: {error}") from error
    if not isinstance(document, dict):
        raise BackfillArtifactError("backfill manifest must be an object")
    run_id = _run_id(document)
    expected = _expected_pins(document, run_id=run_id)
    run_directory = manifest_path.parent
    actual_files: set[str] = set()
    for current, directories, filenames in os.walk(run_directory, followlinks=False):
        current_path = Path(current)
        for name in directories:
            if (current_path / name).is_symlink():
                raise BackfillArtifactError("backfill run contains a symlinked directory")
        for name in filenames:
            item = current_path / name
            if item.is_symlink() or not item.is_file():
                raise BackfillArtifactError("backfill run contains an unsafe file")
            relative = item.relative_to(run_directory).as_posix()
            if relative == _MANIFEST_NAME:
                continue
            actual_files.add(relative)
    if actual_files != set(expected):
        missing = sorted(set(expected) - actual_files)
        extra = sorted(actual_files - set(expected))
        detail = []
        if missing:
            detail.append("missing: " + ", ".join(missing))
        if extra:
            detail.append("unlisted: " + ", ".join(extra))
        raise BackfillArtifactError("backfill pinned file set is invalid (" + "; ".join(detail) + ")")
    for relative, expected_sha in expected.items():
        path = run_directory / relative
        if _sha256(path.read_bytes()) != expected_sha:
            raise BackfillArtifactError(f"backfill pinned input digest mismatch: {relative}")
    return document, expected


def validate_retrospective_artifact_tree(manifest_path: str | Path) -> dict[str, Any]:
    """Validate a run manifest and every pinned file in its immutable tree."""

    path = Path(manifest_path).absolute()
    try:
        document, _ = _validated_manifest_document(path)
    except OSError as error:
        raise BackfillArtifactError(f"cannot validate backfill artifact tree: {error}") from error
    return document


def _fsync_directory(path: Path) -> None:
    descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_DIRECTORY", 0))
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def _write_fsynced(path: Path, content: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("xb") as stream:
        stream.write(content)
        stream.flush()
        os.fsync(stream.fileno())


def _sync_tree(root: Path) -> None:
    directories: list[Path] = []
    for current, child_dirs, _ in os.walk(root, topdown=True, followlinks=False):
        path = Path(current)
        directories.append(path)
        for child in child_dirs:
            if (path / child).is_symlink():
                raise BackfillArtifactError("temporary backfill tree contains a symlink")
    for directory in reversed(directories):
        _fsync_directory(directory)


def _remove_tree(path: Path) -> None:
    if path.is_symlink():
        path.unlink()
    elif path.is_dir():
        shutil.rmtree(path)
    elif path.exists():
        path.unlink()


def _remove_abandoned_trees(parent: Path, run_id: str) -> bool:
    removed = False
    for path in parent.glob(f".{run_id}.tmp-*"):
        if path.parent != parent:
            continue
        _remove_tree(path)
        removed = True
    return removed


def _existing_result(
    target: Path,
    *,
    expected_input_fingerprint: str,
    expected_pins: Mapping[str, str],
) -> Path:
    if target.is_symlink() or not target.is_dir():
        raise BackfillArtifactError(f"backfill run path already exists and is unsafe: {target}")
    manifest_path = target / _MANIFEST_NAME
    existing, existing_pins = _validated_manifest_document(manifest_path)
    if existing["input_fingerprint"] != expected_input_fingerprint:
        raise BackfillArtifactError("existing immutable backfill conflicts with current inputs")
    if dict(existing_pins) != dict(expected_pins):
        raise BackfillArtifactError("existing backfill pins conflict with current inputs")
    return manifest_path


def publish_retrospective_artifact_tree(
    *,
    run_directory: str | Path,
    manifest: Mapping[str, Any],
    pinned_files: Mapping[str, bytes],
) -> Path:
    """Publish one complete immutable run tree with atomic directory visibility.

    ``pinned_files`` keys are paths relative to ``run_directory``, such as
    ``inputs/profile.yaml``. The exact expected paths and SHA-256 digests are
    derived from the manifest's run-root-relative references.
    """

    run_id = _run_id(manifest)
    fingerprint = str(manifest["input_fingerprint"])
    target_input = Path(run_directory).absolute()
    if target_input.name != run_id or target_input.parent.name != "backfills":
        raise BackfillArtifactError("run_directory must be backfills/<reconstruction_run_id>")
    parent = target_input.parent.resolve()
    parent.mkdir(parents=True, exist_ok=True)
    target = parent / run_id
    expected_pins = _expected_pins(manifest, run_id=run_id)
    if not isinstance(pinned_files, Mapping):
        raise BackfillArtifactError("pinned_files must be a path-to-bytes mapping")
    normalized_files: dict[str, bytes] = {}
    for raw_path, content in pinned_files.items():
        relative = _safe_relative(raw_path, field="pinned_files path")
        if relative == _MANIFEST_NAME:
            raise BackfillArtifactError("pinned_files cannot replace manifest.json")
        if not isinstance(content, bytes):
            raise BackfillArtifactError(f"pinned_files[{relative}] must contain bytes")
        if relative in normalized_files:
            raise BackfillArtifactError(f"duplicate pinned file path: {relative}")
        normalized_files[relative] = content
    if set(normalized_files) != set(expected_pins):
        missing = sorted(set(expected_pins) - set(normalized_files))
        extra = sorted(set(normalized_files) - set(expected_pins))
        detail = []
        if missing:
            detail.append("missing: " + ", ".join(missing))
        if extra:
            detail.append("unlisted: " + ", ".join(extra))
        raise BackfillArtifactError("pinned input set does not match manifest (" + "; ".join(detail) + ")")
    for relative, content in normalized_files.items():
        if _sha256(content) != expected_pins[relative]:
            raise BackfillArtifactError(f"pinned input digest changed before publication: {relative}")
    try:
        manifest_bytes = canonical_json(dict(manifest)) + b"\n"
    except (TypeError, ValueError) as error:
        raise BackfillArtifactError(f"manifest cannot be serialized: {error}") from error

    lock_path = parent / f".{run_id}.publish.lock"
    try:
        lock_descriptor = os.open(lock_path, os.O_CREAT | os.O_RDWR, 0o600)
    except OSError as error:
        raise BackfillArtifactError(f"cannot create backfill publication lock: {error}") from error
    temporary: Path | None = None
    try:
        fcntl.flock(lock_descriptor, fcntl.LOCK_EX)
        if _remove_abandoned_trees(parent, run_id):
            _fsync_directory(parent)
        if target.exists() or target.is_symlink():
            return _existing_result(
                target,
                expected_input_fingerprint=fingerprint,
                expected_pins=expected_pins,
            )

        temporary = Path(tempfile.mkdtemp(prefix=f".{run_id}.tmp-", dir=parent))
        for relative, content in sorted(normalized_files.items()):
            _write_fsynced(temporary / relative, content)
        _write_fsynced(temporary / _MANIFEST_NAME, manifest_bytes)
        _sync_tree(temporary)
        validate_retrospective_artifact_tree(temporary / _MANIFEST_NAME)

        # The lock serializes cooperating publishers. Recheck before rename so
        # an unexpected writer cannot be overwritten by this completed tree.
        if target.exists() or target.is_symlink():
            result = _existing_result(
                target,
                expected_input_fingerprint=fingerprint,
                expected_pins=expected_pins,
            )
            _remove_tree(temporary)
            temporary = None
            return result
        os.rename(temporary, target)
        temporary = None
        _fsync_directory(parent)
        return target / _MANIFEST_NAME
    except BackfillArtifactError:
        raise
    except OSError as error:
        raise BackfillArtifactError(f"cannot publish backfill artifact tree: {error}") from error
    finally:
        if temporary is not None:
            _remove_tree(temporary)
            try:
                _fsync_directory(parent)
            except OSError:
                pass
        try:
            fcntl.flock(lock_descriptor, fcntl.LOCK_UN)
        finally:
            os.close(lock_descriptor)


__all__ = [
    "BackfillArtifactError",
    "publish_retrospective_artifact_tree",
    "validate_retrospective_artifact_tree",
]
