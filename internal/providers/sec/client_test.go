package sec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeGetter struct {
	responses map[string][]byte
	failures  map[string]error
}

func (f fakeGetter) Get(_ context.Context, u string) ([]byte, error) {
	for suffix, err := range f.failures {
		if strings.HasSuffix(u, suffix) {
			return nil, err
		}
	}
	for suffix, b := range f.responses {
		if strings.HasSuffix(u, suffix) {
			return b, nil
		}
	}
	return nil, context.Canceled
}

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

func TestCollectCompanyNormalizesAndDeduplicates(t *testing.T) {
	sub := []byte(`{"cik":"0000000001","sic":"3571","name":"Example Corp","stateOfIncorporation":"DE","sicDescription":"Widgets","filings":{"recent":{"accessionNumber":["0001","0001"],"filingDate":["2024-02-02","2024-02-02"],"acceptanceDateTime":["2024-02-02T21:03:04.000Z","2024-02-02T21:03:04.000Z"],"form":["10-K","10-K"],"primaryDocument":["x.htm","x.htm"]}}}`)
	facts := []byte(`{"cik":1,"facts":{"us-gaap":{"Revenue":{"label":"Revenue","units":{"USD":[{"start":"2023-01-01","end":"2023-12-31","val":123.5,"accn":"0001","fy":2023,"fp":"FY","form":"10-K","filed":"2024-02-02"},{"start":"2023-01-01","end":"2023-12-31","val":123.5,"accn":"0001","fy":2023,"fp":"FY","form":"10-K","filed":"2024-02-02"}]}}}}}`)
	c := NewClient(fakeGetter{responses: map[string][]byte{"submissions/CIK0000000001.json": sub, "companyfacts/CIK0000000001.json": facts}})
	c.now = func() time.Time { return time.Date(2024, 2, 3, 12, 0, 0, 0, time.UTC) }
	r, err := c.CollectCompany(context.Background(), "issuer-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Filings) != 1 || len(r.Facts) != 1 || r.RecordsReceived != 4 {
		t.Fatalf("unexpected result: %+v", r)
	}
	f := r.Facts[0]
	if f.Temporal.ObservedPrecision != "date" || f.Temporal.PublishedPrecision != "second" || !f.Temporal.AvailableAt.Equal(time.Date(2024, 2, 2, 21, 3, 4, 0, time.UTC)) {
		t.Fatalf("unsafe availability: %+v", f.Temporal)
	}
	if f.Value != "123.5" || f.IssuerID != "issuer-1" || f.RawPayloadHash == "" {
		t.Fatalf("bad fact: %+v", f)
	}
}

