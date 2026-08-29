"""Deterministic ``fundamental-growth`` feature artifacts."""

from __future__ import annotations

import os
from collections.abc import Mapping
from dataclasses import dataclass
from datetime import date, datetime, timedelta
from decimal import Decimal, localcontext
from pathlib import Path
from typing import Any, Final
from uuid import UUID

from .catalog import ResearchCatalog
from .derived_artifacts import (
    DerivedArtifactContract,
    publish_derived_artifact,
    read_derived_artifact,
)
from .features import (
    FeatureArtifactError,
    _canonical_calendar_pin,
    _canonical_timestamp,
    _canonical_uuid,
    _decimal,
    _decimal_string,
    _duckdb_timestamp,
    _record_value,
    _selected_lineage,
    compute_input_fingerprint,
)
from .taxonomy import TaxonomyMapping, TaxonomyRegistry, load_taxonomy_registry

FEATURE_SET: Final[str] = "fundamental-growth"
FEATURE_SET_VERSION: Final[str] = "1.0.0"
FEATURE_NAMES: Final[tuple[str, ...]] = (
    "revenue",
    "revenue_growth_yoy",
    "operating_margin",
)
DEFAULT_GENERATOR_VERSION: Final[str] = "python-fundamental-growth-1.0.0"
DEFAULT_TAXONOMY_REGISTRY = Path(
    os.environ.get(
        "INVS_SCHEMA_ROOT",
        str(Path(__file__).resolve().parents[2] / "schemas"),
    )
) / "feature-taxonomy-registry.json"

FUNDAMENTAL_GROWTH_CONTRACT = DerivedArtifactContract(
    feature_set=FEATURE_SET,
    feature_set_version=FEATURE_SET_VERSION,
    feature_names=FEATURE_NAMES,
    identity_fields=("security_id", "issuer_id"),
    namespace=UUID("8bb4a2ef-1af1-5a1a-bd11-2e3655d79048"),
    directory_name=FEATURE_SET,
    mapping_field="taxonomy_mapping_sha256",
)
_REQUIRED_CONCEPTS: Final[tuple[str, ...]] = ("revenue", "operating_income")


def _load_mappings(
    taxonomy_registry: TaxonomyRegistry | str | Path,
) -> tuple[TaxonomyRegistry, dict[str, TaxonomyMapping]]:
    registry = (
        taxonomy_registry
        if isinstance(taxonomy_registry, TaxonomyRegistry)
        else load_taxonomy_registry(taxonomy_registry)
    )
    mappings: dict[str, TaxonomyMapping] = {}
    for concept in _REQUIRED_CONCEPTS:
        mapping = registry.resolve(FEATURE_SET, FEATURE_SET_VERSION, concept)
        if mapping.sign not in {"reported", "negated"}:
            raise FeatureArtifactError(f"unsupported sign policy for mapping {mapping.mapping_id}")
        mappings[concept] = mapping
    return registry, mappings


def _date_value(value: Any, *, field: str) -> date:
    if isinstance(value, date) and not isinstance(value, datetime):
        return value
    if isinstance(value, datetime):
        return value.date()
    if isinstance(value, str):
        try:
            return date.fromisoformat(value[:10])
        except ValueError as error:
            raise FeatureArtifactError(f"{field} is not an ISO date") from error
    if hasattr(value, "isoformat"):
        try:
            return date.fromisoformat(str(value.isoformat())[:10])
        except ValueError as error:
            raise FeatureArtifactError(f"{field} is not an ISO date") from error
    raise FeatureArtifactError(f"{field} is not an ISO date")


