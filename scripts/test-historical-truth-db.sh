#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

psql_stdin() {
	docker compose exec -T postgres sh -c \
		'psql -v ON_ERROR_STOP=1 -X -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At'
}

printf '%s\n' 'applying and replaying migrations'
make migrate >/dev/null
make migrate >/dev/null

printf '%s\n' 'checking append-only historical publication constraints'
psql_stdin <<'SQL'
BEGIN;

INSERT INTO data_sources (
    id, code, name, source_kind, base_url, enabled, config, created_at, updated_at
) VALUES (
    '11111111-1111-4111-8111-111111111111',
    'historical_truth_test',
    'Historical truth test source',
    'fixture',
    'https://example.test',
    false,
    '{}'::jsonb,
    '2026-01-01T00:00:00Z',
    '2026-01-01T00:00:00Z'
);

INSERT INTO issuers (
    id, legal_name, country_code, created_at, updated_at
) VALUES (
    '33333333-3333-4333-8333-333333333333',
    'Historical Truth Test Issuer',
    'US',
    '2026-01-01T00:00:00Z',
    '2026-01-01T00:00:00Z'
);

INSERT INTO securities (
    id, issuer_id, name, security_type, exchange_mic, exchange_name,
    currency, primary_listing, created_at, updated_at
) VALUES (
    '22222222-2222-4222-8222-222222222222',
    '33333333-3333-4333-8333-333333333333',
    'Historical Truth Test Security',
    'common_stock',
    'XNAS',
    'Nasdaq',
    'USD',
    true,
    '2026-01-01T00:00:00Z',
    '2026-01-01T00:00:00Z'
);

INSERT INTO security_identifier_versions (
    id, schema_version, security_id, identifier_type, value, normalized_value,
    identifier_scope, valid_from, valid_until, available_at, source_reference,
    recorded_at, data_source_id, raw_payload_hash, revision, is_primary, record_hash
) VALUES (
    'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', '1.0.0',
    '22222222-2222-4222-8222-222222222222', 'ticker', 'ABC', 'ABC', 'XNAS',
    '2026-01-01T00:00:00Z', '2026-03-01T00:00:00Z', '2026-01-02T00:00:00Z',
    'https://example.test/identifier/0', '2026-01-02T00:01:00Z',
    '11111111-1111-4111-8111-111111111111',
    'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 0, true,
    'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
);

-- A later revision may overlap the earlier market interval.
INSERT INTO security_identifier_versions (
    id, schema_version, security_id, identifier_type, value, normalized_value,
    identifier_scope, valid_from, valid_until, available_at, source_reference,
    recorded_at, data_source_id, raw_payload_hash, revision, is_primary, record_hash
) VALUES (
    'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', '1.0.0',
    '22222222-2222-4222-8222-222222222222', 'ticker', 'ABC', 'ABC', 'XNAS',
    '2026-02-01T00:00:00Z', '2026-04-01T00:00:00Z', '2026-01-10T00:00:00Z',
    'https://example.test/identifier/1', '2026-01-10T00:01:00Z',
    '11111111-1111-4111-8111-111111111111',
    'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 1, true,
    'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
);

DO $$
BEGIN
    BEGIN
        INSERT INTO security_identifier_versions (
            id, schema_version, security_id, identifier_type, value, normalized_value,
            identifier_scope, valid_from, valid_until, available_at, source_reference,
            recorded_at, data_source_id, raw_payload_hash, revision, is_primary, record_hash
        ) VALUES (
            'cccccccc-cccc-4ccc-8ccc-cccccccccccc', '1.0.0',
            '22222222-2222-4222-8222-222222222222', 'ticker', 'ABC', 'ABC', 'XNAS',
            '2026-02-15T00:00:00Z', '2026-02-20T00:00:00Z', '2026-01-11T00:00:00Z',
            'https://example.test/identifier/conflict', '2026-01-11T00:01:00Z',
            '11111111-1111-4111-8111-111111111111',
            'cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc', 0, true,
            'cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc'
        );
        RAISE EXCEPTION 'same-source same-revision overlap was accepted';
    EXCEPTION WHEN exclusion_violation THEN
        NULL;
    END;
END;
$$;

DO $$
DECLARE
    before_revision integer;
    at_revision integer;
    after_validity integer;
    same_id_hash text;
