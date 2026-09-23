package nasdaq

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type currentUniverseDoer func(*http.Request) (*http.Response, error)

func (f currentUniverseDoer) Do(request *http.Request) (*http.Response, error) { return f(request) }

func TestParseCurrentUniverseValidatesCompleteCurrentSnapshot(t *testing.T) {
	rows := make([]string, 0, 101)
	for i := 0; i < 101; i++ {
		rows = append(rows, `{"symbol":"S`+string(rune('A'+i/26))+string(rune('A'+i%26))+`","companyName":"Sample Company Inc. Common Stock","sector":""}`)
	}
	body := []byte(`{"status":{"rCode":200},"data":{"date":"Sep 23, 2026 3:10 PM","totalrecords":101,"limit":200,"offset":0,"data":{"rows":[` + strings.Join(rows, ",") + `]}}}`)
	retrieved := time.Date(2026, 9, 23, 22, 0, 0, 987654321, time.FixedZone("BRT", -3*60*60))
	snapshot, err := ParseCurrentUniverse(body, retrieved)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Count != 101 || len(snapshot.Members) != 101 || snapshot.Members[0].Company == "" {
		t.Fatalf("unexpected snapshot summary: %+v", snapshot)
	}
	if got := snapshot.AsOf.Format(time.RFC3339); got != "2026-09-23T19:10:00Z" {
		t.Fatalf("source as-of = %s", got)
	}
	if snapshot.Retrieved.Location() != time.UTC || snapshot.Retrieved.Nanosecond()%int(time.Microsecond) != 0 || len(snapshot.SHA256) != 64 {
		t.Fatalf("snapshot receipt/hash not normalized: %+v", snapshot)
	}
}

func TestParseCurrentUniverseFailsClosedOnPartialAndDuplicateRows(t *testing.T) {
	body := []byte(`{"status":{"rCode":200},"data":{"date":"Sep 23, 2026 3:10 PM","totalrecords":90,"limit":200,"offset":0,"data":{"rows":[{"symbol":"AAPL","companyName":"Apple Inc."}]}}}`)
	if _, err := ParseCurrentUniverse(body, time.Now()); err == nil {
		t.Fatal("partial snapshot was accepted")
	}
	rows := make([]string, 100)
	for i := range rows {
		rows[i] = `{"symbol":"AAPL","companyName":"Apple Inc."}`
	}
	duplicate := []byte(`{"status":{"rCode":200},"data":{"date":"Sep 23, 2026 3:10 PM","totalrecords":100,"limit":200,"offset":0,"data":{"rows":[` + strings.Join(rows, ",") + `]}}}`)
	if _, err := ParseCurrentUniverse(duplicate, time.Now()); err == nil {
		t.Fatal("duplicate symbols were accepted")
	}
}

func TestFetchCurrentUniverseUsesNasdaqBrowserHeadersAndRetainsInvalidBody(t *testing.T) {
	body := []byte(`{"status":{"rCode":503},"data":{}}`)
	client := currentUniverseDoer(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != CurrentUniverseURL || request.Header.Get("Origin") != "https://www.nasdaq.com" || request.Header.Get("Referer") != "https://www.nasdaq.com/" || request.Header.Get("User-Agent") != "invs-test" {
			t.Fatalf("unexpected Nasdaq request: %s headers=%v", request.URL, request.Header)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body))), Request: request}, nil
	})
	_, raw, err := FetchCurrentUniverse(context.Background(), client, "invs-test", time.Now())
	if err == nil || string(raw) != string(body) {
		t.Fatalf("snapshot/raw/error = nil/%q/%v", raw, err)
	}
	if _, _, err := FetchCurrentUniverse(context.Background(), nil, "invs-test", time.Now()); err == nil {
		t.Fatal("nil HTTP client was accepted")
	}
}
