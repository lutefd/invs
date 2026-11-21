#!/usr/bin/env bash
set -euo pipefail

max_age_hours="${INVS_PROJECTION_MAX_AGE_HOURS:-24}"
scope="${INVS_PROJECTION_SCOPE:-${PROJECTION_SCOPE:-all}}"
require_recent_discovery="${INVS_PROJECTION_REQUIRE_RECENT_DISCOVERY:-0}"

if ! [[ "$max_age_hours" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
  echo "INVS_PROJECTION_MAX_AGE_HOURS must be a non-negative number" >&2
  exit 2
fi
case "$scope" in
  all|market) ;;
  *)
    echo "INVS_PROJECTION_SCOPE must be 'all' or 'market' (got: $scope)" >&2
    exit 2
    ;;
esac

run_sql() {
  local sql="$1"
  docker compose exec -T postgres sh -c \
    'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -F "|" -Atc "$1"' \
    sh "$sql"
}

is_stale_age() {
  local age="$1"
  awk -v age="$age" -v threshold="$max_age_hours" 'BEGIN { exit !(age < 0 || age > threshold) }'
}

docker compose up -d --wait postgres >/dev/null

echo "projection_freshness_check=$(date -u +%Y-%m-%dT%H:%M:%SZ) scope=$scope max_age_hours=$max_age_hours"

stale=0

expected_sql=$(cat <<'SQL'
WITH chosen_calendar AS (
    SELECT data_source_id, mic, calendar_version, exchange_timezone
    FROM calendar_manifests
    WHERE mic = 'XNAS'
    ORDER BY available_at DESC, recorded_at DESC, id DESC
    LIMIT 1
), expected AS (
    SELECT session.session_date, session.close_at
    FROM chosen_calendar chosen
    JOIN trading_sessions session
      ON session.data_source_id = chosen.data_source_id
     AND session.mic = chosen.mic
     AND session.calendar_version = chosen.calendar_version
     AND session.exchange_timezone = chosen.exchange_timezone
    WHERE session.session_status = 'open'
      AND session.close_at IS NOT NULL
      AND session.close_at <= now()
    ORDER BY session.session_date DESC
    LIMIT 1
)
SELECT COALESCE(session_date::text, ''),
       COALESCE(EXTRACT(EPOCH FROM (now() - close_at)) / 3600, -1)::numeric(12,2)
FROM expected
SQL
)
expected_row=$(run_sql "$expected_sql")
IFS='|' read -r expected_session expected_session_age_hours <<< "$expected_row"
if [[ -z "${expected_session:-}" ]]; then
  echo "projection_stale projection=calendar reason=no_completed_xnas_session"
  echo "projection_freshness=failed stale_count=1"
  exit 1
fi

market_sql=$(cat <<'SQL'
SELECT 'price',
       COALESCE((max(observed_at) AT TIME ZONE 'America/New_York')::date::text, ''),
       COALESCE(EXTRACT(EPOCH FROM (now() - max(projected_at))) / 3600, -1)::numeric(12,2)
FROM market_price_snapshots
UNION ALL
SELECT 'forward',
       COALESCE(max(market_session)::text, ''),
       COALESCE(EXTRACT(EPOCH FROM (now() - max(decision_at))) / 3600, -1)::numeric(12,2)
FROM discovery_latest_rankings
UNION ALL
SELECT 'history_rankings',
       COALESCE(max(market_session)::text, ''),
       COALESCE(EXTRACT(EPOCH FROM (now() - max(reconstructed_at))) / 3600, -1)::numeric(12,2)
FROM discovery_backfill_rankings_history
UNION ALL
SELECT 'history_prices',
       COALESCE(max(market_session)::text, ''),
       COALESCE(EXTRACT(EPOCH FROM (now() - max(reconstructed_at))) / 3600, -1)::numeric(12,2)
FROM discovery_backfill_prices_history
SQL
)
market_rows=$(run_sql "$market_sql")
enforce_market_age=0
if awk -v age="${expected_session_age_hours:--1}" -v threshold="$max_age_hours" 'BEGIN { exit !(age >= 0 && age <= threshold) }'; then
  enforce_market_age=1
fi

while IFS='|' read -r projection latest_session age_hours; do
  [[ -z "$projection" ]] && continue
  echo "projection=$projection latest_session=${latest_session:-none} expected_session=$expected_session age_hours=${age_hours:--1}"
  if [[ "$latest_session" != "$expected_session" ]]; then
    echo "projection_stale projection=$projection reason=session_behind latest_session=${latest_session:-none} expected_session=$expected_session"
    stale=$((stale + 1))
    continue
  fi
  if (( enforce_market_age == 1 )) || { [[ "$require_recent_discovery" == 1 ]] && [[ "$projection" != price ]]; }; then
    if is_stale_age "${age_hours:--1}"; then
      echo "projection_stale projection=$projection reason=age_exceeded age_hours=${age_hours:--1} max_age_hours=$max_age_hours"
      stale=$((stale + 1))
    fi
  fi
done <<< "$market_rows"

if [[ "$scope" == all ]]; then
  macro_age=$(run_sql "SELECT COALESCE(EXTRACT(EPOCH FROM (now() - max(projected_at))) / 3600, -1)::numeric(12,2) FROM macro_observation_snapshots")
  echo "projection=macro age_hours=${macro_age:--1}"
  if is_stale_age "${macro_age:--1}"; then
    echo "projection_stale projection=macro reason=age_exceeded age_hours=${macro_age:--1} max_age_hours=$max_age_hours"
    stale=$((stale + 1))
  fi
fi

if (( stale != 0 )); then
  echo "projection_freshness=failed stale_count=$stale expected_session=$expected_session max_age_hours=$max_age_hours"
  exit 1
fi

echo "projection_freshness=ok scope=$scope expected_session=$expected_session max_age_hours=$max_age_hours"
