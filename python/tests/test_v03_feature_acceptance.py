from __future__ import annotations

import hashlib
import json
import os
import shutil
from pathlib import Path
from uuid import UUID, uuid5

import duckdb
import pytest

from research.batches import (
    FeatureBatchConflictError,
    FeatureBatchValidationError,
    publish_feature_batch,
    read_feature_batch,
)
from research.catalog import DatasetSchemaError, ResearchCatalog
from research.features import publish_feature_artifact
from research.registry import load_feature_registry

ROOT = Path(__file__).resolve().parents[2]
SCHEMA_ROOT = Path(os.environ.get("INVS_SCHEMA_ROOT", str(ROOT / "schemas")))
REGISTRY_PATH = SCHEMA_ROOT / "feature-set-registry.json"
TAXONOMY_PATH = SCHEMA_ROOT / "feature-taxonomy-registry.json"
COMMIT = "0" * 40
SOURCE_ID = "5d6ac836-54fd-4df2-a745-0744180420db"
RUN_ID = "c7286917-ce45-4879-834f-fc975c80c49e"
CALENDAR_PIN = {
    "data_source_id": "6c8d9b2a-4c2f-4e03-bf84-2bd5bb3b817e",
    "mic": "XNYS",
    "calendar_version": "xnys-2024-fixture",
    "session_fingerprint": "a" * 64,
    "calendar_available_at": "2024-01-01T00:00:00Z",
    "decision_clock_policy": "after_close_next_session",
}
DECISIONS = ["2024-02-01T00:00:00Z", "2025-02-01T00:00:00Z"]
SECURITY_NAMESPACE = UUID("0d9c6dc8-52db-4a7d-9b55-87b6a91d33a1")
ISSUER_NAMESPACE = UUID("c27d4a45-b1b4-4f48-8f98-03ef3eab9e56")
SECURITY_IDS = tuple(str(uuid5(SECURITY_NAMESPACE, f"security-{index}")) for index in range(20))
ISSUER_IDS = tuple(str(uuid5(ISSUER_NAMESPACE, f"issuer-{index}")) for index in range(20))
SECURITY_MAPPINGS = dict(zip(SECURITY_IDS, ISSUER_IDS, strict=True))


