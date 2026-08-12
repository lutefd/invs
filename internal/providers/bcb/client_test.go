package bcb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/luisdourado/invs/internal/model"
)

type fakeGetter struct {
	body  []byte
	err   error
	url   string
	calls int
}

func (f *fakeGetter) Get(_ context.Context, requestURL string) ([]byte, error) {
	f.calls++
	f.url = requestURL
	return f.body, f.err
}

func testSeries() Series {
	return Series{
		Code: "432", Geography: "BR", Unit: "percent", Frequency: "daily",
		SeasonalAdjustment: "not_adjusted",
	}
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestCollectBuildsEncodedSGSURLAndPreservesRawHash(t *testing.T) {
	body := []byte("\"data\";\"valor\"\r\n\"01/01/2024\";\"14,25\"\r\n")
	fake := &fakeGetter{body: body}
	c := NewClient(fake)
	c.baseURL = "https://example.test/root/"
	ingested := time.Date(2024, 1, 2, 12, 30, 0, 0, time.UTC)
	c.now = func() time.Time { return ingested }

	series := testSeries()
	series.Start = "2024-01-01"
	series.End = "2024-01-31"
	result, err := c.Collect(context.Background(), series)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(fake.url)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/root/bcdata.sgs.432/dados" {
		t.Fatalf("request path = %q", parsed.Path)
	}
	if parsed.Query().Get("formato") != "csv" || parsed.Query().Get("dataInicial") != "01/01/2024" || parsed.Query().Get("dataFinal") != "31/01/2024" {
		t.Fatalf("request query = %v", parsed.Query())
	}
	if !strings.Contains(parsed.RawQuery, "dataInicial=01%2F01%2F2024") || !strings.Contains(parsed.RawQuery, "dataFinal=31%2F01%2F2024") {
		t.Fatalf("date query parameters were not URL encoded: %q", parsed.RawQuery)
	}
	if result.RecordsReceived != 1 || result.RecordsRejected != 0 || result.RecordsMissing != 0 || len(result.Observations) != 1 {
		t.Fatalf("counts = received %d rejected %d missing %d observations %d", result.RecordsReceived, result.RecordsRejected, result.RecordsMissing, len(result.Observations))
	}
	if !bytes.Equal(result.Raw, body) || result.SHA256 != sha256Hex(body) {
		t.Fatalf("raw/hash not preserved: raw=%q hash=%q", result.Raw, result.SHA256)
	}
}

func TestParseCSVCanonicalizesExactDecimalsAndHandlesRowsDeterministically(t *testing.T) {
	ingested := time.Date(2024, 2, 1, 12, 30, 0, 0, time.UTC)
	body := []byte(strings.Join([]string{
		`"data";"valor"`,
		`"01/01/2024";"12345678901234567890,12345678901234567891"`,
		`"02/01/2024";"-"`,
		`"03/01/2024";"1,2300"`,
		`"03/01/2024";"2,5000"`,
		`"04/01/2024";""`,
		`"05/01/2024";"NaN"`,
		`"06/01/2024";"Inf"`,
		`"07/01/2024";"1e3"`,
		`"08/01/2024";"1.25"`,
		`"31/02/2024";"1,0"`,
		`"09/01/2024";"1,0";"extra"`,
	}, "\r\n"))
	series := testSeries()
	observations, received, rejected, missing, err := parseCSV(body, series, ingested)
	if err != nil {
		t.Fatal(err)
	}
	if received != 11 || rejected != 6 || missing != 2 {
		t.Fatalf("counts = received %d rejected %d missing %d", received, rejected, missing)
	}
	if len(observations) != 2 {
		t.Fatalf("observations = %d, want 2", len(observations))
	}
	if got, want := observations[0].Value, "12345678901234567890.12345678901234567891"; got != want {
		t.Fatalf("exact decimal = %q, want %q", got, want)
	}
	if got, want := observations[1].Value, "2.5"; got != want {
		t.Fatalf("duplicate winner = %q, want %q", got, want)
	}
	if got := observations[1].Provenance.RawRecordLocator; got != "csv/date=2024-01-03" {
		t.Fatalf("record locator = %q", got)
	}
	for _, observation := range observations {
		if observation.Source != "bcb" || observation.SeriesID != "432" || observation.Geography != "BR" || observation.Unit != "percent" || observation.Frequency != "daily" || observation.SeasonalAdjustment != "not_adjusted" {
			t.Fatalf("metadata not copied: %+v", observation)
		}
		if observation.Temporal.ObservedPrecision != model.PrecisionDate || observation.Temporal.PublishedPrecision != model.PrecisionUnknown {
			t.Fatalf("precision = %+v", observation.Temporal)
		}
		if !observation.Temporal.PublishedAt.Equal(ingested) || !observation.Temporal.AvailableAt.Equal(ingested) || !observation.Temporal.IngestedAt.Equal(ingested) || observation.VintageAt == nil || !observation.VintageAt.Equal(ingested) {
			t.Fatalf("availability semantics = %+v vintage=%v", observation.Temporal, observation.VintageAt)
		}
	}
}

func TestCollectRetainsRawOnMalformedCSV(t *testing.T) {
	cases := map[string][]byte{
		"unterminated quote": []byte("\"data\";\"valor\"\r\n\"01/01/2024;\"1,0\"\r\n"),
		"wrong header":       []byte("\"date\";\"valor\"\r\n\"01/01/2024\";\"1,0\"\r\n"),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			fake := &fakeGetter{body: body}
			c := NewClient(fake)
			c.now = func() time.Time { return time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC) }
			result, err := c.Collect(context.Background(), testSeries())
			if err == nil {
				t.Fatal("expected parse error")
			}
			if !bytes.Equal(result.Raw, body) || result.SHA256 != sha256Hex(body) {
				t.Fatalf("raw/hash not retained: raw=%q hash=%q", result.Raw, result.SHA256)
			}
		})
	}
}

func TestCanonicalValueRejectsNonFiniteAndNonSGSDecimalForms(t *testing.T) {
	for raw, want := range map[string]string{
		"1,2300":                             "1.23",
		"0,0000000000000000000001":           "0.0000000000000000000001",
		"-12,5000":                           "-12.5",
		"12345678901234567890,1234567890123": "12345678901234567890.1234567890123",
	} {
		got, err := canonicalValue(raw)
		if err != nil || got != want {
			t.Errorf("canonicalValue(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"NaN", "Inf", "-Inf", "1e3", "+1,0", "1.2", "1,2,3", "1,", ",5", " 1,2"} {
		if got, err := canonicalValue(raw); err == nil {
			t.Errorf("canonicalValue(%q) = %q, accepted invalid value", raw, got)
		}
	}
}

func TestCollectRejectsInvalidSeriesBeforeHTTP(t *testing.T) {
	cases := map[string]Series{
		"missing code":      func() Series { s := testSeries(); s.Code = ""; return s }(),
		"noncanonical code": func() Series { s := testSeries(); s.Code = "00432"; return s }(),
		"invalid frequency": func() Series { s := testSeries(); s.Frequency = "business_daily"; return s }(),
		"reversed dates":    func() Series { s := testSeries(); s.Start, s.End = "2024-02-01", "2024-01-01"; return s }(),
	}
	for name, series := range cases {
		t.Run(name, func(t *testing.T) {
			fake := &fakeGetter{}
			_, err := NewClient(fake).Collect(context.Background(), series)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if fake.calls != 0 {
				t.Fatalf("HTTP called %d times for invalid series", fake.calls)
			}
		})
	}
}
