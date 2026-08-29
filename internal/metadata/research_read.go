package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func decodeResearchEvidenceRefs(value []byte) ([]ResearchEvidenceRef, error) {
	refs := []ResearchEvidenceRef{}
	if len(value) == 0 {
		return refs, nil
	}
	if err := json.Unmarshal(value, &refs); err != nil {
		return nil, fmt.Errorf("decode research evidence references: %w", err)
	}
	return refs, nil
}

func copyResearchJSON(value []byte) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

func (r *Repository) GetResearchThemeSnapshot(ctx context.Context, themeID string, decisionAt time.Time) (ResearchThemeSnapshot, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchThemeSnapshot{}, err
	}
	var err error
	themeID, err = requireResearchUUID(themeID, "theme_id", false)
	if err != nil {
		return ResearchThemeSnapshot{}, err
	}
	if decisionAt.IsZero() {
		return ResearchThemeSnapshot{}, errors.New("theme snapshot decision_at is required")
	}
	snapshot := ResearchThemeSnapshot{
		SchemaVersion: ResearchSchemaVersion,
		ThemeID:       themeID,
		DecisionAt:    decisionAt.UTC(),
		Themes:        []ResearchThemeBundleTheme{},
		Entities:      []ResearchEntity{},
		Memberships:   []ResearchThemeMembership{},
		Relationships: []ResearchThemeBundleRelationship{},
		Indicators:    []ResearchThemeIndicator{},
		FeatureRefs:   []ResearchThemeFeatureRef{},
		Conditions:    []ResearchThemeCondition{},
	}

	themeRows, err := r.pool.Query(ctx, `
WITH RECURSIVE theme_tree(id) AS (
    SELECT id FROM research_themes WHERE id = $1::uuid
    UNION
    SELECT child.theme_id
    FROM research_theme_revisions child
    JOIN theme_tree parent ON parent.id = child.parent_theme_id
    WHERE child.recorded_at <= $2::timestamptz
), latest AS (
    SELECT DISTINCT ON (revision.theme_id) revision.*
    FROM research_theme_revisions revision
    JOIN theme_tree tree ON tree.id = revision.theme_id
    WHERE revision.recorded_at <= $2::timestamptz
    ORDER BY revision.theme_id, revision.revision DESC
)
SELECT theme.id::text, theme.stable_key,
       revision.theme_id::text, revision.revision, COALESCE(revision.parent_theme_id::text, ''),
       revision.name, revision.knowledge_kind, revision.review_state, revision.description,
       revision.evidence_refs, revision.author_method, revision.recorded_at, revision.record_hash
FROM latest revision
JOIN research_themes theme ON theme.id = revision.theme_id
WHERE revision.review_state = 'reviewed'
ORDER BY theme.id`, themeID, decisionAt.UTC())
	if err != nil {
		return ResearchThemeSnapshot{}, fmt.Errorf("query research theme snapshot themes: %w", err)
	}
	for themeRows.Next() {
		var theme ResearchTheme
		var revision ResearchThemeRevision
		var evidenceRefs []byte
		if err := themeRows.Scan(
			&theme.ID, &theme.StableKey,
			&revision.ThemeID, &revision.Revision, &revision.ParentThemeID,
			&revision.Name, &revision.KnowledgeKind, &revision.ReviewState, &revision.Description,
			&evidenceRefs, &revision.AuthorMethod, &revision.RecordedAt, &revision.RecordHash,
		); err != nil {
			themeRows.Close()
			return ResearchThemeSnapshot{}, fmt.Errorf("scan research theme snapshot theme: %w", err)
		}
		revision.EvidenceRefs, err = decodeResearchEvidenceRefs(evidenceRefs)
		if err != nil {
			themeRows.Close()
			return ResearchThemeSnapshot{}, err
		}
		snapshot.Themes = append(snapshot.Themes, ResearchThemeBundleTheme{Theme: theme, Revision: revision})
	}
	if err := themeRows.Err(); err != nil {
		themeRows.Close()
		return ResearchThemeSnapshot{}, fmt.Errorf("iterate research theme snapshot themes: %w", err)
	}
	themeRows.Close()
	if len(snapshot.Themes) == 0 {
		return ResearchThemeSnapshot{}, errors.New("no reviewed theme revision exists at decision_at")
	}

	entityRows, err := r.pool.Query(ctx, `
WITH RECURSIVE theme_tree(id) AS (
    SELECT id FROM research_themes WHERE id = $1::uuid
    UNION
    SELECT child.theme_id
    FROM research_theme_revisions child
    JOIN theme_tree parent ON parent.id = child.parent_theme_id
    WHERE child.recorded_at <= $2::timestamptz
), latest_membership AS (
    SELECT DISTINCT ON (membership.theme_id, membership.entity_id) membership.*
    FROM research_theme_memberships membership
    JOIN theme_tree tree ON tree.id = membership.theme_id
    WHERE membership.recorded_at <= $2::timestamptz
      AND membership.valid_from <= $2::timestamptz
      AND (membership.valid_until IS NULL OR $2::timestamptz < membership.valid_until)
    ORDER BY membership.theme_id, membership.entity_id, membership.revision DESC
)
SELECT entity.id::text, entity.entity_type, entity.stable_key, entity.display_name,
       COALESCE(entity.issuer_id::text, ''), COALESCE(entity.security_id::text, ''), entity.metadata,
       membership.theme_id::text, membership.entity_id::text, membership.revision,
       membership.role, membership.knowledge_kind, membership.confidence::double precision,
       membership.evidence_refs, membership.author_method, membership.valid_from,
       membership.valid_until, membership.recorded_at, membership.revision_state, membership.record_hash
FROM latest_membership membership
JOIN research_entities entity ON entity.id = membership.entity_id
WHERE membership.revision_state = 'reviewed'
ORDER BY membership.theme_id, membership.entity_id`, themeID, decisionAt.UTC())
	if err != nil {
		return ResearchThemeSnapshot{}, fmt.Errorf("query research theme snapshot memberships: %w", err)
	}
	for entityRows.Next() {
		var entity ResearchEntity
		var membership ResearchThemeMembership
		var metadataJSON, evidenceRefs []byte
		if err := entityRows.Scan(
			&entity.ID, &entity.EntityType, &entity.StableKey, &entity.DisplayName,
			&entity.IssuerID, &entity.SecurityID, &metadataJSON,
			&membership.ThemeID, &membership.EntityID, &membership.Revision,
			&membership.Role, &membership.KnowledgeKind, &membership.Confidence,
			&evidenceRefs, &membership.AuthorMethod, &membership.ValidFrom,
			&membership.ValidUntil, &membership.RecordedAt, &membership.RevisionState, &membership.RecordHash,
		); err != nil {
			entityRows.Close()
			return ResearchThemeSnapshot{}, fmt.Errorf("scan research theme snapshot membership: %w", err)
		}
		entity.Metadata = copyResearchJSON(metadataJSON)
		membership.EvidenceRefs, err = decodeResearchEvidenceRefs(evidenceRefs)
		if err != nil {
			entityRows.Close()
			return ResearchThemeSnapshot{}, err
		}
		snapshot.Entities = append(snapshot.Entities, entity)
		snapshot.Memberships = append(snapshot.Memberships, membership)
	}
	if err := entityRows.Err(); err != nil {
		entityRows.Close()
		return ResearchThemeSnapshot{}, fmt.Errorf("iterate research theme snapshot memberships: %w", err)
	}
	entityRows.Close()

	relationshipRows, err := r.pool.Query(ctx, `
WITH RECURSIVE theme_tree(id) AS (
    SELECT id FROM research_themes WHERE id = $1::uuid
    UNION
    SELECT child.theme_id
    FROM research_theme_revisions child
    JOIN theme_tree parent ON parent.id = child.parent_theme_id
    WHERE child.recorded_at <= $2::timestamptz
), latest_membership AS (
    SELECT DISTINCT ON (membership.theme_id, membership.entity_id) membership.*
    FROM research_theme_memberships membership
    JOIN theme_tree tree ON tree.id = membership.theme_id
    WHERE membership.recorded_at <= $2::timestamptz
      AND membership.valid_from <= $2::timestamptz
      AND (membership.valid_until IS NULL OR $2::timestamptz < membership.valid_until)
    ORDER BY membership.theme_id, membership.entity_id, membership.revision DESC
), active_entities AS (
    SELECT DISTINCT entity_id
    FROM latest_membership
    WHERE revision_state = 'reviewed'
), latest_relationship AS (
    SELECT DISTINCT ON (revision.relationship_id) revision.*
    FROM research_relationship_revisions revision
    JOIN research_relationships relationship ON relationship.id = revision.relationship_id
    JOIN active_entities from_entity ON from_entity.entity_id = revision.from_entity_id
    JOIN active_entities to_entity ON to_entity.entity_id = revision.to_entity_id
    WHERE revision.recorded_at <= $2::timestamptz
      AND revision.valid_from <= $2::timestamptz
      AND (revision.valid_until IS NULL OR $2::timestamptz < revision.valid_until)
    ORDER BY revision.relationship_id, revision.revision DESC
)
SELECT revision.relationship_id::text, revision.revision,
       revision.from_entity_id::text, revision.to_entity_id::text, revision.relationship_type,
       revision.direction, revision.knowledge_kind, revision.confidence::double precision,
       revision.evidence_refs, revision.author_method, revision.valid_from,
       revision.valid_until, revision.recorded_at, revision.revision_state, revision.record_hash
FROM latest_relationship revision
JOIN research_relationships relationship ON relationship.id = revision.relationship_id
WHERE revision.revision_state = 'reviewed'
ORDER BY revision.relationship_id`, themeID, decisionAt.UTC())
	if err != nil {
		return ResearchThemeSnapshot{}, fmt.Errorf("query research theme snapshot relationships: %w", err)
	}
	for relationshipRows.Next() {
		var relationship ResearchRelationship
		var revision ResearchRelationshipRevision
		var evidenceRefs []byte
		if err := relationshipRows.Scan(
			&relationship.ID, &revision.Revision,
			&revision.FromEntityID, &revision.ToEntityID,
			&revision.RelationshipType, &revision.Direction, &revision.KnowledgeKind, &revision.Confidence,
			&evidenceRefs, &revision.AuthorMethod, &revision.ValidFrom, &revision.ValidUntil,
			&revision.RecordedAt, &revision.RevisionState, &revision.RecordHash,
		); err != nil {
			relationshipRows.Close()
			return ResearchThemeSnapshot{}, fmt.Errorf("scan research theme snapshot relationship: %w", err)
		}
		revision.RelationshipID = relationship.ID
		revision.EvidenceRefs, err = decodeResearchEvidenceRefs(evidenceRefs)
		if err != nil {
			relationshipRows.Close()
			return ResearchThemeSnapshot{}, err
		}
		snapshot.Relationships = append(snapshot.Relationships, ResearchThemeBundleRelationship{Relationship: relationship, Revision: revision})
	}
	if err := relationshipRows.Err(); err != nil {
		relationshipRows.Close()
		return ResearchThemeSnapshot{}, fmt.Errorf("iterate research theme snapshot relationships: %w", err)
	}
	relationshipRows.Close()

	indicatorRows, err := r.pool.Query(ctx, `
WITH RECURSIVE theme_tree(id) AS (
    SELECT id FROM research_themes WHERE id = $1::uuid
    UNION
    SELECT child.theme_id
    FROM research_theme_revisions child
    JOIN theme_tree parent ON parent.id = child.parent_theme_id
    WHERE child.recorded_at <= $2::timestamptz
)
SELECT indicator.indicator_id::text, indicator.theme_id::text, indicator.indicator_key,
       indicator.display_name, indicator.source_ref, indicator.evidence_refs,
       indicator.recorded_at, indicator.record_hash
FROM research_theme_indicators indicator
JOIN theme_tree tree ON tree.id = indicator.theme_id
WHERE indicator.recorded_at <= $2::timestamptz
ORDER BY indicator.theme_id, indicator.indicator_key`, themeID, decisionAt.UTC())
	if err != nil {
		return ResearchThemeSnapshot{}, fmt.Errorf("query research theme snapshot indicators: %w", err)
	}
	for indicatorRows.Next() {
		var indicator ResearchThemeIndicator
		var evidenceRefs []byte
		if err := indicatorRows.Scan(
			&indicator.IndicatorID, &indicator.ThemeID, &indicator.IndicatorKey,
			&indicator.DisplayName, &indicator.SourceRef, &evidenceRefs,
			&indicator.RecordedAt, &indicator.RecordHash,
		); err != nil {
			indicatorRows.Close()
			return ResearchThemeSnapshot{}, fmt.Errorf("scan research theme snapshot indicator: %w", err)
		}
		indicator.EvidenceRefs, err = decodeResearchEvidenceRefs(evidenceRefs)
		if err != nil {
			indicatorRows.Close()
			return ResearchThemeSnapshot{}, err
		}
		snapshot.Indicators = append(snapshot.Indicators, indicator)
	}
	if err := indicatorRows.Err(); err != nil {
		indicatorRows.Close()
		return ResearchThemeSnapshot{}, fmt.Errorf("iterate research theme snapshot indicators: %w", err)
	}
	indicatorRows.Close()

	featureRows, err := r.pool.Query(ctx, `
WITH RECURSIVE theme_tree(id) AS (
    SELECT id FROM research_themes WHERE id = $1::uuid
    UNION
    SELECT child.theme_id
    FROM research_theme_revisions child
    JOIN theme_tree parent ON parent.id = child.parent_theme_id
    WHERE child.recorded_at <= $2::timestamptz
)
SELECT feature.feature_ref_id::text, feature.theme_id::text, feature.feature_set,
       feature.feature_set_version, feature.artifact_ref, feature.evidence_refs,
       feature.recorded_at, feature.record_hash
FROM research_theme_feature_refs feature
JOIN theme_tree tree ON tree.id = feature.theme_id
WHERE feature.recorded_at <= $2::timestamptz
ORDER BY feature.theme_id, feature.feature_set, feature.feature_set_version`, themeID, decisionAt.UTC())
	if err != nil {
		return ResearchThemeSnapshot{}, fmt.Errorf("query research theme snapshot feature refs: %w", err)
	}
	for featureRows.Next() {
		var feature ResearchThemeFeatureRef
		var artifactRef, evidenceRefs []byte
		if err := featureRows.Scan(
			&feature.FeatureRefID, &feature.ThemeID, &feature.FeatureSet, &feature.FeatureSetVersion,
			&artifactRef, &evidenceRefs, &feature.RecordedAt, &feature.RecordHash,
		); err != nil {
			featureRows.Close()
			return ResearchThemeSnapshot{}, fmt.Errorf("scan research theme snapshot feature ref: %w", err)
		}
		feature.ArtifactRef = copyResearchJSON(artifactRef)
		feature.EvidenceRefs, err = decodeResearchEvidenceRefs(evidenceRefs)
		if err != nil {
			featureRows.Close()
			return ResearchThemeSnapshot{}, err
		}
		snapshot.FeatureRefs = append(snapshot.FeatureRefs, feature)
	}
	if err := featureRows.Err(); err != nil {
		featureRows.Close()
		return ResearchThemeSnapshot{}, fmt.Errorf("iterate research theme snapshot feature refs: %w", err)
	}
	featureRows.Close()

	conditionRows, err := r.pool.Query(ctx, `
WITH RECURSIVE theme_tree(id) AS (
    SELECT id FROM research_themes WHERE id = $1::uuid
    UNION
    SELECT child.theme_id
    FROM research_theme_revisions child
    JOIN theme_tree parent ON parent.id = child.parent_theme_id
    WHERE child.recorded_at <= $2::timestamptz
)
SELECT condition.condition_id::text, condition.theme_id::text, condition.condition_type,
       condition.condition, condition.evidence_refs, condition.recorded_at, condition.record_hash
FROM research_theme_conditions condition
JOIN theme_tree tree ON tree.id = condition.theme_id
WHERE condition.recorded_at <= $2::timestamptz
ORDER BY condition.theme_id, condition.condition_id`, themeID, decisionAt.UTC())
	if err != nil {
		return ResearchThemeSnapshot{}, fmt.Errorf("query research theme snapshot conditions: %w", err)
	}
	for conditionRows.Next() {
		var condition ResearchThemeCondition
		var evidenceRefs []byte
		if err := conditionRows.Scan(
			&condition.ConditionID, &condition.ThemeID, &condition.ConditionType,
			&condition.Condition, &evidenceRefs, &condition.RecordedAt, &condition.RecordHash,
		); err != nil {
			conditionRows.Close()
			return ResearchThemeSnapshot{}, fmt.Errorf("scan research theme snapshot condition: %w", err)
		}
		condition.EvidenceRefs, err = decodeResearchEvidenceRefs(evidenceRefs)
		if err != nil {
			conditionRows.Close()
			return ResearchThemeSnapshot{}, err
		}
		snapshot.Conditions = append(snapshot.Conditions, condition)
	}
	if err := conditionRows.Err(); err != nil {
		conditionRows.Close()
		return ResearchThemeSnapshot{}, fmt.Errorf("iterate research theme snapshot conditions: %w", err)
	}
	conditionRows.Close()

	return snapshot, nil
}

