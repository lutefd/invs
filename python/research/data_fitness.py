"""Fail-closed validation for the release data-fitness matrix."""

from __future__ import annotations

import json
import re
from collections.abc import Mapping
from datetime import date
from pathlib import Path
from typing import Any, Final

MATRIX_VERSION: Final[str] = "1.0.0"
RELEASE_VERSION: Final[str] = "1.0.0"

CATALOG_DATASETS: Final[frozenset[str]] = frozenset(
    {"prices", "fundamentals", "macroeconomics", "filings", "fx"}
)
BACKTEST_INPUT_KINDS: Final[frozenset[str]] = frozenset(
    {"prices", "calendar", "membership", "corporate_actions", "fx", "feature", "macro", "risk_free"}
)
FEATURE_SETS: Final[frozenset[str]] = frozenset(
    {"market-basic", "market-momentum", "fundamental-growth", "macro-state"}
)
WORKFLOW_EVIDENCE: Final[frozenset[str]] = frozenset({"commodity"})
FITNESS_VALUES: Final[frozenset[str]] = frozenset(
    {"backtest_safe", "current_research_only", "installation_replay_only", "unsupported"}
)
_SHA256 = re.compile(r"^[0-9a-f]{64}$")
_COMMIT = re.compile(r"^[0-9a-f]{40}$")
_SAFE_PATH = re.compile(r"^(?!/)(?!.*(^|/)\.\.(/|$)).+")


class DataFitnessError(ValueError):
    """Raised when the release data-fitness matrix is incomplete or unsafe."""


def _strict_json(path: Path) -> dict[str, Any]:
    def no_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for key, value in pairs:
            if key in result:
                raise ValueError(f"duplicate JSON key {key!r}")
            result[key] = value
        return result

    try:
        value = json.loads(
            path.read_text(encoding="utf-8"),
            object_pairs_hook=no_duplicates,
            parse_constant=lambda constant: (_ for _ in ()).throw(
                ValueError(f"invalid JSON constant {constant}")
            ),
        )
    except (OSError, UnicodeError, ValueError) as error:
        raise DataFitnessError(f"invalid data-fitness matrix {path}: {error}") from error
    if not isinstance(value, dict):
        raise DataFitnessError(f"data-fitness matrix {path} must be an object")
    return value


