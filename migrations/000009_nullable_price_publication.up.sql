BEGIN;

ALTER TABLE market_price_snapshots
    ALTER COLUMN published_at DROP NOT NULL;

ALTER TABLE market_price_snapshots
    DROP CONSTRAINT market_price_snapshots_temporal_order;

ALTER TABLE market_price_snapshots
    ADD CONSTRAINT market_price_snapshots_temporal_order CHECK (
        (published_at IS NULL OR (
            observed_at <= published_at
            AND published_at <= available_at
        ))
        AND available_at <= ingested_at
        AND ingested_at <= projected_at
    );

COMMENT ON COLUMN market_price_snapshots.published_at IS
    'Earliest defensible source publication time; NULL when unavailable. Operational availability remains explicit.';

COMMIT;
