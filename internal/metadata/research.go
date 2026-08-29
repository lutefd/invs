package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var researchSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

var researchRelationshipTypes = map[string]struct{}{
	"SUPPLIES": {}, "CUSTOMER_OF": {}, "DEPENDS_ON": {}, "BENEFITS_FROM": {},
	"EXPOSED_TO": {}, "COMPETES_WITH": {}, "CONSUMES": {}, "PRODUCES": {},
	"INDICATOR_FOR": {},
}

func requireResearchRepository(r *Repository) error {
	if r == nil || r.pool == nil {
		return errors.New("PostgreSQL repository is required")
	}
	return nil
}

func canonicalResearchJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode research record: %w", err)
	}
	return encoded, nil
}

func researchRecordHash(value any) (string, error) {
	encoded, err := canonicalResearchJSON(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func requireResearchUUID(value, label string, optional bool) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" && optional {
		return "", nil
	}
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.String() != value {
		return "", fmt.Errorf("%s must be a canonical UUID", label)
	}
	return value, nil
}

func requireResearchHash(value, label string) error {
	if !researchSHA256Pattern.MatchString(strings.TrimSpace(value)) {
		return fmt.Errorf("%s must be a lowercase SHA-256", label)
	}
	return nil
}

func requireResearchJSON(value json.RawMessage, label, expectedType string, nonEmpty bool) ([]byte, error) {
	if len(value) == 0 {
		if expectedType == "array" {
			return []byte("[]"), nil
		}
		return []byte("{}"), nil
	}
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return nil, fmt.Errorf("%s must be JSON: %w", label, err)
	}
	actual := "value"
	switch decoded.(type) {
	case map[string]any:
		actual = "object"
	case []any:
		actual = "array"
	}
	if actual != expectedType {
		return nil, fmt.Errorf("%s must be a JSON %s", label, expectedType)
	}
	if nonEmpty {
		switch typed := decoded.(type) {
		case map[string]any:
			if len(typed) == 0 {
				return nil, fmt.Errorf("%s must not be empty", label)
			}
		case []any:
			if len(typed) == 0 {
				return nil, fmt.Errorf("%s must not be empty", label)
			}
		}
	}
	return append([]byte(nil), value...), nil
}

func evidenceRefsJSON(refs []ResearchEvidenceRef) ([]byte, error) {
	if refs == nil {
		refs = []ResearchEvidenceRef{}
	}
	for index, ref := range refs {
		if strings.TrimSpace(ref.Kind) == "" || strings.TrimSpace(ref.ID) == "" || strings.TrimSpace(ref.Locator) == "" {
			return nil, fmt.Errorf("evidence_refs[%d] requires kind, id, and locator", index)
		}
		if err := requireResearchHash(ref.SHA256, fmt.Sprintf("evidence_refs[%d].sha256", index)); err != nil {
			return nil, err
		}
	}
	return json.Marshal(refs)
}

func nextResearchRevision(ctx context.Context, tx pgx.Tx, table, identitySQL string, identity any) (int, error) {
	var revision int
	query := fmt.Sprintf("SELECT coalesce(max(revision), 0) + 1 FROM %s WHERE %s", table, identitySQL)
	if err := tx.QueryRow(ctx, query, identity).Scan(&revision); err != nil {
		return 0, err
	}
	return revision, nil
}

func nextResearchRevisionPair(ctx context.Context, tx pgx.Tx, table, identitySQL string, first, second any) (int, error) {
	var revision int
	query := fmt.Sprintf("SELECT coalesce(max(revision), 0) + 1 FROM %s WHERE %s", table, identitySQL)
	if err := tx.QueryRow(ctx, query, first, second).Scan(&revision); err != nil {
		return 0, err
	}
	return revision, nil
}

func (r *Repository) CreateResearchEntity(ctx context.Context, entity ResearchEntity) (ResearchEntity, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchEntity{}, err
	}
	entity.EntityType = strings.TrimSpace(entity.EntityType)
	entity.StableKey = strings.TrimSpace(entity.StableKey)
	entity.DisplayName = strings.TrimSpace(entity.DisplayName)
	if entity.EntityType == "" || entity.StableKey == "" || entity.DisplayName == "" {
		return ResearchEntity{}, errors.New("research entity type, stable_key, and display_name are required")
	}
	if entity.Metadata == nil {
		entity.Metadata = json.RawMessage(`{}`)
	}
	metadata, err := requireResearchJSON(entity.Metadata, "metadata", "object", false)
	if err != nil {
		return ResearchEntity{}, err
	}
	issuerID, err := requireResearchUUID(entity.IssuerID, "issuer_id", true)
	if err != nil {
		return ResearchEntity{}, err
	}
	securityID, err := requireResearchUUID(entity.SecurityID, "security_id", true)
	if err != nil {
		return ResearchEntity{}, err
	}
	if entity.ID != "" {
		if _, err := requireResearchUUID(entity.ID, "id", false); err != nil {
			return ResearchEntity{}, err
		}
	}
	query := `
INSERT INTO research_entities(id, entity_type, stable_key, display_name, issuer_id, security_id, metadata)
VALUES (COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()), $2, $3, $4, NULLIF($5, '')::uuid, NULLIF($6, '')::uuid, $7::jsonb)
RETURNING id::text, created_at`
	if err := r.pool.QueryRow(ctx, query, entity.ID, entity.EntityType, entity.StableKey, entity.DisplayName, issuerID, securityID, metadata).Scan(&entity.ID, &entity.CreatedAt); err != nil {
		return ResearchEntity{}, fmt.Errorf("create research entity: %w", err)
	}
	return entity, nil
}

func (r *Repository) CreateResearchTheme(ctx context.Context, theme ResearchTheme) (ResearchTheme, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchTheme{}, err
	}
	theme.StableKey = strings.TrimSpace(theme.StableKey)
	if theme.StableKey == "" {
		return ResearchTheme{}, errors.New("research theme stable_key is required")
	}
	if theme.ID != "" {
		if _, err := requireResearchUUID(theme.ID, "id", false); err != nil {
			return ResearchTheme{}, err
		}
	}
	query := `
INSERT INTO research_themes(id, stable_key)
VALUES (COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()), $2)
RETURNING id::text, created_at`
	if err := r.pool.QueryRow(ctx, query, theme.ID, theme.StableKey).Scan(&theme.ID, &theme.CreatedAt); err != nil {
		return ResearchTheme{}, fmt.Errorf("create research theme: %w", err)
	}
	return theme, nil
}

func (r *Repository) AppendResearchThemeRevision(ctx context.Context, revision ResearchThemeRevision) (ResearchThemeRevision, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchThemeRevision{}, err
	}
	themeID, err := requireResearchUUID(revision.ThemeID, "theme_id", false)
	if err != nil {
		return ResearchThemeRevision{}, err
	}
	parentID, err := requireResearchUUID(revision.ParentThemeID, "parent_theme_id", true)
	if err != nil {
		return ResearchThemeRevision{}, err
	}
	revision.ThemeID, revision.ParentThemeID = themeID, parentID
	revision.Name = strings.TrimSpace(revision.Name)
	revision.Description = strings.TrimSpace(revision.Description)
	revision.KnowledgeKind = strings.TrimSpace(revision.KnowledgeKind)
	revision.ReviewState = strings.TrimSpace(revision.ReviewState)
	revision.AuthorMethod = strings.TrimSpace(revision.AuthorMethod)
	if revision.Name == "" || revision.Description == "" || revision.AuthorMethod == "" {
		return ResearchThemeRevision{}, errors.New("theme revision name, description, and author_method are required")
	}
	if revision.RecordedAt.IsZero() {
		revision.RecordedAt = time.Now().UTC()
	}
	evidenceRefs, err := evidenceRefsJSON(revision.EvidenceRefs)
	if err != nil {
		return ResearchThemeRevision{}, err
	}
	if revision.Revision == 0 {
		tx, txErr := r.pool.Begin(ctx)
		if txErr != nil {
			return ResearchThemeRevision{}, txErr
		}
		defer tx.Rollback(ctx)
		revision.Revision, err = nextResearchRevision(ctx, tx, "research_theme_revisions", "theme_id = $1", themeID)
		if err != nil {
			return ResearchThemeRevision{}, err
		}
		if err := insertResearchThemeRevision(ctx, tx, &revision, evidenceRefs); err != nil {
			return ResearchThemeRevision{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ResearchThemeRevision{}, err
		}
		return revision, nil
	}
	if revision.Revision < 1 {
		return ResearchThemeRevision{}, errors.New("theme revision must be positive")
	}
	if err := insertResearchThemeRevision(ctx, r.pool, &revision, evidenceRefs); err != nil {
		return ResearchThemeRevision{}, err
	}
	return revision, nil
}

