#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
cd "$repo_root"

runtime_dir="${INVS_MARKET_RUNTIME_DIR:-.runtime/market-cycle}"
output_host="${INVS_MARKET_OUTPUT_ROOT:-data/research/forward/nasdaq-100-starter}"
output_container="/data/${output_host#data/}"
profile_host="${INVS_MARKET_PROFILE:-config/universes/nasdaq-100-starter.yaml}"
source="${INVS_MARKET_SOURCE:-prices}"
mkdir -p "$runtime_dir" logs

exec 9>"$runtime_dir/market-cycle.lock"
if ! flock -n 9; then
  echo "market_cycle=skipped reason=already_running"
  exit 75
fi

activation_path="$runtime_dir/activated-at"
if [[ ! -f "$activation_path" ]]; then
  date -u +%Y-%m-%dT%H:%M:%SZ >"$activation_path"
fi
activation=$(tr -d '[:space:]' <"$activation_path")
invocation=$(date -u +%Y-%m-%dT%H%M%SZ)
log_path="logs/market-cycle-${invocation}.log"
exec > >(tee -a "$log_path") 2>&1

echo "market_cycle_start=$invocation activation=$activation source=$source"
if [[ "${INVS_MARKET_SKIP_COLLECTION:-0}" != 1 ]]; then
  make ingest SOURCE="$source" RUN_KEY="market-${source}-${invocation}"
fi
make reconcile

calendar_path="$runtime_dir/calendar-snapshot.json"
docker compose exec -T postgres sh -c 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At' >"$calendar_path" <<'SQL'
WITH chosen AS (
  SELECT *
  FROM calendar_manifests
  WHERE mic = 'XNAS'
  ORDER BY available_at DESC, recorded_at DESC, id DESC
  LIMIT 1
)
SELECT json_build_object(
  'manifest', json_build_object(
    'data_source_id', chosen.data_source_id,
    'mic', chosen.mic,
    'calendar_version', chosen.calendar_version,
    'session_fingerprint', chosen.session_fingerprint,
    'available_at', chosen.available_at
  ),
  'sessions', COALESCE((
    SELECT json_agg(json_build_object(
      'session_date', session_date,
      'session_status', session_status,
      'open_at', open_at,
      'close_at', close_at,
      'is_early_close', is_early_close,
      'available_at', available_at,
      'revision', revision
    ) ORDER BY session_date)
    FROM trading_sessions
    WHERE data_source_id = chosen.data_source_id
      AND mic = chosen.mic
      AND calendar_version = chosen.calendar_version
      AND exchange_timezone = chosen.exchange_timezone
  ), '[]'::json)
)
FROM chosen;
SQL
if [[ ! -s "$calendar_path" ]]; then
  echo "market_cycle=failed reason=no_xnas_calendar" >&2
  exit 1
fi

decision_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
result_path="$runtime_dir/materialization.json"
git_commit=$(git rev-parse --verify HEAD | tr '[:upper:]' '[:lower:]')
docker compose run --rm --no-deps \
  -v "$repo_root/$profile_host:/tmp/operator-profile.yaml:ro" \
  -v "$repo_root/$calendar_path:/tmp/operator-calendar.json:ro" \
  jupyter python -m research.operator_cli \
    --profile /tmp/operator-profile.yaml \
    --calendar-snapshot /tmp/operator-calendar.json \
    --data-root /data \
    --output-root "$output_container" \
    --decision-at "$decision_at" \
    --not-before "$activation" \
    --git-commit "$git_commit" | tee "$result_path"

container_to_host() {
  local path=$1
  case "$path" in
    /data/*) printf '%s/data/%s\n' "$repo_root" "${path#/data/}" ;;
    *) echo "unexpected container data path: $path" >&2; return 1 ;;
  esac
}

universe_host=$(container_to_host "$(jq -r '.feature.universe' "$result_path")")
schedule_host=$(container_to_host "$(jq -r '.feature.schedule' "$result_path")")
calendar_pin_host=$(container_to_host "$(jq -r '.feature.calendar_pin' "$result_path")")
mappings_host=$(container_to_host "$(jq -r '.feature.security_mappings' "$result_path")")
make feature-batch \
  BATCH_UNIVERSE="$universe_host" \
  BATCH_SCHEDULE="$schedule_host" \
  CALENDAR_PIN="$calendar_pin_host" \
  SECURITY_MAPPINGS="$mappings_host" \
  FEATURE_SET=market-basic \
  FEATURE_SET_VERSION=1.0.0

status=$(jq -r '.status' "$result_path")
if [[ "$status" == paper_ready ]]; then
  account_id=$(jq -r '.paper.account_id' "$result_path")
  generated_spec=$(container_to_host "$(jq -r '.paper.spec' "$result_path")")
  inputs_container=$(jq -r '.paper.inputs' "$result_path")
  ledger_container=$(jq -r '.paper.ledger_root' "$result_path")
  ledger_host=$(container_to_host "$ledger_container")
  persisted_spec="$ledger_host/accounts/$account_id/account.json"
  if [[ ! -f "$persisted_spec" ]]; then
    make paper-create-account PAPER_SPEC="$generated_spec" PAPER_LEDGER_ROOT="$ledger_container"
    persisted_spec="$ledger_host/accounts/$account_id/account.json"
  fi
  session_date=$(jq -r '.session_date' "$result_path")
  make paper-validate-inputs \
    PAPER_SPEC="$persisted_spec" \
    PAPER_DATA_ROOT=/data \
    PAPER_INPUTS="$inputs_container" \
    PAPER_SESSION_DATE="$session_date" \
    PAPER_DECISION_AT="$decision_at"
  make paper-run \
    PAPER_SPEC="$persisted_spec" \
    PAPER_DATA_ROOT=/data \
    PAPER_INPUTS="$inputs_container" \
    PAPER_LEDGER_ROOT="$ledger_container" \
    PAPER_SESSION_DATE="$session_date" \
    PAPER_DECISION_AT="$decision_at"
  make paper-reconcile PAPER_ACCOUNT_ID="$account_id" PAPER_LEDGER_ROOT="$ledger_container"
else
  echo "paper_status=$status reason=$(jq -r '.paper_reason // "not ready"' "$result_path")"
fi

echo "market_cycle_status=ok materialization=$result_path log=$log_path"
