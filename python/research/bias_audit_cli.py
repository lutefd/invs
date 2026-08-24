"""Operator CLI for immutable point-in-time bias audits."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

if __package__:
    from .bias_audit import BiasAuditError, publish_bias_audit, validate_bias_audit
else:  # pragma: no cover - exercised by the dependency-free host Make target
    from bias_audit import BiasAuditError, publish_bias_audit, validate_bias_audit


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="invs-bias-audit",
        description="Publish or validate a bounded point-in-time bias audit.",
    )
    commands = parser.add_subparsers(dest="command", required=True)
    publish = commands.add_parser("publish")
    publish.add_argument("--spec", required=True)
    publish.add_argument("--audits-root", default="data/audits/point-in-time")
    validate = commands.add_parser("validate")
    validate.add_argument("--manifest", required=True)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        if args.command == "publish":
            path = publish_bias_audit(args.spec, audits_root=args.audits_root)
            action = "published"
        else:
            path = Path(args.manifest)
            action = "validated"
        artifact = validate_bias_audit(path)
    except (BiasAuditError, OSError, TypeError, ValueError) as error:
        print(f"invs-bias-audit: {error}", file=sys.stderr)
        return 1
    manifest = artifact.manifest
    print(
        json.dumps(
            {
                "action": action,
                "manifest_path": str(artifact.manifest_path),
                "artifact_id": manifest["artifact_id"],
                "audit_id": manifest["audit_id"],
                "status": manifest["status"],
                "regions": manifest["regions"],
                "probe_count": manifest["probe_count"],
                "verified_artifact_count": len(manifest["verified_artifacts"]),
            },
            indent=2,
            sort_keys=True,
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
