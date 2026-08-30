"""Operator CLI for deterministic v0.5 backtest runs and comparisons."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from .backtest import simulate_backtest
from .backtest_results import (
    BacktestResultError,
    compare_backtest_results,
    publish_backtest_result,
    read_backtest_result,
)
from .experiments import BacktestSpecError, read_experiment_spec


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="invs-backtest",
        description="Run, validate, and compare immutable point-in-time backtests.",
    )
    commands = parser.add_subparsers(dest="command", required=True)
    run = commands.add_parser("run", help="run one experiment and publish immutable result artifacts")
    run.add_argument("--spec", required=True)
    run.add_argument("--data-root", required=True)
    run.add_argument("--results-root", required=True)
    run.add_argument("--checkpoint-root")
    run.add_argument("--resume", action="store_true")
    run.add_argument("--stop-after-session", type=int)
    validate = commands.add_parser("validate", help="validate one published result")
    validate.add_argument("--manifest", required=True)
    compare = commands.add_parser("compare", help="compare published results without mutation")
    compare.add_argument("--manifest", required=True, action="append", dest="manifests")
    return parser


def _run(args: argparse.Namespace) -> dict[str, object]:
    validated = read_experiment_spec(Path(args.spec))
    run = simulate_backtest(
        validated.spec,
        data_root=args.data_root,
        checkpoint_root=args.checkpoint_root,
        resume=args.resume,
        stop_after_session=args.stop_after_session,
    )
    manifest_path = publish_backtest_result(run, results_root=args.results_root)
    result = read_backtest_result(manifest_path)
    return {
        "action": "published",
        "manifest_path": str(result.manifest_path),
        "result_id": result.manifest["result_id"],
        "experiment_id": result.manifest["experiment_id"],
        "summary": result.manifest["summary"],
    }


def _validate(args: argparse.Namespace) -> dict[str, object]:
    result = read_backtest_result(args.manifest)
    return {
        "action": "validated",
        "manifest_path": str(result.manifest_path),
        "result_id": result.manifest["result_id"],
        "experiment_id": result.manifest["experiment_id"],
        "summary": result.manifest["summary"],
    }


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        if args.command == "run":
            output = _run(args)
        elif args.command == "validate":
            output = _validate(args)
        else:
            output = compare_backtest_results(args.manifests)
    except (BacktestResultError, BacktestSpecError, OSError, ValueError) as error:
        print(f"invs-backtest: {error}", file=sys.stderr)
        return 1
    print(json.dumps(output, ensure_ascii=False, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
