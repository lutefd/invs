BEGIN;

-- pgcrypto provides gen_random_uuid(); btree_gist lets exclusion constraints
-- combine scalar identity columns with validity ranges.
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE TYPE ingestion_run_status AS ENUM (
    'queued',
    'running',
    'succeeded',
    'partial',
    'failed',
    'cancelled'
);

CREATE TABLE issuers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_name text NOT NULL CHECK (btrim(legal_name) <> ''),
    country_code text NOT NULL CHECK (country_code ~ '^[A-Z]{2}$'),
    sector text CHECK (sector IS NULL OR btrim(sector) <> ''),
    industry text CHECK (industry IS NULL OR btrim(industry) <> ''),
    cik text CHECK (cik IS NULL OR cik ~ '^[0-9]{10}$'),
    cvm_code text CHECK (cvm_code IS NULL OR cvm_code ~ '^[A-Za-z0-9._-]+$'),
    lei text CHECK (lei IS NULL OR lei ~ '^[A-Z0-9]{20}$'),
    active_from timestamptz,
    active_until timestamptz,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT issuers_active_range CHECK (
        active_until IS NULL OR active_from IS NULL OR active_until > active_from
    ),
    CONSTRAINT issuers_update_order CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX issuers_cik_unique
    ON issuers (cik) WHERE cik IS NOT NULL;
CREATE UNIQUE INDEX issuers_cvm_code_unique
    ON issuers (cvm_code) WHERE cvm_code IS NOT NULL;
CREATE UNIQUE INDEX issuers_lei_unique
    ON issuers (lei) WHERE lei IS NOT NULL;
CREATE INDEX issuers_country_idx ON issuers (country_code);
CREATE INDEX issuers_name_search_idx ON issuers (lower(legal_name));

