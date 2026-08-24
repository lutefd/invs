"""Operator CLI for deterministic corporate-action adjustment artifacts."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any

from .adjustments import (
    AdjustmentArtifactError,
    publish_adjusted_prices,
    validate_adjustment_artifact,
)


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="invs-adjust",
        description="Publish or validate immutable adjusted-price artifacts.",
    )
    commands = parser.add_subparsers(dest="command", required=True)
    publish = commands.add_parser("publish")
    publish.add_argument("--raw-manifest", required=True)
    publish.add_argument("--actions", required=True)
    publish.add_argument("--decision-at", required=True)
    publish.add_argument("--adjustments-root", default="/data/adjusted/prices")
    validate = commands.add_parser("validate")
    validate.add_argument("--manifest", required=True)
    return parser


def _load_actions(path: str) -> list[dict[str, Any]]:
    document = json.loads(Path(path).read_text(encoding="utf-8"))
    if isinstance(document, dict):
        document = document.get("actions")
    if not isinstance(document, list) or not all(isinstance(row, dict) for row in document):
        raise TypeError("actions input must be an array or an object containing an actions array")
    return document


def _summary(manifest_path: Path, *, action: str) -> dict[str, Any]:
    artifact = validate_adjustment_artifact(manifest_path)
    manifest = artifact.manifest
    return {
        "action": action,
        "manifest_path": str(artifact.manifest_path),
        "artifact_id": manifest["artifact_id"],
        "policy_version": manifest["policy_version"],
        "security_id": manifest["security_id"],
        "decision_at": manifest["decision_at"],
        "selected_action_count": len(manifest["selected_actions"]),
        "row_count": len(artifact.rows),
    }


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        if args.command == "publish":
            path = publish_adjusted_prices(
                args.raw_manifest,
                _load_actions(args.actions),
                decision_at=args.decision_at,
                adjustments_root=args.adjustments_root,
            )
            summary = _summary(path, action="published")
        else:
            summary = _summary(Path(args.manifest), action="validated")
    except (AdjustmentArtifactError, OSError, TypeError, ValueError) as error:
        print(f"invs-adjust: {error}", file=sys.stderr)
        return 1
    print(json.dumps(summary, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
