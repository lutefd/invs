BEGIN;

-- v0.4 keeps research metadata relational and small. Source bytes and
-- analytical values remain in immutable filesystem artifacts; these tables
-- retain identity, review state, and the hashes needed to find them again.

CREATE TABLE research_entities (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_type text NOT NULL CHECK (entity_type IN ('company', 'issuer', 'security', 'indicator', 'geography', 'provider')),
    stable_key text NOT NULL CHECK (btrim(stable_key) <> ''),
    display_name text NOT NULL CHECK (btrim(display_name) <> ''),
    issuer_id uuid REFERENCES issuers (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    security_id uuid REFERENCES securities (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT research_entities_security_link_check CHECK (
        entity_type <> 'security' OR security_id IS NOT NULL
    ),
    CONSTRAINT research_entities_issuer_link_check CHECK (
        entity_type <> 'issuer' OR issuer_id IS NOT NULL
    ),
    CONSTRAINT research_entities_cross_link_check CHECK (
        issuer_id IS NULL OR entity_type IN ('company', 'issuer')
    )
);

CREATE UNIQUE INDEX research_entities_stable_key_idx ON research_entities (stable_key);
CREATE INDEX research_entities_type_idx ON research_entities (entity_type, stable_key);

CREATE TABLE research_themes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    stable_key text NOT NULL UNIQUE CHECK (stable_key ~ '^[a-z][a-z0-9_-]{1,63}$'),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE research_theme_revisions (
    theme_id uuid NOT NULL REFERENCES research_themes (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    revision integer NOT NULL CHECK (revision > 0),
    parent_theme_id uuid REFERENCES research_themes (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    name text NOT NULL CHECK (btrim(name) <> ''),
    knowledge_kind text NOT NULL CHECK (knowledge_kind IN ('fact', 'interpretation')),
    review_state text NOT NULL CHECK (review_state IN ('draft', 'reviewed', 'retired')),
    description text NOT NULL CHECK (btrim(description) <> ''),
    evidence_refs jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(evidence_refs) = 'array'),
    author_method text NOT NULL CHECK (btrim(author_method) <> ''),
    recorded_at timestamptz NOT NULL,
    record_hash text NOT NULL CHECK (record_hash ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (theme_id, revision),
    CONSTRAINT research_theme_parent_not_self CHECK (parent_theme_id IS NULL OR parent_theme_id <> theme_id)
);

CREATE INDEX research_theme_revisions_lookup_idx
    ON research_theme_revisions (theme_id, revision DESC);

CREATE TABLE research_theme_memberships (
    theme_id uuid NOT NULL REFERENCES research_themes (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    entity_id uuid NOT NULL REFERENCES research_entities (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    revision integer NOT NULL CHECK (revision > 0),
    role text NOT NULL CHECK (btrim(role) <> ''),
    knowledge_kind text NOT NULL CHECK (knowledge_kind IN ('fact', 'interpretation')),
    confidence numeric(6,5) NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    evidence_refs jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(evidence_refs) = 'array'),
    author_method text NOT NULL CHECK (btrim(author_method) <> ''),
    valid_from timestamptz NOT NULL,
    valid_until timestamptz,
    recorded_at timestamptz NOT NULL,
    revision_state text NOT NULL CHECK (revision_state IN ('proposed', 'reviewed', 'superseded', 'rejected')),
    record_hash text NOT NULL CHECK (record_hash ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (theme_id, entity_id, revision),
    CONSTRAINT research_theme_membership_valid_range CHECK (valid_until IS NULL OR valid_until > valid_from)
);

CREATE INDEX research_theme_memberships_current_idx
    ON research_theme_memberships (theme_id, entity_id, revision DESC);

CREATE TABLE research_relationships (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE research_relationship_revisions (
    relationship_id uuid NOT NULL REFERENCES research_relationships (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    revision integer NOT NULL CHECK (revision > 0),
    from_entity_id uuid NOT NULL REFERENCES research_entities (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    to_entity_id uuid NOT NULL REFERENCES research_entities (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    relationship_type text NOT NULL CHECK (relationship_type IN (
        'SUPPLIES', 'CUSTOMER_OF', 'DEPENDS_ON', 'BENEFITS_FROM', 'EXPOSED_TO',
        'COMPETES_WITH', 'CONSUMES', 'PRODUCES', 'INDICATOR_FOR'
    )),
    direction text NOT NULL CHECK (direction IN ('forward', 'reverse', 'bidirectional')),
    knowledge_kind text NOT NULL CHECK (knowledge_kind IN ('fact', 'interpretation')),
    confidence numeric(6,5) NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    evidence_refs jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(evidence_refs) = 'array'),
    author_method text NOT NULL CHECK (btrim(author_method) <> ''),
    valid_from timestamptz NOT NULL,
    valid_until timestamptz,
    recorded_at timestamptz NOT NULL,
    revision_state text NOT NULL CHECK (revision_state IN ('proposed', 'reviewed', 'superseded', 'rejected')),
    record_hash text NOT NULL CHECK (record_hash ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (relationship_id, revision),
    CONSTRAINT research_relationship_distinct_endpoints CHECK (from_entity_id <> to_entity_id),
    CONSTRAINT research_relationship_valid_range CHECK (valid_until IS NULL OR valid_until > valid_from)
);

CREATE INDEX research_relationship_revisions_lookup_idx
    ON research_relationship_revisions (from_entity_id, to_entity_id, relationship_type, revision DESC);

CREATE TABLE research_documents (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source text NOT NULL CHECK (btrim(source) <> ''),
    source_document_id text NOT NULL CHECK (btrim(source_document_id) <> ''),
    media_type text NOT NULL CHECK (btrim(media_type) <> ''),
    source_uri text NOT NULL CHECK (btrim(source_uri) <> ''),
    published_at timestamptz,
    available_at timestamptz NOT NULL,
    retrieved_at timestamptz NOT NULL,
    supersedes_document_id uuid REFERENCES research_documents (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT research_documents_available_order CHECK (available_at <= retrieved_at),
    CONSTRAINT research_documents_supersession_not_self CHECK (
        supersedes_document_id IS NULL OR supersedes_document_id <> id
    ),
    UNIQUE (source, source_document_id)
);

CREATE INDEX research_documents_available_idx
    ON research_documents (source, available_at, id);

CREATE TABLE research_document_entities (
    document_id uuid NOT NULL REFERENCES research_documents (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    entity_id uuid NOT NULL REFERENCES research_entities (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    relation_kind text NOT NULL CHECK (relation_kind IN ('issuer', 'subject', 'mentioned')),
    recorded_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (document_id, entity_id, relation_kind)
);

CREATE TABLE research_document_artifacts (
    artifact_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id uuid NOT NULL REFERENCES research_documents (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    artifact_kind text NOT NULL CHECK (artifact_kind = 'raw'),
    path text NOT NULL CHECK (
        btrim(path) <> '' AND left(path, 1) <> '/' AND left(path, 1) <> chr(92)
        AND position('..' in path) = 0
    ),
    sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
    content_type text NOT NULL CHECK (btrim(content_type) <> ''),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (document_id, artifact_kind, sha256)
);

CREATE TABLE research_document_text_artifacts (
    artifact_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id uuid NOT NULL REFERENCES research_documents (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    status text NOT NULL CHECK (status IN ('extracted', 'failed')),
    path text CHECK (
        path IS NULL OR (
            btrim(path) <> '' AND left(path, 1) <> '/' AND left(path, 1) <> chr(92)
            AND position('..' in path) = 0
        )
    ),
    input_sha256 text NOT NULL CHECK (input_sha256 ~ '^[0-9a-f]{64}$'),
    output_sha256 text CHECK (output_sha256 IS NULL OR output_sha256 ~ '^[0-9a-f]{64}$'),
    extractor text NOT NULL CHECK (btrim(extractor) <> ''),
    extractor_version text NOT NULL CHECK (btrim(extractor_version) <> ''),
    config jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(config) = 'object'),
    locators jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(locators) = 'array'),
    errors jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(errors) = 'array'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (document_id, input_sha256),
    CONSTRAINT research_document_text_result_check CHECK (
        (status = 'extracted' AND path IS NOT NULL AND output_sha256 IS NOT NULL AND jsonb_array_length(errors) = 0)
        OR
        (status = 'failed' AND jsonb_array_length(errors) > 0)
    )
);

CREATE TABLE research_event_proposals (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE research_event_proposal_revisions (
    proposal_id uuid NOT NULL REFERENCES research_event_proposals (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    revision integer NOT NULL CHECK (revision > 0),
    document_id uuid NOT NULL REFERENCES research_documents (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    event_type text NOT NULL CHECK (event_type IN (
        'capex_guidance', 'production_guidance', 'material_customer_supplier',
        'financing', 'theme_exposure'
    )),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object' AND payload <> '{}'::jsonb),
    source_spans jsonb NOT NULL CHECK (jsonb_typeof(source_spans) = 'array' AND jsonb_array_length(source_spans) > 0),
    prompt_version text NOT NULL CHECK (btrim(prompt_version) <> ''),
    model text NOT NULL CHECK (btrim(model) <> ''),
    parameters jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(parameters) = 'object'),
    extraction_version text NOT NULL CHECK (btrim(extraction_version) <> ''),
    source_document_sha256 text NOT NULL CHECK (source_document_sha256 ~ '^[0-9a-f]{64}$'),
    confidence numeric(6,5) NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    status text NOT NULL CHECK (status IN ('proposed', 'accepted', 'rejected', 'superseded')),
    proposed_at timestamptz NOT NULL,
    reviewer_id text,
    review_method text,
    reviewed_at timestamptz,
    review_note text,
    record_hash text NOT NULL CHECK (record_hash ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (proposal_id, revision),
    CONSTRAINT research_event_review_auth_check CHECK (
        status = 'proposed'
        OR (
            btrim(coalesce(reviewer_id, '')) <> ''
            AND review_method = 'human'
            AND reviewed_at IS NOT NULL
            AND btrim(coalesce(review_note, '')) <> ''
        )
    )
);

CREATE INDEX research_event_proposal_status_idx
    ON research_event_proposal_revisions (document_id, status, proposed_at);

CREATE TABLE research_evidence_packs (
    id uuid PRIMARY KEY,
    decision_at timestamptz NOT NULL,
    path text NOT NULL CHECK (
        btrim(path) <> '' AND left(path, 1) <> '/' AND left(path, 1) <> chr(92)
        AND position('..' in path) = 0
    ),
    content_sha256 text NOT NULL CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL,
    UNIQUE (content_sha256)
);

CREATE INDEX research_evidence_packs_decision_idx
    ON research_evidence_packs (decision_at, id);

CREATE TABLE research_hypotheses (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title text NOT NULL CHECK (btrim(title) <> ''),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'closed', 'invalidated')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE research_hypothesis_revisions (
    hypothesis_id uuid NOT NULL REFERENCES research_hypotheses (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    revision integer NOT NULL CHECK (revision > 0),
    thesis text NOT NULL CHECK (btrim(thesis) <> ''),
    causal_model jsonb NOT NULL CHECK (jsonb_typeof(causal_model) = 'object' AND causal_model <> '{}'::jsonb),
    horizon text NOT NULL CHECK (btrim(horizon) <> ''),
    benchmark text NOT NULL CHECK (btrim(benchmark) <> ''),
    universe jsonb NOT NULL CHECK (jsonb_typeof(universe) = 'array' AND jsonb_array_length(universe) > 0),
    invalidation_conditions jsonb NOT NULL CHECK (jsonb_typeof(invalidation_conditions) = 'array' AND jsonb_array_length(invalidation_conditions) > 0),
    decision_at timestamptz NOT NULL,
    evidence_pack_id uuid NOT NULL REFERENCES research_evidence_packs (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    evidence_pack_sha256 text NOT NULL CHECK (evidence_pack_sha256 ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    record_hash text NOT NULL CHECK (record_hash ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (hypothesis_id, revision)
);

CREATE INDEX research_hypothesis_revisions_lookup_idx
    ON research_hypothesis_revisions (hypothesis_id, revision DESC);

CREATE TABLE research_hypothesis_evidence (
    evidence_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    hypothesis_id uuid NOT NULL,
    hypothesis_revision integer NOT NULL,
    evidence_ref jsonb NOT NULL CHECK (jsonb_typeof(evidence_ref) = 'object'),
    direction text NOT NULL CHECK (direction IN ('supports', 'contradicts', 'neutral')),
    weight numeric(8,5) NOT NULL CHECK (weight >= 0),
    note text NOT NULL CHECK (btrim(note) <> ''),
    available_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    record_hash text NOT NULL CHECK (record_hash ~ '^[0-9a-f]{64}$'),
    FOREIGN KEY (hypothesis_id, hypothesis_revision)
        REFERENCES research_hypothesis_revisions (hypothesis_id, revision)
        ON UPDATE RESTRICT ON DELETE RESTRICT
);

CREATE TABLE research_predictions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    hypothesis_id uuid NOT NULL,
    hypothesis_revision integer NOT NULL,
    asset_or_universe jsonb NOT NULL CHECK (jsonb_typeof(asset_or_universe) = 'array' AND jsonb_array_length(asset_or_universe) > 0),
    expected_direction text NOT NULL CHECK (expected_direction IN (
        'up', 'down', 'flat', 'relative_outperformance', 'relative_underperformance'
    )),
    expected_range jsonb NOT NULL CHECK (jsonb_typeof(expected_range) = 'object' AND expected_range <> '{}'::jsonb),
    horizon text NOT NULL CHECK (btrim(horizon) <> ''),
    confidence numeric(6,5) NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    created_at timestamptz NOT NULL DEFAULT now(),
    frozen_at timestamptz,
    frozen_revision integer,
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'frozen', 'measured', 'invalidated')),
    record_hash text NOT NULL CHECK (record_hash ~ '^[0-9a-f]{64}$'),
    FOREIGN KEY (hypothesis_id, hypothesis_revision)
        REFERENCES research_hypothesis_revisions (hypothesis_id, revision)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT research_prediction_freeze_fields_check CHECK (
        (frozen_at IS NULL AND frozen_revision IS NULL AND status = 'draft')
        OR
        (frozen_at IS NOT NULL AND frozen_revision IS NOT NULL AND frozen_revision = hypothesis_revision AND status IN ('frozen', 'measured', 'invalidated'))
    )
);

CREATE INDEX research_predictions_hypothesis_idx
    ON research_predictions (hypothesis_id, hypothesis_revision, created_at);

CREATE TABLE research_prediction_outcomes (
    outcome_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    prediction_id uuid NOT NULL REFERENCES research_predictions (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    measurement_policy_version text NOT NULL CHECK (btrim(measurement_policy_version) <> ''),
    status text NOT NULL CHECK (status IN ('measured', 'unavailable', 'invalid')),
    realized_result jsonb,
    benchmark_result jsonb,
    drawdown numeric,
    measured_at timestamptz NOT NULL,
    input_artifact_refs jsonb NOT NULL CHECK (jsonb_typeof(input_artifact_refs) = 'array' AND jsonb_array_length(input_artifact_refs) > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    record_hash text NOT NULL CHECK (record_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT research_prediction_outcome_payload_check CHECK (
        status = 'measured' OR (realized_result IS NULL AND benchmark_result IS NULL AND drawdown IS NULL)
    ),
    UNIQUE (prediction_id, measurement_policy_version, measured_at)
);

CREATE OR REPLACE FUNCTION research_append_only_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is append-only; % is not permitted', TG_TABLE_NAME, TG_OP;
END;
$$;

CREATE OR REPLACE FUNCTION research_revision_order_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    expected_revision integer;
    lock_key text;
BEGIN
    IF TG_TABLE_NAME = 'research_theme_revisions' THEN
        lock_key := TG_TABLE_NAME || ':' || NEW.theme_id::text;
        PERFORM pg_advisory_xact_lock(hashtextextended(lock_key, 0));
        SELECT coalesce(max(revision), 0) + 1 INTO expected_revision
        FROM research_theme_revisions WHERE theme_id = NEW.theme_id;
    ELSIF TG_TABLE_NAME = 'research_theme_memberships' THEN
        lock_key := TG_TABLE_NAME || ':' || NEW.theme_id::text || ':' || NEW.entity_id::text;
        PERFORM pg_advisory_xact_lock(hashtextextended(lock_key, 0));
        SELECT coalesce(max(revision), 0) + 1 INTO expected_revision
        FROM research_theme_memberships WHERE theme_id = NEW.theme_id AND entity_id = NEW.entity_id;
    ELSIF TG_TABLE_NAME = 'research_relationship_revisions' THEN
        lock_key := TG_TABLE_NAME || ':' || NEW.relationship_id::text;
        PERFORM pg_advisory_xact_lock(hashtextextended(lock_key, 0));
        SELECT coalesce(max(revision), 0) + 1 INTO expected_revision
        FROM research_relationship_revisions WHERE relationship_id = NEW.relationship_id;
    ELSIF TG_TABLE_NAME = 'research_event_proposal_revisions' THEN
        lock_key := TG_TABLE_NAME || ':' || NEW.proposal_id::text;
        PERFORM pg_advisory_xact_lock(hashtextextended(lock_key, 0));
        SELECT coalesce(max(revision), 0) + 1 INTO expected_revision
        FROM research_event_proposal_revisions WHERE proposal_id = NEW.proposal_id;
    ELSIF TG_TABLE_NAME = 'research_hypothesis_revisions' THEN
        lock_key := TG_TABLE_NAME || ':' || NEW.hypothesis_id::text;
        PERFORM pg_advisory_xact_lock(hashtextextended(lock_key, 0));
        SELECT coalesce(max(revision), 0) + 1 INTO expected_revision
        FROM research_hypothesis_revisions WHERE hypothesis_id = NEW.hypothesis_id;
    ELSE
        RAISE EXCEPTION 'revision-order guard not configured for %', TG_TABLE_NAME;
    END IF;
    IF NEW.revision <> expected_revision THEN
        RAISE EXCEPTION '% revision must be %, got %', TG_TABLE_NAME, expected_revision, NEW.revision;
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION research_prediction_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'research_predictions are immutable; DELETE is not permitted';
    END IF;
    IF OLD.frozen_at IS NOT NULL THEN
        RAISE EXCEPTION 'frozen prediction % is immutable', OLD.id;
    END IF;
    IF NEW.id <> OLD.id OR NEW.created_at <> OLD.created_at OR NEW.hypothesis_id <> OLD.hypothesis_id
       OR NEW.hypothesis_revision <> OLD.hypothesis_revision THEN
        RAISE EXCEPTION 'prediction identity fields are immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION research_hypothesis_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'research_hypotheses are immutable; DELETE is not permitted';
    END IF;
    IF NEW.id <> OLD.id OR NEW.title <> OLD.title OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'hypothesis identity fields are immutable';
    END IF;
    IF OLD.status = 'closed' AND NEW.status <> OLD.status THEN
        RAISE EXCEPTION 'closed hypothesis % cannot reopen', OLD.id;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER research_theme_revision_order
    BEFORE INSERT ON research_theme_revisions
    FOR EACH ROW EXECUTE FUNCTION research_revision_order_guard();
CREATE TRIGGER research_membership_revision_order
    BEFORE INSERT ON research_theme_memberships
    FOR EACH ROW EXECUTE FUNCTION research_revision_order_guard();
CREATE TRIGGER research_relationship_revision_order
    BEFORE INSERT ON research_relationship_revisions
    FOR EACH ROW EXECUTE FUNCTION research_revision_order_guard();
CREATE TRIGGER research_event_revision_order
    BEFORE INSERT ON research_event_proposal_revisions
    FOR EACH ROW EXECUTE FUNCTION research_revision_order_guard();
CREATE TRIGGER research_hypothesis_revision_order
    BEFORE INSERT ON research_hypothesis_revisions
    FOR EACH ROW EXECUTE FUNCTION research_revision_order_guard();

CREATE TRIGGER research_theme_revision_immutable
    BEFORE UPDATE OR DELETE ON research_theme_revisions
    FOR EACH ROW EXECUTE FUNCTION research_append_only_guard();
CREATE TRIGGER research_membership_immutable
    BEFORE UPDATE OR DELETE ON research_theme_memberships
    FOR EACH ROW EXECUTE FUNCTION research_append_only_guard();
CREATE TRIGGER research_relationship_revision_immutable
    BEFORE UPDATE OR DELETE ON research_relationship_revisions
    FOR EACH ROW EXECUTE FUNCTION research_append_only_guard();
CREATE TRIGGER research_document_entity_immutable
    BEFORE UPDATE OR DELETE ON research_document_entities
    FOR EACH ROW EXECUTE FUNCTION research_append_only_guard();
CREATE TRIGGER research_document_artifact_immutable
    BEFORE UPDATE OR DELETE ON research_document_artifacts
    FOR EACH ROW EXECUTE FUNCTION research_append_only_guard();
CREATE TRIGGER research_document_text_immutable
    BEFORE UPDATE OR DELETE ON research_document_text_artifacts
    FOR EACH ROW EXECUTE FUNCTION research_append_only_guard();
CREATE TRIGGER research_event_revision_immutable
    BEFORE UPDATE OR DELETE ON research_event_proposal_revisions
    FOR EACH ROW EXECUTE FUNCTION research_append_only_guard();
CREATE TRIGGER research_evidence_pack_immutable
    BEFORE UPDATE OR DELETE ON research_evidence_packs
    FOR EACH ROW EXECUTE FUNCTION research_append_only_guard();
CREATE TRIGGER research_hypothesis_revision_immutable
    BEFORE UPDATE OR DELETE ON research_hypothesis_revisions
    FOR EACH ROW EXECUTE FUNCTION research_append_only_guard();
CREATE TRIGGER research_hypothesis_evidence_immutable
    BEFORE UPDATE OR DELETE ON research_hypothesis_evidence
    FOR EACH ROW EXECUTE FUNCTION research_append_only_guard();
CREATE TRIGGER research_prediction_outcome_immutable
    BEFORE UPDATE OR DELETE ON research_prediction_outcomes
    FOR EACH ROW EXECUTE FUNCTION research_append_only_guard();
CREATE TRIGGER research_prediction_immutable_after_freeze
    BEFORE UPDATE OR DELETE ON research_predictions
    FOR EACH ROW EXECUTE FUNCTION research_prediction_guard();
CREATE TRIGGER research_hypothesis_identity_guard
    BEFORE UPDATE OR DELETE ON research_hypotheses
    FOR EACH ROW EXECUTE FUNCTION research_hypothesis_guard();

COMMENT ON TABLE research_theme_revisions IS
    'Append-only reviewed theme nodes; a theme identity is stable and every correction is a new revision.';
COMMENT ON TABLE research_relationship_revisions IS
    'Append-only typed relationship facts or interpretations with explicit validity and evidence references.';
COMMENT ON TABLE research_document_text_artifacts IS
    'Deterministic text extraction metadata; failed extraction retains the independently stored raw artifact.';
COMMENT ON TABLE research_event_proposal_revisions IS
    'LLM-derived proposals only; accepted/rejected states require an explicit human review revision.';
COMMENT ON TABLE research_predictions IS
    'A draft may be edited before freezing; a frozen prediction cannot be updated or deleted.';
COMMENT ON TABLE research_prediction_outcomes IS
    'Append-only policy-pinned measurements; unavailable and invalid outcomes remain explicit.';

COMMIT;
