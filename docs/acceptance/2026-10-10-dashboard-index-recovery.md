# Dashboard indexing recovery — 2026-10-10

The stock dashboard was reading a retrospective catalog ending on September 23,
while the daily discovery catalog ended on October 5. Price collection had reached
October 9. The scheduled market cycle did not refresh the retrospective catalog;
on October 6 DuckDB exhausted the research container's 1,024-file soft limit, and
subsequent runs stopped after rejecting a malformed WBD quote.

## Change

- Research jobs now receive a 65,536-file limit through Compose; the running
  Jupyter service was recreated and its effective limit was verified.
- Every market cycle refreshes the three-month retrospective catalog with the
  verified latest completed XNAS session as its explicit end date. Scheduled
  refreshes use the deployed schema without rebuilding PostgreSQL.
- A failed collection proceeds only if the exact source-qualified Yahoo run key
  is recorded as `partial`, reconciliation passes, and the entire active roster
  has a common latest completed session. Dashboard publication reports degraded
  collection explicitly, and paper processing is halted. Failed runs and stale
  roster snapshots still block publication.
- `ops-status` checks daily rankings, historical rankings, and historical prices
  independently of collector and latest-price freshness.

## Live evidence

The actual `invs-market-cycle.service` invocation completed on October 10 at
21:24:45 America/Sao_Paulo with `Result=success` and `ExecMainStatus=0`. Its final
log reports `market_cycle_status=degraded reason=partial_collection` and
`dashboard_session=2026-10-09`; paper reports `halted reason=partial_collection`.
The enabled timer's next run is October 12 at 18:30 America/Sao_Paulo.

| Catalog | Latest session | Rows |
| --- | --- | --- |
| Daily rankings | 2026-10-09 | 101 |
| Historical rankings | 2026-10-09 | 6,666 |
| Historical prices | 2026-10-09 | 2,479 |

The retrospective range contains 66 sessions from July 9 through October 9.
For September 28 through October 9, each of the ten trading sessions has 101
prices and 101 ranking records: 1,010 rows in each dataset. Historical records
retain `retrospective_current_universe` and `installation_replay_only` labels;
missed daily observations were recovered only as retrospective history.

The historical artifact is
`data/research/discovery/retrospective-current-universe/backfills/36c5b388-d3f7-5845-90db-e5da59d93477/manifest.json`.
Repeated registration returned `already_present=true` with registration hash
`a974f2382b3cf00562b47504a740c33eb5f4f73f873419f26ad9096609dfeeb2`.
The daily discovery ID is `8978d537-e3c8-5ef1-bc04-9e2897e25188`.

## Verification and limits

- Scheduler acceptance passed for normal publication, exact source-qualified
  partial-run lookup, failed collection, stale active roster, immutable paper
  retry clocks, and manual recovery with paper halted.
- Operational-status acceptance passed for current prices with stale historical
  publication and for exchange-session freshness across non-trading days.
- `make dashboard-smoke` passed against the running PostgreSQL database. All
  nine stock-discovery dashboard queries were also executed for real results.
- Shell syntax, Compose configuration, and `git diff --check` passed.
- Live reconciliation reported zero integrity issues, with 200 existing legacy
  lineage advisories retained.

This recovery does not claim that every source is healthy or every stock is
scorable. WBD's malformed quote remains rejected, SEC collection remains stale,
and `ops-status` correctly continues to report those source alerts. Of the 101
daily ranking records, 21 are eligible and 80 contain explicit null-feature
rejection reasons due to insufficient available feature history. No scores or
missing prices were invented to conceal those limits.
