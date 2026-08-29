from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path

import pytest

from research.registry import FeatureRegistryError, load_feature_registry

ROOT = Path(__file__).resolve().parents[2]
REGISTRY_PATH = Path(
    os.environ.get("INVS_SCHEMA_ROOT", str(ROOT / "schemas"))
) / "feature-set-registry.json"


def _registry_document() -> dict:
    return json.loads(REGISTRY_PATH.read_text(encoding="utf-8"))


def _write_registry(tmp_path: Path, document: str | dict) -> Path:
    tmp_path.mkdir(parents=True, exist_ok=True)
    path = tmp_path / "feature-set-registry.json"
    if isinstance(document, str):
        path.write_text(document, encoding="utf-8")
    else:
        path.write_text(json.dumps(document, indent=2) + "\n", encoding="utf-8")
    return path


def test_checked_in_registry_resolves_market_basic_and_fingerprints_exact_bytes() -> None:
    registry = load_feature_registry(REGISTRY_PATH)
    definition = registry.resolve("market-basic", "1.0.0")

    assert registry.registry_version == "1.0.0"
    assert registry.registry_sha256 == hashlib.sha256(REGISTRY_PATH.read_bytes()).hexdigest()
    assert definition.feature_names == ("close", "return_1d", "range_1d", "volume")
    assert definition.required_datasets == ("prices",)
    assert definition.inputs[0].historical_fitness == "installation_replay_only"
    assert definition.inputs[0].availability_policy == "conservative_receipt_time"
    assert definition.calendar.decision_clock_policy == "after_close_next_session"
    assert definition.computation.delay_seconds == 0
    assert definition.outputs[0].nullable is False
    assert definition.outputs[1].nullable is True


def test_unknown_feature_set_and_version_fail_closed() -> None:
    registry = load_feature_registry(REGISTRY_PATH)

    with pytest.raises(FeatureRegistryError, match="unsupported feature set"):
        registry.resolve("future-set", "1.0.0")
    with pytest.raises(FeatureRegistryError, match="unsupported feature set"):
        registry.resolve("market-basic", "2.0.0")


def test_unknown_fields_duplicate_identities_and_unsorted_entries_are_rejected(tmp_path: Path) -> None:
    unknown = _registry_document()
    unknown["unexpected"] = True
    with pytest.raises(FeatureRegistryError, match="unknown field"):
        load_feature_registry(_write_registry(tmp_path / "unknown", unknown))

    duplicate = _registry_document()
    duplicate["entries"].append(duplicate["entries"][0])
    with pytest.raises(FeatureRegistryError, match="duplicate feature-set identities"):
        load_feature_registry(_write_registry(tmp_path / "duplicate", duplicate))

    unsorted = _registry_document()
    extra = json.loads(json.dumps(unsorted["entries"][0]))
    extra["feature_set"] = "alpha-set"
    unsorted["entries"] = [unsorted["entries"][0], extra]
    with pytest.raises(FeatureRegistryError, match="sorted"):
        load_feature_registry(_write_registry(tmp_path / "unsorted", unsorted))


def test_duplicate_json_keys_and_semantic_policy_mismatches_fail_closed(tmp_path: Path) -> None:
    duplicate_keys = '{"registry_version":"1.0.0","registry_version":"1.0.0","entries":[]}'
    with pytest.raises(FeatureRegistryError, match="duplicate JSON key"):
        load_feature_registry(_write_registry(tmp_path / "duplicate-keys", duplicate_keys))

    mismatch = _registry_document()
    mismatch["entries"][0]["outputs"][0]["nullable"] = True
    with pytest.raises(FeatureRegistryError, match="nullable must match"):
        load_feature_registry(_write_registry(tmp_path / "nullable", mismatch))

    replay = _registry_document()
    replay["entries"][0]["inputs"][0]["historical_fitness"] = "not-a-classification"
    with pytest.raises(FeatureRegistryError, match="historical_fitness"):
        load_feature_registry(_write_registry(tmp_path / "fitness", replay))


def test_calendar_and_availability_contracts_are_explicit(tmp_path: Path) -> None:
    document = _registry_document()
    calendar = document["entries"][0]["calendar"]
    calendar["pin_required"] = False
    with pytest.raises(FeatureRegistryError, match="pin_required must match"):
        load_feature_registry(_write_registry(tmp_path / "calendar", document))

    document = _registry_document()
    document["entries"][0]["computation"]["availability_rule"] = "latest_value"
    with pytest.raises(FeatureRegistryError, match="availability_rule"):
        load_feature_registry(_write_registry(tmp_path / "availability", document))
