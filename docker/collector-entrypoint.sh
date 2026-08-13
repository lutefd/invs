#!/bin/sh
set -eu

# This is the only file coupled to the collector CLI. Compose passes optional
# arguments through unchanged, so CLI evolution does not require image changes.
collector_config=${INVS_CONFIG:-/etc/invs/config.yaml}
if [ "${1:-}" = "reconcile" ]; then
	shift
	exec /usr/local/bin/reconcile "$@"
fi
exec /usr/local/bin/collector --config "$collector_config" "$@"
