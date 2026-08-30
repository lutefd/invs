#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

compose=(docker compose --progress quiet)
acceptance_root="$repo_root/data/research/acceptance/v0.6/reproduction"

mkdir -p "$acceptance_root"
"${compose[@]}" run --rm --no-deps \
	-v "$acceptance_root:/data/research/acceptance/v0.6/reproduction" \
	jupyter sh -c "pip install -q -e '.[dev]' && python -m research.paper_acceptance --acceptance-root /data/research/acceptance/v0.6/reproduction" \
	> "$acceptance_root/paper-reproduction-run.json"

report="$acceptance_root/paper-reproduction.json"
jq -e '
  .status == "passed" and
  .session_count >= 20 and
  .acceptance.forward_sessions and
  .acceptance.separate_strategy_sleeves.equal_weight and
  .acceptance.separate_strategy_sleeves.momentum_12_1 and
  .acceptance.rebalance and
  .acceptance.no_op and
  .acceptance.stale_data_halt and
  .acceptance.risk_rejection and
  .acceptance.dividend and
  .acceptance.corporate_actions and
  .acceptance.restart and
  .acceptance.duplicate_auto_cycle and
  .acceptance.rebuild_exact and
  .acceptance.backup_restore and
  .acceptance.reconciliation
' "$report" >/dev/null
printf '%s\n' "v0.6 paper reproduction acceptance passed"
printf '%s\n' "report: $report"
