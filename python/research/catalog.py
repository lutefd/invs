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
import yaml


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
class SecurityMapping:
    """Configured link between the price security and SEC issuer identifiers."""

    security_id: str
    issuer_id: str
    ticker: str | None = None
    legal_name: str | None = None


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
            _Field("source", "VARCHAR", ("source",), True),
            _Field("security_id", "VARCHAR", ("security_id",), True),
            _Field("currency", "VARCHAR", ("currency",), True),
            _Field("observed_at", "TIMESTAMPTZ", ("observed_at",), True),
            _Field("trading_date", "DATE", ("observed_at",), True),
            _Field("published_at", "TIMESTAMPTZ", ("published_at",), True),
            _Field("published_precision", "VARCHAR", ("published_precision",), True),
            _Field("available_at", "TIMESTAMPTZ", ("available_at",), True),
            _Field("ingested_at", "TIMESTAMPTZ", ("ingested_at",), True),
            _Field("open", "DOUBLE", ("open",), True),
            _Field("high", "DOUBLE", ("high",), True),
            _Field("low", "DOUBLE", ("low",), True),
            _Field("close", "DOUBLE", ("close",), True),
            _Field("volume", "BIGINT", ("volume",), True),
            _Field("raw_payload_hash", "VARCHAR", ("raw_payload_hash",), True),
        ),
    ),
    "fundamentals": (
        "fundamentals/**/*.parquet",
        (
            _Field("source", "VARCHAR", ("source",), True),
            _Field("issuer_id", "VARCHAR", ("issuer_id",), True),
            _Field("taxonomy", "VARCHAR", ("taxonomy",), True),
            _Field("concept", "VARCHAR", ("concept",), True),
            _Field("unit", "VARCHAR", ("unit",), True),
            _Field("observed_at", "TIMESTAMPTZ", ("observed_at",), True),
            _Field("published_at", "TIMESTAMPTZ", ("published_at",), True),
            _Field("published_precision", "VARCHAR", ("published_precision",), True),
            _Field("available_at", "TIMESTAMPTZ", ("available_at",), True),
            _Field("ingested_at", "TIMESTAMPTZ", ("ingested_at",), True),
            _Field("period_start", "DATE", ("period_start",), True),
            _Field("has_period_start", "BOOLEAN", ("has_period_start",), True),
            _Field("period_end", "DATE", ("period_end",), True),
            _Field("value", "DOUBLE", ("value",), True),
            _Field("accession_number", "VARCHAR", ("accession_number",), True),
            _Field("form", "VARCHAR", ("form",), True),
            _Field("fiscal_year", "INTEGER", ("fiscal_year",), True),
            _Field("fiscal_period", "VARCHAR", ("fiscal_period",), True),
            _Field("frame", "VARCHAR", ("frame",), True),
            _Field("raw_payload_hash", "VARCHAR", ("raw_payload_hash",), True),
        ),
    ),
    "macroeconomics": (
        "macroeconomics/**/*.parquet",
        (
            _Field("source", "VARCHAR", ("source",), True),
            _Field("series_id", "VARCHAR", ("series_id",), True),
            _Field("unit", "VARCHAR", ("unit",), True),
            _Field("observed_at", "TIMESTAMPTZ", ("observed_at",), True),
            _Field("observation_date", "DATE", ("observed_at",), True),
            _Field("published_at", "TIMESTAMPTZ", ("published_at",), True),
            _Field("published_precision", "VARCHAR", ("published_precision",), True),
            _Field("available_at", "TIMESTAMPTZ", ("available_at",), True),
            _Field("ingested_at", "TIMESTAMPTZ", ("ingested_at",), True),
            _Field("value", "DOUBLE", ("value",), True),
            _Field("vintage_at", "TIMESTAMPTZ", ("vintage_at",), True),
            _Field("has_vintage_at", "BOOLEAN", ("has_vintage_at",), True),
            _Field("raw_payload_hash", "VARCHAR", ("raw_payload_hash",), True),
        ),
    ),
}


def _quote_literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def _quote_identifier(value: str) -> str:
    return '"' + value.replace('"', '""') + '"'


