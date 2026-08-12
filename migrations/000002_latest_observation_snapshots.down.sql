BEGIN;

DROP TABLE IF EXISTS macro_observation_snapshots;
DROP TABLE IF EXISTS market_price_snapshots;

ALTER TABLE ingestion_runs
    DROP CONSTRAINT IF EXISTS ingestion_runs_id_data_source_unique;

COMMIT;
