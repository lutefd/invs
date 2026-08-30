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
printf '%s\n' '{"account_id":"40000000-0000-4000-8000-000000000001"}' > "$paper_spec"

spec_path="$run_root/spec.json"
jq -n \
	--arg schema '../schemas/daily-cycle.schema.json' \
	--arg cycle_id 'v1-daily-cycle-acceptance' \
	--arg session_date '2026-08-30' \
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
		"schema_version": "1.0.0",
		"cycle_id": $cycle_id,
		"session_date": $session_date,
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
	and ([.stages[] | select(.name | startswith("paper:"))] | length == 3)
	and ([.stages[] | select(.name == "backup" and .status == "resumed")] | length == 1)
' "${run_root}/cycle.json" >/dev/null

while IFS= read -r log_path; do
	test -s "$repo_root/$log_path"
done < <(jq -r '.stages[].log_path' "${run_root}/cycle.json")

printf '%s\n' 'v1 daily-cycle CLI acceptance passed'
printf 'report: %s\n' "$run_root/cycle.json"
