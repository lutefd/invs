#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

run_guard_probe() {
	local expected_message=$1
	shift
	local output exit_code
	if output=$("$@" 2>&1); then
		echo 'v1 release guard probe unexpectedly succeeded' >&2
	printf '%s\n' "$output" >&2
		exit 1
	else
		exit_code=$?
	fi
	test "$exit_code" -eq 2
	printf '%s\n' "$output" | grep -Fq "$expected_message"
	if printf '%s\n' "$output" | grep -Fq 'validated release'; then
		echo 'v1 release guard entered the acceptance ladder before its precondition passed' >&2
		exit 1
	fi
}

run_guard_probe \
	'V1_FORWARD_RECORD is required for v1 release acceptance' \
	env -u V1_FORWARD_RECORD -u V1_PAPER_REPORT INVS_BIND_ADDRESS=127.0.0.1 \
	make v1-release-acceptance

run_guard_probe \
	'V1_FORWARD_RECORD must be a safe repository-relative path' \
	env V1_FORWARD_RECORD=/tmp/invs-forward-record.json \
	V1_PAPER_REPORT=data/research/acceptance/v0.6/reproduction/paper-reproduction.json \
	INVS_BIND_ADDRESS=127.0.0.1 make v1-release-acceptance

guard_root=$(mktemp -d "$repo_root/.runtime/v1-release-guard.XXXXXX")
outside_root=$(mktemp -d)
cleanup() {
	rm -rf -- "$guard_root" "$outside_root"
}
trap cleanup EXIT

printf '%s\n' '{}' > "$outside_root/forward.json"
printf '%s\n' '{}' > "$outside_root/paper.json"
ln -s "$outside_root" "$guard_root/evidence"

forward_relative="${guard_root#"$repo_root/"}/evidence/forward.json"
paper_relative="${guard_root#"$repo_root/"}/evidence/paper.json"
run_guard_probe \
	'V1_FORWARD_RECORD must resolve inside the repository' \
	env V1_FORWARD_RECORD="$forward_relative" V1_PAPER_REPORT="$paper_relative" \
	INVS_BIND_ADDRESS=127.0.0.1 make v1-release-acceptance

printf '%s\n' 'v1 release guard acceptance passed'
