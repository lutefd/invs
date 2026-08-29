BEGIN;

CREATE TABLE feature_artifact_input_fitness (
    artifact_id uuid NOT NULL REFERENCES feature_artifacts (artifact_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    dataset text NOT NULL CHECK (dataset ~ '^[a-z][a-z0-9_-]{1,63}$'),
    historical_fitness text NOT NULL CHECK (
        historical_fitness IN ('backtest_safe', 'current_research_only', 'installation_replay_only', 'unsupported')
    ),
    availability_policy text NOT NULL CHECK (
        availability_policy IN ('exact_publication', 'source_declared', 'conservative_receipt_time', 'current_snapshot', 'unknown')
    ),
    PRIMARY KEY (artifact_id, dataset)
);

COMMENT ON TABLE feature_artifact_input_fitness IS
    'Historical-fitness and availability labels copied from the controlled feature registry at batch registration.';

COMMIT;
