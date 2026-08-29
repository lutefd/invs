"""Read-only coverage, null-quality, and lineage reports for feature batches."""

from __future__ import annotations

import hashlib
import json
import re
from collections import Counter, defaultdict
from collections.abc import Mapping
from datetime import UTC, date, datetime
from pathlib import Path
from typing import Any

import duckdb

from .batches import FeatureBatchError, read_feature_batch
from .features import FeatureArtifactError, read_feature_artifact
from .registry import FeatureRegistry, FeatureRegistryError

_SHA256_PATTERN = re.compile(r"^[0-9a-f]{64}$")
_PART_PATTERN = re.compile(r"^part-[0-9a-f]{64}\.parquet$")
_REPORT_VERSION = "1.0.0"
_DEFAULT_STALE_AFTER_SECONDS = 30 * 24 * 60 * 60


class FeatureQualityReportError(ValueError):
    """Raised when a feature-quality report cannot prove its lineage."""


def _sha256_file(path: Path, *, label: str) -> str:
    digest = hashlib.sha256()
    try:
        with path.open("rb") as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(chunk)
    except OSError as error:
        raise FeatureQualityReportError(f"cannot read {label} {path}: {error}") from error
    return digest.hexdigest()


def _strict_json(path: Path, *, label: str) -> dict[str, Any]:
    def object_without_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        value: dict[str, Any] = {}
        for key, item in pairs:
            if key in value:
                raise FeatureQualityReportError(f"duplicate JSON key {key!r} in {label} {path}")
            value[key] = item
        return value

    def reject_constant(value: str) -> Any:
        raise FeatureQualityReportError(f"invalid JSON constant {value} in {label} {path}")

    try:
        document = json.loads(
            path.read_text(encoding="utf-8"),
            object_pairs_hook=object_without_duplicates,
            parse_constant=reject_constant,
        )
    except FeatureQualityReportError:
        raise
    except (OSError, UnicodeError, ValueError) as error:
        raise FeatureQualityReportError(f"invalid {label} {path}: {error}") from error
    if not isinstance(document, dict):
        raise FeatureQualityReportError(f"{label} {path} must be a JSON object")
    return document


def _safe_relative_path(value: Any, *, label: str) -> str:
    if not isinstance(value, str) or not value or Path(value).is_absolute():
        raise FeatureQualityReportError(f"{label} must be a non-empty relative path")
    if "\x00" in value or "\\" in value or any(part in {"", ".", ".."} for part in Path(value).parts):
        raise FeatureQualityReportError(f"{label} contains an unsafe path")
    return value


def _hash_ref(value: Any, *, label: str) -> str:
    if not isinstance(value, str) or _SHA256_PATTERN.fullmatch(value) is None:
        raise FeatureQualityReportError(f"{label} must be a lower-case SHA-256")
    return value


def _quote_literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def _as_text(value: Any) -> str | None:
    if value is None:
        return None
    if hasattr(value, "isoformat"):
        return str(value.isoformat())
    return str(value)


