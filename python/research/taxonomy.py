"""Strict, versioned source-to-canonical mappings for feature inputs."""

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
_DATE_PATTERN: Final[re.Pattern[str]] = re.compile(r"^[0-9]{4}-[0-9]{2}-[0-9]{2}$")

_REGISTRY_FIELDS = frozenset({"registry_version", "entries"})
_ENTRY_FIELDS = frozenset(
    {
        "feature_set",
        "feature_set_version",
        "source",
        "taxonomy",
        "source_concept",
        "canonical_concept",
        "unit",
        "currency",
        "period_type",
        "fiscal_periods",
        "scope",
        "sign",
        "review_status",
        "reviewed_at",
        "review_note",
    }
)


class TaxonomyRegistryError(ValueError):
    """Raised when a reviewed taxonomy registry is invalid or unsupported."""


@dataclass(frozen=True)
class TaxonomyMapping:
    feature_set: str
    feature_set_version: str
    source: str
    taxonomy: str
    source_concept: str
    canonical_concept: str
    unit: str
    currency: str
    period_type: str
    fiscal_periods: tuple[str, ...]
    scope: str
    sign: str
    review_status: str
    reviewed_at: str
    review_note: str

    @property
    def mapping_id(self) -> str:
        """Return the stable human-readable mapping identity."""
        return f"{self.source}:{self.taxonomy}:{self.source_concept}->{self.canonical_concept}"


@dataclass(frozen=True)
class TaxonomyRegistry:
    """An immutable in-memory view of one exact mapping document."""

    path: Path
    registry_version: str
    mappings: tuple[TaxonomyMapping, ...]
    registry_sha256: str

    def resolve(
        self,
        feature_set: str,
        feature_set_version: str,
        canonical_concept: str,
    ) -> TaxonomyMapping:
        """Resolve one mapping by exact feature and canonical concept identity."""
        matches = tuple(
            item
            for item in self.mappings
            if (
                item.feature_set == feature_set
                and item.feature_set_version == feature_set_version
                and item.canonical_concept == canonical_concept
            )
        )
        if len(matches) != 1:
            raise TaxonomyRegistryError(
                f"expected one mapping for {feature_set!r} {feature_set_version!r} "
                f"{canonical_concept!r}; found {len(matches)}"
            )
        return matches[0]

    def for_feature_set(
        self, feature_set: str, feature_set_version: str
    ) -> tuple[TaxonomyMapping, ...]:
        """Return mappings for one exact feature-set identity in registry order."""
        return tuple(
            item
            for item in self.mappings
            if item.feature_set == feature_set and item.feature_set_version == feature_set_version
        )


def _reject_json_constant(value: str) -> Any:
    raise TaxonomyRegistryError(f"invalid JSON constant {value}")


def _object_without_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    document: dict[str, Any] = {}
    for key, value in pairs:
        if key in document:
            raise TaxonomyRegistryError(f"duplicate JSON key {key!r}")
        document[key] = value
    return document


def _fields(value: Any, expected: frozenset[str], *, label: str) -> dict[str, Any]:
    if not isinstance(value, dict) or set(value) != expected:
        raise TaxonomyRegistryError(f"{label} has missing or unknown fields")
    return value


def _string(value: Any, *, label: str, pattern: re.Pattern[str] | None = None) -> str:
    if not isinstance(value, str) or not value:
        raise TaxonomyRegistryError(f"{label} must be a non-empty string")
    if pattern is not None and pattern.fullmatch(value) is None:
        raise TaxonomyRegistryError(f"{label} has an invalid value {value!r}")
    return value


def _enum(value: Any, allowed: frozenset[str], *, label: str) -> str:
    text = _string(value, label=label)
    if text not in allowed:
        raise TaxonomyRegistryError(f"{label} {text!r} is unsupported")
    return text


def _periods(value: Any, *, label: str) -> tuple[str, ...]:
    if not isinstance(value, list) or not value:
        raise TaxonomyRegistryError(f"{label} must be a non-empty array")
    allowed = frozenset({"FY", "Q1", "Q2", "Q3", "Q4", "H1", "H2", "YTD", "instant", "other"})
    result = tuple(_enum(item, allowed, label=f"{label}[{index}]") for index, item in enumerate(value))
    if len(set(result)) != len(result):
        raise TaxonomyRegistryError(f"{label} must contain unique values")
    return result


