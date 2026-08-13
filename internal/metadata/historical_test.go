package metadata

import (
	"strings"
	"testing"
	"time"
)

const (
	testHistoricalDataSource = "11111111-1111-4111-8111-111111111111"
	testHistoricalSecurity   = "22222222-2222-4222-8222-222222222222"
	testHistoricalRecord     = "33333333-3333-4333-8333-333333333333"
)

func historicalTestTime(hour int) time.Time {
	return time.Date(2026, time.January, 2, hour, 0, 0, 0, time.UTC)
}

func historicalTestHash(fill byte) string {
	return strings.Repeat(string(fill), 64)
}

func validHistoricalIdentifier() SecurityIdentifierVersion {
	availableAt := historicalTestTime(12)
	return SecurityIdentifierVersion{
		SchemaVersion:   HistoricalSchemaVersion,
		ID:              testHistoricalRecord,
		SecurityID:      testHistoricalSecurity,
		IdentifierType:  "ticker",
		Value:           "ABC",
		NormalizedValue: "ABC",
		IdentifierScope: "XNAS",
		ValidFrom:       historicalTestTime(9),
		AvailableAt:     availableAt,
		SourceReference: "https://example.test/identifiers/1",
		RecordedAt:      availableAt.Add(time.Minute),
		DataSourceID:    testHistoricalDataSource,
		RawPayloadHash:  historicalTestHash('a'),
		Revision:        0,
		IsPrimary:       true,
	}
}

func validHistoricalSessions() []TradingSession {
	availableAt := historicalTestTime(12)
	openAt := time.Date(2026, time.January, 2, 14, 30, 0, 0, time.UTC)
	closeAt := time.Date(2026, time.January, 2, 21, 0, 0, 0, time.UTC)
	return []TradingSession{
		{
			SchemaVersion:    HistoricalSchemaVersion,
			ID:               "44444444-4444-4444-8444-444444444444",
			CalendarVersion:  "nasdaq-2026",
			MIC:              "XNAS",
			ExchangeTimezone: "America/New_York",
			SessionDate:      "2026-01-02",
			SessionStatus:    "open",
			OpenAt:           &openAt,
			CloseAt:          &closeAt,
			AvailableAt:      availableAt,
			SourceReference:  "https://example.test/calendars/nasdaq-2026",
			RecordedAt:       availableAt.Add(time.Minute),
			DataSourceID:     testHistoricalDataSource,
			RawPayloadHash:   historicalTestHash('b'),
			Revision:         0,
		},
		{
			SchemaVersion:    HistoricalSchemaVersion,
			ID:               "55555555-5555-4555-8555-555555555555",
			CalendarVersion:  "nasdaq-2026",
			MIC:              "XNAS",
			ExchangeTimezone: "America/New_York",
			SessionDate:      "2026-01-03",
			SessionStatus:    "closed",
			AvailableAt:      availableAt,
			SourceReference:  "https://example.test/calendars/nasdaq-2026",
			RecordedAt:       availableAt.Add(time.Minute),
			DataSourceID:     testHistoricalDataSource,
			RawPayloadHash:   historicalTestHash('c'),
			Revision:         0,
		},
	}
}

func TestCanonicalHistoricalHashesNormalizeEquivalentUTCInstants(t *testing.T) {
	location := time.FixedZone("BRT", -3*60*60)
	local := time.Date(2026, time.January, 2, 9, 0, 0, 0, location)
	utc := local.UTC()

	first := validHistoricalIdentifier()
	first.ValidFrom = local
	first.AvailableAt = local.Add(3 * time.Hour)
	first.RecordedAt = local.Add(4 * time.Hour)

	second := first
	second.ValidFrom = utc
	second.AvailableAt = utc.Add(3 * time.Hour)
	second.RecordedAt = utc.Add(4 * time.Hour)

	firstHash, err := canonicalRecordHash(first)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := canonicalRecordHash(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatalf("equivalent UTC instants produced different hashes: %s != %s", firstHash, secondHash)
	}
}

