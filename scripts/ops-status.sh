#!/usr/bin/env bash
set -euo pipefail

data_root="${INVS_DATA_DIR:-data}"
stale_after_hours="${INVS_STALE_AFTER_HOURS:-26}"
projection_after_hours="${INVS_PROJECTION_AFTER_HOURS:-26}"
disk_warn_percent="${INVS_DISK_WARN_PERCENT:-85}"
backup_after_hours="${INVS_BACKUP_AFTER_HOURS:-30}"

validate_number() {
  local name="$1"
  local value="$2"
  if ! [[ "$value" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
    echo "$name must be a non-negative number" >&2
    exit 2
  fi
}

validate_number INVS_STALE_AFTER_HOURS "$stale_after_hours"
validate_number INVS_PROJECTION_AFTER_HOURS "$projection_after_hours"
validate_number INVS_DISK_WARN_PERCENT "$disk_warn_percent"
validate_number INVS_BACKUP_AFTER_HOURS "$backup_after_hours"
if ! awk -v value="$disk_warn_percent" 'BEGIN { exit !(value <= 100) }'; then
  echo "INVS_DISK_WARN_PERCENT must be at most 100" >&2
  exit 2
fi

if [[ ! -d "$data_root" ]]; then
  echo "data root does not exist: $data_root" >&2
  exit 2
fi

run_sql() {
  local sql="$1"
  docker compose exec -T postgres sh -c \
    'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -F "|" -Atc "$1"' \
    sh "$sql"
}

docker compose up -d --wait postgres >/dev/null

echo "operational_status_check=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "thresholds stale_hours=$stale_after_hours projection_hours=$projection_after_hours disk_warn_percent=$disk_warn_percent backup_after_hours=$backup_after_hours"

issues=0
source_sql=$(cat <<'SQL'
WITH latest_success AS (
    SELECT data_source_id, max(started_at) AS started_at
    FROM ingestion_runs
    WHERE status = 'succeeded'
    GROUP BY data_source_id
)
SELECT s.code,
       CASE WHEN s.enabled THEN 'enabled' ELSE 'disabled' END,
       COALESCE(EXTRACT(EPOCH FROM (now() - latest_success.started_at)) / 3600, -1)::numeric(12,2),
       count(*) FILTER (WHERE r.status IN ('queued', 'running')),
       count(*) FILTER (
         WHERE r.status IN ('failed', 'partial')
           AND r.started_at >= now() - interval '24 hours'
           AND (latest_success.started_at IS NULL OR r.started_at > latest_success.started_at)
       )
FROM data_sources s
LEFT JOIN ingestion_runs r ON r.data_source_id = s.id
LEFT JOIN latest_success ON latest_success.data_source_id = s.id
GROUP BY s.id, s.code, s.enabled, latest_success.started_at
ORDER BY s.code
SQL
)
source_rows=$(run_sql "$source_sql")
if [[ -n "$source_rows" ]]; then
  while IFS='|' read -r code enabled age_hours active_runs unresolved_troubled_runs; do
    echo "source=$code enabled=$enabled age_hours=$age_hours unresolved_troubled_runs_24h=$unresolved_troubled_runs"
    if [[ "$enabled" == enabled ]]; then
      # Yahoo price freshness is checked against the latest completed XNAS
      # session below. Wall-clock age is deliberately not authoritative for a
      # market that closes on weekends and exchange holidays.
      if [[ "$code" != yahoo ]] && awk -v age="$age_hours" -v threshold="$stale_after_hours" 'BEGIN { exit !(age < 0 || age > threshold) }'; then
        echo "attention=source_stale source=$code age_hours=$age_hours"
        issues=1
      elif [[ "$code" == yahoo ]]; then
        echo "source_freshness_policy=yahoo basis=xnas_completed_session"
      fi
      if (( active_runs > 0 )); then
        echo "attention=source_active_runs source=$code count=$active_runs"
        issues=1
      fi
      if (( unresolved_troubled_runs > 0 )); then
        echo "attention=source_failures source=$code count_24h=$unresolved_troubled_runs"
        issues=1
      fi
    fi
  done <<< "$source_rows"
fi

price_projection_sql=$(cat <<'SQL'
WITH chosen_calendar AS (
    SELECT data_source_id, mic, calendar_version, exchange_timezone
    FROM calendar_manifests
    WHERE mic = 'XNAS'
    ORDER BY available_at DESC, recorded_at DESC, id DESC
    LIMIT 1
), expected AS (
    SELECT max(session.session_date) AS session_date
    FROM chosen_calendar chosen
    JOIN trading_sessions session
      ON session.data_source_id = chosen.data_source_id
     AND session.mic = chosen.mic
     AND session.calendar_version = chosen.calendar_version
     AND session.exchange_timezone = chosen.exchange_timezone
    WHERE session.session_status = 'open'
      AND session.close_at IS NOT NULL
      AND session.close_at <= now()
), latest AS (
    SELECT (max(observed_at) AT TIME ZONE 'America/New_York')::date AS session_date,
           COALESCE(EXTRACT(EPOCH FROM (now() - max(projected_at))) / 3600, -1)::numeric(12,2) AS age_hours
    FROM market_price_snapshots
)
SELECT COALESCE(expected.session_date::text, ''),
       COALESCE(latest.session_date::text, ''),
       latest.age_hours
FROM expected CROSS JOIN latest
SQL
)
price_projection_row=$(run_sql "$price_projection_sql")
IFS='|' read -r expected_price_session latest_price_session price_projection_age <<< "$price_projection_row"
echo "projection=price age_hours=${price_projection_age:--1} latest_session=${latest_price_session:-none} expected_session=${expected_price_session:-none} freshness_basis=xnas_completed_session"
if [[ -z "${expected_price_session:-}" || "$latest_price_session" != "$expected_price_session" ]]; then
  echo "attention=projection_stale projection=price latest_session=${latest_price_session:-none} expected_session=${expected_price_session:-none}"
  issues=1
fi

# Collection freshness alone cannot prove that dashboard publication ran.
discovery_rows=$(run_sql "SELECT 'forward', COALESCE(max(market_session)::text, '') FROM discovery_latest_rankings UNION ALL SELECT 'history_rankings', COALESCE(max(market_session)::text, '') FROM discovery_backfill_rankings_history UNION ALL SELECT 'history_prices', COALESCE(max(market_session)::text, '') FROM discovery_backfill_prices_history")
while IFS='|' read -r projection latest_session; do
  echo "projection=$projection latest_session=${latest_session:-none} expected_session=${expected_price_session:-none}"
  if [[ -z "${expected_price_session:-}" || "$latest_session" != "$expected_price_session" ]]; then
    echo "attention=projection_stale projection=$projection latest_session=${latest_session:-none} expected_session=${expected_price_session:-none}"
    issues=1
  fi
done <<< "$discovery_rows"

macro_projection_sql=$(cat <<'SQL'
SELECT COALESCE(EXTRACT(EPOCH FROM (now() - max(projected_at))) / 3600, -1)::numeric(12,2)
FROM macro_observation_snapshots
SQL
)
macro_projection_age=$(run_sql "$macro_projection_sql")
echo "projection=macro age_hours=$macro_projection_age"
if awk -v age="$macro_projection_age" -v threshold="$projection_after_hours" 'BEGIN { exit !(age < 0 || age > threshold) }'; then
  echo "attention=projection_stale projection=macro age_hours=$macro_projection_age"
  issues=1
fi

disk_row=$(df -P -k -- "$data_root" | awk 'NR == 2 { gsub(/%/, "", $5); print $5 "|" $4 }')
if [[ -z "$disk_row" ]]; then
  echo "could not determine disk usage for $data_root" >&2
  exit 2
fi
IFS='|' read -r disk_used_percent disk_free_kib <<< "$disk_row"
echo "disk path=$data_root used_percent=$disk_used_percent free_kib=$disk_free_kib"
if awk -v used="$disk_used_percent" -v threshold="$disk_warn_percent" 'BEGIN { exit !(used >= threshold) }'; then
  echo "attention=disk_headroom used_percent=$disk_used_percent"
  issues=1
fi

backup_root="${INVS_BACKUP_ROOT:-${INVS_BACKUP_DIR:-}}"
if [[ -z "$backup_root" ]]; then
  echo "backup_check=not_configured"
else
  if [[ "$backup_root" == "/" || "$backup_root" == "/home" || "$backup_root" == "/tmp" ]]; then
    echo "attention=backup_path_is_too_broad"
    issues=1
  elif [[ -f "$backup_root/backup-manifest.txt" ]]; then
    latest_backup="$backup_root"
  elif [[ -d "$backup_root" ]]; then
    latest_entry=$(find "$backup_root" -mindepth 1 -maxdepth 1 -type f -name backup-manifest.txt -printf '%T@|%h\n' | sort -nr | head -n 1)
    if [[ -z "$latest_entry" ]]; then
      latest_entry=$(find "$backup_root" -mindepth 1 -maxdepth 2 -type f -name backup-manifest.txt -printf '%T@|%h\n' | sort -nr | head -n 1)
    fi
    latest_backup=${latest_entry#*|}
  fi
  if [[ -z "${latest_backup:-}" || ! -f "$latest_backup/backup-manifest.txt" ]]; then
    echo "attention=backup_missing path=$backup_root"
    issues=1
  else
    backup_mtime=$(stat -c '%Y' -- "$latest_backup/backup-manifest.txt")
    backup_age_seconds=$(( $(date +%s) - backup_mtime ))
    (( backup_age_seconds < 0 )) && backup_age_seconds=0
    backup_age_hours=$(awk -v seconds="$backup_age_seconds" 'BEGIN { printf "%.2f", seconds / 3600 }')
    echo "backup=present age_hours=$backup_age_hours"
    if awk -v age="$backup_age_hours" -v threshold="$backup_after_hours" 'BEGIN { exit !(age > threshold) }'; then
      echo "attention=backup_stale age_hours=$backup_age_hours"
      issues=1
    fi
  fi
fi

if (( issues != 0 )); then
  echo "operational_status=attention"
  exit 1
fi

echo "operational_status=ok"
