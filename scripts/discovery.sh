#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
cd "$repo_root"

profile=${1:?profile path is required}
momentum_manifest=${2:?momentum batch manifest is required}
market_session=${3:?market session is required}
output_root=${4:-/data/research/discovery/nasdaq-100-starter}
git_commit=$(git rev-parse --verify HEAD | tr '[:upper:]' '[:lower:]')
case "$profile" in
  /*) profile_path=$profile ;;
  *) profile_path="$repo_root/$profile" ;;
esac

result=$(docker compose run --rm --no-deps \
  -v "$profile_path:/tmp/discovery-profile.yaml:ro" \
  -v "$repo_root/schemas/feature-set-registry.json:/tmp/feature-set-registry.json:ro" \
  jupyter python -m research.discovery_cli publish \
    --profile /tmp/discovery-profile.yaml \
    --momentum-batch "$momentum_manifest" \
    --features-root /data/features \
    --registry /tmp/feature-set-registry.json \
    --output-root "$output_root" \
    --market-session "$market_session" \
    --candidate-count 5 \
    --git-commit "$git_commit")
printf '%s\n' "$result"
manifest=$(printf '%s\n' "$result" | jq -er '.manifest_path')

LOCAL_UID=$(id -u) LOCAL_GID=$(id -g) INVS_GIT_COMMIT="$git_commit" \
  docker compose --profile collect run --rm \
    --no-deps --entrypoint invs-discovery-catalog collector \
    --manifest "$manifest" \
    --discovery-root /data/research/discovery
