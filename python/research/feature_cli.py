"""Operator CLI for deterministic market-basic feature artifacts."""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path
from typing import Any

from .catalog import DatasetSchemaError, ResearchCatalog
from .features import (
    FeatureArtifactError,
    publish_market_basic,
    validate_feature_artifact,
)


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


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="invs-feature",
        description="Publish or validate immutable market-basic feature artifacts.",
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
    return parser


def _load_calendar_pin(path: str) -> dict[str, Any]:
    def object_without_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        document: dict[str, Any] = {}
        for key, value in pairs:
            if key in document:
                raise ValueError(f"duplicate JSON key {key!r}")
            document[key] = value
        return document

    document = json.loads(
        Path(path).read_text(encoding="utf-8"),
        object_pairs_hook=object_without_duplicates,
    )
    if not isinstance(document, dict):
        raise TypeError("calendar pin must be a JSON object")
    return document


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
        else:
            artifact = validate_feature_artifact(Path(args.manifest))
            result = _summary(artifact, action="validated")
    except (DatasetSchemaError, FeatureArtifactError, OSError, TypeError, ValueError) as error:
        print(f"invs-feature: {error}", file=sys.stderr)
        return 1

    print(json.dumps(result, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
