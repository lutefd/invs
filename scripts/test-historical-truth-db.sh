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

INSERT INTO ingestion_runs (
    id, data_source_id, run_key, status, started_at
) VALUES (
    '77777777-7777-4777-8777-777777777777',
    '11111111-1111-4111-8111-111111111111',
    'historical-truth-corporate-action-fixture',
    'running',
    '2026-01-01T00:00:00Z'
);

INSERT INTO market_price_snapshots (
    data_source_id, security_id, ingestion_run_id, schema_version, interval,
    price_basis, currency, observed_at, observed_precision, published_at,
    available_at, ingested_at, published_precision, open_value, high_value,
    low_value, close_value, volume_value, raw_payload_hash
) VALUES (
    '11111111-1111-4111-8111-111111111111',
    '22222222-2222-4222-8222-222222222222',
    '77777777-7777-4777-8777-777777777777',
    '1.0.0', '1d', 'raw', 'USD', '2021-09-08T00:00:00Z', 'date', NULL,
    '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 'unknown',
    '10', '12', '9', '11', '100',
    'dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd'
);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM market_price_snapshots
        WHERE security_id = '22222222-2222-4222-8222-222222222222'
          AND published_at IS NULL
    ) THEN
        RAISE EXCEPTION 'nullable price publication was not retained';
    END IF;
END;
$$;

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

INSERT INTO corporate_action_versions (
    id, schema_version, security_id, source_event_id, revision, action_status,
    action_type, observed_at, observed_precision, published_at,
    published_precision, available_at, effective_at, effective_precision,
    record_date, payment_date, ratio_numerator, ratio_denominator, cash_amount,
    currency, target_security_id, source_reference, raw_record_locator,
    recorded_at, data_source_id, ingestion_run_id, raw_payload_hash, ingested_at,
    normalizer_version, record_hash
) VALUES (
    '12121212-1212-4212-8212-121212121212', '2.0.0',
    '22222222-2222-4222-8222-222222222222',
    'fixture/cash-dividend', 0, 'active', 'cash_dividend',
    '2026-01-08T00:00:00Z', 'date', '2026-01-02T00:00:00Z', 'date',
    '2026-01-03T00:00:00Z', '2026-01-08T00:00:00Z', 'date',
    '2026-01-07', '2026-01-10', NULL, NULL, '1.25', 'USD', NULL,
    'https://example.test/action/0', 'fixture.csv#row=2',
    '2026-01-03T00:01:00Z', '11111111-1111-4111-8111-111111111111',
    '77777777-7777-4777-8777-777777777777',
    '1212121212121212121212121212121212121212121212121212121212121212',
    '2026-01-03T00:00:30Z', 'fixture-action-v1',
    '1212121212121212121212121212121212121212121212121212121212121212'
), (
    '13131313-1313-4313-8313-131313131313', '2.0.0',
    '22222222-2222-4222-8222-222222222222',
    'fixture/cash-dividend', 1, 'active', 'cash_dividend',
    '2026-01-08T00:00:00Z', 'date', '2026-01-04T00:00:00Z', 'date',
    '2026-01-05T00:00:00Z', '2026-01-08T00:00:00Z', 'date',
    '2026-01-07', '2026-01-10', NULL, NULL, '1.30', 'USD', NULL,
    'https://example.test/action/1', 'fixture.csv#row=3',
    '2026-01-05T00:01:00Z', '11111111-1111-4111-8111-111111111111',
    '77777777-7777-4777-8777-777777777777',
    '1313131313131313131313131313131313131313131313131313131313131313',
    '2026-01-05T00:00:30Z', 'fixture-action-v1',
    '1313131313131313131313131313131313131313131313131313131313131313'
);

-- Unsupported action versions must remain storable even when the missing
-- economics are the reason downstream adjustment will fail closed.
INSERT INTO corporate_action_versions (
    id, schema_version, security_id, source_event_id, revision, action_status,
    action_type, observed_at, observed_precision, published_at,
    published_precision, available_at, effective_at, effective_precision,
    record_date, payment_date, ratio_numerator, ratio_denominator, cash_amount,
    currency, target_security_id, source_reference, raw_record_locator,
    recorded_at, data_source_id, ingestion_run_id, raw_payload_hash, ingested_at,
    normalizer_version, record_hash
)
SELECT
    '14141414-1414-4414-8414-141414141414', schema_version, security_id,
    'fixture/unsupported-split', 0, 'unsupported', 'split', observed_at,
    observed_precision, published_at, published_precision, available_at,
    effective_at, effective_precision, NULL, NULL, NULL, NULL, NULL, NULL,
    NULL, 'https://example.test/action/unsupported', 'fixture.csv#row=4',
    recorded_at, data_source_id, ingestion_run_id,
    '1414141414141414141414141414141414141414141414141414141414141414',
    ingested_at, normalizer_version,
    '1414141414141414141414141414141414141414141414141414141414141414'
