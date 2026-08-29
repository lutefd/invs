from __future__ import annotations

import hashlib
import json
from pathlib import Path

import duckdb
import pytest

from research.catalog import ResearchCatalog
from research.features import FeatureArtifactError, read_feature_artifact
from research.macro_state import compute_macro_state_features, publish_macro_state

SECURITY_ID = "469fc20f-7d4b-45bb-b827-05f8410e71aa"
SOURCE_ID = "5d6ac836-54fd-4df2-a745-0744180420db"
RUN_ID = "c7286917-ce45-4879-834f-fc975c80c49e"
CALENDAR_PIN = {
    "data_source_id": "6c8d9b2a-4c2f-4e03-bf84-2bd5bb3b817e",
    "mic": "XNYS",
    "calendar_version": "xnas-2026-v1",
    "session_fingerprint": "a" * 64,
    "calendar_available_at": "2023-01-01T00:00:00Z",
    "decision_clock_policy": "after_close_next_session",
}


def _write_parquet(path: Path, query: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    connection = duckdb.connect(":memory:")
    connection.execute(f"COPY ({query}) TO '{path}' (FORMAT PARQUET)")
    connection.close()


def _write_manifest(directory: Path, part_path: Path) -> Path:
    digest = hashlib.sha256(part_path.read_bytes()).hexdigest()
    committed = part_path.with_name(f"part-{digest}.parquet")
    part_path.replace(committed)
    connection = duckdb.connect(":memory:")
    count = connection.execute(f"SELECT count(*) FROM read_parquet('{committed}')").fetchone()[0]
    connection.close()
    manifest_path = directory / "manifest.json"
    manifest_path.write_text(
        json.dumps(
            {
                "manifest_version": 1,
                "schema_version": "1.0.0",
                "normalizer_version": "go-v1",
                "git_commit": "0" * 40,
                "source": "alfred",
                "data_source_id": SOURCE_ID,
                "ingestion_run_id": RUN_ID,
                "partition": {"dataset": "macroeconomics", "source": "alfred", "series_id": "CPIAUCSL"},
                "row_count": count,
                "parts": [{"path": committed.name, "sha256": digest, "row_count": count}],
            }
        )
        + "\n",
        encoding="utf-8",
    )
    return manifest_path


def _macro_row(
    *,
    observed: str,
    available: str,
    value: str,
    revision: int = 0,
    vintage: str | None = None,
    has_value: bool = True,
) -> str:
    vintage_value = vintage or available
    value_sql = f"'{value}'::VARCHAR" if has_value else "NULL::VARCHAR"
    return f"""
        SELECT
          '1.0.0'::VARCHAR AS schema_version, 'alfred'::VARCHAR AS source,
          'CPIAUCSL'::VARCHAR AS series_id, 'US'::VARCHAR AS geography,
          'Index'::VARCHAR AS unit, 'monthly'::VARCHAR AS frequency,
          'SA'::VARCHAR AS seasonal_adjustment, true AS has_seasonal_adjustment,
          TIMESTAMPTZ '{observed} 00:00:00Z' AS observed_at,
          'date'::VARCHAR AS observed_precision,
          TIMESTAMPTZ '{available} 00:00:00Z' AS published_at,
          'second'::VARCHAR AS published_precision,
          TIMESTAMPTZ '{available} 00:00:00Z' AS available_at,
          TIMESTAMPTZ '{available} 00:00:00Z' AS ingested_at,
          {value_sql} AS value, {'true' if has_value else 'false'} AS has_value,
          {revision}::INTEGER AS revision,
          TIMESTAMPTZ '{vintage_value} 00:00:00Z' AS vintage_at, true AS has_vintage_at,
          repeat('{revision + 1}', 64)::VARCHAR AS raw_payload_hash,
          '{SOURCE_ID}'::VARCHAR AS data_source_id,
          '{RUN_ID}'::VARCHAR AS ingestion_run_id,
          'alfred/series=CPIAUCSL/date={observed}/revision={revision}'::VARCHAR AS raw_record_locator,
          'go-v1'::VARCHAR AS normalizer_version
    """


def _catalog(tmp_path: Path, *, revised: bool = False) -> ResearchCatalog:
    directory = tmp_path / "normalized" / "macroeconomics" / "source=alfred" / "series_id=CPIAUCSL"
    queries = [
        _macro_row(observed="2023-01-01", available="2023-01-15", value="100"),
        _macro_row(observed="2024-01-01", available="2024-01-15", value="110"),
    ]
    if revised:
        queries.append(
            _macro_row(
                observed="2024-01-01",
                available="2025-01-15",
                value="115",
                revision=1,
                vintage="2025-01-15",
            )
        )
    part = directory / "macroeconomics.parquet"
    _write_parquet(part, "\nUNION ALL\n".join(queries))
    _write_manifest(directory, part)
    return ResearchCatalog(tmp_path).register()


def _kwargs() -> dict[str, str]:
    return {
        "source": "alfred",
        "series_id": "CPIAUCSL",
        "geography": "US",
        "unit": "Index",
        "frequency": "monthly",
    }


def test_macro_state_uses_historical_vintages_and_exact_decimal_math(tmp_path: Path) -> None:
    catalog = _catalog(tmp_path)
    features = compute_macro_state_features(
        catalog,
        decision_at="2024-02-01T00:00:00Z",
        security_id=SECURITY_ID,
        calendar_pin=CALENDAR_PIN,
        **_kwargs(),
    )

    assert features == {"macro_level": "110", "macro_change_yoy": "0.1"}


def test_macro_state_revision_boundary_changes_only_eligible_selection(tmp_path: Path) -> None:
    before = compute_macro_state_features(
        _catalog(tmp_path / "before", revised=True),
        decision_at="2024-12-31T00:00:00Z",
        security_id=SECURITY_ID,
        calendar_pin=CALENDAR_PIN,
        **_kwargs(),
    )
    after = compute_macro_state_features(
        _catalog(tmp_path / "after", revised=True),
        decision_at="2025-02-01T00:00:00Z",
        security_id=SECURITY_ID,
        calendar_pin=CALENDAR_PIN,
        **_kwargs(),
    )

    assert before == {"macro_level": "110", "macro_change_yoy": "0.1"}
    assert after == {"macro_level": "115", "macro_change_yoy": "0.15"}


def test_macro_state_publishes_and_strictly_dispatches_reader(tmp_path: Path) -> None:
    catalog = _catalog(tmp_path)
    manifest_path = publish_macro_state(
        catalog,
        decision_at="2024-02-01T00:00:00Z",
        security_id=SECURITY_ID,
        calendar_pin=CALENDAR_PIN,
        features_root=tmp_path / "features",
        git_commit="0" * 40,
        **_kwargs(),
    )

    artifact = read_feature_artifact(manifest_path)
    assert artifact.manifest["feature_set"] == "macro-state"
    assert artifact.manifest["series_id"] == "CPIAUCSL"
    assert artifact.observations[0]["features"] == {
        "macro_level": "110",
        "macro_change_yoy": "0.1",
    }


def test_macro_state_rejects_non_vintage_rows(tmp_path: Path) -> None:
    directory = tmp_path / "normalized" / "macroeconomics" / "source=alfred" / "series_id=CPIAUCSL"
    query = _macro_row(observed="2024-01-01", available="2024-01-15", value="110").replace(
        "true AS has_vintage_at", "false AS has_vintage_at"
    )
    part = directory / "macroeconomics.parquet"
    _write_parquet(part, query)
    _write_manifest(directory, part)

    with pytest.raises(FeatureArtifactError, match="historical-vintage"):
        compute_macro_state_features(
            ResearchCatalog(tmp_path).register(),
            decision_at="2024-02-01T00:00:00Z",
            security_id=SECURITY_ID,
            calendar_pin=CALENDAR_PIN,
            **_kwargs(),
        )
