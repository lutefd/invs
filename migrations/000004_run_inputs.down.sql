BEGIN;

ALTER TABLE ingestion_runs
    DROP CONSTRAINT IF EXISTS ingestion_runs_metadata_run_inputs_check;

COMMENT ON COLUMN ingestion_runs.metadata IS NULL;

COMMIT;
