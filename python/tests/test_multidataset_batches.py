from __future__ import annotations

import os
from pathlib import Path

import pytest
from test_fundamental_growth import CALENDAR_PIN as FUNDAMENTAL_CALENDAR_PIN
from test_fundamental_growth import ISSUER_ID
from test_fundamental_growth import REGISTRY_PATH as FUNDAMENTAL_TAXONOMY_PATH
from test_fundamental_growth import SECURITY_ID as FUNDAMENTAL_SECURITY_ID
from test_fundamental_growth import _catalog as fundamental_catalog
from test_macro_state import CALENDAR_PIN as MACRO_CALENDAR_PIN
from test_macro_state import _catalog as macro_catalog

from research.batches import publish_feature_batch, read_feature_batch
from research.registry import load_feature_registry

ROOT = Path(__file__).resolve().parents[2]
REGISTRY_PATH = Path(
    os.environ.get("INVS_SCHEMA_ROOT", str(ROOT / "schemas"))
) / "feature-set-registry.json"


def test_fundamental_batch_requires_explicit_mapping_and_publishes_child(
    tmp_path: Path,
) -> None:
    registry = load_feature_registry(REGISTRY_PATH)
    batch_path = publish_feature_batch(
        fundamental_catalog(tmp_path),
        registry=registry,
        security_ids=[FUNDAMENTAL_SECURITY_ID],
        decision_ats=["2025-02-01T00:00:00Z"],
        calendar_pin=FUNDAMENTAL_CALENDAR_PIN,
        features_root=tmp_path / "features",
        feature_set="fundamental-growth",
        feature_set_version="1.0.0",
        security_mappings={FUNDAMENTAL_SECURITY_ID: ISSUER_ID},
        taxonomy_registry=FUNDAMENTAL_TAXONOMY_PATH,
        git_commit="0" * 40,
    )

    batch = read_feature_batch(batch_path, features_root=tmp_path / "features", registry=registry)
    assert batch.manifest["run_summary"] == {
        "requested_partitions": 1,
        "accepted_partitions": 1,
        "rejected_partitions": 0,
        "row_count": 1,
        "status": "completed",
    }
    assert batch.observations[0]["features"]["operating_margin"] == "0.25"


def test_macro_batch_uses_explicit_series_and_vintage_contract(tmp_path: Path) -> None:
    registry = load_feature_registry(REGISTRY_PATH)
    batch_path = publish_feature_batch(
        macro_catalog(tmp_path),
        registry=registry,
        security_ids=[FUNDAMENTAL_SECURITY_ID],
        decision_ats=["2024-02-01T00:00:00Z"],
        calendar_pin=MACRO_CALENDAR_PIN,
        features_root=tmp_path / "features",
        feature_set="macro-state",
        feature_set_version="1.0.0",
        macro_source="alfred",
        macro_series_id="CPIAUCSL",
        macro_geography="US",
        macro_unit="Index",
        macro_frequency="monthly",
        git_commit="0" * 40,
    )

    batch = read_feature_batch(batch_path, features_root=tmp_path / "features", registry=registry)
    assert batch.manifest["run_summary"]["accepted_partitions"] == 1
    assert batch.observations[0]["features"]["macro_change_yoy"] == "0.1"


def test_fundamental_batch_records_missing_mapping_without_silent_substitution(tmp_path: Path) -> None:
    registry = load_feature_registry(REGISTRY_PATH)
    batch_path = publish_feature_batch(
        fundamental_catalog(tmp_path),
        registry=registry,
        security_ids=[FUNDAMENTAL_SECURITY_ID],
        decision_ats=["2025-02-01T00:00:00Z"],
        calendar_pin=FUNDAMENTAL_CALENDAR_PIN,
        features_root=tmp_path / "features",
        feature_set="fundamental-growth",
        feature_set_version="1.0.0",
        taxonomy_registry=FUNDAMENTAL_TAXONOMY_PATH,
        git_commit="0" * 40,
    )

    batch = read_feature_batch(batch_path, features_root=tmp_path / "features", registry=registry)
    assert batch.row_count == 0
    assert batch.manifest["rejected"] == [
        {
            "security_id": FUNDAMENTAL_SECURITY_ID,
            "decision_at": "2025-02-01T00:00:00Z",
            "reason": "missing_security_mapping",
            "detail": "fundamental-growth requires an explicit security-to-issuer mapping",
        }
    ]


def test_security_mapping_outside_universe_fails_closed(tmp_path: Path) -> None:
    registry = load_feature_registry(REGISTRY_PATH)
    with pytest.raises(ValueError, match="outside"):
        publish_feature_batch(
            fundamental_catalog(tmp_path),
            registry=registry,
            security_ids=[FUNDAMENTAL_SECURITY_ID],
            decision_ats=["2025-02-01T00:00:00Z"],
            calendar_pin=FUNDAMENTAL_CALENDAR_PIN,
            features_root=tmp_path / "features",
            feature_set="fundamental-growth",
            feature_set_version="1.0.0",
            security_mappings={
                "2e75c0a8-7b47-45a5-9f03-bf9ecfb1c0b5": ISSUER_ID,
            },
            taxonomy_registry=FUNDAMENTAL_TAXONOMY_PATH,
            git_commit="0" * 40,
        )
