"""Strict resolver for the checked-in versioned feature-set registry."""

from __future__ import annotations

import hashlib
import json
import re
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Final

_REGISTRY_VERSION: Final[str] = "1.0.0"
_VERSION_PATTERN: Final[re.Pattern[str]] = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+$")
_IDENTIFIER_PATTERN: Final[re.Pattern[str]] = re.compile(r"^[a-z][a-z0-9_-]{1,63}$")
_FIELD_PATTERN: Final[re.Pattern[str]] = re.compile(r"^[a-z][a-z0-9_]{0,63}$")
_FITNESS_VALUES: Final[frozenset[str]] = frozenset(
    {
        "backtest_safe",
        "current_research_only",
        "installation_replay_only",
        "unsupported",
    }
)
_AVAILABILITY_POLICIES: Final[frozenset[str]] = frozenset(
    {
        "exact_publication",
        "source_declared",
        "conservative_receipt_time",
        "current_snapshot",
        "unknown",
    }
)

_REGISTRY_FIELDS = frozenset({"registry_version", "entries"})
_FEATURE_SET_FIELDS = frozenset(
    {
        "feature_set",
        "feature_set_version",
        "description",
        "entity_type",
        "decision_frequency",
        "lookback",
        "inputs",
        "calendar",
        "null_policy",
        "computation",
        "outputs",
        "generator",
    }
)
_LOOKBACK_FIELDS = frozenset({"unit", "minimum_observations", "warmup_policy"})
_INPUT_FIELDS = frozenset(
    {
        "dataset",
        "contract_version",
        "required_fields",
        "selection_mode",
        "selection_policy",
        "availability_field",
        "observed_field",
        "historical_fitness",
        "availability_policy",
    }
)
_CALENDAR_FIELDS = frozenset(
    {"required", "scope", "pin_required", "decision_clock_policy"}
)
_NULL_POLICY_FIELDS = frozenset({"mode", "reason_vocabulary"})
_COMPUTATION_FIELDS = frozenset({"delay_seconds", "availability_rule"})
_OUTPUT_FIELDS = frozenset({"name", "value_type", "nullable", "description"})
_GENERATOR_FIELDS = frozenset({"version", "implementation"})


class FeatureRegistryError(ValueError):
    """Raised when the reviewed feature registry is invalid or unsupported."""


@dataclass(frozen=True)
class InputRequirement:
    dataset: str
    contract_version: str
    required_fields: tuple[str, ...]
    selection_mode: str
    selection_policy: str
    availability_field: str
    observed_field: str | None
    historical_fitness: str
    availability_policy: str


@dataclass(frozen=True)
class LookbackPolicy:
    unit: str
    minimum_observations: int
    warmup_policy: str


@dataclass(frozen=True)
class CalendarPolicy:
    required: bool
    scope: str
    pin_required: bool
    decision_clock_policy: str


@dataclass(frozen=True)
class NullPolicy:
    mode: str
    reason_vocabulary: tuple[str, ...]


@dataclass(frozen=True)
class ComputationPolicy:
    delay_seconds: int
    availability_rule: str


@dataclass(frozen=True)
class FeatureOutput:
    name: str
    value_type: str
    nullable: bool
    description: str


@dataclass(frozen=True)
class GeneratorContract:
    version: str
    implementation: str


@dataclass(frozen=True)
class FeatureSetDefinition:
    feature_set: str
    feature_set_version: str
    description: str
    entity_type: str
    decision_frequency: str
    lookback: LookbackPolicy
    inputs: tuple[InputRequirement, ...]
    calendar: CalendarPolicy
    null_policy: NullPolicy
    computation: ComputationPolicy
    outputs: tuple[FeatureOutput, ...]
    generator: GeneratorContract

    @property
    def feature_names(self) -> tuple[str, ...]:
        """Return the output names in the reviewed contract order."""
        return tuple(output.name for output in self.outputs)

    @property
    def required_datasets(self) -> tuple[str, ...]:
        """Return required input datasets in registry order."""
        return tuple(item.dataset for item in self.inputs)


