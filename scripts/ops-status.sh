#!/usr/bin/env bash
set -euo pipefail

data_root="${INVS_DATA_DIR:-data}"
stale_after_hours="${INVS_STALE_AFTER_HOURS:-26}"
projection_after_hours="${INVS_PROJECTION_AFTER_HOURS:-26}"
disk_warn_percent="${INVS_DISK_WARN_PERCENT:-85}"

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
echo "thresholds stale_hours=$stale_after_hours projection_hours=$projection_after_hours disk_warn_percent=$disk_warn_percent"

issues=0
source_sql=$(cat <<'SQL'
SELECT s.code,
       CASE WHEN s.enabled THEN 'enabled' ELSE 'disabled' END,
       COALESCE(EXTRACT(EPOCH FROM (now() - max(r.started_at))) / 3600, -1)::numeric(12,2),
       count(*) FILTER (WHERE r.status IN ('queued', 'running')),
       count(*) FILTER (
         WHERE r.status IN ('failed', 'partial')
           AND r.started_at >= now() - interval '24 hours'
       )
FROM data_sources s
LEFT JOIN ingestion_runs r ON r.data_source_id = s.id
GROUP BY s.id, s.code, s.enabled
ORDER BY s.code
SQL
)
source_rows=$(run_sql "$source_sql")
if [[ -n "$source_rows" ]]; then
  while IFS='|' read -r code enabled age_hours active_runs troubled_runs; do
    echo "source=$code enabled=$enabled age_hours=$age_hours active_runs=$active_runs troubled_runs_24h=$troubled_runs"
    if [[ "$enabled" == enabled ]]; then
      if awk -v age="$age_hours" -v threshold="$stale_after_hours" 'BEGIN { exit !(age < 0 || age > threshold) }'; then
        echo "attention=source_stale source=$code age_hours=$age_hours"
        issues=1
      fi
      if (( active_runs > 0 )); then
        echo "attention=source_active_runs source=$code count=$active_runs"
        issues=1
      fi
      if (( troubled_runs > 0 )); then
        echo "attention=source_failures source=$code count_24h=$troubled_runs"
        issues=1
      fi
    fi
  done <<< "$source_rows"
fi

projection_sql=$(cat <<'SQL'
SELECT 'price',
       COALESCE(EXTRACT(EPOCH FROM (now() - max(projected_at))) / 3600, -1)::numeric(12,2)
FROM market_price_snapshots
UNION ALL
SELECT 'macro',
       COALESCE(EXTRACT(EPOCH FROM (now() - max(projected_at))) / 3600, -1)::numeric(12,2)
FROM macro_observation_snapshots
SQL
)
projection_rows=$(run_sql "$projection_sql")
while IFS='|' read -r projection age_hours; do
  echo "projection=$projection age_hours=$age_hours"
  if awk -v age="$age_hours" -v threshold="$projection_after_hours" 'BEGIN { exit !(age < 0 || age > threshold) }'; then
    echo "attention=projection_stale projection=$projection age_hours=$age_hours"
    issues=1
  fi
done <<< "$projection_rows"

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

if (( issues != 0 )); then
  echo "operational_status=attention"
  exit 1
fi

echo "operational_status=ok"
