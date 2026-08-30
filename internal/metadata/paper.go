package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const PaperSchemaVersion = "1.0.0"

type PaperAccount struct {
	AccountID          string          `json:"account_id"`
	SchemaVersion      string          `json:"schema_version"`
	Name               string          `json:"name"`
	BaseCurrency       string          `json:"base_currency"`
	StrategyName       string          `json:"strategy_name"`
	StrategyVersion    string          `json:"strategy_version"`
	StrategyParameters json.RawMessage `json:"strategy_parameters"`
	StartDate          string          `json:"start_date"`
	EndDate            string          `json:"end_date"`
	InputFingerprint   string          `json:"input_fingerprint"`
	PolicyVersion      string          `json:"policy_version"`
	ApprovalMode       string          `json:"approval_mode"`
	SpecPath           string          `json:"spec_path"`
	SpecSHA256         string          `json:"spec_sha256"`
	EngineVersion      string          `json:"engine_version"`
	Status             string          `json:"status,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	RegisteredAt       time.Time       `json:"registered_at,omitempty"`
	RegistrationSHA256 string          `json:"registration_sha256,omitempty"`
}

type PaperAccountRegistration struct {
	Account PaperAccount `json:"account"`
}

type PaperAccountRegistrationResult struct {
	AccountID          string `json:"account_id"`
	RegistrationSHA256 string `json:"registration_sha256"`
	AlreadyPresent     bool   `json:"already_present"`
}

type PaperAccountEvent struct {
	AccountID          string          `json:"account_id"`
	Sequence           int64           `json:"sequence"`
	EventID            string          `json:"event_id"`
	IdempotencyKey     string          `json:"idempotency_key"`
	EventAt            time.Time       `json:"event_at"`
	SessionDate        string          `json:"session_date"`
	EventType          string          `json:"event_type"`
	DecisionID         string          `json:"decision_id,omitempty"`
	TargetID           string          `json:"target_id,omitempty"`
	OrderID            string          `json:"order_id,omitempty"`
	SecurityID         string          `json:"security_id,omitempty"`
	Currency           string          `json:"currency"`
	QuantityDelta      string          `json:"quantity_delta"`
	AmountLocalDelta   string          `json:"amount_local_delta"`
	AmountBaseDelta    string          `json:"amount_base_delta"`
	NavBase            *string         `json:"nav_base,omitempty"`
	CashBase           *string         `json:"cash_base,omitempty"`
	PositionsValueBase *string         `json:"positions_value_base,omitempty"`
	Details            json.RawMessage `json:"details"`
	RecordHash         string          `json:"record_hash"`
}

type PaperAccountReport struct {
	Account    PaperAccount       `json:"account"`
	EventCount int64              `json:"event_count"`
	LastEvent  *PaperAccountEvent `json:"last_event,omitempty"`
}

var (
	paperIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)
	paperVersionPattern    = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	paperCurrencyPattern   = regexp.MustCompile(`^[A-Z]{3}$`)
	paperDatePattern       = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	paperDecimalPattern    = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)
	paperHashPattern       = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

const insertPaperAccountSQL = `
INSERT INTO paper_accounts (
    account_id, schema_version, name, base_currency, strategy_name,
    strategy_version, strategy_parameters, start_date, end_date,
    input_fingerprint, policy_version, approval_mode, spec_path, spec_sha256,
    engine_version, status, created_at, registration_sha256
) VALUES (
    $1::uuid, $2, $3, $4, $5,
    $6, $7::jsonb, $8::date, $9::date,
    $10, $11, $12, $13, $14,
    $15, $16, $17, $18
)
ON CONFLICT (account_id) DO NOTHING
RETURNING registration_sha256`

const insertPaperAccountEventSQL = `
INSERT INTO paper_account_events (
    account_id, sequence, event_id, idempotency_key, event_at, session_date,
    event_type, decision_id, target_id, order_id, security_id, currency,
    quantity_delta, amount_local_delta, amount_base_delta, nav_base, cash_base,
    positions_value_base, details, record_hash
) VALUES (
    $1::uuid, $2, $3::uuid, $4, $5, $6::date,
    $7, NULLIF($8, '')::uuid, NULLIF($9, '')::uuid,
    NULLIF($10, '')::uuid, NULLIF($11, '')::uuid, $12,
    $13::numeric, $14::numeric, $15::numeric, NULLIF($16, '')::numeric,
    NULLIF($17, '')::numeric, NULLIF($18, '')::numeric, $19::jsonb, $20
)
RETURNING record_hash`

func normalizePaperObject(value json.RawMessage, label string) (json.RawMessage, error) {
	if len(bytes.TrimSpace(value)) == 0 {
		return []byte("{}"), nil
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	decoded, err := decodeBacktestJSONValue(decoder)
	if err != nil {
		return nil, fmt.Errorf("%s must be valid JSON: %w", label, err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s has trailing JSON: %w", label, err)
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a JSON object", label)
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("canonicalize %s: %w", label, err)
	}
	return canonical, nil
}

func normalizePaperAccount(account PaperAccount) (PaperAccount, error) {
	account.AccountID = strings.TrimSpace(account.AccountID)
	account.SchemaVersion = strings.TrimSpace(account.SchemaVersion)
	account.Name = strings.TrimSpace(account.Name)
	account.BaseCurrency = strings.TrimSpace(account.BaseCurrency)
	account.StrategyName = strings.TrimSpace(account.StrategyName)
	account.StrategyVersion = strings.TrimSpace(account.StrategyVersion)
	account.StartDate = strings.TrimSpace(account.StartDate)
	account.EndDate = strings.TrimSpace(account.EndDate)
	account.InputFingerprint = strings.TrimSpace(account.InputFingerprint)
	account.PolicyVersion = strings.TrimSpace(account.PolicyVersion)
	account.ApprovalMode = strings.TrimSpace(account.ApprovalMode)
	account.SpecPath = strings.TrimSpace(account.SpecPath)
	account.SpecSHA256 = strings.TrimSpace(account.SpecSHA256)
	account.EngineVersion = strings.TrimSpace(account.EngineVersion)
	account.Status = strings.TrimSpace(account.Status)
	if account.SchemaVersion == "" {
		account.SchemaVersion = PaperSchemaVersion
	}
	if _, err := requireResearchUUID(account.AccountID, "account.account_id", false); err != nil {
		return PaperAccount{}, err
	}
	if account.SchemaVersion != PaperSchemaVersion {
		return PaperAccount{}, fmt.Errorf("account.schema_version %q is unsupported", account.SchemaVersion)
	}
	if account.Name == "" {
		return PaperAccount{}, errors.New("account.name is required")
	}
	if !paperCurrencyPattern.MatchString(account.BaseCurrency) {
		return PaperAccount{}, fmt.Errorf("account.base_currency %q is invalid", account.BaseCurrency)
	}
	if !paperIdentifierPattern.MatchString(account.StrategyName) {
		return PaperAccount{}, fmt.Errorf("account.strategy_name %q is invalid", account.StrategyName)
	}
	if !paperVersionPattern.MatchString(account.StrategyVersion) {
		return PaperAccount{}, fmt.Errorf("account.strategy_version %q is invalid", account.StrategyVersion)
	}
	parameters, err := normalizePaperObject(account.StrategyParameters, "account.strategy_parameters")
	if err != nil {
		return PaperAccount{}, err
	}
	account.StrategyParameters = parameters
	for _, item := range []struct{ value, label string }{{account.StartDate, "account.start_date"}, {account.EndDate, "account.end_date"}} {
		if !paperDatePattern.MatchString(item.value) {
			return PaperAccount{}, fmt.Errorf("%s must be an ISO date", item.label)
		}
		if _, err := time.Parse("2006-01-02", item.value); err != nil {
			return PaperAccount{}, fmt.Errorf("%s must be an ISO date: %w", item.label, err)
		}
	}
	if account.StartDate > account.EndDate {
		return PaperAccount{}, errors.New("account.start_date must not be after account.end_date")
	}
	if err := requireResearchHash(account.InputFingerprint, "account.input_fingerprint"); err != nil {
		return PaperAccount{}, err
	}
	if !paperVersionPattern.MatchString(account.PolicyVersion) {
		return PaperAccount{}, fmt.Errorf("account.policy_version %q is invalid", account.PolicyVersion)
	}
	if account.ApprovalMode != "manual" && account.ApprovalMode != "auto" {
		return PaperAccount{}, fmt.Errorf("account.approval_mode %q is unsupported", account.ApprovalMode)
	}
	if err := requireBacktestPath(account.SpecPath, "account.spec_path"); err != nil {
		return PaperAccount{}, err
	}
	if err := requireResearchHash(account.SpecSHA256, "account.spec_sha256"); err != nil {
		return PaperAccount{}, err
	}
	if account.EngineVersion == "" {
		return PaperAccount{}, errors.New("account.engine_version is required")
	}
	if account.Status == "" {
		account.Status = "active"
	}
	if account.Status != "active" && account.Status != "halted" && account.Status != "closed" {
		return PaperAccount{}, fmt.Errorf("account.status %q is unsupported", account.Status)
	}
	if account.CreatedAt.IsZero() {
		account.CreatedAt = time.Now().UTC()
	} else {
		account.CreatedAt = account.CreatedAt.UTC()
	}
	if !account.RegisteredAt.IsZero() {
		account.RegisteredAt = account.RegisteredAt.UTC()
	}
	return account, nil
}

type paperAccountIdentity struct {
	AccountID          string          `json:"account_id"`
	SchemaVersion      string          `json:"schema_version"`
	Name               string          `json:"name"`
	BaseCurrency       string          `json:"base_currency"`
	StrategyName       string          `json:"strategy_name"`
	StrategyVersion    string          `json:"strategy_version"`
	StrategyParameters json.RawMessage `json:"strategy_parameters"`
	StartDate          string          `json:"start_date"`
	EndDate            string          `json:"end_date"`
	InputFingerprint   string          `json:"input_fingerprint"`
	PolicyVersion      string          `json:"policy_version"`
	ApprovalMode       string          `json:"approval_mode"`
	SpecPath           string          `json:"spec_path"`
	SpecSHA256         string          `json:"spec_sha256"`
	EngineVersion      string          `json:"engine_version"`
	Status             string          `json:"status"`
}

func paperAccountRegistrationSHA256(account PaperAccount) (string, error) {
	identity := paperAccountIdentity{
		AccountID: account.AccountID, SchemaVersion: account.SchemaVersion, Name: account.Name,
		BaseCurrency: account.BaseCurrency, StrategyName: account.StrategyName,
		StrategyVersion: account.StrategyVersion, StrategyParameters: account.StrategyParameters,
		StartDate: account.StartDate, EndDate: account.EndDate, InputFingerprint: account.InputFingerprint,
		PolicyVersion: account.PolicyVersion, ApprovalMode: account.ApprovalMode, SpecPath: account.SpecPath,
		SpecSHA256: account.SpecSHA256, EngineVersion: account.EngineVersion, Status: account.Status,
	}
	return canonicalBacktestHash(identity, "paper account registration")
}

func (registration PaperAccountRegistration) normalized() (PaperAccountRegistration, error) {
	account, err := normalizePaperAccount(registration.Account)
	if err != nil {
		return PaperAccountRegistration{}, err
	}
	return PaperAccountRegistration{Account: account}, nil
}

func normalizePaperEvent(event PaperAccountEvent) (PaperAccountEvent, error) {
	event.AccountID = strings.TrimSpace(event.AccountID)
	event.EventID = strings.TrimSpace(event.EventID)
	event.IdempotencyKey = strings.TrimSpace(event.IdempotencyKey)
	event.SessionDate = strings.TrimSpace(event.SessionDate)
	event.EventType = strings.TrimSpace(event.EventType)
	event.Currency = strings.TrimSpace(event.Currency)
	for _, field := range map[string]*string{
		"decision_id": &event.DecisionID, "target_id": &event.TargetID,
		"order_id": &event.OrderID, "security_id": &event.SecurityID,
	} {
		*field = strings.TrimSpace(*field)
	}
	if _, err := requireResearchUUID(event.AccountID, "event.account_id", false); err != nil {
		return PaperAccountEvent{}, err
	}
	if _, err := requireResearchUUID(event.EventID, "event.event_id", false); err != nil {
		return PaperAccountEvent{}, err
	}
	if event.IdempotencyKey == "" {
		return PaperAccountEvent{}, errors.New("event.idempotency_key is required")
	}
	if event.Sequence <= 0 {
		return PaperAccountEvent{}, errors.New("event.sequence must be positive")
	}
	if event.EventAt.IsZero() {
		return PaperAccountEvent{}, errors.New("event.event_at is required")
	}
	event.EventAt = event.EventAt.UTC()
	if !paperDatePattern.MatchString(event.SessionDate) {
		return PaperAccountEvent{}, errors.New("event.session_date must be an ISO date")
	}
	if _, err := time.Parse("2006-01-02", event.SessionDate); err != nil {
		return PaperAccountEvent{}, fmt.Errorf("event.session_date is invalid: %w", err)
	}
	if _, ok := map[string]struct{}{
		"cash_deposit": {}, "decision": {}, "target_published": {}, "risk_approved": {},
		"risk_rejected": {}, "halt": {}, "no_op": {}, "approval": {}, "manual_rejection": {},
		"order": {}, "fill": {}, "fee": {}, "tax": {}, "dividend": {}, "split": {},
		"delisting": {}, "valuation": {}, "reconciliation": {}, "fx_conversion": {},
	}[event.EventType]; !ok {
		return PaperAccountEvent{}, fmt.Errorf("event.event_type %q is unsupported", event.EventType)
	}
	if !paperCurrencyPattern.MatchString(event.Currency) {
		return PaperAccountEvent{}, fmt.Errorf("event.currency %q is invalid", event.Currency)
	}
	for _, item := range []struct{ value, label string }{
		{event.QuantityDelta, "event.quantity_delta"}, {event.AmountLocalDelta, "event.amount_local_delta"}, {event.AmountBaseDelta, "event.amount_base_delta"},
	} {
		if !paperDecimalPattern.MatchString(item.value) {
			return PaperAccountEvent{}, fmt.Errorf("%s must be a canonical decimal", item.label)
		}
	}
	for _, item := range []struct{ value, label string }{{event.DecisionID, "event.decision_id"}, {event.TargetID, "event.target_id"}, {event.OrderID, "event.order_id"}, {event.SecurityID, "event.security_id"}} {
		if item.value != "" {
			if _, err := requireResearchUUID(item.value, item.label, false); err != nil {
				return PaperAccountEvent{}, err
			}
		}
	}
	for _, item := range []struct {
		value *string
		label string
	}{{event.NavBase, "event.nav_base"}, {event.CashBase, "event.cash_base"}, {event.PositionsValueBase, "event.positions_value_base"}} {
		if item.value != nil && !regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?$`).MatchString(*item.value) {
			return PaperAccountEvent{}, fmt.Errorf("%s must be a canonical non-negative decimal", item.label)
		}
	}
	details, err := normalizePaperObject(event.Details, "event.details")
	if err != nil {
		return PaperAccountEvent{}, err
	}
	event.Details = details
	recordHash, err := paperAccountEventRecordHash(event)
	if err != nil {
		return PaperAccountEvent{}, err
	}
	if event.RecordHash != "" && event.RecordHash != recordHash {
		return PaperAccountEvent{}, errors.New("event.record_hash does not match event payload")
	}
	event.RecordHash = recordHash
	return event, nil
}