func insertResearchThemeRevision(ctx context.Context, exec interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, revision *ResearchThemeRevision, evidenceRefs []byte) error {
	if revision.RecordHash == "" {
		var err error
		revision.RecordHash, err = researchRecordHash(struct {
			ThemeID, ParentThemeID, Name, KnowledgeKind, ReviewState, Description, AuthorMethod string
			Revision                                                                            int
			EvidenceRefs                                                                        []ResearchEvidenceRef
			RecordedAt                                                                          time.Time
		}{revision.ThemeID, revision.ParentThemeID, revision.Name, revision.KnowledgeKind, revision.ReviewState, revision.Description, revision.AuthorMethod, revision.Revision, revision.EvidenceRefs, revision.RecordedAt.UTC()})
		if err != nil {
			return err
		}
	}
	if err := requireResearchHash(revision.RecordHash, "record_hash"); err != nil {
		return err
	}
	_, err := exec.Exec(ctx, `
INSERT INTO research_theme_revisions(theme_id, revision, parent_theme_id, name, knowledge_kind, review_state, description, evidence_refs, author_method, recorded_at, record_hash)
VALUES ($1::uuid, $2, NULLIF($3, '')::uuid, $4, $5, $6, $7, $8::jsonb, $9, $10, $11)`,
		revision.ThemeID, revision.Revision, revision.ParentThemeID, revision.Name, revision.KnowledgeKind,
		revision.ReviewState, revision.Description, evidenceRefs, revision.AuthorMethod, revision.RecordedAt.UTC(), revision.RecordHash)
	return err
}

func (r *Repository) AppendResearchThemeMembership(ctx context.Context, membership ResearchThemeMembership) (ResearchThemeMembership, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchThemeMembership{}, err
	}
	var err error
	membership.ThemeID, err = requireResearchUUID(membership.ThemeID, "theme_id", false)
	if err != nil {
		return ResearchThemeMembership{}, err
	}
	membership.EntityID, err = requireResearchUUID(membership.EntityID, "entity_id", false)
	if err != nil {
		return ResearchThemeMembership{}, err
	}
	membership.Role = strings.TrimSpace(membership.Role)
	membership.AuthorMethod = strings.TrimSpace(membership.AuthorMethod)
	if membership.Role == "" || membership.AuthorMethod == "" {
		return ResearchThemeMembership{}, errors.New("theme membership role and author_method are required")
	}
	if membership.RecordedAt.IsZero() {
		membership.RecordedAt = time.Now().UTC()
	}
	evidenceRefs, err := evidenceRefsJSON(membership.EvidenceRefs)
	if err != nil {
		return ResearchThemeMembership{}, err
	}
	if membership.Revision == 0 {
		tx, txErr := r.pool.Begin(ctx)
		if txErr != nil {
			return ResearchThemeMembership{}, txErr
		}
		defer tx.Rollback(ctx)
		membership.Revision, err = nextResearchRevisionPair(ctx, tx, "research_theme_memberships", "theme_id = $1 AND entity_id = $2", membership.ThemeID, membership.EntityID)
		if err != nil {
			return ResearchThemeMembership{}, err
		}
		if err := insertResearchThemeMembership(ctx, tx, &membership, evidenceRefs); err != nil {
			return ResearchThemeMembership{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ResearchThemeMembership{}, err
		}
		return membership, nil
	}
	if membership.Revision < 1 {
		return ResearchThemeMembership{}, errors.New("theme membership revision must be positive")
	}
	if err := insertResearchThemeMembership(ctx, r.pool, &membership, evidenceRefs); err != nil {
		return ResearchThemeMembership{}, err
	}
	return membership, nil
}

func insertResearchThemeMembership(ctx context.Context, exec interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, membership *ResearchThemeMembership, evidenceRefs []byte) error {
	if membership.RecordHash == "" {
		var err error
		membership.RecordHash, err = researchRecordHash(struct {
			ThemeID, EntityID, Role, KnowledgeKind, AuthorMethod, RevisionState string
			Revision                                                            int
			Confidence                                                          float64
			EvidenceRefs                                                        []ResearchEvidenceRef
			ValidFrom                                                           time.Time
			ValidUntil                                                          *time.Time
			RecordedAt                                                          time.Time
		}{membership.ThemeID, membership.EntityID, membership.Role, membership.KnowledgeKind, membership.AuthorMethod, membership.RevisionState, membership.Revision, membership.Confidence, membership.EvidenceRefs, membership.ValidFrom.UTC(), membership.ValidUntil, membership.RecordedAt.UTC()})
		if err != nil {
			return err
		}
	}
	if err := requireResearchHash(membership.RecordHash, "record_hash"); err != nil {
		return err
	}
	_, err := exec.Exec(ctx, `
INSERT INTO research_theme_memberships(theme_id, entity_id, revision, role, knowledge_kind, confidence, evidence_refs, author_method, valid_from, valid_until, recorded_at, revision_state, record_hash)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7::jsonb, $8, $9, $10, $11, $12, $13)`,
		membership.ThemeID, membership.EntityID, membership.Revision, membership.Role, membership.KnowledgeKind,
		membership.Confidence, evidenceRefs, membership.AuthorMethod, membership.ValidFrom.UTC(), membership.ValidUntil,
		membership.RecordedAt.UTC(), membership.RevisionState, membership.RecordHash)
	return err
}

func (r *Repository) AppendResearchThemeIndicator(ctx context.Context, indicator ResearchThemeIndicator) (ResearchThemeIndicator, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchThemeIndicator{}, err
	}
	var err error
	indicator.ThemeID, err = requireResearchUUID(indicator.ThemeID, "theme_id", false)
	if err != nil {
		return ResearchThemeIndicator{}, err
	}
	indicator.IndicatorID, err = requireResearchUUID(indicator.IndicatorID, "indicator_id", true)
	if err != nil {
		return ResearchThemeIndicator{}, err
	}
	indicator.IndicatorKey = strings.TrimSpace(indicator.IndicatorKey)
	indicator.DisplayName = strings.TrimSpace(indicator.DisplayName)
	indicator.SourceRef = strings.TrimSpace(indicator.SourceRef)
	if indicator.IndicatorKey == "" || indicator.DisplayName == "" || indicator.SourceRef == "" {
		return ResearchThemeIndicator{}, errors.New("theme indicator key, display_name, and source_ref are required")
	}
	if indicator.RecordedAt.IsZero() {
		indicator.RecordedAt = time.Now().UTC()
	}
	evidenceRefs, err := evidenceRefsJSON(indicator.EvidenceRefs)
	if err != nil {
		return ResearchThemeIndicator{}, err
	}
	if indicator.RecordHash == "" {
		indicator.RecordHash, err = researchRecordHash(struct {
			IndicatorID, ThemeID, IndicatorKey, DisplayName, SourceRef string
			EvidenceRefs                                               []ResearchEvidenceRef
			RecordedAt                                                 time.Time
		}{indicator.IndicatorID, indicator.ThemeID, indicator.IndicatorKey, indicator.DisplayName, indicator.SourceRef, indicator.EvidenceRefs, indicator.RecordedAt.UTC()})
		if err != nil {
			return ResearchThemeIndicator{}, err
		}
	}
	if err := requireResearchHash(indicator.RecordHash, "record_hash"); err != nil {
		return ResearchThemeIndicator{}, err
	}
	if err := r.pool.QueryRow(ctx, `
INSERT INTO research_theme_indicators(indicator_id, theme_id, indicator_key, display_name, source_ref, evidence_refs, recorded_at, record_hash)
VALUES (COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()), $2::uuid, $3, $4, $5, $6::jsonb, $7, $8)
RETURNING indicator_id::text`, indicator.IndicatorID, indicator.ThemeID, indicator.IndicatorKey, indicator.DisplayName, indicator.SourceRef, evidenceRefs, indicator.RecordedAt.UTC(), indicator.RecordHash).Scan(&indicator.IndicatorID); err != nil {
		return ResearchThemeIndicator{}, fmt.Errorf("append research theme indicator: %w", err)
	}
	return indicator, nil
}

