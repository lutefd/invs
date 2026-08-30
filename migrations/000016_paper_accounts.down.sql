BEGIN;

DROP VIEW IF EXISTS paper_account_status;
DROP TRIGGER IF EXISTS paper_account_event_immutable ON paper_account_events;
DROP TRIGGER IF EXISTS paper_account_immutable ON paper_accounts;
DROP TRIGGER IF EXISTS paper_account_event_order ON paper_account_events;
DROP FUNCTION IF EXISTS paper_account_event_order_guard();
DROP TABLE IF EXISTS paper_account_events;
DROP TABLE IF EXISTS paper_accounts;

COMMIT;
