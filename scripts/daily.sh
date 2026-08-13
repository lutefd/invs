#!/usr/bin/env bash
set -uo pipefail

if (( $# > 1 )); then
  echo "usage: $0 [YYYY-MM-DD]" >&2
  exit 2
fi

run_date="${1:-${INVS_DAILY_DATE:-$(date -u +%F)}}"
source="${INVS_DAILY_SOURCE:-all}"
run_key="${INVS_DAILY_RUN_KEY:-daily-${run_date}}"
runtime_dir="${INVS_RUNTIME_DIR:-.runtime}"
log_dir="${INVS_LOG_DIR:-logs}"

if ! parsed_date=$(date -u -d "$run_date" +%F 2>/dev/null) || [[ "$parsed_date" != "$run_date" ]]; then
  echo "daily date must be a valid YYYY-MM-DD: $run_date" >&2
  exit 2
fi
if [[ -z "$source" || -z "$run_key" ]]; then
  echo "INVS_DAILY_SOURCE and INVS_DAILY_RUN_KEY must not be empty" >&2
  exit 2
fi

mkdir -p "$runtime_dir" "$log_dir"
exec 9>"$runtime_dir/daily.lock"
if ! flock -n 9; then
  echo "daily run already active: $runtime_dir/daily.lock" >&2
  exit 75
fi

log_file="$log_dir/daily-${run_date}.log"
exec > >(tee -a "$log_file") 2>&1

echo "daily_start date=$run_date source=$source run_key=$run_key"

collection_status=0
if [[ "${INVS_DAILY_SKIP_COLLECTION:-0}" == 1 ]]; then
  echo "collection=skipped reason=INVS_DAILY_SKIP_COLLECTION"
else
  make ingest SOURCE="$source" RUN_KEY="$run_key"
  collection_status=$?
fi
echo "collection_status=$collection_status"

make reconcile
reconcile_status=$?
echo "reconcile_status=$reconcile_status"

make ops-status
status_status=$?
echo "ops_status=$status_status"

if (( collection_status != 0 || reconcile_status != 0 || status_status != 0 )); then
  echo "daily_status=attention"
  exit 1
fi

echo "daily_status=ok"
