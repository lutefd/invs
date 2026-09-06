BEGIN;

CREATE TABLE discovery_indexes (
    discovery_id uuid PRIMARY KEY,
    model_name text NOT NULL CHECK (model_name ~ '^[a-z][a-z0-9_-]{1,63}$'),
    model_version text NOT NULL CHECK (model_version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
    git_commit text NOT NULL CHECK (git_commit = 'unknown' OR git_commit ~ '^[0-9a-f]{40}$'),
    universe_id uuid NOT NULL,
    universe_name text NOT NULL CHECK (universe_name ~ '^[a-z][a-z0-9_-]{1,63}$'),
    universe_fingerprint text NOT NULL CHECK (universe_fingerprint ~ '^[0-9a-f]{64}$'),
    market_session date NOT NULL,
    decision_at timestamptz NOT NULL,
    feature_batch_id uuid NOT NULL,
    feature_input_fingerprint text NOT NULL CHECK (feature_input_fingerprint ~ '^[0-9a-f]{64}$'),
    candidate_count integer NOT NULL CHECK (candidate_count >= 0),
    eligible_count integer NOT NULL CHECK (eligible_count >= 0),
    rejected_count integer NOT NULL CHECK (rejected_count >= 0),
    manifest_path text NOT NULL CHECK (
        btrim(manifest_path) <> '' AND left(manifest_path, 1) <> '/'
        AND left(manifest_path, 1) <> chr(92) AND position('..' in manifest_path) = 0
    ),
    manifest_sha256 text NOT NULL CHECK (manifest_sha256 ~ '^[0-9a-f]{64}$'),
    registration_sha256 text NOT NULL CHECK (registration_sha256 ~ '^[0-9a-f]{64}$'),
    registered_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT discovery_indexes_counts CHECK (candidate_count <= eligible_count),
    UNIQUE (model_name, model_version, universe_id, market_session)
);

CREATE TABLE discovery_rankings (
    discovery_id uuid NOT NULL REFERENCES discovery_indexes (discovery_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
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
    PRIMARY KEY (discovery_id, security_id),
    CONSTRAINT discovery_rankings_eligibility CHECK (
        (eligibility_status = 'eligible' AND rank IS NOT NULL AND score IS NOT NULL)
        OR (eligibility_status = 'rejected' AND rank IS NULL AND score IS NULL AND NOT candidate)
    )
);

CREATE UNIQUE INDEX discovery_rankings_rank_idx
    ON discovery_rankings (discovery_id, rank) WHERE rank IS NOT NULL;
CREATE INDEX discovery_rankings_security_history_idx
    ON discovery_rankings (security_id, discovery_id);

CREATE VIEW discovery_latest_rankings AS
WITH latest AS (
    SELECT DISTINCT ON (model_name, model_version, universe_id)
        discovery_id
    FROM discovery_indexes
    ORDER BY model_name, model_version, universe_id, market_session DESC, decision_at DESC
)
SELECT
    index.discovery_id,
    index.market_session,
    index.decision_at,
    index.model_name,
    index.model_version,
    index.universe_id,
    index.universe_name,
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
FROM latest
JOIN discovery_indexes index USING (discovery_id)
JOIN discovery_rankings ranking USING (discovery_id);

COMMENT ON TABLE discovery_indexes IS
    'Immutable daily stock-discovery index envelopes; referenced feature artifacts remain authoritative.';
COMMENT ON TABLE discovery_rankings IS
    'Daily cross-sectional ranks and explicit eligibility results for tracked securities.';

COMMIT;
