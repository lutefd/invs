from __future__ import annotations

import hashlib
import json
from datetime import UTC, datetime
from pathlib import Path

import pytest

from research.measurement import MeasurementValidationError, measure_prediction

MEASURED_AT = datetime(2026, 9, 30, 21, 0, tzinfo=UTC)


def _artifact(tmp_path: Path) -> tuple[Path, str]:
    path = tmp_path / "prices.json"
    document = {
        "schema_version": "1.0.0",
        "artifact_id": "fixture-prices-2026-09",
        "available_at": "2026-09-30T21:00:00Z",
        "rows": [
            {"asset_id": "asset-a", "observed_at": "2026-08-29T21:00:00Z", "close": "100"},
            {"asset_id": "asset-a", "observed_at": "2026-09-30T21:00:00Z", "close": "120"},
            {"asset_id": "asset-b", "observed_at": "2026-08-29T21:00:00Z", "close": "80"},
            {"asset_id": "asset-b", "observed_at": "2026-09-30T21:00:00Z", "close": "88"},
            {"asset_id": "benchmark", "observed_at": "2026-08-29T21:00:00Z", "close": "200"},
            {"asset_id": "benchmark", "observed_at": "2026-09-30T21:00:00Z", "close": "210"},
        ],
    }
    path.write_bytes(json.dumps(document, sort_keys=True, separators=(",", ":")).encode())
    return path, hashlib.sha256(path.read_bytes()).hexdigest()


def _prediction(direction: str = "relative_outperformance") -> dict:
    return {
        "id": "dddddddd-dddd-4ddd-8ddd-dddddddddddd",
        "status": "frozen",
        "expected_direction": direction,
        "asset_or_universe": ["asset-a", "asset-b"],
    }


def test_measurement_computes_basket_benchmark_and_hit(tmp_path: Path) -> None:
    path, digest = _artifact(tmp_path)
    outcome = measure_prediction(
        _prediction(),
        price_artifact=path,
        price_artifact_sha256=digest,
        benchmark_asset_id="benchmark",
        measured_at=MEASURED_AT,
        input_artifact_refs=[{"kind": "price-fixture", "id": "fixture-prices-2026-09", "sha256": digest}],
    )

    assert outcome["status"] == "measured"
    assert outcome["realized_result"]["basket_return"] == "0.15"
    assert outcome["benchmark_result"]["relative_return"] == "0.10"
    assert outcome["realized_result"]["hit"] is True


def test_measurement_rejects_unpinned_or_future_artifact(tmp_path: Path) -> None:
    path, digest = _artifact(tmp_path)
    with pytest.raises(MeasurementValidationError, match="hash mismatch"):
        measure_prediction(
            _prediction(),
            price_artifact=path,
            price_artifact_sha256="a" * 64,
            benchmark_asset_id="benchmark",
            measured_at=MEASURED_AT,
            input_artifact_refs=[{"kind": "price-fixture", "id": "fixture", "sha256": digest}],
        )

    with pytest.raises(MeasurementValidationError, match="not available"):
        measure_prediction(
            _prediction(),
            price_artifact=path,
            price_artifact_sha256=digest,
            benchmark_asset_id="benchmark",
            measured_at=datetime(2026, 9, 1, tzinfo=UTC),
            input_artifact_refs=[{"kind": "price-fixture", "id": "fixture", "sha256": digest}],
        )