type paperAccountEventIdentity struct {
	AccountID          string          `json:"account_id"`
	Sequence           int64           `json:"sequence"`
	EventID            string          `json:"event_id"`
	IdempotencyKey     string          `json:"idempotency_key"`
	EventAt            time.Time       `json:"event_at"`
	SessionDate        string          `json:"session_date"`
	EventType          string          `json:"event_type"`
	DecisionID         string          `json:"decision_id,omitempty"`
	TargetID           string          `json:"target_id,omitempty"`
	OrderID            string          `json:"order_id,omitempty"`
	SecurityID         string          `json:"security_id,omitempty"`
	Currency           string          `json:"currency"`
	QuantityDelta      string          `json:"quantity_delta"`
	AmountLocalDelta   string          `json:"amount_local_delta"`
	AmountBaseDelta    string          `json:"amount_base_delta"`
	NavBase            *string         `json:"nav_base,omitempty"`
	CashBase           *string         `json:"cash_base,omitempty"`
	PositionsValueBase *string         `json:"positions_value_base,omitempty"`
	Details            json.RawMessage `json:"details"`
}

func paperAccountEventRecordHash(event PaperAccountEvent) (string, error) {
	return canonicalBacktestHash(paperAccountEventIdentity{
		AccountID: event.AccountID, Sequence: event.Sequence, EventID: event.EventID,
		IdempotencyKey: event.IdempotencyKey, EventAt: event.EventAt.UTC(), SessionDate: event.SessionDate,
		EventType: event.EventType, DecisionID: event.DecisionID, TargetID: event.TargetID,
		OrderID: event.OrderID, SecurityID: event.SecurityID, Currency: event.Currency,
		QuantityDelta: event.QuantityDelta, AmountLocalDelta: event.AmountLocalDelta,
		AmountBaseDelta: event.AmountBaseDelta, NavBase: event.NavBase, CashBase: event.CashBase,
		PositionsValueBase: event.PositionsValueBase, Details: event.Details,
	}, "paper account event")
}

