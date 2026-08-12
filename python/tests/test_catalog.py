from __future__ import annotations

from pathlib import Path

import duckdb
import pytest

from research import DatasetSchemaError, ResearchCatalog, SecurityMapping, load_security_mappings

SECURITY_ID = "469fc20f-7d4b-45bb-b827-05f8410e71aa"
ISSUER_ID = "1b3d88f5-55b8-4dc5-a6be-2f77e9e99201"
SOURCE_ID = "5d6ac836-54fd-4df2-a745-0744180420db"
RUN_ID = "c7286917-ce45-4879-834f-fc975c80c49e"


def _write_parquet(path: Path, query: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    connection = duckdb.connect(":memory:")
    connection.execute(f"COPY ({query}) TO '{path}' (FORMAT PARQUET)")
    connection.close()


def _price_select(
    *,
    observed: str = "2025-01-10 21:00:00Z",
    available: str = "2025-01-11 00:00:00Z",
    close: str = "100.123456789012345678",
    schema_version: str = "1.0.0",
) -> str:
    return f"""
        SELECT
          '{schema_version}'::VARCHAR AS schema_version, 'yahoo'::VARCHAR AS source,
          '{SECURITY_ID}'::VARCHAR AS security_id, '1d'::VARCHAR AS interval,
          'raw'::VARCHAR AS price_basis, 'USD'::VARCHAR AS currency,
          TIMESTAMPTZ '{observed}' AS observed_at,
          TIMESTAMPTZ '{observed}' AS published_at, true AS has_published_at,
          'second'::VARCHAR AS published_precision,
          TIMESTAMPTZ '{available}' AS available_at,
          TIMESTAMPTZ '{available}' AS ingested_at,
          '99.5'::VARCHAR AS open, '101.5'::VARCHAR AS high,
          '98.25'::VARCHAR AS low, '{close}'::VARCHAR AS close,
          '1000.25'::VARCHAR AS volume, true AS has_volume,
          repeat('a', 64)::VARCHAR AS raw_payload_hash,
          '{SOURCE_ID}'::VARCHAR AS data_source_id,
          '{RUN_ID}'::VARCHAR AS ingestion_run_id,
          'chart/result[0]'::VARCHAR AS raw_record_locator,
          'go-v1'::VARCHAR AS normalizer_version
    """


def _write_prices(root: Path) -> None:
    path = root / "prices" / "source=yahoo" / f"security_id={SECURITY_ID}" / "data.parquet"
    _write_parquet(
        path,
        f"""
        {_price_select()}
        UNION ALL
        {_price_select(observed="2025-02-10 21:00:00Z", available="2025-02-11 00:00:00Z", close="110")}
        UNION ALL
        {_price_select(observed="2026-01-10 21:00:00Z", available="2025-02-01 00:00:00Z", close="200")}
        """,
    )


def _write_fundamentals(root: Path, *, sentinel: bool = False) -> None:
    period_start = "DATE '1970-01-01'" if sentinel else "DATE '2024-10-01'"
    has_period_start = "false" if sentinel else "true"
    _write_parquet(
        root / "fundamentals" / "source=sec" / f"issuer_id={ISSUER_ID}" / "data.parquet",
        f"""
        SELECT
          '1.0.0'::VARCHAR AS schema_version, 'sec'::VARCHAR AS source,
          '{ISSUER_ID}'::VARCHAR AS issuer_id, ''::VARCHAR AS security_id,
          false AS has_security_id, 'us-gaap'::VARCHAR AS taxonomy,
          'Revenue'::VARCHAR AS concept, 'USD'::VARCHAR AS unit,
          'USD'::VARCHAR AS currency, true AS has_currency,
          TIMESTAMPTZ '2024-12-31 00:00:00Z' AS observed_at,
          TIMESTAMPTZ '2025-01-20 12:00:00Z' AS published_at,
          'second'::VARCHAR AS published_precision,
          TIMESTAMPTZ '2025-01-20 12:00:00Z' AS available_at,
          TIMESTAMPTZ '2025-03-01 00:00:00Z' AS ingested_at,
          {period_start} AS period_start, {has_period_start} AS has_period_start,
          DATE '2024-12-31' AS period_end, '11.000000000000000001'::VARCHAR AS value,
          true AS has_value, 0::INTEGER AS revision, '0002'::VARCHAR AS accession_number,
          '10-Q'::VARCHAR AS form, 2024::INTEGER AS fiscal_year,
          'Q4'::VARCHAR AS fiscal_period, ''::VARCHAR AS frame,
          repeat('b', 64)::VARCHAR AS raw_payload_hash,
          '{SOURCE_ID}'::VARCHAR AS data_source_id,
          '{RUN_ID}'::VARCHAR AS ingestion_run_id,
          'companyfacts/facts/0'::VARCHAR AS raw_record_locator,
          'go-v1'::VARCHAR AS normalizer_version
        """,
    )


def _write_macro(root: Path, *, sentinel: bool = False) -> None:
    vintage_at = "TIMESTAMPTZ '1970-01-01 00:00:00Z'" if sentinel else (
        "TIMESTAMPTZ '2025-01-15 13:00:00Z'"
    )
    has_vintage = "false" if sentinel else "true"
    _write_parquet(
        root / "macroeconomics" / "source=fred" / "series_id=GDP" / "data.parquet",
        f"""
        SELECT
          '1.0.0'::VARCHAR AS schema_version, 'fred'::VARCHAR AS source,
          'GDP'::VARCHAR AS series_id, 'US'::VARCHAR AS geography,
          'Index'::VARCHAR AS unit, 'quarterly'::VARCHAR AS frequency,
          ''::VARCHAR AS seasonal_adjustment, false AS has_seasonal_adjustment,
          TIMESTAMPTZ '2025-01-01 00:00:00Z' AS observed_at,
          TIMESTAMPTZ '2025-01-15 13:00:00Z' AS published_at,
          'second'::VARCHAR AS published_precision,
          TIMESTAMPTZ '2025-01-15 13:00:00Z' AS available_at,
          TIMESTAMPTZ '2025-03-01 00:00:00Z' AS ingested_at,
          '2.100000000000000001'::VARCHAR AS value, true AS has_value,
          0::INTEGER AS revision, {vintage_at} AS vintage_at,
          {has_vintage} AS has_vintage_at, repeat('c', 64)::VARCHAR AS raw_payload_hash,
          '{SOURCE_ID}'::VARCHAR AS data_source_id,
          '{RUN_ID}'::VARCHAR AS ingestion_run_id,
          'csv/date=2025-01-01'::VARCHAR AS raw_record_locator,
          'go-v1'::VARCHAR AS normalizer_version
        """,
    )


def test_fresh_data_root_registers_typed_empty_views(tmp_path: Path) -> None:
    catalog = ResearchCatalog(tmp_path).register()
    mapping = SecurityMapping("missing", "missing")

    assert catalog.missing() == ("prices", "fundamentals", "macroeconomics")
    assert catalog.connection.execute("select count(*) from prices_canonical").fetchone() == (0,)
    assert catalog.research_snapshot(
        decision_at="2025-03-01T00:00:00Z",
        mapping=mapping,
        fundamental_concept="Revenue",
        macro_series_id="GDP",
    ).empty


def test_canonical_v1_preserves_strings_provenance_and_adds_numeric_views(
    tmp_path: Path,
) -> None:
    root = tmp_path / "normalized"
    _write_prices(root)
    _write_fundamentals(root)
    _write_macro(root)

    catalog = ResearchCatalog(tmp_path).register()
    canonical = catalog.connection.execute(
        "SELECT close, data_source_id, ingestion_run_id, raw_record_locator, "
        "normalizer_version FROM prices_canonical ORDER BY observed_at LIMIT 1"
    ).fetchone()
    numeric = catalog.connection.execute(
        "SELECT close_value, close_decimal, close, volume_value, volume_decimal "
        "FROM prices ORDER BY observed_at LIMIT 1"
    ).fetchone()

    assert canonical == (
        "100.123456789012345678",
        SOURCE_ID,
        RUN_ID,
        "chart/result[0]",
        "go-v1",
    )
    assert numeric[0] == "100.123456789012345678"
    assert str(numeric[1]) == "100.123456789012345678"
    assert numeric[2] == pytest.approx(100.12345678901235)
    assert numeric[3] == "1000.25"
    assert str(numeric[4]) == "1000.250000000000000000"
    assert [item.file_count for item in catalog.status()] == [1, 1, 1]


def test_deterministic_point_in_time_snapshot_excludes_future_observations(
    tmp_path: Path,
) -> None:
    root = tmp_path / "normalized"
    _write_prices(root)
    _write_fundamentals(root)
    _write_macro(root)
    catalog = ResearchCatalog(tmp_path).register()
    mapping = SecurityMapping(SECURITY_ID, ISSUER_ID, "TEST")

    frame = catalog.research_snapshot(
        decision_at="2025-02-11T00:00:00Z",
        mapping=mapping,
        fundamental_concept="Revenue",
        macro_series_id="GDP",
    )

    assert catalog.available_mappings([mapping]) == (mapping,)
    assert frame["trading_date"].astype(str).tolist() == ["2025-01-10", "2025-02-10"]
    assert frame["close_value"].tolist() == ["100.123456789012345678", "110"]
    assert frame["fundamental_value_text"].tolist() == ["11.000000000000000001"] * 2
    assert frame["macro_value_text"].tolist() == ["2.100000000000000001"] * 2


def test_price_revisions_are_selected_as_known_at_decision_time(tmp_path: Path) -> None:
    root = tmp_path / "normalized"
    _write_prices(root)
    path = root / "prices" / "source=yahoo" / f"security_id={SECURITY_ID}" / "revision.parquet"
    _write_parquet(
        path,
        _price_select(
            observed="2025-01-10 21:00:00Z",
            available="2025-03-05 00:00:00Z",
            close="101",
        ),
    )
    catalog = ResearchCatalog(tmp_path).register()
    mapping = SecurityMapping(SECURITY_ID, ISSUER_ID)

    before = catalog.research_snapshot(
        decision_at="2025-03-04T00:00:00Z",
        mapping=mapping,
        fundamental_concept="Revenue",
        macro_series_id="GDP",
    )
    after = catalog.research_snapshot(
        decision_at="2025-03-06T00:00:00Z",
        mapping=mapping,
        fundamental_concept="Revenue",
        macro_series_id="GDP",
    )

    assert before.loc[before["trading_date"].astype(str) == "2025-01-10", "close"].item() == pytest.approx(100.12345678901235)
    assert after.loc[after["trading_date"].astype(str) == "2025-01-10", "close"].item() == 101
    assert before["trading_date"].is_unique and after["trading_date"].is_unique


def test_legacy_or_unsupported_schema_fails_with_migration_message(tmp_path: Path) -> None:
    legacy = tmp_path / "legacy" / "normalized" / "prices" / "legacy.parquet"
    _write_parquet(legacy, "SELECT 'yahoo'::VARCHAR AS source, 1.0::DOUBLE AS close")
    with pytest.raises(DatasetSchemaError, match="legacy.*migration required"):
        ResearchCatalog(tmp_path / "legacy").register()

    unsupported_root = tmp_path / "unsupported" / "normalized"
    path = unsupported_root / "prices" / "data.parquet"
    _write_parquet(path, _price_select(schema_version="2.0.0"))
    with pytest.raises(DatasetSchemaError, match="schema_version 1.0.0.*migration required"):
        ResearchCatalog(tmp_path / "unsupported").register()


def test_non_utf8_or_invalid_decimal_fails_closed(tmp_path: Path) -> None:
    numeric_root = tmp_path / "numeric" / "normalized"
    query = _price_select().replace("'100.123456789012345678'::VARCHAR AS close", "100.0::DOUBLE AS close")
    _write_parquet(numeric_root / "prices" / "data.parquet", query)
    with pytest.raises(DatasetSchemaError, match="non-UTF8: close"):
        ResearchCatalog(tmp_path / "numeric").register()

    invalid_root = tmp_path / "invalid" / "normalized"
    _write_parquet(
        invalid_root / "prices" / "data.parquet", _price_select(close="1e2")
    )
    with pytest.raises(DatasetSchemaError, match="invalid canonical decimal.*close"):
        ResearchCatalog(tmp_path / "invalid").register()


def test_physical_sentinels_are_exposed_as_sql_null(tmp_path: Path) -> None:
    root = tmp_path / "normalized"
    _write_fundamentals(root, sentinel=True)
    _write_macro(root, sentinel=True)
    catalog = ResearchCatalog(tmp_path).register()

    assert catalog.connection.execute(
        "SELECT security_id, period_start FROM fundamentals_canonical"
    ).fetchone() == (None, None)
    assert catalog.connection.execute(
        "SELECT seasonal_adjustment, vintage_at FROM macroeconomics_canonical"
    ).fetchone() == (None, None)


def test_configured_mapping_is_loaded_and_not_cross_joined(tmp_path: Path) -> None:
    config = tmp_path / "config.yaml"
    config.write_text(
        f"""
universe:
  - security_id: {SECURITY_ID}
    issuer_id: {ISSUER_ID}
    ticker: AAPL
    legal_name: Apple Inc.
""",
        encoding="utf-8",
    )

    assert load_security_mappings(config) == (
        SecurityMapping(SECURITY_ID, ISSUER_ID, "AAPL", "Apple Inc."),
    )
