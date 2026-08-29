from __future__ import annotations

import json
import os
from pathlib import Path

import pytest

from research.taxonomy import TaxonomyRegistryError, load_taxonomy_registry

ROOT = Path(__file__).resolve().parents[2]
REGISTRY_PATH = Path(
    os.environ.get("INVS_SCHEMA_ROOT", str(ROOT / "schemas"))
) / "feature-taxonomy-registry.json"


def test_taxonomy_registry_resolves_reviewed_mappings_and_hash(tmp_path: Path) -> None:
    registry = load_taxonomy_registry(REGISTRY_PATH)

    revenue = registry.resolve("fundamental-growth", "1.0.0", "revenue")
    operating_income = registry.resolve("fundamental-growth", "1.0.0", "operating_income")

    assert revenue.mapping_id == "sec:us-gaap:Revenue->revenue"
    assert revenue.fiscal_periods == ("FY", "Q4")
    assert operating_income.period_type == "duration"
    assert len(registry.registry_sha256) == 64


def test_taxonomy_registry_rejects_duplicate_json_keys_and_duplicate_identity(tmp_path: Path) -> None:
    duplicate_key = tmp_path / "duplicate.json"
    duplicate_key.write_text(
        '{"registry_version":"1.0.0","registry_version":"1.0.0","entries":[]}',
        encoding="utf-8",
    )
    with pytest.raises(TaxonomyRegistryError, match="duplicate JSON key"):
        load_taxonomy_registry(duplicate_key)

    document = json.loads(REGISTRY_PATH.read_text(encoding="utf-8"))
    document["entries"].append(dict(document["entries"][0]))
    duplicate_identity = tmp_path / "identity.json"
    duplicate_identity.write_text(json.dumps(document), encoding="utf-8")
    with pytest.raises(TaxonomyRegistryError, match="duplicate mapping identities"):
        load_taxonomy_registry(duplicate_identity)


def test_taxonomy_registry_rejects_unreviewed_mapping(tmp_path: Path) -> None:
    document = json.loads(REGISTRY_PATH.read_text(encoding="utf-8"))
    document["entries"][0]["review_status"] = "proposed"
    path = tmp_path / "unreviewed.json"
    path.write_text(json.dumps(document), encoding="utf-8")

    with pytest.raises(TaxonomyRegistryError, match="unsupported"):
        load_taxonomy_registry(path)
