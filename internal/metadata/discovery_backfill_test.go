package metadata

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func backfillNumber(value string) *DiscoveryBackfillNumber {
	return &DiscoveryBackfillNumber{text: value, valid: true}
}

func testDiscoveryBackfillRegistration() DiscoveryBackfillRegistration {
	fingerprint := strings.Repeat("a", 64)
	runID := uuid.NewSHA1(discoveryBackfillNamespace, []byte(fingerprint)).String()
	return DiscoveryBackfillRegistration{
		ManifestPath: "backfills/" + runID + "/manifest.json", ManifestSHA256: strings.Repeat("d", 64),
		Manifest: DiscoveryBackfillManifest{
			SchemaVersion: DiscoveryBackfillSchemaVersion, GeneratorVersion: DiscoveryBackfillGeneratorVersion,
			ReconstructionRunID: runID, ObservationMode: discoveryBackfillObservationMode,
			UniverseBasis: discoveryBackfillUniverseBasis, ReconstructedAt: "2026-09-22T15:00:00-03:00",
			InputFingerprint: fingerprint,
			Profile: DiscoveryBackfillProfile{
				UniverseID: "20000000-0000-4000-8000-000000000001", Name: "test-universe",
				MembershipAsOf: "2026-09-21", Fingerprint: strings.Repeat("b", 64),
				ProfileSHA256: strings.Repeat("c", 64), Size: 1,
				SnapshotPath: "backfills/" + runID + "/inputs/profile.yaml",
			},
			Model: DiscoveryBackfillModel{
				Name: "momentum-risk-rank", Version: "1.0.0", GitCommit: "unknown",
				Weights: map[string]string{"return_3m": "0.35", "return_6m": "0.30"},
			},
			Range: DiscoveryBackfillRange{StartDate: "2026-09-21", EndDate: "2026-09-21"},
			Calendar: DiscoveryBackfillCalendar{
				DataSourceID: "30000000-0000-4000-8000-000000000001", MIC: "XNAS",
				CalendarVersion: "nasdaq-2026", SessionCount: 1, SessionFingerprint: strings.Repeat("e", 64),
				AvailableAt: "2026-09-20T12:00:00Z", SnapshotSHA256: strings.Repeat("f", 64),
				SnapshotPath: "backfills/" + runID + "/inputs/calendar-snapshot.json",
			},
			PriceInputs: DiscoveryBackfillPriceInputs{
				Source: "yahoo", Interval: "1d", PriceBasis: "split_adjusted", Currency: "USD",
				HistoricalFitness: discoveryBackfillInputFitness, AvailabilityPolicy: discoveryBackfillAvailabilityPolicy,
				InputFingerprint: strings.Repeat("1", 64), SelectedRowCount: 2,
				Manifests: []DiscoveryBackfillManifestRef{{
					Path:       "backfills/" + runID + "/inputs/manifests/" + strings.Repeat("2", 64) + ".json",
					SourcePath: "normalized/prices/manifest.json", SHA256: strings.Repeat("2", 64),
					Parts: []DiscoveryBackfillPartRef{{
						Path:       "backfills/" + runID + "/inputs/normalized/prices/part.parquet",
						SourcePath: "normalized/prices/part.parquet", SHA256: strings.Repeat("3", 64),
					}},
				}},
			},
			Sessions: []DiscoveryBackfillSession{{
				IndexID: "40000000-0000-4000-8000-000000000001", MarketSession: "2026-09-21",
				DecisionAt: "2026-09-21T20:00:00Z", CandidateCount: 1, EligibleCount: 1,
				PriceInputFingerprint: strings.Repeat("4", 64),
				Rankings: []DiscoveryBackfillRanking{{
					Rank: intPointer(1), SecurityID: "50000000-0000-4000-8000-000000000001",
					Ticker: "AAA", Sector: "technology", Candidate: true, Score: backfillNumber("75.2"),
					Features: DiscoveryBackfillFeatures{
						Return1M: backfillNumber("0.1"), Return3M: backfillNumber("0.2"),
						Return6M: backfillNumber("0.3"), Return12M: backfillNumber("0.4"),
						RealizedVolatility1M: backfillNumber("0.05"), MaxDrawdown1M: backfillNumber("0.02"),
					}, Eligibility: DiscoveryBackfillEligibility{Status: "eligible", Reasons: []string{}},
				}},
				PriceBars: []DiscoveryBackfillPriceBar{{
					SecurityID: "50000000-0000-4000-8000-000000000001", Ticker: "AAA",
					ObservedAt: "2026-09-21T20:00:00Z", AvailableAt: "2026-09-21T20:01:00Z", IngestedAt: "2026-09-21T20:02:00Z",
					Source: "yahoo", Interval: "1d", PriceBasis: "split_adjusted", Currency: "USD",
					Open: DiscoveryBackfillNumber{text: "10", valid: true}, High: DiscoveryBackfillNumber{text: "12", valid: true},
					Low: DiscoveryBackfillNumber{text: "9", valid: true}, Close: DiscoveryBackfillNumber{text: "11", valid: true},
					RawPayloadHash: strings.Repeat("5", 64), DataSourceID: "60000000-0000-4000-8000-000000000001",
					IngestionRunID: "70000000-0000-4000-8000-000000000001", RawRecordLocator: "prices/AAA/2026-09-21",
					ManifestPath:       "backfills/" + runID + "/inputs/manifests/" + strings.Repeat("2", 64) + ".json",
					SourceManifestPath: "normalized/prices/manifest.json", ManifestSHA256: strings.Repeat("2", 64),
					PartPath:       "backfills/" + runID + "/inputs/normalized/prices/part.parquet",
					SourcePartPath: "normalized/prices/part.parquet", PartSHA256: strings.Repeat("3", 64),
				}},
			}},
		},
	}
}