func TestCollectCompanyTruncatesInjectedReceiptTime(t *testing.T) {
	sub := []byte(`{"cik":"0000000001","name":"Example Corp","filings":{"recent":{"accessionNumber":["0001"],"filingDate":["2024-02-02"],"acceptanceDateTime":["2024-02-02T21:03:04.123Z"],"form":["10-K"],"primaryDocument":["x.htm"]}}}`)
	facts := []byte(`{"cik":1,"facts":{"us-gaap":{"Revenue":{"units":{"USD":[{"end":"2023-12-31","val":123.5,"accn":"0001","fy":2023,"fp":"FY","form":"10-K","filed":"2024-02-02"}]}}}}}`)
	receivedAt := time.Date(2024, 2, 3, 12, 0, 0, 987654321, time.FixedZone("BRT", -3*60*60))
	wantIngested := receivedAt.UTC().Truncate(time.Microsecond)
	c := NewClient(fakeGetter{responses: map[string][]byte{
		"submissions/CIK0000000001.json":  sub,
		"companyfacts/CIK0000000001.json": facts,
	}})
	c.now = func() time.Time { return receivedAt }

	r, err := c.CollectCompany(context.Background(), "issuer-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Filings) != 1 || len(r.Facts) != 1 {
		t.Fatalf("filings=%d facts=%d, want one of each", len(r.Filings), len(r.Facts))
	}
	assertMicrosecondUTC(t, "filing ingested_at", r.Filings[0].IngestedAt)
	if !r.Filings[0].IngestedAt.Equal(wantIngested) {
		t.Fatalf("filing ingested_at = %s, want %s", r.Filings[0].IngestedAt, wantIngested)
	}

	f := r.Facts[0]
	assertMicrosecondUTC(t, "temporal published_at", f.Temporal.PublishedAt)
	assertMicrosecondUTC(t, "temporal available_at", f.Temporal.AvailableAt)
	assertMicrosecondUTC(t, "temporal ingested_at", f.Temporal.IngestedAt)
	assertMicrosecondUTC(t, "provenance ingested_at", f.Provenance.IngestedAt)
	if !f.Temporal.IngestedAt.Equal(wantIngested) || !f.Provenance.IngestedAt.Equal(wantIngested) {
		t.Fatalf("receipt timestamps = temporal %s provenance %s, want %s", f.Temporal.IngestedAt, f.Provenance.IngestedAt, wantIngested)
	}
	wantPublished := time.Date(2024, 2, 2, 21, 3, 4, 123000000, time.UTC)
	if !f.Temporal.PublishedAt.Equal(wantPublished) || !f.Temporal.AvailableAt.Equal(wantPublished) {
		t.Fatalf("source publication timestamps = published %s available %s, want %s", f.Temporal.PublishedAt, f.Temporal.AvailableAt, wantPublished)
	}
	if !f.Temporal.ObservedAt.Equal(time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)) || f.Temporal.ObservedPrecision != "date" || f.Temporal.PublishedPrecision != "second" {
		t.Fatalf("source precision mapping changed: %+v", f.Temporal)
	}
}

func TestInvalidFactTimestampRejected(t *testing.T) {
	facts := []byte(`{"facts":{"us-gaap":{"Revenue":{"units":{"USD":[{"end":"bad","val":1,"accn":"1","filed":"2024-01-01"}]}}}}}`)
	got, received, rejected, err := parseCompanyFacts(facts, "issuer", 0, time.Now(), nil)
	if err != nil || len(got) != 0 || received != 1 || rejected != 1 {
		t.Fatalf("got=%v received=%d rejected=%d err=%v", got, received, rejected, err)
	}
}

func TestFactWithoutAcceptanceUsesFortyEightHourFallback(t *testing.T) {
	facts := []byte(`{"facts":{"us-gaap":{"Revenue":{"units":{"USD":[{"end":"2023-12-31","val":1,"accn":"1","filed":"2024-01-01"}]}}}}}`)
	got, _, _, err := parseCompanyFacts(facts, "issuer", 0, time.Now(), nil)
	if err != nil || len(got) != 1 {
		t.Fatal(err)
	}
	if want := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC); !got[0].Temporal.AvailableAt.Equal(want) {
		t.Fatalf("got %s want %s", got[0].Temporal.AvailableAt, want)
	}
	if !got[0].Temporal.PublishedAt.Equal(got[0].Temporal.AvailableAt) {
		t.Fatal("published_at exposed unsafe filed midnight")
	}
	if got[0].Temporal.ObservedPrecision != "date" {
		t.Fatalf("observed precision=%q want date", got[0].Temporal.ObservedPrecision)
	}
}

func TestSubmissionsRejectsCIKMismatch(t *testing.T) {
	b := []byte(`{"cik":"0000000002","name":"Wrong","filings":{"recent":{}}}`)
	if _, _, _, _, err := parseSubmissions(b, "issuer", 1, time.Now()); err == nil {
		t.Fatal("CIK mismatch accepted")
	}
}

