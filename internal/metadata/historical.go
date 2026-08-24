package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const HistoricalSchemaVersion = "1.0.0"

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// SecurityIdentifierVersion is a source-backed identifier assertion. Validity
// describes the market assignment; AvailableAt describes when this exact
// version became eligible for historical research.
type SecurityIdentifierVersion struct {
	SchemaVersion   string     `json:"schema_version"`
	ID              string     `json:"id"`
	SecurityID      string     `json:"security_id"`
	IdentifierType  string     `json:"identifier_type"`
	Value           string     `json:"value"`
	NormalizedValue string     `json:"normalized_value"`
	IdentifierScope string     `json:"identifier_scope"`
	ValidFrom       time.Time  `json:"valid_from"`
	ValidUntil      *time.Time `json:"valid_until"`
	AvailableAt     time.Time  `json:"available_at"`
	SourceReference string     `json:"source_reference"`
	RecordedAt      time.Time  `json:"recorded_at"`
	DataSourceID    string     `json:"data_source_id"`
	RawPayloadHash  string     `json:"raw_payload_hash"`
	Revision        int        `json:"revision"`
	IsPrimary       bool       `json:"is_primary"`
}

// SecurityListingVersion records a security's issuer, MIC, currency, and
// primary-listing state over one source-backed validity interval.
type SecurityListingVersion struct {
	SchemaVersion   string     `json:"schema_version"`
	ID              string     `json:"id"`
	SecurityID      string     `json:"security_id"`
	IssuerID        *string    `json:"issuer_id"`
	Exchange        string     `json:"exchange"`
	MIC             string     `json:"mic"`
	Currency        string     `json:"currency"`
	PrimaryListing  bool       `json:"primary_listing"`
	ValidFrom       time.Time  `json:"valid_from"`
	ValidUntil      *time.Time `json:"valid_until"`
	AvailableAt     time.Time  `json:"available_at"`
	SourceReference string     `json:"source_reference"`
	RecordedAt      time.Time  `json:"recorded_at"`
	DataSourceID    string     `json:"data_source_id"`
	RawPayloadHash  string     `json:"raw_payload_hash"`
	Revision        int        `json:"revision"`
}

// UniverseMembership is an append-only positive or corrective membership
// assertion. Member=false represents a removal/correction without deleting the
// earlier knowledge vintage.
type UniverseMembership struct {
	SchemaVersion   string     `json:"schema_version"`
	ID              string     `json:"id"`
	UniverseID      string     `json:"universe_id"`
	SecurityID      string     `json:"security_id"`
	Member          bool       `json:"member"`
	ValidFrom       time.Time  `json:"valid_from"`
	ValidUntil      *time.Time `json:"valid_until"`
	AnnouncedAt     *time.Time `json:"announced_at"`
	AvailableAt     time.Time  `json:"available_at"`
	SourceReference string     `json:"source_reference"`
	RecordedAt      time.Time  `json:"recorded_at"`
	DataSourceID    string     `json:"data_source_id"`
	RawPayloadHash  string     `json:"raw_payload_hash"`
	Revision        int        `json:"revision"`
}

// CalendarManifest pins the source evidence and deterministic fingerprint for
// one MIC-scoped calendar version.
type CalendarManifest struct {
	SchemaVersion      string    `json:"schema_version"`
	ID                 string    `json:"id"`
	CalendarVersion    string    `json:"calendar_version"`
	MIC                string    `json:"mic"`
	ExchangeTimezone   string    `json:"exchange_timezone"`
	AvailableAt        time.Time `json:"available_at"`
	SourceReference    string    `json:"source_reference"`
	RecordedAt         time.Time `json:"recorded_at"`
	DataSourceID       string    `json:"data_source_id"`
	RawPayloadHash     string    `json:"raw_payload_hash"`
	SessionFingerprint string    `json:"session_fingerprint"`
	SessionCount       int       `json:"session_count"`
}

// TradingSession is one explicit open or closed exchange-local session date.
// OpenAt/CloseAt are nil for a closed date and UTC instants for an open date.
type TradingSession struct {
	SchemaVersion    string     `json:"schema_version"`
	ID               string     `json:"id"`
	CalendarVersion  string     `json:"calendar_version"`
	MIC              string     `json:"mic"`
	ExchangeTimezone string     `json:"exchange_timezone"`
	SessionDate      string     `json:"session_date"`
	SessionStatus    string     `json:"session_status"`
	OpenAt           *time.Time `json:"open_at"`
	CloseAt          *time.Time `json:"close_at"`
	IsEarlyClose     bool       `json:"is_early_close"`
	AvailableAt      time.Time  `json:"available_at"`
	SourceReference  string     `json:"source_reference"`
	RecordedAt       time.Time  `json:"recorded_at"`
	DataSourceID     string     `json:"data_source_id"`
	RawPayloadHash   string     `json:"raw_payload_hash"`
	Revision         int        `json:"revision"`
}

