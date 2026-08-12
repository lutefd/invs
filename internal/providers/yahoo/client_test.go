package yahoo

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

func TestChartURLDoesNotDoubleEscapeSymbols(t *testing.T) {
	cases := map[string]string{"AAPL": "AAPL", "^BVSP": "%5EBVSP", "BRK/B": "BRK%2FB"}
	for symbol, want := range cases {
		u, err := chartURL(defaultBaseURL, symbol)
		if err != nil {
			t.Fatal(err)
		}
		if got := u.EscapedPath(); got != "/v8/finance/chart/"+want {
			t.Errorf("%s: %s", symbol, got)
		}
	}
}

func TestParseNormalizesExchangeCloseAndRejectsMissing(t *testing.T) {
	b := []byte(`{"chart":{"result":[{"meta":{"currency":"USD","exchangeTimezoneName":"America/New_York"},"timestamp":[1719840600,1719927000],"indicators":{"quote":[{"open":[10,null],"high":[12,null],"low":[9,null],"close":[11,null],"volume":[100,null]}]}}],"error":null}}`)
	ingested := time.Date(2024, 7, 3, 22, 0, 0, 0, time.UTC)
	bars, received, rejected, err := parse(b, "security-1", "USD", ingested)
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 1 || received != 2 || rejected != 1 {
		t.Fatalf("bars=%v received=%d rejected=%d", bars, received, rejected)
	}
	want := time.Date(2024, 7, 1, 20, 0, 0, 0, time.UTC)
	if !bars[0].Temporal.ObservedAt.Equal(want) || bars[0].Temporal.ObservedPrecision != model.PrecisionSecond || bars[0].Source != "yahoo" {
		t.Fatalf("bar=%+v", bars[0])
	}
	if !bars[0].Temporal.AvailableAt.Equal(ingested) || !bars[0].Temporal.PublishedAt.Equal(ingested) {
		t.Fatalf("vendor publication time was fabricated: %+v", bars[0].Temporal)
	}
}

func TestCollectTruncatesInjectedReceiptTime(t *testing.T) {
	b := []byte(`{"chart":{"result":[{"meta":{"currency":"USD","exchangeTimezoneName":"America/New_York"},"timestamp":[1719840600],"indicators":{"quote":[{"open":[10],"high":[12],"low":[9],"close":[11],"volume":[100]}]}}],"error":null}}`)
	receivedAt := time.Date(2024, 7, 3, 22, 0, 0, 987654321, time.FixedZone("BRT", -3*60*60))
	wantIngested := receivedAt.UTC().Truncate(time.Microsecond)
	c := NewClient(fakeGetter{body: b})
	c.now = func() time.Time { return receivedAt }

	result, err := c.Collect(context.Background(), model.HistoricalPriceRequest{
		SecurityID: "security-1", VendorSymbol: "AAPL", Currency: "USD",
		Start: time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2024, 7, 3, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Bars) != 1 {
		t.Fatalf("bars=%d, want 1", len(result.Bars))
	}
	bar := result.Bars[0]
	assertMicrosecondUTC(t, "temporal published_at", bar.Temporal.PublishedAt)
	assertMicrosecondUTC(t, "temporal available_at", bar.Temporal.AvailableAt)
	assertMicrosecondUTC(t, "temporal ingested_at", bar.Temporal.IngestedAt)
	assertMicrosecondUTC(t, "provenance ingested_at", bar.Provenance.IngestedAt)
	for name, got := range map[string]time.Time{
		"temporal published_at":  bar.Temporal.PublishedAt,
		"temporal available_at":  bar.Temporal.AvailableAt,
		"temporal ingested_at":   bar.Temporal.IngestedAt,
		"provenance ingested_at": bar.Provenance.IngestedAt,
	} {
		if !got.Equal(wantIngested) {
			t.Fatalf("%s = %s, want %s", name, got, wantIngested)
		}
	}
	if !bar.Temporal.ObservedAt.Equal(time.Date(2024, 7, 1, 20, 0, 0, 0, time.UTC)) || bar.Temporal.ObservedPrecision != model.PrecisionSecond {
		t.Fatalf("source observed timestamp/precision changed: %+v", bar.Temporal)
	}
}

func TestParseRejectsIncompleteTradingDay(t *testing.T) {
	b := []byte(`{"chart":{"result":[{"meta":{"currency":"USD","exchangeTimezoneName":"America/New_York"},"timestamp":[1719840600],"indicators":{"quote":[{"open":[10],"high":[12],"low":[9],"close":[11],"volume":[100]}]}}],"error":null}}`)
	// Noon UTC is before the 16:00 New York close (20:00 UTC during DST).
	bars, received, rejected, err := parse(b, "security-1", "USD", time.Date(2024, 7, 1, 12, 0, 0, 0, time.UTC))
	if err != nil || len(bars) != 0 || received != 1 || rejected != 0 {
		t.Fatalf("bars=%v received=%d rejected=%d err=%v", bars, received, rejected, err)
	}
}

func TestCollectRetainsRawOnParseError(t *testing.T) {
	cases := map[string][]byte{
		"malformed JSON":       []byte(`{"chart":`),
		"invalid chart schema": []byte(`{"chart":{"result":[],"error":null}}`),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			c := NewClient(fakeGetter{body: body})
			r, err := c.Collect(context.Background(), model.HistoricalPriceRequest{
				SecurityID: "security-1", VendorSymbol: "AAPL", Currency: "USD",
				Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
				End:   time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
			})
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