def _mapping(value: Any, *, label: str) -> TaxonomyMapping:
    data = _fields(value, _ENTRY_FIELDS, label=label)
    period_type = _enum(data["period_type"], frozenset({"instant", "duration"}), label=f"{label}.period_type")
    fiscal_periods = _periods(data["fiscal_periods"], label=f"{label}.fiscal_periods")
    if period_type == "instant" and fiscal_periods != ("instant",):
        raise TaxonomyRegistryError(f"{label}.instant mappings must use fiscal_periods ['instant']")
    reviewed_at = _string(data["reviewed_at"], label=f"{label}.reviewed_at", pattern=_DATE_PATTERN)
    return TaxonomyMapping(
        feature_set=_string(data["feature_set"], label=f"{label}.feature_set", pattern=_IDENTIFIER_PATTERN),
        feature_set_version=_string(
            data["feature_set_version"], label=f"{label}.feature_set_version", pattern=_VERSION_PATTERN
        ),
        source=_string(data["source"], label=f"{label}.source", pattern=_IDENTIFIER_PATTERN),
        taxonomy=_string(data["taxonomy"], label=f"{label}.taxonomy"),
        source_concept=_string(data["source_concept"], label=f"{label}.source_concept"),
        canonical_concept=_string(
            data["canonical_concept"], label=f"{label}.canonical_concept", pattern=_IDENTIFIER_PATTERN
        ),
        unit=_string(data["unit"], label=f"{label}.unit"),
        currency=_string(data["currency"], label=f"{label}.currency"),
        period_type=period_type,
        fiscal_periods=fiscal_periods,
        scope=_enum(
            data["scope"],
            frozenset({"consolidated", "parent_only", "segment", "unknown"}),
            label=f"{label}.scope",
        ),
        sign=_enum(data["sign"], frozenset({"reported", "negated"}), label=f"{label}.sign"),
        review_status=_enum(data["review_status"], frozenset({"reviewed"}), label=f"{label}.review_status"),
        reviewed_at=reviewed_at,
        review_note=_string(data["review_note"], label=f"{label}.review_note"),
    )


def load_taxonomy_registry(path: str | Path) -> TaxonomyRegistry:
    """Load one strict, checked-in taxonomy mapping registry."""
    registry_path = Path(path).expanduser().resolve()
    try:
        content = registry_path.read_bytes()
    except OSError as error:
        raise TaxonomyRegistryError(f"cannot read taxonomy registry {registry_path}: {error}") from error
    try:
        document = json.loads(
            content.decode("utf-8"),
            object_pairs_hook=_object_without_duplicates,
            parse_constant=_reject_json_constant,
        )
    except TaxonomyRegistryError:
        raise
    except (UnicodeError, ValueError) as error:
        raise TaxonomyRegistryError(f"invalid taxonomy registry {registry_path}: {error}") from error
    data = _fields(document, _REGISTRY_FIELDS, label="taxonomy registry")
    if data["registry_version"] != _REGISTRY_VERSION:
        raise TaxonomyRegistryError(
            f"taxonomy registry_version must be {_REGISTRY_VERSION!r}"
        )
    entries = data["entries"]
    if not isinstance(entries, list) or not entries:
        raise TaxonomyRegistryError("taxonomy registry entries must be a non-empty array")
    mappings = tuple(
        _mapping(item, label=f"taxonomy registry entries[{index}]")
        for index, item in enumerate(entries)
    )
    identities = [
        (item.feature_set, item.feature_set_version, item.canonical_concept)
        for item in mappings
    ]
    if len(set(identities)) != len(identities):
        raise TaxonomyRegistryError("taxonomy registry contains duplicate mapping identities")
    if identities != sorted(identities):
        raise TaxonomyRegistryError(
            "taxonomy registry entries must be sorted by feature set, version, and canonical concept"
        )
    return TaxonomyRegistry(
        path=registry_path,
        registry_version=data["registry_version"],
        mappings=mappings,
        registry_sha256=hashlib.sha256(content).hexdigest(),
    )


__all__ = [
    "TaxonomyMapping",
    "TaxonomyRegistry",
    "TaxonomyRegistryError",
    "load_taxonomy_registry",
]