// HistoricalTruthBatch is one atomic publication unit. Every row is immutable;
// replaying identical IDs is a no-op while changing an existing ID is a conflict.
type HistoricalTruthBatch struct {
	Identifiers []SecurityIdentifierVersion
	Listings    []SecurityListingVersion
	Memberships []UniverseMembership
	Calendars   []CalendarManifest
	Sessions    []TradingSession
}

const insertSecurityIdentifierVersionSQL = `
INSERT INTO security_identifier_versions (
	id, schema_version, security_id, identifier_type, value, normalized_value,
	identifier_scope, valid_from, valid_until, available_at, source_reference,
	recorded_at, data_source_id, raw_payload_hash, revision, is_primary, record_hash
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT (id) DO NOTHING
RETURNING id::text`

const insertSecurityListingVersionSQL = `
INSERT INTO security_listing_versions (
	id, schema_version, security_id, issuer_id, exchange, mic, currency,
	primary_listing, valid_from, valid_until, available_at, source_reference,
	recorded_at, data_source_id, raw_payload_hash, revision, record_hash
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT (id) DO NOTHING
RETURNING id::text`

const insertUniverseMembershipSQL = `
INSERT INTO universe_memberships (
	id, schema_version, universe_id, security_id, member, valid_from, valid_until,
	announced_at, available_at, source_reference, recorded_at, data_source_id,
	raw_payload_hash, revision, record_hash
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
ON CONFLICT (id) DO NOTHING
RETURNING id::text`

const insertCalendarManifestSQL = `
INSERT INTO calendar_manifests (
	id, schema_version, calendar_version, mic, exchange_timezone, available_at,
	source_reference, recorded_at, data_source_id, raw_payload_hash,
	session_fingerprint, session_count, record_hash
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (id) DO NOTHING
RETURNING id::text`

const insertTradingSessionSQL = `
INSERT INTO trading_sessions (
	id, schema_version, calendar_version, mic, exchange_timezone, session_date,
	session_status, open_at, close_at, is_early_close, available_at,
	source_reference, recorded_at, data_source_id, raw_payload_hash, revision,
	record_hash
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT (id) DO NOTHING
RETURNING id::text`

const selectSecurityIdentifierHashSQL = `
SELECT record_hash FROM security_identifier_versions WHERE id=$1`

const selectSecurityListingHashSQL = `
SELECT record_hash FROM security_listing_versions WHERE id=$1`

const selectUniverseMembershipHashSQL = `
SELECT record_hash FROM universe_memberships WHERE id=$1`

const selectCalendarManifestHashSQL = `
SELECT record_hash FROM calendar_manifests WHERE id=$1`

const selectTradingSessionHashSQL = `
SELECT record_hash FROM trading_sessions WHERE id=$1`

const resolveSecurityIdentifierSQL = `
SELECT id::text, schema_version, security_id::text, identifier_type, value,
       normalized_value, identifier_scope, valid_from, valid_until, available_at,
       source_reference, recorded_at, data_source_id::text, raw_payload_hash,
       revision, is_primary
FROM security_identifier_versions
WHERE data_source_id=$1::uuid
  AND identifier_type=$2
  AND normalized_value=$3
  AND identifier_scope=$4
  AND valid_from <= $5::timestamptz
  AND (valid_until IS NULL OR $5::timestamptz < valid_until)
  AND available_at <= $6::timestamptz
ORDER BY available_at DESC, revision DESC, recorded_at DESC, id DESC
LIMIT 2`

const resolveSecurityListingSQL = `
SELECT id::text, schema_version, security_id::text, issuer_id::text, exchange, mic,
       currency, primary_listing, valid_from, valid_until, available_at,
       source_reference, recorded_at, data_source_id::text, raw_payload_hash, revision
FROM security_listing_versions
WHERE data_source_id=$1::uuid
  AND security_id=$2::uuid
  AND mic=$3
  AND valid_from <= $4::timestamptz
  AND (valid_until IS NULL OR $4::timestamptz < valid_until)
  AND available_at <= $5::timestamptz
ORDER BY available_at DESC, revision DESC, recorded_at DESC, id DESC
LIMIT 2`

