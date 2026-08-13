BEGIN;

DROP TABLE IF EXISTS trading_sessions;
DROP TABLE IF EXISTS calendar_manifests;
DROP TABLE IF EXISTS universe_memberships;
DROP TABLE IF EXISTS security_listing_versions;
DROP TABLE IF EXISTS security_identifier_versions;
DROP FUNCTION IF EXISTS reject_historical_truth_mutation();

COMMIT;
