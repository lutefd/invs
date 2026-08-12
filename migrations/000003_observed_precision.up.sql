BEGIN;

ALTER TABLE market_price_snapshots
    ADD COLUMN observed_precision text NOT NULL DEFAULT 'unknown'
        CHECK (observed_precision IN ('date', 'second', 'unknown'));

ALTER TABLE macro_observation_snapshots
    ADD COLUMN observed_precision text NOT NULL DEFAULT 'unknown'
        CHECK (observed_precision IN ('date', 'second', 'unknown'));

COMMIT;