const resolveUniverseSQL = `
SELECT id::text, schema_version, universe_id, security_id::text, member,
       valid_from, valid_until, announced_at, available_at, source_reference,
       recorded_at, data_source_id::text, raw_payload_hash, revision
FROM universe_memberships
WHERE data_source_id=$1::uuid
  AND universe_id=$2
  AND valid_from <= $3::timestamptz
  AND (valid_until IS NULL OR $3::timestamptz < valid_until)
  AND available_at <= $4::timestamptz
ORDER BY security_id, available_at DESC, revision DESC, recorded_at DESC, id DESC`

const tradingSessionAtSQL = `
SELECT id::text, schema_version, calendar_version, mic, exchange_timezone,
       session_date::text, session_status, open_at, close_at, is_early_close,
       available_at, source_reference, recorded_at, data_source_id::text,
       raw_payload_hash, revision
FROM trading_sessions
WHERE data_source_id=$1::uuid
  AND mic=$2
  AND calendar_version=$3
  AND session_status='open'
  AND open_at <= $4::timestamptz
  AND $4::timestamptz < close_at
  AND available_at <= $5::timestamptz
ORDER BY available_at DESC, revision DESC, recorded_at DESC, id DESC
LIMIT 2`

const nextTradingSessionSQL = `
SELECT id::text, schema_version, calendar_version, mic, exchange_timezone,
       session_date::text, session_status, open_at, close_at, is_early_close,
       available_at, source_reference, recorded_at, data_source_id::text,
       raw_payload_hash, revision
FROM trading_sessions
WHERE data_source_id=$1::uuid
  AND mic=$2
  AND calendar_version=$3
  AND session_status='open'
  AND open_at > $4::timestamptz
  AND available_at <= $5::timestamptz
ORDER BY open_at, available_at DESC, revision DESC, recorded_at DESC, id DESC`

func canonicalRecordHash(record any) (string, error) {
	record = canonicalHistoricalRecord(record)
	encoded, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("canonicalize historical record: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func canonicalHistoricalRecord(record any) any {
	switch value := record.(type) {
	case SecurityIdentifierVersion:
		value.ValidFrom = value.ValidFrom.UTC()
		value.ValidUntil = canonicalTimePointer(value.ValidUntil)
		value.AvailableAt = value.AvailableAt.UTC()
		value.RecordedAt = value.RecordedAt.UTC()
		return value
	case SecurityListingVersion:
		value.ValidFrom = value.ValidFrom.UTC()
		value.ValidUntil = canonicalTimePointer(value.ValidUntil)
		value.AvailableAt = value.AvailableAt.UTC()
		value.RecordedAt = value.RecordedAt.UTC()
		return value
	case UniverseMembership:
		value.ValidFrom = value.ValidFrom.UTC()
		value.ValidUntil = canonicalTimePointer(value.ValidUntil)
		value.AnnouncedAt = canonicalTimePointer(value.AnnouncedAt)
		value.AvailableAt = value.AvailableAt.UTC()
		value.RecordedAt = value.RecordedAt.UTC()
		return value
	case CalendarManifest:
		value.AvailableAt = value.AvailableAt.UTC()
		value.RecordedAt = value.RecordedAt.UTC()
		return value
	case TradingSession:
		value.OpenAt = canonicalTimePointer(value.OpenAt)
		value.CloseAt = canonicalTimePointer(value.CloseAt)
		value.AvailableAt = value.AvailableAt.UTC()
		value.RecordedAt = value.RecordedAt.UTC()
		return value
	default:
		return record
	}
}

func canonicalTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	canonical := value.UTC()
	return &canonical
}

