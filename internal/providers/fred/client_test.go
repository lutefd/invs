package fred

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/luisdourado/invs/internal/model"
)

type fakeGetter struct{ body []byte }

func (f fakeGetter) Get(context.Context, string) ([]byte, error) { return f.body, nil }

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func assertMicrosecondUTC(t *testing.T, name string, got time.Time) {
	t.Helper()
	if got.Location() != time.UTC || got.Nanosecond()%int(time.Microsecond) != 0 {
		t.Fatalf("%s = %s, want UTC microsecond precision", name, got)
	}
}

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
	if obs[0].Temporal.ObservedPrecision != model.PrecisionDate || !obs[0].Temporal.AvailableAt.Equal(ingested) || !obs[0].Temporal.PublishedAt.Equal(ingested) || obs[0].Temporal.PublishedPrecision != model.PrecisionUnknown || obs[0].Temporal.ObservedAt.Equal(ingested) || obs[0].VintageAt == nil || !obs[0].VintageAt.Equal(ingested) {
		t.Fatalf("bad temporal semantics: %+v", obs[0].Temporal)
	}
}

func TestCollectTruncatesInjectedReceiptTime(t *testing.T) {
	body := []byte("observation_date,DGS10\n2024-01-02,4.25\n")
	receivedAt := time.Date(2024, 8, 1, 12, 30, 0, 987654321, time.FixedZone("BRT", -3*60*60))
	wantIngested := receivedAt.UTC().Truncate(time.Microsecond)
	c := NewClient(fakeGetter{body: body})
	c.now = func() time.Time { return receivedAt }

	result, err := c.Collect(context.Background(), "DGS10")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Observations) != 1 {
		t.Fatalf("observations=%d, want 1", len(result.Observations))
	}
	observation := result.Observations[0]
	assertMicrosecondUTC(t, "temporal published_at", observation.Temporal.PublishedAt)
	assertMicrosecondUTC(t, "temporal available_at", observation.Temporal.AvailableAt)
	assertMicrosecondUTC(t, "temporal ingested_at", observation.Temporal.IngestedAt)
	assertMicrosecondUTC(t, "provenance ingested_at", observation.Provenance.IngestedAt)
	assertMicrosecondUTC(t, "vintage_at", *observation.VintageAt)
	for name, got := range map[string]time.Time{
		"temporal published_at":  observation.Temporal.PublishedAt,
		"temporal available_at":  observation.Temporal.AvailableAt,
		"temporal ingested_at":   observation.Temporal.IngestedAt,
		"provenance ingested_at": observation.Provenance.IngestedAt,
		"vintage_at":             *observation.VintageAt,
	} {
		if !got.Equal(wantIngested) {
			t.Fatalf("%s = %s, want %s", name, got, wantIngested)
		}
	}
	if !observation.Temporal.ObservedAt.Equal(time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)) || observation.Temporal.ObservedPrecision != model.PrecisionDate || observation.Temporal.PublishedPrecision != model.PrecisionUnknown {
		t.Fatalf("source observed timestamp/precision changed: %+v", observation.Temporal)
	}
}

func TestCollectRetainsRawOnParseError(t *testing.T) {
	cases := map[string][]byte{
		"malformed CSV":         []byte("observation_date,DGS10\n2024-01-01,\"4.2\n"),
		"invalid column schema": []byte("date,DGS10\n2024-01-01,4.2\n"),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			c := NewClient(fakeGetter{body: body})
			r, err := c.Collect(context.Background(), "DGS10")
			if err == nil {
				t.Fatal("expected parse error")
			}
			if !bytes.Equal(r.Raw, body) {
				t.Fatalf("raw=%q want %q", r.Raw, body)
			}
			if r.SHA256 != sha256Hex(body) {
				t.Fatalf("sha256=%q want %q", r.SHA256, sha256Hex(body))
			}
			if len(r.Resources) != 1 || string(r.Resources[0].Bytes) != string(body) || r.Resources[0].SHA256 != sha256Hex(body) || r.Resources[0].Kind != "series" || r.Resources[0].ContentType != "text/csv" || r.Resources[0].FetchedAt.IsZero() {
				t.Fatalf("common downloaded resource = %+v, want the malformed response and metadata", r.Resources)
			}
		})
	}
}