BEGIN
    SELECT revision INTO before_revision
    FROM security_identifier_versions
    WHERE data_source_id = '11111111-1111-4111-8111-111111111111'
      AND identifier_type = 'ticker'
      AND normalized_value = 'ABC'
      AND identifier_scope = 'XNAS'
      AND valid_from <= '2026-01-10T00:00:00Z'
      AND '2026-01-10T00:00:00Z' < valid_until
      AND available_at <= '2026-01-09T23:59:59.999999Z'
    ORDER BY available_at DESC, revision DESC
    LIMIT 1;
    IF before_revision <> 0 THEN
        RAISE EXCEPTION 'availability boundary selected revision %, want 0', before_revision;
    END IF;

    SELECT revision INTO at_revision
    FROM security_identifier_versions
    WHERE data_source_id = '11111111-1111-4111-8111-111111111111'
      AND identifier_type = 'ticker'
      AND normalized_value = 'ABC'
      AND identifier_scope = 'XNAS'
      AND valid_from <= '2026-02-15T00:00:00Z'
      AND '2026-02-15T00:00:00Z' < valid_until
      AND available_at <= '2026-01-10T00:00:00Z'
    ORDER BY available_at DESC, revision DESC
    LIMIT 1;
    IF at_revision <> 1 THEN
        RAISE EXCEPTION 'inclusive availability boundary selected revision %, want 1', at_revision;
    END IF;

    SELECT count(*) INTO after_validity
    FROM security_identifier_versions
    WHERE data_source_id = '11111111-1111-4111-8111-111111111111'
      AND identifier_type = 'ticker'
      AND normalized_value = 'ABC'
      AND identifier_scope = 'XNAS'
      AND valid_from <= '2026-04-01T00:00:00Z'
      AND '2026-04-01T00:00:00Z' < valid_until;
    IF after_validity <> 0 THEN
        RAISE EXCEPTION 'exclusive validity boundary returned % rows', after_validity;
    END IF;

    SELECT record_hash INTO same_id_hash
    FROM security_identifier_versions
    WHERE id = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa';
    IF same_id_hash <> 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' THEN
        RAISE EXCEPTION 'stored record hash was not preserved';
    END IF;
END;
$$;

DO $$
BEGIN
    BEGIN
        UPDATE security_identifier_versions
        SET value = 'MUTATED'
        WHERE id = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa';
        RAISE EXCEPTION 'append-only update was accepted';
    EXCEPTION WHEN SQLSTATE '55000' THEN
        NULL;
    END;
END;
$$;

INSERT INTO calendar_manifests (
    id, schema_version, calendar_version, mic, exchange_timezone, available_at,
    source_reference, recorded_at, data_source_id, raw_payload_hash,
    session_fingerprint, session_count, record_hash
) VALUES (
    'dddddddd-dddd-4ddd-8ddd-dddddddddddd', '1.0.0', 'nasdaq-2026', 'XNAS',
    'America/New_York', '2026-01-01T00:00:00Z',
    'https://example.test/calendar/nasdaq-2026', '2026-01-01T00:01:00Z',
    '11111111-1111-4111-8111-111111111111',
    'dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd',
    'eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee', 2,
    'dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd'
);

INSERT INTO trading_sessions (
    id, schema_version, calendar_version, mic, exchange_timezone, session_date,
    session_status, open_at, close_at, is_early_close, available_at,
    source_reference, recorded_at, data_source_id, raw_payload_hash, revision,
    record_hash
) VALUES (
    'eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee', '1.0.0', 'nasdaq-2026', 'XNAS',
    'America/New_York', '2026-01-02', 'open', '2026-01-02T14:30:00Z',
    '2026-01-02T21:00:00Z', false, '2026-01-01T00:00:00Z',
    'https://example.test/calendar/nasdaq-2026/2026-01-02', '2026-01-01T00:01:00Z',
    '11111111-1111-4111-8111-111111111111',
    'ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff', 0,
    'ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff'
), (
    '99999999-9999-4999-8999-999999999999', '1.0.0', 'nasdaq-2026', 'XNAS',
    'America/New_York', '2026-01-03', 'closed', NULL, NULL, false,
    '2026-01-01T00:00:00Z',
    'https://example.test/calendar/nasdaq-2026/2026-01-03', '2026-01-01T00:01:00Z',
    '11111111-1111-4111-8111-111111111111',
    '9999999999999999999999999999999999999999999999999999999999999999', 0,
    '9999999999999999999999999999999999999999999999999999999999999999'
);

DO $$
DECLARE
    before_close integer;
    at_close integer;
BEGIN
    SELECT count(*) INTO before_close
    FROM trading_sessions
    WHERE data_source_id = '11111111-1111-4111-8111-111111111111'
      AND calendar_version = 'nasdaq-2026'
      AND mic = 'XNAS'
      AND session_status = 'open'
      AND open_at <= '2026-01-02T20:59:59.999999Z'
      AND '2026-01-02T20:59:59.999999Z' < close_at;
    SELECT count(*) INTO at_close
    FROM trading_sessions
    WHERE data_source_id = '11111111-1111-4111-8111-111111111111'
      AND calendar_version = 'nasdaq-2026'
      AND mic = 'XNAS'
      AND session_status = 'open'
      AND open_at <= '2026-01-02T21:00:00Z'
      AND '2026-01-02T21:00:00Z' < close_at;
    IF before_close <> 1 OR at_close <> 0 THEN
        RAISE EXCEPTION 'session half-open boundary failed: before %, at %', before_close, at_close;
    END IF;
