"""CLI for live market operator input materialization."""

from __future__ import annotations

import argparse
import json
import sys

from .operator import OperatorMaterializationError, materialize_operator_snapshot


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="invs-operator")
    parser.add_argument("--profile", required=True)
    parser.add_argument("--calendar-snapshot", required=True)
    parser.add_argument("--data-root", default="/data")
    parser.add_argument("--output-root", required=True)
    parser.add_argument("--decision-at", required=True)
    parser.add_argument("--not-before", required=True)
    parser.add_argument("--git-commit", default="unknown")
    args = parser.parse_args(argv)
    try:
        result = materialize_operator_snapshot(
            profile_path=args.profile,
            calendar_snapshot_path=args.calendar_snapshot,
            data_root=args.data_root,
            output_root=args.output_root,
            decision_at=args.decision_at,
            not_before=args.not_before,
            git_commit=args.git_commit,
        )
    except (OSError, TypeError, ValueError, OperatorMaterializationError) as error:
        print(f"invs-operator: {error}", file=sys.stderr)
        return 1
    print(json.dumps(result, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
