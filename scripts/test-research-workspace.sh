#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

compose=(docker compose --progress quiet)
acceptance_root="$repo_root/data/research/acceptance/v0.4"
acceptance_db="invs_v04_acceptance_$$"

collector_database_url=$(${compose[@]} --profile collect config --format json | jq -r '.services.collector.environment.DATABASE_URL')
database_base=${collector_database_url%%\?*}
database_query=""
if [[ "$collector_database_url" == *\?* ]]; then
	database_query="?${collector_database_url#*\?}"
fi
acceptance_database_url="${database_base%/*}/${acceptance_db}${database_query}"
psql_acceptance() {
	${compose[@]} exec -T postgres sh -c 'psql -v ON_ERROR_STOP=1 -X -U "$POSTGRES_USER" -d "$1" -At' sh "$acceptance_db"
}

psql_value() {
	${compose[@]} exec -T postgres sh -c 'psql -v ON_ERROR_STOP=1 -X -U "$POSTGRES_USER" -d "$1" -Atc "$2"' sh "$acceptance_db" "$1"
}

research_cli_file() {
	local operation=$1
	local input_path=$2
	${compose[@]} --profile collect run --rm -T \
		-e "DATABASE_URL=$acceptance_database_url" \
		--entrypoint invs-research collector --operation "$operation" < "$input_path"
}

research_cli_json() {
	local operation=$1
	local input=$2
	printf '%s' "$input" | ${compose[@]} --profile collect run --rm -T \
		-e "DATABASE_URL=$acceptance_database_url" \
		--entrypoint invs-research collector --operation "$operation"
}

cleanup() {
	${compose[@]} exec -T postgres sh -c 'dropdb -U "$POSTGRES_USER" "$1"' sh "$acceptance_db" >/dev/null 2>&1 || true
}

mkdir -p "$acceptance_root"
${compose[@]} exec -T postgres sh -c 'createdb -U "$POSTGRES_USER" "$1"' sh "$acceptance_db"
trap cleanup EXIT