func (r *Repository) AppendResearchThemeFeatureRef(ctx context.Context, featureRef ResearchThemeFeatureRef) (ResearchThemeFeatureRef, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchThemeFeatureRef{}, err
	}
	var err error
	featureRef.ThemeID, err = requireResearchUUID(featureRef.ThemeID, "theme_id", false)
	if err != nil {
		return ResearchThemeFeatureRef{}, err
	}
	featureRef.FeatureRefID, err = requireResearchUUID(featureRef.FeatureRefID, "feature_ref_id", true)
	if err != nil {
		return ResearchThemeFeatureRef{}, err
	}
	featureRef.FeatureSet = strings.TrimSpace(featureRef.FeatureSet)
	featureRef.FeatureSetVersion = strings.TrimSpace(featureRef.FeatureSetVersion)
	if featureRef.FeatureSet == "" || featureRef.FeatureSetVersion == "" {
		return ResearchThemeFeatureRef{}, errors.New("theme feature_ref feature_set and feature_set_version are required")
	}
	artifactRef, err := requireResearchJSON(featureRef.ArtifactRef, "artifact_ref", "object", true)
	if err != nil {
		return ResearchThemeFeatureRef{}, err
	}
	if featureRef.RecordedAt.IsZero() {
		featureRef.RecordedAt = time.Now().UTC()
	}
	evidenceRefs, err := evidenceRefsJSON(featureRef.EvidenceRefs)
	if err != nil {
		return ResearchThemeFeatureRef{}, err
	}
	if featureRef.RecordHash == "" {
		featureRef.RecordHash, err = researchRecordHash(struct {
			FeatureRefID, ThemeID, FeatureSet, FeatureSetVersion string
			ArtifactRef                                          json.RawMessage
			EvidenceRefs                                         []ResearchEvidenceRef
			RecordedAt                                           time.Time
		}{featureRef.FeatureRefID, featureRef.ThemeID, featureRef.FeatureSet, featureRef.FeatureSetVersion, featureRef.ArtifactRef, featureRef.EvidenceRefs, featureRef.RecordedAt.UTC()})
		if err != nil {
			return ResearchThemeFeatureRef{}, err
		}
	}
	if err := requireResearchHash(featureRef.RecordHash, "record_hash"); err != nil {
		return ResearchThemeFeatureRef{}, err
	}
	if err := r.pool.QueryRow(ctx, `
INSERT INTO research_theme_feature_refs(feature_ref_id, theme_id, feature_set, feature_set_version, artifact_ref, evidence_refs, recorded_at, record_hash)
VALUES (COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()), $2::uuid, $3, $4, $5::jsonb, $6::jsonb, $7, $8)
RETURNING feature_ref_id::text`, featureRef.FeatureRefID, featureRef.ThemeID, featureRef.FeatureSet, featureRef.FeatureSetVersion, artifactRef, evidenceRefs, featureRef.RecordedAt.UTC(), featureRef.RecordHash).Scan(&featureRef.FeatureRefID); err != nil {
		return ResearchThemeFeatureRef{}, fmt.Errorf("append research theme feature reference: %w", err)
	}
	return featureRef, nil
}

func (r *Repository) AppendResearchThemeCondition(ctx context.Context, condition ResearchThemeCondition) (ResearchThemeCondition, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchThemeCondition{}, err
	}
	var err error
	condition.ThemeID, err = requireResearchUUID(condition.ThemeID, "theme_id", false)
	if err != nil {
		return ResearchThemeCondition{}, err
	}
	condition.ConditionID, err = requireResearchUUID(condition.ConditionID, "condition_id", true)
	if err != nil {
		return ResearchThemeCondition{}, err
	}
	condition.ConditionType = strings.TrimSpace(condition.ConditionType)
	condition.Condition = strings.TrimSpace(condition.Condition)
	if condition.ConditionType != "invalidation" && condition.ConditionType != "weaken" {
		return ResearchThemeCondition{}, errors.New("theme condition type must be invalidation or weaken")
	}
	if condition.Condition == "" {
		return ResearchThemeCondition{}, errors.New("theme condition is required")
	}
	if condition.RecordedAt.IsZero() {
		condition.RecordedAt = time.Now().UTC()
	}
	evidenceRefs, err := evidenceRefsJSON(condition.EvidenceRefs)
	if err != nil {
		return ResearchThemeCondition{}, err
	}
	if condition.RecordHash == "" {
		condition.RecordHash, err = researchRecordHash(struct {
			ConditionID, ThemeID, ConditionType, Condition string
			EvidenceRefs                                   []ResearchEvidenceRef
			RecordedAt                                     time.Time
		}{condition.ConditionID, condition.ThemeID, condition.ConditionType, condition.Condition, condition.EvidenceRefs, condition.RecordedAt.UTC()})
		if err != nil {
			return ResearchThemeCondition{}, err
		}
	}
	if err := requireResearchHash(condition.RecordHash, "record_hash"); err != nil {
		return ResearchThemeCondition{}, err
	}
	if err := r.pool.QueryRow(ctx, `
INSERT INTO research_theme_conditions(condition_id, theme_id, condition_type, condition, evidence_refs, recorded_at, record_hash)
VALUES (COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()), $2::uuid, $3, $4, $5::jsonb, $6, $7)
RETURNING condition_id::text`, condition.ConditionID, condition.ThemeID, condition.ConditionType, condition.Condition, evidenceRefs, condition.RecordedAt.UTC(), condition.RecordHash).Scan(&condition.ConditionID); err != nil {
		return ResearchThemeCondition{}, fmt.Errorf("append research theme condition: %w", err)
	}
	return condition, nil
}

func (r *Repository) CreateResearchRelationship(ctx context.Context, relationship ResearchRelationship) (ResearchRelationship, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchRelationship{}, err
	}
	if relationship.ID != "" {
		if _, err := requireResearchUUID(relationship.ID, "id", false); err != nil {
			return ResearchRelationship{}, err
		}
	}
	if err := r.pool.QueryRow(ctx, `
INSERT INTO research_relationships(id)
VALUES (COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()))
RETURNING id::text, created_at`, relationship.ID).Scan(&relationship.ID, &relationship.CreatedAt); err != nil {
		return ResearchRelationship{}, fmt.Errorf("create research relationship: %w", err)
	}
	return relationship, nil
}

