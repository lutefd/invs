from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path

import duckdb
import pytest

from research.batches import (
    FeatureBatchValidationError,
    publish_feature_batch,
    read_feature_batch,
)
from research.catalog import ResearchCatalog
from research.feature_cli import main as feature_cli_main
from research.registry import load_feature_registry

SECURITY_ONE = "469fc20f-7d4b-45bb-b827-05f8410e71aa"
SECURITY_TWO = "00000000-0000-4000-8000-000000000002"
MISSING_SECURITY = "00000000-0000-4000-8000-000000000099"
SOURCE_ID = "5d6ac836-54fd-4df2-a745-0744180420db"
RUN_ID = "c7286917-ce45-4879-834f-fc975c80c49e"
REGISTRY_PATH = Path(
    os.environ.get(
        "INVS_SCHEMA_ROOT",
        str(Path(__file__).resolve().parents[2] / "schemas"),
    )
) / "feature-set-registry.json"
CALENDAR_PIN = {
    "data_source_id": "11111111-1111-4111-8111-111111111111",
    "mic": "XNAS",
    "calendar_version": "xnas_2025_fixture",
    "session_fingerprint": "f" * 64,
    "calendar_available_at": "2024-12-01T00:00:00Z",
    "decision_clock_policy": "after_close_next_session",
}


def _sql(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def _price_select(security_id: str, row: dict[str, str], index: int) -> str:
    return f"""
        SELECT
          '1.0.0'::VARCHAR AS schema_version, 'yahoo'::VARCHAR AS source,
          '{security_id}'::VARCHAR AS security_id, '1d'::VARCHAR AS interval,
          'raw'::VARCHAR AS price_basis, 'USD'::VARCHAR AS currency,
          TIMESTAMPTZ '{row['observed']}' AS observed_at,
          'second'::VARCHAR AS observed_precision,
          TIMESTAMPTZ '{row['observed']}' AS published_at, true AS has_published_at,
          'second'::VARCHAR AS published_precision,
          TIMESTAMPTZ '{row['available']}' AS available_at,
          TIMESTAMPTZ '{row['ingested']}' AS ingested_at,
          {_sql(row['open'])}::VARCHAR AS open,
          {_sql(row['high'])}::VARCHAR AS high,
          {_sql(row['low'])}::VARCHAR AS low,
          {_sql(row['close'])}::VARCHAR AS close,
          {_sql(row['volume'])}::VARCHAR AS volume, true AS has_volume,
          repeat('{chr(97 + index)}', 64)::VARCHAR AS raw_payload_hash,
          '{SOURCE_ID}'::VARCHAR AS data_source_id,
          '{RUN_ID}'::VARCHAR AS ingestion_run_id,
          'chart/result[{index}]'::VARCHAR AS raw_record_locator,
          'go-v1'::VARCHAR AS normalizer_version
    """


def _rows(offset: int) -> list[dict[str, str]]:
    return [
        {
            "observed": "2025-01-01 21:00:00Z",
            "available": "2025-01-02 00:00:00Z",
            "ingested": "2025-01-02 00:01:00Z",
            "open": str(99 + offset),
            "high": str(101 + offset),
            "low": str(98 + offset),
            "close": str(100 + offset),
            "volume": "100000",
        },
        {
            "observed": "2025-01-02 21:00:00Z",
            "available": "2025-01-02 22:00:00Z",
            "ingested": "2025-01-02 22:01:00Z",
            "open": str(100 + offset),
            "high": str(103 + offset),
            "low": str(99 + offset),
            "close": str(102 + offset),
            "volume": "110000",
        },
        {
            "observed": "2025-01-03 21:00:00Z",
            "available": "2025-01-03 22:00:00Z",
            "ingested": "2025-01-03 22:01:00Z",
            "open": str(102 + offset),
            "high": str(104 + offset),
            "low": str(101 + offset),
            "close": str(103 + offset),
            "volume": "120000",
        },
    ]


def _write_prices(data_root: Path, security_id: str, rows: list[dict[str, str]]) -> None:
    directory = data_root / "normalized" / "prices" / "source=yahoo" / f"security_id={security_id}"
    directory.mkdir(parents=True, exist_ok=True)
    temporary = directory / "prices.pending.parquet"
    query = "\nUNION ALL\n".join(
        _price_select(security_id, row, index) for index, row in enumerate(rows)
    )
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
        "partition": {"dataset": "prices", "source": "yahoo", "security_id": security_id},
        "row_count": len(rows),
        "parts": [{"path": part.name, "sha256": digest, "row_count": len(rows)}],
    }
    (directory / "manifest.json").write_text(
        json.dumps(manifest, indent=2) + "\n", encoding="utf-8"
    )


def _catalog(tmp_path: Path, *, include_second: bool = True) -> ResearchCatalog:
    data_root = tmp_path / "data"
    _write_prices(data_root, SECURITY_ONE, _rows(0))
    if include_second:
        _write_prices(data_root, SECURITY_TWO, _rows(10))
    return ResearchCatalog(data_root).register()


def _publish(catalog: ResearchCatalog, features_root: Path, security_ids: list[str]) -> Path:
    return publish_feature_batch(
        catalog,
        registry=load_feature_registry(REGISTRY_PATH),
        security_ids=security_ids,
        decision_ats=["2025-01-03T23:00:00Z", "2025-01-02T23:00:00Z"],
        calendar_pin=CALENDAR_PIN,
        features_root=features_root,
        git_commit="0" * 40,
    )


