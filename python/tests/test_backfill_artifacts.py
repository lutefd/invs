from __future__ import annotations

import hashlib
import json
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

import pytest

from research.backfill_artifacts import (
    BackfillArtifactError,
    publish_retrospective_artifact_tree,
    validate_retrospective_artifact_tree,
)

RUN_ID = "a804a81b-0d23-54f3-bdbd-cf204f70d6a0"


def _sha(content: bytes) -> str:
    return hashlib.sha256(content).hexdigest()


def _inputs_and_manifest() -> tuple[dict[str, object], dict[str, bytes]]:
    files = {
        "inputs/profile.yaml": b"universe: starter\n",
        "inputs/calendar-snapshot.json": b'{"sessions":[]}\n',
        "inputs/prices/manifest.json": b'{"parts":[]}\n',
        "inputs/prices/part-prices.parquet": b"PAR1test-parquet",
    }
    manifest: dict[str, object] = {
        "schema_version": "1.0.0",
        "reconstruction_run_id": RUN_ID,
        "input_fingerprint": "1" * 64,
        "profile": {
            "snapshot_path": f"backfills/{RUN_ID}/inputs/profile.yaml",
            "profile_sha256": _sha(files["inputs/profile.yaml"]),
        },
        "calendar": {
            "snapshot_path": f"backfills/{RUN_ID}/inputs/calendar-snapshot.json",
            "snapshot_sha256": _sha(files["inputs/calendar-snapshot.json"]),
        },
        "price_inputs": {
            "manifests": [
                {
                    "path": f"backfills/{RUN_ID}/inputs/prices/manifest.json",
                    "sha256": _sha(files["inputs/prices/manifest.json"]),
                    "parts": [
                        {
                            "path": f"backfills/{RUN_ID}/inputs/prices/part-prices.parquet",
                            "sha256": _sha(files["inputs/prices/part-prices.parquet"]),
                        }
                    ],
                }
            ]
        },
    }
    return manifest, files


def _target(tmp_path: Path) -> Path:
    return tmp_path / "backfills" / RUN_ID


def test_publish_writes_complete_tree_and_validates_all_pins(tmp_path: Path) -> None:
    manifest, files = _inputs_and_manifest()

    manifest_path = publish_retrospective_artifact_tree(
        run_directory=_target(tmp_path), manifest=manifest, pinned_files=files
    )

    assert manifest_path == _target(tmp_path) / "manifest.json"
    assert validate_retrospective_artifact_tree(manifest_path) == manifest
    assert json.loads(manifest_path.read_text()) == manifest
    assert {
        path.relative_to(_target(tmp_path)).as_posix()
        for path in _target(tmp_path).rglob("*")
        if path.is_file()
    } == {"manifest.json", *files}
    assert not list(_target(tmp_path).parent.glob(f".{RUN_ID}.tmp-*"))


def test_same_run_publication_is_idempotent_under_concurrency(tmp_path: Path) -> None:
    manifest, files = _inputs_and_manifest()

    def publish() -> Path:
        return publish_retrospective_artifact_tree(
            run_directory=_target(tmp_path), manifest=manifest, pinned_files=files
        )

    with ThreadPoolExecutor(max_workers=2) as executor:
        paths = list(executor.map(lambda _: publish(), range(2)))

    assert paths == [_target(tmp_path) / "manifest.json"] * 2
    assert validate_retrospective_artifact_tree(paths[0]) == manifest
    assert not list(_target(tmp_path).parent.glob(f".{RUN_ID}.tmp-*"))


def test_existing_run_must_have_matching_fingerprint_and_intact_pins(tmp_path: Path) -> None:
    manifest, files = _inputs_and_manifest()
    path = publish_retrospective_artifact_tree(
        run_directory=_target(tmp_path), manifest=manifest, pinned_files=files
    )

    changed_manifest = {**manifest, "input_fingerprint": "2" * 64}
    with pytest.raises(BackfillArtifactError, match="conflicts"):
        publish_retrospective_artifact_tree(
            run_directory=_target(tmp_path), manifest=changed_manifest, pinned_files=files
        )

    part = _target(tmp_path) / "inputs/prices/part-prices.parquet"
    part.write_bytes(b"corrupt")
    with pytest.raises(BackfillArtifactError, match="digest mismatch"):
        publish_retrospective_artifact_tree(
            run_directory=_target(tmp_path), manifest=manifest, pinned_files=files
        )
    with pytest.raises(BackfillArtifactError, match="digest mismatch"):
        validate_retrospective_artifact_tree(path)


@pytest.mark.parametrize(
    "mutate, message",
    [
        ("missing", "missing: inputs/prices/part-prices.parquet"),
        ("extra", "unlisted: surprise.bin"),
        ("digest", "digest changed before publication"),
        ("traversal", "safe relative path"),
    ],
)
def test_invalid_pins_are_rejected_before_publication(
    tmp_path: Path, mutate: str, message: str
) -> None:
    manifest, files = _inputs_and_manifest()
    changed = dict(files)
    if mutate == "missing":
        changed.pop("inputs/prices/part-prices.parquet")
    elif mutate == "extra":
        changed["surprise.bin"] = b"extra"
    elif mutate == "digest":
        changed["inputs/profile.yaml"] = b"changed profile"
    elif mutate == "traversal":
        changed["../escape"] = b"escape"

    with pytest.raises(BackfillArtifactError, match=message):
        publish_retrospective_artifact_tree(
            run_directory=_target(tmp_path), manifest=manifest, pinned_files=changed
        )
    assert not _target(tmp_path).exists()


def test_abandoned_sibling_temp_tree_is_removed_before_retry(tmp_path: Path) -> None:
    manifest, files = _inputs_and_manifest()
    parent = _target(tmp_path).parent
    parent.mkdir(parents=True)
    abandoned = parent / f".{RUN_ID}.tmp-abandoned"
    abandoned.mkdir()
    (abandoned / "partial.parquet").write_bytes(b"partial")

    publish_retrospective_artifact_tree(
        run_directory=_target(tmp_path), manifest=manifest, pinned_files=files
    )

    assert not abandoned.exists()
    assert validate_retrospective_artifact_tree(_target(tmp_path) / "manifest.json")
