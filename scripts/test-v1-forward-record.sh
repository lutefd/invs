#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

account_id="40000000-0000-4000-8000-000000000101"
ledger_root="data/research/acceptance/v0.6/reproduction/ledger"
output_path="data/research/acceptance/v1/.forward-record-replay-probe-${BASHPID}.json"
log_path=$(mktemp)

cleanup() {
	rm -f -- "$log_path" "$output_path"
}
trap cleanup EXIT

if [[ -e "$output_path" || -L "$output_path" ]]; then
	echo "forward-record probe output already exists: $output_path" >&2
	exit 1
fi

if env INVS_BIND_ADDRESS=127.0.0.1 make forward-record-capture \
	FORWARD_LEDGER_ROOT="$ledger_root" \
	FORWARD_ACCOUNT_ID="$account_id" \
	FORWARD_OUTPUT="$output_path" >"$log_path" 2>&1; then
	echo 'forward-record replay probe unexpectedly succeeded' >&2
cat "$log_path" >&2
	exit 1
else
	exit_code=$?
fi

if (( exit_code == 0 )); then
	echo 'forward-record replay probe returned success unexpectedly' >&2
exit 1
fi
if ! grep -Fq 'latest paper session is older than 7 calendar days at capture' "$log_path"; then
	echo 'forward-record replay probe failed for an unexpected reason' >&2
cat "$log_path" >&2
exit 1
fi
if [[ -e "$output_path" || -L "$output_path" ]]; then
	echo 'forward-record replay probe wrote an output despite rejection' >&2
exit 1
fi

printf '%s\n' 'v1 forward-record replay rejection acceptance passed'