FROM corporate_action_versions
WHERE id = '12121212-1212-4212-8212-121212121212';

DO $$
DECLARE
    before_revision text;
    at_revision text;
BEGIN
    SELECT cash_amount INTO before_revision
    FROM corporate_action_versions
    WHERE data_source_id = '11111111-1111-4111-8111-111111111111'
      AND security_id = '22222222-2222-4222-8222-222222222222'
      AND source_event_id = 'fixture/cash-dividend'
      AND available_at <= '2026-01-04T23:59:59.999999Z'
    ORDER BY available_at DESC, revision DESC, recorded_at DESC
    LIMIT 1;
    IF before_revision <> '1.25' THEN
        RAISE EXCEPTION 'pre-correction action amount %, want 1.25', before_revision;
    END IF;

    SELECT cash_amount INTO at_revision
    FROM corporate_action_versions
    WHERE data_source_id = '11111111-1111-4111-8111-111111111111'
      AND security_id = '22222222-2222-4222-8222-222222222222'
      AND source_event_id = 'fixture/cash-dividend'
      AND available_at <= '2026-01-05T00:00:00Z'
    ORDER BY available_at DESC, revision DESC, recorded_at DESC
    LIMIT 1;
    IF at_revision <> '1.30' THEN
        RAISE EXCEPTION 'inclusive action correction amount %, want 1.30', at_revision;
    END IF;
END;
$$;

DO $$
BEGIN
    BEGIN
        UPDATE corporate_action_versions
        SET cash_amount = '9.99'
        WHERE id = '12121212-1212-4212-8212-121212121212';
        RAISE EXCEPTION 'corporate-action append-only update was accepted';
    EXCEPTION WHEN SQLSTATE '55000' THEN
        NULL;
    END;
END;
$$;

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
	sh "SELECT (SELECT count(*) FROM security_identifier_versions WHERE id = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa') + (SELECT count(*) FROM corporate_action_versions WHERE id = '12121212-1212-4212-8212-121212121212');")
if [[ "$rollback_count" != "0" ]]; then
	printf 'fixture rows survived rollback: %s\n' "$rollback_count" >&2
	exit 1
fi

printf '%s\n' 'checking fresh-image migration wiring'
docker compose build postgres >/dev/null
fresh_image=$(docker image inspect --format '{{.Id}}' invs-postgres:latest)
docker run --rm --entrypoint sh "$fresh_image" \
	-c 'test -r /docker-entrypoint-initdb.d/000006_historical_truth.sql && test -r /docker-entrypoint-initdb.d/000007_corporate_actions.sql && test -r /docker-entrypoint-initdb.d/000008_price_basis.sql && test -r /docker-entrypoint-initdb.d/000009_nullable_price_publication.sql'

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
		"SELECT count(*) FROM pg_class WHERE oid IN (to_regclass('public.security_identifier_versions'), to_regclass('public.security_listing_versions'), to_regclass('public.universe_memberships'), to_regclass('public.calendar_manifests'), to_regclass('public.trading_sessions'), to_regclass('public.corporate_action_versions'));" 2>/dev/null | tr -d '[:space:]' || true)
	if [[ "$fresh_table_count" == "6" ]]; then
		break
	fi
	if [[ "$attempt" == 120 ]]; then
		printf 'fresh postgres initialization created %s historical tables\n' "${fresh_table_count:-none}" >&2
		docker logs "$fresh_container" >&2 || true
		exit 1
	fi
	sleep 1
done
if [[ "$fresh_table_count" != "6" ]]; then
	printf 'fresh initialization created %s historical tables, want 6\n' "$fresh_table_count" >&2
	exit 1
fi
fresh_price_basis=$(docker exec "$fresh_container" psql -v ON_ERROR_STOP=1 -X \
	-U historical_truth_test -d historical_truth_test -Atc \
	"SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid = 'public.market_price_snapshots'::regclass AND conname = 'market_price_snapshots_price_basis_check';")
if [[ "$fresh_price_basis" != *"split_adjusted"* || "$fresh_price_basis" != *"total_return_adjusted"* ]]; then
	printf 'fresh initialization retained the raw-only price basis constraint: %s\n' "$fresh_price_basis" >&2
	exit 1
