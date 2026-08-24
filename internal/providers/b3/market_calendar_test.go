package b3

import (
	"bytes"
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/luisdourado/invs/internal/httpx"
	"github.com/luisdourado/invs/internal/providers"
)

type marketCalendarGetter struct {
	body     []byte
	requests []string
}

func (g *marketCalendarGetter) Get(_ context.Context, requestURL string) ([]byte, error) {
	g.requests = append(g.requests, requestURL)
	return append([]byte(nil), g.body...), nil
}

func marketCalendarFixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/market-calendar-2026.html")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestCollectMarketCalendarRetainsRawAndParsesListedEvents(t *testing.T) {
	body := marketCalendarFixture(t)
	getter := &marketCalendarGetter{body: body}
	client := NewClient(getter)
	client.marketCalendarBaseURL = "https://b3.test/calendar/holidays"
	client.now = func() time.Time {
		return time.Date(2026, 8, 23, 17, 18, 19, 123456789, time.FixedZone("BRT", -3*60*60))
	}

	result, err := client.CollectMarketCalendar(context.Background(), MarketCalendarRequest{Year: 2026})
	if err != nil {
		t.Fatal(err)
	}
	if len(getter.requests) != 1 || getter.requests[0] != "https://b3.test/calendar/holidays" {
		t.Fatalf("requests = %#v", getter.requests)
	}
	if len(result.Resources) != 1 {
		t.Fatalf("resources = %+v", result.Resources)
	}
	resource := result.Resources[0]
	if resource.Kind != MarketCalendarResourceKind || resource.ParserVersion != MarketCalendarParserVersion || resource.URL != getter.requests[0] {
		t.Fatalf("resource = %+v", resource)
	}
	if !bytes.Equal(resource.Bytes, body) || resource.SHA256 != providers.SHA256(body) {
		t.Fatal("market-calendar HTML was not retained exactly")
	}
	if result.Stats.RowsReceived != 4 || result.Stats.RowsRejected != 0 || result.Stats.Duplicates != 0 || result.Stats.ListedRows != 3 || len(result.Events) != 3 {
		t.Fatalf("stats/events = %+v/%+v", result.Stats, result.Events)
	}
	if resource.ParserMetadata["year"] != "2026" || resource.ParserMetadata["closed_rows"] != "2" || resource.ParserMetadata["special_hours_rows"] != "1" {
		t.Fatalf("parser metadata = %+v", resource.ParserMetadata)
	}

	if result.Events[0].Date.Format(time.DateOnly) != "2026-01-01" || !result.Events[0].IsClosed || result.Events[0].Status != "closed" {
		t.Fatalf("first event = %+v", result.Events[0])
	}
	if result.Events[1].Date.Format(time.DateOnly) != "2026-02-16" || !result.Events[1].IsClosed {
		t.Fatalf("second event = %+v", result.Events[1])
	}
	if result.Events[2].Date.Format(time.DateOnly) != "2026-02-18" || result.Events[2].IsClosed || !result.Events[2].IsSpecialHours || result.Events[2].Status != "special_hours" || !strings.Contains(result.Events[2].ListedDescription, "1:00 p.m.") {
		t.Fatalf("special-hours event = %+v", result.Events[2])
	}
}

func TestParseMarketCalendarRequiresListedEvidence(t *testing.T) {
	body := []byte(`<h2>Market Calendar 2026</h2><li class="accordion-navigation"><a href="#x">March</a><table><tr><td>01</td><td>FX only</td><td></td><td>Foreign Exchange Clearinghouse</td></tr></table></li>`)
	if _, _, err := parseMarketCalendar(body, 2026); err == nil || !strings.Contains(err.Error(), "no listed-market events") {
		t.Fatalf("error = %v, want missing listed evidence", err)
	}
	if _, err := normalizeMarketCalendarRequest(MarketCalendarRequest{Year: 1999}); err == nil {
		t.Fatal("out-of-range calendar year accepted")
	}
}

func TestLiveB3MarketCalendar(t *testing.T) {
	if os.Getenv("INVS_B3_CALENDAR_LIVE") != "1" {
		t.Skip("set INVS_B3_CALENDAR_LIVE=1 to fetch the public B3 market calendar")
	}
	yearText := strings.TrimSpace(os.Getenv("B3_CALENDAR_YEAR"))
	if yearText == "" {
		yearText = "2026"
	}
	year, err := strconv.Atoi(yearText)
	if err != nil {
		t.Fatalf("B3_CALENDAR_YEAR: %v", err)
	}
	getter, err := httpx.New(httpx.Config{
		UserAgent:         "invs-b3-calendar-live-acceptance research@example.com",
		Timeout:           90 * time.Second,
		RequestsPerSecond: 1,
		Burst:             1,
		MaxAttempts:       2,
		InitialBackoff:    250 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewClient(getter).CollectMarketCalendar(context.Background(), MarketCalendarRequest{Year: year})
	if err != nil {
		t.Fatal(err)
	}
	closed := 0
	specialHours := 0
	for _, event := range result.Events {
		if event.IsClosed {
			closed++
		}
		if event.IsSpecialHours {
			specialHours++
		}
	}
	if len(result.Events) < 5 || closed == 0 || specialHours == 0 {
		t.Fatalf("incomplete live calendar events=%d closed=%d special_hours=%d stats=%+v", len(result.Events), closed, specialHours, result.Stats)
	}
	t.Logf("year=%d bytes=%d sha256=%s listed_events=%d closed=%d special_hours=%d rows_received=%d rows_rejected=%d", year, len(result.Resources[0].Bytes), result.Resources[0].SHA256, len(result.Events), closed, specialHours, result.Stats.RowsReceived, result.Stats.RowsRejected)
}