END;
$$;

ROLLBACK;
SQL

printf '%s\n' 'checking transactional rollback'
rollback_count=$(docker compose exec -T postgres sh -c \
	'psql -v ON_ERROR_STOP=1 -X -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "$1"' \
	sh "SELECT count(*) FROM security_identifier_versions WHERE id = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa';")
if [[ "$rollback_count" != "0" ]]; then
	printf 'fixture rows survived rollback: %s\n' "$rollback_count" >&2
	exit 1
fi

printf '%s\n' 'checking fresh-image migration wiring'
docker compose build postgres >/dev/null
fresh_image=$(docker image inspect --format '{{.Id}}' invs-postgres:latest)
docker run --rm --entrypoint sh "$fresh_image" \
	-c 'test -r /docker-entrypoint-initdb.d/000006_historical_truth.sql'

fresh_volume="invs-historical-truth-test-$PPID-$$"
fresh_container="invs-historical-truth-test-$PPID-$$"

cleanup_fresh() {
	docker rm -f "$fresh_container" >/dev/null 2>&1 || true
	docker volume rm "$fresh_volume" >/dev/null 2>&1 || true
}
trap cleanup_fresh EXIT

docker volume create "$fresh_volume" >/dev/null
docker run -d --name "$fresh_container" \
	-e POSTGRES_DB=historical_truth_test \
	-e POSTGRES_USER=historical_truth_test \
	-e POSTGRES_PASSWORD=historical_truth_test \
	-v "$fresh_volume":/var/lib/postgresql/data \
	"$fresh_image" >/dev/null

fresh_table_count=
for attempt in $(seq 1 120); do
	fresh_table_count=$(docker exec "$fresh_container" psql -v ON_ERROR_STOP=1 -X \
		-U historical_truth_test -d historical_truth_test -Atc \
		"SELECT count(*) FROM pg_class WHERE oid IN (to_regclass('public.security_identifier_versions'), to_regclass('public.security_listing_versions'), to_regclass('public.universe_memberships'), to_regclass('public.calendar_manifests'), to_regclass('public.trading_sessions'));" 2>/dev/null | tr -d '[:space:]' || true)
	if [[ "$fresh_table_count" == "5" ]]; then
		break
	fi
	if [[ "$attempt" == 120 ]]; then
		printf 'fresh postgres initialization created %s historical tables\n' "${fresh_table_count:-none}" >&2
		docker logs "$fresh_container" >&2 || true
		exit 1
	fi
	sleep 1
done
if [[ "$fresh_table_count" != "5" ]]; then
	printf 'fresh initialization created %s historical tables, want 5\n' "$fresh_table_count" >&2
	exit 1
fi

docker exec -i "$fresh_container" psql -v ON_ERROR_STOP=1 -X \
	-U historical_truth_test -d historical_truth_test \
	< migrations/000006_historical_truth.down.sql >/dev/null
after_down_count=$(docker exec "$fresh_container" psql -v ON_ERROR_STOP=1 -X \
	-U historical_truth_test -d historical_truth_test -Atc \
	"SELECT count(*) FROM pg_class WHERE oid IN (to_regclass('public.security_identifier_versions'), to_regclass('public.security_listing_versions'), to_regclass('public.universe_memberships'), to_regclass('public.calendar_manifests'), to_regclass('public.trading_sessions'));" | tr -d '[:space:]')
if [[ "$after_down_count" != "0" ]]; then
	printf 'migration rollback left %s historical tables, want 0\n' "$after_down_count" >&2
	exit 1
fi

docker exec -i "$fresh_container" psql -v ON_ERROR_STOP=1 -X \
	-U historical_truth_test -d historical_truth_test \
	< migrations/000006_historical_truth.up.sql >/dev/null
after_up_count=$(docker exec "$fresh_container" psql -v ON_ERROR_STOP=1 -X \
	-U historical_truth_test -d historical_truth_test -Atc \
	"SELECT count(*) FROM pg_class WHERE oid IN (to_regclass('public.security_identifier_versions'), to_regclass('public.security_listing_versions'), to_regclass('public.universe_memberships'), to_regclass('public.calendar_manifests'), to_regclass('public.trading_sessions'));" | tr -d '[:space:]')
if [[ "$after_up_count" != "5" ]]; then
	printf 'migration re-apply created %s historical tables, want 5\n' "$after_up_count" >&2
	exit 1
fi

printf '%s\n' 'historical truth PostgreSQL migration checks passed'
