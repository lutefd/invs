#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
cd "$repo_root"

test_root=$(mktemp -d "${TMPDIR:-/tmp}/invs-projection-freshness.XXXXXX")
trap 'rm -rf -- "$test_root"' EXIT

cat >"$test_root/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [[ " $* " == *" compose up "* ]]; then
  exit 0
fi

sql=${!#}
case "$sql" in
  *"FROM expected"*)
    printf '%s|%s\n' \
      "${INVS_FRESH_TEST_EXPECTED_SESSION:-2026-11-20}" \
      "${INVS_FRESH_TEST_EXPECTED_AGE:-2.00}"
    ;;
  *"FROM market_price_snapshots"*)
    printf 'price|%s|%s\n' \
      "${INVS_FRESH_TEST_PRICE_SESSION:-2026-11-20}" \
      "${INVS_FRESH_TEST_PRICE_AGE:-1.00}"
    printf 'forward|%s|%s\n' \
      "${INVS_FRESH_TEST_FORWARD_SESSION:-2026-11-20}" \
      "${INVS_FRESH_TEST_FORWARD_AGE:-0.50}"
    printf 'history_rankings|%s|%s\n' \
      "${INVS_FRESH_TEST_HISTORY_SESSION:-2026-11-20}" \
      "${INVS_FRESH_TEST_HISTORY_AGE:-0.50}"
    printf 'history_prices|%s|%s\n' \
      "${INVS_FRESH_TEST_HISTORY_SESSION:-2026-11-20}" \
      "${INVS_FRESH_TEST_HISTORY_AGE:-0.50}"
    ;;
  *"FROM macro_observation_snapshots"*)
    printf '%s\n' "${INVS_FRESH_TEST_MACRO_AGE:-1.00}"
    ;;
  *)
    echo "unexpected SQL: $sql" >&2
    exit 2
    ;;
esac
EOF
chmod 0755 "$test_root/docker"

# 1. All projections fresh -> ok.
PATH="$test_root:$PATH" scripts/check-projection-freshness.sh >"$test_root/ok.log"
grep -Fq 'projection_freshness=ok scope=all expected_session=2026-11-20 max_age_hours=24' "$test_root/ok.log"

# 2. Projections stuck on an older session (like Oct 9 vs Nov 20) -> fail.
if INVS_FRESH_TEST_FORWARD_SESSION=2026-10-09 PATH="$test_root:$PATH" \
    scripts/check-projection-freshness.sh >"$test_root/stale-session.log" 2>&1; then
  echo 'projection freshness unexpectedly accepted stale forward session' >&2
  exit 1
fi
grep -Fq 'projection_stale projection=forward reason=session_behind latest_session=2026-10-09 expected_session=2026-11-20' "$test_root/stale-session.log"
grep -Fq 'projection_freshness=failed' "$test_root/stale-session.log"

# 3. Projection older than 24h on a trading day -> fail.
if INVS_FRESH_TEST_HISTORY_AGE=25.50 PATH="$test_root:$PATH" \
    scripts/check-projection-freshness.sh >"$test_root/stale-age.log" 2>&1; then
  echo 'projection freshness unexpectedly accepted >24h history projection' >&2
  exit 1
fi
grep -Fq 'projection_stale projection=history_rankings reason=age_exceeded age_hours=25.50 max_age_hours=24' "$test_root/stale-age.log"

# 4. Macro older than 24h fails in scope=all, ignored in scope=market.
if INVS_FRESH_TEST_MACRO_AGE=30.00 PATH="$test_root:$PATH" \
    scripts/check-projection-freshness.sh >"$test_root/stale-macro.log" 2>&1; then
  echo 'projection freshness unexpectedly accepted stale macro in all scope' >&2
  exit 1
fi
grep -Fq 'projection_stale projection=macro reason=age_exceeded age_hours=30.00 max_age_hours=24' "$test_root/stale-macro.log"

INVS_FRESH_TEST_MACRO_AGE=30.00 INVS_PROJECTION_SCOPE=market PATH="$test_root:$PATH" \
  scripts/check-projection-freshness.sh >"$test_root/market-scope.log"
grep -Fq 'projection_freshness=ok scope=market' "$test_root/market-scope.log"

# 5. Weekend: price projected Friday (40h ago) still passes if latest_session matches Friday,
#    but discovery still fails if INVS_PROJECTION_REQUIRE_RECENT_DISCOVERY=1 and >24h old.
INVS_FRESH_TEST_EXPECTED_AGE=40.00 \
INVS_FRESH_TEST_PRICE_AGE=40.00 \
INVS_FRESH_TEST_FORWARD_AGE=40.00 \
INVS_FRESH_TEST_HISTORY_AGE=40.00 \
INVS_PROJECTION_SCOPE=market \
PATH="$test_root:$PATH" scripts/check-projection-freshness.sh >"$test_root/weekend.log"
grep -Fq 'projection_freshness=ok scope=market' "$test_root/weekend.log"

if INVS_FRESH_TEST_EXPECTED_AGE=40.00 \
    INVS_FRESH_TEST_PRICE_AGE=40.00 \
    INVS_FRESH_TEST_FORWARD_AGE=40.00 \
    INVS_PROJECTION_SCOPE=market \
    INVS_PROJECTION_REQUIRE_RECENT_DISCOVERY=1 \
    PATH="$test_root:$PATH" scripts/check-projection-freshness.sh >"$test_root/weekend-stale-discovery.log" 2>&1; then
  echo 'projection freshness accepted stale discovery after a market cycle' >&2
  exit 1
fi
grep -Fq 'projection_stale projection=forward reason=age_exceeded' "$test_root/weekend-stale-discovery.log"

printf '%s\n' 'projection freshness acceptance passed'
