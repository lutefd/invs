BEGIN;

CREATE TABLE corporate_action_versions (
    id uuid PRIMARY KEY,
    schema_version text NOT NULL DEFAULT '2.0.0' CHECK (schema_version = '2.0.0'),
    security_id uuid NOT NULL REFERENCES securities (id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    source_event_id text NOT NULL CHECK (btrim(source_event_id) <> ''),
    revision integer NOT NULL CHECK (revision >= 0),
    action_status text NOT NULL CHECK (
        action_status IN ('active', 'cancelled', 'unsupported')
    ),
    action_type text NOT NULL CHECK (
        action_type IN (
            'split', 'reverse_split', 'cash_dividend', 'stock_dividend',
            'spinoff', 'merger', 'acquisition', 'delisting', 'ticker_change',
            'exchange_change', 'rights_issue', 'other'
        )
    ),
    observed_at timestamptz NOT NULL,
    observed_precision text NOT NULL CHECK (
        observed_precision IN ('date', 'second', 'unknown')
    ),
    published_at timestamptz NOT NULL,
    published_precision text NOT NULL CHECK (
        published_precision IN ('date', 'second', 'unknown')
    ),
    available_at timestamptz NOT NULL,
    effective_at timestamptz NOT NULL,
    effective_precision text NOT NULL CHECK (
        effective_precision IN ('date', 'second', 'unknown')
    ),
    record_date date,
    payment_date date,
    ratio_numerator text CHECK (
        ratio_numerator IS NULL OR ratio_numerator ~ '^(0|[1-9][0-9]*)(\.[0-9]+)?$'
    ),
    ratio_denominator text CHECK (
        ratio_denominator IS NULL OR ratio_denominator ~ '^(0|[1-9][0-9]*)(\.[0-9]+)?$'
    ),
    cash_amount text CHECK (
        cash_amount IS NULL OR cash_amount ~ '^(0|[1-9][0-9]*)(\.[0-9]+)?$'
    ),
    currency text CHECK (currency IS NULL OR currency ~ '^[A-Z]{3}$'),
    target_security_id uuid REFERENCES securities (id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    source_reference text NOT NULL CHECK (btrim(source_reference) <> ''),
    raw_record_locator text CHECK (
        raw_record_locator IS NULL OR btrim(raw_record_locator) <> ''
    ),
    recorded_at timestamptz NOT NULL,
    data_source_id uuid NOT NULL REFERENCES data_sources (id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    ingestion_run_id uuid NOT NULL,
    raw_payload_hash text NOT NULL CHECK (raw_payload_hash ~ '^[0-9a-f]{64}$'),
    ingested_at timestamptz NOT NULL,
    normalizer_version text CHECK (
        normalizer_version IS NULL OR btrim(normalizer_version) <> ''
    ),
    record_hash text NOT NULL CHECK (record_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT corporate_action_versions_run_source_fk FOREIGN KEY (
        ingestion_run_id, data_source_id
    ) REFERENCES ingestion_runs (id, data_source_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT corporate_action_versions_identity_unique UNIQUE (
        data_source_id, source_event_id, revision
    ),
    CONSTRAINT corporate_action_versions_availability_order CHECK (
        published_at <= available_at
        AND available_at <= ingested_at
        AND ingested_at <= recorded_at
    ),
    CONSTRAINT corporate_action_versions_observed_precision CHECK (
        observed_precision = 'unknown'
        OR observed_precision = 'date' AND observed_at = (
            date_trunc('day', observed_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
        )
        OR observed_precision = 'second' AND observed_at = date_trunc('second', observed_at)
    ),
    CONSTRAINT corporate_action_versions_published_precision CHECK (
        published_precision = 'unknown'
        OR published_precision = 'date' AND published_at = (
            date_trunc('day', published_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
        )
        OR published_precision = 'second' AND published_at = date_trunc('second', published_at)
    ),
    CONSTRAINT corporate_action_versions_effective_precision CHECK (
        effective_precision = 'unknown'
        OR effective_precision = 'date' AND effective_at = (
            date_trunc('day', effective_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
        )
        OR effective_precision = 'second' AND effective_at = date_trunc('second', effective_at)
    ),
    CONSTRAINT corporate_action_versions_split_shape CHECK (
        action_status <> 'active'
        OR action_type NOT IN ('split', 'reverse_split') OR (
            ratio_numerator IS NOT NULL
            AND ratio_denominator IS NOT NULL
            AND ratio_numerator::numeric > 0
            AND ratio_denominator::numeric > 0
            AND cash_amount IS NULL
            AND currency IS NULL
        )
    ),
    CONSTRAINT corporate_action_versions_cash_shape CHECK (
        action_status <> 'active' OR action_type <> 'cash_dividend' OR (
            cash_amount IS NOT NULL
            AND currency IS NOT NULL
            AND ratio_numerator IS NULL
            AND ratio_denominator IS NULL
        )
    )
);

CREATE INDEX corporate_action_versions_lookup_idx
    ON corporate_action_versions (
        data_source_id, security_id, available_at DESC, revision DESC,
        recorded_at DESC
    );
CREATE INDEX corporate_action_versions_event_idx
    ON corporate_action_versions (
        data_source_id, source_event_id, available_at DESC, revision DESC
    );

CREATE TRIGGER corporate_action_versions_append_only
    BEFORE UPDATE OR DELETE ON corporate_action_versions
    FOR EACH ROW EXECUTE FUNCTION reject_historical_truth_mutation();

COMMIT;
