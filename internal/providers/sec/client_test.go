package sec

import (
	"context"
	"strings"
	"testing"
	"time"
)

type fakeGetter struct{ responses map[string][]byte }

func (f fakeGetter) Get(_ context.Context, u string) ([]byte, error) {
	for suffix, b := range f.responses {
		if strings.HasSuffix(u, suffix) {
			return b, nil
		}
	}
	return nil, context.Canceled
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
	if f.Temporal.PublishedPrecision != "second" || !f.Temporal.AvailableAt.Equal(time.Date(2024, 2, 2, 21, 3, 4, 0, time.UTC)) {
		t.Fatalf("unsafe availability: %+v", f.Temporal)
	}
	if f.Value != "123.5" || f.IssuerID != "issuer-1" || f.RawPayloadHash == "" {
		t.Fatalf("bad fact: %+v", f)
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
}

func TestSubmissionsRejectsCIKMismatch(t *testing.T) {
	b := []byte(`{"cik":"0000000002","name":"Wrong","filings":{"recent":{}}}`)
	if _, _, _, _, err := parseSubmissions(b, "issuer", 1, time.Now()); err == nil {
		t.Fatal("CIK mismatch accepted")
	}
}
