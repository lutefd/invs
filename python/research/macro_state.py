"""Deterministic ``macro-state`` feature artifacts from historical vintages."""

from __future__ import annotations

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

FEATURE_SET: Final[str] = "macro-state"
FEATURE_SET_VERSION: Final[str] = "1.0.0"
FEATURE_NAMES: Final[tuple[str, ...]] = ("macro_level", "macro_change_yoy")
DEFAULT_GENERATOR_VERSION: Final[str] = "python-macro-state-1.0.0"
DEFAULT_SOURCE: Final[str] = "alfred"
DEFAULT_SERIES_ID: Final[str] = "CPIAUCSL"
DEFAULT_GEOGRAPHY: Final[str] = "US"
DEFAULT_UNIT: Final[str] = "Index"
DEFAULT_FREQUENCY: Final[str] = "monthly"

MACRO_STATE_CONTRACT = DerivedArtifactContract(
    feature_set=FEATURE_SET,
    feature_set_version=FEATURE_SET_VERSION,
    feature_names=FEATURE_NAMES,
    identity_fields=("security_id", "source", "series_id", "geography", "unit", "frequency"),
    namespace=UUID("cbbf4f0e-6e7c-5d8a-8ed5-95b8b5e5cc64"),
    directory_name=FEATURE_SET,
)


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


def _latest(records: list[Mapping[str, Any]]) -> Mapping[str, Any] | None:
    if not records:
        return None
    return max(
        records,
        key=lambda record: (
            _date_value(_record_value(record, "observation_date"), field="macro.observation_date"),
            int(_record_value(record, "revision")),
            _duckdb_timestamp(_record_value(record, "available_at"), field="available_at"),
            _duckdb_timestamp(_record_value(record, "vintage_at"), field="vintage_at"),
            str(_record_value(record, "raw_payload_hash")),
        ),
    )


def _prior_year(
    records: list[Mapping[str, Any]],
    current: Mapping[str, Any],
) -> Mapping[str, Any] | None:
    current_date = _date_value(_record_value(current, "observation_date"), field="macro.observation_date")
    candidates = [
        record
        for record in records
        if (_date_value(_record_value(record, "observation_date"), field="macro.observation_date").year
            == current_date.year - 1)
        and (
            _date_value(_record_value(record, "observation_date"), field="macro.observation_date").month
            == current_date.month
        )
    ]
    return _latest(candidates)


def _value(record: Mapping[str, Any], *, field: str) -> Decimal | None:
    if _record_value(record, "has_value") is not True:
        return None
    raw_value = _record_value(record, "value_text")
    return None if raw_value is None else _decimal(raw_value, field=field)


def _compute_features(records: list[Mapping[str, Any]]) -> dict[str, str | None]:
    current = _latest(records)
    if current is None:
        raise FeatureArtifactError("no historical macro rows are eligible at the requested decision_at")
    for index, record in enumerate(records):
        if _record_value(record, "has_vintage_at") is not True or _record_value(record, "vintage_at") is None:
            raise FeatureArtifactError(f"macro row {index} has no historical vintage timestamp")
    current_value = _value(current, field="macro_level")
    previous = _prior_year(records, current)
    previous_value = None if previous is None else _value(previous, field="prior macro value")
    change: str | None = None
    with localcontext() as context:
        context.prec = 256
        if current_value is not None and previous_value is not None and not previous_value.is_zero():
            change = _decimal_string(current_value / previous_value - Decimal(1), field="macro_change_yoy")
    return {
        "macro_level": None if current_value is None else _decimal_string(current_value, field="macro_level"),
        "macro_change_yoy": change,
    }


@dataclass(frozen=True)
class _Selection:
    security_id: str
    source: str
    series_id: str
    geography: str
    unit: str
    frequency: str
    decision_at: str
    calendar_pin: dict[str, str]
    input_available_at: str
    available_at: str
    computation_delay_seconds: int
    input_fingerprint: str
    selected_input_manifests: list[dict[str, str]]
    selected_input_parts: list[dict[str, str]]
    features: dict[str, str | None]