func (r *Repository) AppendResearchRelationshipRevision(ctx context.Context, revision ResearchRelationshipRevision) (ResearchRelationshipRevision, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchRelationshipRevision{}, err
	}
	var err error
	revision.RelationshipID, err = requireResearchUUID(revision.RelationshipID, "relationship_id", false)
	if err != nil {
		return ResearchRelationshipRevision{}, err
	}
	revision.FromEntityID, err = requireResearchUUID(revision.FromEntityID, "from_entity_id", false)
	if err != nil {
		return ResearchRelationshipRevision{}, err
	}
	revision.ToEntityID, err = requireResearchUUID(revision.ToEntityID, "to_entity_id", false)
	if err != nil {
		return ResearchRelationshipRevision{}, err
	}
	if revision.FromEntityID == revision.ToEntityID {
		return ResearchRelationshipRevision{}, errors.New("relationship endpoints must differ")
	}
	revision.RelationshipType = strings.TrimSpace(revision.RelationshipType)
	if _, ok := researchRelationshipTypes[revision.RelationshipType]; !ok {
		return ResearchRelationshipRevision{}, fmt.Errorf("unsupported relationship type %q", revision.RelationshipType)
	}
	revision.AuthorMethod = strings.TrimSpace(revision.AuthorMethod)
	if revision.AuthorMethod == "" {
		return ResearchRelationshipRevision{}, errors.New("relationship author_method is required")
	}
	if revision.RecordedAt.IsZero() {
		revision.RecordedAt = time.Now().UTC()
	}
	evidenceRefs, err := evidenceRefsJSON(revision.EvidenceRefs)
	if err != nil {
		return ResearchRelationshipRevision{}, err
	}
	if revision.Revision == 0 {
		tx, txErr := r.pool.Begin(ctx)
		if txErr != nil {
			return ResearchRelationshipRevision{}, txErr
		}
		defer tx.Rollback(ctx)
		revision.Revision, err = nextResearchRevision(ctx, tx, "research_relationship_revisions", "relationship_id = $1", revision.RelationshipID)
		if err != nil {
			return ResearchRelationshipRevision{}, err
		}
		if err := insertResearchRelationshipRevision(ctx, tx, &revision, evidenceRefs); err != nil {
			return ResearchRelationshipRevision{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ResearchRelationshipRevision{}, err
		}
		return revision, nil
	}
	if revision.Revision < 1 {
		return ResearchRelationshipRevision{}, errors.New("relationship revision must be positive")
	}
	if err := insertResearchRelationshipRevision(ctx, r.pool, &revision, evidenceRefs); err != nil {
		return ResearchRelationshipRevision{}, err
	}
	return revision, nil
}

func insertResearchRelationshipRevision(ctx context.Context, exec interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, revision *ResearchRelationshipRevision, evidenceRefs []byte) error {
	if revision.RecordHash == "" {
		var err error
		revision.RecordHash, err = researchRecordHash(struct {
			RelationshipID, FromEntityID, ToEntityID, RelationshipType, Direction, KnowledgeKind, AuthorMethod, RevisionState string
			Revision                                                                                                          int
			Confidence                                                                                                        float64
			EvidenceRefs                                                                                                      []ResearchEvidenceRef
			ValidFrom                                                                                                         time.Time
			ValidUntil                                                                                                        *time.Time
			RecordedAt                                                                                                        time.Time
		}{revision.RelationshipID, revision.FromEntityID, revision.ToEntityID, revision.RelationshipType, revision.Direction, revision.KnowledgeKind, revision.AuthorMethod, revision.RevisionState, revision.Revision, revision.Confidence, revision.EvidenceRefs, revision.ValidFrom.UTC(), revision.ValidUntil, revision.RecordedAt.UTC()})
		if err != nil {
			return err
		}
	}
	if err := requireResearchHash(revision.RecordHash, "record_hash"); err != nil {
		return err
	}
	_, err := exec.Exec(ctx, `
INSERT INTO research_relationship_revisions(relationship_id, revision, from_entity_id, to_entity_id, relationship_type, direction, knowledge_kind, confidence, evidence_refs, author_method, valid_from, valid_until, recorded_at, revision_state, record_hash)
VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5, $6, $7, $8, $9::jsonb, $10, $11, $12, $13, $14, $15)`,
		revision.RelationshipID, revision.Revision, revision.FromEntityID, revision.ToEntityID, revision.RelationshipType,
		revision.Direction, revision.KnowledgeKind, revision.Confidence, evidenceRefs, revision.AuthorMethod,
		revision.ValidFrom.UTC(), revision.ValidUntil, revision.RecordedAt.UTC(), revision.RevisionState, revision.RecordHash)
	return err
}

func (r *Repository) CreateResearchDocument(ctx context.Context, document ResearchDocument) (ResearchDocument, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchDocument{}, err
	}
	for label, value := range map[string]string{"source": document.Source, "source_document_id": document.SourceDocumentID, "media_type": document.MediaType, "source_uri": document.SourceURI} {
		if strings.TrimSpace(value) == "" {
			return ResearchDocument{}, fmt.Errorf("document %s is required", label)
		}
	}
	if document.Metadata == nil {
		document.Metadata = json.RawMessage(`{}`)
	}
	metadata, err := requireResearchJSON(document.Metadata, "metadata", "object", false)
	if err != nil {
		return ResearchDocument{}, err
	}
	if document.AvailableAt.IsZero() || document.RetrievedAt.IsZero() {
		return ResearchDocument{}, errors.New("document available_at and retrieved_at are required")
	}
	document.SupersedesDocumentID, err = requireResearchUUID(document.SupersedesDocumentID, "supersedes_document_id", true)
	if err != nil {
		return ResearchDocument{}, err
	}
	document.ID, err = requireResearchUUID(document.ID, "id", true)
	if err != nil {
		return ResearchDocument{}, err
	}
	query := `
INSERT INTO research_documents(id, source, source_document_id, media_type, source_uri, published_at, available_at, retrieved_at, supersedes_document_id, metadata)
VALUES (COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()), $2, $3, $4, $5, $6, $7, $8, NULLIF($9, '')::uuid, $10::jsonb)
RETURNING id::text, created_at`
	if err := r.pool.QueryRow(ctx, query, document.ID, strings.TrimSpace(document.Source), strings.TrimSpace(document.SourceDocumentID), strings.TrimSpace(document.MediaType), strings.TrimSpace(document.SourceURI), document.PublishedAt, document.AvailableAt.UTC(), document.RetrievedAt.UTC(), document.SupersedesDocumentID, metadata).Scan(&document.ID, &document.CreatedAt); err != nil {
		return ResearchDocument{}, fmt.Errorf("create research document: %w", err)
	}
	return document, nil
}