def test_batch_is_deterministic_resumable_and_lineage_complete(tmp_path: Path) -> None:
    features_root = tmp_path / "features"
    registry = load_feature_registry(REGISTRY_PATH)
    first = _publish(_catalog(tmp_path), features_root, [SECURITY_TWO, SECURITY_ONE])
    first_bytes = first.read_bytes()
    first_mtime = first.stat().st_mtime_ns

    artifact = read_feature_batch(first, features_root=features_root, registry=registry)
    manifest = artifact.manifest
    assert artifact.row_count == 4
    assert manifest["universe"]["security_ids"] == sorted([SECURITY_ONE, SECURITY_TWO])
    assert manifest["decision_schedule"] == [
        "2025-01-02T23:00:00Z",
        "2025-01-03T23:00:00Z",
    ]
    assert manifest["input_fitness"] == [
        {
            "dataset": "prices",
            "historical_fitness": "installation_replay_only",
            "availability_policy": "conservative_receipt_time",
        }
    ]
    assert manifest["run_summary"] == {
        "requested_partitions": 4,
        "accepted_partitions": 4,
        "rejected_partitions": 0,
        "row_count": 4,
        "status": "completed",
    }
    assert len(manifest["selected_input_manifests"]) == 2
    assert len(manifest["selected_input_parts"]) == 2

    second = _publish(_catalog(tmp_path / "replay"), features_root, [SECURITY_ONE, SECURITY_TWO])
    assert second == first
    assert first.read_bytes() == first_bytes
    assert first.stat().st_mtime_ns == first_mtime


def test_market_momentum_batch_uses_registered_producer(tmp_path: Path) -> None:
    features_root = tmp_path / "features"
    registry = load_feature_registry(REGISTRY_PATH)
    batch = publish_feature_batch(
        _catalog(tmp_path),
        registry=registry,
        security_ids=[SECURITY_ONE],
        decision_ats=["2025-01-03T23:00:00Z"],
        calendar_pin=CALENDAR_PIN,
        features_root=features_root,
        feature_set="market-momentum",
        feature_set_version="1.0.0",
        git_commit="0" * 40,
    )

    artifact = read_feature_batch(batch, features_root=features_root, registry=registry)
    assert artifact.manifest["feature_set"] == "market-momentum"
    assert artifact.manifest["run_summary"]["accepted_partitions"] == 1
    assert artifact.observations[0]["features"] == {
        "return_1m": None,
        "return_3m": None,
        "return_6m": None,
        "return_12m": None,
        "realized_volatility_1m": None,
        "max_drawdown_1m": None,
    }


def test_batch_records_missing_security_as_explicit_rejection(tmp_path: Path) -> None:
    features_root = tmp_path / "features"
    batch = publish_feature_batch(
        _catalog(tmp_path, include_second=False),
        registry=load_feature_registry(REGISTRY_PATH),
        security_ids=[SECURITY_ONE, MISSING_SECURITY],
        decision_ats=["2025-01-02T23:00:00Z"],
        calendar_pin=CALENDAR_PIN,
        features_root=features_root,
        git_commit="0" * 40,
    )
    artifact = read_feature_batch(
        batch, features_root=features_root, registry=load_feature_registry(REGISTRY_PATH)
    )
    assert artifact.row_count == 1
    assert artifact.manifest["run_summary"]["rejected_partitions"] == 1
    assert artifact.manifest["rejected"][0]["security_id"] == MISSING_SECURITY
    assert artifact.manifest["rejected"][0]["reason"] == "input_rejected"


def test_batch_fails_closed_when_a_child_part_is_tampered(tmp_path: Path) -> None:
    features_root = tmp_path / "features"
    batch = _publish(_catalog(tmp_path), features_root, [SECURITY_ONE])
    manifest = json.loads(batch.read_text(encoding="utf-8"))
    (features_root / manifest["parts"][0]["part_path"]).write_bytes(
        (features_root / manifest["parts"][0]["part_path"]).read_bytes() + b"tampered"
    )
    with pytest.raises(FeatureBatchValidationError):
        read_feature_batch(batch, features_root=features_root)


def test_batch_cli_publishes_and_validates_explicit_files(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    data_root = tmp_path / "data"
    _write_prices(data_root, SECURITY_ONE, _rows(0))
    features_root = data_root / "features"
    universe_path = tmp_path / "universe.json"
    schedule_path = tmp_path / "schedule.json"
    calendar_path = tmp_path / "calendar.json"
    universe_path.write_text(json.dumps({"security_ids": [SECURITY_ONE]}), encoding="utf-8")
    schedule_path.write_text(
        json.dumps({"decision_ats": ["2025-01-02T23:00:00Z"]}), encoding="utf-8"
    )
    calendar_path.write_text(json.dumps(CALENDAR_PIN), encoding="utf-8")

    assert feature_cli_main(
        [
            "batch-publish",
            "--data-root",
            str(data_root),
            "--features-root",
            str(features_root),
            "--registry",
            str(REGISTRY_PATH),
            "--universe",
            str(universe_path),
            "--schedule",
            str(schedule_path),
            "--calendar-pin",
            str(calendar_path),
            "--git-commit",
            "0" * 40,
        ]
    ) == 0
    published = json.loads(capsys.readouterr().out)
    assert published["action"] == "published"
    assert published["accepted_partitions"] == 1

    assert feature_cli_main(
        [
            "batch-validate",
            "--manifest",
            published["manifest_path"],
            "--features-root",
            str(features_root),
            "--registry",
            str(REGISTRY_PATH),
        ]
    ) == 0
    validated = json.loads(capsys.readouterr().out)
    assert validated["action"] == "validated"
    assert validated["batch_id"] == published["batch_id"]