def _sql(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def _write_part(directory: Path, query: str, manifest: dict[str, object], row_count: int) -> None:
    directory.mkdir(parents=True, exist_ok=True)
    temporary = directory / "pending.parquet"
    connection = duckdb.connect(":memory:")
    try:
        connection.execute(f"COPY ({query}) TO {_sql(str(temporary))} (FORMAT PARQUET)")
    finally:
        connection.close()
    digest = hashlib.sha256(temporary.read_bytes()).hexdigest()
    part = directory / f"part-{digest}.parquet"
    temporary.rename(part)
    manifest["row_count"] = row_count
    manifest["parts"] = [{"path": part.name, "sha256": digest, "row_count": row_count}]
    (directory / "manifest.json").write_text(
        json.dumps(manifest, indent=2) + "\n", encoding="utf-8"
    )


def _write_prices(data_root: Path) -> None:
    rows = [
        ("2024-01-01 21:00:00Z", "2024-01-02 00:00:00Z", "100", "101", "99", "100000"),
        ("2024-01-02 21:00:00Z", "2024-01-03 00:00:00Z", "101", "103", "100", "110000"),
        ("2025-01-02 21:00:00Z", "2025-01-03 00:00:00Z", "102", "105", "101", "120000"),
    ]
    for index, security_id in enumerate(SECURITY_IDS):
        queries = []
        for row_index, (observed, available, open_value, high, low, volume) in enumerate(rows):
            raw_hash = hashlib.sha256(f"price:{index}:{row_index}".encode()).hexdigest()
            offset = index * 10
            queries.append(
                f"""
                    SELECT
                      '1.0.0'::VARCHAR AS schema_version,
                      'yahoo'::VARCHAR AS source,
                      {_sql(security_id)}::VARCHAR AS security_id,
                      '1d'::VARCHAR AS interval,
                      'raw'::VARCHAR AS price_basis,
                      'USD'::VARCHAR AS currency,
                      TIMESTAMPTZ '{observed}' AS observed_at,
                      'second'::VARCHAR AS observed_precision,
                      TIMESTAMPTZ '{observed}' AS published_at,
                      true AS has_published_at,
                      'second'::VARCHAR AS published_precision,
                      TIMESTAMPTZ '{available}' AS available_at,
                      TIMESTAMPTZ '{available}' AS ingested_at,
                      {_sql(str(int(open_value) + offset))}::VARCHAR AS open,
                      {_sql(str(int(high) + offset))}::VARCHAR AS high,
                      {_sql(str(int(low) + offset))}::VARCHAR AS low,
                      {_sql(str(int(open_value) + offset + (row_index + 1)))}::VARCHAR AS close,
                      {_sql(volume)}::VARCHAR AS volume,
                      true AS has_volume,
                      {_sql(raw_hash)}::VARCHAR AS raw_payload_hash,
                      {_sql(SOURCE_ID)}::VARCHAR AS data_source_id,
                      {_sql(RUN_ID)}::VARCHAR AS ingestion_run_id,
                      {_sql(f'chart/{security_id}/{row_index}')}::VARCHAR AS raw_record_locator,
                      'go-v1'::VARCHAR AS normalizer_version
                """
            )
        directory = (
            data_root
            / "normalized"
            / "prices"
            / "source=yahoo"
            / f"security_id={security_id}"
        )
        _write_part(
            directory,
            "\nUNION ALL\n".join(queries),
            {
                "manifest_version": 1,
                "schema_version": "1.0.0",
                "normalizer_version": "go-v1",
                "git_commit": COMMIT,
                "source": "yahoo",
                "data_source_id": SOURCE_ID,
                "ingestion_run_id": RUN_ID,
                "partition": {"dataset": "prices", "source": "yahoo", "security_id": security_id},
            },
            len(rows),
        )


def _fundamental_query(issuer_id: str, issuer_index: int) -> str:
    rows = [
        ("Revenue", "2023-01-01", "2023-12-31", "2024-01-20 12:00:00", "100", "revenue-2023"),
        ("Revenue", "2024-01-01", "2024-12-31", "2025-01-20 12:00:00", "120", "revenue-2024"),
        (
            "OperatingIncomeLoss",
            "2023-01-01",
            "2023-12-31",
            "2024-01-20 12:00:00",
            "20",
            "operating-2023",
        ),
        (
            "OperatingIncomeLoss",
            "2024-01-01",
            "2024-12-31",
            "2025-01-20 12:00:00",
            "30",
            "operating-2024",
        ),
    ]
    queries = []
    for concept, period_start, period_end, available, value, accession_suffix in rows:
        raw_hash = hashlib.sha256(f"fundamental:{issuer_index}:{accession_suffix}".encode()).hexdigest()
        queries.append(
            f"""
                SELECT
                  '1.0.0'::VARCHAR AS schema_version,
                  'sec'::VARCHAR AS source,
                  {_sql(issuer_id)}::VARCHAR AS issuer_id,
                  ''::VARCHAR AS security_id,
                  false AS has_security_id,
                  'us-gaap'::VARCHAR AS taxonomy,
                  {_sql(concept)}::VARCHAR AS concept,
                  'USD'::VARCHAR AS unit,
                  'USD'::VARCHAR AS currency,
                  true AS has_currency,
                  TIMESTAMPTZ '{period_end} 00:00:00Z' AS observed_at,
                  'date'::VARCHAR AS observed_precision,
                  TIMESTAMPTZ '{available}Z' AS published_at,
                  'second'::VARCHAR AS published_precision,
                  TIMESTAMPTZ '{available}Z' AS available_at,
                  TIMESTAMPTZ '{available}Z' AS ingested_at,
                  DATE '{period_start}' AS period_start,
                  true AS has_period_start,
                  DATE '{period_end}' AS period_end,
                  {_sql(value)}::VARCHAR AS value,
                  true AS has_value,
                  0::INTEGER AS revision,
                  {_sql(f'{issuer_index}-{accession_suffix}')}::VARCHAR AS accession_number,
                  '10-K'::VARCHAR AS form,
                  CAST(substr('{period_end}', 1, 4) AS INTEGER) AS fiscal_year,
                  'Q4'::VARCHAR AS fiscal_period,
                  ''::VARCHAR AS frame,
                  {_sql(raw_hash)}::VARCHAR AS raw_payload_hash,
                  {_sql(SOURCE_ID)}::VARCHAR AS data_source_id,
                  {_sql(RUN_ID)}::VARCHAR AS ingestion_run_id,
                  {_sql(f'companyfacts/{issuer_id}/{accession_suffix}')}::VARCHAR AS raw_record_locator,
                  'go-v1'::VARCHAR AS normalizer_version
            """
        )
    return "\nUNION ALL\n".join(queries)


def _write_fundamentals(data_root: Path) -> None:
    for index, issuer_id in enumerate(ISSUER_IDS):
        directory = data_root / "normalized" / "fundamentals" / "source=sec" / f"issuer_id={issuer_id}"
        _write_part(
            directory,
            _fundamental_query(issuer_id, index),
            {
                "manifest_version": 1,
                "schema_version": "1.0.0",
                "normalizer_version": "go-v1",
                "git_commit": COMMIT,
                "source": "sec",
                "data_source_id": SOURCE_ID,
                "ingestion_run_id": RUN_ID,
                "partition": {"dataset": "fundamentals", "source": "sec", "issuer_id": issuer_id},
            },
            4,
        )


def _macro_query() -> str:
    rows = [
        ("2023-01-01", "2023-01-15", "100", 0),
        ("2024-01-01", "2024-01-15", "110", 0),
        ("2024-01-01", "2025-01-15", "115", 1),
    ]
    queries = []
    for observed, available, value, revision in rows:
        raw_hash = hashlib.sha256(f"macro:{observed}:{revision}".encode()).hexdigest()
        queries.append(
            f"""
                SELECT
                  '1.0.0'::VARCHAR AS schema_version,
                  'alfred'::VARCHAR AS source,
                  'CPIAUCSL'::VARCHAR AS series_id,
                  'US'::VARCHAR AS geography,
                  'Index'::VARCHAR AS unit,
                  'monthly'::VARCHAR AS frequency,
                  'SA'::VARCHAR AS seasonal_adjustment,
                  true AS has_seasonal_adjustment,
                  TIMESTAMPTZ '{observed} 00:00:00Z' AS observed_at,
                  'date'::VARCHAR AS observed_precision,
                  TIMESTAMPTZ '{available} 00:00:00Z' AS published_at,
                  'date'::VARCHAR AS published_precision,
                  TIMESTAMPTZ '{available} 00:00:00Z' AS available_at,
                  TIMESTAMPTZ '{available} 00:00:00Z' AS ingested_at,
                  {_sql(value)}::VARCHAR AS value,
                  true AS has_value,
                  {revision}::INTEGER AS revision,
                  TIMESTAMPTZ '{available} 00:00:00Z' AS vintage_at,
                  true AS has_vintage_at,
                  {_sql(raw_hash)}::VARCHAR AS raw_payload_hash,
                  {_sql(SOURCE_ID)}::VARCHAR AS data_source_id,
                  {_sql(RUN_ID)}::VARCHAR AS ingestion_run_id,
                  {_sql(f'alfred/CPIAUCSL/{observed}/revision={revision}')}::VARCHAR AS raw_record_locator,
                  'go-v1'::VARCHAR AS normalizer_version
            """
        )
    return "\nUNION ALL\n".join(queries)


def _write_macro(data_root: Path) -> None:
    directory = data_root / "normalized" / "macroeconomics" / "source=alfred" / "series_id=CPIAUCSL"
    _write_part(
        directory,
        _macro_query(),
        {
            "manifest_version": 1,
            "schema_version": "1.0.0",
            "normalizer_version": "go-v1",
            "git_commit": COMMIT,
            "source": "alfred",
            "data_source_id": SOURCE_ID,
            "ingestion_run_id": RUN_ID,
            "partition": {"dataset": "macroeconomics", "source": "alfred", "series_id": "CPIAUCSL"},
        },
        3,
    )


def _catalog(data_root: Path) -> ResearchCatalog:
    _write_prices(data_root)
    _write_fundamentals(data_root)
    _write_macro(data_root)
    return ResearchCatalog(data_root).register()


def _batch_kwargs(feature_set: str, features_root: Path) -> dict[str, object]:
    kwargs: dict[str, object] = {
        "registry": load_feature_registry(REGISTRY_PATH),
        "security_ids": list(SECURITY_IDS),
        "decision_ats": DECISIONS,
        "calendar_pin": CALENDAR_PIN,
        "features_root": features_root,
        "feature_set": feature_set,
        "feature_set_version": "1.0.0",
        "git_commit": COMMIT,
    }
    if feature_set == "fundamental-growth":
        kwargs.update(
            {
                "security_mappings": SECURITY_MAPPINGS,
                "taxonomy_registry": TAXONOMY_PATH,
            }
        )
    if feature_set == "macro-state":
        kwargs.update(
            {
                "macro_source": "alfred",
                "macro_series_id": "CPIAUCSL",
                "macro_geography": "US",
                "macro_unit": "Index",
                "macro_frequency": "monthly",
            }
        )
    return kwargs


def _child_kwargs(feature_set: str, features_root: Path, security_id: str, decision_at: str) -> dict[str, object]:
    kwargs: dict[str, object] = {
        "decision_at": decision_at,
        "security_id": security_id,
        "calendar_pin": CALENDAR_PIN,
        "features_root": features_root,
        "feature_set": feature_set,
        "feature_set_version": "1.0.0",
        "git_commit": COMMIT,
    }
    if feature_set == "fundamental-growth":
        kwargs.update({"issuer_id": SECURITY_MAPPINGS[security_id], "taxonomy_registry": TAXONOMY_PATH})
    if feature_set == "macro-state":
        kwargs.update(
            {
                "macro_source": "alfred",
                "macro_series_id": "CPIAUCSL",
                "macro_geography": "US",
                "macro_unit": "Index",
                "macro_frequency": "monthly",
            }
        )
    return kwargs


def _publish_all(catalog: ResearchCatalog, features_root: Path, *, interrupted: bool) -> dict[str, Path]:
    result = {}
    for feature_set in ("market-basic", "fundamental-growth", "macro-state"):
        child_path = None
        child_bytes = None
        child_mtime = None
        if interrupted:
            child_path = publish_feature_artifact(
                catalog,
                **_child_kwargs(feature_set, features_root, SECURITY_IDS[0], DECISIONS[0]),
            )
            child_bytes = child_path.read_bytes()
            child_mtime = child_path.stat().st_mtime_ns
        batch_path = publish_feature_batch(catalog, **_batch_kwargs(feature_set, features_root))
        if child_path is not None:
            assert child_path.read_bytes() == child_bytes
            assert child_path.stat().st_mtime_ns == child_mtime
        result[feature_set] = batch_path
    return result


def _tree_bytes(root: Path) -> dict[str, bytes]:
    return {
        path.relative_to(root).as_posix(): path.read_bytes()
        for path in sorted(root.rglob("*"))
        if path.is_file()
    }


def test_v03_reproduces_interrupted_multi_asset_batches_in_a_clean_root(
    tmp_path: Path,
) -> None:
    source_data = tmp_path / "interrupted-data"
    clean_data = tmp_path / "clean-data"
    interrupted_features = tmp_path / "interrupted-features"
    clean_features = tmp_path / "clean-features"
    source_catalog = _catalog(source_data)
    shutil.copytree(source_data, clean_data)
    clean_catalog = ResearchCatalog(clean_data).register()

    interrupted = _publish_all(source_catalog, interrupted_features, interrupted=True)
    _publish_all(clean_catalog, clean_features, interrupted=False)

    assert len(SECURITY_IDS) == 20
    assert _tree_bytes(interrupted_features) == _tree_bytes(clean_features)
    registry = load_feature_registry(REGISTRY_PATH)
    fundamental = read_feature_batch(
        interrupted["fundamental-growth"],
        features_root=interrupted_features,
        registry=registry,
    )
    macro = read_feature_batch(
        interrupted["macro-state"],
        features_root=interrupted_features,
        registry=registry,
    )
    assert fundamental.manifest["run_summary"] == {
        "requested_partitions": 40,
        "accepted_partitions": 40,
        "rejected_partitions": 0,
        "row_count": 40,
        "status": "completed",
    }
    assert macro.manifest["run_summary"] == fundamental.manifest["run_summary"]
    fundamental_by_key = {
        (row["decision_at"], row["security_id"]): row for row in fundamental.observations
    }
    macro_by_key = {(row["decision_at"], row["security_id"]): row for row in macro.observations}
    assert fundamental_by_key[(DECISIONS[0], SECURITY_IDS[0])]["features"] == {
        "revenue": "100",
        "revenue_growth_yoy": None,
        "operating_margin": "0.2",
    }
    assert fundamental_by_key[(DECISIONS[1], SECURITY_IDS[0])]["features"] == {
        "revenue": "120",
        "revenue_growth_yoy": "0.2",
        "operating_margin": "0.25",
    }
    assert macro_by_key[(DECISIONS[0], SECURITY_IDS[0])]["features"] == {
        "macro_level": "110",
        "macro_change_yoy": "0.1",
    }
    assert macro_by_key[(DECISIONS[1], SECURITY_IDS[0])]["features"] == {
        "macro_level": "115",
        "macro_change_yoy": "0.15",
    }


def test_v03_tamper_boundaries_fail_closed(tmp_path: Path) -> None:
    data_root = tmp_path / "data"
    features_root = tmp_path / "features"
    catalog = _catalog(data_root)
    batches = _publish_all(catalog, features_root, interrupted=False)
    registry = load_feature_registry(REGISTRY_PATH)

    tampered_input = tmp_path / "tampered-input"
    shutil.copytree(data_root, tampered_input)
    input_part = next((tampered_input / "normalized").rglob("part-*.parquet"))
    input_part.write_bytes(input_part.read_bytes() + b"tampered")
    with pytest.raises(DatasetSchemaError, match="SHA-256 mismatch"):
        ResearchCatalog(tampered_input).register()

    tampered_output = tmp_path / "tampered-output"
    shutil.copytree(features_root, tampered_output)
    market_manifest = json.loads(batches["market-basic"].read_text(encoding="utf-8"))
    output_part = tampered_output / market_manifest["parts"][0]["part_path"]
    output_part.write_bytes(output_part.read_bytes() + b"tampered")
    with pytest.raises(FeatureBatchValidationError, match="hash mismatch"):
        read_feature_batch(
            tampered_output / batches["market-basic"].relative_to(features_root),
            features_root=tampered_output,
            registry=registry,
        )

    changed_registry_document = json.loads(REGISTRY_PATH.read_text(encoding="utf-8"))
    changed_registry_document["entries"][0]["description"] += " changed"
    changed_registry = tmp_path / "changed-feature-set-registry.json"
    changed_registry.write_text(json.dumps(changed_registry_document) + "\n", encoding="utf-8")
    with pytest.raises(FeatureBatchValidationError, match="registry fingerprint"):
        read_feature_batch(
            batches["market-basic"],
            features_root=features_root,
            registry=load_feature_registry(changed_registry),
        )

    changed_taxonomy_document = json.loads(TAXONOMY_PATH.read_text(encoding="utf-8"))
    changed_taxonomy_document["entries"][0]["review_note"] += " changed"
    changed_taxonomy = tmp_path / "changed-taxonomy.json"
    changed_taxonomy.write_text(json.dumps(changed_taxonomy_document) + "\n", encoding="utf-8")
    taxonomy_features = tmp_path / "taxonomy-features"
    shutil.copytree(features_root, taxonomy_features)
    with pytest.raises(FeatureBatchConflictError, match="immutable feature batch"):
        publish_feature_batch(
            catalog,
            **{
                **_batch_kwargs("fundamental-growth", taxonomy_features),
                "taxonomy_registry": changed_taxonomy,
            },
        )

    universe_document = json.loads(batches["market-basic"].read_text(encoding="utf-8"))
    universe_document["universe"]["security_ids"] = universe_document["universe"]["security_ids"][1:]
    tampered_universe = tmp_path / "tampered-universe" / "manifest.json"
    tampered_universe.parent.mkdir()
    tampered_universe.write_text(json.dumps(universe_document) + "\n", encoding="utf-8")
    with pytest.raises(FeatureBatchValidationError, match="universe fingerprint"):
        read_feature_batch(
            tampered_universe,
            features_root=features_root,
            registry=registry,
        )