func (r *Repository) RegisterPaperAccount(ctx context.Context, registration PaperAccountRegistration) (PaperAccountRegistrationResult, error) {
	if err := requireResearchRepository(r); err != nil {
		return PaperAccountRegistrationResult{}, err
	}
	normalized, err := registration.normalized()
	if err != nil {
		return PaperAccountRegistrationResult{}, err
	}
	registrationHash, err := paperAccountRegistrationSHA256(normalized.Account)
	if err != nil {
		return PaperAccountRegistrationResult{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return PaperAccountRegistrationResult{}, err
	}
	defer tx.Rollback(ctx)
	var insertedHash string
	err = tx.QueryRow(ctx, insertPaperAccountSQL,
		normalized.Account.AccountID, normalized.Account.SchemaVersion, normalized.Account.Name,
		normalized.Account.BaseCurrency, normalized.Account.StrategyName, normalized.Account.StrategyVersion,
		normalized.Account.StrategyParameters, normalized.Account.StartDate, normalized.Account.EndDate,
		normalized.Account.InputFingerprint, normalized.Account.PolicyVersion, normalized.Account.ApprovalMode,
		normalized.Account.SpecPath, normalized.Account.SpecSHA256, normalized.Account.EngineVersion,
		normalized.Account.Status, normalized.Account.CreatedAt, registrationHash,
	).Scan(&insertedHash)
	if err == nil {
		if insertedHash != registrationHash {
			return PaperAccountRegistrationResult{}, errors.New("database returned an unexpected paper account registration hash")
		}
		if err := tx.Commit(ctx); err != nil {
			return PaperAccountRegistrationResult{}, err
		}
		return PaperAccountRegistrationResult{AccountID: normalized.Account.AccountID, RegistrationSHA256: registrationHash}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PaperAccountRegistrationResult{}, err
	}
	var existingHash string
	if err := tx.QueryRow(ctx, `SELECT registration_sha256 FROM paper_accounts WHERE account_id = $1::uuid`, normalized.Account.AccountID).Scan(&existingHash); err != nil {
		return PaperAccountRegistrationResult{}, err
	}
	if existingHash != registrationHash {
		return PaperAccountRegistrationResult{}, fmt.Errorf("paper account %s conflicts with existing registration", normalized.Account.AccountID)
	}
	if err := tx.Commit(ctx); err != nil {
		return PaperAccountRegistrationResult{}, err
	}
	return PaperAccountRegistrationResult{AccountID: normalized.Account.AccountID, RegistrationSHA256: registrationHash, AlreadyPresent: true}, nil
}

func (r *Repository) AppendPaperAccountEvent(ctx context.Context, event PaperAccountEvent) (PaperAccountEventResult, error) {
	if err := requireResearchRepository(r); err != nil {
		return PaperAccountEventResult{}, err
	}
	normalized, err := normalizePaperEvent(event)
	if err != nil {
		return PaperAccountEventResult{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return PaperAccountEventResult{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "paper-account:"+normalized.AccountID); err != nil {
		return PaperAccountEventResult{}, fmt.Errorf("lock paper account event stream: %w", err)
	}
	var existingID, existingHash string
	var existingSequence int64
	err = tx.QueryRow(ctx, `SELECT event_id::text, sequence, record_hash FROM paper_account_events WHERE account_id = $1::uuid AND idempotency_key = $2`, normalized.AccountID, normalized.IdempotencyKey).Scan(&existingID, &existingSequence, &existingHash)
	if err == nil {
		if existingID != normalized.EventID || existingHash != normalized.RecordHash || existingSequence != normalized.Sequence {
			return PaperAccountEventResult{}, fmt.Errorf("paper event %s conflicts with existing idempotency key", normalized.IdempotencyKey)
		}
		if err := tx.Commit(ctx); err != nil {
			return PaperAccountEventResult{}, err
		}
		return PaperAccountEventResult{AccountID: normalized.AccountID, Sequence: normalized.Sequence, EventID: normalized.EventID, RecordHash: normalized.RecordHash, AlreadyPresent: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PaperAccountEventResult{}, err
	}
	var insertedHash string
	err = tx.QueryRow(ctx, insertPaperAccountEventSQL,
		normalized.AccountID, normalized.Sequence, normalized.EventID, normalized.IdempotencyKey,
		normalized.EventAt, normalized.SessionDate, normalized.EventType, normalized.DecisionID,
		normalized.TargetID, normalized.OrderID, normalized.SecurityID, normalized.Currency,
		normalized.QuantityDelta, normalized.AmountLocalDelta, normalized.AmountBaseDelta,
		optionalPaperDecimal(normalized.NavBase), optionalPaperDecimal(normalized.CashBase),
		optionalPaperDecimal(normalized.PositionsValueBase), normalized.Details, normalized.RecordHash,
	).Scan(&insertedHash)
	if err != nil {
		return PaperAccountEventResult{}, fmt.Errorf("insert paper account event: %w", err)
	}
	if insertedHash != normalized.RecordHash {
		return PaperAccountEventResult{}, errors.New("database returned an unexpected paper event hash")
	}
	if err := tx.Commit(ctx); err != nil {
		return PaperAccountEventResult{}, err
	}
	return PaperAccountEventResult{AccountID: normalized.AccountID, Sequence: normalized.Sequence, EventID: normalized.EventID, RecordHash: normalized.RecordHash}, nil
}

type PaperAccountEventResult struct {
	AccountID      string `json:"account_id"`
	Sequence       int64  `json:"sequence"`
	EventID        string `json:"event_id"`
	RecordHash     string `json:"record_hash"`
	AlreadyPresent bool   `json:"already_present"`
}

func optionalPaperDecimal(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func scanPaperAccount(row pgx.Row) (PaperAccount, error) {
	var account PaperAccount
	var parameters []byte
	if err := row.Scan(
		&account.AccountID, &account.SchemaVersion, &account.Name, &account.BaseCurrency,
		&account.StrategyName, &account.StrategyVersion, &parameters, &account.StartDate,
		&account.EndDate, &account.InputFingerprint, &account.PolicyVersion, &account.ApprovalMode,
		&account.SpecPath, &account.SpecSHA256, &account.EngineVersion, &account.Status,
		&account.CreatedAt, &account.RegisteredAt, &account.RegistrationSHA256,
	); err != nil {
		return PaperAccount{}, err
	}
	account.StrategyParameters = append(json.RawMessage(nil), parameters...)
	return account, nil
}

func (r *Repository) GetPaperAccountReport(ctx context.Context, accountID string) (PaperAccountReport, error) {
	if err := requireResearchRepository(r); err != nil {
		return PaperAccountReport{}, err
	}
	if _, err := requireResearchUUID(accountID, "account_id", false); err != nil {
		return PaperAccountReport{}, err
	}
	account, err := scanPaperAccount(r.pool.QueryRow(ctx, `
SELECT account_id::text, schema_version, name, base_currency, strategy_name,
       strategy_version, strategy_parameters, start_date::text, end_date::text,
       input_fingerprint, policy_version, approval_mode, spec_path, spec_sha256,
       engine_version, status, created_at, registered_at, registration_sha256
FROM paper_accounts
WHERE account_id = $1::uuid`, accountID))
	if err != nil {
		return PaperAccountReport{}, fmt.Errorf("load paper account %s: %w", accountID, err)
	}
	var report PaperAccountReport
	report.Account = account
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM paper_account_events WHERE account_id = $1::uuid`, accountID).Scan(&report.EventCount); err != nil {
		return PaperAccountReport{}, fmt.Errorf("count paper account events: %w", err)
	}
	var event PaperAccountEvent
	var details []byte
	var decisionID, targetID, orderID, securityID string
	var navBase, cashBase, positionsValueBase *string
	err = r.pool.QueryRow(ctx, `
SELECT account_id::text, sequence, event_id::text, idempotency_key, event_at,
       session_date::text, event_type, coalesce(decision_id::text, ''),
       coalesce(target_id::text, ''), coalesce(order_id::text, ''),
       coalesce(security_id::text, ''), currency, quantity_delta::text,
       amount_local_delta::text, amount_base_delta::text, nav_base::text,
       cash_base::text, positions_value_base::text, details, record_hash
FROM paper_account_events
WHERE account_id = $1::uuid
ORDER BY sequence DESC
LIMIT 1`, accountID).Scan(
		&event.AccountID, &event.Sequence, &event.EventID, &event.IdempotencyKey, &event.EventAt,
		&event.SessionDate, &event.EventType, &decisionID, &targetID, &orderID, &securityID,
		&event.Currency, &event.QuantityDelta, &event.AmountLocalDelta, &event.AmountBaseDelta,
		&navBase, &cashBase, &positionsValueBase, &details, &event.RecordHash,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return report, nil
	}
	if err != nil {
		return PaperAccountReport{}, fmt.Errorf("load latest paper account event: %w", err)
	}
	event.DecisionID, event.TargetID, event.OrderID, event.SecurityID = decisionID, targetID, orderID, securityID
	event.NavBase, event.CashBase, event.PositionsValueBase = navBase, cashBase, positionsValueBase
	event.Details = append(json.RawMessage(nil), details...)
	report.LastEvent = &event
	return report, nil
}