def _select(
    catalog: ResearchCatalog,
    *,
    decision_at: str | datetime,
    security_id: str | UUID,
    source: str,
    series_id: str,
    geography: str,
    unit: str,
    frequency: str,
    calendar_pin: Mapping[str, Any],
    computation_delay_seconds: int,
) -> _Selection:
    if not all(isinstance(item, str) and item for item in (source, series_id, geography, unit, frequency)):
        raise FeatureArtifactError("macro selectors must be non-empty strings")
    if not isinstance(computation_delay_seconds, int) or isinstance(computation_delay_seconds, bool) or computation_delay_seconds < 0:
        raise FeatureArtifactError("computation_delay_seconds must be a non-negative integer")
    canonical_decision, decision_datetime = _canonical_timestamp(decision_at, field="decision_at")
    canonical_security_id = _canonical_uuid(security_id, field="security_id")
    canonical_calendar = _canonical_calendar_pin(calendar_pin, decision_at=decision_datetime)
    try:
        selected = catalog.point_in_time_inputs(
            decision_at=canonical_decision,
            dataset="macroeconomics",
            source=source,
            series_id=series_id,
            geography=geography,
            unit=unit,
            frequency=frequency,
        )
    except Exception as error:
        if isinstance(error, FeatureArtifactError):
            raise
        raise FeatureArtifactError(f"point-in-time macro selection failed: {error}") from error
    records = [dict(record) for record in selected.frame.to_dict(orient="records")]
    if not records:
        raise FeatureArtifactError("no historical macro rows are eligible at the requested decision_at")
    for index, record in enumerate(records):
        if _record_value(record, "has_vintage_at") is not True:
            raise FeatureArtifactError(f"macro row {index} is not a historical-vintage observation")
    available_datetimes = [
        _duckdb_timestamp(_record_value(record, "available_at"), field="available_at")
        for record in records
    ]
    if any(value > decision_datetime for value in available_datetimes):
        raise FeatureArtifactError("point-in-time macro selection returned a future available_at")
    input_available_datetime = max(available_datetimes)
    _, calendar_available_datetime = _canonical_timestamp(
        canonical_calendar["calendar_available_at"], field="calendar_pin.calendar_available_at"
    )
    input_available_datetime = max(input_available_datetime, calendar_available_datetime)
    input_available_at = _canonical_timestamp(input_available_datetime, field="input_available_at")[0]
    available_at = _canonical_timestamp(
        input_available_datetime + timedelta(seconds=computation_delay_seconds), field="available_at"
    )[0]
    selected_manifests, selected_parts = _selected_lineage(catalog, records)
    input_fingerprint = compute_input_fingerprint(
        {
            "calendar_pin": canonical_calendar,
            "decision_at": canonical_decision,
            "feature_set": FEATURE_SET,
            "feature_set_version": FEATURE_SET_VERSION,
            "selected_input_manifests": selected_manifests,
            "selected_input_parts": selected_parts,
        }
    )
    return _Selection(
        security_id=canonical_security_id,
        source=source,
        series_id=series_id,
        geography=geography,
        unit=unit,
        frequency=frequency,
        decision_at=canonical_decision,
        calendar_pin=canonical_calendar,
        input_available_at=input_available_at,
        available_at=available_at,
        computation_delay_seconds=computation_delay_seconds,
        input_fingerprint=input_fingerprint,
        selected_input_manifests=selected_manifests,
        selected_input_parts=selected_parts,
        features=_compute_features(records),
    )


def compute_macro_state_features(
    catalog: ResearchCatalog,
    *,
    decision_at: str | datetime,
    security_id: str | UUID,
    calendar_pin: Mapping[str, Any],
    source: str = DEFAULT_SOURCE,
    series_id: str = DEFAULT_SERIES_ID,
    geography: str = DEFAULT_GEOGRAPHY,
    unit: str = DEFAULT_UNIT,
    frequency: str = DEFAULT_FREQUENCY,
) -> dict[str, str | None]:
    """Compute macro-state features from explicitly selected historical vintages."""
    return _select(
        catalog,
        decision_at=decision_at,
        security_id=security_id,
        source=source,
        series_id=series_id,
        geography=geography,
        unit=unit,
        frequency=frequency,
        calendar_pin=calendar_pin,
        computation_delay_seconds=0,
    ).features


def publish_macro_state(
    catalog: ResearchCatalog,
    *,
    decision_at: str | datetime,
    security_id: str | UUID,
    calendar_pin: Mapping[str, Any],
    source: str = DEFAULT_SOURCE,
    series_id: str = DEFAULT_SERIES_ID,
    geography: str = DEFAULT_GEOGRAPHY,
    unit: str = DEFAULT_UNIT,
    frequency: str = DEFAULT_FREQUENCY,
    features_root: str | Path = "data/features",
    computation_delay_seconds: int = 0,
    artifact_id: str | UUID | None = None,
    generator_version: str = DEFAULT_GENERATOR_VERSION,
    git_commit: str = "unknown",
    created_at: str | None = None,
) -> Path:
    """Publish one immutable deterministic macro-state artifact."""
    selection = _select(
        catalog,
        decision_at=decision_at,
        security_id=security_id,
        source=source,
        series_id=series_id,
        geography=geography,
        unit=unit,
        frequency=frequency,
        calendar_pin=calendar_pin,
        computation_delay_seconds=computation_delay_seconds,
    )
    return publish_derived_artifact(
        contract=MACRO_STATE_CONTRACT,
        identity={
            "security_id": selection.security_id,
            "source": selection.source,
            "series_id": selection.series_id,
            "geography": selection.geography,
            "unit": selection.unit,
            "frequency": selection.frequency,
        },
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
    )


def read_macro_state_artifact(manifest_path: str | Path):
    """Read one strict macro-state artifact."""
    return read_derived_artifact(manifest_path, contract=MACRO_STATE_CONTRACT)


def validate_macro_state_artifact(manifest_path: str | Path):
    """Validate one strict macro-state artifact."""
    return read_macro_state_artifact(manifest_path)


__all__ = [
    "DEFAULT_FREQUENCY",
    "DEFAULT_GENERATOR_VERSION",
    "DEFAULT_GEOGRAPHY",
    "DEFAULT_SERIES_ID",
    "DEFAULT_SOURCE",
    "DEFAULT_UNIT",
    "FEATURE_NAMES",
    "FEATURE_SET",
    "FEATURE_SET_VERSION",
    "compute_macro_state_features",
    "publish_macro_state",
    "read_macro_state_artifact",
    "validate_macro_state_artifact",
]
