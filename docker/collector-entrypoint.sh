#!/bin/sh
set -eu

# This is the only file coupled to the collector CLI. Compose passes optional
# arguments through unchanged, so CLI evolution does not require image changes.
exec /usr/local/bin/collector --config "${INVS_CONFIG:-/etc/invs/config.yaml}" "$@"