func intPointer(value int) *int { return &value }

func TestDiscoveryBackfillRegistrationNormalizesAndHashes(t *testing.T) {
	registration := testDiscoveryBackfillRegistration()
	normalized, err := ValidateDiscoveryBackfillRegistration(registration)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Manifest.ReconstructedAt != "2026-09-22T18:00:00Z" {
		t.Fatalf("reconstructed_at = %q, want UTC timestamp", normalized.Manifest.ReconstructedAt)
	}
	if normalized.Manifest.Sessions[0].PriceBars[0].Volume != nil {
		t.Fatal("missing volume should remain SQL NULL")
	}
	first, err := discoveryBackfillRegistrationSHA256(normalized)
	if err != nil {
		t.Fatal(err)
	}

	permuted := normalized
	permuted.Manifest.Sessions = append([]DiscoveryBackfillSession(nil), normalized.Manifest.Sessions...)
	permuted.Manifest.Sessions[0].Rankings = append([]DiscoveryBackfillRanking(nil), normalized.Manifest.Sessions[0].Rankings...)
	permuted.Manifest.Sessions[0].PriceBars = append([]DiscoveryBackfillPriceBar(nil), normalized.Manifest.Sessions[0].PriceBars...)
	normalizedAgain, err := ValidateDiscoveryBackfillRegistration(permuted)
	if err != nil {
		t.Fatal(err)
	}
	second, err := discoveryBackfillRegistrationSHA256(normalizedAgain)
	if err != nil || first != second {
		t.Fatalf("registration digests differ: %s %s err=%v", first, second, err)
	}
	if got := nullableBackfillNumberSQLText(nil); got != nil {
		t.Fatalf("nil volume SQL value = %v, want nil", got)
	}
	volume := backfillNumber("0")
	if got := nullableBackfillNumberSQLText(volume); got == nil || *got != "0" {
		t.Fatalf("non-null volume SQL value = %v, want 0", got)
	}
}

func TestDiscoveryBackfillRegistrationRejectsUnsafePathsAndIncompleteCoverage(t *testing.T) {
	t.Run("unsafe archived path", func(t *testing.T) {
		registration := testDiscoveryBackfillRegistration()
		registration.Manifest.PriceInputs.Manifests[0].Parts[0].Path = "backfills/../../part.parquet"
		if _, err := ValidateDiscoveryBackfillRegistration(registration); err == nil || !strings.Contains(err.Error(), "unsafe") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("eligible member missing bar", func(t *testing.T) {
		registration := testDiscoveryBackfillRegistration()
		registration.Manifest.Profile.Size = 2
		registration.Manifest.Sessions[0].EligibleCount = 2
		secondRank := 2
		registration.Manifest.Sessions[0].Rankings = append(registration.Manifest.Sessions[0].Rankings, DiscoveryBackfillRanking{
			Rank: &secondRank, SecurityID: "50000000-0000-4000-8000-000000000002", Ticker: "BBB",
			Sector: "technology", Candidate: false, Score: backfillNumber("60"),
			Features:    registration.Manifest.Sessions[0].Rankings[0].Features,
			Eligibility: DiscoveryBackfillEligibility{Status: "eligible", Reasons: []string{}},
		})
		if _, err := ValidateDiscoveryBackfillRegistration(registration); err == nil || !strings.Contains(err.Error(), "no price bar but is not rejected") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("explicitly rejected member may have no bar", func(t *testing.T) {
		registration := testDiscoveryBackfillRegistration()
		registration.Manifest.Profile.Size = 2
		registration.Manifest.Sessions[0].RejectedCount = 1
		missing := registration.Manifest.Sessions[0].Rankings[0]
		missing.SecurityID = "50000000-0000-4000-8000-000000000002"
		missing.Ticker = "BBB"
		missing.Rank = nil
		missing.Score = nil
		missing.Candidate = false
		missing.Features = DiscoveryBackfillFeatures{}
		missing.Eligibility = DiscoveryBackfillEligibility{
			Status: "rejected", Reasons: []string{discoveryBackfillMissingBarReason},
		}
		registration.Manifest.Sessions[0].Rankings = append(
			registration.Manifest.Sessions[0].Rankings, missing,
		)
		if _, err := ValidateDiscoveryBackfillRegistration(registration); err != nil {
			t.Fatalf("explicitly rejected member without a bar was rejected: %v", err)
		}
	})
	t.Run("all-missing session is retained", func(t *testing.T) {
		registration := testDiscoveryBackfillRegistration()
		session := &registration.Manifest.Sessions[0]
		session.CandidateCount, session.EligibleCount, session.RejectedCount = 0, 0, 1
		session.PriceBars = nil
		row := &session.Rankings[0]
		row.Rank, row.Score, row.Candidate = nil, nil, false
		row.Features = DiscoveryBackfillFeatures{}
		row.Eligibility = DiscoveryBackfillEligibility{
			Status: "rejected", Reasons: []string{discoveryBackfillMissingBarReason},
		}
		if _, err := ValidateDiscoveryBackfillRegistration(registration); err != nil {
			t.Fatalf("all-missing session was rejected: %v", err)
		}
	})
	t.Run("invalid timestamp", func(t *testing.T) {
		registration := testDiscoveryBackfillRegistration()
		registration.Manifest.Sessions[0].PriceBars[0].ObservedAt = time.Now().Format("2006-01-02T15:04:05")
		if _, err := ValidateDiscoveryBackfillRegistration(registration); err == nil || !strings.Contains(err.Error(), "offset") {
			t.Fatalf("got %v", err)
		}
	})
}
