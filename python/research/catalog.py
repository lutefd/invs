"""Register normalized Parquet as stable, point-in-time-aware DuckDB views.

Collectors own the physical Parquet layout. This module owns the small compatibility
boundary used by notebooks: recursive discovery, a few additive aliases, and typed
empty views on a fresh checkout. A populated but incompatible dataset fails loudly.
"""

from __future__ import annotations

from collections.abc import Iterable
from dataclasses import dataclass
from pathlib import Path
from typing import Final

import duckdb


class DatasetSchemaError(ValueError):
    """Raised when present Parquet cannot satisfy the canonical research contract."""


@dataclass(frozen=True)
class DatasetStatus:
    name: str
    pattern: str
    file_count: int
    row_count: int
    available: bool
    message: str


@dataclass(frozen=True)
class _Field:
    name: str
    sql_type: str
    aliases: tuple[str, ...]
    required: bool = False


_DATASETS: Final[dict[str, tuple[str, tuple[_Field, ...]]]] = {
    "prices": (
        "prices/**/*.parquet",
        (
            _Field("security_id", "VARCHAR", ("security_id", "asset_id"), True),
            _Field("trading_date", "DATE", ("trading_date", "date", "observed_at"), True),
            _Field("observed_at", "TIMESTAMPTZ", ("observed_at",)),
            _Field("close", "DOUBLE", ("close", "close_price"), True),
            _Field("currency", "VARCHAR", ("currency",)),
            _Field("source", "VARCHAR", ("source", "source_code", "provider")),
            _Field("published_at", "TIMESTAMPTZ", ("published_at", "available_at")),
            _Field("available_at", "TIMESTAMPTZ", ("available_at", "published_at"), True),
            _Field("ingested_at", "TIMESTAMPTZ", ("ingested_at",)),
        ),
    ),
    "fundamentals": (
        "fundamentals/**/*.parquet",
        (
            _Field("issuer_id", "VARCHAR", ("issuer_id",), True),
            _Field("concept", "VARCHAR", ("concept", "metric", "fact_name"), True),
            _Field("value", "DOUBLE", ("value", "numeric_value"), True),
            _Field("period_end", "DATE", ("period_end", "observed_at"), True),
            _Field("published_at", "TIMESTAMPTZ", ("published_at", "filed_at"), True),
            _Field("available_at", "TIMESTAMPTZ", ("available_at", "published_at"), True),
            _Field("unit", "VARCHAR", ("unit", "currency")),
            _Field("source", "VARCHAR", ("source", "source_code", "provider")),
            _Field("revision", "VARCHAR", ("revision", "accession_number")),
            _Field("ingested_at", "TIMESTAMPTZ", ("ingested_at",)),
            _Field("period_start", "DATE", ("period_start",)),
            _Field("has_period_start", "BOOLEAN", ("has_period_start",)),
        ),
    ),
    "macroeconomics": (
        "macroeconomics/**/*.parquet",
        (
            _Field("series_id", "VARCHAR", ("series_id", "indicator_id"), True),
            _Field(
                "observation_date",
                "DATE",
                ("observation_date", "observed_at", "period_end"),
                True,
            ),
            _Field("value", "DOUBLE", ("value", "numeric_value"), True),
            _Field("published_at", "TIMESTAMPTZ", ("published_at", "vintage_date"), True),
            _Field("available_at", "TIMESTAMPTZ", ("available_at", "published_at"), True),
            _Field("source", "VARCHAR", ("source", "source_code", "provider")),
            _Field("revision", "VARCHAR", ("revision", "vintage_date")),
            _Field("ingested_at", "TIMESTAMPTZ", ("ingested_at",)),
            _Field("vintage_at", "TIMESTAMPTZ", ("vintage_at",)),
            _Field("has_vintage_at", "BOOLEAN", ("has_vintage_at",)),
        ),
    ),
}


def _quote_literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def _quote_identifier(value: str) -> str:
    return '"' + value.replace('"', '""') + '"'