def load_security_mappings(config_path: str | Path) -> tuple[SecurityMapping, ...]:
    """Read the collector universe and preserve its issuer/security relationship."""
    path = Path(config_path).expanduser().resolve()
    try:
        document = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
    except (OSError, yaml.YAMLError) as error:
        raise ValueError(f"cannot read collector configuration {path}: {error}") from error
    universe = document.get("universe")
    if not isinstance(universe, list):
        raise TypeError(f"collector configuration {path} must contain a universe list")
    mappings: list[SecurityMapping] = []
    pairs: set[tuple[str, str]] = set()
    for index, item in enumerate(universe):
        if not isinstance(item, dict):
            raise TypeError(f"universe[{index}] must be an object")
        security_id, issuer_id = item.get("security_id"), item.get("issuer_id")
        if not isinstance(security_id, str) or not isinstance(issuer_id, str):
            raise TypeError(f"universe[{index}] requires string security_id and issuer_id")
        pair = (security_id, issuer_id)
        if pair in pairs:
            raise ValueError(f"duplicate universe mapping {security_id} -> {issuer_id}")
        pairs.add(pair)
        mappings.append(
            SecurityMapping(
                security_id=security_id,
                issuer_id=issuer_id,
                ticker=item.get("ticker") if isinstance(item.get("ticker"), str) else None,
                legal_name=(
                    item.get("legal_name")
                    if isinstance(item.get("legal_name"), str)
                    else None
                ),
            )
        )
    return tuple(mappings)


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

    def available_mappings(
        self, mappings: Iterable[SecurityMapping]
    ) -> tuple[SecurityMapping, ...]:
        """Return configured mappings with both price and fundamental observations."""
        price_ids = {
            row[0]
            for row in self.connection.execute(
                "SELECT DISTINCT security_id FROM prices WHERE security_id IS NOT NULL"
            ).fetchall()
        }
        issuer_ids = {
            row[0]
            for row in self.connection.execute(
                "SELECT DISTINCT issuer_id FROM fundamentals WHERE issuer_id IS NOT NULL"
            ).fetchall()
        }
        return tuple(
            mapping
            for mapping in mappings
            if mapping.security_id in price_ids and mapping.issuer_id in issuer_ids
        )

    def research_snapshot(
        self,
        *,
        decision_at: str,
        mapping: SecurityMapping,
        fundamental_concept: str,
        macro_series_id: str,
        start: str | None = None,
        end: str | None = None,
    ):
        """Return price history and latest facts as known at one decision timestamp.

        Price revisions are collapsed per observed session to the latest version
        available by ``decision_at``. Fundamentals and macro values are likewise the
        latest eligible deterministic revision at that decision timestamp. This is a
        research snapshot, not a historical backtest whose decision time varies by row.
        """
        clauses = ["security_id = $security_id"]
        parameters: dict[str, object] = {
            "security_id": mapping.security_id,
            "issuer_id": mapping.issuer_id,
            "fundamental_concept": fundamental_concept,
            "macro_series_id": macro_series_id,
            "decision_at": decision_at,
        }
        if start is not None:
            clauses.append("trading_date >= CAST($start AS DATE)")
            parameters["start"] = start
        if end is not None:
            clauses.append("trading_date <= CAST($end AS DATE)")
            parameters["end"] = end

        sql = f"""
            WITH selected_prices AS (
                SELECT *, CAST($decision_at AS TIMESTAMPTZ) AS known_at
                FROM prices
                WHERE {' AND '.join(clauses)}
                  AND available_at <= CAST($decision_at AS TIMESTAMPTZ)
                  AND observed_at <= CAST($decision_at AS TIMESTAMPTZ)
                QUALIFY row_number() OVER (
                    PARTITION BY security_id, observed_at
                    ORDER BY available_at DESC, ingested_at DESC, raw_payload_hash DESC
                ) = 1
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
            LEFT JOIN LATERAL (
                SELECT * FROM selected_fundamentals candidate
                WHERE candidate.available_at <= CAST($decision_at AS TIMESTAMPTZ)
                  AND candidate.observed_at <= CAST($decision_at AS TIMESTAMPTZ)
                  AND candidate.period_end <= CAST($decision_at AS DATE)
                ORDER BY
                    candidate.available_at DESC,
                    candidate.period_end DESC,
                    candidate.published_at DESC,
                    candidate.accession_number DESC,
                    candidate.ingested_at DESC,
                    candidate.raw_payload_hash DESC
                LIMIT 1
            ) f ON TRUE
            LEFT JOIN LATERAL (
                SELECT * FROM selected_macro candidate
                WHERE candidate.available_at <= CAST($decision_at AS TIMESTAMPTZ)
                  AND candidate.observed_at <= CAST($decision_at AS TIMESTAMPTZ)
                ORDER BY
                    candidate.available_at DESC,
                    candidate.observed_at DESC,
                    candidate.published_at DESC,
                    candidate.vintage_at DESC NULLS LAST,
                    candidate.ingested_at DESC,
                    candidate.raw_payload_hash DESC
                LIMIT 1
            ) m ON TRUE
            ORDER BY p.trading_date
        """
        return self.connection.execute(sql, parameters).fetchdf()

    def point_in_time_frame(self, **kwargs):
        """Compatibility alias for :meth:`research_snapshot`; decision_at is required."""
        if "decision_at" not in kwargs:
            raise TypeError("point_in_time_frame requires an explicit decision_at")
        return self.research_snapshot(**kwargs)

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
        if name == "fundamentals":
            projections = [
                "CASE WHEN TRY_CAST(\"has_period_start\" AS BOOLEAN) "
                "THEN TRY_CAST(\"period_start\" AS DATE) END AS \"period_start\""
                if projection.endswith(' AS "period_start"')
                else projection
                for projection in projections
            ]
        if name == "macroeconomics":
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
