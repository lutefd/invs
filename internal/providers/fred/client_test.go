package fred

import (
	"testing"
	"time"
)

func TestParseCSVUsesConservativeAvailabilityAndDeduplicates(t *testing.T) {
	ingested := time.Date(2024, 8, 1, 12, 30, 0, 0, time.UTC)
	b := []byte("observation_date,DGS10\n2024-01-01,.\n2024-01-02,4.25\n2024-01-02,4.25\n")
	obs, received, rejected, err := parseCSV(b, "DGS10", ingested)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 1 || received != 3 || rejected != 0 {
		t.Fatalf("obs=%v received=%d rejected=%d", obs, received, rejected)
	}
	if !obs[0].Temporal.AvailableAt.Equal(ingested) || obs[0].Temporal.ObservedAt.Equal(ingested) || obs[0].VintageAt == nil || !obs[0].VintageAt.Equal(ingested) {
		t.Fatalf("bad temporal semantics: %+v", obs[0].Temporal)
	}
}
