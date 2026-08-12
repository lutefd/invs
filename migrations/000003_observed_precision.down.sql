BEGIN;

ALTER TABLE macro_observation_snapshots
    DROP COLUMN IF EXISTS observed_precision;

ALTER TABLE market_price_snapshots
    DROP COLUMN IF EXISTS observed_precision;

COMMIT;
