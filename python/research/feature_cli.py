"""Operator CLI for deterministic feature artifacts and batches."""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path
from typing import Any

from .batches import (
    FeatureBatchError,
    publish_feature_batch,
    validate_feature_batch,
)
from .catalog import DatasetSchemaError, ResearchCatalog
from .features import (
    FeatureArtifactError,
    publish_market_basic,
    validate_feature_artifact,
)
from .registry import FeatureRegistryError, load_feature_registry


def _summary(artifact: Any, *, action: str) -> dict[str, Any]:
    manifest = artifact.manifest
    return {
        "action": action,
        "manifest_path": str(artifact.manifest_path),
        "artifact_id": manifest["artifact"]["artifact_id"],
        "feature_set": manifest["feature_set"],
        "feature_set_version": manifest["feature_set_version"],
        "calendar_pin": manifest["calendar_pin"],
        "decision_at": manifest["decision_at"],
        "input_available_at": manifest["input_available_at"],
        "available_at": manifest["available_at"],
        "input_fingerprint": manifest["input_fingerprint"],
        "row_count": artifact.row_count,
        "features": dict(artifact.observations[0]["features"]),
    }


def _batch_summary(artifact: Any, *, action: str) -> dict[str, Any]:
    manifest = artifact.manifest
    summary = manifest["run_summary"]
    return {
        "action": action,
        "manifest_path": str(artifact.manifest_path),
        "batch_id": manifest["batch"]["batch_id"],
        "feature_set": manifest["feature_set"],
        "feature_set_version": manifest["feature_set_version"],
        "registry_sha256": manifest["registry_sha256"],
        "universe_size": len(manifest["universe"]["security_ids"]),
        "decision_count": len(manifest["decision_schedule"]),
        "row_count": artifact.row_count,
        "accepted_partitions": summary["accepted_partitions"],
        "rejected_partitions": summary["rejected_partitions"],
        "input_fitness": manifest["input_fitness"],
    }


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="invs-feature",
        description="Publish or validate immutable deterministic feature artifacts.",
    )
    subcommands = parser.add_subparsers(dest="command", required=True)

    publish = subcommands.add_parser(
        "publish", help="select point-in-time price inputs and publish one artifact"
    )
    publish.add_argument("--data-root", default="/data")
    publish.add_argument("--features-root", default="/data/features")
    publish.add_argument("--security-id", required=True)
    publish.add_argument("--decision-at", required=True)
    publish.add_argument(
        "--calendar-pin",
        required=True,
        help="path to an exact calendar-manifest pin JSON object",
    )
    publish.add_argument("--computation-delay-seconds", type=int, default=0)
    publish.add_argument("--git-commit", default=os.environ.get("INVS_GIT_COMMIT", "unknown"))

    validate = subcommands.add_parser(
        "validate", help="validate one manifest and exactly its listed immutable parts"
    )
    validate.add_argument("--manifest", required=True)

    batch_publish = subcommands.add_parser(
        "batch-publish", help="publish or resume a dataset-level feature batch"
    )
    batch_publish.add_argument("--data-root", default="/data")
    batch_publish.add_argument("--features-root", default="/data/features")
    batch_publish.add_argument("--registry", required=True)
    batch_publish.add_argument("--universe", required=True, help="JSON explicit security-list snapshot")
    batch_publish.add_argument("--schedule", required=True, help="JSON decision timestamp schedule")
    batch_publish.add_argument("--calendar-pin", required=True)
    batch_publish.add_argument("--feature-set", default="market-basic")
    batch_publish.add_argument("--feature-set-version", default="1.0.0")
    batch_publish.add_argument("--git-commit", default=os.environ.get("INVS_GIT_COMMIT", "unknown"))

    batch_validate = subcommands.add_parser(
        "batch-validate", help="validate one batch and every referenced child artifact"
    )
    batch_validate.add_argument("--manifest", required=True)
    batch_validate.add_argument("--features-root", required=True)
    batch_validate.add_argument("--registry", required=True)
    return parser


def _load_json_object(path: str, *, label: str) -> dict[str, Any]:
    def object_without_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        document: dict[str, Any] = {}
        for key, value in pairs:
            if key in document:
                raise ValueError(f"duplicate JSON key {key!r}")
            document[key] = value
        return document

    def reject_json_constant(value: str) -> Any:
        raise ValueError(f"invalid JSON constant {value}")

    document = json.loads(
        Path(path).read_text(encoding="utf-8"),
        object_pairs_hook=object_without_duplicates,
        parse_constant=reject_json_constant,
    )
    if not isinstance(document, dict):
        raise TypeError(f"{label} must be a JSON object")
    return document


def _load_calendar_pin(path: str) -> dict[str, Any]:
    return _load_json_object(path, label="calendar pin")


def _load_security_ids(path: str) -> list[str]:
    document = _load_json_object(path, label="universe")
    if set(document) != {"security_ids"} or not isinstance(document["security_ids"], list):
        raise ValueError("universe must contain exactly one security_ids array")
    return document["security_ids"]


def _load_decision_ats(path: str) -> list[str]:
    document = _load_json_object(path, label="schedule")
    if set(document) != {"decision_ats"} or not isinstance(document["decision_ats"], list):
        raise ValueError("schedule must contain exactly one decision_ats array")
    return document["decision_ats"]


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        if args.command == "publish":
            catalog = ResearchCatalog(args.data_root).register()
            manifest_path = publish_market_basic(
                catalog,
                decision_at=args.decision_at,
                security_id=args.security_id,
                calendar_pin=_load_calendar_pin(args.calendar_pin),
                features_root=args.features_root,
                computation_delay_seconds=args.computation_delay_seconds,
                git_commit=args.git_commit,
            )
            artifact = validate_feature_artifact(manifest_path)
            result = _summary(artifact, action="published")
        elif args.command == "validate":
            artifact = validate_feature_artifact(Path(args.manifest))
            result = _summary(artifact, action="validated")
        elif args.command == "batch-publish":
            registry = load_feature_registry(args.registry)
            catalog = ResearchCatalog(args.data_root).register()
            manifest_path = publish_feature_batch(
                catalog,
                registry=registry,
                security_ids=_load_security_ids(args.universe),
                decision_ats=_load_decision_ats(args.schedule),
                calendar_pin=_load_calendar_pin(args.calendar_pin),
                features_root=args.features_root,
                feature_set=args.feature_set,
                feature_set_version=args.feature_set_version,
                git_commit=args.git_commit,
            )
            artifact = validate_feature_batch(
                manifest_path,
                features_root=args.features_root,
                registry=registry,
            )
            result = _batch_summary(artifact, action="published")
        else:
            registry = load_feature_registry(args.registry)
            artifact = validate_feature_batch(
                Path(args.manifest),
                features_root=args.features_root,
                registry=registry,
            )
            result = _batch_summary(artifact, action="validated")
    except (
        DatasetSchemaError,
        FeatureArtifactError,
        FeatureBatchError,
        FeatureRegistryError,
        OSError,
        TypeError,
        ValueError,
    ) as error:
        print(f"invs-feature: {error}", file=sys.stderr)
        return 1

    print(json.dumps(result, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
