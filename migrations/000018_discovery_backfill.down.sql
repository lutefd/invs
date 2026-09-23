BEGIN;

DROP VIEW discovery_backfill_prices_history;
DROP VIEW discovery_backfill_prices_all_runs;
DROP VIEW discovery_backfill_latest_rankings;
DROP VIEW discovery_backfill_rankings_history;
DROP VIEW discovery_backfill_rankings_all_runs;
DROP TABLE discovery_backfill_prices;
DROP TABLE discovery_backfill_rankings;
DROP TABLE discovery_backfill_indexes;
DROP TABLE discovery_backfill_runs;

COMMIT;
