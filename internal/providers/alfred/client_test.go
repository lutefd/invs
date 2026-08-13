package alfred

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/luisdourado/invs/internal/model"
)

const testAPIKey = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type recordingGetter struct {
	responses map[string][]byte
	err       error
	urls      []string
}

func (g *recordingGetter) Get(_ context.Context, requestURL string) ([]byte, error) {
	g.urls = append(g.urls, requestURL)
	if g.err != nil {
		return nil, g.err
	}
	parsed, err := url.Parse(requestURL)
	if err != nil {
		return nil, err
	}
	offset := parsed.Query().Get("offset")
	body, ok := g.responses[offset]
	if !ok {
		return nil, fmt.Errorf("unexpected offset %q", offset)
	}
	return body, nil
}

func testSeries() Series {
	return Series{
		ID: "CPIAUCSL", Geography: "US", Unit: "index_1982_1984_100",
		Frequency: "monthly", SeasonalAdjustment: "seasonally_adjusted",
		RealtimeEnd:      "2024-12-31",
		ObservationStart: "2019-01-01", ObservationEnd: "2024-11-01",
	}
}

func TestCollectBuildsBoundedRequestAndKeepsCredentialsOutOfResult(t *testing.T) {
	body := []byte(`{"realtime_start":"2020-01-01","realtime_end":"2024-12-31","observation_start":"2019-01-01","observation_end":"2024-11-01","output_type":1,"count":1,"offset":0,"limit":100000,"observations":[{"realtime_start":"2020-02-13","realtime_end":"2020-03-10","date":"2020-01-01","value":"258.678"}]}`)
	getter := &recordingGetter{responses: map[string][]byte{"0": body}}
	client := NewClient(getter, testAPIKey)
	client.now = func() time.Time { return time.Date(2026, 8, 12, 12, 0, 0, 123456789, time.UTC) }

	result, err := client.Collect(context.Background(), testSeries())
	if err != nil {
		t.Fatal(err)
	}
	if len(getter.urls) != 1 || len(result.Pages) != 1 || len(result.Observations) != 1 {
		t.Fatalf("requests=%d pages=%d observations=%d", len(getter.urls), len(result.Pages), len(result.Observations))
	}
	request, err := url.Parse(getter.urls[0])
	if err != nil {
		t.Fatal(err)
	}
	query := request.Query()
	for key, want := range map[string]string{
		"api_key": testAPIKey, "file_type": "json", "series_id": "CPIAUCSL", "units": "lin",
		"output_type": "1", "realtime_start": EarliestRealtimeStart, "realtime_end": "2024-12-31",
		"observation_start": "2019-01-01", "observation_end": "2024-11-01",
		"sort_order": "asc", "limit": "100000", "offset": "0",
	} {
		if got := query.Get(key); got != want {
			t.Fatalf("query[%q]=%q, want %q", key, got, want)
		}
	}
	if strings.Contains(fmt.Sprintf("%+v", result), testAPIKey) {
		t.Fatal("result exposed API key")
	}
	page := result.Pages[0]
	if string(page.Bytes) != string(body) || page.SHA256 != digest(body) || page.Offset != 0 || page.Count != 1 || page.Limit != PageLimit {
		t.Fatalf("unexpected raw page: %+v", page)
	}
}

func TestCollectPreservesHistoricalVintagesAndConservativeAvailability(t *testing.T) {
	body := []byte(`{"output_type":1,"count":2,"offset":0,"limit":100000,"observations":[{"realtime_start":"2020-02-13","realtime_end":"2020-03-10","date":"2020-01-01","value":"258.6780"},{"realtime_start":"2020-03-11","realtime_end":"9999-12-31","date":"2020-01-01","value":"258.824"}]}`)
	getter := &recordingGetter{responses: map[string][]byte{"0": body}}
	client := NewClient(getter, testAPIKey)
	client.now = func() time.Time { return time.Date(2020, 3, 20, 17, 30, 0, 987654321, time.UTC) }

	result, err := client.Collect(context.Background(), testSeries())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Observations) != 2 || result.RecordsReceived != 2 || result.RecordsRejected != 0 || result.RecordsMissing != 0 {
		t.Fatalf("unexpected result counts: %+v", result)
	}
	for i, want := range []struct {
		published time.Time
		value     string
	}{{time.Date(2020, 2, 13, 0, 0, 0, 0, time.UTC), "258.678"}, {time.Date(2020, 3, 11, 0, 0, 0, 0, time.UTC), "258.824"}} {
		observation := result.Observations[i]
		if observation.Source != source || observation.Value != want.value || observation.Revision != i {
			t.Fatalf("observation[%d] identity/value = %+v", i, observation)
		}
		if !observation.Temporal.PublishedAt.Equal(want.published) || !observation.Temporal.AvailableAt.Equal(want.published.Add(36*time.Hour)) {
			t.Fatalf("observation[%d] temporal = %+v", i, observation.Temporal)
		}
		if observation.Temporal.PublishedPrecision != model.PrecisionDate || observation.VintageAt == nil || !observation.VintageAt.Equal(want.published) {
			t.Fatalf("observation[%d] vintage semantics = %+v", i, observation)
		}
		if observation.Provenance.RawPayloadHash != digest(body) || !strings.Contains(observation.Provenance.RawRecordLocator, fmt.Sprintf("observations/%d", i)) {
			t.Fatalf("observation[%d] provenance = %+v", i, observation.Provenance)
		}
	}
}

