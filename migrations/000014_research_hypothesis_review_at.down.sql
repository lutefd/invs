BEGIN;

ALTER TABLE research_hypothesis_revisions
    DROP COLUMN review_at;

COMMIT;
