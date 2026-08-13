BEGIN;

CREATE TABLE security_identifier_versions (
    id uuid PRIMARY KEY,
    schema_version text NOT NULL DEFAULT '1.0.0' CHECK (schema_version = '1.0.0'),
    security_id uuid NOT NULL REFERENCES securities (id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    identifier_type text NOT NULL CHECK (identifier_type ~ '^[a-z][a-z0-9_]{1,63}$'),
    value text NOT NULL CHECK (btrim(value) <> ''),
    normalized_value text NOT NULL CHECK (btrim(normalized_value) <> ''),
    identifier_scope text NOT NULL CHECK (btrim(identifier_scope) <> ''),
    valid_from timestamptz NOT NULL,
    valid_until timestamptz,
    available_at timestamptz NOT NULL,
    source_reference text NOT NULL CHECK (btrim(source_reference) <> ''),
    recorded_at timestamptz NOT NULL,
    data_source_id uuid NOT NULL REFERENCES data_sources (id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    raw_payload_hash text NOT NULL CHECK (raw_payload_hash ~ '^[0-9a-f]{64}$'),
    record_hash text NOT NULL CHECK (record_hash ~ '^[0-9a-f]{64}$'),
    revision integer NOT NULL CHECK (revision >= 0),
    is_primary boolean NOT NULL,
    CONSTRAINT security_identifier_versions_valid_range CHECK (
        valid_until IS NULL OR valid_until > valid_from
    ),
    CONSTRAINT security_identifier_versions_recorded_order CHECK (
        available_at <= recorded_at
    ),
    CONSTRAINT security_identifier_versions_no_authority_overlap EXCLUDE USING gist (
        data_source_id WITH =,
        identifier_type WITH =,
        identifier_scope WITH =,
        normalized_value WITH =,
        revision WITH =,
        tstzrange(valid_from, valid_until, '[)') WITH &&
    )
);

CREATE INDEX security_identifier_versions_security_idx
    ON security_identifier_versions (security_id, available_at DESC, revision DESC);
CREATE INDEX security_identifier_versions_lookup_idx
    ON security_identifier_versions (
        data_source_id, identifier_type, identifier_scope, normalized_value,
        available_at DESC, revision DESC
    );

CREATE TABLE security_listing_versions (
    id uuid PRIMARY KEY,
    schema_version text NOT NULL DEFAULT '1.0.0' CHECK (schema_version = '1.0.0'),
    security_id uuid NOT NULL REFERENCES securities (id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    issuer_id uuid REFERENCES issuers (id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    exchange text NOT NULL CHECK (btrim(exchange) <> ''),
    mic text NOT NULL CHECK (mic ~ '^[A-Z0-9]{4}$'),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    primary_listing boolean NOT NULL,
    valid_from timestamptz NOT NULL,
    valid_until timestamptz,
    available_at timestamptz NOT NULL,
    source_reference text NOT NULL CHECK (btrim(source_reference) <> ''),
    recorded_at timestamptz NOT NULL,
    data_source_id uuid NOT NULL REFERENCES data_sources (id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    raw_payload_hash text NOT NULL CHECK (raw_payload_hash ~ '^[0-9a-f]{64}$'),
    record_hash text NOT NULL CHECK (record_hash ~ '^[0-9a-f]{64}$'),
    revision integer NOT NULL CHECK (revision >= 0),
    CONSTRAINT security_listing_versions_valid_range CHECK (
        valid_until IS NULL OR valid_until > valid_from
    ),
    CONSTRAINT security_listing_versions_recorded_order CHECK (
        available_at <= recorded_at
    ),
    CONSTRAINT security_listing_versions_no_authority_overlap EXCLUDE USING gist (
        data_source_id WITH =,
        security_id WITH =,
        mic WITH =,
        revision WITH =,
        tstzrange(valid_from, valid_until, '[)') WITH &&
    )
);

CREATE INDEX security_listing_versions_lookup_idx
    ON security_listing_versions (
        data_source_id, security_id, mic, available_at DESC, revision DESC
    );
CREATE INDEX security_listing_versions_issuer_idx
    ON security_listing_versions (issuer_id, valid_from DESC);

CREATE TABLE universe_memberships (
    id uuid PRIMARY KEY,
    schema_version text NOT NULL DEFAULT '1.0.0' CHECK (schema_version = '1.0.0'),
    universe_id text NOT NULL CHECK (universe_id ~ '^[a-z][a-z0-9_]{1,63}$'),
    security_id uuid NOT NULL REFERENCES securities (id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    member boolean NOT NULL,
    valid_from timestamptz NOT NULL,
    valid_until timestamptz,
    announced_at timestamptz,
    available_at timestamptz NOT NULL,
    source_reference text NOT NULL CHECK (btrim(source_reference) <> ''),
    recorded_at timestamptz NOT NULL,
    data_source_id uuid NOT NULL REFERENCES data_sources (id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    raw_payload_hash text NOT NULL CHECK (raw_payload_hash ~ '^[0-9a-f]{64}$'),
    record_hash text NOT NULL CHECK (record_hash ~ '^[0-9a-f]{64}$'),
    revision integer NOT NULL CHECK (revision >= 0),
    CONSTRAINT universe_memberships_valid_range CHECK (
        valid_until IS NULL OR valid_until > valid_from
    ),
    CONSTRAINT universe_memberships_availability_order CHECK (
        (announced_at IS NULL OR announced_at <= available_at)
        AND available_at <= recorded_at
    ),
    CONSTRAINT universe_memberships_no_authority_overlap EXCLUDE USING gist (
        data_source_id WITH =,
        universe_id WITH =,
        security_id WITH =,
        revision WITH =,
        tstzrange(valid_from, valid_until, '[)') WITH &&
    )
);

CREATE INDEX universe_memberships_lookup_idx
    ON universe_memberships (
        data_source_id, universe_id, security_id, available_at DESC, revision DESC
    );

CREATE TABLE calendar_manifests (
    id uuid PRIMARY KEY,
    schema_version text NOT NULL DEFAULT '1.0.0' CHECK (schema_version = '1.0.0'),
    calendar_version text NOT NULL CHECK (calendar_version ~ '^[a-z][a-z0-9_-]{1,63}$'),
    mic text NOT NULL CHECK (mic ~ '^[A-Z0-9]{4}$'),
    exchange_timezone text NOT NULL CHECK (
        exchange_timezone ~ '^[A-Za-z0-9._+-]+/[A-Za-z0-9._+-]+$'
    ),
    available_at timestamptz NOT NULL,
    source_reference text NOT NULL CHECK (btrim(source_reference) <> ''),
    recorded_at timestamptz NOT NULL,
    data_source_id uuid NOT NULL REFERENCES data_sources (id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    raw_payload_hash text NOT NULL CHECK (raw_payload_hash ~ '^[0-9a-f]{64}$'),
    record_hash text NOT NULL CHECK (record_hash ~ '^[0-9a-f]{64}$'),
    session_fingerprint text NOT NULL CHECK (session_fingerprint ~ '^[0-9a-f]{64}$'),
    session_count integer NOT NULL CHECK (session_count >= 0),
    CONSTRAINT calendar_manifests_recorded_order CHECK (available_at <= recorded_at),
    CONSTRAINT calendar_manifests_version_unique UNIQUE (
        data_source_id, mic, calendar_version
    ),
    CONSTRAINT calendar_manifests_session_parent_unique UNIQUE (
        data_source_id, mic, calendar_version, exchange_timezone
    )
);

CREATE INDEX calendar_manifests_selection_idx
    ON calendar_manifests (mic, calendar_version, available_at DESC);

CREATE TABLE trading_sessions (
    id uuid PRIMARY KEY,
    schema_version text NOT NULL DEFAULT '1.0.0' CHECK (schema_version = '1.0.0'),
    calendar_version text NOT NULL CHECK (calendar_version ~ '^[a-z][a-z0-9_-]{1,63}$'),
    mic text NOT NULL CHECK (mic ~ '^[A-Z0-9]{4}$'),
    exchange_timezone text NOT NULL CHECK (
        exchange_timezone ~ '^[A-Za-z0-9._+-]+/[A-Za-z0-9._+-]+$'
    ),
    session_date date NOT NULL,
    session_status text NOT NULL CHECK (session_status IN ('open', 'closed')),
    open_at timestamptz,
    close_at timestamptz,
    is_early_close boolean NOT NULL,
    available_at timestamptz NOT NULL,
    source_reference text NOT NULL CHECK (btrim(source_reference) <> ''),
    recorded_at timestamptz NOT NULL,
    data_source_id uuid NOT NULL REFERENCES data_sources (id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    raw_payload_hash text NOT NULL CHECK (raw_payload_hash ~ '^[0-9a-f]{64}$'),
    record_hash text NOT NULL CHECK (record_hash ~ '^[0-9a-f]{64}$'),
    revision integer NOT NULL CHECK (revision >= 0),
    CONSTRAINT trading_sessions_manifest_fk FOREIGN KEY (
        data_source_id, mic, calendar_version, exchange_timezone
    ) REFERENCES calendar_manifests (
        data_source_id, mic, calendar_version, exchange_timezone
    ) ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT trading_sessions_version_date_revision_unique UNIQUE (
        data_source_id, mic, calendar_version, session_date, revision
    ),
    CONSTRAINT trading_sessions_recorded_order CHECK (available_at <= recorded_at),
    CONSTRAINT trading_sessions_open_closed_temporal CHECK (
        (
            session_status = 'open'
            AND open_at IS NOT NULL
            AND close_at IS NOT NULL
            AND open_at < close_at
        )
        OR
        (
            session_status = 'closed'
            AND open_at IS NULL
            AND close_at IS NULL
            AND NOT is_early_close
        )
    )
);

CREATE INDEX trading_sessions_lookup_idx
    ON trading_sessions (
        data_source_id, mic, calendar_version, session_date, available_at DESC, revision DESC
    );
CREATE INDEX trading_sessions_instant_idx
    ON trading_sessions (mic, calendar_version, open_at, close_at)
    WHERE session_status = 'open';

CREATE FUNCTION reject_historical_truth_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION '% is append-only; % is not allowed', TG_TABLE_NAME, TG_OP
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER security_identifier_versions_append_only
    BEFORE UPDATE OR DELETE ON security_identifier_versions
    FOR EACH ROW EXECUTE FUNCTION reject_historical_truth_mutation();
CREATE TRIGGER security_listing_versions_append_only
    BEFORE UPDATE OR DELETE ON security_listing_versions
    FOR EACH ROW EXECUTE FUNCTION reject_historical_truth_mutation();
CREATE TRIGGER universe_memberships_append_only
    BEFORE UPDATE OR DELETE ON universe_memberships
    FOR EACH ROW EXECUTE FUNCTION reject_historical_truth_mutation();
CREATE TRIGGER calendar_manifests_append_only
    BEFORE UPDATE OR DELETE ON calendar_manifests
    FOR EACH ROW EXECUTE FUNCTION reject_historical_truth_mutation();
CREATE TRIGGER trading_sessions_append_only
    BEFORE UPDATE OR DELETE ON trading_sessions
    FOR EACH ROW EXECUTE FUNCTION reject_historical_truth_mutation();

COMMENT ON COLUMN security_identifier_versions.record_hash IS
    'Lowercase SHA-256 of deterministic canonical record content for exact-idempotent inserts.';
COMMENT ON COLUMN security_listing_versions.record_hash IS
    'Lowercase SHA-256 of deterministic canonical record content for exact-idempotent inserts.';
COMMENT ON COLUMN universe_memberships.record_hash IS
    'Lowercase SHA-256 of deterministic canonical record content for exact-idempotent inserts.';
COMMENT ON COLUMN calendar_manifests.record_hash IS
    'Lowercase SHA-256 of deterministic canonical record content for exact-idempotent inserts.';
COMMENT ON COLUMN trading_sessions.record_hash IS
    'Lowercase SHA-256 of deterministic canonical record content for exact-idempotent inserts.';
COMMENT ON CONSTRAINT security_identifier_versions_no_authority_overlap
    ON security_identifier_versions IS
    'Rejects overlapping authoritative intervals within one source and revision; later revisions may overlap.';
COMMENT ON CONSTRAINT security_listing_versions_no_authority_overlap
    ON security_listing_versions IS
    'Rejects overlapping authoritative intervals within one source and revision; later revisions may overlap.';
COMMENT ON CONSTRAINT universe_memberships_no_authority_overlap
    ON universe_memberships IS
    'Rejects overlapping authoritative intervals within one source and revision; later revisions may overlap.';

COMMIT;
