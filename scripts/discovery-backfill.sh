#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
cd "$repo_root"

months=${MONTHS:-3}
start_date=${START_DATE:-}
end_date=${END_DATE:-}
profile=${PROFILE:-config/universes/nasdaq-100-starter.yaml}
output_root=${OUTPUT_ROOT:-/data/research/discovery/retrospective-current-universe}
runtime_dir="$repo_root/.runtime/discovery-backfill"
calendar_snapshot="$runtime_dir/calendar-snapshot.json"
expected_sessions=${EXPECTED_SESSION_COUNT:-}
expected_start_date=${EXPECTED_START_DATE:-}
expected_end_date=${EXPECTED_END_DATE:-}

if ! [[ "$months" =~ ^[1-9][0-9]*$ ]]; then
  echo "MONTHS must be a positive integer" >&2
  exit 2
fi
case "$output_root" in
  /data/*) ;;
  *) echo "OUTPUT_ROOT must be under /data so the container can persist it" >&2; exit 2 ;;
esac
case "$profile" in
  /*) profile_path=$profile ;;
  *) profile_path="$repo_root/$profile" ;;
esac
if [[ ! -f "$profile_path" ]]; then
  echo "discovery profile does not exist: $profile_path" >&2
  exit 2
fi

docker compose config --quiet
mkdir -p "$runtime_dir"
exec 9>"$runtime_dir/backfill.lock"
if ! flock -n 9; then
  echo "discovery_backfill=skipped reason=already_running" >&2
  exit 75
fi
umask 077
docker compose exec -T postgres sh -c \
  'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At' >"$calendar_snapshot" <<'SQL'
WITH chosen AS (
  SELECT *
  FROM calendar_manifests
  WHERE mic = 'XNAS'
  ORDER BY available_at DESC, recorded_at DESC, id DESC
  LIMIT 1
), sessions AS (
  SELECT
    mic, exchange_timezone, session_date, session_status, open_at, close_at,
    is_early_close, available_at, revision
  FROM trading_sessions
  WHERE data_source_id = (SELECT data_source_id FROM chosen)
    AND mic = (SELECT mic FROM chosen)
    AND calendar_version = (SELECT calendar_version FROM chosen)
)
SELECT json_build_object(
  'manifest', json_build_object(
    'data_source_id', chosen.data_source_id,
    'mic', chosen.mic,
    'exchange_timezone', chosen.exchange_timezone,
    'calendar_version', chosen.calendar_version,
    'session_fingerprint', chosen.session_fingerprint,
    'session_count', chosen.session_count,
    'available_at', chosen.available_at
  ),
  'sessions', COALESCE((
    SELECT json_agg(json_build_object(
      'mic', mic,
      'exchange_timezone', exchange_timezone,
      'session_date', session_date,
      'session_status', session_status,
      'open_at', open_at,
      'close_at', close_at,
      'is_early_close', is_early_close,
      'available_at', available_at,
      'revision', revision
    ) ORDER BY session_date, revision, available_at)
    FROM sessions
  ), '[]'::json)
)
FROM chosen;
SQL
if [[ ! -s "$calendar_snapshot" ]]; then
  echo "discovery-backfill=failed reason=no_xnas_calendar" >&2
  exit 1
fi

git_commit=$(git rev-parse --verify HEAD | tr '[:upper:]' '[:lower:]')
publish_args=(
  --profile /tmp/discovery-profile.yaml
  --calendar-snapshot /tmp/calendar-snapshot.json
  --data-root /data
  --output-root "$output_root"
  --months "$months"
  --git-commit "$git_commit"
)
if [[ -n "$start_date" ]]; then
  publish_args+=(--start-date "$start_date")
fi
if [[ -n "$end_date" ]]; then
  publish_args+=(--end-date "$end_date")
fi
result=$(docker compose run --rm --no-deps \
  -v "$profile_path:/tmp/discovery-profile.yaml:ro" \
  -v "$calendar_snapshot:/tmp/calendar-snapshot.json:ro" \
  jupyter python -m research.retrospective_discovery_cli publish "${publish_args[@]}")
printf '%s\n' "$result"
manifest=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["manifest_path"])' <<<"$result")
session_count=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["session_count"])' <<<"$result")
start_date=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["start_date"])' <<<"$result")
end_date=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["end_date"])' <<<"$result")
if [[ -n "$expected_sessions" && "$session_count" != "$expected_sessions" ]]; then
  echo "discovery_backfill=failed reason=session_count_mismatch expected=$expected_sessions actual=$session_count" >&2
  exit 1
fi
if [[ -n "$expected_start_date" && "$start_date" != "$expected_start_date" ]]; then
  echo "discovery_backfill=failed reason=start_date_mismatch expected=$expected_start_date actual=$start_date" >&2
  exit 1
fi
if [[ -n "$expected_end_date" && "$end_date" != "$expected_end_date" ]]; then
  echo "discovery_backfill=failed reason=end_date_mismatch expected=$expected_end_date actual=$end_date" >&2
  exit 1
fi

LOCAL_UID=$(id -u) LOCAL_GID=$(id -g) INVS_GIT_COMMIT="$git_commit" \
  docker compose --profile collect run --rm --build --no-deps \
    --entrypoint invs-discovery-backfill-catalog collector \
    --manifest "$manifest" \
    --discovery-root "$output_root"
