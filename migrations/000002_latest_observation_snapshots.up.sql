BEGIN;

-- The composite key lets projection rows prove that their ingestion run belongs
-- to the same source without relying on application-side checks.
ALTER TABLE ingestion_runs
    ADD CONSTRAINT ingestion_runs_id_data_source_unique
    UNIQUE (id, data_source_id);

-- This is a latest-only operational projection for Grafana. Canonical history
-- remains in Parquet; replacing this row never rewrites historical evidence.
CREATE TABLE market_price_snapshots (
    data_source_id uuid NOT NULL,
    security_id uuid NOT NULL REFERENCES securities (id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    ingestion_run_id uuid NOT NULL,
    schema_version text NOT NULL DEFAULT '1.0.0'
        CHECK (schema_version = '1.0.0'),
    interval text NOT NULL DEFAULT '1d'
        CHECK (interval = '1d'),
    price_basis text NOT NULL DEFAULT 'raw'
        CHECK (price_basis = 'raw'),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    observed_at timestamptz NOT NULL,
    published_at timestamptz NOT NULL,
    available_at timestamptz NOT NULL,
    ingested_at timestamptz NOT NULL,
    published_precision text NOT NULL
        CHECK (published_precision IN ('date', 'second', 'unknown')),
    -- Canonical decimals remain text so this projection cannot round values on
    -- ingestion. Grafana queries may cast validated values to numeric.
    open_value text NOT NULL
        CHECK (open_value ~ '^(0|[1-9][0-9]*)(\.[0-9]+)?$'),
    high_value text NOT NULL
        CHECK (high_value ~ '^(0|[1-9][0-9]*)(\.[0-9]+)?$'),
    low_value text NOT NULL
        CHECK (low_value ~ '^(0|[1-9][0-9]*)(\.[0-9]+)?$'),
    close_value text NOT NULL
        CHECK (close_value ~ '^(0|[1-9][0-9]*)(\.[0-9]+)?$'),
    volume_value text NOT NULL
        CHECK (volume_value ~ '^(0|[1-9][0-9]*)(\.[0-9]+)?$'),
    raw_payload_hash text NOT NULL
        CHECK (raw_payload_hash ~ '^[0-9a-f]{64}$'),
    projected_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (data_source_id, security_id),
    CONSTRAINT market_price_snapshots_source_fk
        FOREIGN KEY (data_source_id) REFERENCES data_sources (id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT market_price_snapshots_run_source_fk
        FOREIGN KEY (ingestion_run_id, data_source_id)
        REFERENCES ingestion_runs (id, data_source_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT market_price_snapshots_ohlc_non_negative CHECK (
        open_value::numeric >= 0
        AND high_value::numeric >= 0
        AND low_value::numeric >= 0
        AND close_value::numeric >= 0
    ),
    CONSTRAINT market_price_snapshots_ohlc_order CHECK (
        low_value::numeric <= LEAST(open_value::numeric, close_value::numeric)
        AND high_value::numeric >= GREATEST(open_value::numeric, close_value::numeric)
        AND low_value::numeric <= high_value::numeric
    ),
    CONSTRAINT market_price_snapshots_temporal_order CHECK (
        observed_at <= published_at
        AND published_at <= available_at
        AND available_at <= ingested_at
        AND ingested_at <= projected_at
    )
);

CREATE INDEX market_price_snapshots_observed_idx
    ON market_price_snapshots (observed_at DESC);
CREATE INDEX market_price_snapshots_run_idx
    ON market_price_snapshots (ingestion_run_id);

-- This table exposes only the most recent current-vintage macro value per
-- source/series for dashboards. Revision history remains exclusively in Parquet.
CREATE TABLE macro_observation_snapshots (
    data_source_id uuid NOT NULL,
    ingestion_run_id uuid NOT NULL,
    schema_version text NOT NULL DEFAULT '1.0.0'
        CHECK (schema_version = '1.0.0'),
    series_id text NOT NULL CHECK (btrim(series_id) <> ''),
    geography text NOT NULL CHECK (btrim(geography) <> ''),
    unit text NOT NULL CHECK (btrim(unit) <> ''),
    frequency text NOT NULL CHECK (
        frequency IN (
            'daily', 'weekly', 'monthly', 'quarterly',
            'semiannual', 'annual', 'irregular'
        )
    ),
    seasonal_adjustment text
        CHECK (seasonal_adjustment IS NULL OR btrim(seasonal_adjustment) <> ''),
    observed_at timestamptz NOT NULL,
    published_at timestamptz NOT NULL,
    available_at timestamptz NOT NULL,
    ingested_at timestamptz NOT NULL,
    published_precision text NOT NULL
        CHECK (published_precision IN ('date', 'second', 'unknown')),
    value text NOT NULL
        CHECK (value ~ '^-?(0|[1-9][0-9]*)(\.[0-9]+)?$'),
    revision integer NOT NULL DEFAULT 0 CHECK (revision >= 0),
    vintage_at timestamptz,
    raw_payload_hash text NOT NULL
        CHECK (raw_payload_hash ~ '^[0-9a-f]{64}$'),
    projected_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (data_source_id, series_id),
    CONSTRAINT macro_observation_snapshots_source_fk
        FOREIGN KEY (data_source_id) REFERENCES data_sources (id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT macro_observation_snapshots_run_source_fk
        FOREIGN KEY (ingestion_run_id, data_source_id)
        REFERENCES ingestion_runs (id, data_source_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT macro_observation_snapshots_temporal_order CHECK (
        observed_at <= published_at
        AND published_at <= available_at
        AND available_at <= ingested_at
        AND ingested_at <= projected_at
    ),
    CONSTRAINT macro_observation_snapshots_vintage_order CHECK (
        vintage_at IS NULL
        OR (observed_at <= vintage_at AND vintage_at <= ingested_at)
    )
);

CREATE INDEX macro_observation_snapshots_observed_idx
    ON macro_observation_snapshots (observed_at DESC);
CREATE INDEX macro_observation_snapshots_run_idx
    ON macro_observation_snapshots (ingestion_run_id);

COMMENT ON TABLE market_price_snapshots IS
    'Latest-only Yahoo-compatible canonical v1 raw daily price projection for operational dashboards; this subset requires published_at and volume, while Parquet remains authoritative history.';
COMMENT ON TABLE macro_observation_snapshots IS
    'Latest-only canonical v1 macro projection for operational dashboards; Parquet remains authoritative history.';
COMMENT ON COLUMN market_price_snapshots.projected_at IS
    'UTC instant when this replaceable dashboard projection was written.';
COMMENT ON COLUMN macro_observation_snapshots.projected_at IS
    'UTC instant when this replaceable dashboard projection was written.';

COMMIT;