func (r *Repository) GetResearchStatusReport(ctx context.Context, asOf time.Time) (ResearchStatusReport, error) {
	if err := requireResearchRepository(r); err != nil {
		return ResearchStatusReport{}, err
	}
	if asOf.IsZero() {
		return ResearchStatusReport{}, errors.New("research status report as_of is required")
	}
	report := ResearchStatusReport{
		SchemaVersion:    ResearchSchemaVersion,
		AsOf:             asOf.UTC(),
		ActiveHypotheses: []ResearchHypothesisStatus{},
		UpcomingReviews:  []ResearchUpcomingReview{},
		Predictions:      []ResearchPredictionStatus{},
	}
	hypothesisRows, err := r.pool.Query(ctx, `
WITH latest AS (
    SELECT DISTINCT ON (revision.hypothesis_id) revision.*
    FROM research_hypothesis_revisions revision
    WHERE revision.created_at <= $1::timestamptz
    ORDER BY revision.hypothesis_id, revision.revision DESC
)
SELECT hypothesis.id::text, hypothesis.title, hypothesis.status,
       latest.revision, latest.thesis, latest.horizon, latest.benchmark,
       latest.decision_at, latest.review_at, latest.evidence_pack_id::text,
       latest.evidence_pack_sha256,
       evidence.evidence_count, evidence.latest_evidence_at,
       predictions.prediction_count, predictions.measured_prediction_count
FROM research_hypotheses hypothesis
JOIN latest ON latest.hypothesis_id = hypothesis.id
LEFT JOIN LATERAL (
    SELECT count(*)::int AS evidence_count, max(available_at) AS latest_evidence_at
    FROM research_hypothesis_evidence evidence
    WHERE evidence.hypothesis_id = hypothesis.id
      AND evidence.hypothesis_revision = latest.revision
      AND evidence.available_at <= $1::timestamptz
) evidence ON true
LEFT JOIN LATERAL (
    SELECT count(*)::int AS prediction_count,
           count(*) FILTER (WHERE EXISTS (
               SELECT 1 FROM research_prediction_outcomes outcome
               WHERE outcome.prediction_id = prediction.id
                 AND outcome.status = 'measured'
                 AND outcome.measured_at <= $1::timestamptz
           ))::int AS measured_prediction_count
    FROM research_predictions prediction
    WHERE prediction.hypothesis_id = hypothesis.id
      AND prediction.hypothesis_revision = latest.revision
      AND prediction.created_at <= $1::timestamptz
) predictions ON true
WHERE hypothesis.status = 'active'
  AND hypothesis.created_at <= $1::timestamptz
ORDER BY latest.review_at, hypothesis.id`, asOf.UTC())
	if err != nil {
		return ResearchStatusReport{}, fmt.Errorf("query research status hypotheses: %w", err)
	}
	for hypothesisRows.Next() {
		var status ResearchHypothesisStatus
		if err := hypothesisRows.Scan(
			&status.HypothesisID, &status.Title, &status.Status,
			&status.Revision, &status.Thesis, &status.Horizon, &status.Benchmark,
			&status.DecisionAt, &status.ReviewAt, &status.EvidencePackID,
			&status.EvidencePackSHA256, &status.EvidenceCount, &status.LatestEvidenceAt,
			&status.PredictionCount, &status.MeasuredPredictionCount,
		); err != nil {
			hypothesisRows.Close()
			return ResearchStatusReport{}, fmt.Errorf("scan research status hypothesis: %w", err)
		}
		report.ActiveHypotheses = append(report.ActiveHypotheses, status)
		if status.ReviewAt.After(asOf) {
			report.UpcomingReviews = append(report.UpcomingReviews, ResearchUpcomingReview{
				HypothesisID: status.HypothesisID,
				Title:        status.Title,
				Revision:     status.Revision,
				ReviewAt:     status.ReviewAt,
			})
		}
	}
	if err := hypothesisRows.Err(); err != nil {
		hypothesisRows.Close()
		return ResearchStatusReport{}, fmt.Errorf("iterate research status hypotheses: %w", err)
	}
	hypothesisRows.Close()

	predictionRows, err := r.pool.Query(ctx, `
WITH active_hypotheses AS (
    SELECT id FROM research_hypotheses
    WHERE status = 'active' AND created_at <= $1::timestamptz
)
SELECT prediction.id::text, prediction.hypothesis_id::text, prediction.hypothesis_revision,
       prediction.asset_or_universe, prediction.expected_direction, prediction.expected_range,
       prediction.horizon, prediction.confidence::double precision, prediction.created_at,
       prediction.frozen_at, prediction.status,
       COALESCE(outcome.outcome_id::text, ''), COALESCE(outcome.status, ''),
       COALESCE(outcome.measurement_policy_version, ''), outcome.measured_at
FROM research_predictions prediction
JOIN active_hypotheses hypothesis ON hypothesis.id = prediction.hypothesis_id
LEFT JOIN LATERAL (
    SELECT outcome_id, status, measurement_policy_version, measured_at
    FROM research_prediction_outcomes outcome
    WHERE outcome.prediction_id = prediction.id
      AND outcome.measured_at <= $1::timestamptz
    ORDER BY outcome.measured_at DESC, outcome.created_at DESC, outcome.outcome_id DESC
    LIMIT 1
) outcome ON true
WHERE prediction.created_at <= $1::timestamptz
ORDER BY prediction.created_at, prediction.id`, asOf.UTC())
	if err != nil {
		return ResearchStatusReport{}, fmt.Errorf("query research status predictions: %w", err)
	}
	for predictionRows.Next() {
		var prediction ResearchPredictionStatus
		if err := predictionRows.Scan(
			&prediction.PredictionID, &prediction.HypothesisID, &prediction.HypothesisRevision,
			&prediction.AssetOrUniverse, &prediction.ExpectedDirection, &prediction.ExpectedRange,
			&prediction.Horizon, &prediction.Confidence, &prediction.CreatedAt,
			&prediction.FrozenAt, &prediction.Status,
			&prediction.OutcomeID, &prediction.OutcomeStatus, &prediction.MeasurementPolicy, &prediction.MeasuredAt,
		); err != nil {
			predictionRows.Close()
			return ResearchStatusReport{}, fmt.Errorf("scan research status prediction: %w", err)
		}
		prediction.AssetOrUniverse = copyResearchJSON(prediction.AssetOrUniverse)
		prediction.ExpectedRange = copyResearchJSON(prediction.ExpectedRange)
		report.Predictions = append(report.Predictions, prediction)
	}
	if err := predictionRows.Err(); err != nil {
		predictionRows.Close()
		return ResearchStatusReport{}, fmt.Errorf("iterate research status predictions: %w", err)
	}
	predictionRows.Close()

	return report, nil
}
