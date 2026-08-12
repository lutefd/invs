from __future__ import annotations

from pathlib import Path

import duckdb
import pytest

from research import DatasetSchemaError, ResearchCatalog


def _write_parquet(path: Path, query: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    connection = duckdb.connect(":memory:")
    connection.execute(f"COPY ({query}) TO '{path}' (FORMAT PARQUET)")
    connection.close()


def test_fresh_data_root_registers_typed_empty_views(tmp_path: Path) -> None:
    catalog = ResearchCatalog(tmp_path).register()

    assert catalog.missing() == ("prices", "fundamentals", "macroeconomics")
    assert catalog.connection.execute("select count(*) from prices").fetchone() == (0,)
    assert catalog.point_in_time_frame(
        security_id="missing",
        issuer_id="missing",
        fundamental_concept="Revenue",
        macro_series_id="GDP",
    ).empty


def test_recursive_files_and_point_in_time_join(tmp_path: Path) -> None:
    root = tmp_path / "normalized"
    _write_parquet(
        root / "prices" / "year=2025" / "part.parquet",
        """
        SELECT * FROM (VALUES
          ('sec-1', DATE '2025-01-10', 100.0, 'USD', TIMESTAMPTZ '2025-01-11 00:00:00Z'),
          ('sec-1', DATE '2025-02-10', 110.0, 'USD', TIMESTAMPTZ '2025-02-11 00:00:00Z')
        ) t(security_id, trading_date, close, currency, available_at)
        """,
    )
    _write_parquet(
        root / "fundamentals" / "part.parquet",
        """
        SELECT * FROM (VALUES
          ('issuer-1', 'Revenue', 10.0, DATE '2024-09-30', TIMESTAMPTZ '2025-01-20 12:00:00Z', TIMESTAMPTZ '2025-01-20 12:00:00Z'),
          ('issuer-1', 'Revenue', 12.0, DATE '2024-12-31', TIMESTAMPTZ '2025-02-20 12:00:00Z', TIMESTAMPTZ '2025-02-20 12:00:00Z')
        ) t(issuer_id, concept, value, period_end, published_at, available_at)
        """,
    )
    _write_parquet(
        root / "macroeconomics" / "part.parquet",
        """
        SELECT * FROM (VALUES
          ('GDP', DATE '2024-10-01', 2.0, TIMESTAMPTZ '2025-01-15 13:00:00Z', TIMESTAMPTZ '2025-01-15 13:00:00Z')
        ) t(series_id, observation_date, value, published_at, available_at)
        """,
    )

    catalog = ResearchCatalog(tmp_path).register()
    frame = catalog.point_in_time_frame(
        security_id="sec-1",
        issuer_id="issuer-1",
        fundamental_concept="Revenue",
        macro_series_id="GDP",
    )

    assert [item.file_count for item in catalog.status()] == [1, 1, 1]
    assert frame["fundamental_value"].isna().iloc[0]
    assert frame["fundamental_value"].iloc[1] == 10.0
    assert frame["macro_value"].isna().iloc[0]
    assert frame["macro_value"].iloc[1] == 2.0
    has_fundamental = frame["fundamental_available_at"].notna()
    assert (
        frame.loc[has_fundamental, "fundamental_available_at"]
        <= frame.loc[has_fundamental, "known_at"]
    ).all()


def test_populated_incompatible_dataset_fails_loudly(tmp_path: Path) -> None:
    _write_parquet(
        tmp_path / "normalized" / "prices" / "bad.parquet",
        "SELECT 1 AS unexpected",
    )

    with pytest.raises(DatasetSchemaError, match="security_id, trading_date, close"):
        ResearchCatalog(tmp_path).register()


def test_physical_sentinel_dates_are_exposed_as_null(tmp_path: Path) -> None:
    root = tmp_path / "normalized"
    _write_parquet(
        root / "fundamentals" / "part.parquet",
        """
        SELECT 'issuer-1' AS issuer_id, 'Revenue' AS concept, 1.0 AS value,
               DATE '2025-01-01' AS period_end,
               TIMESTAMPTZ '2025-02-01 00:00:00Z' AS published_at,
               TIMESTAMPTZ '2025-02-01 00:00:00Z' AS available_at,
               DATE '1970-01-01' AS period_start, false AS has_period_start
        """,
    )
    _write_parquet(
        root / "macroeconomics" / "part.parquet",
        """
        SELECT 'GDP' AS series_id, DATE '2025-01-01' AS observation_date,
               1.0 AS value, TIMESTAMPTZ '2025-02-01 00:00:00Z' AS published_at,
               TIMESTAMPTZ '2025-02-01 00:00:00Z' AS available_at,
               TIMESTAMPTZ '1970-01-01 00:00:00Z' AS vintage_at, false AS has_vintage_at
        """,
    )

    catalog = ResearchCatalog(tmp_path).register()

    assert catalog.connection.execute("select period_start from fundamentals").fetchone() == (None,)
    assert catalog.connection.execute("select vintage_at from macroeconomics").fetchone() == (None,)
