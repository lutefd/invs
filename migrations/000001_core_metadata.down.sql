BEGIN;

DROP TABLE IF EXISTS ingestion_runs;
DROP TABLE IF EXISTS security_identifiers;
DROP TABLE IF EXISTS data_sources;
DROP TABLE IF EXISTS securities;
DROP TABLE IF EXISTS issuers;
DROP TYPE IF EXISTS ingestion_run_status;

-- Extensions may be shared by other schemas, so this migration deliberately leaves
-- pgcrypto and btree_gist installed.

COMMIT;