def _period_rows(
    records: list[Mapping[str, Any]],
    mapping: TaxonomyMapping,
) -> list[Mapping[str, Any]]:
    result: list[Mapping[str, Any]] = []
    for index, record in enumerate(records):
        if _record_value(record, "taxonomy") != mapping.taxonomy:
            raise FeatureArtifactError(
                f"fundamental row {index} taxonomy differs from reviewed mapping {mapping.mapping_id}"
            )
        if _record_value(record, "unit") != mapping.unit:
            raise FeatureArtifactError(
                f"fundamental row {index} unit differs from reviewed mapping {mapping.mapping_id}"
            )
        if _record_value(record, "currency") != mapping.currency:
            raise FeatureArtifactError(
                f"fundamental row {index} currency differs from reviewed mapping {mapping.mapping_id}"
            )
        fiscal_period = _record_value(record, "fiscal_period")
        if fiscal_period not in mapping.fiscal_periods:
            continue
        _date_value(_record_value(record, "period_end"), field="fundamental.period_end")
        result.append(record)
    return result


def _latest_period(
    records: list[Mapping[str, Any]],
) -> Mapping[str, Any] | None:
    if not records:
        return None
    return max(
        records,
        key=lambda record: (
            _date_value(_record_value(record, "period_end"), field="fundamental.period_end"),
            str(_record_value(record, "fiscal_period")),
            _duckdb_timestamp(_record_value(record, "available_at"), field="available_at"),
            int(_record_value(record, "revision")),
            str(_record_value(record, "raw_payload_hash")),
        ),
    )


def _same_prior_year(
    records: list[Mapping[str, Any]],
    current: Mapping[str, Any],
) -> Mapping[str, Any] | None:
    current_period = _date_value(_record_value(current, "period_end"), field="fundamental.period_end")
    current_fiscal_period = _record_value(current, "fiscal_period")
    candidates = [
        record
        for record in records
        if _record_value(record, "fiscal_period") == current_fiscal_period
        and _date_value(_record_value(record, "period_end"), field="fundamental.period_end").year
        == current_period.year - 1
    ]
    return _latest_period(candidates)


def _signed_value(record: Mapping[str, Any], mapping: TaxonomyMapping, *, field: str) -> Decimal | None:
    raw_value = _record_value(record, "value_text")
    if raw_value is None or _record_value(record, "has_value") is not True:
        return None
    value = _decimal(raw_value, field=field)
    return value if mapping.sign == "reported" else -value


def _compute_features(
    revenue_rows: list[Mapping[str, Any]],
    operating_income_rows: list[Mapping[str, Any]],
    mappings: Mapping[str, TaxonomyMapping],
) -> dict[str, str | None]:
    current_revenue = _latest_period(revenue_rows)
    if current_revenue is None:
        raise FeatureArtifactError("no reviewed revenue rows are eligible at the requested decision_at")
    revenue_value = _signed_value(current_revenue, mappings["revenue"], field="revenue")
    revenue_text = None if revenue_value is None else _decimal_string(revenue_value, field="revenue")
    growth_text: str | None = None
    margin_text: str | None = None
    prior_revenue = _same_prior_year(revenue_rows, current_revenue)
    current_operating_income = next(
        (
            record
            for record in operating_income_rows
            if _record_value(record, "fiscal_period") == _record_value(current_revenue, "fiscal_period")
            and _date_value(_record_value(record, "period_end"), field="fundamental.period_end")
            == _date_value(_record_value(current_revenue, "period_end"), field="fundamental.period_end")
        ),
        None,
    )
    prior_value = (
        None
        if prior_revenue is None
        else _signed_value(prior_revenue, mappings["revenue"], field="prior revenue")
    )
    operating_income_value = (
        None
        if current_operating_income is None
        else _signed_value(
            current_operating_income,
            mappings["operating_income"],
            field="operating income",
        )
    )
    with localcontext() as context:
        context.prec = 256
        if revenue_value is not None and prior_value is not None and not prior_value.is_zero():
            growth_text = _decimal_string(
                revenue_value / prior_value - Decimal(1),
                field="revenue_growth_yoy",
            )
        if (
            revenue_value is not None
            and operating_income_value is not None
            and not revenue_value.is_zero()
        ):
            margin_text = _decimal_string(
                operating_income_value / revenue_value,
                field="operating_margin",
            )
    return {
        "revenue": revenue_text,
        "revenue_growth_yoy": growth_text,
        "operating_margin": margin_text,
    }