fi
fresh_price_publication_nullable=$(docker exec "$fresh_container" psql -v ON_ERROR_STOP=1 -X \
	-U historical_truth_test -d historical_truth_test -Atc \
	"SELECT is_nullable FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'market_price_snapshots' AND column_name = 'published_at';")
if [[ "$fresh_price_publication_nullable" != "YES" ]]; then
	printf 'fresh initialization price publication nullable=%s, want YES\n' "$fresh_price_publication_nullable" >&2
	exit 1
fi

docker exec -i "$fresh_container" psql -v ON_ERROR_STOP=1 -X \
	-U historical_truth_test -d historical_truth_test \
	< migrations/000009_nullable_price_publication.down.sql >/dev/null
docker exec -i "$fresh_container" psql -v ON_ERROR_STOP=1 -X \
	-U historical_truth_test -d historical_truth_test \
	< migrations/000008_price_basis.down.sql >/dev/null
docker exec -i "$fresh_container" psql -v ON_ERROR_STOP=1 -X \
	-U historical_truth_test -d historical_truth_test \
	< migrations/000007_corporate_actions.down.sql >/dev/null
docker exec -i "$fresh_container" psql -v ON_ERROR_STOP=1 -X \
	-U historical_truth_test -d historical_truth_test \
	< migrations/000006_historical_truth.down.sql >/dev/null
after_down_count=$(docker exec "$fresh_container" psql -v ON_ERROR_STOP=1 -X \
	-U historical_truth_test -d historical_truth_test -Atc \
	"SELECT count(*) FROM pg_class WHERE oid IN (to_regclass('public.security_identifier_versions'), to_regclass('public.security_listing_versions'), to_regclass('public.universe_memberships'), to_regclass('public.calendar_manifests'), to_regclass('public.trading_sessions'), to_regclass('public.corporate_action_versions'));" | tr -d '[:space:]')
if [[ "$after_down_count" != "0" ]]; then
	printf 'migration rollback left %s historical tables, want 0\n' "$after_down_count" >&2
	exit 1
fi

docker exec -i "$fresh_container" psql -v ON_ERROR_STOP=1 -X \
	-U historical_truth_test -d historical_truth_test \
	< migrations/000006_historical_truth.up.sql >/dev/null
docker exec -i "$fresh_container" psql -v ON_ERROR_STOP=1 -X \
	-U historical_truth_test -d historical_truth_test \
	< migrations/000007_corporate_actions.up.sql >/dev/null
docker exec -i "$fresh_container" psql -v ON_ERROR_STOP=1 -X \
	-U historical_truth_test -d historical_truth_test \
	< migrations/000008_price_basis.up.sql >/dev/null
docker exec -i "$fresh_container" psql -v ON_ERROR_STOP=1 -X \
	-U historical_truth_test -d historical_truth_test \
	< migrations/000009_nullable_price_publication.up.sql >/dev/null
after_up_count=$(docker exec "$fresh_container" psql -v ON_ERROR_STOP=1 -X \
	-U historical_truth_test -d historical_truth_test -Atc \
	"SELECT count(*) FROM pg_class WHERE oid IN (to_regclass('public.security_identifier_versions'), to_regclass('public.security_listing_versions'), to_regclass('public.universe_memberships'), to_regclass('public.calendar_manifests'), to_regclass('public.trading_sessions'), to_regclass('public.corporate_action_versions'));" | tr -d '[:space:]')
if [[ "$after_up_count" != "6" ]]; then
	printf 'migration re-apply created %s historical tables, want 6\n' "$after_up_count" >&2
	exit 1
fi
reapplied_price_basis=$(docker exec "$fresh_container" psql -v ON_ERROR_STOP=1 -X \
	-U historical_truth_test -d historical_truth_test -Atc \
	"SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid = 'public.market_price_snapshots'::regclass AND conname = 'market_price_snapshots_price_basis_check';")
if [[ "$reapplied_price_basis" != *"split_adjusted"* || "$reapplied_price_basis" != *"total_return_adjusted"* ]]; then
	printf 'migration re-apply retained the raw-only price basis constraint: %s\n' "$reapplied_price_basis" >&2
	exit 1
fi
reapplied_price_publication_nullable=$(docker exec "$fresh_container" psql -v ON_ERROR_STOP=1 -X \
	-U historical_truth_test -d historical_truth_test -Atc \
	"SELECT is_nullable FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'market_price_snapshots' AND column_name = 'published_at';")
if [[ "$reapplied_price_publication_nullable" != "YES" ]]; then
	printf 'migration re-apply price publication nullable=%s, want YES\n' "$reapplied_price_publication_nullable" >&2
	exit 1
fi

printf '%s\n' 'historical truth PostgreSQL migration checks passed'