func validateCommonHistoricalRecord(schemaVersion, id, dataSourceID, sourceReference, rawHash string, revision int, availableAt, recordedAt time.Time) error {
	if schemaVersion != HistoricalSchemaVersion {
		return fmt.Errorf("unsupported historical schema version %q", schemaVersion)
	}
	for name, value := range map[string]string{
		"id": id, "data_source_id": dataSourceID, "source_reference": sourceReference,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if !sha256Pattern.MatchString(rawHash) {
		return errors.New("raw_payload_hash must be a lowercase SHA-256")
	}
	if revision < 0 {
		return errors.New("revision must be non-negative")
	}
	if availableAt.IsZero() || recordedAt.IsZero() {
		return errors.New("available_at and recorded_at are required")
	}
	if recordedAt.Before(availableAt) {
		return errors.New("recorded_at must not precede available_at")
	}
	return nil
}

func validateInterval(validFrom time.Time, validUntil *time.Time) error {
	if validFrom.IsZero() {
		return errors.New("valid_from is required")
	}
	if validUntil != nil && !validUntil.After(validFrom) {
		return errors.New("valid_until must be after valid_from")
	}
	return nil
}

func validateIdentifier(record SecurityIdentifierVersion) error {
	if err := validateCommonHistoricalRecord(record.SchemaVersion, record.ID, record.DataSourceID, record.SourceReference, record.RawPayloadHash, record.Revision, record.AvailableAt, record.RecordedAt); err != nil {
		return err
	}
	if err := validateInterval(record.ValidFrom, record.ValidUntil); err != nil {
		return err
	}
	if strings.TrimSpace(record.SecurityID) == "" || strings.TrimSpace(record.IdentifierType) == "" || strings.TrimSpace(record.IdentifierScope) == "" || strings.TrimSpace(record.Value) == "" || strings.TrimSpace(record.NormalizedValue) == "" {
		return errors.New("identifier identity fields are required")
	}
	return nil
}

func validateListing(record SecurityListingVersion) error {
	if err := validateCommonHistoricalRecord(record.SchemaVersion, record.ID, record.DataSourceID, record.SourceReference, record.RawPayloadHash, record.Revision, record.AvailableAt, record.RecordedAt); err != nil {
		return err
	}
	if err := validateInterval(record.ValidFrom, record.ValidUntil); err != nil {
		return err
	}
	if strings.TrimSpace(record.SecurityID) == "" || strings.TrimSpace(record.Exchange) == "" || strings.TrimSpace(record.MIC) == "" || strings.TrimSpace(record.Currency) == "" {
		return errors.New("listing identity fields are required")
	}
	return nil
}

func validateMembership(record UniverseMembership) error {
	if err := validateCommonHistoricalRecord(record.SchemaVersion, record.ID, record.DataSourceID, record.SourceReference, record.RawPayloadHash, record.Revision, record.AvailableAt, record.RecordedAt); err != nil {
		return err
	}
	if err := validateInterval(record.ValidFrom, record.ValidUntil); err != nil {
		return err
	}
	if strings.TrimSpace(record.UniverseID) == "" || strings.TrimSpace(record.SecurityID) == "" {
		return errors.New("membership identity fields are required")
	}
	if record.AnnouncedAt != nil && record.AnnouncedAt.After(record.AvailableAt) {
		return errors.New("announced_at must not follow available_at")
	}
	return nil
}

func validateCalendar(record CalendarManifest) error {
	if err := validateCommonHistoricalRecord(record.SchemaVersion, record.ID, record.DataSourceID, record.SourceReference, record.RawPayloadHash, 0, record.AvailableAt, record.RecordedAt); err != nil {
		return err
	}
	if strings.TrimSpace(record.CalendarVersion) == "" || strings.TrimSpace(record.MIC) == "" || strings.TrimSpace(record.ExchangeTimezone) == "" {
		return errors.New("calendar identity fields are required")
	}
	if _, err := time.LoadLocation(record.ExchangeTimezone); err != nil {
		return fmt.Errorf("exchange_timezone is not an IANA timezone: %w", err)
	}
	if record.SessionCount < 0 || !sha256Pattern.MatchString(record.SessionFingerprint) {
		return errors.New("calendar session count or fingerprint is invalid")
	}
	return nil
}

func validateSession(record TradingSession) error {
	if err := validateCommonHistoricalRecord(record.SchemaVersion, record.ID, record.DataSourceID, record.SourceReference, record.RawPayloadHash, record.Revision, record.AvailableAt, record.RecordedAt); err != nil {
		return err
	}
	if strings.TrimSpace(record.CalendarVersion) == "" || strings.TrimSpace(record.MIC) == "" || strings.TrimSpace(record.ExchangeTimezone) == "" || strings.TrimSpace(record.SessionDate) == "" {
		return errors.New("session identity fields are required")
	}
	if _, err := time.LoadLocation(record.ExchangeTimezone); err != nil {
		return fmt.Errorf("exchange_timezone is not an IANA timezone: %w", err)
	}
	if _, err := time.Parse(time.DateOnly, record.SessionDate); err != nil {
		return fmt.Errorf("session_date must be YYYY-MM-DD: %w", err)
	}
	switch record.SessionStatus {
	case "open":
		if record.OpenAt == nil || record.CloseAt == nil || !record.CloseAt.After(*record.OpenAt) {
			return errors.New("open session requires open_at before close_at")
		}
	case "closed":
		if record.OpenAt != nil || record.CloseAt != nil || record.IsEarlyClose {
			return errors.New("closed session cannot have open/close instants or early-close status")
		}
	default:
		return fmt.Errorf("unsupported session status %q", record.SessionStatus)
	}
	return nil
}

type calendarKey struct {
	DataSourceID    string
	MIC             string
	CalendarVersion string
}

type fingerprintSession struct {
	CalendarVersion  string     `json:"calendar_version"`
	MIC              string     `json:"mic"`
	ExchangeTimezone string     `json:"exchange_timezone"`
	SessionDate      string     `json:"session_date"`
	SessionStatus    string     `json:"session_status"`
	OpenAt           *time.Time `json:"open_at"`
	CloseAt          *time.Time `json:"close_at"`
	IsEarlyClose     bool       `json:"is_early_close"`
}

func keyForCalendar(dataSourceID, mic, version string) calendarKey {
	return calendarKey{DataSourceID: dataSourceID, MIC: mic, CalendarVersion: version}
}

// CalendarSessionFingerprint returns the canonical semantic fingerprint used
// by calendar manifests and publication validation.
func CalendarSessionFingerprint(records []TradingSession) (string, error) {
	canonical := make([]fingerprintSession, len(records))
	for i, record := range records {
		canonical[i] = fingerprintSession{
			CalendarVersion:  record.CalendarVersion,
			MIC:              record.MIC,
			ExchangeTimezone: record.ExchangeTimezone,
			SessionDate:      record.SessionDate,
			SessionStatus:    record.SessionStatus,
			OpenAt:           canonicalTimePointer(record.OpenAt),
			CloseAt:          canonicalTimePointer(record.CloseAt),
			IsEarlyClose:     record.IsEarlyClose,
		}
	}
	slices.SortFunc(canonical, func(a, b fingerprintSession) int {
		return strings.Compare(fingerprintSortKey(a), fingerprintSortKey(b))
	})
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("canonicalize calendar sessions: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func calendarSessionFingerprint(records []TradingSession) (string, error) {
	return CalendarSessionFingerprint(records)
}

func fingerprintSortKey(record fingerprintSession) string {
	openAt, closeAt := "<nil>", "<nil>"
	if record.OpenAt != nil {
		openAt = record.OpenAt.UTC().Format(time.RFC3339Nano)
	}
	if record.CloseAt != nil {
		closeAt = record.CloseAt.UTC().Format(time.RFC3339Nano)
	}
	return strings.Join([]string{
		record.CalendarVersion,
		record.MIC,
		record.ExchangeTimezone,
		record.SessionDate,
		record.SessionStatus,
		openAt,
		closeAt,
		fmt.Sprint(record.IsEarlyClose),
	}, "\x00")
}

func validateHistoricalTruthBatch(batch HistoricalTruthBatch) error {
	for i, record := range batch.Identifiers {
		if err := validateIdentifier(record); err != nil {
			return fmt.Errorf("identifier %d: %w", i, err)
		}
	}
	for i, record := range batch.Listings {
		if err := validateListing(record); err != nil {
			return fmt.Errorf("listing %d: %w", i, err)
		}
	}
	for i, record := range batch.Memberships {
		if err := validateMembership(record); err != nil {
			return fmt.Errorf("membership %d: %w", i, err)
		}
	}
	calendars := make(map[calendarKey]CalendarManifest, len(batch.Calendars))
	for i, record := range batch.Calendars {
		if err := validateCalendar(record); err != nil {
			return fmt.Errorf("calendar %d: %w", i, err)
		}
		key := keyForCalendar(record.DataSourceID, record.MIC, record.CalendarVersion)
		if _, exists := calendars[key]; exists {
			return fmt.Errorf("calendar %d: duplicate calendar identity", i)
		}
		calendars[key] = record
	}
	sessions := make(map[calendarKey][]TradingSession, len(batch.Calendars))
	for i, record := range batch.Sessions {
		if err := validateSession(record); err != nil {
			return fmt.Errorf("session %d: %w", i, err)
		}
		key := keyForCalendar(record.DataSourceID, record.MIC, record.CalendarVersion)
		calendar, exists := calendars[key]
		if !exists {
			return fmt.Errorf("session %d: matching calendar manifest is required in the same batch", i)
		}
		if calendar.ExchangeTimezone != record.ExchangeTimezone {
			return fmt.Errorf("session %d: exchange timezone differs from calendar manifest", i)
		}
		sessions[key] = append(sessions[key], record)
	}
	for key, calendar := range calendars {
		records := sessions[key]
		if len(records) != calendar.SessionCount {
			return fmt.Errorf("calendar %s/%s session_count=%d but batch has %d", calendar.MIC, calendar.CalendarVersion, calendar.SessionCount, len(records))
		}
		fingerprint, err := calendarSessionFingerprint(records)
		if err != nil {
			return err
		}
		if fingerprint != calendar.SessionFingerprint {
			return fmt.Errorf("calendar %s/%s session fingerprint mismatch", calendar.MIC, calendar.CalendarVersion)
		}
	}
	return nil
}

// ValidateHistoricalTruthBatch exposes the same fail-closed validation used by
// PostgreSQL publication to deterministic compilers and acceptance tests.
func ValidateHistoricalTruthBatch(batch HistoricalTruthBatch) error {
	return validateHistoricalTruthBatch(batch)
}

// PublishHistoricalTruth atomically inserts a batch of source-backed history.
// Identical replay is accepted; a reused ID with different canonical content or
// an overlapping same-source/same-revision interval aborts the transaction.
func (r *Repository) PublishHistoricalTruth(ctx context.Context, batch HistoricalTruthBatch) error {
	if r == nil {
		return errors.New("PostgreSQL metadata repository is required for historical publication")
	}
	if err := validateHistoricalTruthBatch(batch); err != nil {
		return err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for i, record := range batch.Identifiers {
		hash, err := canonicalRecordHash(record)
		if err != nil {
			return fmt.Errorf("identifier %d: %w", i, err)
		}
		if err := insertImmutable(ctx, tx, "identifier", record.ID, insertSecurityIdentifierVersionSQL,
			selectSecurityIdentifierHashSQL, hash,
			record.ID, record.SchemaVersion, record.SecurityID, record.IdentifierType,
			record.Value, record.NormalizedValue, record.IdentifierScope, record.ValidFrom.UTC(),
			utcPointer(record.ValidUntil), record.AvailableAt.UTC(), record.SourceReference,
			record.RecordedAt.UTC(), record.DataSourceID, record.RawPayloadHash,
			record.Revision, record.IsPrimary, hash); err != nil {
			return fmt.Errorf("identifier %d: %w", i, err)
		}
	}
	for i, record := range batch.Listings {
		hash, err := canonicalRecordHash(record)
		if err != nil {
			return fmt.Errorf("listing %d: %w", i, err)
		}
		if err := insertImmutable(ctx, tx, "listing", record.ID, insertSecurityListingVersionSQL,
			selectSecurityListingHashSQL, hash,
			record.ID, record.SchemaVersion, record.SecurityID, record.IssuerID, record.Exchange,
			record.MIC, record.Currency, record.PrimaryListing, record.ValidFrom.UTC(),
			utcPointer(record.ValidUntil), record.AvailableAt.UTC(), record.SourceReference,
			record.RecordedAt.UTC(), record.DataSourceID, record.RawPayloadHash,
			record.Revision, hash); err != nil {
			return fmt.Errorf("listing %d: %w", i, err)
		}
	}
	for i, record := range batch.Memberships {
		hash, err := canonicalRecordHash(record)
		if err != nil {
			return fmt.Errorf("membership %d: %w", i, err)
		}
		if err := insertImmutable(ctx, tx, "membership", record.ID, insertUniverseMembershipSQL,
			selectUniverseMembershipHashSQL, hash,
			record.ID, record.SchemaVersion, record.UniverseID, record.SecurityID, record.Member,
			record.ValidFrom.UTC(), utcPointer(record.ValidUntil), utcPointer(record.AnnouncedAt),
			record.AvailableAt.UTC(), record.SourceReference, record.RecordedAt.UTC(),
			record.DataSourceID, record.RawPayloadHash, record.Revision, hash); err != nil {
			return fmt.Errorf("membership %d: %w", i, err)
		}
	}
	for i, record := range batch.Calendars {
		hash, err := canonicalRecordHash(record)
		if err != nil {
			return fmt.Errorf("calendar %d: %w", i, err)
		}
		if err := insertImmutable(ctx, tx, "calendar", record.ID, insertCalendarManifestSQL,
			selectCalendarManifestHashSQL, hash,
			record.ID, record.SchemaVersion, record.CalendarVersion, record.MIC,
			record.ExchangeTimezone, record.AvailableAt.UTC(), record.SourceReference,
			record.RecordedAt.UTC(), record.DataSourceID, record.RawPayloadHash,
			record.SessionFingerprint, record.SessionCount, hash); err != nil {
			return fmt.Errorf("calendar %d: %w", i, err)
		}
	}
	for i, record := range batch.Sessions {
		hash, err := canonicalRecordHash(record)
		if err != nil {
			return fmt.Errorf("session %d: %w", i, err)
		}
		if err := insertImmutable(ctx, tx, "session", record.ID, insertTradingSessionSQL,
			selectTradingSessionHashSQL, hash,
			record.ID, record.SchemaVersion, record.CalendarVersion, record.MIC,
			record.ExchangeTimezone, record.SessionDate, record.SessionStatus,
			utcPointer(record.OpenAt), utcPointer(record.CloseAt), record.IsEarlyClose,
			record.AvailableAt.UTC(), record.SourceReference, record.RecordedAt.UTC(),
			record.DataSourceID, record.RawPayloadHash, record.Revision, hash); err != nil {
			return fmt.Errorf("session %d: %w", i, err)
		}
	}
	return tx.Commit(ctx)
}

func insertImmutable(ctx context.Context, tx pgx.Tx, kind, id, insertQuery, hashQuery, expectedHash string, args ...any) error {
	var returnedID string
	err := tx.QueryRow(ctx, insertQuery, args...).Scan(&returnedID)
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var existingHash string
	if err := tx.QueryRow(ctx, hashQuery, id).Scan(&existingHash); err != nil {
		return fmt.Errorf("check existing %s %s: %w", kind, id, err)
	}
	if existingHash != expectedHash {
		return fmt.Errorf("%s ID %s already exists with different content", kind, id)
	}
	return nil
}

func utcPointer(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

func sameRank(availableA time.Time, revisionA int, recordedA time.Time, availableB time.Time, revisionB int, recordedB time.Time) bool {
	return availableA.Equal(availableB) && revisionA == revisionB && recordedA.Equal(recordedB)
}

// ResolveSecurityIdentifier returns the latest source-backed identifier vintage
// valid at asOf and knowable by decisionAt. The source is explicit because v0.2
// has no implicit multi-source priority rule.
func (r *Repository) ResolveSecurityIdentifier(ctx context.Context, dataSourceID, identifierType, value, scope string, asOf, decisionAt time.Time) (*SecurityIdentifierVersion, error) {
	if r == nil {
		return nil, errors.New("PostgreSQL metadata repository is required for historical resolution")
	}
	rows, err := r.pool.Query(ctx, resolveSecurityIdentifierSQL, dataSourceID, strings.ToLower(strings.TrimSpace(identifierType)), strings.ToUpper(strings.TrimSpace(value)), strings.ToUpper(strings.TrimSpace(scope)), asOf.UTC(), decisionAt.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []SecurityIdentifierVersion
	for rows.Next() {
		var record SecurityIdentifierVersion
		if err := rows.Scan(&record.ID, &record.SchemaVersion, &record.SecurityID, &record.IdentifierType, &record.Value, &record.NormalizedValue, &record.IdentifierScope, &record.ValidFrom, &record.ValidUntil, &record.AvailableAt, &record.SourceReference, &record.RecordedAt, &record.DataSourceID, &record.RawPayloadHash, &record.Revision, &record.IsPrimary); err != nil {
			return nil, err
		}
		candidates = append(candidates, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	if len(candidates) > 1 && sameRank(candidates[0].AvailableAt, candidates[0].Revision, candidates[0].RecordedAt, candidates[1].AvailableAt, candidates[1].Revision, candidates[1].RecordedAt) && candidates[0].SecurityID != candidates[1].SecurityID {
		return nil, errors.New("equal-ranked identifier versions resolve to different securities")
	}
	return &candidates[0], nil
}

// ResolveSecurityListing returns one source-backed listing vintage for a
// security/MIC pair at explicit market and knowledge cutoffs.
func (r *Repository) ResolveSecurityListing(ctx context.Context, dataSourceID, securityID, mic string, asOf, decisionAt time.Time) (*SecurityListingVersion, error) {
	if r == nil {
		return nil, errors.New("PostgreSQL metadata repository is required for historical resolution")
	}
	rows, err := r.pool.Query(ctx, resolveSecurityListingSQL, dataSourceID, securityID, strings.ToUpper(strings.TrimSpace(mic)), asOf.UTC(), decisionAt.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []SecurityListingVersion
	for rows.Next() {
		var record SecurityListingVersion
		if err := rows.Scan(&record.ID, &record.SchemaVersion, &record.SecurityID, &record.IssuerID, &record.Exchange, &record.MIC, &record.Currency, &record.PrimaryListing, &record.ValidFrom, &record.ValidUntil, &record.AvailableAt, &record.SourceReference, &record.RecordedAt, &record.DataSourceID, &record.RawPayloadHash, &record.Revision); err != nil {
			return nil, err
		}
		candidates = append(candidates, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	if len(candidates) > 1 && sameRank(candidates[0].AvailableAt, candidates[0].Revision, candidates[0].RecordedAt, candidates[1].AvailableAt, candidates[1].Revision, candidates[1].RecordedAt) && listingIdentity(candidates[0]) != listingIdentity(candidates[1]) {
		return nil, errors.New("equal-ranked listing versions disagree")
	}
	return &candidates[0], nil
}

func listingIdentity(record SecurityListingVersion) string {
	issuer := ""
	if record.IssuerID != nil {
		issuer = *record.IssuerID
	}
	return strings.Join([]string{issuer, record.Exchange, record.MIC, record.Currency, fmt.Sprint(record.PrimaryListing)}, "\x00")
}

// ResolveUniverse returns stable security IDs whose latest eligible assertion
// is member=true. It never consults the current YAML universe.
func (r *Repository) ResolveUniverse(ctx context.Context, dataSourceID, universeID string, asOf, decisionAt time.Time) ([]string, error) {
	if r == nil {
		return nil, errors.New("PostgreSQL metadata repository is required for historical resolution")
	}
	rows, err := r.pool.Query(ctx, resolveUniverseSQL, dataSourceID, universeID, asOf.UTC(), decisionAt.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bySecurity := make(map[string][]UniverseMembership)
	for rows.Next() {
		var record UniverseMembership
		if err := rows.Scan(&record.ID, &record.SchemaVersion, &record.UniverseID, &record.SecurityID, &record.Member, &record.ValidFrom, &record.ValidUntil, &record.AnnouncedAt, &record.AvailableAt, &record.SourceReference, &record.RecordedAt, &record.DataSourceID, &record.RawPayloadHash, &record.Revision); err != nil {
			return nil, err
		}
		bySecurity[record.SecurityID] = append(bySecurity[record.SecurityID], record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]string, 0, len(bySecurity))
	for securityID, records := range bySecurity {
		if len(records) > 1 && sameRank(records[0].AvailableAt, records[0].Revision, records[0].RecordedAt, records[1].AvailableAt, records[1].Revision, records[1].RecordedAt) && records[0].Member != records[1].Member {
			return nil, fmt.Errorf("equal-ranked membership versions disagree for security %s", securityID)
		}
		if records[0].Member {
			result = append(result, securityID)
		}
	}
	slices.Sort(result)
	return result, nil
}

// TradingSessionAt returns the explicit open session containing timestamp. The
// close boundary is exclusive, matching ADR 0007.
func (r *Repository) TradingSessionAt(ctx context.Context, dataSourceID, mic, calendarVersion string, timestamp, decisionAt time.Time) (*TradingSession, error) {
	return r.resolveTradingSession(ctx, tradingSessionAtSQL, dataSourceID, strings.ToUpper(strings.TrimSpace(mic)), calendarVersion, timestamp.UTC(), decisionAt.UTC())
}

// NextTradingSession returns the first explicit open session after timestamp.
func (r *Repository) NextTradingSession(ctx context.Context, dataSourceID, mic, calendarVersion string, timestamp, decisionAt time.Time) (*TradingSession, error) {
	return r.resolveTradingSession(ctx, nextTradingSessionSQL, dataSourceID, strings.ToUpper(strings.TrimSpace(mic)), calendarVersion, timestamp.UTC(), decisionAt.UTC())
}

func (r *Repository) resolveTradingSession(ctx context.Context, query, dataSourceID, mic, calendarVersion string, timestamp, decisionAt time.Time) (*TradingSession, error) {
	if r == nil {
		return nil, errors.New("PostgreSQL metadata repository is required for historical resolution")
	}
	rows, err := r.pool.Query(ctx, query, dataSourceID, mic, calendarVersion, timestamp, decisionAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []TradingSession
	for rows.Next() {
		var record TradingSession
		if err := rows.Scan(&record.ID, &record.SchemaVersion, &record.CalendarVersion, &record.MIC, &record.ExchangeTimezone, &record.SessionDate, &record.SessionStatus, &record.OpenAt, &record.CloseAt, &record.IsEarlyClose, &record.AvailableAt, &record.SourceReference, &record.RecordedAt, &record.DataSourceID, &record.RawPayloadHash, &record.Revision); err != nil {
			return nil, err
		}
		if len(candidates) == 0 || candidates[0].OpenAt != nil && record.OpenAt != nil && record.OpenAt.Equal(*candidates[0].OpenAt) {
			candidates = append(candidates, record)
			continue
		}
		break
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	if len(candidates) > 1 && sameRank(candidates[0].AvailableAt, candidates[0].Revision, candidates[0].RecordedAt, candidates[1].AvailableAt, candidates[1].Revision, candidates[1].RecordedAt) && sessionIdentity(candidates[0]) != sessionIdentity(candidates[1]) {
		return nil, errors.New("equal-ranked trading sessions disagree")
	}
	return &candidates[0], nil
}

func sessionIdentity(record TradingSession) string {
	openAt, closeAt := "", ""
	if record.OpenAt != nil {
		openAt = record.OpenAt.UTC().Format(time.RFC3339Nano)
	}
	if record.CloseAt != nil {
		closeAt = record.CloseAt.UTC().Format(time.RFC3339Nano)
	}
	return strings.Join([]string{record.SessionDate, record.SessionStatus, openAt, closeAt, fmt.Sprint(record.IsEarlyClose)}, "\x00")
}