func TestCollectCompanyRetainsRawOnParseError(t *testing.T) {
	validSubmissions := []byte(`{"cik":"0000000001","name":"Example Corp","filings":{"recent":{}}}`)
	validFacts := []byte(`{"cik":1,"facts":{}}`)
	cases := map[string]struct {
		submissions []byte
		facts       []byte
	}{
		"malformed submissions JSON": {
			submissions: []byte(`{"cik":`),
			facts:       validFacts,
		},
		"invalid companyfacts schema": {
			submissions: validSubmissions,
			facts:       []byte(`{"cik":2,"facts":{}}`),
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := NewClient(fakeGetter{responses: map[string][]byte{
				"submissions/CIK0000000001.json":  tc.submissions,
				"companyfacts/CIK0000000001.json": tc.facts,
			}})
			r, err := c.CollectCompany(context.Background(), "issuer-1", 1)
			if err == nil {
				t.Fatal("expected parse error")
			}
			want := []RawDocument{
				{Kind: "submissions", Data: tc.submissions, SHA256: sha256Hex(tc.submissions)},
				{Kind: "companyfacts", Data: tc.facts, SHA256: sha256Hex(tc.facts)},
			}
			if len(r.Raw) != len(want) {
				t.Fatalf("raw documents=%d want %d", len(r.Raw), len(want))
			}
			for i := range want {
				if r.Raw[i].Kind != want[i].Kind || !bytes.Equal(r.Raw[i].Data, want[i].Data) || r.Raw[i].SHA256 != want[i].SHA256 {
					t.Fatalf("raw[%d]=%+v want %+v", i, r.Raw[i], want[i])
				}
				if r.Resources[i].Kind != want[i].Kind || !bytes.Equal(r.Resources[i].Bytes, want[i].Data) || r.Resources[i].SHA256 != want[i].SHA256 || r.Resources[i].ContentType != "application/json" || r.Resources[i].FetchedAt.IsZero() {
					t.Fatalf("common resource[%d]=%+v want the downloaded response and metadata", i, r.Resources[i])
				}
			}
		})
	}
}

func TestCollectCompanyRetainsSubmissionsOnCompanyFactsTransportError(t *testing.T) {
	submissions := []byte(`{"cik":"0000000001","name":"Example Corp","filings":{"recent":{}}}`)
	transportErr := errors.New("companyfacts unavailable")
	c := NewClient(fakeGetter{
		responses: map[string][]byte{
			"submissions/CIK0000000001.json": submissions,
		},
		failures: map[string]error{
			"companyfacts/CIK0000000001.json": transportErr,
		},
	})

	r, err := c.CollectCompany(context.Background(), "issuer-1", 1)
	if !errors.Is(err, transportErr) {
		t.Fatalf("err=%v want wrapped transport error", err)
	}
	if len(r.Raw) != 1 {
		t.Fatalf("raw documents=%d want 1", len(r.Raw))
	}
	if len(r.Resources) != 1 {
		t.Fatalf("common downloaded resources=%d want 1", len(r.Resources))
	}
	got := r.Raw[0]
	if got.Kind != "submissions" || !bytes.Equal(got.Data, submissions) || got.SHA256 != sha256Hex(submissions) {
		t.Fatalf("raw=%+v want submissions payload with SHA-256 %s", got, sha256Hex(submissions))
	}
	if r.Resources[0].Kind != "submissions" || !bytes.Equal(r.Resources[0].Bytes, submissions) || r.Resources[0].SHA256 != sha256Hex(submissions) {
		t.Fatalf("common resource=%+v want submissions payload with SHA-256 %s", r.Resources[0], sha256Hex(submissions))
	}
}

func TestCollectCompanyReturnsNoRawOnSubmissionsTransportError(t *testing.T) {
	transportErr := errors.New("submissions unavailable")
	c := NewClient(fakeGetter{failures: map[string]error{
		"submissions/CIK0000000001.json": transportErr,
	}})

	r, err := c.CollectCompany(context.Background(), "issuer-1", 1)
	if !errors.Is(err, transportErr) {
		t.Fatalf("err=%v want wrapped transport error", err)
	}
	if len(r.Raw) != 0 {
		t.Fatalf("raw documents=%d want 0", len(r.Raw))
	}
}
