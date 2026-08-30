BEGIN;

-- Backtest rows and result artifacts remain immutable files. PostgreSQL keeps
-- only the small experiment catalog, lineage pointers, and append-only run
-- event stream needed for discovery and operational recovery.
CREATE TABLE backtest_experiments (
    experiment_id uuid PRIMARY KEY,
    experiment_sha256 text NOT NULL CHECK (experiment_sha256 ~ '^[0-9a-f]{64}$'),
    schema_version text NOT NULL CHECK (schema_version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
    strategy_name text NOT NULL CHECK (strategy_name ~ '^[a-z][a-z0-9_-]{1,63}$'),
    strategy_version text NOT NULL CHECK (strategy_version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
    strategy_git_commit text NOT NULL CHECK (strategy_git_commit = 'unknown' OR strategy_git_commit ~ '^[0-9a-f]{40}$'),
    strategy_parameters jsonb NOT NULL CHECK (jsonb_typeof(strategy_parameters) = 'object'),
    universe_id uuid NOT NULL,
    universe_version text NOT NULL CHECK (universe_version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
    membership_fingerprint text NOT NULL CHECK (membership_fingerprint ~ '^[0-9a-f]{64}$'),
    base_currency text NOT NULL CHECK (base_currency ~ '^[A-Z]{3}$'),
    reporting_currency text CHECK (reporting_currency IS NULL OR reporting_currency ~ '^[A-Z]{3}$'),
    start_date date NOT NULL,
    end_date date NOT NULL,
    validation_partition text NOT NULL CHECK (validation_partition ~ '^[a-z][a-z0-9_-]{1,63}$'),
    benchmark_security_id uuid NOT NULL,
    benchmark_currency text NOT NULL CHECK (benchmark_currency ~ '^[A-Z]{3}$'),
    decision_policy text NOT NULL CHECK (decision_policy = 'after_close_next_session_open'),
    cost_policy_version text NOT NULL CHECK (cost_policy_version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
    rebalance_frequency text NOT NULL CHECK (rebalance_frequency IN ('once', 'daily', 'monthly')),
    input_fingerprint text NOT NULL CHECK (input_fingerprint ~ '^[0-9a-f]{64}$'),
    spec_path text NOT NULL CHECK (
        btrim(spec_path) <> ''
        AND left(spec_path, 1) <> '/'
        AND left(spec_path, 1) <> chr(92)
        AND position('..' in spec_path) = 0
    ),
    engine_version text NOT NULL CHECK (btrim(engine_version) <> ''),
    created_at timestamptz NOT NULL,
    registered_at timestamptz NOT NULL DEFAULT now(),
    registration_sha256 text NOT NULL CHECK (registration_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT backtest_experiment_period_check CHECK (start_date <= end_date),
    UNIQUE (experiment_sha256)
);

CREATE INDEX backtest_experiments_strategy_idx
    ON backtest_experiments (strategy_name, strategy_version, start_date, end_date);
CREATE INDEX backtest_experiments_universe_idx
    ON backtest_experiments (universe_id, start_date, end_date);

CREATE TABLE backtest_experiment_inputs (
    experiment_id uuid NOT NULL REFERENCES backtest_experiments (experiment_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    ordinal integer NOT NULL CHECK (ordinal >= 0),
    input_kind text NOT NULL CHECK (input_kind IN ('prices', 'calendar', 'membership', 'corporate_actions', 'fx', 'feature', 'macro')),
    artifact_id uuid NOT NULL,
    path text NOT NULL CHECK (
        btrim(path) <> ''
        AND left(path, 1) <> '/'
        AND left(path, 1) <> chr(92)
        AND position('..' in path) = 0
    ),
    sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    available_at timestamptz NOT NULL,
    historical_fitness text NOT NULL CHECK (historical_fitness = 'backtest_safe'),
    PRIMARY KEY (experiment_id, ordinal),
    UNIQUE (experiment_id, input_kind)
);

CREATE INDEX backtest_experiment_inputs_artifact_idx
    ON backtest_experiment_inputs (artifact_id, input_kind);

CREATE TABLE backtest_runs (
    run_id uuid PRIMARY KEY,
    experiment_id uuid NOT NULL REFERENCES backtest_experiments (experiment_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    attempt integer NOT NULL CHECK (attempt > 0),
    engine_version text NOT NULL CHECK (btrim(engine_version) <> ''),
    environment jsonb NOT NULL CHECK (jsonb_typeof(environment) = 'object'),
    environment_sha256 text NOT NULL CHECK (environment_sha256 ~ '^[0-9a-f]{64}$'),
    holdout_attempted boolean NOT NULL,
    started_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    record_hash text NOT NULL CHECK (record_hash ~ '^[0-9a-f]{64}$'),
    UNIQUE (experiment_id, attempt)
);

CREATE INDEX backtest_runs_experiment_idx
    ON backtest_runs (experiment_id, attempt);

CREATE TABLE backtest_run_events (
    run_id uuid NOT NULL REFERENCES backtest_runs (run_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    sequence integer NOT NULL CHECK (sequence >= 0),
    status text NOT NULL CHECK (status IN ('running', 'completed', 'failed', 'cancelled')),
    event_at timestamptz NOT NULL,
    result_id uuid,
    result_manifest_path text CHECK (
        result_manifest_path IS NULL OR (
            btrim(result_manifest_path) <> ''
            AND left(result_manifest_path, 1) <> '/'
            AND left(result_manifest_path, 1) <> chr(92)
            AND position('..' in result_manifest_path) = 0
        )
    ),
    result_manifest_sha256 text CHECK (result_manifest_sha256 IS NULL OR result_manifest_sha256 ~ '^[0-9a-f]{64}$'),
    failure_code text,
    failure_message text,
    record_hash text NOT NULL CHECK (record_hash ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (run_id, sequence),
    CONSTRAINT backtest_run_event_result_pair_check CHECK (
        (result_manifest_path IS NULL AND result_manifest_sha256 IS NULL)
        OR (result_manifest_path IS NOT NULL AND result_manifest_sha256 IS NOT NULL)
    ),
    CONSTRAINT backtest_run_event_status_payload_check CHECK (
        (status = 'completed' AND result_id IS NOT NULL AND result_manifest_path IS NOT NULL
            AND result_manifest_sha256 IS NOT NULL AND failure_code IS NULL AND failure_message IS NULL)
        OR (status IN ('failed', 'cancelled') AND result_id IS NULL AND result_manifest_path IS NULL
            AND result_manifest_sha256 IS NULL AND btrim(coalesce(failure_message, '')) <> '')
        OR (status = 'running' AND result_id IS NULL AND result_manifest_path IS NULL
            AND result_manifest_sha256 IS NULL AND failure_code IS NULL AND failure_message IS NULL)
    )
);

CREATE INDEX backtest_run_events_status_idx
    ON backtest_run_events (run_id, sequence DESC);

CREATE OR REPLACE FUNCTION backtest_run_event_order_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    expected_sequence integer;
    started_at_value timestamptz;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('backtest-run:' || NEW.run_id::text, 0));
    SELECT started_at INTO started_at_value
    FROM backtest_runs
    WHERE run_id = NEW.run_id;
    IF started_at_value IS NULL THEN
        RAISE EXCEPTION 'backtest run % does not exist', NEW.run_id;
    END IF;
    IF NEW.event_at < started_at_value THEN
        RAISE EXCEPTION 'backtest run event cannot precede run start';
    END IF;
    SELECT coalesce(max(sequence), -1) + 1 INTO expected_sequence
    FROM backtest_run_events
    WHERE run_id = NEW.run_id;
    IF NEW.sequence <> expected_sequence THEN
        RAISE EXCEPTION 'backtest run event sequence must be %, got %', expected_sequence, NEW.sequence;
    END IF;
    IF EXISTS (
        SELECT 1 FROM backtest_run_events
        WHERE run_id = NEW.run_id AND status IN ('completed', 'failed', 'cancelled')
    ) THEN
        RAISE EXCEPTION 'backtest run % already has a terminal event', NEW.run_id;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER backtest_run_event_order
    BEFORE INSERT ON backtest_run_events
    FOR EACH ROW EXECUTE FUNCTION backtest_run_event_order_guard();

CREATE TRIGGER backtest_experiment_immutable
    BEFORE UPDATE OR DELETE ON backtest_experiments
    FOR EACH ROW EXECUTE FUNCTION research_append_only_guard();
CREATE TRIGGER backtest_experiment_input_immutable
    BEFORE UPDATE OR DELETE ON backtest_experiment_inputs
    FOR EACH ROW EXECUTE FUNCTION research_append_only_guard();
CREATE TRIGGER backtest_run_immutable
    BEFORE UPDATE OR DELETE ON backtest_runs
    FOR EACH ROW EXECUTE FUNCTION research_append_only_guard();
CREATE TRIGGER backtest_run_event_immutable
    BEFORE UPDATE OR DELETE ON backtest_run_events
    FOR EACH ROW EXECUTE FUNCTION research_append_only_guard();

CREATE VIEW backtest_run_status AS
SELECT DISTINCT ON (run_id)
       run_id, sequence, status, event_at, result_id, result_manifest_path,
       result_manifest_sha256, failure_code, failure_message, record_hash
FROM backtest_run_events
ORDER BY run_id, sequence DESC;

COMMENT ON TABLE backtest_experiments IS
    'Immutable point-in-time experiment catalog; the canonical specification remains an addressed file.';
COMMENT ON TABLE backtest_experiment_inputs IS
    'Content-addressed, backtest-safe input references captured with an experiment identity.';
COMMENT ON TABLE backtest_runs IS
    'Immutable operational attempts; retries receive a new attempt and run identity.';
COMMENT ON TABLE backtest_run_events IS
    'Append-only start, completion, and failure events used for recovery without mutating run history.';
COMMENT ON VIEW backtest_run_status IS
    'Latest append-only event for each backtest run; terminal status is derived, not updated in place.';

COMMIT;
