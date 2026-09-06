package metadata

import (
	"strings"
	"testing"
	"time"
)

func testDiscoveryRegistration() DiscoveryIndexRegistration {
	one, two := 1, 2
	scoreOne, scoreTwo := "70", "60"
	value := "0.1"
	return DiscoveryIndexRegistration{
		DiscoveryID: "10000000-0000-4000-8000-000000000001", ModelName: "momentum-risk-rank",
		ModelVersion: "1.0.0", GitCommit: "unknown",
		UniverseID: "20000000-0000-4000-8000-000000000001", UniverseName: "test-universe",
		UniverseFingerprint: strings.Repeat("a", 64), MarketSession: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC),
		DecisionAt: time.Date(2026, 9, 8, 22, 0, 0, 0, time.UTC), FeatureBatchID: "30000000-0000-4000-8000-000000000001",
		FeatureInputFingerprint: strings.Repeat("b", 64), CandidateCount: 1, EligibleCount: 2,
		ManifestPath: "nasdaq/indexes/manifest.json", ManifestSHA256: strings.Repeat("c", 64),
		Rankings: []DiscoveryRanking{
			{SecurityID: "50000000-0000-4000-8000-000000000002", Rank: &two, Ticker: "BBB", Sector: "technology", Score: &scoreTwo, Return1M: &value, Return3M: &value, Return6M: &value, Return12M: &value, RealizedVolatility1M: &value, MaxDrawdown1M: &value, EligibilityStatus: "eligible", RejectionReasons: []string{}},
			{SecurityID: "50000000-0000-4000-8000-000000000001", Rank: &one, Ticker: "AAA", Sector: "technology", Candidate: true, Score: &scoreOne, Return1M: &value, Return3M: &value, Return6M: &value, Return12M: &value, RealizedVolatility1M: &value, MaxDrawdown1M: &value, EligibilityStatus: "eligible", RejectionReasons: []string{}},
		},
	}
}

func TestDiscoveryRegistrationNormalizesAndFingerprints(t *testing.T) {
	registration := testDiscoveryRegistration()
	normalized, err := registration.normalized()
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Rankings[0].Ticker != "AAA" {
		t.Fatalf("rankings not canonical: %+v", normalized.Rankings)
	}
	first, err := discoveryRegistrationSHA256(normalized)
	if err != nil {
		t.Fatal(err)
	}
	registration.Rankings[0], registration.Rankings[1] = registration.Rankings[1], registration.Rankings[0]
	normalizedAgain, err := registration.normalized()
	if err != nil {
		t.Fatal(err)
	}
	second, err := discoveryRegistrationSHA256(normalizedAgain)
	if err != nil || first != second {
		t.Fatalf("discovery digests differ: %s %s err=%v", first, second, err)
	}
}

func TestDiscoveryRegistrationRejectsNonContiguousRanks(t *testing.T) {
	registration := testDiscoveryRegistration()
	three := 3
	registration.Rankings[1].Rank = &three
	if _, err := registration.normalized(); err == nil || !strings.Contains(err.Error(), "contiguous") {
		t.Fatalf("got %v", err)
	}
}