def _keys(value: Any, expected: set[str], *, field: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise DataFitnessError(f"{field} must be an object")
    actual = set(value)
    missing = sorted(expected - actual)
    unknown = sorted(actual - expected)
    if missing:
        raise DataFitnessError(f"{field} is missing fields: {', '.join(missing)}")
    if unknown:
        raise DataFitnessError(f"{field} contains unknown fields: {', '.join(unknown)}")
    return value


def _nonempty(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not value:
        raise DataFitnessError(f"{field} must be a non-empty string")
    return value


def _list(value: Any, *, field: str, min_items: int = 0) -> list[Any]:
    if not isinstance(value, list) or len(value) < min_items:
        raise DataFitnessError(f"{field} must be an array with at least {min_items} item(s)")
    return value


def _unique_strings(value: Any, *, field: str, min_items: int = 0) -> list[str]:
    values = _list(value, field=field, min_items=min_items)
    if any(not isinstance(item, str) or not item for item in values):
        raise DataFitnessError(f"{field} must contain non-empty strings")
    if len(set(values)) != len(values):
        raise DataFitnessError(f"{field} must not contain duplicates")
    return values


def _safe_repo_file(repo_root: Path, value: Any, *, field: str) -> Path:
    relative = _nonempty(value, field=field)
    if not _SAFE_PATH.fullmatch(relative) or "\x00" in relative:
        raise DataFitnessError(f"{field} must be a safe repository-relative path")
    path = (repo_root / relative).resolve()
    try:
        path.relative_to(repo_root)
    except ValueError as error:
        raise DataFitnessError(f"{field} escapes the repository root") from error
    if not path.is_file():
        raise DataFitnessError(f"{field} does not identify a file: {relative}")
    return path


def _date_or_null(value: Any, *, field: str) -> None:
    if value is None:
        return
    if not isinstance(value, str):
        raise DataFitnessError(f"{field} must be an ISO date or null")
    try:
        date.fromisoformat(value)
    except ValueError as error:
        raise DataFitnessError(f"{field} must be an ISO date or null") from error


def _validate_evidence(values: Any, *, repo_root: Path, field: str) -> None:
    entries = _list(values, field=field, min_items=1)
    for index, raw in enumerate(entries):
        evidence = _keys(
            raw,
            {"kind", "path", "commit", "status"},
            field=f"{field}[{index}]",
        )
        if evidence["kind"] not in {"implementation", "acceptance", "fixture", "limitation"}:
            raise DataFitnessError(f"{field}[{index}].kind is unsupported")
        _safe_repo_file(repo_root, evidence["path"], field=f"{field}[{index}].path")
        commit = evidence["commit"]
        if commit is not None and (not isinstance(commit, str) or not _COMMIT.fullmatch(commit)):
            raise DataFitnessError(f"{field}[{index}].commit must be a full lowercase commit or null")
        if evidence["status"] not in {"accepted", "bounded", "blocked", "ready"}:
            raise DataFitnessError(f"{field}[{index}].status is unsupported")


def _validate_entry(raw: Any, *, index: int, repo_root: Path, source_codes: set[str]) -> dict[str, Any]:
    field = f"entries[{index}]"
    entry = _keys(
        raw,
        {
            "id",
            "dataset",
            "source",
            "contract_version",
            "feature_set",
            "authority",
            "access",
            "coverage",
            "time_semantics",
            "revision_policy",
            "historical_fitness",
            "identity_policy",
            "quality_checks",
            "evidence",
            "consumers",
            "backtest_input_kinds",
            "notes",
        },
        field=field,
    )
    entry_id = _nonempty(entry["id"], field=f"{field}.id")
    if not re.fullmatch(r"^[a-z][a-z0-9_-]{1,63}$", entry_id):
        raise DataFitnessError(f"{field}.id is not a valid slug")
    dataset = _nonempty(entry["dataset"], field=f"{field}.dataset")
    source = _nonempty(entry["source"], field=f"{field}.source")
    source_codes.add(source)
    contract_version = _nonempty(entry["contract_version"], field=f"{field}.contract_version")
    if not re.fullmatch(r"^[0-9]+\.[0-9]+\.[0-9]+$", contract_version):
        raise DataFitnessError(f"{field}.contract_version must be semantic version x.y.z")
    feature_set = entry["feature_set"]
    if feature_set is not None and (not isinstance(feature_set, str) or not feature_set):
        raise DataFitnessError(f"{field}.feature_set must be a non-empty string or null")

    authority = _keys(
        entry["authority"], {"name", "reference", "access_status"}, field=f"{field}.authority"
    )
    for key in ("name", "reference"):
        _nonempty(authority[key], field=f"{field}.authority.{key}")
    if authority["access_status"] not in {"accepted", "bounded", "review_required", "not_admitted", "fixture_only"}:
        raise DataFitnessError(f"{field}.authority.access_status is unsupported")

    access = _keys(entry["access"], {"mode", "credentials", "unattended"}, field=f"{field}.access")
    if access["mode"] not in {"api", "public_file", "contracted_product", "manual", "fixture"}:
        raise DataFitnessError(f"{field}.access.mode is unsupported")
    if access["credentials"] not in {"none", "optional", "required", "not_admitted"}:
        raise DataFitnessError(f"{field}.access.credentials is unsupported")
    if not isinstance(access["unattended"], bool):
        raise DataFitnessError(f"{field}.access.unattended must be boolean")

    coverage = _keys(
        entry["coverage"],
        {"regions", "entities", "fields", "start", "end", "frequency", "known_gaps"},
        field=f"{field}.coverage",
    )
    _unique_strings(coverage["regions"], field=f"{field}.coverage.regions", min_items=1)
    _nonempty(coverage["entities"], field=f"{field}.coverage.entities")
    _unique_strings(coverage["fields"], field=f"{field}.coverage.fields", min_items=1)
    _date_or_null(coverage["start"], field=f"{field}.coverage.start")
    _date_or_null(coverage["end"], field=f"{field}.coverage.end")
    if coverage["start"] is not None and coverage["end"] is not None and coverage["start"] > coverage["end"]:
        raise DataFitnessError(f"{field}.coverage dates are reversed")
    _nonempty(coverage["frequency"], field=f"{field}.coverage.frequency")
    _unique_strings(coverage["known_gaps"], field=f"{field}.coverage.known_gaps")

    semantics = _keys(
        entry["time_semantics"],
        {"observed", "published", "available", "precision", "historical_availability", "decision_rule"},
        field=f"{field}.time_semantics",
    )
    for key in ("observed", "published", "available", "decision_rule"):
        _nonempty(semantics[key], field=f"{field}.time_semantics.{key}")
    if semantics["precision"] not in {"microsecond", "second", "date", "unknown", "not_applicable"}:
        raise DataFitnessError(f"{field}.time_semantics.precision is unsupported")
    if semantics["historical_availability"] not in {
        "exact_publication", "source_declared", "installation_receipt", "not_available", "not_applicable"
    }:
        raise DataFitnessError(f"{field}.time_semantics.historical_availability is unsupported")

    if entry["revision_policy"] not in {
        "append", "replace", "current_snapshot", "installation_replay", "source_versioned", "unknown"
    }:
        raise DataFitnessError(f"{field}.revision_policy is unsupported")
    if entry["historical_fitness"] not in FITNESS_VALUES:
        raise DataFitnessError(f"{field}.historical_fitness is unsupported")

    identity = _keys(
        entry["identity_policy"],
        {"natural_key", "mapping_source", "historical_identity"},
        field=f"{field}.identity_policy",
    )
    for key in ("natural_key", "mapping_source"):
        _nonempty(identity[key], field=f"{field}.identity_policy.{key}")
    if identity["historical_identity"] not in {"versioned", "current_only", "not_applicable", "fixture_defined"}:
        raise DataFitnessError(f"{field}.identity_policy.historical_identity is unsupported")
    _unique_strings(entry["quality_checks"], field=f"{field}.quality_checks", min_items=1)
    _validate_evidence(entry["evidence"], repo_root=repo_root, field=f"{field}.evidence")
    consumers = _unique_strings(entry["consumers"], field=f"{field}.consumers", min_items=1)
    allowed_consumers = {
        "research_catalog", "historical_resolver", "feature_sets", "backtest", "paper",
        "workflow", "adjustments", "operator", "acceptance"
    }
    if not set(consumers).issubset(allowed_consumers):
        raise DataFitnessError(f"{field}.consumers contains an unsupported consumer")
    kinds = _unique_strings(entry["backtest_input_kinds"], field=f"{field}.backtest_input_kinds")
    if not set(kinds).issubset(BACKTEST_INPUT_KINDS):
        raise DataFitnessError(f"{field}.backtest_input_kinds contains an unsupported kind")
    _nonempty(entry["notes"], field=f"{field}.notes")
    if feature_set is not None and dataset != "features":
        raise DataFitnessError(f"{field}.feature_set is only allowed for features entries")
    if dataset == "features" and feature_set is None:
        raise DataFitnessError(f"{field}.feature_set is required for features entries")
    return dict(entry)


def validate_data_fitness_matrix(
    matrix_path: str | Path = "release/data-fitness.json", *, repo_root: str | Path | None = None
) -> dict[str, Any]:
    """Validate the matrix and prove every reachable input surface is classified."""

    matrix_file = Path(matrix_path).resolve()
    root = Path(repo_root).resolve() if repo_root is not None else matrix_file.parent.parent
    matrix = _strict_json(matrix_file)
    _keys(
        matrix,
        {
            "$schema",
            "registry_version",
            "release_version",
            "unknown_source_policy",
            "backtest_admission",
            "reachable_surfaces",
            "source_codes",
            "entries",
        },
        field="matrix",
    )
    if matrix["$schema"] != "../schemas/data-fitness-matrix.schema.json":
        raise DataFitnessError("matrix.$schema is not the supported data-fitness schema")
    if matrix["registry_version"] != MATRIX_VERSION:
        raise DataFitnessError(f"unsupported data-fitness registry version {matrix['registry_version']!r}")
    if matrix["release_version"] != RELEASE_VERSION:
        raise DataFitnessError(f"unsupported data-fitness release version {matrix['release_version']!r}")
    if matrix["unknown_source_policy"] != "reject_unlisted":
        raise DataFitnessError("matrix.unknown_source_policy must be reject_unlisted")
    if matrix["backtest_admission"] != "backtest_safe_only":
        raise DataFitnessError("matrix.backtest_admission must be backtest_safe_only")

    surfaces = _keys(
        matrix["reachable_surfaces"],
        {"catalog_datasets", "backtest_input_kinds", "feature_sets", "workflow_evidence"},
        field="matrix.reachable_surfaces",
    )
    catalog_datasets = set(_unique_strings(surfaces["catalog_datasets"], field="matrix.reachable_surfaces.catalog_datasets", min_items=1))
    backtest_kinds = set(_unique_strings(surfaces["backtest_input_kinds"], field="matrix.reachable_surfaces.backtest_input_kinds", min_items=1))
    feature_sets = set(_unique_strings(surfaces["feature_sets"], field="matrix.reachable_surfaces.feature_sets", min_items=1))
    workflow_evidence = set(_unique_strings(surfaces["workflow_evidence"], field="matrix.reachable_surfaces.workflow_evidence", min_items=1))
    if catalog_datasets != CATALOG_DATASETS:
        raise DataFitnessError(f"catalog dataset surface mismatch: {sorted(catalog_datasets)}")
    if backtest_kinds != BACKTEST_INPUT_KINDS:
        raise DataFitnessError(f"backtest input surface mismatch: {sorted(backtest_kinds)}")
    if feature_sets != FEATURE_SETS:
        raise DataFitnessError(f"feature-set surface mismatch: {sorted(feature_sets)}")
    if workflow_evidence != WORKFLOW_EVIDENCE:
        raise DataFitnessError(f"workflow evidence surface mismatch: {sorted(workflow_evidence)}")

    declared_sources = set(_unique_strings(matrix["source_codes"], field="matrix.source_codes", min_items=1))
    raw_entries = _list(matrix["entries"], field="matrix.entries", min_items=1)
    entries: list[dict[str, Any]] = []
    entry_ids: set[str] = set()
    source_codes: set[str] = set()
    for index, raw in enumerate(raw_entries):
        entry = _validate_entry(raw, index=index, repo_root=root, source_codes=source_codes)
        if entry["id"] in entry_ids:
            raise DataFitnessError(f"duplicate matrix entry id {entry['id']!r}")
        entry_ids.add(entry["id"])
        entries.append(entry)
    if source_codes != declared_sources:
        raise DataFitnessError("matrix.source_codes does not match entry sources")

    classified_catalog = {entry["dataset"] for entry in entries}
    if not catalog_datasets.issubset(classified_catalog):
        missing = sorted(catalog_datasets - classified_catalog)
        raise DataFitnessError(f"catalog datasets are not classified: {', '.join(missing)}")
    classified_kinds = {kind for entry in entries for kind in entry["backtest_input_kinds"]}
    if classified_kinds != BACKTEST_INPUT_KINDS:
        missing = sorted(BACKTEST_INPUT_KINDS - classified_kinds)
        extra = sorted(classified_kinds - BACKTEST_INPUT_KINDS)
        details = []
        if missing:
            details.append(f"missing {', '.join(missing)}")
        if extra:
            details.append(f"unexpected {', '.join(extra)}")
        raise DataFitnessError("backtest input classifications are incomplete: " + "; ".join(details))
    classified_features = {entry["feature_set"] for entry in entries if entry["feature_set"] is not None}
    if classified_features != FEATURE_SETS:
        raise DataFitnessError("feature-set classifications are incomplete")
    if not any(entry["dataset"] == "commodity_evidence" for entry in entries):
        raise DataFitnessError("commodity workflow evidence is not classified")
    for kind in BACKTEST_INPUT_KINDS:
        if not any(kind in entry["backtest_input_kinds"] and entry["historical_fitness"] == "backtest_safe" for entry in entries):
            raise DataFitnessError(f"no backtest_safe admission is classified for {kind}")
    return matrix


__all__ = [
    "BACKTEST_INPUT_KINDS",
    "CATALOG_DATASETS",
    "FEATURE_SETS",
    "FITNESS_VALUES",
    "MATRIX_VERSION",
    "RELEASE_VERSION",
    "DataFitnessError",
    "validate_data_fitness_matrix",
]
