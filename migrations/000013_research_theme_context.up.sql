BEGIN;

CREATE TABLE research_theme_indicators (
    indicator_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    theme_id uuid NOT NULL REFERENCES research_themes (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    indicator_key text NOT NULL CHECK (btrim(indicator_key) <> ''),
    display_name text NOT NULL CHECK (btrim(display_name) <> ''),
    source_ref text NOT NULL CHECK (btrim(source_ref) <> ''),
    evidence_refs jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(evidence_refs) = 'array'),
    recorded_at timestamptz NOT NULL,
    record_hash text NOT NULL CHECK (record_hash ~ '^[0-9a-f]{64}$'),
    UNIQUE (theme_id, indicator_key)
);

CREATE TABLE research_theme_feature_refs (
    feature_ref_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    theme_id uuid NOT NULL REFERENCES research_themes (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    feature_set text NOT NULL CHECK (feature_set ~ '^[a-z][a-z0-9_-]{1,63}$'),
    feature_set_version text NOT NULL CHECK (feature_set_version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
    artifact_ref jsonb NOT NULL CHECK (jsonb_typeof(artifact_ref) = 'object'),
    evidence_refs jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(evidence_refs) = 'array'),
    recorded_at timestamptz NOT NULL,
    record_hash text NOT NULL CHECK (record_hash ~ '^[0-9a-f]{64}$'),
    UNIQUE (theme_id, feature_set, feature_set_version)
);

CREATE TABLE research_theme_conditions (
    condition_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    theme_id uuid NOT NULL REFERENCES research_themes (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    condition_type text NOT NULL CHECK (condition_type IN ('invalidation', 'weaken')),
    condition text NOT NULL CHECK (btrim(condition) <> ''),
    evidence_refs jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(evidence_refs) = 'array'),
    recorded_at timestamptz NOT NULL,
    record_hash text NOT NULL CHECK (record_hash ~ '^[0-9a-f]{64}$')
);

CREATE INDEX research_theme_indicators_lookup_idx ON research_theme_indicators (theme_id, indicator_key);
CREATE INDEX research_theme_feature_refs_lookup_idx ON research_theme_feature_refs (theme_id, feature_set, feature_set_version);
CREATE INDEX research_theme_conditions_lookup_idx ON research_theme_conditions (theme_id, condition_type, condition_id);

CREATE TRIGGER research_theme_indicator_immutable
    BEFORE UPDATE OR DELETE ON research_theme_indicators
    FOR EACH ROW EXECUTE FUNCTION research_append_only_guard();
CREATE TRIGGER research_theme_feature_ref_immutable
    BEFORE UPDATE OR DELETE ON research_theme_feature_refs
    FOR EACH ROW EXECUTE FUNCTION research_append_only_guard();
CREATE TRIGGER research_theme_condition_immutable
    BEFORE UPDATE OR DELETE ON research_theme_conditions
    FOR EACH ROW EXECUTE FUNCTION research_append_only_guard();

COMMENT ON TABLE research_theme_indicators IS
    'Reviewed indicator references attached to a theme; source and evidence are explicit.';
COMMENT ON TABLE research_theme_feature_refs IS
    'Reviewed references from a theme to a versioned deterministic feature set or artifact.';
COMMENT ON TABLE research_theme_conditions IS
    'Append-only invalidation and weaken conditions for a reviewed theme interpretation.';

COMMIT;
