"""Command-line entry point for release compatibility validation."""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

from .release import CompatibilityError, validate_release_manifest


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="invs-release")
    subparsers = parser.add_subparsers(dest="command", required=True)
    validate = subparsers.add_parser("validate", help="validate a release compatibility manifest")
    validate.add_argument("--manifest", default="release/compatibility.json")
    validate.add_argument("--repo-root", default=None)
    validate.add_argument(
        "--check-runtime",
        action="store_true",
        help="also require the running Python and installed packages to match the manifest",
    )
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    if args.command == "validate":
        try:
            result = validate_release_manifest(
                args.manifest,
                repo_root=Path(args.repo_root) if args.repo_root else None,
                check_runtime=args.check_runtime,
            )
        except CompatibilityError as error:
            print(f"release compatibility validation failed: {error}", file=sys.stderr)
            return 1
        print(
            f"validated release {result.manifest['release_version']} "
            f"({len(result.manifest['contracts']['schemas'])} schemas, "
            f"{len(result.manifest['contracts']['migrations']['files'])} migrations)"
        )
        return 0
    raise AssertionError(f"unhandled command {args.command}")


if __name__ == "__main__":
    raise SystemExit(main())
