#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$*" >> "$INVS_DAILY_ACCEPTANCE_COMMAND_LOG"
printf 'daily-cycle acceptance make %s\n' "$*"
if [[ "${INVS_DAILY_ACCEPTANCE_FAIL_COMMAND:-}" == "${1:-}" ]]; then
	exit 7
fi
