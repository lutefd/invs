#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

compose=(docker compose --progress quiet)
acceptance_root="$repo_root/data/research/acceptance/v0.5"
acceptance_db="invs_v05_acceptance_$$"

collector_database_url=$("${compose[@]}" --profile collect config --format json | jq -r '.services.collector.environment.DATABASE_URL')
database_base=${collector_database_url%%\?*}
database_query=""
if [[ "$collector_database_url" == *\?* ]]; then
	database_query="?${collector_database_url#*\?}"
fi
acceptance_database_url="${database_base%/*}/${acceptance_db}${database_query}"

psql_acceptance() {
	"${compose[@]}" exec -T postgres sh -c 'psql -v ON_ERROR_STOP=1 -X -U "$POSTGRES_USER" -d "$1" -At' sh "$acceptance_db"
}

psql_value() {
	"${compose[@]}" exec -T postgres sh -c 'psql -v ON_ERROR_STOP=1 -X -U "$POSTGRES_USER" -d "$1" -Atc "$2"' sh "$acceptance_db" "$1"
}

research_cli_file() {
	local operation=$1
	local input_path=$2
	"${compose[@]}" --profile collect run --rm -T \
		-e "DATABASE_URL=$acceptance_database_url" \
		--entrypoint invs-research collector --operation "$operation" < "$input_path"
}

cleanup() {
	"${compose[@]}" exec -T postgres sh -c 'dropdb -U "$POSTGRES_USER" "$1"' sh "$acceptance_db" >/dev/null 2>&1 || true
}

mkdir -p "$acceptance_root"
"${compose[@]}" exec -T postgres sh -c 'createdb -U "$POSTGRES_USER" "$1"' sh "$acceptance_db"
trap cleanup EXIT

for migration in "$repo_root"/migrations/*.up.sql; do
	"${compose[@]}" exec -T postgres sh -c 'psql -v ON_ERROR_STOP=1 -X -U "$POSTGRES_USER" -d "$1"' sh "$acceptance_db" < "$migration" >/dev/null
done

"${compose[@]}" --profile collect build collector >/dev/null

registration_result=$(research_cli_file register-backtest-experiment "$repo_root/fixtures/research/backtest-experiment-registration.json")
jq -e '.already_present == false and (.registration_sha256 | test("^[0-9a-f]{64}$"))' <<<"$registration_result" >/dev/null
registration_repeat=$(research_cli_file register-backtest-experiment "$repo_root/fixtures/research/backtest-experiment-registration.json")
jq -e '.already_present == true' <<<"$registration_repeat" >/dev/null

run_result=$(research_cli_file start-backtest-run "$repo_root/fixtures/research/backtest-run.json")
jq -e '.already_present == false and .attempt == 1' <<<"$run_result" >/dev/null
run_repeat=$(research_cli_file start-backtest-run "$repo_root/fixtures/research/backtest-run.json")
jq -e '.already_present == true' <<<"$run_repeat" >/dev/null

completion_result=$(research_cli_file backtest-run-event "$repo_root/fixtures/research/backtest-completed-event.json")
jq -e '.already_present == false and .sequence == 1' <<<"$completion_result" >/dev/null
completion_repeat=$(research_cli_file backtest-run-event "$repo_root/fixtures/research/backtest-completed-event.json")
jq -e '.already_present == true' <<<"$completion_repeat" >/dev/null

retry_run_result=$(research_cli_file start-backtest-run "$repo_root/fixtures/research/backtest-retry-run.json")
jq -e '.already_present == false and .attempt == 2' <<<"$retry_run_result" >/dev/null
failed_result=$(research_cli_file backtest-run-event "$repo_root/fixtures/research/backtest-failed-event.json")
jq -e '.already_present == false and .sequence == 1' <<<"$failed_result" >/dev/null

research_cli_file backtest-report "$repo_root/fixtures/research/backtest-report.json" > "$acceptance_root/backtest-report.json"
jq -e '
  .schema_version == "1.0.0" and
  .experiment.experiment_id == "10000000-0000-4000-8000-000000000001" and
  (.inputs | length == 3) and
  ([.inputs[].kind] | sort == ["calendar", "membership", "prices"]) and
  ([.runs[].status] | sort == ["completed", "failed"]) and
  ([.runs[].holdout_attempted] | all) and
  ([.runs[].last_event_sequence] | sort == [1, 1])
' "$acceptance_root/backtest-report.json" >/dev/null

psql_acceptance <<'SQL' >/dev/null
DO $$
DECLARE
    update_succeeded boolean := false;
BEGIN
    BEGIN
        UPDATE backtest_experiments
        SET spec_path = 'mutated/spec.json'
        WHERE experiment_id = '10000000-0000-4000-8000-000000000001';
        update_succeeded := true;
    EXCEPTION WHEN raise_exception THEN
        NULL;
    END;
    IF update_succeeded THEN
        RAISE EXCEPTION 'append-only backtest experiment update was accepted';
    END IF;
END;
$$;

DO $$
DECLARE
    insert_succeeded boolean := false;
BEGIN
    BEGIN
        INSERT INTO backtest_run_events (
            run_id, sequence, status, event_at, record_hash
        ) VALUES (
            '20000000-0000-4000-8000-000000000001', 3, 'running',
            '2026-01-02T23:00:00Z',
            'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
        );
        insert_succeeded := true;
    EXCEPTION WHEN raise_exception THEN
        NULL;
    END;
    IF insert_succeeded THEN
        RAISE EXCEPTION 'backtest run event sequence gap was accepted';
    END IF;
END;
$$;

DO $$
DECLARE
    insert_succeeded boolean := false;
BEGIN
    BEGIN
        INSERT INTO backtest_run_events (
            run_id, sequence, status, event_at, record_hash
        ) VALUES (
            '20000000-0000-4000-8000-000000000001', 2, 'running',
            '2026-01-02T23:00:00Z',
            'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
        );
        insert_succeeded := true;
    EXCEPTION WHEN raise_exception THEN
        NULL;
    END;
    IF insert_succeeded THEN
        RAISE EXCEPTION 'post-terminal run event was accepted';
    END IF;
END;
$$;

DO $$
DECLARE
    update_succeeded boolean := false;
BEGIN
    BEGIN
        UPDATE backtest_run_events
        SET failure_message = 'mutated'
        WHERE run_id = '20000000-0000-4000-8000-000000000002' AND sequence = 1;
        update_succeeded := true;
    EXCEPTION WHEN raise_exception THEN
        NULL;
    END;
    IF update_succeeded THEN
        RAISE EXCEPTION 'append-only backtest run event update was accepted';
    END IF;
END;
$$;
SQL

counts=$(psql_value "SELECT (SELECT count(*) FROM backtest_experiments) || '|' || (SELECT count(*) FROM backtest_experiment_inputs) || '|' || (SELECT count(*) FROM backtest_runs) || '|' || (SELECT count(*) FROM backtest_run_events)")
if [[ "$counts" != "1|3|2|4" ]]; then
	echo "unexpected v0.5 backtest acceptance counts: $counts" >&2
	exit 1
fi

statuses=$(psql_value "SELECT string_agg(status, ',' ORDER BY run_id) FROM backtest_run_status")
if [[ "$statuses" != "completed,failed" ]]; then
	echo "unexpected v0.5 backtest statuses: $statuses" >&2
	exit 1
fi

printf '%s\n' "v0.5 backtest metadata acceptance passed"
printf '%s\n' "artifacts: $acceptance_root"
