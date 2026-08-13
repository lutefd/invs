BEGIN;

DELETE FROM macro_observation_snapshots WHERE value IS NULL;

ALTER TABLE macro_observation_snapshots
    ALTER COLUMN value SET NOT NULL;

COMMIT;
