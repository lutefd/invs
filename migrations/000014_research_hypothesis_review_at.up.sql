BEGIN;

ALTER TABLE research_hypothesis_revisions
    ADD COLUMN review_at timestamptz NOT NULL,
    ADD CONSTRAINT research_hypothesis_revision_review_order
        CHECK (review_at > decision_at);

COMMENT ON COLUMN research_hypothesis_revisions.review_at IS
    'Explicit next review or measurement checkpoint for this hypothesis revision.';

COMMIT;