@dataclass(frozen=True)
class FeatureRegistry:
    """An immutable in-memory view of one exact registry document."""

    path: Path
    registry_version: str
    definitions: tuple[FeatureSetDefinition, ...]
    registry_sha256: str

    def resolve(self, feature_set: str, feature_set_version: str) -> FeatureSetDefinition:
        """Resolve a feature set by exact name and version."""
        for definition in self.definitions:
            if (
                definition.feature_set == feature_set
                and definition.feature_set_version == feature_set_version
            ):
                return definition
        raise FeatureRegistryError(
            f"unsupported feature set {feature_set!r} version {feature_set_version!r}"
        )

    def require(self, feature_set: str, feature_set_version: str) -> FeatureSetDefinition:
        """Explicit alias for callers that treat registry resolution as a gate."""
        return self.resolve(feature_set, feature_set_version)


def _json_object_without_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    document: dict[str, Any] = {}
    for key, value in pairs:
        if key in document:
            raise FeatureRegistryError(f"duplicate JSON key {key!r}")
        document[key] = value
    return document


def _read_document(path: Path) -> tuple[dict[str, Any], bytes]:
    try:
        content = path.read_bytes()
    except OSError as error:
        raise FeatureRegistryError(f"cannot read feature registry {path}: {error}") from error
    try:
        document = json.loads(
            content.decode("utf-8"),
            object_pairs_hook=_json_object_without_duplicates,
            parse_constant=_reject_json_constant,
        )
    except FeatureRegistryError:
        raise
    except (UnicodeError, ValueError) as error:
        raise FeatureRegistryError(f"invalid feature registry {path}: {error}") from error
    if not isinstance(document, dict):
        raise FeatureRegistryError(f"invalid feature registry {path}: expected a JSON object")
    return document, content


def _reject_json_constant(value: str) -> Any:
    raise FeatureRegistryError(f"invalid JSON constant {value}")


