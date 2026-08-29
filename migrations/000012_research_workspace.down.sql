BEGIN;

DROP TRIGGER IF EXISTS research_hypothesis_identity_guard ON research_hypotheses;
DROP TRIGGER IF EXISTS research_prediction_immutable_after_freeze ON research_predictions;
DROP TRIGGER IF EXISTS research_prediction_outcome_immutable ON research_prediction_outcomes;
DROP TRIGGER IF EXISTS research_hypothesis_evidence_immutable ON research_hypothesis_evidence;
DROP TRIGGER IF EXISTS research_hypothesis_revision_immutable ON research_hypothesis_revisions;
DROP TRIGGER IF EXISTS research_evidence_pack_immutable ON research_evidence_packs;
DROP TRIGGER IF EXISTS research_event_revision_immutable ON research_event_proposal_revisions;
DROP TRIGGER IF EXISTS research_document_text_immutable ON research_document_text_artifacts;
DROP TRIGGER IF EXISTS research_document_artifact_immutable ON research_document_artifacts;
DROP TRIGGER IF EXISTS research_document_entity_immutable ON research_document_entities;
DROP TRIGGER IF EXISTS research_relationship_revision_immutable ON research_relationship_revisions;
DROP TRIGGER IF EXISTS research_membership_immutable ON research_theme_memberships;
DROP TRIGGER IF EXISTS research_theme_revision_immutable ON research_theme_revisions;
DROP TRIGGER IF EXISTS research_hypothesis_revision_order ON research_hypothesis_revisions;
DROP TRIGGER IF EXISTS research_event_revision_order ON research_event_proposal_revisions;
DROP TRIGGER IF EXISTS research_relationship_revision_order ON research_relationship_revisions;
DROP TRIGGER IF EXISTS research_membership_revision_order ON research_theme_memberships;
DROP TRIGGER IF EXISTS research_theme_revision_order ON research_theme_revisions;

DROP FUNCTION IF EXISTS research_hypothesis_guard();
DROP FUNCTION IF EXISTS research_prediction_guard();
DROP FUNCTION IF EXISTS research_revision_order_guard();
DROP FUNCTION IF EXISTS research_append_only_guard();

DROP TABLE IF EXISTS research_prediction_outcomes;
DROP TABLE IF EXISTS research_predictions;
DROP TABLE IF EXISTS research_hypothesis_evidence;
DROP TABLE IF EXISTS research_hypothesis_revisions;
DROP TABLE IF EXISTS research_hypotheses;
DROP TABLE IF EXISTS research_evidence_packs;
DROP TABLE IF EXISTS research_event_proposal_revisions;
DROP TABLE IF EXISTS research_event_proposals;
DROP TABLE IF EXISTS research_document_text_artifacts;
DROP TABLE IF EXISTS research_document_artifacts;
DROP TABLE IF EXISTS research_document_entities;
DROP TABLE IF EXISTS research_documents;
DROP TABLE IF EXISTS research_relationship_revisions;
DROP TABLE IF EXISTS research_relationships;
DROP TABLE IF EXISTS research_theme_memberships;
DROP TABLE IF EXISTS research_theme_revisions;
DROP TABLE IF EXISTS research_themes;
DROP TABLE IF EXISTS research_entities;

COMMIT;
