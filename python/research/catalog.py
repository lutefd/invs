"""Register canonical v1 Parquet and point-in-time DuckDB research views.

The public ``*_canonical`` views preserve lossless decimal strings and provenance.
The shorter research views add explicitly lossy DECIMAL/DOUBLE projections for
analysis. Populated legacy or unsupported files fail closed with a migration error.
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


@dataclass(frozen=True)
class _Dataset:
    pattern: str
    fields: tuple[_Field, ...]
    decimal_fields: tuple[tuple[str, bool, bool], ...]


_SCHEMA_VERSION: Final = "1.0.0"
_DECIMAL_TYPE: Final = "DECIMAL(38, 18)"

_PRICE_FIELDS = (
    _Field("schema_version", "VARCHAR"),
    _Field("source", "VARCHAR"),
    _Field("security_id", "VARCHAR"),
    _Field("interval", "VARCHAR"),
    _Field("price_basis", "VARCHAR"),
    _Field("currency", "VARCHAR"),
    _Field("observed_at", "TIMESTAMPTZ"),
    _Field("published_at", "TIMESTAMPTZ"),
    _Field("has_published_at", "BOOLEAN"),
    _Field("published_precision", "VARCHAR"),
    _Field("available_at", "TIMESTAMPTZ"),
    _Field("ingested_at", "TIMESTAMPTZ"),
    _Field("open", "VARCHAR"),
    _Field("high", "VARCHAR"),
    _Field("low", "VARCHAR"),
    _Field("close", "VARCHAR"),
    _Field("volume", "VARCHAR"),
    _Field("has_volume", "BOOLEAN"),
    _Field("raw_payload_hash", "VARCHAR"),
    _Field("data_source_id", "VARCHAR"),
    _Field("ingestion_run_id", "VARCHAR"),
    _Field("raw_record_locator", "VARCHAR"),
    _Field("normalizer_version", "VARCHAR"),
)

_FUNDAMENTAL_FIELDS = (
    _Field("schema_version", "VARCHAR"),
    _Field("source", "VARCHAR"),
    _Field("issuer_id", "VARCHAR"),
    _Field("security_id", "VARCHAR"),
    _Field("has_security_id", "BOOLEAN"),
    _Field("taxonomy", "VARCHAR"),
    _Field("concept", "VARCHAR"),
    _Field("unit", "VARCHAR"),
    _Field("currency", "VARCHAR"),
    _Field("has_currency", "BOOLEAN"),
    _Field("observed_at", "TIMESTAMPTZ"),
    _Field("published_at", "TIMESTAMPTZ"),
    _Field("published_precision", "VARCHAR"),
    _Field("available_at", "TIMESTAMPTZ"),
    _Field("ingested_at", "TIMESTAMPTZ"),
    _Field("period_start", "DATE"),
    _Field("has_period_start", "BOOLEAN"),
    _Field("period_end", "DATE"),
    _Field("value", "VARCHAR"),
    _Field("has_value", "BOOLEAN"),
    _Field("revision", "INTEGER"),
    _Field("accession_number", "VARCHAR"),
    _Field("form", "VARCHAR"),
    _Field("fiscal_year", "INTEGER"),
    _Field("fiscal_period", "VARCHAR"),
    _Field("frame", "VARCHAR"),
    _Field("raw_payload_hash", "VARCHAR"),
    _Field("data_source_id", "VARCHAR"),
    _Field("ingestion_run_id", "VARCHAR"),
    _Field("raw_record_locator", "VARCHAR"),
    _Field("normalizer_version", "VARCHAR"),
)

_MACRO_FIELDS = (
    _Field("schema_version", "VARCHAR"),
    _Field("source", "VARCHAR"),
    _Field("series_id", "VARCHAR"),
    _Field("geography", "VARCHAR"),
    _Field("unit", "VARCHAR"),
    _Field("frequency", "VARCHAR"),
    _Field("seasonal_adjustment", "VARCHAR"),
    _Field("has_seasonal_adjustment", "BOOLEAN"),
    _Field("observed_at", "TIMESTAMPTZ"),
    _Field("published_at", "TIMESTAMPTZ"),
    _Field("published_precision", "VARCHAR"),
    _Field("available_at", "TIMESTAMPTZ"),
    _Field("ingested_at", "TIMESTAMPTZ"),
    _Field("value", "VARCHAR"),
    _Field("has_value", "BOOLEAN"),
    _Field("revision", "INTEGER"),
    _Field("vintage_at", "TIMESTAMPTZ"),
    _Field("has_vintage_at", "BOOLEAN"),
    _Field("raw_payload_hash", "VARCHAR"),
    _Field("data_source_id", "VARCHAR"),
    _Field("ingestion_run_id", "VARCHAR"),
    _Field("raw_record_locator", "VARCHAR"),
    _Field("normalizer_version", "VARCHAR"),
)

_DATASETS: Final[dict[str, _Dataset]] = {
    "prices": _Dataset(
        "prices/**/*.parquet",
        _PRICE_FIELDS,
        (("open", False, False), ("high", False, False), ("low", False, False),
         ("close", False, False), ("volume", False, True)),
    ),
    "fundamentals": _Dataset(
        "fundamentals/**/*.parquet", _FUNDAMENTAL_FIELDS, (("value", False, True),)
    ),
    "macroeconomics": _Dataset(
        "macroeconomics/**/*.parquet", _MACRO_FIELDS, (("value", False, True),)
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
    """A DuckDB session over canonical prices, fundamentals, and macro data."""

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
        """Register all canonical and research views and return this catalog."""
        for name, dataset in _DATASETS.items():
            self._register_dataset(name, dataset)
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
        """Return price history and latest facts known at one decision timestamp."""
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
                    PARTITION BY source, security_id, interval, price_basis, observed_at
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
                p.close_value,
                p.currency AS price_currency,
                p.known_at,
                f.issuer_id,
                f.concept AS fundamental_concept,
                f.value AS fundamental_value,
                f.value_text AS fundamental_value_text,
                f.period_end AS fundamental_period_end,
                f.published_at AS fundamental_published_at,
                f.available_at AS fundamental_available_at,
                m.series_id AS macro_series_id,
                m.value AS macro_value,
                m.value_text AS macro_value_text,
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
                    candidate.revision DESC,
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
                    candidate.revision DESC,
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

    def _register_dataset(self, name: str, dataset: _Dataset) -> None:
        absolute_pattern = self.normalized_root / dataset.pattern
        files = sorted(self.normalized_root.glob(dataset.pattern))
        canonical_view = f"{name}_canonical"
        if not files:
            projections = ", ".join(
                f"CAST(NULL AS {field.sql_type}) AS {_quote_identifier(field.name)}"
                for field in dataset.fields
            )
            self.connection.execute(
                f"CREATE OR REPLACE VIEW {_quote_identifier(canonical_view)} AS "
                f"SELECT {projections} WHERE FALSE"
            )
            self._create_research_view(name)
            self._statuses[name] = DatasetStatus(
                name=name,
                pattern=str(absolute_pattern),
                file_count=0,
                row_count=0,
                available=False,
                message="No Parquet files found; registered typed empty canonical views.",
            )
            return

        self._validate_file_schemas(name, files, dataset)
        physical_view = f"_{name}_physical"
        self.connection.execute(
            f"CREATE OR REPLACE VIEW {_quote_identifier(physical_view)} AS "
            f"SELECT * FROM read_parquet({_quote_literal(str(absolute_pattern))}, "
            "union_by_name=true, hive_partitioning=false)"
        )
        versions = self.connection.execute(
            f"SELECT DISTINCT schema_version FROM {_quote_identifier(physical_view)} "
            "ORDER BY schema_version NULLS FIRST"
        ).fetchall()
        if versions != [(_SCHEMA_VERSION,)]:
            found = ", ".join("NULL" if row[0] is None else repr(row[0]) for row in versions)
            raise DatasetSchemaError(
                f"{name} Parquet requires canonical schema_version {_SCHEMA_VERSION}; "
                f"found {found or 'no rows'}. Normalized Parquet migration required."
            )
        self._validate_decimal_strings(name, physical_view, dataset)
        projections = self._canonical_projections(name, dataset.fields)
        self.connection.execute(
            f"CREATE OR REPLACE VIEW {_quote_identifier(canonical_view)} AS SELECT "
            + ", ".join(projections)
            + f" FROM {_quote_identifier(physical_view)}"
        )
        self._create_research_view(name)
        row_count = self.connection.execute(
            f"SELECT count(*) FROM {_quote_identifier(canonical_view)}"
        ).fetchone()[0]
        self._statuses[name] = DatasetStatus(
            name=name,
            pattern=str(absolute_pattern),
            file_count=len(files),
            row_count=row_count,
            available=True,
            message=f"Registered canonical Parquet schema {_SCHEMA_VERSION}.",
        )

    def _validate_file_schemas(
        self, name: str, files: list[Path], dataset: _Dataset
    ) -> None:
        required = {field.name for field in dataset.fields}
        string_decimals = {field[0] for field in dataset.decimal_fields}
        for path in files:
            rows = self.connection.execute(
                "DESCRIBE SELECT * FROM read_parquet("
                f"{_quote_literal(str(path))}, hive_partitioning=false)"
            ).fetchall()
            columns = {row[0].lower(): row[1].upper() for row in rows}
            missing = sorted(required - columns.keys())
            if missing:
                raise DatasetSchemaError(
                    f"{name} file {path} is legacy or incompatible; missing canonical v1 "
                    f"field(s): {', '.join(missing)}. Normalized Parquet migration required."
                )
            wrong_decimal_types = sorted(
                field for field in string_decimals if columns[field] != "VARCHAR"
            )
            if wrong_decimal_types:
                raise DatasetSchemaError(
                    f"{name} file {path} stores canonical decimal field(s) as non-UTF8: "
                    f"{', '.join(wrong_decimal_types)}. Normalized Parquet migration required."
                )

    def _validate_decimal_strings(
        self, name: str, physical_view: str, dataset: _Dataset
    ) -> None:
        signed_pattern = r"^-?(0|[1-9][0-9]*)(\.[0-9]+)?$"
        unsigned_pattern = r"^(0|[1-9][0-9]*)(\.[0-9]+)?$"
        for field, integer_only, optional in dataset.decimal_fields:
            pattern = r"^(0|[1-9][0-9]*)$" if integer_only else (
                signed_pattern if name != "prices" else unsigned_pattern
            )
            presence = f"has_{field}" if optional else None
            predicate = f"{_quote_identifier(presence)} AND " if presence is not None else ""
            invalid = self.connection.execute(
                f"SELECT count(*) FROM {_quote_identifier(physical_view)} WHERE {predicate}"
                f"({_quote_identifier(field)} IS NULL OR NOT regexp_full_match("
                f"{_quote_identifier(field)}, {_quote_literal(pattern)}))"
            ).fetchone()[0]
            if invalid:
                raise DatasetSchemaError(
                    f"{name} Parquet contains {invalid} invalid canonical decimal "
                    f"value(s) in {field}."
                )

    @staticmethod
    def _canonical_projections(name: str, fields: tuple[_Field, ...]) -> list[str]:
        nullable_values = {
            "prices": {"published_at": "has_published_at", "volume": "has_volume"},
            "fundamentals": {
                "security_id": "has_security_id",
                "currency": "has_currency",
                "period_start": "has_period_start",
                "value": "has_value",
            },
            "macroeconomics": {
                "seasonal_adjustment": "has_seasonal_adjustment",
                "value": "has_value",
                "vintage_at": "has_vintage_at",
            },
        }[name]
        projections = []
        for field in fields:
            source = _quote_identifier(field.name)
            cast = f"CAST({source} AS {field.sql_type})"
            if presence := nullable_values.get(field.name):
                cast = f"CASE WHEN {_quote_identifier(presence)} THEN {cast} END"
            projections.append(f"{cast} AS {source}")
        return projections

    def _create_research_view(self, name: str) -> None:
        canonical = _quote_identifier(f"{name}_canonical")
        if name == "prices":
            sql = f"""
                SELECT
                    schema_version, source, security_id, interval, price_basis, currency,
                    observed_at, CAST(observed_at AS DATE) AS trading_date,
                    published_at, has_published_at, published_precision,
                    available_at, ingested_at,
                    open AS open_value, TRY_CAST(open AS {_DECIMAL_TYPE}) AS open_decimal,
                    TRY_CAST(open AS DOUBLE) AS open,
                    high AS high_value, TRY_CAST(high AS {_DECIMAL_TYPE}) AS high_decimal,
                    TRY_CAST(high AS DOUBLE) AS high,
                    low AS low_value, TRY_CAST(low AS {_DECIMAL_TYPE}) AS low_decimal,
                    TRY_CAST(low AS DOUBLE) AS low,
                    close AS close_value, TRY_CAST(close AS {_DECIMAL_TYPE}) AS close_decimal,
                    TRY_CAST(close AS DOUBLE) AS close,
                    volume AS volume_value, TRY_CAST(volume AS {_DECIMAL_TYPE}) AS volume_decimal,
                    TRY_CAST(volume AS DOUBLE) AS volume, has_volume,
                    raw_payload_hash, data_source_id, ingestion_run_id,
                    raw_record_locator, normalizer_version
                FROM {canonical}
            """
        elif name == "fundamentals":
            sql = f"""
                SELECT
                    schema_version, source, issuer_id, security_id, has_security_id,
                    taxonomy, concept, unit, currency, has_currency,
                    observed_at, published_at, published_precision, available_at, ingested_at,
                    period_start, has_period_start, period_end,
                    value AS value_text, TRY_CAST(value AS {_DECIMAL_TYPE}) AS value_decimal,
                    TRY_CAST(value AS DOUBLE) AS value, has_value, revision,
                    accession_number, form, fiscal_year, fiscal_period, frame,
                    raw_payload_hash, data_source_id, ingestion_run_id,
                    raw_record_locator, normalizer_version
                FROM {canonical}
            """
        else:
            sql = f"""
                SELECT
                    schema_version, source, series_id, geography, unit, frequency,
                    seasonal_adjustment, has_seasonal_adjustment,
                    observed_at, CAST(observed_at AS DATE) AS observation_date,
                    published_at, published_precision, available_at, ingested_at,
                    value AS value_text, TRY_CAST(value AS {_DECIMAL_TYPE}) AS value_decimal,
                    TRY_CAST(value AS DOUBLE) AS value, has_value, revision,
                    vintage_at, has_vintage_at, raw_payload_hash,
                    data_source_id, ingestion_run_id, raw_record_locator, normalizer_version
                FROM {canonical}
            """
        self.connection.execute(
            f"CREATE OR REPLACE VIEW {_quote_identifier(name)} AS {sql}"
        )
