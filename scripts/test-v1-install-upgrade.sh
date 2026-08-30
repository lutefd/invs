#!/usr/bin/env bash
set -uo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

acceptance_root="$repo_root/data/research/acceptance/v1"
run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
run_root="$acceptance_root/install-upgrade-$run_id"
log_root="$run_root/logs"
report_path="$acceptance_root/v1-install-upgrade.json"
temporary_root=$(mktemp -d)
project="invs-v1-install-$PPID-$$"
restore_db="restore_v1_install_${run_id//[^A-Za-z0-9]/_}"
overall=0
steps_file="$run_root/steps.tsv"

mkdir -p "$log_root"

compose=(docker compose --progress quiet -p "$project")
export COMPOSE_PROJECT_NAME="$project"
export INVS_BIND_ADDRESS=127.0.0.1
export POSTGRES_PORT=0
export POSTGRES_DB="v1_install_acceptance"
export POSTGRES_USER="v1_install_acceptance"
export POSTGRES_PASSWORD="v1_install_acceptance_local"
export INVS_CONFIG_FILE="$repo_root/config/config.example.yaml"

cleanup() {
	"${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
	rm -rf -- "$temporary_root"
}
trap cleanup EXIT

record_step() {
	local name=$1
	local step_status=$2
	local exit_code=$3
	local duration_seconds=$4
	local log_path=$5
	printf '%s\t%s\t%s\t%s\t%s\n' "$name" "$step_status" "$exit_code" "$duration_seconds" "$log_path" >> "$steps_file"
}

run_step() {
	local name=$1
	shift
	local log_path="$log_root/$name.log"
	local started finished exit_code
	started=$(date +%s)
	if "$@" >"$log_path" 2>&1; then
		exit_code=0
	else
		exit_code=$?
	fi
	finished=$(date +%s)
	if (( exit_code == 0 )); then
		record_step "$name" passed "$exit_code" "$((finished - started))" "${log_path#"$repo_root/"}"
	else
		record_step "$name" failed "$exit_code" "$((finished - started))" "${log_path#"$repo_root/"}"
		overall=1
	fi
}

postgres_query() {
	local query=$1
	"${compose[@]}" exec -T postgres sh -c \
		'psql -v ON_ERROR_STOP=1 -X -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "$1"' \
		sh "$query"
}

postgres_query_db() {
	local database=$1
	local query=$2
	"${compose[@]}" exec -T postgres sh -c \
		'psql -v ON_ERROR_STOP=1 -X -U "$POSTGRES_USER" -d "$1" -Atc "$2"' \
		sh "$database" "$query"
}

assert_current_schema() {
	local schema_state
	schema_state=$(postgres_query "
		SELECT CASE WHEN
			to_regclass('public.issuers') IS NOT NULL
			AND to_regclass('public.market_price_snapshots') IS NOT NULL
			AND to_regclass('public.macro_observation_snapshots') IS NOT NULL
			AND to_regclass('public.security_identifier_versions') IS NOT NULL
			AND to_regclass('public.corporate_action_versions') IS NOT NULL
			AND to_regclass('public.feature_artifacts') IS NOT NULL
			AND to_regclass('public.feature_artifact_input_fitness') IS NOT NULL
			AND to_regclass('public.research_entities') IS NOT NULL
			AND to_regclass('public.research_theme_indicators') IS NOT NULL
			AND to_regclass('public.backtest_experiments') IS NOT NULL
			AND to_regclass('public.paper_accounts') IS NOT NULL
			AND (
				SELECT count(*) FROM information_schema.columns
				WHERE table_schema = 'public'
				  AND table_name IN ('market_price_snapshots', 'macro_observation_snapshots')
				  AND column_name = 'observed_precision'
			) = 2
			AND EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = 'public'
				  AND table_name = 'research_hypothesis_revisions'
				  AND column_name = 'review_at'
			)
			AND EXISTS (
				SELECT 1 FROM pg_constraint
				WHERE conrelid = 'public.market_price_snapshots'::regclass
				  AND conname = 'market_price_snapshots_price_basis_check'
			)
		THEN 'ready' ELSE 'incomplete' END;") || return 1
	if [[ "$schema_state" != "ready" ]]; then
		printf 'current schema state: %s\n' "$schema_state" >&2
		return 1
	fi
}

fresh_install() {
	"${compose[@]}" up -d --wait --build postgres || return 1
	assert_current_schema || return 1
	if ! "${compose[@]}" logs --no-color postgres | grep -q 'PostgreSQL init process complete'; then
		printf '%s\n' 'fresh Compose project did not reach the PostgreSQL initialization marker' >&2
		return 1
	fi
}

