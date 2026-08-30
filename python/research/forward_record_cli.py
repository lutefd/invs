"""Operator CLI for capturing and validating wall-clock paper evidence."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from .forward_record import (
    ForwardRecordError,
    capture_forward_record,
    load_forward_record,
)


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="invs-forward-record",
        description="Capture and validate hash-pinned wall-clock paper evidence.",
    )
    commands = parser.add_subparsers(dest="command", required=True)
    capture = commands.add_parser("capture", help="capture recent reconciled paper reports")
    capture.add_argument("--repo-root", default=".")
    capture.add_argument("--ledger-root", required=True)
    capture.add_argument("--account-id", action="append", required=True)
    capture.add_argument("--output", required=True)
    validate = commands.add_parser("validate", help="validate one captured forward record")
    validate.add_argument("--repo-root", default=".")
    validate.add_argument("--evidence", required=True)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        root = Path(args.repo_root).expanduser().resolve()
        if args.command == "capture":
            output = capture_forward_record(
                repo_root=root,
                ledger_root=args.ledger_root,
                account_ids=args.account_id,
                output=args.output,
            )
            evidence = load_forward_record(output, repo_root=root)
            result = {
                "action": "captured",
                "evidence_path": str(output),
                "record_id": evidence["record_id"],
                "account_ids": evidence["account_ids"],
                "session_dates": evidence["session_dates"],
                "fitness": evidence["fitness"],
            }
        else:
            evidence = load_forward_record(args.evidence, repo_root=root)
            result = {
                "action": "validated",
                "evidence_path": str(Path(args.evidence).expanduser().resolve()),
                "record_id": evidence["record_id"],
                "account_ids": evidence["account_ids"],
                "session_dates": evidence["session_dates"],
                "fitness": evidence["fitness"],
            }
    except (ForwardRecordError, OSError, ValueError) as error:
        print(f"invs-forward-record: {error}", file=sys.stderr)
        return 1
    print(json.dumps(result, ensure_ascii=False, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
