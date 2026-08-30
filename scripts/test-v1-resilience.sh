#!/usr/bin/env bash
set -uo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

acceptance_root="$repo_root/data/research/acceptance/v1"
run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
run_root="$acceptance_root/resilience-$run_id"
log_root="$run_root/logs"
report_path="$acceptance_root/v1-resilience.json"
temporary_root=$(mktemp -d)
restore_db="restore_v1_$(date -u +%Y%m%d%H%M%S)_$$"
restore_db_reserved=0
overall=0
steps_file="$run_root/steps.tsv"

mkdir -p "$log_root"

drop_restore_database() {
	if (( restore_db_reserved == 1 )); then
		docker compose exec -T postgres sh -c \
			'dropdb -U "$POSTGRES_USER" -- "$1"' sh "$restore_db" >/dev/null 2>&1 || true
	fi
	rm -rf -- "$temporary_root"
}
trap drop_restore_database EXIT

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

run_step security-check env INVS_BIND_ADDRESS=127.0.0.1 make security-check
run_step backup-fixture make backup-restore-acceptance
run_step historical-truth make historical-truth-db-test
run_step bias-and-backtest make backtest-reproduction
run_step paper-ledger make paper-reproduction
run_step daily-resume docker compose --progress quiet run --rm --no-deps \
	-v "$repo_root:/repo:ro" \
	-e PYTHONPATH=/workspace:/repo \
	jupyter sh -c "pip install -q -e '.[dev]' && python -m pytest /repo/python/tests/test_daily_cycle.py"

backup_dir="$temporary_root/backup"
restore_dir="$temporary_root/restored"
run_step backup-live env INVS_BIND_ADDRESS=127.0.0.1 make backup BACKUP_DIR="$backup_dir"
if [[ -d "$backup_dir" ]]; then
	run_step backup-live-validate make backup-validate BACKUP_DIR="$backup_dir"
	restore_db_reserved=1
	run_step restore-live env INVS_BIND_ADDRESS=127.0.0.1 make restore \
		BACKUP_DIR="$backup_dir" RESTORE_DIR="$restore_dir" RESTORE_DB="$restore_db"
	if [[ -d "$restore_dir" ]]; then
		collector_database_url=$(docker compose --profile collect config --format json | jq -r '.services.collector.environment.DATABASE_URL')
		restore_database_url="${collector_database_url%/*}/$restore_db?sslmode=disable"
		run_step restore-reconcile env INVS_BIND_ADDRESS=127.0.0.1 docker compose --progress quiet \
			--profile collect run --rm --build --no-deps \
			-v "$restore_dir/data:/restore-data:ro" \
			-e "DATABASE_URL=$restore_database_url" \
			collector reconcile --data-root /restore-data --fail-on-issues
	else
		record_step restore-reconcile skipped 0 0 "${run_root#"$repo_root/"}/restore-reconcile.log"
	fi
else
	record_step backup-live-validate skipped 0 0 "${run_root#"$repo_root/"}/backup-live-validate.log"
	record_step restore-live skipped 0 0 "${run_root#"$repo_root/"}/restore-live.log"
	record_step restore-reconcile skipped 0 0 "${run_root#"$repo_root/"}/restore-reconcile.log"
fi

backtest_report="$repo_root/data/research/acceptance/v0.5/reproduction/backtest-reproduction.json"
paper_report="$repo_root/data/research/acceptance/v0.6/reproduction/paper-reproduction.json"
backtest_status=failed
paper_status=failed
if [[ -f "$backtest_report" ]] && jq -e '.status == "passed" and .bias_audit.status == "passed" and .reproduction.manifest_equal and .reproduction.artifact_files_equal' "$backtest_report" >/dev/null 2>&1; then
	backtest_status=passed
else
	overall=1
fi
if [[ -f "$paper_report" ]] && jq -e '.status == "passed" and .acceptance.backup_restore and .acceptance.rebuild_exact and .acceptance.reconciliation' "$paper_report" >/dev/null 2>&1; then
	paper_status=passed
else
	overall=1
fi

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
backtest_sha256=missing
paper_sha256=missing
if [[ -f "$backtest_report" ]]; then
	backtest_sha256=$(sha256sum "$backtest_report" | awk '{print $1}')
fi
if [[ -f "$paper_report" ]]; then
	paper_sha256=$(sha256sum "$paper_report" | awk '{print $1}')
fi

mkdir -p "$(dirname "$report_path")"
report_temporary="$report_path.tmp.$$"
jq -n \
	--arg schema_version '1.0.0' \
	--arg status "$(if (( overall == 0 )); then printf passed; else printf failed; fi)" \
	--arg release_commit "$(git rev-parse --verify HEAD 2>/dev/null || printf unknown)" \
	--arg generated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
	--arg backtest_status "$backtest_status" \
	--arg paper_status "$paper_status" \
	--arg backtest_path "${backtest_report#"$repo_root/"}" \
	--arg backtest_sha256 "$backtest_sha256" \
	--arg paper_path "${paper_report#"$repo_root/"}" \
	--arg paper_sha256 "$paper_sha256" \
	--arg restore_database "$restore_db" \
	--argjson steps "$steps_json" \
	'{
		"schema_version": $schema_version,
		"status": $status,
		"release_commit": $release_commit,
		"generated_at": $generated_at,
		"steps": $steps,
		"scenarios": {
			"clean_migration_and_reapply": ([ $steps[] | select(.name == "historical-truth") | .status ] | .[0] == "passed"),
			"historical_bias": ($backtest_status == "passed"),
			"paper_interruption_idempotency_and_rebuild": ($paper_status == "passed"),
			"daily_cycle_resume_after_failed_dependency": ([ $steps[] | select(.name == "daily-resume") | .status ] | .[0] == "passed"),
			"disposable_backup_restore_integrity": ([ $steps[] | select(.name == "backup-fixture") | .status ] | .[0] == "passed"),
			"clean_root_postgres_restore_and_reconcile": ([ $steps[] | select(.name == "restore-reconcile") | .status ] | .[0] == "passed")
		},
		"evidence": {
			"backtest_report": {"path": $backtest_path, "sha256": $backtest_sha256},
			"paper_report": {"path": $paper_path, "sha256": $paper_sha256},
			"restore_database": $restore_database
		},
		"limitations": [
			"The bias and paper proofs use deterministic retained fixtures; they do not create a genuine wall-clock forward record.",
			"The clean-root restore is local Compose evidence and is not a multi-host or off-site disaster-recovery proof.",
			"The security step uses a loopback environment override when the operator checkout has a deliberate non-loopback .env override."
		]
	}' > "$report_temporary"
mv -- "$report_temporary" "$report_path"

if (( overall == 0 )); then
	printf '%s\n' 'v1 resilience and historical-bias acceptance passed'
	printf 'report: %s\n' "$report_path"
	exit 0
fi

printf '%s\n' 'v1 resilience and historical-bias acceptance failed' >&2
printf 'report: %s\n' "$report_path" >&2
exit 1
