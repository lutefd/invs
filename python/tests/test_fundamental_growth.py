from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path

import duckdb
import pytest

from research.catalog import ResearchCatalog
from research.features import FeatureArtifactError, read_feature_artifact
from research.fundamental_growth import (
    compute_fundamental_growth_features,
    publish_fundamental_growth,
)
from research.taxonomy import load_taxonomy_registry

SECURITY_ID = "469fc20f-7d4b-45bb-b827-05f8410e71aa"
ISSUER_ID = "1b3d88f5-55b8-4dc5-a6be-2f77e9e99201"
SOURCE_ID = "5d6ac836-54fd-4df2-a745-0744180420db"
RUN_ID = "c7286917-ce45-4879-834f-fc975c80c49e"
ROOT = Path(__file__).resolve().parents[2]
REGISTRY_PATH = Path(
    os.environ.get("INVS_SCHEMA_ROOT", str(ROOT / "schemas"))
) / "feature-taxonomy-registry.json"
CALENDAR_PIN = {
    "data_source_id": "6c8d9b2a-4c2f-4e03-bf84-2bd5bb3b817e",
    "mic": "XNYS",
    "calendar_version": "xnas-2026-v1",
    "session_fingerprint": "a" * 64,
    "calendar_available_at": "2024-01-01T00:00:00Z",
    "decision_clock_policy": "after_close_next_session",
}