func (r *Repository) LinkResearchDocumentEntity(ctx context.Context, link ResearchDocumentEntityLink) error {
	if err := requireResearchRepository(r); err != nil {
		return err
	}
	var err error
	link.DocumentID, err = requireResearchUUID(link.DocumentID, "document_id", false)
	if err != nil {
		return err
	}
	link.EntityID, err = requireResearchUUID(link.EntityID, "entity_id", false)
	if err != nil {
		return err
	}
	link.RelationKind = strings.TrimSpace(link.RelationKind)
	if link.RecordedAt.IsZero() {
		link.RecordedAt = time.Now().UTC()
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO research_document_entities(document_id, entity_id, relation_kind, recorded_at) VALUES ($1::uuid, $2::uuid, $3, $4)`, link.DocumentID, link.EntityID, link.RelationKind, link.RecordedAt.UTC())
	if err != nil {
		return fmt.Errorf("link research document entity: %w", err)
	}
	return nil
}

func (r *Repository) RegisterResearchRawArtifact(ctx context.Context, artifact ResearchRawArtifact) (ResearchRawArtifact, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchRawArtifact{}, err
	}
	var err error
	artifact.DocumentID, err = requireResearchUUID(artifact.DocumentID, "document_id", false)
	if err != nil {
		return ResearchRawArtifact{}, err
	}
	artifact.ArtifactID, err = requireResearchUUID(artifact.ArtifactID, "artifact_id", true)
	if err != nil {
		return ResearchRawArtifact{}, err
	}
	if err := requireResearchHash(artifact.SHA256, "sha256"); err != nil {
		return ResearchRawArtifact{}, err
	}
	if strings.TrimSpace(artifact.Path) == "" || strings.TrimSpace(artifact.ContentType) == "" || artifact.SizeBytes < 0 {
		return ResearchRawArtifact{}, errors.New("raw artifact path, content type, and non-negative size are required")
	}
	if err := r.pool.QueryRow(ctx, `
INSERT INTO research_document_artifacts(artifact_id, document_id, artifact_kind, path, sha256, size_bytes, content_type)
VALUES (COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()), $2::uuid, 'raw', $3, $4, $5, $6)
RETURNING artifact_id::text, created_at`, artifact.ArtifactID, artifact.DocumentID, artifact.Path, artifact.SHA256, artifact.SizeBytes, artifact.ContentType).Scan(&artifact.ArtifactID, &artifact.CreatedAt); err != nil {
		return ResearchRawArtifact{}, fmt.Errorf("register raw document artifact: %w", err)
	}
	return artifact, nil
}

func (r *Repository) RegisterResearchTextArtifact(ctx context.Context, artifact ResearchDocumentTextArtifact) (ResearchDocumentTextArtifact, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchDocumentTextArtifact{}, err
	}
	var err error
	artifact.DocumentID, err = requireResearchUUID(artifact.DocumentID, "document_id", false)
	if err != nil {
		return ResearchDocumentTextArtifact{}, err
	}
	artifact.ArtifactID, err = requireResearchUUID(artifact.ArtifactID, "artifact_id", true)
	if err != nil {
		return ResearchDocumentTextArtifact{}, err
	}
	if err := requireResearchHash(artifact.InputSHA256, "input_sha256"); err != nil {
		return ResearchDocumentTextArtifact{}, err
	}
	if artifact.OutputSHA256 != "" {
		if err := requireResearchHash(artifact.OutputSHA256, "output_sha256"); err != nil {
			return ResearchDocumentTextArtifact{}, err
		}
	}
	for label, value := range map[string]string{"status": artifact.Status, "extractor": artifact.Extractor, "extractor_version": artifact.ExtractorVersion} {
		if strings.TrimSpace(value) == "" {
			return ResearchDocumentTextArtifact{}, fmt.Errorf("text artifact %s is required", label)
		}
	}
	config, err := requireResearchJSON(artifact.Config, "config", "object", false)
	if err != nil {
		return ResearchDocumentTextArtifact{}, err
	}
	locators, err := requireResearchJSON(artifact.Locators, "locators", "array", false)
	if err != nil {
		return ResearchDocumentTextArtifact{}, err
	}
	errorsJSON, err := requireResearchJSON(artifact.Errors, "errors", "array", false)
	if err != nil {
		return ResearchDocumentTextArtifact{}, err
	}
	if artifact.Status == "extracted" && (artifact.Path == "" || artifact.OutputSHA256 == "") {
		return ResearchDocumentTextArtifact{}, errors.New("extracted text artifact requires path and output_sha256")
	}
	if artifact.Status == "failed" && string(errorsJSON) == "[]" {
		return ResearchDocumentTextArtifact{}, errors.New("failed text artifact requires errors")
	}
	if err := r.pool.QueryRow(ctx, `
INSERT INTO research_document_text_artifacts(artifact_id, document_id, status, path, input_sha256, output_sha256, extractor, extractor_version, config, locators, errors)
VALUES (COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()), $2::uuid, $3, NULLIF($4, ''), $5, NULLIF($6, ''), $7, $8, $9::jsonb, $10::jsonb, $11::jsonb)
RETURNING artifact_id::text, created_at`, artifact.ArtifactID, artifact.DocumentID, artifact.Status, artifact.Path, artifact.InputSHA256, artifact.OutputSHA256, artifact.Extractor, artifact.ExtractorVersion, config, locators, errorsJSON).Scan(&artifact.ArtifactID, &artifact.CreatedAt); err != nil {
		return ResearchDocumentTextArtifact{}, fmt.Errorf("register document text artifact: %w", err)
	}
	return artifact, nil
}

func (r *Repository) CreateResearchEventProposal(ctx context.Context, proposal ResearchEventProposal) (ResearchEventProposal, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchEventProposal{}, err
	}
	var err error
	proposal.ID, err = requireResearchUUID(proposal.ID, "id", true)
	if err != nil {
		return ResearchEventProposal{}, err
	}
	if err := r.pool.QueryRow(ctx, `INSERT INTO research_event_proposals(id) VALUES (COALESCE(NULLIF($1, '')::uuid, gen_random_uuid())) RETURNING id::text, created_at`, proposal.ID).Scan(&proposal.ID, &proposal.CreatedAt); err != nil {
		return ResearchEventProposal{}, fmt.Errorf("create research event proposal: %w", err)
	}
	return proposal, nil
}

func (r *Repository) AppendResearchEventProposalRevision(ctx context.Context, revision ResearchEventProposalRevision) (ResearchEventProposalRevision, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchEventProposalRevision{}, err
	}
	var err error
	revision.ProposalID, err = requireResearchUUID(revision.ProposalID, "proposal_id", false)
	if err != nil {
		return ResearchEventProposalRevision{}, err
	}
	revision.DocumentID, err = requireResearchUUID(revision.DocumentID, "document_id", false)
	if err != nil {
		return ResearchEventProposalRevision{}, err
	}
	revision.EventType = strings.TrimSpace(revision.EventType)
	revision.PromptVersion = strings.TrimSpace(revision.PromptVersion)
	revision.Model = strings.TrimSpace(revision.Model)
	revision.ExtractionVersion = strings.TrimSpace(revision.ExtractionVersion)
	if revision.ProposedAt.IsZero() {
		revision.ProposedAt = time.Now().UTC()
	}
	for label, value := range map[string]string{"event_type": revision.EventType, "prompt_version": revision.PromptVersion, "model": revision.Model, "extraction_version": revision.ExtractionVersion} {
		if value == "" {
			return ResearchEventProposalRevision{}, fmt.Errorf("event proposal %s is required", label)
		}
	}
	if err := requireResearchHash(revision.SourceDocumentSHA256, "source_document_sha256"); err != nil {
		return ResearchEventProposalRevision{}, err
	}
	payload, err := requireResearchJSON(revision.Payload, "payload", "object", true)
	if err != nil {
		return ResearchEventProposalRevision{}, err
	}
	spans, err := requireResearchJSON(revision.SourceSpans, "source_spans", "array", true)
	if err != nil {
		return ResearchEventProposalRevision{}, err
	}
	parameters, err := requireResearchJSON(revision.Parameters, "parameters", "object", false)
	if err != nil {
		return ResearchEventProposalRevision{}, err
	}
	if revision.ReviewerID != "" || revision.ReviewMethod != "" || revision.ReviewedAt != nil || revision.ReviewNote != "" {
		if revision.Status == "proposed" {
			return ResearchEventProposalRevision{}, errors.New("proposed event cannot carry review authorization")
		}
		if strings.TrimSpace(revision.ReviewerID) == "" || revision.ReviewMethod != "human" || revision.ReviewedAt == nil || strings.TrimSpace(revision.ReviewNote) == "" {
			return ResearchEventProposalRevision{}, errors.New("reviewed event requires reviewer_id, human review_method, reviewed_at, and review_note")
		}
	}
	if revision.Status != "proposed" && (strings.TrimSpace(revision.ReviewerID) == "" || revision.ReviewMethod != "human" || revision.ReviewedAt == nil || strings.TrimSpace(revision.ReviewNote) == "") {
		return ResearchEventProposalRevision{}, errors.New("accepted, rejected, and superseded event revisions require human review authorization")
	}
	if revision.Revision == 0 {
		tx, txErr := r.pool.Begin(ctx)
		if txErr != nil {
			return ResearchEventProposalRevision{}, txErr
		}
		defer tx.Rollback(ctx)
		revision.Revision, err = nextResearchRevision(ctx, tx, "research_event_proposal_revisions", "proposal_id = $1", revision.ProposalID)
		if err != nil {
			return ResearchEventProposalRevision{}, err
		}
		if err := insertResearchEventProposalRevision(ctx, tx, &revision, payload, spans, parameters); err != nil {
			return ResearchEventProposalRevision{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ResearchEventProposalRevision{}, err
		}
		return revision, nil
	}
	if revision.Revision < 1 {
		return ResearchEventProposalRevision{}, errors.New("event proposal revision must be positive")
	}
	if err := insertResearchEventProposalRevision(ctx, r.pool, &revision, payload, spans, parameters); err != nil {
		return ResearchEventProposalRevision{}, err
	}
	return revision, nil
}

func insertResearchEventProposalRevision(ctx context.Context, exec interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, revision *ResearchEventProposalRevision, payload, spans, parameters []byte) error {
	if revision.RecordHash == "" {
		var err error
		revision.RecordHash, err = researchRecordHash(struct {
			ProposalID, DocumentID, EventType, PromptVersion, Model, ExtractionVersion, SourceDocumentSHA256, Status, ReviewerID, ReviewMethod, ReviewNote string
			Revision                                                                                                                                       int
			Payload, SourceSpans, Parameters                                                                                                               json.RawMessage
			Confidence                                                                                                                                     float64
			ProposedAt                                                                                                                                     time.Time
			ReviewedAt                                                                                                                                     *time.Time
		}{revision.ProposalID, revision.DocumentID, revision.EventType, revision.PromptVersion, revision.Model, revision.ExtractionVersion, revision.SourceDocumentSHA256, revision.Status, revision.ReviewerID, revision.ReviewMethod, revision.ReviewNote, revision.Revision, revision.Payload, revision.SourceSpans, revision.Parameters, revision.Confidence, revision.ProposedAt.UTC(), revision.ReviewedAt})
		if err != nil {
			return err
		}
	}
	if err := requireResearchHash(revision.RecordHash, "record_hash"); err != nil {
		return err
	}
	_, err := exec.Exec(ctx, `
