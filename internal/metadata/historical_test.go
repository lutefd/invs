package metadata

import (
	"encoding/json"
	"os"
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

func validCorporateAction() CorporateActionVersion {
	cashAmount := "0.82"
	currency := "USD"
	recordDate := "2020-08-10"
	paymentDate := "2020-08-13"
	locator := "0000320193-20-000060/exhibit-99.1/cash-dividend"
	normalizer := "sec-action-v1"
	ingestedAt := time.Date(2026, time.August, 24, 2, 59, 0, 0, time.UTC)
	return CorporateActionVersion{
		SchemaVersion: CorporateActionSchemaVersion,
		ID:            "88888888-8888-4888-8888-888888888888", SecurityID: testHistoricalSecurity,
		SourceEventID: "0000320193-20-000060/exhibit-99.1/cash-dividend",
		Revision:      0, ActionStatus: "active", ActionType: "cash_dividend",
		ObservedAt:         time.Date(2020, time.August, 7, 0, 0, 0, 0, time.UTC),
		ObservedPrecision:  "date",
		PublishedAt:        time.Date(2020, time.July, 30, 22, 55, 4, 0, time.UTC),
		PublishedPrecision: "second",
		AvailableAt:        time.Date(2020, time.July, 30, 22, 55, 4, 0, time.UTC),
		EffectiveAt:        time.Date(2020, time.August, 7, 0, 0, 0, 0, time.UTC),
		EffectivePrecision: "date",
		RecordDate:         &recordDate, PaymentDate: &paymentDate,
		CashAmount: &cashAmount, Currency: &currency,
		SourceReference: "https://www.sec.gov/Archives/edgar/data/320193/000032019320000060/",
		RecordedAt:      time.Date(2026, time.August, 24, 3, 0, 0, 0, time.UTC),
		Provenance: CorporateActionProvenance{
			DataSourceID:      testHistoricalDataSource,
			IngestionRunID:    "99999999-9999-4999-8999-999999999999",
			RawPayloadHash:    historicalTestHash('9'),
			RawRecordLocator:  &locator,
			IngestedAt:        ingestedAt,
			NormalizerVersion: &normalizer,
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

func TestCalendarFingerprintExcludesVersionLineage(t *testing.T) {
	first := validHistoricalSessions()
	second := validHistoricalSessions()
	for index := range second {
		second[index].CalendarVersion = "xnas_2026_corrected"
		second[index].AvailableAt = second[index].AvailableAt.Add(24 * time.Hour)
		second[index].RecordedAt = second[index].RecordedAt.Add(24 * time.Hour)
		second[index].RawPayloadHash = strings.Repeat("b", 64)
		second[index].Revision++
	}
	firstHash, err := calendarSessionFingerprint(first)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := calendarSessionFingerprint(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatalf("identical session semantics across source versions produced %s and %s", firstHash, secondHash)
	}
}

func TestCalendarFingerprintMatchesSharedSchemaFixture(t *testing.T) {
	body, err := os.ReadFile("../../schemas/calendar.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Manifests []CalendarManifest `json:"manifests"`
		Sessions  []TradingSession   `json:"sessions"`
	}
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, manifest := range fixture.Manifests {
		sessions := make([]TradingSession, 0, manifest.SessionCount)
		for _, session := range fixture.Sessions {
			if session.MIC == manifest.MIC && session.CalendarVersion == manifest.CalendarVersion {
				sessions = append(sessions, session)
			}
		}
		fingerprint, err := CalendarSessionFingerprint(sessions)
		if err != nil {
			t.Fatal(err)
		}
		if fingerprint != manifest.SessionFingerprint {
			t.Fatalf("%s/%s fingerprint = %s, want %s", manifest.MIC, manifest.CalendarVersion, fingerprint, manifest.SessionFingerprint)
		}
	}
}

func TestSelectCalendarManifestUsesDecisionClockRankAndFailsClosed(t *testing.T) {
	base := CalendarManifest{
		SchemaVersion: HistoricalSchemaVersion, ID: "11111111-1111-4111-8111-111111111111",
		CalendarVersion: "xnas_2025_original", MIC: "XNAS", ExchangeTimezone: "America/New_York",
		AvailableAt: time.Date(2024, 12, 13, 5, 0, 0, 0, time.UTC),
		RecordedAt:  time.Date(2026, 8, 24, 3, 0, 0, 0, time.UTC), DataSourceID: testHistoricalDataSource,
		SourceReference: "source/original", RawPayloadHash: strings.Repeat("a", 64),
		SessionFingerprint: strings.Repeat("c", 64), SessionCount: 3,
	}
	corrected := base
	corrected.ID = "22222222-2222-4222-8222-222222222222"
	corrected.CalendarVersion = "xnas_2025_corrected"
	corrected.AvailableAt = base.AvailableAt.Add(24 * time.Hour)
	corrected.SourceReference = "source/corrected"
	corrected.RawPayloadHash = strings.Repeat("b", 64)
	selected, err := selectCalendarManifest([]CalendarManifest{base, corrected})
	if err != nil || selected == nil || selected.CalendarVersion != corrected.CalendarVersion {
		t.Fatalf("selected manifest/error = %+v/%v", selected, err)
	}

	conflict := corrected
	conflict.ID = "33333333-3333-4333-8333-333333333333"
	conflict.CalendarVersion = "xnas_2025_conflict"
	conflict.RawPayloadHash = strings.Repeat("d", 64)
	if _, err := selectCalendarManifest([]CalendarManifest{corrected, conflict}); err == nil || !strings.Contains(err.Error(), "equal-ranked") {
		t.Fatalf("equal-ranked conflict error = %v", err)
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

func TestValidateCorporateActionVersionsFailClosed(t *testing.T) {
	action := validCorporateAction()
	if err := validateHistoricalTruthBatch(HistoricalTruthBatch{Actions: []CorporateActionVersion{action}}); err != nil {
		t.Fatalf("valid corporate action rejected: %v", err)
	}

	for name, mutate := range map[string]func(*CorporateActionVersion){
		"availability before publication": func(record *CorporateActionVersion) {
			record.AvailableAt = record.PublishedAt.Add(-time.Microsecond)
		},
		"date precision with time": func(record *CorporateActionVersion) {
			record.ObservedAt = record.ObservedAt.Add(time.Microsecond)
		},
		"cash without currency": func(record *CorporateActionVersion) {
			record.Currency = nil
		},
		"noncanonical decimal": func(record *CorporateActionVersion) {
			value := "00.82"
			record.CashAmount = &value
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := validCorporateAction()
			mutate(&candidate)
			if err := validateHistoricalTruthBatch(HistoricalTruthBatch{Actions: []CorporateActionVersion{candidate}}); err == nil {
				t.Fatal("invalid corporate action accepted")
			}
		})
	}

	split := validCorporateAction()
	split.ActionType = "split"
	split.CashAmount, split.Currency = nil, nil
	numerator, denominator := "4", "1"
	split.RatioNumerator, split.RatioDenominator = &numerator, &denominator
	if err := validateHistoricalTruthBatch(HistoricalTruthBatch{Actions: []CorporateActionVersion{split}}); err != nil {
		t.Fatalf("valid split rejected: %v", err)
	}
	zero := "0"
	split.RatioNumerator = &zero
	if err := validateHistoricalTruthBatch(HistoricalTruthBatch{Actions: []CorporateActionVersion{split}}); err == nil || !strings.Contains(err.Error(), "positive ratio") {
		t.Fatalf("zero split ratio error = %v", err)
	}
	unsupportedSplit := split
	unsupportedSplit.ActionStatus = "unsupported"
	unsupportedSplit.RatioNumerator, unsupportedSplit.RatioDenominator = nil, nil
	if err := validateHistoricalTruthBatch(HistoricalTruthBatch{Actions: []CorporateActionVersion{unsupportedSplit}}); err != nil {
		t.Fatalf("unsupported incomplete split rejected: %v", err)
	}

	duplicate := validCorporateAction()
	duplicate.ID = "aaaaaaaa-8888-4888-8888-888888888888"
	if err := validateHistoricalTruthBatch(HistoricalTruthBatch{Actions: []CorporateActionVersion{action, duplicate}}); err == nil || !strings.Contains(err.Error(), "duplicate source event revision") {
		t.Fatalf("duplicate action error = %v", err)
	}
}

func TestSelectCorporateActionRevisionsHonorsCorrectionsAndStates(t *testing.T) {
	original := validCorporateAction()
	corrected := validCorporateAction()
	corrected.ID = "88888888-8888-4888-8888-888888888889"
	corrected.Revision = 1
	corrected.AvailableAt = original.AvailableAt.Add(24 * time.Hour)
	corrected.RecordedAt = original.RecordedAt.Add(time.Minute)
	correctedAmount := "0.84"
	corrected.CashAmount = &correctedAmount

	selected, err := selectCorporateActionRevisions([]CorporateActionVersion{original, corrected})
	if err != nil || len(selected) != 1 || selected[0].Revision != 1 || *selected[0].CashAmount != correctedAmount {
		t.Fatalf("selected correction/error = %+v/%v", selected, err)
	}

	cancelled := corrected
	cancelled.ID = "88888888-8888-4888-8888-888888888890"
	cancelled.Revision = 2
	cancelled.ActionStatus = "cancelled"
	cancelled.AvailableAt = corrected.AvailableAt.Add(time.Hour)
	cancelled.RecordedAt = corrected.RecordedAt.Add(time.Hour)
	selected, err = selectCorporateActionRevisions([]CorporateActionVersion{original, corrected, cancelled})
	if err != nil || len(selected) != 0 {
		t.Fatalf("cancelled family selection/error = %+v/%v", selected, err)
	}

	unsupported := corrected
	unsupported.ID = "88888888-8888-4888-8888-888888888891"
	unsupported.SourceEventID += "/unsupported"
	unsupported.ActionStatus = "unsupported"
	selected, err = selectCorporateActionRevisions([]CorporateActionVersion{unsupported})
	if err != nil || len(selected) != 1 || selected[0].ActionStatus != "unsupported" {
		t.Fatalf("unsupported selection/error = %+v/%v", selected, err)
	}
}

func TestSelectCorporateActionRevisionsRejectsEqualRankConflict(t *testing.T) {
	first := validCorporateAction()
	conflict := validCorporateAction()
	conflict.ID = "88888888-8888-4888-8888-888888888889"
	amount := "0.83"
	conflict.CashAmount = &amount
	if _, err := selectCorporateActionRevisions([]CorporateActionVersion{first, conflict}); err == nil || !strings.Contains(err.Error(), "equal-ranked") {
		t.Fatalf("equal-ranked action error = %v", err)
	}
}

func TestIdentityCorrectionsShortenOpenEndedIntervals(t *testing.T) {
	asOf := time.Date(2026, time.January, 10, 0, 0, 0, 0, time.UTC)
	initial := validHistoricalIdentifier()
	initial.ID = "33333333-3333-4333-8333-333333333331"
	correction := initial
	correction.ID = "33333333-3333-4333-8333-333333333332"
	validUntil := time.Date(2026, time.January, 5, 0, 0, 0, 0, time.UTC)
	correction.ValidUntil = &validUntil
	correction.AvailableAt = time.Date(2026, time.January, 4, 0, 0, 0, 0, time.UTC)
	correction.RecordedAt = correction.AvailableAt
	correction.Revision = 1

	selected, err := selectIdentifierRevision([]SecurityIdentifierVersion{initial}, asOf)
	if err != nil || selected == nil || selected.ID != initial.ID {
		t.Fatalf("pre-correction identifier = %+v, %v", selected, err)
	}
	selected, err = selectIdentifierRevision([]SecurityIdentifierVersion{initial, correction}, asOf)
	if err != nil || selected != nil {
		t.Fatalf("corrected identifier = %+v, %v; want nil", selected, err)
	}

	issuerID := "44444444-4444-4444-8444-444444444444"
	listing := SecurityListingVersion{
		ID: "55555555-5555-4555-8555-555555555551", SecurityID: initial.SecurityID,
		IssuerID: &issuerID, Exchange: "NASDAQ", MIC: "XNAS", Currency: "USD",
		PrimaryListing: true, ValidFrom: initial.ValidFrom, AvailableAt: initial.AvailableAt,
		RecordedAt: initial.RecordedAt,
	}
	listingCorrection := listing
	listingCorrection.ID = "55555555-5555-4555-8555-555555555552"
	listingCorrection.ValidUntil = &validUntil
	listingCorrection.AvailableAt = correction.AvailableAt
	listingCorrection.RecordedAt = correction.RecordedAt
	listingCorrection.Revision = 1

	selectedListing, err := selectListingRevision([]SecurityListingVersion{listing}, asOf)
	if err != nil || selectedListing == nil || selectedListing.ID != listing.ID {
		t.Fatalf("pre-correction listing = %+v, %v", selectedListing, err)
	}
	selectedListing, err = selectListingRevision([]SecurityListingVersion{listing, listingCorrection}, asOf)
	if err != nil || selectedListing != nil {
		t.Fatalf("corrected listing = %+v, %v; want nil", selectedListing, err)
	}
}

func TestEqualRankIntervalCorrectionsFailClosed(t *testing.T) {
	initial := validHistoricalIdentifier()
	first := initial
	first.ID = "33333333-3333-4333-8333-333333333331"
	first.Revision = 1
	first.ValidUntil = historicalTimePointer(time.Date(2026, time.January, 5, 0, 0, 0, 0, time.UTC))
	conflict := first
	conflict.ID = "33333333-3333-4333-8333-333333333332"
	conflict.ValidUntil = historicalTimePointer(time.Date(2026, time.January, 6, 0, 0, 0, 0, time.UTC))
	if _, err := selectIdentifierRevision([]SecurityIdentifierVersion{initial, first, conflict}, historicalTestTime(10)); err == nil || !strings.Contains(err.Error(), "corrections disagree") {
		t.Fatalf("equal-rank correction error = %v", err)
	}
}

func historicalTimePointer(value time.Time) *time.Time {
	return &value
}
