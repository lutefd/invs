BEGIN;

-- Feature rows remain immutable Parquet objects. PostgreSQL stores only the
-- small, queryable registration envelope and explicit lineage pointers.
CREATE TABLE feature_artifacts (
    artifact_id uuid PRIMARY KEY,
    artifact_version text NOT NULL CHECK (artifact_version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
    feature_set text NOT NULL CHECK (feature_set ~ '^[a-z][a-z0-9_-]{1,63}$'),
    feature_set_version text NOT NULL CHECK (feature_set_version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
    registry_sha256 text NOT NULL CHECK (registry_sha256 ~ '^[0-9a-f]{64}$'),
    generator_version text NOT NULL CHECK (btrim(generator_version) <> ''),
    git_commit text NOT NULL CHECK (git_commit = 'unknown' OR git_commit ~ '^[0-9a-f]{40}$'),
    decision_start timestamptz NOT NULL,
    decision_end timestamptz NOT NULL,
    universe_fingerprint text NOT NULL CHECK (universe_fingerprint ~ '^[0-9a-f]{64}$'),
    input_fingerprint text NOT NULL CHECK (input_fingerprint ~ '^[0-9a-f]{64}$'),
    output_manifest_path text NOT NULL CHECK (
        btrim(output_manifest_path) <> ''
        AND left(output_manifest_path, 1) <> '/'
        AND left(output_manifest_path, 1) <> chr(92)
        AND position('..' in output_manifest_path) = 0
    ),
    output_manifest_sha256 text NOT NULL CHECK (output_manifest_sha256 ~ '^[0-9a-f]{64}$'),
    calendar_data_source_id uuid NOT NULL,
    calendar_mic text NOT NULL CHECK (calendar_mic ~ '^[A-Z0-9]{4}$'),
    calendar_version text NOT NULL CHECK (calendar_version ~ '^[a-z][a-z0-9_-]{1,63}$'),
    calendar_session_fingerprint text NOT NULL CHECK (calendar_session_fingerprint ~ '^[0-9a-f]{64}$'),
    calendar_available_at timestamptz NOT NULL,
    decision_clock_policy text NOT NULL CHECK (btrim(decision_clock_policy) <> ''),
    row_count bigint NOT NULL CHECK (row_count >= 0),
    requested_partitions bigint NOT NULL CHECK (requested_partitions > 0),
    accepted_partitions bigint NOT NULL CHECK (accepted_partitions >= 0),
    rejected_partitions bigint NOT NULL CHECK (rejected_partitions >= 0),
    status text NOT NULL DEFAULT 'published' CHECK (status IN ('published', 'invalid', 'orphaned')),
    created_at timestamptz NOT NULL,
    registered_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    registration_sha256 text NOT NULL CHECK (registration_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT feature_artifacts_decision_range CHECK (decision_start <= decision_end),
    CONSTRAINT feature_artifacts_calendar_boundary CHECK (calendar_available_at <= decision_start),
    CONSTRAINT feature_artifacts_partition_counts CHECK (
        requested_partitions = accepted_partitions + rejected_partitions
    ),
    CONSTRAINT feature_artifacts_update_order CHECK (updated_at >= registered_at)
);

CREATE INDEX feature_artifacts_lookup_idx
    ON feature_artifacts (feature_set, feature_set_version, decision_start DESC);
CREATE INDEX feature_artifacts_status_idx
    ON feature_artifacts (status, registered_at DESC);
CREATE UNIQUE INDEX feature_artifacts_content_idx
    ON feature_artifacts (feature_set, feature_set_version, input_fingerprint);

CREATE TABLE feature_artifact_decision_points (
    artifact_id uuid NOT NULL REFERENCES feature_artifacts (artifact_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    ordinal integer NOT NULL CHECK (ordinal >= 0),
    decision_at timestamptz NOT NULL,
    PRIMARY KEY (artifact_id, ordinal),
    UNIQUE (artifact_id, decision_at)
);

CREATE TABLE feature_artifact_universe_members (
    artifact_id uuid NOT NULL REFERENCES feature_artifacts (artifact_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    ordinal integer NOT NULL CHECK (ordinal >= 0),
    security_id uuid NOT NULL,
    PRIMARY KEY (artifact_id, ordinal),
    UNIQUE (artifact_id, security_id)
);

CREATE TABLE feature_artifact_input_refs (
    artifact_id uuid NOT NULL REFERENCES feature_artifacts (artifact_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    input_kind text NOT NULL CHECK (input_kind IN ('manifest', 'part')),
    path text NOT NULL CHECK (
        btrim(path) <> ''
        AND left(path, 1) <> '/'
        AND left(path, 1) <> chr(92)
        AND position('..' in path) = 0
    ),
    sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (artifact_id, input_kind, path)
);

CREATE TABLE feature_artifact_partitions (
    artifact_id uuid NOT NULL REFERENCES feature_artifacts (artifact_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    security_id uuid NOT NULL,
    decision_at timestamptz NOT NULL,
    child_artifact_id uuid NOT NULL,
    manifest_path text NOT NULL CHECK (
        btrim(manifest_path) <> ''
        AND left(manifest_path, 1) <> '/'
        AND left(manifest_path, 1) <> chr(92)
        AND position('..' in manifest_path) = 0
    ),
    manifest_sha256 text NOT NULL CHECK (manifest_sha256 ~ '^[0-9a-f]{64}$'),
    part_path text NOT NULL CHECK (
        btrim(part_path) <> ''
        AND left(part_path, 1) <> '/'
        AND left(part_path, 1) <> chr(92)
        AND position('..' in part_path) = 0
    ),
    part_sha256 text NOT NULL CHECK (part_sha256 ~ '^[0-9a-f]{64}$'),
    row_count bigint NOT NULL CHECK (row_count > 0),
    PRIMARY KEY (artifact_id, security_id, decision_at),
    UNIQUE (artifact_id, child_artifact_id)
);

CREATE INDEX feature_artifact_partitions_child_idx
    ON feature_artifact_partitions (child_artifact_id);

COMMENT ON TABLE feature_artifacts IS
    'Small PostgreSQL discovery and lineage envelope for an immutable feature artifact; feature rows remain in Parquet.';
COMMENT ON COLUMN feature_artifacts.output_manifest_path IS
    'Path relative to the configured feature root. The manifest and its listed parts remain authoritative.';
COMMENT ON COLUMN feature_artifacts.registration_sha256 IS
    'Digest of the complete normalized registration envelope, including lineage child rows.';
COMMENT ON TABLE feature_artifact_input_refs IS
    'Selected canonical manifest and part hashes for the registered feature batch.';
COMMENT ON TABLE feature_artifact_partitions IS
    'Accepted child feature artifact pointers; read the child manifest for row-level feature values.';

COMMIT;
