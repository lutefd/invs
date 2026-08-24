BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM market_price_snapshots WHERE published_at IS NULL) THEN
        RAISE EXCEPTION 'cannot restore required price publication time while NULL snapshots exist';
    END IF;
END;
$$;

ALTER TABLE market_price_snapshots
    DROP CONSTRAINT market_price_snapshots_temporal_order;

ALTER TABLE market_price_snapshots
    ALTER COLUMN published_at SET NOT NULL;

ALTER TABLE market_price_snapshots
    ADD CONSTRAINT market_price_snapshots_temporal_order CHECK (
        observed_at <= published_at
        AND published_at <= available_at
        AND available_at <= ingested_at
        AND ingested_at <= projected_at
    );

COMMENT ON COLUMN market_price_snapshots.published_at IS NULL;

COMMIT;
