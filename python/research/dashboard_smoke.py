"""Strictly validate Grafana dashboard JSON and emit PostgreSQL query smoke SQL."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any


def _strict_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON key: {key}")
        result[key] = value
    return result


def load_dashboard(path: Path) -> dict[str, Any]:
    document = json.loads(
        path.read_text(encoding="utf-8"), object_pairs_hook=_strict_object
    )
    if not isinstance(document, dict):
        raise TypeError(f"dashboard {path} must be a JSON object")
    return document


def dashboard_queries(document: dict[str, Any]) -> tuple[str, ...]:
    queries: list[str] = []
    for panel in document.get("panels", []):
        for target in panel.get("targets", []):
            query = target.get("rawSql")
            if isinstance(query, str) and query.strip():
                if "$__" in query:
                    raise ValueError("query smoke does not support unresolved Grafana macros")
                queries.append(query.rstrip(";"))
    if not queries:
        raise ValueError("dashboard contains no SQL queries")
    return tuple(queries)


def smoke_sql(paths: list[Path]) -> str:
    statements = ["BEGIN;"]
    for path in paths:
        for index, query in enumerate(dashboard_queries(load_dashboard(path)), start=1):
            statements.append(f"-- {path.name} query {index}\nEXPLAIN {query};")
    statements.append("ROLLBACK;")
    return "\n".join(statements)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("dashboards", nargs="+", type=Path)
    args = parser.parse_args()
    print(smoke_sql(args.dashboards))


if __name__ == "__main__":
    main()
