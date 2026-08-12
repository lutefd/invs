BEGIN;

-- Legacy run metadata may omit run_inputs, but new collector runs must store the
-- typed replay payload and a lowercase SHA-256 of its canonical JSON.
ALTER TABLE ingestion_runs
    ADD CONSTRAINT ingestion_runs_metadata_run_inputs_check
    CHECK (
        NOT (metadata ? 'run_inputs')
        OR (
            jsonb_typeof(metadata->'run_inputs') = 'object'
            AND metadata->'run_inputs' ? 'schema_version'
            AND metadata->'run_inputs' ? 'source'
            AND metadata->'run_inputs' ? 'provider'
            AND metadata->'run_inputs' ? 'canonical_json_sha256'
            AND jsonb_typeof(metadata->'run_inputs'->'provider') = 'object'
            AND metadata->'run_inputs'->>'canonical_json_sha256' IS NOT NULL
            AND metadata->'run_inputs'->>'canonical_json_sha256' ~ '^[0-9a-f]{64}$'
        )
    );

COMMENT ON COLUMN ingestion_runs.metadata IS
    'JSON object for operational run metadata. New collector runs include run_inputs with effective non-secret provider inputs and canonical_json_sha256.';

COMMIT;
