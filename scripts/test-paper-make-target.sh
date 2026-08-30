#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

common_args=(
	PAPER_SPEC=fixtures/research/daily-cycle-paper-account.json
	PAPER_LEDGER_ROOT=/data/research/forward/v1/ledger
	PAPER_SESSION_DATE=2026-08-31
	PAPER_DECISION_AT=2026-08-31T21:05:00Z
)

custom_output=$(make -s -n paper-run "${common_args[@]}" PAPER_DATA_ROOT=/data/research/forward/v1)
grep -Fq -- '--data-root "/data/research/forward/v1"' <<<"$custom_output"
if grep -Fq -- '--data-root "/data"' <<<"$custom_output"; then
	echo 'paper-run ignored PAPER_DATA_ROOT' >&2
	exit 1
fi

default_output=$(make -s -n paper-run "${common_args[@]}")
grep -Fq -- '--data-root "/data"' <<<"$default_output"

printf '%s\n' 'paper Make target acceptance passed'
