"""Operator CLI for deterministic integrated workflow reports."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from .workflow import (
    WorkflowError,
    build_workflow_report,
    load_workflow_spec,
    read_workflow_report,
    write_workflow_report,
)


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="invs-workflow",
        description="Build and validate immutable research-to-paper workflow reports.",
    )
    commands = parser.add_subparsers(dest="command", required=True)
    build = commands.add_parser("build", help="build one deterministic workflow report")
    build.add_argument("--spec", required=True)
    build.add_argument("--repo-root", default=".")
    build.add_argument("--output", required=True)
    validate = commands.add_parser("validate", help="validate one workflow report and its references")
    validate.add_argument("--report", required=True)
    validate.add_argument("--repo-root", default=".")
    validate.add_argument("--spec")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        root = Path(args.repo_root).expanduser().resolve()
        if args.command == "build":
            spec = load_workflow_spec(args.spec, repo_root=root)
            report = build_workflow_report(spec, repo_root=root)
            output = write_workflow_report(report, path=args.output, repo_root=root)
            result = {
                "action": "built",
                "report_path": str(output),
                "workflow_id": report["workflow_id"],
                "scenario": report["scenario"],
                "status": report["status"],
            }
        else:
            spec = load_workflow_spec(args.spec, repo_root=root) if args.spec else None
            validated = read_workflow_report(args.report, repo_root=root, spec=spec)
            result = {
                "action": "validated",
                "report_path": str(validated.report_path),
                "workflow_id": validated.report["workflow_id"],
                "scenario": validated.report["scenario"],
                "status": validated.report["status"],
            }
    except (WorkflowError, OSError, ValueError) as error:
        print(f"invs-workflow: {error}", file=sys.stderr)
        return 1
    print(json.dumps(result, ensure_ascii=False, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
