package fred

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

type fakeGetter struct{ body []byte }

func (f fakeGetter) Get(context.Context, string) ([]byte, error) { return f.body, nil }

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
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
	if !obs[0].Temporal.AvailableAt.Equal(ingested) || obs[0].Temporal.ObservedAt.Equal(ingested) || obs[0].VintageAt == nil || !obs[0].VintageAt.Equal(ingested) {
		t.Fatalf("bad temporal semantics: %+v", obs[0].Temporal)
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
		})
	}
}