prepare_upgrade_state() {
	local latest_tables
	postgres_query "
		DROP VIEW IF EXISTS paper_account_status;
		DROP TABLE IF EXISTS paper_account_events, paper_accounts CASCADE;
		DROP VIEW IF EXISTS backtest_run_status;
		DROP TABLE IF EXISTS backtest_run_events, backtest_runs,
			backtest_experiment_inputs, backtest_experiments CASCADE;" || return 1
	latest_tables=$(postgres_query "
		SELECT coalesce(to_regclass('public.backtest_experiments')::text, '') || '|' ||
			coalesce(to_regclass('public.paper_accounts')::text, '') || '|' ||
			coalesce(to_regclass('public.research_entities')::text, '');") || return 1
	if [[ "$latest_tables" != "||research_entities" ]]; then
		printf 'upgrade baseline state: %s\n' "$latest_tables" >&2
		return 1
	fi
}

upgrade_from_pre_v1() {
	prepare_upgrade_state || return 1
	env INVS_BIND_ADDRESS=127.0.0.1 make migrate || return 1
	assert_current_schema || return 1
}

reapply_migrations() {
	env INVS_BIND_ADDRESS=127.0.0.1 make migrate || return 1
	assert_current_schema || return 1
}

backup_restore() {
	local source_root="$temporary_root/source-data"
	local backup_root="$temporary_root/backup"
	local restore_root="$temporary_root/restored"
	local restored_schema restored_payload
	mkdir -p "$source_root/raw/source=acceptance" "$source_root/research"
	printf '%s\n' 'v1 install upgrade backup fixture' > "$source_root/raw/source=acceptance/part.txt"
	printf '%s\n' '{"status":"fixture"}' > "$source_root/research/report.json"

	env INVS_DATA_DIR="$source_root" INVS_BIND_ADDRESS=127.0.0.1 \
		make backup BACKUP_DIR="$backup_root" || return 1
	env INVS_BIND_ADDRESS=127.0.0.1 make backup-validate BACKUP_DIR="$backup_root" || return 1
	env INVS_BIND_ADDRESS=127.0.0.1 make restore \
		BACKUP_DIR="$backup_root" RESTORE_DIR="$restore_root" RESTORE_DB="$restore_db" || return 1

	cmp -s "$source_root/raw/source=acceptance/part.txt" \
		"$restore_root/data/raw/source=acceptance/part.txt" || return 1
	cmp -s "$source_root/research/report.json" \
		"$restore_root/data/research/report.json" || return 1
	restored_payload=$(postgres_query_db "$restore_db" \
		"SELECT to_regclass('public.paper_accounts')::text || '|' || to_regclass('public.backtest_experiments')::text;") || return 1
	restored_schema="paper_accounts|backtest_experiments"
	if [[ "$restored_payload" != "$restored_schema" ]]; then
		printf 'restored schema state: %s\n' "$restored_payload" >&2
		return 1
	fi
}

backup_fixture() {
	make backup-restore-acceptance || return 1
}

interrupted_recovery() {
	make v1-daily-cycle-acceptance || return 1
}

run_step fresh-install fresh_install
run_step upgrade-from-pre-v1 upgrade_from_pre_v1
run_step reapply-migrations reapply_migrations
run_step backup-restore backup_restore
run_step backup-fixture backup_fixture
run_step interrupted-recovery interrupted_recovery

steps_json=$(jq -R -s '
	split("\n")
	| map(select(length > 0) | split("\t") | {
		name: .[0],
		status: .[1],
		exit_code: (.[2] | tonumber),
		duration_seconds: (.[3] | tonumber),
		log_path: .[4]
	})
' "$steps_file")

mkdir -p "$(dirname "$report_path")"
report_temporary="$report_path.tmp.$$"
jq -n \
	--arg schema_version '1.0.0' \
	--arg status "$(if (( overall == 0 )); then printf passed; else printf failed; fi)" \
	--arg release_commit "$(git rev-parse --verify HEAD 2>/dev/null || printf unknown)" \
	--arg generated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
	--arg run_root "${run_root#"$repo_root/"}" \
	--arg restore_database "$restore_db" \
	--arg compose_project "$project" \
	--argjson steps "$steps_json" \
	'{
		"schema_version": $schema_version,
		"status": $status,
		"release_commit": $release_commit,
		"generated_at": $generated_at,
		"steps": $steps,
		"scenarios": {
			"fresh_install": ([ $steps[] | select(.name == "fresh-install") | .status ] | .[0] == "passed"),
			"upgrade_from_pre_v1_schema": ([ $steps[] | select(.name == "upgrade-from-pre-v1") | .status ] | .[0] == "passed"),
			"idempotent_migration_reapply": ([ $steps[] | select(.name == "reapply-migrations") | .status ] | .[0] == "passed"),
			"isolated_backup_restore": ([ $steps[] | select(.name == "backup-restore") | .status ] | .[0] == "passed"),
			"backup_tamper_rejection": ([ $steps[] | select(.name == "backup-fixture") | .status ] | .[0] == "passed"),
			"interrupted_daily_cycle_recovery": ([ $steps[] | select(.name == "interrupted-recovery") | .status ] | .[0] == "passed")
		},
		"evidence": {
			"run_root": $run_root,
			"restore_database": $restore_database,
			"compose_project": $compose_project
		},
		"limitations": [
			"The upgrade rehearsal constructs the accepted pre-v1 boundary by removing the additive backtest and paper schemas from a fresh database, then runs the current make migrate path.",
			"The database, volume, and restore database are disposable local Docker evidence and are removed on exit.",
			"The interrupted-recovery stage is the deterministic CLI failure/resume proof; it does not simulate a host power loss."
		]
	}' > "$report_temporary"
mv -- "$report_temporary" "$report_path"

if (( overall == 0 )); then
	printf '%s\n' 'v1 install, upgrade, backup, restore, and interrupted-recovery acceptance passed'
	printf 'report: %s\n' "$report_path"
	exit 0
fi

printf '%s\n' 'v1 install, upgrade, backup, restore, and interrupted-recovery acceptance failed' >&2
printf 'report: %s\n' "$report_path" >&2
exit 1
