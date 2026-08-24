BEGIN;

ALTER TABLE market_price_snapshots
    DROP CONSTRAINT market_price_snapshots_price_basis_check;

ALTER TABLE market_price_snapshots
    ADD CONSTRAINT market_price_snapshots_price_basis_check CHECK (
        price_basis = 'raw'
    );

COMMIT;