@dataclass(frozen=True)
class _Selection:
    security_id: str
    issuer_id: str
    decision_at: str
    calendar_pin: dict[str, str]
    input_available_at: str
    available_at: str
    computation_delay_seconds: int
    input_fingerprint: str
    selected_input_manifests: list[dict[str, str]]
    selected_input_parts: list[dict[str, str]]
    mapping_sha256: str
    features: dict[str, str | None]


def _select(
    catalog: ResearchCatalog,
    *,
    decision_at: str | datetime,
    security_id: str | UUID,
    issuer_id: str | UUID,
    taxonomy_registry: TaxonomyRegistry | str | Path,
    calendar_pin: Mapping[str, Any],
    computation_delay_seconds: int,
) -> _Selection:
    delay = computation_delay_seconds
    if not isinstance(delay, int) or isinstance(delay, bool) or delay < 0:
        raise FeatureArtifactError("computation_delay_seconds must be a non-negative integer")
    canonical_decision, decision_datetime = _canonical_timestamp(decision_at, field="decision_at")
    canonical_security_id = _canonical_uuid(security_id, field="security_id")
    canonical_issuer_id = _canonical_uuid(issuer_id, field="issuer_id")
    canonical_calendar = _canonical_calendar_pin(
        calendar_pin,
        decision_at=decision_datetime,
    )
    registry, mappings = _load_mappings(taxonomy_registry)
    rows_by_concept: dict[str, list[Mapping[str, Any]]] = {}
    all_records: list[Mapping[str, Any]] = []
    for concept, mapping in mappings.items():
        try:
            selected = catalog.point_in_time_inputs(
                decision_at=canonical_decision,
                dataset="fundamentals",
                issuer_id=canonical_issuer_id,
                source=mapping.source,
                taxonomy=mapping.taxonomy,
                concept=mapping.source_concept,
                unit=mapping.unit,
                currency=mapping.currency,
            )
        except Exception as error:
            if isinstance(error, FeatureArtifactError):
                raise
            raise FeatureArtifactError(f"point-in-time fundamental selection failed: {error}") from error
        records = _period_rows(
            [dict(record) for record in selected.frame.to_dict(orient="records")],
            mapping,
        )
        if not records:
            if concept == "revenue":
                raise FeatureArtifactError(
                    "no reviewed revenue rows are eligible at the requested decision_at"
                )
            rows_by_concept[concept] = []
            continue
        rows_by_concept[concept] = records
        all_records.extend(records)
    if not all_records:
        raise FeatureArtifactError("no reviewed fundamental rows are eligible at the requested decision_at")
    available_datetimes = [
        _duckdb_timestamp(_record_value(record, "available_at"), field="available_at")
        for record in all_records
    ]
    if any(value > decision_datetime for value in available_datetimes):
        raise FeatureArtifactError("point-in-time fundamental selection returned a future available_at")
    input_available_datetime = max(available_datetimes)
    _, calendar_available_datetime = _canonical_timestamp(
        canonical_calendar["calendar_available_at"],
        field="calendar_pin.calendar_available_at",
    )
    input_available_datetime = max(input_available_datetime, calendar_available_datetime)
    input_available_at = _canonical_timestamp(input_available_datetime, field="input_available_at")[0]
    available_at = _canonical_timestamp(
        input_available_datetime + timedelta(seconds=delay), field="available_at"
    )[0]
    selected_manifests, selected_parts = _selected_lineage(catalog, all_records)
    fingerprint = compute_input_fingerprint(
        {
            "calendar_pin": canonical_calendar,
            "decision_at": canonical_decision,
            "feature_set": FEATURE_SET,
            "feature_set_version": FEATURE_SET_VERSION,
            "selected_input_manifests": selected_manifests,
            "selected_input_parts": selected_parts,
            "taxonomy_mapping_sha256": registry.registry_sha256,
        }
    )
    return _Selection(
        security_id=canonical_security_id,
        issuer_id=canonical_issuer_id,
        decision_at=canonical_decision,
        calendar_pin=canonical_calendar,
        input_available_at=input_available_at,
        available_at=available_at,
        computation_delay_seconds=delay,
        input_fingerprint=fingerprint,
        selected_input_manifests=selected_manifests,
        selected_input_parts=selected_parts,
        mapping_sha256=registry.registry_sha256,
        features=_compute_features(
            rows_by_concept["revenue"],
            rows_by_concept["operating_income"],
            mappings,
        ),
    )


