#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

compose=(docker compose --progress quiet)
acceptance_root="$repo_root/data/research/acceptance/v0.6/catalog"
acceptance_db="invs_v06_paper_acceptance_$$"

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

registration_result=$(research_cli_file register-paper-account "$repo_root/fixtures/research/paper-account-registration.json")
jq -e '.already_present == false and .account_id == "40000000-0000-4000-8000-000000000201" and (.registration_sha256 | test("^[0-9a-f]{64}$"))' <<<"$registration_result" >/dev/null
registration_repeat=$(research_cli_file register-paper-account "$repo_root/fixtures/research/paper-account-registration.json")
jq -e '.already_present == true' <<<"$registration_repeat" >/dev/null

event_result=$(research_cli_file paper-account-event "$repo_root/fixtures/research/paper-account-event.json")
jq -e '.already_present == false and .sequence == 1 and (.record_hash | test("^[0-9a-f]{64}$"))' <<<"$event_result" >/dev/null
event_repeat=$(research_cli_file paper-account-event "$repo_root/fixtures/research/paper-account-event.json")
jq -e '.already_present == true and .sequence == 1' <<<"$event_repeat" >/dev/null

research_cli_file paper-account-report "$repo_root/fixtures/research/paper-account-report.json" > "$acceptance_root/paper-account-report.json"
jq -e '
  .account.account_id == "40000000-0000-4000-8000-000000000201" and
  .account.status == "active" and
  .event_count == 1 and
  .last_event.sequence == 1 and
  .last_event.event_type == "cash_deposit"
' "$acceptance_root/paper-account-report.json" >/dev/null

psql_acceptance <<'SQL' >/dev/null
DO $$
DECLARE
    update_succeeded boolean := false;
BEGIN
    BEGIN
        UPDATE paper_accounts
        SET status = 'closed'
        WHERE account_id = '40000000-0000-4000-8000-000000000201';
        update_succeeded := true;
    EXCEPTION WHEN raise_exception THEN
        NULL;
    END;
    IF update_succeeded THEN
        RAISE EXCEPTION 'append-only paper account update was accepted';
    END IF;
END;
$$;

DO $$
DECLARE
    insert_succeeded boolean := false;
BEGIN
    BEGIN
        INSERT INTO paper_account_events (
            account_id, sequence, event_id, idempotency_key, event_at, session_date,
            event_type, currency, quantity_delta, amount_local_delta, amount_base_delta,
            details, record_hash
        ) VALUES (
            '40000000-0000-4000-8000-000000000201', 3,
            '50000000-0000-4000-8000-000000000202', 'sequence-gap:v0.6',
            '2026-01-02T12:00:00Z', '2026-01-02', 'valuation', 'USD',
            0, 0, 0, '{}'::jsonb,
            'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
        );
        insert_succeeded := true;
    EXCEPTION WHEN raise_exception THEN
        NULL;
    END;
    IF insert_succeeded THEN
        RAISE EXCEPTION 'paper account event sequence gap was accepted';
    END IF;
END;
$$;

DO $$
DECLARE
    update_succeeded boolean := false;
BEGIN
    BEGIN
        UPDATE paper_account_events
        SET amount_local_delta = 1
        WHERE account_id = '40000000-0000-4000-8000-000000000201' AND sequence = 1;
        update_succeeded := true;
    EXCEPTION WHEN raise_exception THEN
        NULL;
    END;
    IF update_succeeded THEN
        RAISE EXCEPTION 'append-only paper account event update was accepted';
    END IF;
END;
$$;
SQL

counts=$(psql_value "SELECT (SELECT count(*) FROM paper_accounts) || '|' || (SELECT count(*) FROM paper_account_events)")
if [[ "$counts" != "1|1" ]]; then
	echo "unexpected v0.6 paper catalog counts: $counts" >&2
	exit 1
fi

status=$(psql_value "SELECT status || '|' || coalesce(last_event_sequence::text, '0') || '|' || coalesce(last_event_type, '') FROM paper_account_status")
if [[ "$status" != "active|1|cash_deposit" ]]; then
	echo "unexpected v0.6 paper catalog status: $status" >&2
	exit 1
fi

printf '%s\n' "v0.6 paper metadata acceptance passed"
printf '%s\n' "report: $acceptance_root/paper-account-report.json"