for migration in "$repo_root"/migrations/*.up.sql; do
	${compose[@]} exec -T postgres sh -c 'psql -v ON_ERROR_STOP=1 -X -U "$POSTGRES_USER" -d "$1"' sh "$acceptance_db" < "$migration" >/dev/null
done

${compose[@]} --profile collect build collector >/dev/null

research_cli_file seed-theme "$repo_root/fixtures/research/ai-infrastructure-theme.json" >/dev/null
research_cli_json theme-snapshot '{"theme_id":"10000000-0000-4000-8000-000000000001","decision_at":"2026-08-29T12:00:00Z"}' > "$acceptance_root/theme-snapshot.json"

${compose[@]} run --rm --no-deps \
	-v "$acceptance_root:/tmp/acceptance" \
	-v "$repo_root/fixtures/research:/tmp/research-fixtures:ro" \
	-v "$repo_root/scripts/research_acceptance.py:/tmp/research_acceptance.py:ro" \
	jupyter python /tmp/research_acceptance.py \
	--root /tmp/acceptance --snapshot /tmp/acceptance/theme-snapshot.json >/dev/null

jq -e '.future_reference_rejected == true and .outcome_policy == "close_to_close_equal_weight_v1"' \
	"$acceptance_root/acceptance-artifacts.json" >/dev/null

document_result=$(research_cli_file create-document "$acceptance_root/inputs/document.json")
jq -e --arg id "70000000-0000-4000-8000-000000000001" '.id == $id' <<<"$document_result" >/dev/null
research_cli_file link-document-entity "$acceptance_root/inputs/document-link.json" >/dev/null
research_cli_file raw-artifact "$acceptance_root/inputs/raw-artifact.json" >/dev/null
research_cli_file text-artifact "$acceptance_root/inputs/text-artifact.json" >/dev/null

research_cli_file create-event-proposal "$acceptance_root/inputs/bad-proposal.json" >/dev/null
research_cli_file event-revision "$acceptance_root/inputs/bad-event-revision.json" >/dev/null
research_cli_file event-revision "$acceptance_root/inputs/bad-event-rejected.json" >/dev/null
research_cli_file create-event-proposal "$acceptance_root/inputs/correct-proposal.json" >/dev/null
research_cli_file event-revision "$acceptance_root/inputs/correct-event-revision.json" >/dev/null
if research_cli_file event-revision "$acceptance_root/inputs/invalid-review.json" >/dev/null 2>&1; then
	echo "event review authorization failure was not enforced" >&2
	exit 1
fi
research_cli_file event-revision "$acceptance_root/inputs/correct-event-accepted.json" >/dev/null

research_cli_file register-pack "$acceptance_root/inputs/evidence-pack.json" >/dev/null
research_cli_file create-hypothesis "$acceptance_root/inputs/hypothesis.json" >/dev/null
research_cli_file hypothesis-revision "$acceptance_root/inputs/hypothesis-revision.json" >/dev/null
research_cli_file hypothesis-evidence "$acceptance_root/inputs/hypothesis-evidence.json" >/dev/null
research_cli_file create-prediction "$acceptance_root/inputs/prediction.json" >/dev/null
research_cli_file freeze-prediction "$acceptance_root/inputs/freeze.json" >/dev/null

psql_acceptance <<'SQL' >/dev/null
DO $$
DECLARE
    update_succeeded boolean := false;
BEGIN
    BEGIN
        UPDATE research_predictions
        SET expected_direction = 'down'
        WHERE id = '73000000-0000-4000-8000-000000000001';
        update_succeeded := true;
    EXCEPTION WHEN raise_exception THEN
        NULL;
    END;
    IF update_succeeded THEN
        RAISE EXCEPTION 'frozen prediction update was accepted';
    END IF;
END;
$$;

DO $$
DECLARE
    insert_succeeded boolean := false;
BEGIN
    BEGIN
        INSERT INTO research_event_proposal_revisions (
            proposal_id, revision, document_id, event_type, payload, source_spans,
            prompt_version, model, parameters, extraction_version, source_document_sha256,
            confidence, status, proposed_at, reviewer_id, review_method, reviewed_at,
            review_note, record_hash
        ) VALUES (
            '71000000-0000-4000-8000-000000000002', 3,
            '70000000-0000-4000-8000-000000000001', 'theme_exposure',
            '{"claim":"unauthorized"}'::jsonb, '[{"locator":"chars:0-1"}]'::jsonb,
            'fixture-prompt-v1', 'fixture-model-v1', '{}'::jsonb, 'event-proposal-1.0.0',
            'f2296908b2223bc082a2dc3a7a563bc2a12866be8a74a815e1a2bfd1d5056ec3',
            0.5, 'accepted', '2026-08-29T12:00:00Z', NULL, NULL, NULL, NULL,
            'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
        );
        insert_succeeded := true;
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;
    IF insert_succeeded THEN
        RAISE EXCEPTION 'unauthorized event review insert was accepted';
    END IF;
END;
$$;

DO $$
DECLARE
    insert_succeeded boolean := false;
BEGIN
    BEGIN
        INSERT INTO research_relationship_revisions (
            relationship_id, revision, from_entity_id, to_entity_id, relationship_type,
            direction, knowledge_kind, confidence, evidence_refs, author_method,
            valid_from, valid_until, recorded_at, revision_state, record_hash
        ) VALUES (
            '30000000-0000-4000-8000-000000000001', 2,
            '20000000-0000-4000-8000-000000000002',
            '20000000-0000-4000-8000-000000000001', 'SUPPLIES', 'forward', 'interpretation',
            0.9, '[]'::jsonb, 'acceptance-invalid-range', '2026-01-01T00:00:00Z',
            '2026-01-01T00:00:00Z', '2026-08-29T12:00:00Z', 'reviewed',
            'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
        );
        insert_succeeded := true;
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;
    IF insert_succeeded THEN
        RAISE EXCEPTION 'invalid relationship validity range was accepted';
    END IF;
END;
$$;

DO $$
DECLARE
    insert_succeeded boolean := false;
BEGIN
    BEGIN
        INSERT INTO research_theme_revisions (
            theme_id, revision, parent_theme_id, name, knowledge_kind, review_state,
            description, evidence_refs, author_method, recorded_at, record_hash
        ) VALUES (
            '10000000-0000-4000-8000-000000000001', 3, NULL, 'Invalid skipped revision',
            'interpretation', 'reviewed', 'This revision must not be admitted.', '[]'::jsonb,
            'acceptance-invalid-order', '2026-08-29T12:00:00Z',
            'cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc'
        );
        insert_succeeded := true;
    EXCEPTION WHEN raise_exception THEN
        NULL;
    END;
    IF insert_succeeded THEN
        RAISE EXCEPTION 'revision gap was accepted';
    END IF;
END;
$$;

DO $$
DECLARE
    update_succeeded boolean := false;
BEGIN
    BEGIN
        UPDATE research_theme_revisions
        SET description = 'mutated'
        WHERE theme_id = '10000000-0000-4000-8000-000000000001' AND revision = 1;
        update_succeeded := true;
    EXCEPTION WHEN raise_exception THEN
        NULL;
    END;
    IF update_succeeded THEN
        RAISE EXCEPTION 'append-only theme revision update was accepted';
    END IF;
END;
$$;
SQL

outcome_result=$(research_cli_file outcome "$acceptance_root/inputs/outcome.json")
jq -e --arg id "$(jq -r '.outcome_id' "$acceptance_root/acceptance-artifacts.json")" '.outcome_id == $id' <<<"$outcome_result" >/dev/null
printf '%s\n' "$outcome_result" > "$acceptance_root/outcome.json"

research_cli_json status-report '{"as_of":"2026-09-30T21:00:00Z"}' > "$acceptance_root/status-report.json"
jq -e '
  (.active_hypotheses | length == 1) and
  (.active_hypotheses[0].evidence_count == 1) and
  (.active_hypotheses[0].prediction_count == 1) and
  (.active_hypotheses[0].measured_prediction_count == 1) and
  (.upcoming_reviews | length == 0) and
  (.predictions | length == 1) and
  (.predictions[0].outcome_status == "measured") and
  (.predictions[0].measurement_policy == "close_to_close_equal_weight_v1")
' "$acceptance_root/status-report.json" >/dev/null

counts=$(psql_value "SELECT (SELECT count(*) FROM research_themes) || '|' || (SELECT count(*) FROM research_entities) || '|' || (SELECT count(*) FROM research_theme_memberships) || '|' || (SELECT count(*) FROM research_relationship_revisions) || '|' || (SELECT count(*) FROM research_theme_indicators) || '|' || (SELECT count(*) FROM research_theme_feature_refs) || '|' || (SELECT count(*) FROM research_theme_conditions) || '|' || (SELECT count(*) FROM research_documents) || '|' || (SELECT count(*) FROM research_document_artifacts) || '|' || (SELECT count(*) FROM research_document_text_artifacts) || '|' || (SELECT count(*) FROM research_event_proposals) || '|' || (SELECT count(*) FROM research_event_proposal_revisions) || '|' || (SELECT count(*) FROM research_evidence_packs) || '|' || (SELECT count(*) FROM research_hypotheses) || '|' || (SELECT count(*) FROM research_hypothesis_revisions) || '|' || (SELECT count(*) FROM research_hypothesis_evidence) || '|' || (SELECT count(*) FROM research_predictions) || '|' || (SELECT count(*) FROM research_prediction_outcomes)")
if [[ "$counts" != "7|8|7|6|2|2|3|1|1|1|2|4|1|1|1|1|1|1" ]]; then
	echo "unexpected v0.4 acceptance counts: $counts" >&2
	exit 1
fi

statuses=$(psql_value "SELECT string_agg(status, ',' ORDER BY proposal_id, revision) FROM research_event_proposal_revisions")
if [[ "$statuses" != "proposed,rejected,proposed,accepted" ]]; then
	echo "unexpected event review history: $statuses" >&2
	exit 1
fi

printf '%s\n' "v0.4 research workspace acceptance passed"
printf '%s\n' "artifacts: $acceptance_root"
