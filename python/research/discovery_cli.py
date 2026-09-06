"""CLI for daily stock discovery indexes."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
from pathlib import Path

from .discovery import DiscoveryError, publish_discovery_index, read_discovery_index


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="invs-discovery")
    commands = parser.add_subparsers(dest="command", required=True)
    publish = commands.add_parser("publish")
    publish.add_argument("--profile", required=True)
    publish.add_argument("--momentum-batch", required=True)
    publish.add_argument("--features-root", default="/data/features")
    publish.add_argument("--registry", required=True)
    publish.add_argument("--output-root", required=True)
    publish.add_argument("--market-session", required=True)
    publish.add_argument("--candidate-count", type=int, default=5)
    publish.add_argument("--git-commit", default=os.environ.get("INVS_GIT_COMMIT", "unknown"))
    validate = commands.add_parser("validate")
    validate.add_argument("--manifest", required=True)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        if args.command == "publish":
            path = publish_discovery_index(
                profile_path=args.profile,
                momentum_batch_manifest=args.momentum_batch,
                features_root=args.features_root,
                registry_path=args.registry,
                output_root=args.output_root,
                market_session=args.market_session,
                candidate_count=args.candidate_count,
                git_commit=args.git_commit,
            )
        else:
            path = Path(args.manifest).resolve()
        document = read_discovery_index(path)
        print(
            json.dumps(
                {
                    "action": "published" if args.command == "publish" else "validated",
                    "manifest_path": str(path),
                    "manifest_sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
                    "discovery_id": document["discovery_id"],
                    "market_session": document["market_session"],
                    **document["summary"],
                    "candidates": [row["ticker"] for row in document["rows"] if row["candidate"]],
                },
                indent=2,
                sort_keys=True,
            )
        )
        return 0
    except (DiscoveryError, OSError, ValueError) as error:
        print(f"invs-discovery: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
