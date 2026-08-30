BEGIN;

-- Paper account specifications and event envelopes are cataloged in
-- PostgreSQL for discovery and monitoring. The canonical account JSON,
-- targets, orders, reports, and complete replay ledger remain addressed local
-- artifacts under data/research.
CREATE TABLE paper_accounts (
    account_id uuid PRIMARY KEY,
    schema_version text NOT NULL CHECK (schema_version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
    name text NOT NULL CHECK (btrim(name) <> ''),
    base_currency text NOT NULL CHECK (base_currency ~ '^[A-Z]{3}$'),
    strategy_name text NOT NULL CHECK (strategy_name ~ '^[a-z][a-z0-9_-]{1,63}$'),
    strategy_version text NOT NULL CHECK (strategy_version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
    strategy_parameters jsonb NOT NULL CHECK (jsonb_typeof(strategy_parameters) = 'object'),
    start_date date NOT NULL,
    end_date date NOT NULL,
    input_fingerprint text NOT NULL CHECK (input_fingerprint ~ '^[0-9a-f]{64}$'),
    policy_version text NOT NULL CHECK (policy_version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
    approval_mode text NOT NULL CHECK (approval_mode IN ('manual', 'auto')),
    spec_path text NOT NULL CHECK (
        btrim(spec_path) <> ''
        AND left(spec_path, 1) <> '/'
        AND left(spec_path, 1) <> chr(92)
        AND position('..' in spec_path) = 0
    ),
    spec_sha256 text NOT NULL CHECK (spec_sha256 ~ '^[0-9a-f]{64}$'),
    engine_version text NOT NULL CHECK (btrim(engine_version) <> ''),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'halted', 'closed')),
    created_at timestamptz NOT NULL,
    registered_at timestamptz NOT NULL DEFAULT now(),
    registration_sha256 text NOT NULL CHECK (registration_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT paper_account_period_check CHECK (start_date <= end_date),
    UNIQUE (spec_sha256)
);

CREATE INDEX paper_accounts_strategy_idx
    ON paper_accounts (strategy_name, strategy_version, start_date, end_date);
CREATE INDEX paper_accounts_status_idx
    ON paper_accounts (status, registered_at DESC);

CREATE TABLE paper_account_events (
    account_id uuid NOT NULL REFERENCES paper_accounts (account_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    sequence bigint NOT NULL CHECK (sequence > 0),
    event_id uuid NOT NULL UNIQUE,
    idempotency_key text NOT NULL CHECK (btrim(idempotency_key) <> ''),
    event_at timestamptz NOT NULL,
    session_date date NOT NULL,
    event_type text NOT NULL CHECK (event_type IN (
        'cash_deposit', 'decision', 'target_published', 'risk_approved',
        'risk_rejected', 'halt', 'no_op', 'approval', 'manual_rejection',
        'order', 'fill', 'fee', 'tax', 'dividend', 'split', 'delisting',
        'valuation', 'reconciliation', 'fx_conversion'
    )),
    decision_id uuid,
    target_id uuid,
    order_id uuid,
    security_id uuid,
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    quantity_delta numeric NOT NULL,
    amount_local_delta numeric NOT NULL,
    amount_base_delta numeric NOT NULL,
    nav_base numeric CHECK (nav_base IS NULL OR nav_base >= 0),
    cash_base numeric CHECK (cash_base IS NULL OR cash_base >= 0),
    positions_value_base numeric CHECK (positions_value_base IS NULL OR positions_value_base >= 0),
    details jsonb NOT NULL CHECK (jsonb_typeof(details) = 'object'),
    record_hash text NOT NULL CHECK (record_hash ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, sequence),
    UNIQUE (account_id, idempotency_key)
);

CREATE INDEX paper_account_events_lookup_idx
    ON paper_account_events (account_id, session_date, sequence);
CREATE INDEX paper_account_events_decision_idx
    ON paper_account_events (account_id, decision_id, sequence)
    WHERE decision_id IS NOT NULL;
CREATE INDEX paper_account_events_type_idx
    ON paper_account_events (account_id, event_type, sequence DESC);

CREATE OR REPLACE FUNCTION paper_account_event_order_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    expected_sequence bigint;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('paper-account:' || NEW.account_id::text, 0));
    IF NOT EXISTS (SELECT 1 FROM paper_accounts WHERE account_id = NEW.account_id) THEN
        RAISE EXCEPTION 'paper account % does not exist', NEW.account_id;
    END IF;
    SELECT coalesce(max(sequence), 0) + 1 INTO expected_sequence
    FROM paper_account_events
    WHERE account_id = NEW.account_id;
    IF NEW.sequence <> expected_sequence THEN
        RAISE EXCEPTION 'paper account event sequence must be %, got %', expected_sequence, NEW.sequence;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER paper_account_event_order
    BEFORE INSERT ON paper_account_events
    FOR EACH ROW EXECUTE FUNCTION paper_account_event_order_guard();

CREATE TRIGGER paper_account_immutable
    BEFORE UPDATE OR DELETE ON paper_accounts
    FOR EACH ROW EXECUTE FUNCTION research_append_only_guard();
CREATE TRIGGER paper_account_event_immutable
    BEFORE UPDATE OR DELETE ON paper_account_events
    FOR EACH ROW EXECUTE FUNCTION research_append_only_guard();

CREATE VIEW paper_account_status AS
SELECT account.account_id,
       account.name,
       account.strategy_name,
       account.strategy_version,
       account.base_currency,
       account.status,
       account.approval_mode,
       account.start_date,
       account.end_date,
       latest.sequence AS last_event_sequence,
       latest.session_date AS last_event_session,
       latest.event_type AS last_event_type,
       latest.event_at AS last_event_at,
       latest.nav_base AS last_nav_base,
       latest.cash_base AS last_cash_base,
       latest.positions_value_base AS last_positions_value_base,
       latest.record_hash AS last_event_hash
FROM paper_accounts account
LEFT JOIN LATERAL (
    SELECT event.sequence, event.session_date, event.event_type, event.event_at,
           event.nav_base, event.cash_base, event.positions_value_base,
           event.record_hash
    FROM paper_account_events event
    WHERE event.account_id = account.account_id
    ORDER BY event.sequence DESC
    LIMIT 1
) latest ON true;

COMMENT ON TABLE paper_accounts IS
    'Immutable v0.6 paper account catalog; canonical specifications remain addressed local artifacts.';
COMMENT ON TABLE paper_account_events IS
    'Immutable PostgreSQL event envelope for paper account discovery and monitoring; local ledger remains replay source of truth.';
COMMENT ON VIEW paper_account_status IS
    'Latest append-only paper event joined to its immutable account catalog.';

COMMIT;
