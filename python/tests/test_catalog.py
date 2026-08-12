from __future__ import annotations

from pathlib import Path

import duckdb
import pytest

from research import (
    DatasetSchemaError,
    ResearchCatalog,
    SecurityMapping,
    load_security_mappings,
)

SECURITY_ID = "469fc20f-7d4b-45bb-b827-05f8410e71aa"
ISSUER_ID = "1b3d88f5-55b8-4dc5-a6be-2f77e9e99201"


def _write_parquet(path: Path, query: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    connection = duckdb.connect(":memory:")
    connection.execute(f"COPY ({query}) TO '{path}' (FORMAT PARQUET)")
    connection.close()


def _write_prices(root: Path) -> None:
    _write_parquet(
        root / "prices" / "source=yahoo" / f"security_id={SECURITY_ID}" / "data.parquet",
        f"""
        SELECT * FROM (VALUES
          ('yahoo', '{SECURITY_ID}', 'USD', TIMESTAMPTZ '2025-01-10 21:00:00Z',
           TIMESTAMPTZ '2025-01-10 21:00:00Z', 'second',
           TIMESTAMPTZ '2025-01-11 00:00:00Z', TIMESTAMPTZ '2025-03-01 00:00:00Z',
           99.0, 101.0, 98.0, 100.0, 1000::BIGINT, repeat('a', 64)),
          ('yahoo', '{SECURITY_ID}', 'USD', TIMESTAMPTZ '2025-02-10 21:00:00Z',
           TIMESTAMPTZ '2025-02-10 21:00:00Z', 'second',
           TIMESTAMPTZ '2025-02-11 00:00:00Z', TIMESTAMPTZ '2025-03-01 00:00:00Z',
           109.0, 111.0, 108.0, 110.0, 1100::BIGINT, repeat('b', 64)),
          ('yahoo', '{SECURITY_ID}', 'USD', TIMESTAMPTZ '2026-01-10 21:00:00Z',
           TIMESTAMPTZ '2025-02-01 00:00:00Z', 'second',
           TIMESTAMPTZ '2025-02-01 00:00:00Z', TIMESTAMPTZ '2025-02-01 00:00:00Z',
           199.0, 201.0, 198.0, 200.0, 1200::BIGINT, repeat('8', 64))
        ) t(source, security_id, currency, observed_at, published_at,
            published_precision, available_at, ingested_at, open, high, low,
            close, volume, raw_payload_hash)
        """,
    )


def _write_fundamentals(root: Path, *, sentinel: bool = False) -> None:
    if sentinel:
        values = f"""
          ('sec', '{ISSUER_ID}', 'us-gaap', 'Revenue', 'USD',
           TIMESTAMPTZ '2024-12-31 00:00:00Z', TIMESTAMPTZ '2025-02-01 00:00:00Z',
           'date', TIMESTAMPTZ '2025-02-02 00:00:00Z',
           TIMESTAMPTZ '2025-03-01 00:00:00Z', DATE '1970-01-01', false,
           DATE '2024-12-31', 12.0, '0002', '10-K', 2024, 'FY', '', repeat('c', 64))
        """
    else:
        values = f"""
          ('sec', '{ISSUER_ID}', 'us-gaap', 'Revenue', 'USD',
           TIMESTAMPTZ '2024-09-30 00:00:00Z', TIMESTAMPTZ '2025-01-20 12:00:00Z',
           'second', TIMESTAMPTZ '2025-01-20 12:00:00Z',
           TIMESTAMPTZ '2025-03-01 00:00:00Z', DATE '2024-07-01', true,
           DATE '2024-09-30', 10.0, '0001', '10-Q', 2024, 'Q3', '', repeat('c', 64)),
          ('sec', '{ISSUER_ID}', 'us-gaap', 'Revenue', 'USD',
           TIMESTAMPTZ '2024-12-31 00:00:00Z', TIMESTAMPTZ '2025-01-20 12:00:00Z',
           'second', TIMESTAMPTZ '2025-01-20 12:00:00Z',
           TIMESTAMPTZ '2025-03-01 00:00:00Z', DATE '2024-10-01', true,
           DATE '2024-12-31', 11.0, '0002', '10-Q', 2024, 'Q4', '', repeat('d', 64)),
          ('sec', '{ISSUER_ID}', 'us-gaap', 'Revenue', 'USD',
           TIMESTAMPTZ '2025-03-31 00:00:00Z', TIMESTAMPTZ '2025-02-10 12:00:00Z',
           'second', TIMESTAMPTZ '2025-02-10 12:00:00Z',
           TIMESTAMPTZ '2025-03-01 00:00:00Z', DATE '2025-01-01', true,
           DATE '2025-03-31', 12.0, '0003', '10-Q', 2025, 'Q1', '', repeat('e', 64))
        """
    _write_parquet(
        root / "fundamentals" / "source=sec" / f"issuer_id={ISSUER_ID}" / "data.parquet",
        f"""
        SELECT * FROM (VALUES {values})
        t(source, issuer_id, taxonomy, concept, unit, observed_at, published_at,
          published_precision, available_at, ingested_at, period_start,
          has_period_start, period_end, value, accession_number, form, fiscal_year,
          fiscal_period, frame, raw_payload_hash)
        """,
    )


def _write_macro(root: Path, *, sentinel: bool = False) -> None:
    vintage = "TIMESTAMPTZ '1970-01-01 00:00:00Z', false" if sentinel else (
        "TIMESTAMPTZ '2025-01-15 13:00:00Z', true"
    )
    _write_parquet(
        root / "macroeconomics" / "source=fred" / "series_id=GDP" / "data.parquet",
        f"""
        SELECT * FROM (VALUES
          ('fred', 'GDP', 'Index', TIMESTAMPTZ '2024-10-01 00:00:00Z',
           TIMESTAMPTZ '2025-01-15 13:00:00Z', 'second',
           TIMESTAMPTZ '2025-01-15 13:00:00Z', TIMESTAMPTZ '2025-03-01 00:00:00Z',
           2.0, {vintage}, repeat('f', 64)),
          ('fred', 'GDP', 'Index', TIMESTAMPTZ '2025-01-01 00:00:00Z',
           TIMESTAMPTZ '2025-01-15 13:00:00Z', 'second',
           TIMESTAMPTZ '2025-01-15 13:00:00Z', TIMESTAMPTZ '2025-03-01 00:00:00Z',
           2.1, {vintage}, repeat('0', 64)),
          ('fred', 'GDP', 'Index', TIMESTAMPTZ '2026-01-01 00:00:00Z',
           TIMESTAMPTZ '2025-01-15 13:00:00Z', 'second',
           TIMESTAMPTZ '2025-01-15 13:00:00Z', TIMESTAMPTZ '2025-03-01 00:00:00Z',
           99.0, {vintage}, repeat('1', 64))
        ) t(source, series_id, unit, observed_at, published_at,
            published_precision, available_at, ingested_at, value, vintage_at,
            has_vintage_at, raw_payload_hash)
        """,
    )


def test_fresh_data_root_registers_typed_empty_views(tmp_path: Path) -> None:
    catalog = ResearchCatalog(tmp_path).register()
    mapping = SecurityMapping("missing", "missing")

    assert catalog.missing() == ("prices", "fundamentals", "macroeconomics")
    assert catalog.connection.execute("select count(*) from prices").fetchone() == (0,)
    assert catalog.research_snapshot(
        decision_at="2025-03-01T00:00:00Z",
        mapping=mapping,
        fundamental_concept="Revenue",
        macro_series_id="GDP",
    ).empty


def test_exact_physical_schema_and_deterministic_point_in_time_join(tmp_path: Path) -> None:
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

    assert [item.file_count for item in catalog.status()] == [1, 1, 1]
    assert catalog.available_mappings([mapping]) == (mapping,)
    assert list(frame["fundamental_value"]) == [11.0, 11.0]
    assert list(frame["macro_value"]) == [2.1, 2.1]
    assert frame["trading_date"].astype(str).tolist() == ["2025-01-10", "2025-02-10"]
    assert 12.0 not in frame["fundamental_value"].tolist()
    assert 99.0 not in frame["macro_value"].tolist()
    has_fundamental = frame["fundamental_available_at"].notna()
    assert (
        frame.loc[has_fundamental, "fundamental_available_at"]
        <= frame.loc[has_fundamental, "known_at"]
    ).all()


def test_price_revisions_are_selected_as_known_at_decision_time(tmp_path: Path) -> None:
    root = tmp_path / "normalized"
    _write_prices(root)
    price_path = root / "prices" / "source=yahoo" / f"security_id={SECURITY_ID}" / "revision.parquet"
    _write_parquet(
        price_path,
        f"""
        SELECT 'yahoo' AS source, '{SECURITY_ID}' AS security_id, 'USD' AS currency,
               TIMESTAMPTZ '2025-01-10 21:00:00Z' AS observed_at,
               TIMESTAMPTZ '2025-03-05 00:00:00Z' AS published_at,
               'second' AS published_precision,
               TIMESTAMPTZ '2025-03-05 00:00:00Z' AS available_at,
               TIMESTAMPTZ '2025-03-05 00:00:00Z' AS ingested_at,
               99.0 AS open, 102.0 AS high, 98.0 AS low, 101.0 AS close,
               1000::BIGINT AS volume, repeat('9', 64) AS raw_payload_hash
        """,
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

    assert before.loc[before["trading_date"].astype(str) == "2025-01-10", "close"].item() == 100
    assert after.loc[after["trading_date"].astype(str) == "2025-01-10", "close"].item() == 101
    assert before["trading_date"].is_unique and after["trading_date"].is_unique


def test_populated_incompatible_dataset_fails_loudly(tmp_path: Path) -> None:
    _write_parquet(
        tmp_path / "normalized" / "prices" / "bad.parquet",
        "SELECT 1 AS unexpected",
    )

    with pytest.raises(DatasetSchemaError, match="source, security_id"):
        ResearchCatalog(tmp_path).register()


def test_physical_sentinel_dates_are_exposed_as_sql_null(tmp_path: Path) -> None:
    root = tmp_path / "normalized"
    _write_fundamentals(root, sentinel=True)
    _write_macro(root, sentinel=True)

    catalog = ResearchCatalog(tmp_path).register()

    assert catalog.connection.execute(
        "select period_start, has_period_start from fundamentals"
    ).fetchone() == (None, False)
    assert catalog.connection.execute(
        "select distinct vintage_at, has_vintage_at from macroeconomics"
    ).fetchone() == (None, False)


def test_configured_mapping_is_loaded_and_not_cross_joined(tmp_path: Path) -> None:
    config = tmp_path / "config.yaml"
    config.write_text(
        """
universe:
  - security_id: 469fc20f-7d4b-45bb-b827-05f8410e71aa
    issuer_id: 1b3d88f5-55b8-4dc5-a6be-2f77e9e99201
    ticker: AAPL
    legal_name: Apple Inc.
  - security_id: 75f8ad84-b0bf-4f32-8fcb-40e82cc05d99
    issuer_id: 6f39d702-338b-4f4e-a2d4-d48b86bda194
""",
        encoding="utf-8",
    )

    assert load_security_mappings(config) == (
        SecurityMapping(SECURITY_ID, ISSUER_ID, "AAPL", "Apple Inc."),
        SecurityMapping(
            "75f8ad84-b0bf-4f32-8fcb-40e82cc05d99",
            "6f39d702-338b-4f4e-a2d4-d48b86bda194",
        ),
    )
