package nasdaq

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	dateOnlyBody := []byte(strings.Replace(string(body), "Sep 23, 2026 3:10 PM", "Sep 24, 2026", 1))
	dateOnlyRetrieved := time.Date(2026, 9, 24, 20, 0, 0, 0, time.UTC)
	dateOnlySnapshot, err := ParseCurrentUniverse(dateOnlyBody, dateOnlyRetrieved)
	if err != nil {
		t.Fatal(err)
	}
	if got := dateOnlySnapshot.AsOf.Format(time.RFC3339); got != "2026-09-24T04:00:00Z" {
		t.Fatalf("date-only source as-of = %s", got)
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

func TestFetchGIWCurrentUniverseUsesAuthenticatedEODRequestAndParsesFullMembership(t *testing.T) {
	eastern, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	retrieved := time.Date(2026, 9, 26, 12, 0, 0, 0, eastern)
	body := giwFixtureBody(101)
	client := currentUniverseDoer(func(request *http.Request) (*http.Response, error) {
		form, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		values, err := url.ParseQuery(string(form))
		if err != nil {
			t.Fatal(err)
		}
		query := request.URL.Query()
		if request.Method != http.MethodPost || request.URL.Host != "indexes.nasdaqomx.com" || request.URL.Path != "/reports2/UFFWeighting.ashx" ||
			query.Get("IndexSymbol") != "NDX" || query.Get("Date") != "2026-09-25" || query.Get("Type") != "PIPE" || query.Get("FileType") != "EOD" ||
			values.Get("username") != "giw-user" || values.Get("password") != "giw-pass" || request.URL.RawQuery == "" ||
			request.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Fatalf("unexpected GIW request: method=%s url=%s headers=%v form=%s", request.Method, request.URL, request.Header, string(form))
		}
		if strings.Contains(request.URL.String(), "giw-pass") {
			t.Fatal("GIW password was included in the request URL")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body))), Request: request}, nil
	})
	snapshot, raw, err := FetchGIWCurrentUniverse(context.Background(), client, "giw-user", "giw-pass", "invs-test", retrieved)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Count != 101 || len(snapshot.Members) != 101 || snapshot.Source != CurrentUniverseSourceGIW || snapshot.SourceURL != GIWWeightingURL || len(raw) == 0 {
		t.Fatalf("unexpected GIW snapshot: count=%d source=%s url=%s raw=%d", snapshot.Count, snapshot.Source, snapshot.SourceURL, len(raw))
	}
	if got := snapshot.AsOf.In(eastern).Format(time.DateOnly); got != "2026-09-25" {
		t.Fatalf("GIW as-of date=%s", got)
	}
}

func TestParseGIWCurrentUniverseRejectsPartialAndDuplicateFiles(t *testing.T) {
	eastern, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	date := time.Date(2026, 9, 25, 0, 0, 0, 0, eastern)
	if _, err := ParseGIWCurrentUniverse(giwFixtureBody(89), date, date.Add(time.Hour)); err == nil {
		t.Fatal("partial GIW file was accepted")
	}
	duplicate := strings.Join([]string{
		"NDX2026-09-25|EOD",
		"AAPL|100|10|2|0.1|Apple Inc.",
		"AAPL|100|10|2|0.1|Apple Inc.",
	}, "\n")
	if _, err := ParseGIWCurrentUniverse([]byte(duplicate), date, date.Add(time.Hour)); err == nil {
		t.Fatal("duplicate GIW constituents were accepted")
	}
}

func TestFetchGIWCurrentUniverseRejectsUnauthenticatedResponses(t *testing.T) {
	eastern, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	client := currentUniverseDoer(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("<html>Log in to access this report</html>")), Request: request}, nil
	})
	_, _, err = FetchGIWCurrentUniverse(context.Background(), client, "giw-user", "giw-pass", "invs-test", time.Date(2026, 9, 23, 22, 0, 0, 0, eastern))
	if err == nil || !strings.Contains(err.Error(), "authentication page") {
		t.Fatalf("authentication response error=%v", err)
	}
}

func TestFetchGIWCurrentUniverseRetriesTransientHTTPFailures(t *testing.T) {
	eastern, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	body := giwFixtureBody(100)
	client := currentUniverseDoer(func(request *http.Request) (*http.Response, error) {
		calls++
		status := http.StatusOK
		responseBody := body
		if calls == 1 {
			status = http.StatusBadGateway
			responseBody = []byte("temporary upstream error")
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(responseBody))), Request: request}, nil
	})
	snapshot, _, err := FetchGIWCurrentUniverse(context.Background(), client, "giw-user", "giw-pass", "invs-test", time.Date(2026, 9, 26, 12, 0, 0, 0, eastern))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || snapshot.Count != 100 {
		t.Fatalf("GIW attempts/count=%d/%d, want 2/100", calls, snapshot.Count)
	}
}

func giwFixtureBody(count int) []byte {
	rows := make([]string, 0, count+1)
	rows = append(rows, "NDX2026-09-25|EOD")
	for i := 0; i < count; i++ {
		rows = append(rows, fmt.Sprintf("S%03d|100|1000|10|0.01|Sample Company %03d Inc.", i, i))
	}
	return []byte(strings.Join(rows, "\n"))
}
