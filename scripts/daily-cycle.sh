#!/usr/bin/env bash
set -euo pipefail

exec python3 scripts/daily_cycle.py --repo-root "${INVS_REPO_ROOT:-.}" "$@"
