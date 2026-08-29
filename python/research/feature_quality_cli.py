"""Operator CLI for read-only feature-quality reports."""

from __future__ import annotations

import argparse
import json
import sys

from .feature_quality import (
    FeatureQualityReportError,
    build_feature_quality_report,
)
from .registry import FeatureRegistryError, load_feature_registry


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="invs-feature-quality",
        description="Read and explain one immutable feature batch without mutating state.",
    )
    subcommands = parser.add_subparsers(dest="command", required=True)
    report = subcommands.add_parser(
        "report", help="report coverage, typed nulls, stale inputs, rejects, and lineage"
    )
    report.add_argument("--manifest", required=True)
    report.add_argument("--features-root", required=True)
    report.add_argument("--data-root", required=True)
    report.add_argument("--registry", required=True)
    report.add_argument("--stale-after-seconds", type=int, default=30 * 24 * 60 * 60)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        registry = load_feature_registry(args.registry)
        result = build_feature_quality_report(
            args.manifest,
            features_root=args.features_root,
            data_root=args.data_root,
            registry=registry,
            stale_after_seconds=args.stale_after_seconds,
        )
    except (FeatureQualityReportError, FeatureRegistryError, OSError, ValueError) as error:
        print(f"invs-feature-quality: {error}", file=sys.stderr)
        return 1
    print(json.dumps(result, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
