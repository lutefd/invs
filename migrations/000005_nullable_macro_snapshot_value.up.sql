BEGIN;

ALTER TABLE macro_observation_snapshots
    ALTER COLUMN value DROP NOT NULL;

COMMENT ON COLUMN macro_observation_snapshots.value IS
    'Latest canonical macro value; NULL preserves an explicit missing or deleted source vintage.';

COMMIT;