INSERT INTO research_event_proposal_revisions(proposal_id, revision, document_id, event_type, payload, source_spans, prompt_version, model, parameters, extraction_version, source_document_sha256, confidence, status, proposed_at, reviewer_id, review_method, reviewed_at, review_note, record_hash)
VALUES ($1::uuid, $2, $3::uuid, $4, $5::jsonb, $6::jsonb, $7, $8, $9::jsonb, $10, $11, $12, $13, $14, NULLIF($15, ''), NULLIF($16, ''), $17, NULLIF($18, ''), $19)`,
		revision.ProposalID, revision.Revision, revision.DocumentID, revision.EventType, payload, spans, revision.PromptVersion,
		revision.Model, parameters, revision.ExtractionVersion, revision.SourceDocumentSHA256, revision.Confidence, revision.Status,
		revision.ProposedAt.UTC(), revision.ReviewerID, revision.ReviewMethod, revision.ReviewedAt, revision.ReviewNote, revision.RecordHash)
	return err
}

func (r *Repository) RegisterResearchEvidencePack(ctx context.Context, pack ResearchEvidencePack) (ResearchEvidencePack, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchEvidencePack{}, err
	}
	var err error
	pack.ID, err = requireResearchUUID(pack.ID, "id", false)
	if err != nil {
		return ResearchEvidencePack{}, err
	}
	if err := requireResearchHash(pack.ContentSHA256, "content_sha256"); err != nil {
		return ResearchEvidencePack{}, err
	}
	if strings.TrimSpace(pack.Path) == "" || pack.DecisionAt.IsZero() || pack.CreatedAt.IsZero() {
		return ResearchEvidencePack{}, errors.New("evidence pack path, decision_at, and created_at are required")
	}
	if err := r.pool.QueryRow(ctx, `
INSERT INTO research_evidence_packs(id, decision_at, path, content_sha256, created_at)
VALUES ($1::uuid, $2, $3, $4, $5)
RETURNING id::text`, pack.ID, pack.DecisionAt.UTC(), pack.Path, pack.ContentSHA256, pack.CreatedAt.UTC()).Scan(&pack.ID); err != nil {
		return ResearchEvidencePack{}, fmt.Errorf("register research evidence pack: %w", err)
	}
	return pack, nil
}

func (r *Repository) CreateResearchHypothesis(ctx context.Context, hypothesis ResearchHypothesis) (ResearchHypothesis, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchHypothesis{}, err
	}
	var err error
	hypothesis.ID, err = requireResearchUUID(hypothesis.ID, "id", true)
	if err != nil {
		return ResearchHypothesis{}, err
	}
	hypothesis.Title = strings.TrimSpace(hypothesis.Title)
	if hypothesis.Title == "" {
		return ResearchHypothesis{}, errors.New("hypothesis title is required")
	}
	if hypothesis.Status == "" {
		hypothesis.Status = "active"
	}
	if err := r.pool.QueryRow(ctx, `
INSERT INTO research_hypotheses(id, title, status)
VALUES (COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()), $2, $3)
RETURNING id::text, created_at`, hypothesis.ID, hypothesis.Title, hypothesis.Status).Scan(&hypothesis.ID, &hypothesis.CreatedAt); err != nil {
		return ResearchHypothesis{}, fmt.Errorf("create research hypothesis: %w", err)
	}
	return hypothesis, nil
}

func (r *Repository) AppendResearchHypothesisRevision(ctx context.Context, revision ResearchHypothesisRevision) (ResearchHypothesisRevision, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchHypothesisRevision{}, err
	}
	var err error
	revision.HypothesisID, err = requireResearchUUID(revision.HypothesisID, "hypothesis_id", false)
	if err != nil {
		return ResearchHypothesisRevision{}, err
	}
	revision.EvidencePackID, err = requireResearchUUID(revision.EvidencePackID, "evidence_pack_id", false)
	if err != nil {
		return ResearchHypothesisRevision{}, err
	}
	if err := requireResearchHash(revision.EvidencePackSHA256, "evidence_pack_sha256"); err != nil {
		return ResearchHypothesisRevision{}, err
	}
	for label, value := range map[string]string{"thesis": revision.Thesis, "horizon": revision.Horizon, "benchmark": revision.Benchmark} {
		if strings.TrimSpace(value) == "" {
			return ResearchHypothesisRevision{}, fmt.Errorf("hypothesis revision %s is required", label)
		}
	}
	causalModel, err := requireResearchJSON(revision.CausalModel, "causal_model", "object", true)
	if err != nil {
		return ResearchHypothesisRevision{}, err
	}
	universe, err := requireResearchJSON(revision.Universe, "universe", "array", true)
	if err != nil {
		return ResearchHypothesisRevision{}, err
	}
	invalidation, err := requireResearchJSON(revision.Invalidation, "invalidation_conditions", "array", true)
	if err != nil {
		return ResearchHypothesisRevision{}, err
	}
	if revision.DecisionAt.IsZero() {
		return ResearchHypothesisRevision{}, errors.New("hypothesis decision_at is required")
	}
	if revision.CreatedAt.IsZero() {
		revision.CreatedAt = time.Now().UTC()
	}
	if revision.Revision == 0 {
		tx, txErr := r.pool.Begin(ctx)
		if txErr != nil {
			return ResearchHypothesisRevision{}, txErr
		}
		defer tx.Rollback(ctx)
		revision.Revision, err = nextResearchRevision(ctx, tx, "research_hypothesis_revisions", "hypothesis_id = $1", revision.HypothesisID)
		if err != nil {
			return ResearchHypothesisRevision{}, err
		}
		if err := insertResearchHypothesisRevision(ctx, tx, &revision, causalModel, universe, invalidation); err != nil {
			return ResearchHypothesisRevision{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ResearchHypothesisRevision{}, err
		}
		return revision, nil
	}
	if revision.Revision < 1 {
		return ResearchHypothesisRevision{}, errors.New("hypothesis revision must be positive")
	}
	if err := insertResearchHypothesisRevision(ctx, r.pool, &revision, causalModel, universe, invalidation); err != nil {
		return ResearchHypothesisRevision{}, err
	}
	return revision, nil
}

func insertResearchHypothesisRevision(ctx context.Context, exec interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, revision *ResearchHypothesisRevision, causalModel, universe, invalidation []byte) error {
	if revision.RecordHash == "" {
		var err error
		revision.RecordHash, err = researchRecordHash(struct {
			HypothesisID, Thesis, Horizon, Benchmark, EvidencePackID, EvidencePackSHA256 string
			Revision                                                                     int
			CausalModel, Universe, Invalidation                                          json.RawMessage
			DecisionAt, CreatedAt                                                        time.Time
		}{revision.HypothesisID, revision.Thesis, revision.Horizon, revision.Benchmark, revision.EvidencePackID, revision.EvidencePackSHA256, revision.Revision, revision.CausalModel, revision.Universe, revision.Invalidation, revision.DecisionAt.UTC(), revision.CreatedAt.UTC()})
		if err != nil {
			return err
		}
	}
	if err := requireResearchHash(revision.RecordHash, "record_hash"); err != nil {
		return err
	}
	_, err := exec.Exec(ctx, `
