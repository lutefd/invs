#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

run_root=$(mktemp -d "$repo_root/.runtime/v1-daily-cycle-acceptance.XXXXXX")
backup_root=$(mktemp -d)
cleanup() {
	rm -rf -- "$run_root" "$backup_root"
}
trap cleanup EXIT

bin_root="$run_root/bin"
input_root="$run_root/inputs"
command_log="$run_root/commands.log"
mkdir -p "$bin_root" "$input_root"
cp scripts/test-v1-daily-cycle-make.sh "$bin_root/make"
chmod 0755 "$bin_root/make"

for input_name in universe schedule calendar registry taxonomy; do
	printf '%s\n' '{}' > "$input_root/$input_name.json"
done
paper_spec="$input_root/paper.json"
cp "$repo_root/fixtures/research/daily-cycle-paper-account.json" "$paper_spec"

spec_path="$run_root/spec.json"
jq -n \
	--arg schema '../schemas/daily-cycle.schema.json' \
	--arg cycle_id 'v1-daily-cycle-acceptance' \
	--arg session_date '2026-08-30' \
	--arg decision_at '2026-08-30T21:05:00Z' \
	--arg source 'acceptance' \
	--arg run_key 'v1-daily-cycle-acceptance' \
	--arg backup_dir "$backup_root" \
	--arg report_path "${run_root#"$repo_root/"}/cycle.json" \
	--arg log_dir "${run_root#"$repo_root/"}/logs" \
	--arg universe "${input_root#"$repo_root/"}/universe.json" \
	--arg schedule "${input_root#"$repo_root/"}/schedule.json" \
	--arg calendar_pin "${input_root#"$repo_root/"}/calendar.json" \
	--arg registry "${input_root#"$repo_root/"}/registry.json" \
	--arg taxonomy_registry "${input_root#"$repo_root/"}/taxonomy.json" \
	--arg paper_spec "${paper_spec#"$repo_root/"}" \
	--arg account_id '40000000-0000-4000-8000-000000000001' \
	'{
		"$schema": $schema,
		"schema_version": "1.2.0",
		"cycle_id": $cycle_id,
		"session_date": $session_date,
		"decision_at": $decision_at,
		"source": $source,
		"run_key": $run_key,
		"data_root": "data",
		"ledger_root": "research/acceptance/v1/daily-cycle/ledger",
		"backup_dir": $backup_dir,
		"report_path": $report_path,
		"log_dir": $log_dir,
		"collection": {"enabled": true},
		"feature": {
			"enabled": true,
			"universe": $universe,
			"schedule": $schedule,
			"calendar_pin": $calendar_pin,
			"registry": $registry,
			"taxonomy_registry": $taxonomy_registry,
			"security_mappings": null,
			"feature_set": "market-basic",
			"feature_set_version": "1.0.0"
		},
		"paper": [{"account_id": $account_id, "spec": $paper_spec}]
	}' > "$spec_path"

paper_preflight_spec_path="$run_root/paper-preflight-failure-spec.json"
jq \
	--arg cycle_id 'v1-daily-cycle-paper-preflight-failure' \
	--arg report_path "${run_root#"$repo_root/"}/paper-preflight-failure-cycle.json" \
	--arg log_dir "${run_root#"$repo_root/"}/paper-preflight-failure-logs" \
	'.cycle_id = $cycle_id | .report_path = $report_path | .log_dir = $log_dir' \
	"$spec_path" > "$paper_preflight_spec_path"

export INVS_DAILY_ACCEPTANCE_COMMAND_LOG="$command_log"
export PATH="$bin_root:$PATH"
export INVS_DAILY_ACCEPTANCE_FAIL_COMMAND=feature-batch
first_log="$run_root/first.log"
if scripts/daily-cycle.sh --repo-root "$repo_root" --spec "$spec_path" > "$first_log" 2>&1; then
	echo 'daily-cycle acceptance expected the first run to fail' >&2
	exit 1
else
	first_exit=$?
fi
test "$first_exit" -eq 1
jq -e '
	.status == "attention"
	and ([.stages[] | select(.name == "feature-batch" and .status == "failed")] | length == 1)
	and ([.stages[] | select(.name == "backup" and .status == "passed")] | length == 1)
	and ([.stages[] | select(.name == "observe" and .status == "passed")] | length == 1)
' "${run_root}/cycle.json" >/dev/null

unset INVS_DAILY_ACCEPTANCE_FAIL_COMMAND
scripts/daily-cycle.sh --repo-root "$repo_root" --spec "$spec_path" > "$run_root/second.log" 2>&1
jq -e '
	.status == "passed"
	and ([.stages[] | select(.status != "passed" and .status != "resumed")] | length == 0)
	and ([.stages[] | select(.name | startswith("paper:"))] | length == 4)
	and ([.stages[] | select(.name | endswith(":validate-inputs"))] | length == 1)
	and ([.stages[] | select(.name == "backup" and .status == "resumed")] | length == 1)
' "${run_root}/cycle.json" >/dev/null

while IFS= read -r log_path; do
	test -s "$repo_root/$log_path"
done < <(jq -r '.stages[].log_path' "${run_root}/cycle.json")

preflight_command_log="$run_root/paper-preflight-commands.log"
export INVS_DAILY_ACCEPTANCE_COMMAND_LOG="$preflight_command_log"
export INVS_DAILY_ACCEPTANCE_FAIL_COMMAND=paper-validate-inputs
if scripts/daily-cycle.sh --repo-root "$repo_root" --spec "$paper_preflight_spec_path" > "$run_root/paper-preflight-failure.log" 2>&1; then
	echo 'daily-cycle paper preflight acceptance expected the run to fail' >&2
	exit 1
else
	preflight_exit=$?
fi
test "$preflight_exit" -eq 1
jq -e '
	.status == "attention"
	and ([.stages[] | select((.name | endswith(":validate-inputs")) and .status == "failed")] | length == 1)
	and ([.stages[] | select((.name | endswith(":create")) and .status == "skipped")] | length == 1)
	and ([.stages[] | select((.name | endswith(":run")) and .status == "skipped")] | length == 1)
	and ([.stages[] | select((.name | endswith(":reconcile")) and .status == "skipped")] | length == 1)
	and ([.stages[] | select(.name == "backup" and .status == "passed")] | length == 1)
' "$run_root/paper-preflight-failure-cycle.json" >/dev/null
if grep -Fq 'paper-create-account' "$preflight_command_log"; then
	echo 'daily-cycle paper preflight acceptance created an account after validation failure' >&2
	exit 1
fi

printf '%s\n' 'v1 daily-cycle CLI acceptance passed'
printf 'report: %s\n' "$run_root/cycle.json"