CREATE TABLE securities (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    issuer_id uuid REFERENCES issuers (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    name text NOT NULL CHECK (btrim(name) <> ''),
    security_type text NOT NULL CHECK (security_type ~ '^[a-z][a-z0-9_]{1,63}$'),
    exchange_mic text CHECK (exchange_mic IS NULL OR exchange_mic ~ '^[A-Z0-9]{4}$'),
    exchange_name text CHECK (exchange_name IS NULL OR btrim(exchange_name) <> ''),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    primary_listing boolean NOT NULL DEFAULT false,
    active_from timestamptz,
    active_until timestamptz,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT securities_active_range CHECK (
        active_until IS NULL OR active_from IS NULL OR active_until > active_from
    ),
    CONSTRAINT securities_update_order CHECK (updated_at >= created_at)
);

CREATE INDEX securities_issuer_idx ON securities (issuer_id);
CREATE INDEX securities_exchange_idx ON securities (exchange_mic);
CREATE INDEX securities_active_idx ON securities (active_until) WHERE active_until IS NULL;

CREATE TABLE security_identifiers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    security_id uuid NOT NULL REFERENCES securities (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    identifier_type text NOT NULL CHECK (identifier_type ~ '^[a-z][a-z0-9_]{1,63}$'),
    value text NOT NULL CHECK (btrim(value) <> ''),
    normalized_value text NOT NULL CHECK (btrim(normalized_value) <> ''),
    -- Scope disambiguates identifiers such as tickers that may coexist on different MICs.
    identifier_scope text NOT NULL DEFAULT 'global' CHECK (btrim(identifier_scope) <> ''),
    valid_from timestamptz NOT NULL,
    valid_until timestamptz,
    is_primary boolean NOT NULL DEFAULT false,
    data_source_id uuid,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT security_identifiers_valid_range CHECK (
        valid_until IS NULL OR valid_until > valid_from
    ),
    CONSTRAINT security_identifiers_natural_version UNIQUE (
        security_id,
        identifier_type,
        identifier_scope,
        normalized_value,
        valid_from
    ),
    CONSTRAINT security_identifier_no_ambiguous_assignment EXCLUDE USING gist (
        identifier_type WITH =,
        identifier_scope WITH =,
        normalized_value WITH =,
        tstzrange(valid_from, valid_until, '[)') WITH &&
    )
);

CREATE INDEX security_identifiers_security_idx
    ON security_identifiers (security_id, identifier_type, valid_from DESC);
CREATE INDEX security_identifiers_lookup_idx
    ON security_identifiers (identifier_type, identifier_scope, normalized_value);
CREATE INDEX security_identifiers_current_idx
    ON security_identifiers (identifier_type, identifier_scope, normalized_value)
    WHERE valid_until IS NULL;

CREATE TABLE data_sources (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code text NOT NULL UNIQUE CHECK (code ~ '^[a-z][a-z0-9_-]{1,63}$'),
    name text NOT NULL CHECK (btrim(name) <> ''),
    -- Open vocabulary by design: adding a provider kind does not require a migration.
    source_kind text NOT NULL CHECK (source_kind ~ '^[a-z][a-z0-9_-]{1,63}$'),
    base_url text CHECK (base_url IS NULL OR base_url ~ '^https?://'),
    enabled boolean NOT NULL DEFAULT true,
    config jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(config) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT data_sources_update_order CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX data_sources_name_unique ON data_sources (lower(name));
CREATE INDEX data_sources_enabled_idx ON data_sources (enabled) WHERE enabled;

ALTER TABLE security_identifiers
    ADD CONSTRAINT security_identifiers_data_source_fk
    FOREIGN KEY (data_source_id) REFERENCES data_sources (id)
    ON UPDATE RESTRICT ON DELETE RESTRICT;

CREATE TABLE ingestion_runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    data_source_id uuid NOT NULL REFERENCES data_sources (id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    run_key text NOT NULL CHECK (btrim(run_key) <> ''),
    status ingestion_run_status NOT NULL DEFAULT 'running',
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    records_received bigint NOT NULL DEFAULT 0 CHECK (records_received >= 0),
    records_written bigint NOT NULL DEFAULT 0 CHECK (records_written >= 0),
    records_rejected bigint NOT NULL DEFAULT 0 CHECK (records_rejected >= 0),
    raw_payload_count bigint NOT NULL DEFAULT 0 CHECK (raw_payload_count >= 0),
    raw_bytes bigint NOT NULL DEFAULT 0 CHECK (raw_bytes >= 0),
    raw_payload_manifest_hash text CHECK (
        raw_payload_manifest_hash IS NULL OR raw_payload_manifest_hash ~ '^[0-9a-f]{64}$'
    ),
    error_message text CHECK (error_message IS NULL OR btrim(error_message) <> ''),
    error_details jsonb CHECK (error_details IS NULL OR jsonb_typeof(error_details) = 'object'),
    cursor jsonb CHECK (cursor IS NULL OR jsonb_typeof(cursor) = 'object'),
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ingestion_runs_idempotency UNIQUE (data_source_id, run_key),
    CONSTRAINT ingestion_runs_finish_order CHECK (
        finished_at IS NULL OR finished_at >= started_at
    ),
    CONSTRAINT ingestion_runs_terminal_timestamp CHECK (
        (status IN ('queued', 'running') AND finished_at IS NULL)
        OR
        (status IN ('succeeded', 'partial', 'failed', 'cancelled') AND finished_at IS NOT NULL)
    ),
    CONSTRAINT ingestion_runs_success_is_clean CHECK (
        status <> 'succeeded' OR (records_rejected = 0 AND error_message IS NULL)
    ),
    CONSTRAINT ingestion_runs_failure_has_error CHECK (
        status <> 'failed' OR error_message IS NOT NULL
    )
);

CREATE INDEX ingestion_runs_source_started_idx
    ON ingestion_runs (data_source_id, started_at DESC);
CREATE INDEX ingestion_runs_status_started_idx
    ON ingestion_runs (status, started_at DESC);
CREATE INDEX ingestion_runs_active_idx
    ON ingestion_runs (started_at) WHERE status IN ('queued', 'running');

COMMENT ON COLUMN ingestion_runs.run_key IS
    'Caller-defined logical work key; retries with the same source and key are idempotent.';
COMMENT ON COLUMN ingestion_runs.raw_payload_manifest_hash IS
    'Lowercase SHA-256 of the immutable manifest listing all raw payloads for this run.';
COMMENT ON COLUMN security_identifiers.valid_until IS
    'Exclusive upper bound; NULL means the identifier is currently valid.';

COMMIT;
