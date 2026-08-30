"""Operator CLI for v0.6 forward paper accounts."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any

from .paper import (
    LedgerStore,
    PaperError,
    approve_paper_decision,
    create_paper_account,
    read_paper_report,
    rebuild_paper_account,
    reconcile_paper_account,
    run_paper_session,
)


def _strict_json(path: Path) -> dict[str, Any]:
    def no_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for key, value in pairs:
            if key in result:
                raise ValueError(f"duplicate JSON key {key!r}")
            result[key] = value
        return result

    document = json.loads(
        path.read_text(encoding="utf-8"),
        object_pairs_hook=no_duplicates,
        parse_constant=lambda value: (_ for _ in ()).throw(
            ValueError(f"invalid JSON constant {value}")
        ),
    )
    if not isinstance(document, dict):
        raise TypeError(f"paper account JSON {path} must be an object")
    return document


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="invs-paper",
        description="Create, operate, approve, and rebuild immutable forward paper accounts.",
    )
    commands = parser.add_subparsers(dest="command", required=True)
    create = commands.add_parser("create-account", help="persist one immutable paper account specification")
    create.add_argument("--spec", required=True)
    create.add_argument("--ledger-root", required=True)
    run = commands.add_parser("run", help="run one close decision and next-open settlement cycle")
    run.add_argument("--spec", required=True)
    run.add_argument("--data-root", required=True)
    run.add_argument("--ledger-root", required=True)
    run.add_argument("--session-date", required=True)
    approve = commands.add_parser("approve", help="append a manual approval or rejection")
    approve.add_argument("--account-id", required=True)
    approve.add_argument("--decision-id", required=True)
    approve.add_argument("--ledger-root", required=True)
    decision = approve.add_mutually_exclusive_group(required=True)
    decision.add_argument("--approved", action="store_true")
    decision.add_argument("--rejected", action="store_true")
    report = commands.add_parser("report", help="read one immutable daily report")
    report.add_argument("--account-id", required=True)
    report.add_argument("--ledger-root", required=True)
    report.add_argument("--session-date", required=True)
    rebuild = commands.add_parser("rebuild", help="rebuild the account projection from ledger events")
    rebuild.add_argument("--account-id", required=True)
    rebuild.add_argument("--ledger-root", required=True)
    reconcile = commands.add_parser("reconcile", help="check every valuation and the latest projection")
    reconcile.add_argument("--account-id", required=True)
    reconcile.add_argument("--ledger-root", required=True)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        if args.command == "create-account":
            output = create_paper_account(
                _strict_json(Path(args.spec)), ledger_root=args.ledger_root
            )
        elif args.command == "run":
            output = run_paper_session(
                _strict_json(Path(args.spec)),
                data_root=args.data_root,
                ledger_root=args.ledger_root,
                session_date=args.session_date,
            )
        elif args.command == "approve":
            output = approve_paper_decision(
                args.account_id,
                ledger_root=args.ledger_root,
                decision_id=args.decision_id,
                approved=args.approved,
            )
        elif args.command == "report":
            output = read_paper_report(
                args.account_id, args.session_date, ledger_root=args.ledger_root
            )
        elif args.command == "rebuild":
            output = rebuild_paper_account(args.account_id, ledger_root=args.ledger_root)
        else:
            output = reconcile_paper_account(LedgerStore(args.ledger_root, args.account_id))
    except (PaperError, OSError, TypeError, ValueError, json.JSONDecodeError) as error:
        print(f"invs-paper: {error}", file=sys.stderr)
        return 1
    print(json.dumps(output, ensure_ascii=False, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
