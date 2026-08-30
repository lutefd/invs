#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

compose=(docker compose --progress quiet)
acceptance_root="$repo_root/data/research/acceptance/v0.5/reproduction"

mkdir -p "$acceptance_root"
"${compose[@]}" run --rm --no-deps \
	-v "$acceptance_root:/data/research/acceptance/v0.5/reproduction" \
	jupyter python -m research.backtest_acceptance \
	--acceptance-root /data/research/acceptance/v0.5/reproduction

report="$acceptance_root/backtest-reproduction.json"
jq -e '.status == "passed" and .baseline_count == 5 and .bias_audit.status == "passed" and .reproduction.manifest_equal and .reproduction.artifact_files_equal and .reproduction.perturbation_requires_new_identity' "$report" >/dev/null
printf '%s\n' "v0.5 backtest reproduction acceptance passed"
printf '%s\n' "report: $report"