class ResearchCatalog:
    """A DuckDB session over normalized prices, fundamentals, and macro data."""

    def __init__(
        self,
        data_root: str | Path = "/data",
        *,
        connection: duckdb.DuckDBPyConnection | None = None,
    ) -> None:
        self.data_root = Path(data_root).expanduser().resolve()
        self.normalized_root = self.data_root / "normalized"
        self.connection = connection or duckdb.connect(":memory:")
        self._statuses: dict[str, DatasetStatus] = {}

    def register(self) -> ResearchCatalog:
        """Register all canonical views and return this catalog for fluent use."""
        for name, (pattern, fields) in _DATASETS.items():
            self._register_dataset(name, pattern, fields)
        return self

    def status(self) -> tuple[DatasetStatus, ...]:
        """Return registration results in deterministic dataset order."""
        return tuple(self._statuses[name] for name in _DATASETS if name in self._statuses)

    def missing(self) -> tuple[str, ...]:
        return tuple(item.name for item in self.status() if not item.available)

    def point_in_time_frame(
        self,
        *,
        security_id: str,
        issuer_id: str,
        fundamental_concept: str,
        macro_series_id: str,
        start: str | None = None,
        end: str | None = None,
    ):
        """Join prices to the latest information public at each price observation.

        The query uses as-of joins on ``available_at``. Price securities and SEC
        issuers are distinct identifiers; callers must supply their security-master
        mapping explicitly. Returned values can never have an availability timestamp
        after ``known_at``.
        """
        clauses = ["security_id = $security_id"]
        parameters: dict[str, object] = {
            "security_id": security_id,
            "issuer_id": issuer_id,
            "fundamental_concept": fundamental_concept,
            "macro_series_id": macro_series_id,
        }
        if start is not None:
            clauses.append("trading_date >= CAST($start AS DATE)")
            parameters["start"] = start
        if end is not None:
            clauses.append("trading_date <= CAST($end AS DATE)")
            parameters["end"] = end

        sql = f"""
            WITH selected_prices AS (
                SELECT *, COALESCE(
                    available_at,
                    CAST(trading_date AS TIMESTAMP) + INTERVAL 1 DAY
                ) AS known_at
                FROM prices
                WHERE {' AND '.join(clauses)}
            ),
            selected_fundamentals AS (
                SELECT * FROM fundamentals
                WHERE issuer_id = $issuer_id
                  AND concept = $fundamental_concept
            ),
            selected_macro AS (
                SELECT * FROM macroeconomics
                WHERE series_id = $macro_series_id
            )
            SELECT
                p.security_id,
                p.trading_date,
                p.close,
                p.currency AS price_currency,
                p.known_at,
                f.issuer_id,
                f.concept AS fundamental_concept,
                f.value AS fundamental_value,
                f.period_end AS fundamental_period_end,
                f.published_at AS fundamental_published_at,
                f.available_at AS fundamental_available_at,
                m.series_id AS macro_series_id,
                m.value AS macro_value,
                m.observation_date AS macro_observation_date,
                m.published_at AS macro_published_at,
                m.available_at AS macro_available_at
            FROM selected_prices p
            ASOF LEFT JOIN selected_fundamentals f
              ON p.known_at >= f.available_at
            ASOF LEFT JOIN selected_macro m
              ON p.known_at >= m.available_at
            ORDER BY p.trading_date
        """
        return self.connection.execute(sql, parameters).fetchdf()

    def _register_dataset(
        self,
        name: str,
        relative_pattern: str,
        fields: Iterable[_Field],
    ) -> None:
        absolute_pattern = self.normalized_root / relative_pattern
        files = sorted(self.normalized_root.glob(relative_pattern))
        if not files:
            projections = ", ".join(
                f"CAST(NULL AS {field.sql_type}) AS {_quote_identifier(field.name)}"
                for field in fields
            )
            self.connection.execute(
                f"CREATE OR REPLACE VIEW {_quote_identifier(name)} AS "
                f"SELECT {projections} WHERE FALSE"
            )
            self._statuses[name] = DatasetStatus(
                name=name,
                pattern=str(absolute_pattern),
                file_count=0,
                row_count=0,
                available=False,
                message="No Parquet files found; registered a typed empty view.",
            )
            return

        raw_view = f"_{name}_raw"
        self.connection.execute(
            f"CREATE OR REPLACE VIEW {_quote_identifier(raw_view)} AS "
            f"SELECT * FROM read_parquet({_quote_literal(str(absolute_pattern))}, "
            "union_by_name=true, hive_partitioning=true)"
        )
        columns = {
            row[1].lower(): row[1]
            for row in self.connection.execute(
                f"PRAGMA table_info({_quote_literal(raw_view)})"
            ).fetchall()
        }
        field_list = tuple(fields)
        missing_required = [
            field.name
            for field in field_list
            if field.required and not any(alias.lower() in columns for alias in field.aliases)
        ]
        if missing_required:
            available = ", ".join(sorted(columns))
            required = ", ".join(missing_required)
            raise DatasetSchemaError(
                f"{name} Parquet is present but missing canonical field(s): {required}. "
                f"Available columns: {available}"
            )

        projections: list[str] = []
        for field in field_list:
            source = next(
                (columns[alias.lower()] for alias in field.aliases if alias.lower() in columns),
                None,
            )
            expression = "NULL" if source is None else _quote_identifier(source)
            projections.append(
                f"TRY_CAST({expression} AS {field.sql_type}) AS {_quote_identifier(field.name)}"
            )

        # parquet-go encodes nullable logical primitives as value + presence flag.
        # Decode its epoch sentinel at this boundary so research never sees it as data.
        if name == "fundamentals" and "has_period_start" in columns:
            projections = [
                "CASE WHEN TRY_CAST(\"has_period_start\" AS BOOLEAN) "
                "THEN TRY_CAST(\"period_start\" AS DATE) END AS \"period_start\""
                if projection.endswith(' AS "period_start"')
                else projection
                for projection in projections
            ]
        if name == "macroeconomics" and "has_vintage_at" in columns:
            projections = [
                "CASE WHEN TRY_CAST(\"has_vintage_at\" AS BOOLEAN) "
                "THEN TRY_CAST(\"vintage_at\" AS TIMESTAMPTZ) END AS \"vintage_at\""
                if projection.endswith(' AS "vintage_at"')
                else projection
                for projection in projections
            ]
        self.connection.execute(
            f"CREATE OR REPLACE VIEW {_quote_identifier(name)} AS SELECT "
            + ", ".join(projections)
            + f" FROM {_quote_identifier(raw_view)}"
        )
        row_count = self.connection.execute(
            f"SELECT count(*) FROM {_quote_identifier(name)}"
        ).fetchone()[0]
        self._statuses[name] = DatasetStatus(
            name=name,
            pattern=str(absolute_pattern),
            file_count=len(files),
            row_count=row_count,
            available=True,
            message="Registered normalized Parquet.",
        )
