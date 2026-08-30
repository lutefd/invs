from __future__ import annotations

import json
import os
from copy import deepcopy
from pathlib import Path

import pytest

from research.release import CompatibilityError, validate_release_manifest

REPO_ROOT = Path(os.environ.get("INVS_REPO_ROOT", Path(__file__).resolve().parents[2]))
MANIFEST_PATH = REPO_ROOT / "release" / "compatibility.json"


def _manifest() -> dict:
    return json.loads(MANIFEST_PATH.read_text(encoding="utf-8"))


def _write_manifest(tmp_path: Path, manifest: dict) -> Path:
    path = tmp_path / "compatibility.json"
    path.write_text(json.dumps(manifest), encoding="utf-8")
    return path


def _assert_rejected(tmp_path: Path, manifest: dict) -> None:
    with pytest.raises(CompatibilityError):
        validate_release_manifest(_write_manifest(tmp_path, manifest), repo_root=REPO_ROOT)


def test_checked_in_manifest_is_valid() -> None:
    result = validate_release_manifest(MANIFEST_PATH, repo_root=REPO_ROOT)
    assert result.manifest["release_version"] == "1.0.0"
    assert len(result.manifest["contracts"]["schemas"]) == 55
    assert len(result.manifest["contracts"]["migrations"]["files"]) == 16


def test_unknown_top_level_field_is_rejected(tmp_path: Path) -> None:
    manifest = _manifest()
    manifest["future_field"] = True
    _assert_rejected(tmp_path, manifest)


def test_changed_registry_fingerprint_is_rejected(tmp_path: Path) -> None:
    manifest = _manifest()
    manifest["contracts"]["registries"][0]["sha256"] = "0" * 64
    _assert_rejected(tmp_path, manifest)


def test_changed_image_digest_is_rejected(tmp_path: Path) -> None:
    manifest = _manifest()
    manifest["images"][0]["digest"] = "sha256:" + "0" * 64
    _assert_rejected(tmp_path, manifest)


def test_reordered_migration_is_rejected(tmp_path: Path) -> None:
    manifest = _manifest()
    migrations = manifest["contracts"]["migrations"]["files"]
    migrations[0], migrations[1] = migrations[1], migrations[0]
    _assert_rejected(tmp_path, manifest)


def test_unknown_contract_reference_is_rejected(tmp_path: Path) -> None:
    manifest = deepcopy(_manifest())
    manifest["contracts"]["schemas"].append(
        {
            "name": "not-a-schema",
            "path": "schemas/not-a-schema.schema.json",
            "schema_version": "1.0.0",
            "sha256": "0" * 64,
        }
    )
    _assert_rejected(tmp_path, manifest)