def _fields(value: Any, expected: frozenset[str], *, label: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise FeatureRegistryError(f"{label} must be an object")
    missing = sorted(expected - value.keys())
    unknown = sorted(value.keys() - expected)
    if missing or unknown:
        details: list[str] = []
        if missing:
            details.append(f"missing field(s): {', '.join(missing)}")
        if unknown:
            details.append(f"unknown field(s): {', '.join(unknown)}")
        raise FeatureRegistryError(f"{label} has {'; '.join(details)}")
    return value


def _string(value: Any, *, label: str, pattern: re.Pattern[str] | None = None) -> str:
    if not isinstance(value, str) or not value:
        raise FeatureRegistryError(f"{label} must be a non-empty string")
    if pattern is not None and pattern.fullmatch(value) is None:
        raise FeatureRegistryError(f"{label} has an invalid value {value!r}")
    return value


def _enum(value: Any, allowed: frozenset[str], *, label: str) -> str:
    text = _string(value, label=label)
    if text not in allowed:
        choices = ", ".join(sorted(allowed))
        raise FeatureRegistryError(f"{label} {text!r} is unsupported; use one of {choices}")
    return text


def _bool(value: Any, *, label: str) -> bool:
    if not isinstance(value, bool):
        raise FeatureRegistryError(f"{label} must be a boolean")
    return value


def _positive_int(value: Any, *, label: str) -> int:
    if not isinstance(value, int) or isinstance(value, bool) or value < 1:
        raise FeatureRegistryError(f"{label} must be a positive integer")
    return value


def _nonnegative_int(value: Any, *, label: str) -> int:
    if not isinstance(value, int) or isinstance(value, bool) or value < 0:
        raise FeatureRegistryError(f"{label} must be a non-negative integer")
    return value


def _unique_strings(
    value: Any,
    *,
    label: str,
    pattern: re.Pattern[str],
) -> tuple[str, ...]:
    if not isinstance(value, list) or not value:
        raise FeatureRegistryError(f"{label} must be a non-empty array")
    result: list[str] = []
    for index, item in enumerate(value):
        result.append(_string(item, label=f"{label}[{index}]", pattern=pattern))
    if len(set(result)) != len(result):
        raise FeatureRegistryError(f"{label} must contain unique values")
    return tuple(result)


def _lookback(value: Any, *, label: str) -> LookbackPolicy:
    data = _fields(value, _LOOKBACK_FIELDS, label=label)
    unit = _enum(
        data["unit"],
        frozenset({"observations", "sessions", "calendar_periods"}),
        label=f"{label}.unit",
    )
    warmup_policy = _enum(
        data["warmup_policy"],
        frozenset({"null_until_available", "reject_until_available"}),
        label=f"{label}.warmup_policy",
    )
    return LookbackPolicy(
        unit=unit,
        minimum_observations=_positive_int(
            data["minimum_observations"], label=f"{label}.minimum_observations"
        ),
        warmup_policy=warmup_policy,
    )


def _input(value: Any, *, label: str) -> InputRequirement:
    data = _fields(value, _INPUT_FIELDS, label=label)
    dataset = _string(data["dataset"], label=f"{label}.dataset", pattern=_IDENTIFIER_PATTERN)
    contract_version = _string(
        data["contract_version"], label=f"{label}.contract_version", pattern=_VERSION_PATTERN
    )
    selection_mode = data["selection_mode"]
    if selection_mode != "point_in_time":
        raise FeatureRegistryError(f"{label}.selection_mode must be 'point_in_time'")
    selection_policy = _enum(
        data["selection_policy"],
        frozenset({"available_at_and_observed_at", "available_at_only"}),
        label=f"{label}.selection_policy",
    )
    availability_field = data["availability_field"]
    if availability_field != "available_at":
        raise FeatureRegistryError(f"{label}.availability_field must be 'available_at'")
    observed_field = data["observed_field"]
    if observed_field not in {None, "observed_at"}:
        raise FeatureRegistryError(f"{label}.observed_field must be 'observed_at' or null")
    return InputRequirement(
        dataset=dataset,
        contract_version=contract_version,
        required_fields=_unique_strings(
            data["required_fields"], label=f"{label}.required_fields", pattern=_FIELD_PATTERN
        ),
        selection_mode=selection_mode,
        selection_policy=selection_policy,
        availability_field=availability_field,
        observed_field=observed_field,
        historical_fitness=_enum(
            data["historical_fitness"], _FITNESS_VALUES, label=f"{label}.historical_fitness"
        ),
        availability_policy=_enum(
            data["availability_policy"],
            _AVAILABILITY_POLICIES,
            label=f"{label}.availability_policy",
        ),
    )


def _calendar(value: Any, *, label: str) -> CalendarPolicy:
    data = _fields(value, _CALENDAR_FIELDS, label=label)
    required = _bool(data["required"], label=f"{label}.required")
    pin_required = _bool(data["pin_required"], label=f"{label}.pin_required")
    scope = _enum(
        data["scope"], frozenset({"exchange", "issuer", "none"}), label=f"{label}.scope"
    )
    policy = _enum(
        data["decision_clock_policy"],
        frozenset({"after_close_next_session", "none"}),
        label=f"{label}.decision_clock_policy",
    )
    if required != pin_required:
        raise FeatureRegistryError(f"{label}.pin_required must match required")
    if required and scope == "none":
        raise FeatureRegistryError(f"{label}.required cannot use scope 'none'")
    if not required and policy != "none":
        raise FeatureRegistryError(f"{label}.non-required calendar must use clock policy 'none'")
    if required and policy == "none":
        raise FeatureRegistryError(f"{label}.required calendar needs a decision clock policy")
    return CalendarPolicy(required, scope, pin_required, policy)


def _null_policy(value: Any, *, label: str) -> NullPolicy:
    data = _fields(value, _NULL_POLICY_FIELDS, label=label)
    mode = _enum(
        data["mode"],
        frozenset({"typed_null", "reject_row", "reject_entity"}),
        label=f"{label}.mode",
    )
    return NullPolicy(
        mode=mode,
        reason_vocabulary=_unique_strings(
            data["reason_vocabulary"], label=f"{label}.reason_vocabulary", pattern=_IDENTIFIER_PATTERN
        ),
    )


def _computation(value: Any, *, label: str) -> ComputationPolicy:
    data = _fields(value, _COMPUTATION_FIELDS, label=label)
    rule = data["availability_rule"]
    if rule != "max_selected_input_available_at_plus_delay":
        raise FeatureRegistryError(
            f"{label}.availability_rule must be 'max_selected_input_available_at_plus_delay'"
        )
    return ComputationPolicy(
        delay_seconds=_nonnegative_int(data["delay_seconds"], label=f"{label}.delay_seconds"),
        availability_rule=rule,
    )


def _outputs(value: Any, *, label: str) -> tuple[FeatureOutput, ...]:
    if not isinstance(value, list) or not value:
        raise FeatureRegistryError(f"{label} must be a non-empty array")
    outputs: list[FeatureOutput] = []
    names: set[str] = set()
    value_types = frozenset({"decimal_string", "decimal_string_or_null", "boolean", "string"})
    for index, item in enumerate(value):
        data = _fields(item, _OUTPUT_FIELDS, label=f"{label}[{index}]")
        name = _string(data["name"], label=f"{label}[{index}].name", pattern=_FIELD_PATTERN)
        if name in names:
            raise FeatureRegistryError(f"{label} contains duplicate output {name!r}")
        names.add(name)
        value_type = _enum(data["value_type"], value_types, label=f"{label}[{index}].value_type")
        nullable = _bool(data["nullable"], label=f"{label}[{index}].nullable")
        if nullable != (value_type.endswith("_or_null")):
            raise FeatureRegistryError(
                f"{label}[{index}] nullable must match value_type {value_type!r}"
            )
        outputs.append(
            FeatureOutput(
                name=name,
                value_type=value_type,
                nullable=nullable,
                description=_string(data["description"], label=f"{label}[{index}].description"),
            )
        )
    return tuple(outputs)


def _generator(value: Any, *, label: str) -> GeneratorContract:
    data = _fields(value, _GENERATOR_FIELDS, label=label)
    return GeneratorContract(
        version=_string(data["version"], label=f"{label}.version"),
        implementation=_string(data["implementation"], label=f"{label}.implementation"),
    )


def _definition(value: Any, *, label: str) -> FeatureSetDefinition:
    data = _fields(value, _FEATURE_SET_FIELDS, label=label)
    inputs_value = data["inputs"]
    if not isinstance(inputs_value, list) or not inputs_value:
        raise FeatureRegistryError(f"{label}.inputs must be a non-empty array")
    inputs = tuple(_input(item, label=f"{label}.inputs[{index}]") for index, item in enumerate(inputs_value))
    datasets = [item.dataset for item in inputs]
    if len(set(datasets)) != len(datasets):
        raise FeatureRegistryError(f"{label}.inputs must contain one requirement per dataset")
    return FeatureSetDefinition(
        feature_set=_string(data["feature_set"], label=f"{label}.feature_set", pattern=_IDENTIFIER_PATTERN),
        feature_set_version=_string(
            data["feature_set_version"],
            label=f"{label}.feature_set_version",
            pattern=_VERSION_PATTERN,
        ),
        description=_string(data["description"], label=f"{label}.description"),
        entity_type=_enum(
            data["entity_type"],
            frozenset({"security", "issuer", "macro_series", "cross_asset"}),
            label=f"{label}.entity_type",
        ),
        decision_frequency=_enum(
            data["decision_frequency"],
            frozenset({"daily", "weekly", "monthly"}),
            label=f"{label}.decision_frequency",
        ),
        lookback=_lookback(data["lookback"], label=f"{label}.lookback"),
        inputs=inputs,
        calendar=_calendar(data["calendar"], label=f"{label}.calendar"),
        null_policy=_null_policy(data["null_policy"], label=f"{label}.null_policy"),
        computation=_computation(data["computation"], label=f"{label}.computation"),
        outputs=_outputs(data["outputs"], label=f"{label}.outputs"),
        generator=_generator(data["generator"], label=f"{label}.generator"),
    )


def load_feature_registry(path: str | Path) -> FeatureRegistry:
    """Load and validate one exact checked-in feature registry document."""
    registry_path = Path(path).expanduser().resolve()
    document, content = _read_document(registry_path)
    data = _fields(document, _REGISTRY_FIELDS, label="feature registry")
    if data["registry_version"] != _REGISTRY_VERSION:
        raise FeatureRegistryError(
            f"registry_version must be {_REGISTRY_VERSION!r}; got {data['registry_version']!r}"
        )
    entries = data["entries"]
    if not isinstance(entries, list) or not entries:
        raise FeatureRegistryError("feature registry entries must be a non-empty array")
    definitions = tuple(
        _definition(item, label=f"feature registry entries[{index}]")
        for index, item in enumerate(entries)
    )
    identities = [(item.feature_set, item.feature_set_version) for item in definitions]
    if len(set(identities)) != len(identities):
        raise FeatureRegistryError("feature registry contains duplicate feature-set identities")
    if identities != sorted(identities):
        raise FeatureRegistryError("feature registry entries must be sorted by feature set and version")
    return FeatureRegistry(
        path=registry_path,
        registry_version=data["registry_version"],
        definitions=definitions,
        registry_sha256=hashlib.sha256(content).hexdigest(),
    )


__all__ = [
    "CalendarPolicy",
    "ComputationPolicy",
    "FeatureOutput",
    "FeatureRegistry",
    "FeatureRegistryError",
    "FeatureSetDefinition",
    "GeneratorContract",
    "InputRequirement",
    "LookbackPolicy",
    "NullPolicy",
    "load_feature_registry",
]