INSERT INTO research_hypothesis_revisions(hypothesis_id, revision, thesis, causal_model, horizon, benchmark, universe, invalidation_conditions, decision_at, evidence_pack_id, evidence_pack_sha256, created_at, record_hash)
VALUES ($1::uuid, $2, $3, $4::jsonb, $5, $6, $7::jsonb, $8::jsonb, $9, $10::uuid, $11, $12, $13)`,
		revision.HypothesisID, revision.Revision, revision.Thesis, causalModel, revision.Horizon, revision.Benchmark,
		universe, invalidation, revision.DecisionAt.UTC(), revision.EvidencePackID, revision.EvidencePackSHA256, revision.CreatedAt.UTC(), revision.RecordHash)
	return err
}

func (r *Repository) AddResearchHypothesisEvidence(ctx context.Context, evidence ResearchHypothesisEvidence) (ResearchHypothesisEvidence, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchHypothesisEvidence{}, err
	}
	var err error
	evidence.HypothesisID, err = requireResearchUUID(evidence.HypothesisID, "hypothesis_id", false)
	if err != nil {
		return ResearchHypothesisEvidence{}, err
	}
	evidenceJSON, err := requireResearchJSON(evidence.EvidenceRef, "evidence_ref", "object", true)
	if err != nil {
		return ResearchHypothesisEvidence{}, err
	}
	if evidence.HypothesisRevision < 1 || evidence.AvailableAt.IsZero() || strings.TrimSpace(evidence.Note) == "" {
		return ResearchHypothesisEvidence{}, errors.New("hypothesis evidence revision, available_at, and note are required")
	}
	if evidence.CreatedAt.IsZero() {
		evidence.CreatedAt = time.Now().UTC()
	}
	if evidence.EvidenceID == "" {
		evidence.EvidenceID = uuid.NewString()
	}
	if _, err := requireResearchUUID(evidence.EvidenceID, "evidence_id", false); err != nil {
		return ResearchHypothesisEvidence{}, err
	}
	if evidence.RecordHash == "" {
		evidence.RecordHash, err = researchRecordHash(struct {
			EvidenceID, HypothesisID, Direction, Note string
			HypothesisRevision                        int
			EvidenceRef                               json.RawMessage
			Weight                                    float64
			AvailableAt, CreatedAt                    time.Time
		}{evidence.EvidenceID, evidence.HypothesisID, evidence.Direction, evidence.Note, evidence.HypothesisRevision, evidence.EvidenceRef, evidence.Weight, evidence.AvailableAt.UTC(), evidence.CreatedAt.UTC()})
		if err != nil {
			return ResearchHypothesisEvidence{}, err
		}
	}
	if err := requireResearchHash(evidence.RecordHash, "record_hash"); err != nil {
		return ResearchHypothesisEvidence{}, err
	}
	if err := r.pool.QueryRow(ctx, `
INSERT INTO research_hypothesis_evidence(evidence_id, hypothesis_id, hypothesis_revision, evidence_ref, direction, weight, note, available_at, created_at, record_hash)
VALUES ($1::uuid, $2::uuid, $3, $4::jsonb, $5, $6, $7, $8, $9, $10)
RETURNING evidence_id::text`, evidence.EvidenceID, evidence.HypothesisID, evidence.HypothesisRevision, evidenceJSON, evidence.Direction, evidence.Weight, evidence.Note, evidence.AvailableAt.UTC(), evidence.CreatedAt.UTC(), evidence.RecordHash).Scan(&evidence.EvidenceID); err != nil {
		return ResearchHypothesisEvidence{}, fmt.Errorf("add hypothesis evidence: %w", err)
	}
	return evidence, nil
}

func (r *Repository) CreateResearchPrediction(ctx context.Context, prediction ResearchPrediction) (ResearchPrediction, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchPrediction{}, err
	}
	var err error
	prediction.ID, err = requireResearchUUID(prediction.ID, "id", true)
	if err != nil {
		return ResearchPrediction{}, err
	}
	prediction.HypothesisID, err = requireResearchUUID(prediction.HypothesisID, "hypothesis_id", false)
	if err != nil {
		return ResearchPrediction{}, err
	}
	assetOrUniverse, err := requireResearchJSON(prediction.AssetOrUniverse, "asset_or_universe", "array", true)
	if err != nil {
		return ResearchPrediction{}, err
	}
	expectedRange, err := requireResearchJSON(prediction.ExpectedRange, "expected_range", "object", true)
	if err != nil {
		return ResearchPrediction{}, err
	}
	if prediction.HypothesisRevision < 1 || strings.TrimSpace(prediction.ExpectedDirection) == "" || strings.TrimSpace(prediction.Horizon) == "" {
		return ResearchPrediction{}, errors.New("prediction hypothesis revision, direction, and horizon are required")
	}
	if prediction.CreatedAt.IsZero() {
		prediction.CreatedAt = time.Now().UTC()
	}
	prediction.Status = "draft"
	if prediction.RecordHash == "" {
		prediction.RecordHash, err = researchRecordHash(struct {
			HypothesisID, ExpectedDirection, Horizon string
			HypothesisRevision                       int
			AssetOrUniverse, ExpectedRange           json.RawMessage
			Confidence                               float64
			CreatedAt                                time.Time
		}{prediction.HypothesisID, prediction.ExpectedDirection, prediction.Horizon, prediction.HypothesisRevision, prediction.AssetOrUniverse, prediction.ExpectedRange, prediction.Confidence, prediction.CreatedAt.UTC()})
		if err != nil {
			return ResearchPrediction{}, err
		}
	}
	if err := requireResearchHash(prediction.RecordHash, "record_hash"); err != nil {
		return ResearchPrediction{}, err
	}
	if err := r.pool.QueryRow(ctx, `