def _write_parquet(path: Path, query: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    connection = duckdb.connect(":memory:")
    connection.execute(f"COPY ({query}) TO '{path}' (FORMAT PARQUET)")
    connection.close()


def _write_manifest(directory: Path, part_paths: list[Path]) -> Path:
    parts = []
    for path in part_paths:
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        committed = path.with_name(f"part-{digest}.parquet")
        path.replace(committed)
        connection = duckdb.connect(":memory:")
        count = connection.execute(f"SELECT count(*) FROM read_parquet('{committed}')").fetchone()[0]
        connection.close()
        parts.append({"path": committed.name, "sha256": digest, "row_count": count})
    manifest = {
        "manifest_version": 1,
        "schema_version": "1.0.0",
        "normalizer_version": "go-v1",
        "git_commit": "0" * 40,
        "source": "sec",
        "data_source_id": SOURCE_ID,
        "ingestion_run_id": RUN_ID,
        "partition": {"dataset": "fundamentals", "source": "sec", "issuer_id": ISSUER_ID},
        "row_count": sum(item["row_count"] for item in parts),
        "parts": parts,
    }
    manifest_path = directory / "manifest.json"
    manifest_path.write_text(json.dumps(manifest) + "\n", encoding="utf-8")
    return manifest_path


def _fundamental_row(
    *,
    concept: str,
    period_start: str,
    period_end: str,
    available: str,
    value: str,
    accession: str,
) -> str:
    return f"""
        SELECT
          '1.0.0'::VARCHAR AS schema_version, 'sec'::VARCHAR AS source,
          '{ISSUER_ID}'::VARCHAR AS issuer_id, ''::VARCHAR AS security_id,
          false AS has_security_id, 'us-gaap'::VARCHAR AS taxonomy,
          '{concept}'::VARCHAR AS concept, 'USD'::VARCHAR AS unit,
          'USD'::VARCHAR AS currency, true AS has_currency,
          TIMESTAMPTZ '{period_end} 00:00:00Z' AS observed_at,
          'date'::VARCHAR AS observed_precision,
          TIMESTAMPTZ '{available}' AS published_at, 'second'::VARCHAR AS published_precision,
          TIMESTAMPTZ '{available}' AS available_at,
          TIMESTAMPTZ '{available}' AS ingested_at,
          DATE '{period_start}' AS period_start, true AS has_period_start,
          DATE '{period_end}' AS period_end, '{value}'::VARCHAR AS value,
          true AS has_value, 0::INTEGER AS revision, '{accession}'::VARCHAR AS accession_number,
          '10-K'::VARCHAR AS form, CAST(substr('{period_end}', 1, 4) AS INTEGER) AS fiscal_year,
          'Q4'::VARCHAR AS fiscal_period, ''::VARCHAR AS frame,
          repeat('{concept[0].lower()}', 64)::VARCHAR AS raw_payload_hash,
          '{SOURCE_ID}'::VARCHAR AS data_source_id,
          '{RUN_ID}'::VARCHAR AS ingestion_run_id,
          'companyfacts/{concept}/{period_end}'::VARCHAR AS raw_record_locator,
          'go-v1'::VARCHAR AS normalizer_version
    """


def _catalog(tmp_path: Path) -> ResearchCatalog:
    directory = tmp_path / "normalized" / "fundamentals" / "source=sec" / f"issuer_id={ISSUER_ID}"
    queries = [
        _fundamental_row(
            concept="Revenue",
            period_start="2023-01-01",
            period_end="2023-12-31",
            available="2024-01-20 12:00:00",
            value="100",
            accession="2023-revenue",
        ),
        _fundamental_row(
            concept="Revenue",
            period_start="2024-01-01",
            period_end="2024-12-31",
            available="2025-01-20 12:00:00",
            value="120",
            accession="2024-revenue",
        ),
        _fundamental_row(
            concept="OperatingIncomeLoss",
            period_start="2023-01-01",
            period_end="2023-12-31",
            available="2024-01-20 12:00:00",
            value="20",
            accession="2023-operating-income",
        ),
        _fundamental_row(
            concept="OperatingIncomeLoss",
            period_start="2024-01-01",
            period_end="2024-12-31",
            available="2025-01-20 12:00:00",
            value="30",
            accession="2024-operating-income",
        ),
    ]
    part = directory / "fundamentals.parquet"
    _write_parquet(part, "\nUNION ALL\n".join(queries))
    _write_manifest(directory, [part])
    return ResearchCatalog(tmp_path).register()


def test_fundamental_growth_uses_reviewed_mappings_and_exact_decimal_math(tmp_path: Path) -> None:
    catalog = _catalog(tmp_path)
    features = compute_fundamental_growth_features(
        catalog,
        decision_at="2025-02-01T00:00:00Z",
        security_id=SECURITY_ID,
        issuer_id=ISSUER_ID,
        taxonomy_registry=REGISTRY_PATH,
        calendar_pin=CALENDAR_PIN,
    )

    assert features == {
        "revenue": "120",
        "revenue_growth_yoy": "0.2",
        "operating_margin": "0.25",
    }


def test_fundamental_growth_publishes_and_dispatches_strict_reader(tmp_path: Path) -> None:
    catalog = _catalog(tmp_path)
    features_root = tmp_path / "features"
    manifest_path = publish_fundamental_growth(
        catalog,
        decision_at="2025-02-01T00:00:00Z",
        security_id=SECURITY_ID,
        issuer_id=ISSUER_ID,
        taxonomy_registry=REGISTRY_PATH,
        calendar_pin=CALENDAR_PIN,
        features_root=features_root,
        git_commit="0" * 40,
    )

    artifact = read_feature_artifact(manifest_path)
    assert artifact.manifest["feature_set"] == "fundamental-growth"
    assert artifact.manifest["taxonomy_mapping_sha256"] == load_taxonomy_registry(REGISTRY_PATH).registry_sha256
    assert artifact.observations[0]["features"]["revenue_growth_yoy"] == "0.2"
    assert manifest_path == publish_fundamental_growth(
        catalog,
        decision_at="2025-02-01T00:00:00Z",
        security_id=SECURITY_ID,
        issuer_id=ISSUER_ID,
        taxonomy_registry=REGISTRY_PATH,
        calendar_pin=CALENDAR_PIN,
        features_root=features_root,
        git_commit="0" * 40,
    )


def test_fundamental_growth_mapping_change_forks_identity(tmp_path: Path) -> None:
    catalog = _catalog(tmp_path)
    features_root = tmp_path / "features"
    first = publish_fundamental_growth(
        catalog,
        decision_at="2025-02-01T00:00:00Z",
        security_id=SECURITY_ID,
        issuer_id=ISSUER_ID,
        taxonomy_registry=REGISTRY_PATH,
        calendar_pin=CALENDAR_PIN,
        features_root=features_root,
        git_commit="0" * 40,
    )
    changed = json.loads(REGISTRY_PATH.read_text(encoding="utf-8"))
    changed["entries"][1]["review_note"] += " changed"
    changed_path = tmp_path / "changed-taxonomy.json"
    changed_path.write_text(json.dumps(changed), encoding="utf-8")

    second = publish_fundamental_growth(
        catalog,
        decision_at="2025-02-01T00:00:00Z",
        security_id=SECURITY_ID,
        issuer_id=ISSUER_ID,
        taxonomy_registry=changed_path,
        calendar_pin=CALENDAR_PIN,
        features_root=features_root,
        git_commit="0" * 40,
    )

    assert second != first
    assert second.exists()


def test_fundamental_growth_rejects_missing_revenue_input(tmp_path: Path) -> None:
    catalog = ResearchCatalog(tmp_path / "empty").register()

    with pytest.raises(FeatureArtifactError, match="no reviewed revenue"):
        publish_fundamental_growth(
            catalog,
            decision_at="2025-02-01T00:00:00Z",
            security_id=SECURITY_ID,
            issuer_id=ISSUER_ID,
            taxonomy_registry=REGISTRY_PATH,
            calendar_pin=CALENDAR_PIN,
            features_root=tmp_path / "features",
            git_commit="0" * 40,
        )