def _input_lineage(
    child_manifest: Mapping[str, Any],
    *,
    data_root: Path,
) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    """Verify child-selected canonical files and return traceable input rows."""
    manifests: list[dict[str, str]] = []
    input_rows: list[dict[str, Any]] = []
    selected_parts = {
        (item["path"], item["sha256"])
        for item in child_manifest["selected_input_parts"]
    }
    seen_parts: set[tuple[str, str, str]] = set()
    for manifest_ref in child_manifest["selected_input_manifests"]:
        relative_manifest = _safe_relative_path(
            manifest_ref["path"], label="selected input manifest path"
        )
        manifest_sha = _hash_ref(manifest_ref["sha256"], label="selected input manifest hash")
        manifest_path = (data_root / relative_manifest).resolve()
        try:
            manifest_path.relative_to(data_root.resolve())
        except ValueError as error:
            raise FeatureQualityReportError("selected input manifest escapes data root") from error
        if not manifest_path.is_file():
            raise FeatureQualityReportError(f"selected input manifest is missing: {relative_manifest}")
        if _sha256_file(manifest_path, label="selected input manifest") != manifest_sha:
            raise FeatureQualityReportError(f"selected input manifest hash mismatch: {relative_manifest}")
        normalized_manifest = _strict_json(manifest_path, label="selected input manifest")
        parts = normalized_manifest.get("parts")
        if not isinstance(parts, list):
            raise FeatureQualityReportError(f"selected input manifest has no parts list: {relative_manifest}")
        manifests.append({"path": relative_manifest, "sha256": manifest_sha})
        for part in parts:
            if not isinstance(part, dict):
                raise FeatureQualityReportError(f"selected input manifest has an invalid part: {relative_manifest}")
            part_name = part.get("path")
            part_sha = part.get("sha256")
            if not isinstance(part_name, str) or _PART_PATTERN.fullmatch(part_name) is None:
                raise FeatureQualityReportError(f"selected input manifest has a non-content-named part: {relative_manifest}")
            if not isinstance(part_sha, str) or part_name != f"part-{part_sha}.parquet" or _SHA256_PATTERN.fullmatch(part_sha) is None:
                raise FeatureQualityReportError(f"selected input manifest has an invalid part hash: {relative_manifest}")
            if (part_name, part_sha) not in selected_parts:
                continue
            part_path = manifest_path.parent / part_name
            if not part_path.is_file():
                raise FeatureQualityReportError(f"selected input part is missing: {part_path}")
            if _sha256_file(part_path, label="selected input part") != part_sha:
                raise FeatureQualityReportError(f"selected input part hash mismatch: {part_path}")
            part_key = (relative_manifest, part_name, part_sha)
            if part_key in seen_parts:
                continue
            seen_parts.add(part_key)
            connection = None
            try:
                connection = duckdb.connect(":memory:")
                rows = connection.execute(
                    f"SELECT * FROM read_parquet({_quote_literal(str(part_path))}, hive_partitioning=false)"
                ).fetchall()
                columns = [item[0] for item in connection.description]
            except duckdb.Error as error:
                raise FeatureQualityReportError(f"cannot read selected input part {part_path}: {error}") from error
            finally:
                if connection is not None:
                    connection.close()
            for row in rows:
                input_rows.append(
                    {
                        "source": _as_text(row[columns.index("source")]) if "source" in columns else None,
                        "concept": _as_text(row[columns.index("concept")]) if "concept" in columns else None,
                        "series_id": _as_text(row[columns.index("series_id")]) if "series_id" in columns else None,
                        "observed_at": _as_text(row[columns.index("observed_at")]) if "observed_at" in columns else None,
                        "available_at": _as_text(row[columns.index("available_at")]) if "available_at" in columns else None,
                        "period_end": _as_text(row[columns.index("period_end")]) if "period_end" in columns else None,
                        "fiscal_period": _as_text(row[columns.index("fiscal_period")]) if "fiscal_period" in columns else None,
                        "value": _as_text(row[columns.index("value")]) if "value" in columns else None,
                        "has_value": row[columns.index("has_value")] if "has_value" in columns else None,
                        "close": _as_text(row[columns.index("close")]) if "close" in columns else None,
                        "high": _as_text(row[columns.index("high")]) if "high" in columns else None,
                        "low": _as_text(row[columns.index("low")]) if "low" in columns else None,
                        "volume": _as_text(row[columns.index("volume")]) if "volume" in columns else None,
                        "has_volume": row[columns.index("has_volume")] if "has_volume" in columns else None,
                        "vintage_at": _as_text(row[columns.index("vintage_at")]) if "vintage_at" in columns else None,
                        "has_vintage_at": row[columns.index("has_vintage_at")] if "has_vintage_at" in columns else None,
                        "raw_record_locator": _as_text(row[columns.index("raw_record_locator")]) if "raw_record_locator" in columns else None,
                    }
                )
    if {(item["path"], item["sha256"]) for item in child_manifest["selected_input_parts"]} != {
        (part_name, part_sha)
        for _, part_name, part_sha in seen_parts
    }:
        raise FeatureQualityReportError("child selected input parts are not fully traceable from their manifests")
    sources = sorted({item["source"] for item in input_rows if item["source"] is not None})
    return (
        {
            "manifests": sorted(manifests, key=lambda item: (item["path"], item["sha256"])),
            "parts": [
                {"path": part_name, "sha256": part_sha}
                for _, part_name, part_sha in sorted(seen_parts)
            ],
            "raw_locators": [],
            "sources": sources,
        },
        input_rows,
    )