func TestCalendarFingerprintIsOrderIndependentAndUTCCanonical(t *testing.T) {
	sessions := validHistoricalSessions()
	first, err := calendarSessionFingerprint(sessions)
	if err != nil {
		t.Fatal(err)
	}

	location := time.FixedZone("BRT", -3*60*60)
	openAt := sessions[0].OpenAt.In(location)
	closeAt := sessions[0].CloseAt.In(location)
	sessions[0].OpenAt = &openAt
	sessions[0].CloseAt = &closeAt
	sessions[0], sessions[1] = sessions[1], sessions[0]
	second, err := calendarSessionFingerprint(sessions)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("equivalent/reordered sessions produced different fingerprints: %s != %s", first, second)
	}
}

func TestValidateHistoricalTruthBatchRequiresMatchingCalendarManifest(t *testing.T) {
	sessions := validHistoricalSessions()
	fingerprint, err := calendarSessionFingerprint(sessions)
	if err != nil {
		t.Fatal(err)
	}
	availableAt := historicalTestTime(12)
	calendar := CalendarManifest{
		SchemaVersion:      HistoricalSchemaVersion,
		ID:                 "66666666-6666-4666-8666-666666666666",
		CalendarVersion:    "nasdaq-2026",
		MIC:                "XNAS",
		ExchangeTimezone:   "America/New_York",
		AvailableAt:        availableAt,
		SourceReference:    "https://example.test/calendars/nasdaq-2026",
		RecordedAt:         availableAt.Add(time.Minute),
		DataSourceID:       testHistoricalDataSource,
		RawPayloadHash:     historicalTestHash('d'),
		SessionFingerprint: fingerprint,
		SessionCount:       len(sessions),
	}

	batch := HistoricalTruthBatch{Calendars: []CalendarManifest{calendar}, Sessions: sessions}
	if err := validateHistoricalTruthBatch(batch); err != nil {
		t.Fatalf("valid calendar batch rejected: %v", err)
	}

	badFingerprint := batch
	badFingerprint.Calendars = []CalendarManifest{calendar}
	badFingerprint.Calendars[0].SessionFingerprint = historicalTestHash('e')
	if err := validateHistoricalTruthBatch(badFingerprint); err == nil || !strings.Contains(err.Error(), "fingerprint mismatch") {
		t.Fatalf("bad fingerprint error = %v", err)
	}

	missingManifest := HistoricalTruthBatch{Sessions: sessions}
	if err := validateHistoricalTruthBatch(missingManifest); err == nil || !strings.Contains(err.Error(), "matching calendar manifest") {
		t.Fatalf("missing manifest error = %v", err)
	}
}

func TestValidateHistoricalTruthBatchRejectsInvalidAvailabilityAndSessions(t *testing.T) {
	identifier := validHistoricalIdentifier()
	identifier.RecordedAt = identifier.AvailableAt.Add(-time.Nanosecond)
	if err := validateHistoricalTruthBatch(HistoricalTruthBatch{Identifiers: []SecurityIdentifierVersion{identifier}}); err == nil || !strings.Contains(err.Error(), "recorded_at must not precede available_at") {
		t.Fatalf("availability error = %v", err)
	}

	sessions := validHistoricalSessions()
	sessions[1].IsEarlyClose = true
	fingerprint, err := calendarSessionFingerprint(sessions)
	if err != nil {
		t.Fatal(err)
	}
	availableAt := historicalTestTime(12)
	calendar := CalendarManifest{
		SchemaVersion:      HistoricalSchemaVersion,
		ID:                 "77777777-7777-4777-8777-777777777777",
		CalendarVersion:    "nasdaq-2026",
		MIC:                "XNAS",
		ExchangeTimezone:   "America/New_York",
		AvailableAt:        availableAt,
		SourceReference:    "https://example.test/calendars/nasdaq-2026",
		RecordedAt:         availableAt.Add(time.Minute),
		DataSourceID:       testHistoricalDataSource,
		RawPayloadHash:     historicalTestHash('f'),
		SessionFingerprint: fingerprint,
		SessionCount:       len(sessions),
	}
	if err := validateHistoricalTruthBatch(HistoricalTruthBatch{Calendars: []CalendarManifest{calendar}, Sessions: sessions}); err == nil || !strings.Contains(err.Error(), "closed session cannot") {
		t.Fatalf("invalid closed session error = %v", err)
	}
}
