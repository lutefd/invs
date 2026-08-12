#!/bin/sh
set -eu

set -- jupyter lab \
  --ip=0.0.0.0 \
  --port=8888 \
  --no-browser \
  --ServerApp.root_dir=/workspace

# Secure first boot: when no explicit token is configured, Jupyter generates one
# and prints its localhost URL to `docker compose logs jupyter`.
if [ -n "${JUPYTER_TOKEN:-}" ]; then
  set -- "$@" "--IdentityProvider.token=${JUPYTER_TOKEN}"
fi

exec "$@"