def _eligible_input_rows(
    child_manifest: Mapping[str, Any],
    input_rows: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    decision_at = _parse_timestamp(child_manifest["decision_at"])
    feature_set = child_manifest["feature_set"]
    eligible: list[dict[str, Any]] = []
    for row in input_rows:
        if row["available_at"] is None or row["observed_at"] is None:
            continue
        if _parse_timestamp(row["available_at"]) > decision_at:
            continue
        if _parse_timestamp(row["observed_at"]) > decision_at:
            continue
        if feature_set == "fundamental-growth":
            period_end = _date_from_value(row["period_end"])
            if period_end is None or period_end > decision_at.date():
                continue
        if feature_set == "macro-state" and row["has_vintage_at"] is True and (
            row["vintage_at"] is None or _parse_timestamp(row["vintage_at"]) > decision_at
        ):
            continue
        eligible.append(row)
    return eligible


def _lineage_with_locators(
    lineage: dict[str, Any],
    input_rows: list[dict[str, Any]],
) -> dict[str, Any]:
    locators = {
        row["raw_record_locator"]
        for row in input_rows
        if row["raw_record_locator"] is not None
    }
    result = dict(lineage)
    result["raw_locators"] = sorted(locators)
    result["input_row_count"] = len(input_rows)
    return result


def _date_from_value(value: Any) -> date | None:
    if value is None:
        return None
    text = str(value)
    try:
        return date.fromisoformat(text[:10])
    except ValueError:
        return None


def _null_reason(
    feature_set: str,
    feature_name: str,
    input_rows: list[dict[str, Any]],
) -> str:
    if feature_set == "market-basic":
        if feature_name == "return_1d":
            return "insufficient_history" if len(input_rows) < 2 else "missing_value"
        if feature_name == "range_1d":
            return "missing_value"
        if feature_name == "volume":
            return "missing_volume"
    if feature_set == "market-momentum":
        horizons = {
            "return_1m": 22,
            "return_3m": 64,
            "return_6m": 127,
            "return_12m": 253,
            "realized_volatility_1m": 22,
            "max_drawdown_1m": 21,
        }
        if len(input_rows) < horizons.get(feature_name, 2):
            return "insufficient_history"
        return "missing_close"
    if feature_set == "fundamental-growth":
        revenue = [row for row in input_rows if row["concept"] == "Revenue"]
        operating = [row for row in input_rows if row["concept"] == "OperatingIncomeLoss"]
        if feature_name == "revenue":
            return "missing_value"
        if feature_name == "revenue_growth_yoy":
            if not revenue or all(row["has_value"] is not True for row in revenue):
                return "missing_value"
            periods = {_date_from_value(row["period_end"]).year for row in revenue if _date_from_value(row["period_end"])}
            return "insufficient_history" if len(periods) < 2 else "incomparable_period"
        if feature_name == "operating_margin":
            if not operating:
                return "incomparable_period"
            if any(row["value"] == "0" for row in revenue):
                return "zero_denominator"
            return "missing_value"
    if feature_set == "macro-state":
        if feature_name == "macro_level":
            return "missing_value"
        if len(input_rows) < 2:
            return "insufficient_history"
        observations = {_date_from_value(row["observed_at"]) for row in input_rows}
        years = {item.year for item in observations if item is not None}
        return "insufficient_history" if len(years) < 2 else "incomparable_period"
    return "missing_value"


def _validate_reason(reason: str, definition: Any | None) -> str:
    if definition is not None and reason not in definition.null_policy.reason_vocabulary:
        return definition.null_policy.reason_vocabulary[0]
    return reason


def _parse_timestamp(value: str) -> datetime:
    text = value[:-1] + "+00:00" if value.endswith("Z") else value
    parsed = datetime.fromisoformat(text)
    if parsed.tzinfo is None:
        raise FeatureQualityReportError(f"timestamp is missing timezone: {value}")
    return parsed.astimezone(UTC)


def build_feature_quality_report(
    manifest_path: str | Path,
    *,
    features_root: str | Path,
    data_root: str | Path,
    registry: FeatureRegistry | None = None,
    stale_after_seconds: int = _DEFAULT_STALE_AFTER_SECONDS,
) -> dict[str, Any]:
    """Validate and explain one feature batch without mutating any state."""
    if not isinstance(stale_after_seconds, int) or isinstance(stale_after_seconds, bool) or stale_after_seconds < 0:
        raise FeatureQualityReportError("stale_after_seconds must be a non-negative integer")
    try:
        batch = read_feature_batch(manifest_path, features_root=features_root, registry=registry)
    except (FeatureBatchError, FeatureArtifactError, FeatureRegistryError) as error:
        raise FeatureQualityReportError(str(error)) from error
    root = Path(features_root).expanduser().resolve()
    normalized_root = Path(data_root).expanduser().resolve()
    document = batch.manifest
    definition = None
    if registry is not None:
        definition = registry.resolve(document["feature_set"], document["feature_set_version"])
        feature_names = definition.feature_names
    else:
        feature_names = tuple(
            sorted(
                {
                    name
                    for observation in batch.observations
                    for name in observation["features"]
                }
            )
        )
    universe_size = len(document["universe"]["security_ids"])
    accepted_by_partition: dict[tuple[str, str], dict[str, Any]] = {}
    for part in document["parts"]:
        child_relative = _safe_relative_path(part["manifest_path"], label="batch child manifest")
        child_path = (root / child_relative).resolve()
        try:
            child_path.relative_to(root)
        except ValueError as error:
            raise FeatureQualityReportError("batch child manifest escapes features root") from error
        try:
            child = read_feature_artifact(child_path)
        except FeatureArtifactError as error:
            raise FeatureQualityReportError(str(error)) from error
        lineage, input_rows = _input_lineage(child.manifest, data_root=normalized_root)
        input_rows = _eligible_input_rows(child.manifest, input_rows)
        lineage = _lineage_with_locators(lineage, input_rows)
        if len(child.observations) != 1:
            raise FeatureQualityReportError("feature-quality report requires one observation per child partition")
        observation = child.observations[0]
        accepted_by_partition[(observation["decision_at"], observation["security_id"])] = {
            "observation": observation,
            "lineage": lineage,
            "input_rows": input_rows,
        }

    null_cases: list[dict[str, Any]] = []
    stale_inputs: list[dict[str, Any]] = []
    feature_present: Counter[str] = Counter()
    feature_null: Counter[str] = Counter()
    feature_reasons: dict[str, Counter[str]] = defaultdict(Counter)
    feature_sources: dict[str, Counter[str]] = defaultdict(Counter)
    stale_by_partition: dict[tuple[str, str], bool] = {}
    for (decision_at, security_id), item in sorted(accepted_by_partition.items()):
        observation = item["observation"]
        lineage = item["lineage"]
        input_rows = item["input_rows"]
        age_seconds = int((_parse_timestamp(decision_at) - _parse_timestamp(observation["input_available_at"])).total_seconds())
        is_stale = age_seconds > stale_after_seconds
        stale_by_partition[(decision_at, security_id)] = is_stale
        if is_stale:
            stale_inputs.append(
                {
                    "security_id": security_id,
                    "decision_at": decision_at,
                    "input_available_at": observation["input_available_at"],
                    "age_seconds": age_seconds,
                    "lineage": lineage,
                }
            )
        for source in lineage["sources"]:
            for feature_name in feature_names:
                feature_sources[feature_name][source] += 1
        for feature_name in feature_names:
            value = observation["features"].get(feature_name)
            if value is None:
                feature_null[feature_name] += 1
                reason = _validate_reason(
                    _null_reason(document["feature_set"], feature_name, input_rows),
                    definition,
                )
                feature_reasons[feature_name][reason] += 1
                null_cases.append(
                    {
                        "security_id": security_id,
                        "decision_at": decision_at,
                        "feature_name": feature_name,
                        "reason": reason,
                        "detail": f"{feature_name} is typed null under the reviewed null policy",
                        "lineage": lineage,
                    }
                )
            else:
                feature_present[feature_name] += 1

    rejected_cases = [
        {
            "security_id": item["security_id"],
            "decision_at": item["decision_at"],
            "reason": item["reason"],
            "detail": item["detail"],
            "lineage": {
                "manifests": [],
                "parts": [],
                "raw_locators": [],
                "sources": [],
                "input_row_count": 0,
            },
        }
        for item in document["rejected"]
    ]
    coverage: list[dict[str, Any]] = []
    decisions = document["decision_schedule"]
    for decision_at in decisions:
        rejected_count = sum(item["decision_at"] == decision_at for item in document["rejected"])
        accepted = [
            item
            for (item_decision, _), item in accepted_by_partition.items()
            if item_decision == decision_at
        ]
        for feature_name in feature_names:
            present = sum(item["observation"]["features"].get(feature_name) is not None for item in accepted)
            null_count = len(accepted) - present
            stale_count = sum(
                stale_by_partition.get((decision_at, item["observation"]["security_id"]), False)
                for item in accepted
            )
            coverage.append(
                {
                    "decision_at": decision_at,
                    "feature_name": feature_name,
                    "universe_size": universe_size,
                    "accepted_count": len(accepted),
                    "rejected_count": rejected_count,
                    "present_count": present,
                    "null_count": null_count,
                    "stale_count": stale_count,
                }
            )

    feature_summary = [
        {
            "feature_name": feature_name,
            "present_count": feature_present[feature_name],
            "null_count": feature_null[feature_name],
            "null_reasons": [
                {"reason": reason, "count": count}
                for reason, count in sorted(feature_reasons[feature_name].items())
            ],
            "source_contribution": [
                {"source": source, "count": count}
                for source, count in sorted(feature_sources[feature_name].items())
            ],
        }
        for feature_name in feature_names
    ]
    return {
        "report_version": _REPORT_VERSION,
        "batch": {
            "batch_id": document["batch"]["batch_id"],
            "feature_set": document["feature_set"],
            "feature_set_version": document["feature_set_version"],
            "manifest_path": str(Path(manifest_path).expanduser().resolve()),
            "registry_sha256": document["registry_sha256"],
        },
        "coverage": coverage,
        "features": feature_summary,
        "null_cases": null_cases,
        "rejected_cases": rejected_cases,
        "stale_inputs": stale_inputs,
    }


__all__ = [
    "FeatureQualityReportError",
    "build_feature_quality_report",
]
