BEGIN;

-- Retrospective reconstruction is deliberately isolated from the operational
-- discovery tables, whose uniqueness boundary represents genuine daily runs.
CREATE TABLE discovery_backfill_runs (
    reconstruction_run_id uuid PRIMARY KEY,
    model_name text NOT NULL CHECK (model_name ~ '^[a-z][a-z0-9_-]{1,63}$'),
    model_version text NOT NULL CHECK (model_version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
    git_commit text NOT NULL CHECK (git_commit = 'unknown' OR git_commit ~ '^[0-9a-f]{40}$'),
    generator_version text NOT NULL CHECK (
        generator_version ~ '^python-retrospective-discovery-[0-9]+\.[0-9]+\.[0-9]+$'
    ),
    universe_id uuid NOT NULL,
    universe_name text NOT NULL CHECK (universe_name ~ '^[a-z][a-z0-9_-]{1,63}$'),
    universe_fingerprint text NOT NULL CHECK (universe_fingerprint ~ '^[0-9a-f]{64}$'),
    membership_as_of date NOT NULL,
    profile_sha256 text NOT NULL CHECK (profile_sha256 ~ '^[0-9a-f]{64}$'),
    profile_snapshot_path text NOT NULL CHECK (
        btrim(profile_snapshot_path) <> '' AND left(profile_snapshot_path, 1) <> '/'
        AND left(profile_snapshot_path, 1) <> chr(92) AND position('..' in profile_snapshot_path) = 0
    ),
    observation_mode text NOT NULL CHECK (observation_mode = 'retrospective_current_universe'),
    universe_basis text NOT NULL CHECK (universe_basis = 'current_profile_membership_snapshot'),
    period_start date NOT NULL,
    period_end date NOT NULL,
    reconstructed_at timestamptz NOT NULL,
    calendar_data_source_id uuid NOT NULL REFERENCES data_sources (id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    calendar_mic text NOT NULL CHECK (calendar_mic = 'XNAS'),
    calendar_version text NOT NULL CHECK (calendar_version ~ '^[a-z][a-z0-9_-]{1,63}$'),
    calendar_session_fingerprint text NOT NULL CHECK (calendar_session_fingerprint ~ '^[0-9a-f]{64}$'),
    calendar_session_count integer NOT NULL CHECK (calendar_session_count > 0),
    calendar_available_at timestamptz NOT NULL,
    calendar_snapshot_path text NOT NULL CHECK (
        btrim(calendar_snapshot_path) <> '' AND left(calendar_snapshot_path, 1) <> '/'
        AND left(calendar_snapshot_path, 1) <> chr(92) AND position('..' in calendar_snapshot_path) = 0
    ),
    calendar_snapshot_sha256 text NOT NULL CHECK (calendar_snapshot_sha256 ~ '^[0-9a-f]{64}$'),
    input_fitness text NOT NULL CHECK (input_fitness = 'installation_replay_only'),
    availability_policy text NOT NULL CHECK (
        availability_policy = 'current_vintage_with_preserved_receipt_times'
    ),
    input_fingerprint text NOT NULL CHECK (input_fingerprint ~ '^[0-9a-f]{64}$'),
    session_count integer NOT NULL CHECK (session_count > 0),
    manifest_path text NOT NULL CHECK (
        btrim(manifest_path) <> '' AND left(manifest_path, 1) <> '/'
        AND left(manifest_path, 1) <> chr(92) AND position('..' in manifest_path) = 0
    ),
    manifest_sha256 text NOT NULL CHECK (manifest_sha256 ~ '^[0-9a-f]{64}$'),
    registration_sha256 text NOT NULL CHECK (registration_sha256 ~ '^[0-9a-f]{64}$'),
    registered_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT discovery_backfill_runs_period CHECK (period_start <= period_end),
    UNIQUE (input_fingerprint)
);

CREATE TABLE discovery_backfill_indexes (
    index_id uuid NOT NULL UNIQUE,
    reconstruction_run_id uuid NOT NULL REFERENCES discovery_backfill_runs (reconstruction_run_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    market_session date NOT NULL,
    decision_at timestamptz NOT NULL,
    candidate_count integer NOT NULL CHECK (candidate_count >= 0),
    eligible_count integer NOT NULL CHECK (eligible_count >= 0),
    rejected_count integer NOT NULL CHECK (rejected_count >= 0),
    price_input_fingerprint text NOT NULL CHECK (price_input_fingerprint ~ '^[0-9a-f]{64}$'),
    session_manifest_sha256 text NOT NULL CHECK (session_manifest_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT discovery_backfill_indexes_counts CHECK (candidate_count <= eligible_count),
    PRIMARY KEY (reconstruction_run_id, market_session)
);

CREATE TABLE discovery_backfill_rankings (
    reconstruction_run_id uuid NOT NULL,
    market_session date NOT NULL,
    security_id uuid NOT NULL,
    rank integer CHECK (rank > 0),
    ticker text NOT NULL CHECK (ticker ~ '^[A-Z0-9.-]{1,16}$'),
    sector text NOT NULL CHECK (sector ~ '^[a-z][a-z0-9_]{1,63}$'),
    candidate boolean NOT NULL,
    score numeric,
    return_1m numeric,
    return_3m numeric,
    return_6m numeric,
    return_12m numeric,
    realized_volatility_1m numeric,
    max_drawdown_1m numeric,
    eligibility_status text NOT NULL CHECK (eligibility_status IN ('eligible', 'rejected')),
    rejection_reasons text[] NOT NULL DEFAULT '{}',
    PRIMARY KEY (reconstruction_run_id, market_session, security_id),
    FOREIGN KEY (reconstruction_run_id, market_session)
        REFERENCES discovery_backfill_indexes (reconstruction_run_id, market_session)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT discovery_backfill_rankings_eligibility CHECK (
        (eligibility_status = 'eligible' AND rank IS NOT NULL AND score IS NOT NULL)
        OR (eligibility_status = 'rejected' AND rank IS NULL AND score IS NULL AND NOT candidate)
    )
);

CREATE UNIQUE INDEX discovery_backfill_rankings_rank_idx
    ON discovery_backfill_rankings (reconstruction_run_id, market_session, rank)
    WHERE rank IS NOT NULL;
CREATE INDEX discovery_backfill_rankings_history_idx
    ON discovery_backfill_rankings (security_id, market_session DESC);

CREATE TABLE discovery_backfill_prices (
    reconstruction_run_id uuid NOT NULL,
    market_session date NOT NULL,
    security_id uuid NOT NULL,
    ticker text NOT NULL CHECK (ticker ~ '^[A-Z0-9.-]{1,16}$'),
    observed_at timestamptz NOT NULL,
    available_at timestamptz NOT NULL,
    ingested_at timestamptz NOT NULL,
    price_basis text NOT NULL CHECK (price_basis IN ('raw', 'split_adjusted', 'total_return_adjusted')),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    open_value numeric NOT NULL CHECK (open_value >= 0),
    high_value numeric NOT NULL CHECK (high_value >= 0),
    low_value numeric NOT NULL CHECK (low_value >= 0),
    close_value numeric NOT NULL CHECK (close_value >= 0),
    volume_value numeric CHECK (volume_value IS NULL OR volume_value >= 0),
    raw_payload_hash text NOT NULL CHECK (raw_payload_hash ~ '^[0-9a-f]{64}$'),
    data_source_id uuid NOT NULL REFERENCES data_sources (id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    ingestion_run_id uuid NOT NULL,
    raw_record_locator text NOT NULL CHECK (btrim(raw_record_locator) <> ''),
    manifest_path text NOT NULL CHECK (
        btrim(manifest_path) <> '' AND left(manifest_path, 1) <> '/'
        AND left(manifest_path, 1) <> chr(92) AND position('..' in manifest_path) = 0
    ),
    source_manifest_path text NOT NULL CHECK (
        btrim(source_manifest_path) <> '' AND left(source_manifest_path, 1) <> '/'
        AND left(source_manifest_path, 1) <> chr(92) AND position('..' in source_manifest_path) = 0
    ),
    manifest_sha256 text NOT NULL CHECK (manifest_sha256 ~ '^[0-9a-f]{64}$'),
    part_path text NOT NULL CHECK (
        btrim(part_path) <> '' AND left(part_path, 1) <> '/'
        AND left(part_path, 1) <> chr(92) AND position('..' in part_path) = 0
    ),
    source_part_path text NOT NULL CHECK (
        btrim(source_part_path) <> '' AND left(source_part_path, 1) <> '/'
        AND left(source_part_path, 1) <> chr(92) AND position('..' in source_part_path) = 0
    ),
    part_sha256 text NOT NULL CHECK (part_sha256 ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (reconstruction_run_id, market_session, security_id),
    FOREIGN KEY (ingestion_run_id, data_source_id)
        REFERENCES ingestion_runs (id, data_source_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    FOREIGN KEY (reconstruction_run_id, market_session)
        REFERENCES discovery_backfill_indexes (reconstruction_run_id, market_session)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT discovery_backfill_prices_temporal CHECK (observed_at <= available_at AND available_at <= ingested_at),
    CONSTRAINT discovery_backfill_prices_ohlc CHECK (
        low_value <= LEAST(open_value, close_value)
        AND high_value >= GREATEST(open_value, close_value)
        AND low_value <= high_value
    )
);

CREATE INDEX discovery_backfill_prices_history_idx
    ON discovery_backfill_prices (security_id, market_session DESC);

CREATE VIEW discovery_backfill_rankings_all_runs AS
SELECT
    run.reconstruction_run_id,
    run.observation_mode,
    run.universe_basis,
    run.membership_as_of,
    run.reconstructed_at,
    run.input_fitness,
    index.market_session,
    index.decision_at,
    ranking.security_id,
    ranking.rank,
    ranking.ticker,
    ranking.sector,
    ranking.candidate,
    ranking.score,
    ranking.return_1m,
    ranking.return_3m,
    ranking.return_6m,
    ranking.return_12m,
    ranking.realized_volatility_1m,
    ranking.max_drawdown_1m,
    ranking.eligibility_status,
    ranking.rejection_reasons
FROM discovery_backfill_runs AS run
JOIN discovery_backfill_indexes AS index USING (reconstruction_run_id)
JOIN discovery_backfill_rankings AS ranking
  USING (reconstruction_run_id, market_session);

CREATE VIEW discovery_backfill_rankings_history AS
WITH latest_run AS (
    SELECT DISTINCT ON (model_name, model_version, universe_id)
        reconstruction_run_id
    FROM discovery_backfill_runs
    ORDER BY model_name, model_version, universe_id, reconstructed_at DESC,
             registered_at DESC, reconstruction_run_id DESC
)
SELECT all_runs.*
FROM discovery_backfill_rankings_all_runs AS all_runs
JOIN latest_run USING (reconstruction_run_id);

CREATE VIEW discovery_backfill_latest_rankings AS
WITH latest_session AS (
    SELECT DISTINCT ON (reconstruction_run_id)
        reconstruction_run_id, market_session
    FROM discovery_backfill_indexes
    WHERE reconstruction_run_id IN (
        SELECT DISTINCT ON (model_name, model_version, universe_id)
            reconstruction_run_id
        FROM discovery_backfill_runs
        ORDER BY model_name, model_version, universe_id, reconstructed_at DESC,
                 registered_at DESC, reconstruction_run_id DESC
    )
    ORDER BY reconstruction_run_id, market_session DESC
)
SELECT history.*
FROM discovery_backfill_rankings_history AS history
JOIN latest_session USING (reconstruction_run_id, market_session);

CREATE VIEW discovery_backfill_prices_all_runs AS
SELECT
    run.reconstruction_run_id,
    run.observation_mode,
    run.universe_basis,
    run.membership_as_of,
    run.reconstructed_at,
    run.input_fitness,
    price.market_session,
    price.security_id,
    price.ticker,
    price.observed_at,
    price.available_at,
    price.ingested_at,
    price.price_basis,
    price.currency,
    price.open_value,
    price.high_value,
    price.low_value,
    price.close_value,
    price.volume_value,
    price.raw_payload_hash,
    price.raw_record_locator,
    price.manifest_path,
    price.source_manifest_path,
    price.manifest_sha256,
    price.part_path,
    price.source_part_path,
    price.part_sha256
FROM discovery_backfill_runs AS run
JOIN discovery_backfill_prices AS price USING (reconstruction_run_id);

CREATE VIEW discovery_backfill_prices_history AS
WITH latest_run AS (
    SELECT DISTINCT ON (model_name, model_version, universe_id)
        reconstruction_run_id
    FROM discovery_backfill_runs
    ORDER BY model_name, model_version, universe_id, reconstructed_at DESC,
             registered_at DESC, reconstruction_run_id DESC
)
SELECT history.*
FROM discovery_backfill_prices_all_runs AS history
JOIN latest_run USING (reconstruction_run_id);

COMMENT ON TABLE discovery_backfill_runs IS
    'Immutable retrospective current-universe price/ranking backfill runs; not genuine forward discovery evidence.';
COMMENT ON COLUMN discovery_backfill_runs.input_fitness IS
    'Retrospective Yahoo history uses the installation-time data vintage and cannot claim historical availability.';
COMMENT ON TABLE discovery_backfill_prices IS
    'Retrospective daily price projection with source receipt times and raw Parquet lineage preserved.';

CREATE TRIGGER discovery_backfill_runs_append_only
    BEFORE UPDATE OR DELETE ON discovery_backfill_runs
    FOR EACH ROW EXECUTE FUNCTION reject_historical_truth_mutation();
CREATE TRIGGER discovery_backfill_indexes_append_only
    BEFORE UPDATE OR DELETE ON discovery_backfill_indexes
    FOR EACH ROW EXECUTE FUNCTION reject_historical_truth_mutation();
CREATE TRIGGER discovery_backfill_rankings_append_only
    BEFORE UPDATE OR DELETE ON discovery_backfill_rankings
    FOR EACH ROW EXECUTE FUNCTION reject_historical_truth_mutation();
CREATE TRIGGER discovery_backfill_prices_append_only
    BEFORE UPDATE OR DELETE ON discovery_backfill_prices
    FOR EACH ROW EXECUTE FUNCTION reject_historical_truth_mutation();

COMMIT;
