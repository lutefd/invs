#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
cd "$repo_root"

test_root=$(mktemp -d "${TMPDIR:-/tmp}/invs-ops-status.XXXXXX")
trap 'rm -rf -- "$test_root"' EXIT

cat >"$test_root/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [[ " $* " == *" compose up "* ]]; then
  exit 0
fi

sql=${!#}
case "$sql" in
  *"FROM data_sources"*)
    printf '%s\n' 'yahoo|enabled|72.00|0|0' 'fred|enabled|1.00|0|0'
    ;;
  *"FROM expected CROSS JOIN latest"*)
    printf '2026-09-25|%s|72.00\n' "${INVS_OPS_TEST_LATEST_PRICE_SESSION:-2026-09-25}"
    ;;
  *"FROM macro_observation_snapshots"*)
    printf '%s\n' '1.00'
    ;;
  *"FROM discovery_latest_rankings"*)
    printf 'forward|2026-09-25\nhistory_rankings|%s\nhistory_prices|2026-09-25\n' "${INVS_OPS_TEST_HISTORY_SESSION:-2026-09-25}"
    ;;
  *)
    echo "unexpected SQL: $sql" >&2
    exit 2
    ;;
esac
EOF
chmod 0755 "$test_root/docker"

PATH="$test_root:$PATH" scripts/ops-status.sh >"$test_root/current.log"
grep -Fq 'source_freshness_policy=yahoo basis=xnas_completed_session' "$test_root/current.log"
grep -Fq 'projection=price age_hours=72.00 latest_session=2026-09-25 expected_session=2026-09-25 freshness_basis=xnas_completed_session' "$test_root/current.log"
grep -Fq 'operational_status=ok' "$test_root/current.log"

if INVS_OPS_TEST_LATEST_PRICE_SESSION=2026-09-24 PATH="$test_root:$PATH" \
    scripts/ops-status.sh >"$test_root/stale.log" 2>&1; then
  echo 'ops status unexpectedly accepted a stale market session' >&2
  exit 1
fi
grep -Fq 'attention=projection_stale projection=price latest_session=2026-09-24 expected_session=2026-09-25' "$test_root/stale.log"
grep -Fq 'operational_status=attention' "$test_root/stale.log"

if INVS_OPS_TEST_HISTORY_SESSION=2026-09-23 PATH="$test_root:$PATH" \
    scripts/ops-status.sh >"$test_root/stale-history.log" 2>&1; then
  echo 'ops status accepted stale dashboard history despite current collection' >&2
  exit 1
fi
grep -Fq 'attention=projection_stale projection=history_rankings latest_session=2026-09-23 expected_session=2026-09-25' "$test_root/stale-history.log"

printf '%s\n' 'ops status XNAS-session freshness acceptance passed'
