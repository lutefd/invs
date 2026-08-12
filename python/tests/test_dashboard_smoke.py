from __future__ import annotations

import json
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
    assert "::numeric" in sql
    assert "no snapshot published" in sql
    assert smoke_sql([DASHBOARD]).startswith("BEGIN;")
    assert smoke_sql([DASHBOARD]).endswith("ROLLBACK;")


def test_duplicate_json_keys_are_rejected(tmp_path: Path) -> None:
    path = tmp_path / "duplicate.json"
    path.write_text('{"panels": [], "panels": []}', encoding="utf-8")

    with pytest.raises(ValueError, match="duplicate JSON key: panels"):
        load_dashboard(path)


def test_all_provisioned_dashboards_parse_as_json() -> None:
    for path in DASHBOARD.parent.glob("*.json"):
        document = load_dashboard(path)
        assert json.dumps(document)