INSERT INTO research_predictions(id, hypothesis_id, hypothesis_revision, asset_or_universe, expected_direction, expected_range, horizon, confidence, created_at, status, record_hash)
VALUES (COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()), $2::uuid, $3, $4::jsonb, $5, $6::jsonb, $7, $8, $9, 'draft', $10)
RETURNING id::text`, prediction.ID, prediction.HypothesisID, prediction.HypothesisRevision, assetOrUniverse, prediction.ExpectedDirection, expectedRange, prediction.Horizon, prediction.Confidence, prediction.CreatedAt.UTC(), prediction.RecordHash).Scan(&prediction.ID); err != nil {
		return ResearchPrediction{}, fmt.Errorf("create research prediction: %w", err)
	}
	return prediction, nil
}

func (r *Repository) GetResearchPrediction(ctx context.Context, predictionID string) (ResearchPrediction, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchPrediction{}, err
	}
	predictionID, err := requireResearchUUID(predictionID, "prediction_id", false)
	if err != nil {
		return ResearchPrediction{}, err
	}
	var prediction ResearchPrediction
	var frozenAt *time.Time
	var frozenRevision *int
	if err := r.pool.QueryRow(ctx, `
SELECT id::text, hypothesis_id::text, hypothesis_revision, asset_or_universe, expected_direction, expected_range, horizon, confidence, created_at, frozen_at, frozen_revision, status, record_hash
FROM research_predictions WHERE id = $1::uuid`, predictionID).Scan(
		&prediction.ID, &prediction.HypothesisID, &prediction.HypothesisRevision, &prediction.AssetOrUniverse,
		&prediction.ExpectedDirection, &prediction.ExpectedRange, &prediction.Horizon, &prediction.Confidence,
		&prediction.CreatedAt, &frozenAt, &frozenRevision, &prediction.Status, &prediction.RecordHash); err != nil {
		return ResearchPrediction{}, fmt.Errorf("get research prediction: %w", err)
	}
	prediction.FrozenAt, prediction.FrozenRevision = frozenAt, frozenRevision
	return prediction, nil
}

func (r *Repository) FreezeResearchPrediction(ctx context.Context, predictionID string, frozenAt time.Time) (ResearchPrediction, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchPrediction{}, err
	}
	if frozenAt.IsZero() {
		frozenAt = time.Now().UTC()
	}
	prediction, err := r.GetResearchPrediction(ctx, predictionID)
	if err != nil {
		return ResearchPrediction{}, err
	}
	if prediction.FrozenAt != nil {
		return ResearchPrediction{}, errors.New("prediction is already frozen and cannot be rewritten")
	}
	revision := prediction.HypothesisRevision
	prediction.FrozenAt = &frozenAt
	prediction.FrozenRevision = &revision
	prediction.Status = "frozen"
	prediction.RecordHash, err = researchRecordHash(struct {
		ID, HypothesisID, ExpectedDirection, Horizon, Status string
		HypothesisRevision                                   int
		AssetOrUniverse, ExpectedRange                       json.RawMessage
		Confidence                                           float64
		CreatedAt, FrozenAt                                  time.Time
		FrozenRevision                                       int
	}{prediction.ID, prediction.HypothesisID, prediction.ExpectedDirection, prediction.Horizon, prediction.Status, prediction.HypothesisRevision, prediction.AssetOrUniverse, prediction.ExpectedRange, prediction.Confidence, prediction.CreatedAt.UTC(), frozenAt.UTC(), revision})
	if err != nil {
		return ResearchPrediction{}, err
	}
	if _, err := r.pool.Exec(ctx, `UPDATE research_predictions SET frozen_at = $2, frozen_revision = $3, status = 'frozen', record_hash = $4 WHERE id = $1::uuid AND frozen_at IS NULL`, prediction.ID, frozenAt.UTC(), revision, prediction.RecordHash); err != nil {
		return ResearchPrediction{}, fmt.Errorf("freeze research prediction: %w", err)
	}
	return prediction, nil
}

func (r *Repository) RecordResearchPredictionOutcome(ctx context.Context, outcome ResearchPredictionOutcome) (ResearchPredictionOutcome, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchPredictionOutcome{}, err
	}
	var err error
	outcome.OutcomeID, err = requireResearchUUID(outcome.OutcomeID, "outcome_id", true)
	if err != nil {
		return ResearchPredictionOutcome{}, err
	}
	outcome.PredictionID, err = requireResearchUUID(outcome.PredictionID, "prediction_id", false)
	if err != nil {
		return ResearchPredictionOutcome{}, err
	}
	if strings.TrimSpace(outcome.MeasurementPolicy) == "" || outcome.MeasuredAt.IsZero() {
		return ResearchPredictionOutcome{}, errors.New("outcome measurement policy and measured_at are required")
	}
	refs, err := requireResearchJSON(outcome.InputArtifactRefs, "input_artifact_refs", "array", true)
	if err != nil {
		return ResearchPredictionOutcome{}, err
	}
	if outcome.Status == "measured" {
		if _, err := requireResearchJSON(outcome.RealizedResult, "realized_result", "object", true); err != nil {
			return ResearchPredictionOutcome{}, err
		}
		if _, err := requireResearchJSON(outcome.BenchmarkResult, "benchmark_result", "object", true); err != nil {
			return ResearchPredictionOutcome{}, err
		}
	} else {
		outcome.RealizedResult, outcome.BenchmarkResult, outcome.Drawdown = nil, nil, nil
	}
	if outcome.CreatedAt.IsZero() {
		outcome.CreatedAt = time.Now().UTC()
	}
	if outcome.OutcomeID == "" {
		outcome.OutcomeID = uuid.NewString()
	}
	if outcome.RecordHash == "" {
		outcome.RecordHash, err = researchRecordHash(struct {
			OutcomeID, PredictionID, MeasurementPolicy, Status string
			RealizedResult, BenchmarkResult, InputArtifactRefs json.RawMessage
			Drawdown                                           *float64
			MeasuredAt, CreatedAt                              time.Time
		}{outcome.OutcomeID, outcome.PredictionID, outcome.MeasurementPolicy, outcome.Status, outcome.RealizedResult, outcome.BenchmarkResult, outcome.InputArtifactRefs, outcome.Drawdown, outcome.MeasuredAt.UTC(), outcome.CreatedAt.UTC()})
		if err != nil {
			return ResearchPredictionOutcome{}, err
		}
	}
	if err := requireResearchHash(outcome.RecordHash, "record_hash"); err != nil {
		return ResearchPredictionOutcome{}, err
	}
	if err := r.pool.QueryRow(ctx, `
INSERT INTO research_prediction_outcomes(outcome_id, prediction_id, measurement_policy_version, status, realized_result, benchmark_result, drawdown, measured_at, input_artifact_refs, created_at, record_hash)
VALUES ($1::uuid, $2::uuid, $3, $4, $5::jsonb, $6::jsonb, $7, $8, $9::jsonb, $10, $11)
RETURNING outcome_id::text`, outcome.OutcomeID, outcome.PredictionID, outcome.MeasurementPolicy, outcome.Status, nullableJSON(outcome.RealizedResult), nullableJSON(outcome.BenchmarkResult), outcome.Drawdown, outcome.MeasuredAt.UTC(), refs, outcome.CreatedAt.UTC(), outcome.RecordHash).Scan(&outcome.OutcomeID); err != nil {
		return ResearchPredictionOutcome{}, fmt.Errorf("record research prediction outcome: %w", err)
	}
	return outcome, nil
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func (r *Repository) CloseResearchHypothesis(ctx context.Context, hypothesisID string, invalidated bool) error {
	if err := requireResearchRepository(r); err != nil {
		return err
	}
	hypothesisID, err := requireResearchUUID(hypothesisID, "hypothesis_id", false)
	if err != nil {
		return err
	}
	status := "closed"
	if invalidated {
		status = "invalidated"
	}
	command, err := r.pool.Exec(ctx, `UPDATE research_hypotheses SET status = $2 WHERE id = $1::uuid AND status <> 'closed'`, hypothesisID, status)
	if err != nil {
		return fmt.Errorf("close research hypothesis: %w", err)
	}
	if command.RowsAffected() != 1 {
		return errors.New("research hypothesis was not open")
	}
	return nil
}
