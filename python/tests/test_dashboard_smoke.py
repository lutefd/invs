from __future__ import annotations

import os
from pathlib import Path

import pytest

from research.dashboard_smoke import (
    dashboard_queries,
    load_dashboard,
    smoke_sql,
)

DASHBOARD = Path(
    os.getenv(
        "INVS_DASHBOARD_PATH",
        Path(__file__).parents[2]
        / "docker"
        / "grafana"
        / "dashboards"
        / "market-overview.json",
    )
)


def test_market_dashboard_is_strict_json_with_latest_snapshot_queries() -> None:
    document = load_dashboard(DASHBOARD)
    queries = dashboard_queries(document)
    sql = "\n".join(queries)

    assert len(queries) == 4
    assert "market_price_snapshots" in sql
    assert "macro_observation_snapshots" in sql
    assert "LEFT JOIN market_price_snapshots" in sql
    assert "LEFT JOIN macro_observation_snapshots" in sql
    assert "source_kind = 'macro'" in sql
    assert "ds.code AS source" in sql
    assert "::numeric" in sql
    assert "no snapshot published" in sql
    assert "source disabled" in sql
    assert "ingestion only" in sql
    assert smoke_sql([DASHBOARD]).startswith("BEGIN;")
    assert smoke_sql([DASHBOARD]).endswith("ROLLBACK;")


def test_pipeline_dashboard_includes_macro_snapshot_state() -> None:
    pipeline = DASHBOARD.parent / "pipeline-health.json"
    document = load_dashboard(pipeline)
    queries = dashboard_queries(document)
    sql = "\n".join(queries)

    assert len(queries) == 5
    assert "macro_observation_snapshots" in sql
    assert "source_kind IN ('market_data', 'macro')" in sql
    assert "snapshot_state" in sql
    assert "ingestion only" in sql
    assert "no snapshot published" in sql
    assert "source disabled" in sql


@pytest.mark.parametrize(
    ("payload", "key"),
    [
        ('{"panels": [], "panels": []}', "panels"),
        ('{"panels": [{"targets": [], "targets": []}]}', "targets"),
    ],
)
def test_duplicate_json_keys_are_rejected(
    tmp_path: Path, payload: str, key: str
) -> None:
    path = tmp_path / "duplicate.json"
    path.write_text(payload, encoding="utf-8")

    with pytest.raises(ValueError, match=f"duplicate JSON key: {key}"):
        load_dashboard(path)


def test_all_provisioned_dashboards_are_strict_json_and_explainable() -> None:
    dashboards = sorted(DASHBOARD.parent.glob("*.json"))

    assert dashboards
    assert DASHBOARD.parent / "pipeline-health.json" in dashboards

    sql = smoke_sql(dashboards)
    assert sql.startswith("BEGIN;")
    assert sql.endswith("ROLLBACK;")
    assert "$__" not in sql

    query_count = 0
    for path in dashboards:
        queries = dashboard_queries(load_dashboard(path))
        assert queries
        query_count += len(queries)
        assert sql.count(f"-- {path.name} query") == len(queries)

    assert sql.count("\nEXPLAIN ") == query_count
