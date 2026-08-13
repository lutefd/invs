from __future__ import annotations

import hashlib
import json
import sys
from decimal import Decimal
from pathlib import Path
from uuid import UUID

import duckdb
import pytest

from research import ResearchCatalog
from research.feature_cli import main as feature_cli_main
from research.features import (
    FEATURE_NAMES,
    FeatureArtifactConflictError,
    FeatureArtifactError,
    compute_input_fingerprint,
    compute_market_basic_features,
    publish_market_basic,
    read_feature_artifact,
)

SECURITY_ID = "469fc20f-7d4b-45bb-b827-05f8410e71aa"
SOURCE_ID = "5d6ac836-54fd-4df2-a745-0744180420db"
RUN_ID = "c7286917-ce45-4879-834f-fc975c80c49e"


def _sql(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def _price_select(row: dict[str, str | None], index: int) -> str:
    volume = "NULL::VARCHAR" if row["volume"] is None else f"{_sql(row['volume'])}::VARCHAR"
    has_volume = "false" if row["volume"] is None else "true"
    return f"""
        SELECT
          '1.0.0'::VARCHAR AS schema_version, 'yahoo'::VARCHAR AS source,
          '{SECURITY_ID}'::VARCHAR AS security_id, '1d'::VARCHAR AS interval,
          'raw'::VARCHAR AS price_basis, 'USD'::VARCHAR AS currency,
          TIMESTAMPTZ '{row['observed']}' AS observed_at,
          'second'::VARCHAR AS observed_precision,
          TIMESTAMPTZ '{row['observed']}' AS published_at, true AS has_published_at,
          'second'::VARCHAR AS published_precision,
          TIMESTAMPTZ '{row['available']}' AS available_at,
          TIMESTAMPTZ '{row['ingested']}' AS ingested_at,
          {_sql(row['open'] or '0')}::VARCHAR AS open,
          {_sql(row['high'] or '0')}::VARCHAR AS high,
          {_sql(row['low'] or '0')}::VARCHAR AS low,
          {_sql(row['close'] or '0')}::VARCHAR AS close,
          {volume} AS volume, {has_volume} AS has_volume,
          repeat('{chr(97 + index)}', 64)::VARCHAR AS raw_payload_hash,
          '{SOURCE_ID}'::VARCHAR AS data_source_id,
          '{RUN_ID}'::VARCHAR AS ingestion_run_id,
          'chart/result[{index}]'::VARCHAR AS raw_record_locator,
          'go-v1'::VARCHAR AS normalizer_version
    """


def _write_prices(data_root: Path, rows: list[dict[str, str | None]]) -> Path:
    directory = data_root / "normalized" / "prices" / "source=yahoo" / f"security_id={SECURITY_ID}"
    directory.mkdir(parents=True, exist_ok=True)
    temporary = directory / "prices.pending.parquet"
    query = "\nUNION ALL\n".join(_price_select(row, index) for index, row in enumerate(rows))
    connection = duckdb.connect(":memory:")
    connection.execute(f"COPY ({query}) TO '{temporary}' (FORMAT PARQUET)")
    connection.close()
    digest = hashlib.sha256(temporary.read_bytes()).hexdigest()
    part = directory / f"part-{digest}.parquet"
    temporary.rename(part)
    manifest = {
        "manifest_version": 1,
        "schema_version": "1.0.0",
        "normalizer_version": "go-v1",
        "git_commit": "0" * 40,
        "source": "yahoo",
        "data_source_id": SOURCE_ID,
        "ingestion_run_id": RUN_ID,
        "partition": {
            "dataset": "prices",
            "source": "yahoo",
            "security_id": SECURITY_ID,
        },
        "row_count": len(rows),
        "parts": [{"path": part.name, "sha256": digest, "row_count": len(rows)}],
    }
    (directory / "manifest.json").write_text(
        json.dumps(manifest, indent=2) + "\n", encoding="utf-8"
    )
    return directory / "manifest.json"


def _catalog(tmp_path: Path, rows: list[dict[str, str | None]]) -> ResearchCatalog:
    data_root = tmp_path / "data"
    _write_prices(data_root, rows)
    return ResearchCatalog(data_root).register()


def _two_days() -> list[dict[str, str | None]]:
    return [
        {
            "observed": "2025-01-01 21:00:00Z",
            "available": "2025-01-02 00:00:00Z",
            "ingested": "2025-01-02 00:01:00Z",
            "open": "99.000000000000000000",
            "high": "100.500000000000000000",
            "low": "98.000000000000000000",
            "close": "100.000000000000000000",
            "volume": "100000000000000000.125",
        },
        {
            "observed": "2025-01-02 21:00:00Z",
            "available": "2025-01-02 22:00:00Z",
            "ingested": "2025-01-02 22:01:00Z",
            "open": "100.250000000000000000",
            "high": "101.500000000000000001",
            "low": "98.250000000000000001",
            "close": "101.000000000000000001",
            "volume": "123456789012345678.0001",
        },
        {
            # It is available before the cutoff but observed in the future.
            "observed": "2025-01-03 21:00:00Z",
            "available": "2025-01-01 00:00:00Z",
            "ingested": "2025-01-03 22:01:00Z",
            "open": "998",
            "high": "1001",
            "low": "997",
            "close": "999",
            "volume": "999999",
        },
    ]


def test_market_basic_exact_decimals_point_in_time_and_lineage(tmp_path: Path) -> None:
    catalog = _catalog(tmp_path, _two_days())
    decision_at = "2025-01-02T23:00:00Z"
    manifest_path = publish_market_basic(
        catalog,
        decision_at=decision_at,
        security_id=SECURITY_ID,
        features_root=tmp_path / "features",
        computation_delay_seconds=30,
    )

    artifact = read_feature_artifact(manifest_path)
    manifest = artifact.manifest
    row = artifact.observations[0]
    features = row["features"]

    assert features["close"] == "101.000000000000000001"
    assert features["range_1d"] == "3.250000000000000000"
    assert features["volume"] == "123456789012345678.0001"
    expected_return = format(
        Decimal("101.000000000000000001") / Decimal("100.000000000000000000") - Decimal(1),
        "f",
    )
    assert features["return_1d"] == expected_return
    assert features["close"] != "999"
    assert features["return_1d"] is not None

    assert manifest["decision_at"] == decision_at
    assert manifest["input_available_at"] == "2025-01-02T22:00:00Z"
    assert manifest["available_at"] == "2025-01-02T22:00:30Z"
    assert row["available_at"] == manifest["available_at"]
    assert manifest["selected_input_manifests"]
    assert manifest["selected_input_parts"]
    assert manifest["input_fingerprint"] == compute_input_fingerprint(manifest)
    assert manifest["row_count"] == 1
    assert row["artifact"] == manifest["artifact"]


def test_missing_lookback_and_missing_volume_are_null(tmp_path: Path) -> None:
    catalog = _catalog(
        tmp_path,
        [
            {
                "observed": "2025-01-02 21:00:00Z",
                "available": "2025-01-02 22:00:00Z",
                "ingested": "2025-01-02 22:01:00Z",
                "open": "10.00",
                "high": "11.00",
                "low": "9.50",
                "close": "10.25",
                "volume": None,
            }
        ],
    )
    features = compute_market_basic_features(
        catalog,
        decision_at="2025-01-03T00:00:00Z",
        security_id=SECURITY_ID,
    )
    assert features == {
        "close": "10.25",
        "return_1d": None,
        "range_1d": "1.50",
        "volume": None,
    }


def test_idempotent_publication_and_changed_input_conflict(tmp_path: Path) -> None:
    rows = _two_days()[:2]
    first_catalog = _catalog(tmp_path / "first", rows)
    features_root = tmp_path / "features"
    first = publish_market_basic(
        first_catalog,
        decision_at="2025-01-02T23:00:00Z",
        security_id=SECURITY_ID,
        features_root=features_root,
    )
    first_bytes = first.read_bytes()
    first_mtime = first.stat().st_mtime_ns
    second = publish_market_basic(
        first_catalog,
        decision_at="2025-01-02T23:00:00Z",
        security_id=SECURITY_ID,
        features_root=features_root,
    )
    assert second == first
    assert first.read_bytes() == first_bytes
    assert first.stat().st_mtime_ns == first_mtime

    changed = [dict(row) for row in rows]
    changed[-1]["close"] = "102.000000000000000001"
    changed_catalog = _catalog(tmp_path / "changed", changed)
    with pytest.raises(FeatureArtifactConflictError):
        publish_market_basic(
            changed_catalog,
            decision_at="2025-01-02T23:00:00Z",
            security_id=SECURITY_ID,
            features_root=features_root,
        )


def test_tampered_and_unlisted_parts_are_rejected(tmp_path: Path) -> None:
    catalog = _catalog(tmp_path, _two_days()[:2])
    manifest_path = publish_market_basic(
        catalog,
        decision_at="2025-01-02T23:00:00Z",
        security_id=SECURITY_ID,
        features_root=tmp_path / "features",
    )
    artifact = read_feature_artifact(manifest_path)
    part_path = artifact.part_paths[0]
    part_path.write_bytes(part_path.read_bytes() + b"tampered")
    with pytest.raises(FeatureArtifactError):
        read_feature_artifact(manifest_path)

    # Use a fresh artifact because the first one is intentionally left tampered.
    fresh_manifest = publish_market_basic(
        _catalog(tmp_path / "fresh-input", _two_days()[:2]),
        decision_at="2025-01-02T23:00:00Z",
        security_id=SECURITY_ID,
        features_root=tmp_path / "fresh-features",
    )
    fresh_artifact = read_feature_artifact(fresh_manifest)
    orphan_content = b"unlisted parquet bytes"
    orphan_hash = hashlib.sha256(orphan_content).hexdigest()
    (fresh_artifact.manifest_path.parent / f"part-{orphan_hash}.parquet").write_bytes(orphan_content)
    with pytest.raises(FeatureArtifactError):
        read_feature_artifact(fresh_manifest)


@pytest.mark.parametrize(
    "mutation",
    [
        lambda document: document.update(feature_set_version="2.0.0"),
        lambda document: document.update(feature_names=[*FEATURE_NAMES, "future_feature"]),
    ],
)
def test_unknown_feature_or_version_is_rejected(tmp_path: Path, mutation) -> None:
    manifest_path = publish_market_basic(
        _catalog(tmp_path, _two_days()[:2]),
        decision_at="2025-01-02T23:00:00Z",
        security_id=SECURITY_ID,
        features_root=tmp_path / "features",
    )
    document = json.loads(manifest_path.read_text(encoding="utf-8"))
    mutation(document)
    manifest_path.write_text(json.dumps(document), encoding="utf-8")
    with pytest.raises(FeatureArtifactError):
        read_feature_artifact(manifest_path)


def test_unknown_publish_version_fails_closed(tmp_path: Path) -> None:
    with pytest.raises(FeatureArtifactError):
        publish_market_basic(
            _catalog(tmp_path, _two_days()[:2]),
            decision_at="2025-01-02T23:00:00Z",
            security_id=SECURITY_ID,
            features_root=tmp_path / "features",
            feature_set_version="2.0.0",
        )


def test_feature_module_has_no_strategy_backtest_or_ml_imports() -> None:
    import research.features as features_module

    forbidden = ("strategy", "backtest", "sklearn", "torch", "tensorflow", "xgboost")
    imported = set(sys.modules)
    assert not any(any(word in module.lower() for word in forbidden) for module in imported)
    source = Path(features_module.__file__).read_text(encoding="utf-8").lower()
    assert not any(word in source for word in forbidden)
    assert UUID(SECURITY_ID).version == 4
    assert features_module.DEFAULT_GENERATOR_VERSION


def test_feature_cli_publishes_idempotently_and_validates(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    catalog = _catalog(tmp_path, _two_days()[:2])
    features_root = tmp_path / "features"
    publish_args = [
        "publish",
        "--data-root",
        str(catalog.data_root),
        "--features-root",
        str(features_root),
        "--security-id",
        SECURITY_ID,
        "--decision-at",
        "2025-01-02T23:00:00Z",
        "--computation-delay-seconds",
        "30",
        "--git-commit",
        "0" * 40,
    ]

    assert feature_cli_main(publish_args) == 0
    first = json.loads(capsys.readouterr().out)
    assert first["action"] == "published"
    assert first["row_count"] == 1
    assert first["features"]["close"] == "101.000000000000000001"
    manifest_path = Path(first["manifest_path"])
    manifest_mtime = manifest_path.stat().st_mtime_ns

    assert feature_cli_main(publish_args) == 0
    second = json.loads(capsys.readouterr().out)
    assert second == first
    assert manifest_path.stat().st_mtime_ns == manifest_mtime

    assert feature_cli_main(["validate", "--manifest", str(manifest_path)]) == 0
    validated = json.loads(capsys.readouterr().out)
    assert validated["action"] == "validated"
    assert validated["artifact_id"] == first["artifact_id"]


def test_feature_cli_reports_validation_errors(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    missing = tmp_path / "missing" / "manifest.json"
    assert feature_cli_main(["validate", "--manifest", str(missing)]) == 1
    captured = capsys.readouterr()
    assert captured.out == ""
    assert "does not exist" in captured.err