def compute_fundamental_growth_features(
    catalog: ResearchCatalog,
    *,
    decision_at: str | datetime,
    security_id: str | UUID,
    issuer_id: str | UUID,
    taxonomy_registry: TaxonomyRegistry | str | Path = DEFAULT_TAXONOMY_REGISTRY,
    calendar_pin: Mapping[str, Any],
) -> dict[str, str | None]:
    """Compute reviewed point-in-time fundamental growth features."""
    return _select(
        catalog,
        decision_at=decision_at,
        security_id=security_id,
        issuer_id=issuer_id,
        taxonomy_registry=taxonomy_registry,
        calendar_pin=calendar_pin,
        computation_delay_seconds=0,
    ).features


def publish_fundamental_growth(
    catalog: ResearchCatalog,
    *,
    decision_at: str | datetime,
    security_id: str | UUID,
    issuer_id: str | UUID,
    taxonomy_registry: TaxonomyRegistry | str | Path = DEFAULT_TAXONOMY_REGISTRY,
    calendar_pin: Mapping[str, Any],
    features_root: str | Path = "data/features",
    computation_delay_seconds: int = 0,
    artifact_id: str | UUID | None = None,
    generator_version: str = DEFAULT_GENERATOR_VERSION,
    git_commit: str = "unknown",
    created_at: str | None = None,
) -> Path:
    """Publish one immutable deterministic fundamental-growth artifact."""
    selection = _select(
        catalog,
        decision_at=decision_at,
        security_id=security_id,
        issuer_id=issuer_id,
        taxonomy_registry=taxonomy_registry,
        calendar_pin=calendar_pin,
        computation_delay_seconds=computation_delay_seconds,
    )
    return publish_derived_artifact(
        contract=FUNDAMENTAL_GROWTH_CONTRACT,
        identity={"security_id": selection.security_id, "issuer_id": selection.issuer_id},
        decision_at=selection.decision_at,
        calendar_pin=selection.calendar_pin,
        input_available_at=selection.input_available_at,
        available_at=selection.available_at,
        computation_delay_seconds=selection.computation_delay_seconds,
        input_fingerprint=selection.input_fingerprint,
        selected_input_manifests=selection.selected_input_manifests,
        selected_input_parts=selection.selected_input_parts,
        features=selection.features,
        features_root=features_root,
        generator_version=generator_version,
        git_commit=git_commit,
        artifact_id=artifact_id,
        created_at=created_at,
        mapping_sha256=selection.mapping_sha256,
    )


def read_fundamental_growth_artifact(manifest_path: str | Path):
    """Read one strict fundamental-growth artifact."""
    return read_derived_artifact(manifest_path, contract=FUNDAMENTAL_GROWTH_CONTRACT)


def validate_fundamental_growth_artifact(manifest_path: str | Path):
    """Validate one strict fundamental-growth artifact."""
    return read_fundamental_growth_artifact(manifest_path)


__all__ = [
    "DEFAULT_GENERATOR_VERSION",
    "DEFAULT_TAXONOMY_REGISTRY",
    "FEATURE_NAMES",
    "FEATURE_SET",
    "FEATURE_SET_VERSION",
    "compute_fundamental_growth_features",
    "publish_fundamental_growth",
    "read_fundamental_growth_artifact",
    "validate_fundamental_growth_artifact",
]
