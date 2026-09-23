"""Command-line interface for current-vintage retrospective backfills."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
from pathlib import Path

from .retrospective_discovery import (
    RetrospectiveBackfillError,
    publish_retrospective_backfill,
    validate_retrospective_backfill,
)


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="invs-discovery-backfill")
    commands = parser.add_subparsers(dest="command", required=True)
    publish = commands.add_parser(
        "publish",
        help="reconstruct historical prices and discovery ranks using the installed vintage",
    )
    publish.add_argument("--profile", required=True)
    publish.add_argument("--calendar-snapshot", required=True)
    publish.add_argument("--data-root", default="/data")
    publish.add_argument(
        "--output-root", default="/data/research/discovery/retrospective-current-universe"
    )
    publish.add_argument("--months", type=int, default=3)
    publish.add_argument("--start-date")
    publish.add_argument("--end-date")
    publish.add_argument("--candidate-count", type=int, default=5)
    publish.add_argument("--source", default="yahoo")
    publish.add_argument("--price-basis", default="split_adjusted")
    publish.add_argument("--currency", default="USD")
    publish.add_argument("--reconstructed-at")
    publish.add_argument("--git-commit", default=os.environ.get("INVS_GIT_COMMIT", "unknown"))
    validate = commands.add_parser("validate", help="validate one retained backfill manifest")
    validate.add_argument("--manifest", required=True)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        if args.command == "publish":
            path = publish_retrospective_backfill(
                profile_path=args.profile,
                calendar_snapshot_path=args.calendar_snapshot,
                data_root=args.data_root,
                output_root=args.output_root,
                months=args.months,
                start_date=args.start_date,
                end_date=args.end_date,
                source=args.source,
                price_basis=args.price_basis,
                currency=args.currency,
                candidate_count=args.candidate_count,
                reconstructed_at=args.reconstructed_at,
                git_commit=args.git_commit,
            )
            action = "published"
        else:
            path = Path(args.manifest).resolve()
            action = "validated"
        document = validate_retrospective_backfill(path)
        print(
            json.dumps(
                {
                    "action": action,
                    "manifest_path": str(path),
                    "manifest_sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
                    "reconstruction_run_id": document["reconstruction_run_id"],
                    "observation_mode": document["observation_mode"],
                    "universe_basis": document["universe_basis"],
                    "input_fitness": document["price_inputs"]["historical_fitness"],
                    "start_date": document["range"]["start_date"],
                    "end_date": document["range"]["end_date"],
                    "session_count": len(document["sessions"]),
                    "price_bar_count": sum(
                        len(session["price_bars"]) for session in document["sessions"]
                    ),
                    "ranking_row_count": sum(
                        len(session["rankings"]) for session in document["sessions"]
                    ),
                },
                indent=2,
                sort_keys=True,
            )
        )
        return 0
    except (RetrospectiveBackfillError, OSError, ValueError, TypeError) as error:
        print(f"invs-discovery-backfill: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