func TestCollectPaginatesAndRetainsEveryRawPage(t *testing.T) {
	page0 := []byte(`{"output_type":1,"count":3,"offset":0,"limit":2,"observations":[{"realtime_start":"2020-02-01","realtime_end":"9999-12-31","date":"2020-01-01","value":"1"},{"realtime_start":"2020-03-01","realtime_end":"9999-12-31","date":"2020-02-01","value":"2"}]}`)
	page2 := []byte(`{"output_type":1,"count":3,"offset":2,"limit":2,"observations":[{"realtime_start":"2020-04-01","realtime_end":"9999-12-31","date":"2020-03-01","value":"3"}]}`)
	getter := &recordingGetter{responses: map[string][]byte{"0": page0, "2": page2}}
	client := NewClient(getter, testAPIKey)
	client.now = func() time.Time { return time.Date(2020, 5, 1, 0, 0, 0, 0, time.UTC) }

	result, err := client.Collect(context.Background(), testSeries())
	if err != nil {
		t.Fatal(err)
	}
	if len(getter.urls) != 2 || len(result.Pages) != 2 || len(result.Observations) != 3 {
		t.Fatalf("requests=%d pages=%d observations=%d", len(getter.urls), len(result.Pages), len(result.Observations))
	}
	if result.Pages[0].Offset != 0 || result.Pages[0].SHA256 != digest(page0) || result.Pages[1].Offset != 2 || result.Pages[1].SHA256 != digest(page2) {
		t.Fatalf("unexpected pages: %+v", result.Pages)
	}
}

func TestCollectRetainsRawOnParseError(t *testing.T) {
	body := []byte(`{"output_type":1,"count":1,"offset":0,"limit":100000,"observations":[`)
	getter := &recordingGetter{responses: map[string][]byte{"0": body}}
	client := NewClient(getter, testAPIKey)

	result, err := client.Collect(context.Background(), testSeries())
	if err == nil {
		t.Fatal("expected parse error")
	}
	if len(result.Pages) != 1 || string(result.Pages[0].Bytes) != string(body) || result.Pages[0].SHA256 != digest(body) {
		t.Fatalf("raw page was not retained: %+v", result.Pages)
	}
	if len(result.Resources) != 1 || string(result.Resources[0].Bytes) != string(body) || result.Resources[0].SHA256 != digest(body) || result.Resources[0].Kind != "series_page" || result.Resources[0].ContentType != "application/json" || result.Resources[0].FetchedAt.IsZero() {
		t.Fatalf("common downloaded resource was not retained: %+v", result.Resources)
	}
}

func TestCollectClassifiesMissingInvalidAndNotYetSafelyAvailableRows(t *testing.T) {
	body := []byte(`{"output_type":1,"count":4,"offset":0,"limit":100000,"observations":[{"realtime_start":"2020-02-01","realtime_end":"9999-12-31","date":"2020-01-01","value":"."},{"realtime_start":"2020-02-01","realtime_end":"9999-12-31","date":"bad","value":"1"},{"realtime_start":"2020-02-01","realtime_end":"9999-12-31","date":"2020-01-01","value":"NaN"},{"realtime_start":"2020-02-02","realtime_end":"9999-12-31","date":"2020-01-01","value":"2"}]}`)
	getter := &recordingGetter{responses: map[string][]byte{"0": body}}
	client := NewClient(getter, testAPIKey)
	client.now = func() time.Time { return time.Date(2020, 2, 2, 12, 0, 0, 0, time.UTC) }

	result, err := client.Collect(context.Background(), testSeries())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Observations) != 1 || result.Observations[0].Value != "" || result.Observations[0].Revision != 0 || result.RecordsReceived != 4 || result.RecordsMissing != 1 || result.RecordsRejected != 3 {
		t.Fatalf("unexpected classifications: %+v", result)
	}
}

func TestCollectRejectsInvalidKeyBeforeHTTPAndRedactsKeyFromErrors(t *testing.T) {
	getter := &recordingGetter{responses: map[string][]byte{}}
	if _, err := NewClient(getter, "short").Collect(context.Background(), testSeries()); err == nil {
		t.Fatal("invalid API key accepted")
	}
	if len(getter.urls) != 0 {
		t.Fatal("HTTP called for invalid API key")
	}

	getter.err = errors.New("request failed for https://example.invalid?api_key=" + testAPIKey)
	_, err := NewClient(getter, testAPIKey).Collect(context.Background(), testSeries())
	if err == nil {
		t.Fatal("expected HTTP error")
	}
	if strings.Contains(err.Error(), testAPIKey) || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("API key was not redacted: %v", err)
	}
}
