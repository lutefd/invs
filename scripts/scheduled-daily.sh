#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
cd "$repo_root"

make_command="${INVS_MAKE_PATH:-}"
if [[ -z "$make_command" ]]; then
  make_command=$(command -v make 2>/dev/null || true)
elif [[ "$make_command" != */* ]]; then
  make_command=$(command -v "$make_command" 2>/dev/null || true)
fi
if [[ -z "$make_command" || ! -x "$make_command" ]]; then
  echo "scheduled_daily=failed reason=make_unavailable configured=${INVS_MAKE_PATH:-auto}" >&2
  exit 127
fi

invocation=$(date -u +%Y-%m-%dT%H%M%SZ)
run_date=$(date -u +%F)
run_key="scheduled-${invocation}"

echo "scheduled_daily_start=$invocation run_date=$run_date run_key=$run_key"
INVS_DAILY_RUN_KEY="$run_key" "$make_command" daily DAILY_DATE="$run_date"
echo "scheduled_daily_status=ok run_date=$run_date run_key=$run_key"
