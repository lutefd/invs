from __future__ import annotations

import json
from pathlib import Path

import pytest
from test_batches import (
    CALENDAR_PIN,
    MISSING_SECURITY,
    REGISTRY_PATH,
    SECURITY_ONE,
    _rows,
    _write_prices,
)

from research.batches import publish_feature_batch
from research.catalog import ResearchCatalog
from research.feature_quality import FeatureQualityReportError, build_feature_quality_report
from research.registry import load_feature_registry


def _single_row_catalog(tmp_path: Path) -> ResearchCatalog:
    data_root = tmp_path / "data"
    _write_prices(data_root, SECURITY_ONE, _rows(0)[:1])
    return ResearchCatalog(data_root).register()


def _publish_quality_batch(tmp_path: Path) -> tuple[Path, Path, Path]:
    data_root = tmp_path / "data"
    features_root = tmp_path / "features"
    batch_path = publish_feature_batch(
        _single_row_catalog(tmp_path),
        registry=load_feature_registry(REGISTRY_PATH),
        security_ids=[SECURITY_ONE, MISSING_SECURITY],
        decision_ats=["2025-01-02T23:00:00Z"],
        calendar_pin=CALENDAR_PIN,
        features_root=features_root,
        git_commit="0" * 40,
    )
    return batch_path, features_root, data_root


def test_feature_quality_report_explains_nulls_rejects_freshness_and_raw_lineage(
    tmp_path: Path,
) -> None:
    batch_path, features_root, data_root = _publish_quality_batch(tmp_path)

    report = build_feature_quality_report(
        batch_path,
        features_root=features_root,
        data_root=data_root,
        registry=load_feature_registry(REGISTRY_PATH),
        stale_after_seconds=0,
    )

    assert report["batch"]["feature_set"] == "market-basic"
    assert report["coverage"] == [
        {
            "decision_at": "2025-01-02T23:00:00Z",
            "feature_name": "close",
            "universe_size": 2,
            "accepted_count": 1,
            "rejected_count": 1,
            "present_count": 1,
            "null_count": 0,
            "stale_count": 1,
        },
        {
            "decision_at": "2025-01-02T23:00:00Z",
            "feature_name": "return_1d",
            "universe_size": 2,
            "accepted_count": 1,
            "rejected_count": 1,
            "present_count": 0,
            "null_count": 1,
            "stale_count": 1,
        },
        {
            "decision_at": "2025-01-02T23:00:00Z",
            "feature_name": "range_1d",
            "universe_size": 2,
            "accepted_count": 1,
            "rejected_count": 1,
            "present_count": 1,
            "null_count": 0,
            "stale_count": 1,
        },
        {
            "decision_at": "2025-01-02T23:00:00Z",
            "feature_name": "volume",
            "universe_size": 2,
            "accepted_count": 1,
            "rejected_count": 1,
            "present_count": 1,
            "null_count": 0,
            "stale_count": 1,
        },
    ]
    null_case = next(item for item in report["null_cases"] if item["feature_name"] == "return_1d")
    assert null_case["reason"] == "insufficient_history"
    assert null_case["lineage"]["sources"] == ["yahoo"]
    assert null_case["lineage"]["raw_locators"] == ["chart/result[0]"]
    assert report["rejected_cases"] == [
        {
            "security_id": MISSING_SECURITY,
            "decision_at": "2025-01-02T23:00:00Z",
            "reason": "input_rejected",
            "detail": "no price rows are eligible at the requested decision_at",
            "lineage": {
                "manifests": [],
                "parts": [],
                "raw_locators": [],
                "sources": [],
                "input_row_count": 0,
            },
        }
    ]
    assert report["stale_inputs"][0]["age_seconds"] == 82800


def test_feature_quality_cli_emits_schema_shaped_json(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    batch_path, features_root, data_root = _publish_quality_batch(tmp_path)
    from research.feature_quality_cli import main

    assert main(
        [
            "report",
            "--manifest",
            str(batch_path),
            "--features-root",
            str(features_root),
            "--data-root",
            str(data_root),
            "--registry",
            str(REGISTRY_PATH),
        ]
    ) == 0
    output = json.loads(capsys.readouterr().out)
    assert output["report_version"] == "1.0.0"
    assert output["batch"]["registry_sha256"] == load_feature_registry(REGISTRY_PATH).registry_sha256


def test_feature_quality_report_fails_closed_when_selected_input_is_tampered(tmp_path: Path) -> None:
    batch_path, features_root, data_root = _publish_quality_batch(tmp_path)
    batch = json.loads(batch_path.read_text(encoding="utf-8"))
    selected_part = batch["selected_input_parts"][0]["path"]
    input_part = next((data_root / "normalized").rglob(selected_part))
    input_part.write_bytes(input_part.read_bytes() + b"tampered")

    with pytest.raises(FeatureQualityReportError, match="selected input part hash mismatch"):
        build_feature_quality_report(
            batch_path,
            features_root=features_root,
            data_root=data_root,
            registry=load_feature_registry(REGISTRY_PATH),
        )
