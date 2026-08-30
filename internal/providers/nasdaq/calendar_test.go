package nasdaq

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/luisdourado/invs/internal/httpx"
	"github.com/luisdourado/invs/internal/providers"
)

type calendarGetter struct {
	body     []byte
	err      error
	requests []string
}

func (g *calendarGetter) Get(_ context.Context, requestURL string) ([]byte, error) {
	g.requests = append(g.requests, requestURL)
	return append([]byte(nil), g.body...), g.err
}

func calendarFixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/calendar-2026.html")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestCollectCalendarRetainsRawAndParses2026(t *testing.T) {
	body := calendarFixture(t)
	getter := &calendarGetter{body: body}
	client := NewClient(getter)
	client.now = func() time.Time { return time.Date(2026, 8, 23, 20, 0, 0, 123456789, time.UTC) }
	result, err := client.CollectCalendar(context.Background(), CalendarRequest{Year: 2026})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Resources) != 1 || len(getter.requests) != 1 || getter.requests[0] != DefaultCalendarURL {
		t.Fatalf("resources/requests = %+v/%+v", result.Resources, getter.requests)
	}
	resource := result.Resources[0]
	if !bytes.Equal(resource.Bytes, body) || resource.SHA256 != providers.SHA256(body) || resource.ParserVersion != CalendarParserVersion || resource.Year != 2026 {
		t.Fatalf("resource = %+v", resource)
	}
	if result.CoreHours.OpenLocal != "09:30" || result.CoreHours.CloseLocal != "16:00" {
		t.Fatalf("core hours = %+v", result.CoreHours)
	}
	if len(result.Events) != 12 || countCalendarStatus(result.Events, "closed") != 10 || countCalendarStatus(result.Events, "early_close") != 2 {
		t.Fatalf("events = %+v", result.Events)
	}
	if result.Events[0].Date.Format(time.DateOnly) != "2026-01-01" || result.Events[0].Name != "New Years Day (Observed)" || result.Events[0].Status != "closed" {
		t.Fatalf("first event = %+v", result.Events[0])
	}
	if early := result.Events[9]; early.Date.Format(time.DateOnly) != "2026-11-27" || early.Status != "early_close" || early.SpecialCloseLocal != "13:00" {
		t.Fatalf("Thanksgiving early close = %+v", early)
	}
	if last := result.Events[len(result.Events)-1]; last.Date.Format(time.DateOnly) != "2026-12-25" || last.Status != "closed" {
		t.Fatalf("last event = %+v", last)
	}
	if resource.ParserMetadata["closed_dates"] != "10" || resource.ParserMetadata["early_close_dates"] != "2" || resource.ParserMetadata["mic"] != "XNAS" {
		t.Fatalf("metadata = %+v", resource.ParserMetadata)
	}
}

func TestParseCalendarFailsClosed(t *testing.T) {
	missingTable := []byte("<h1>U.S. Equity and Options Markets Holiday Schedule 2026</h1>")
	if _, _, err := parseCalendar(missingTable, 2026); err == nil || !strings.Contains(err.Error(), "holiday table") {
		t.Fatalf("error = %v, want missing holiday table", err)
	}
	broken := bytes.Replace(calendarFixture(t), []byte("1:00 p.m."), []byte("unknown"), 1)
	if _, _, err := parseCalendar(broken, 2026); err == nil || !strings.Contains(err.Error(), "unsupported status") {
		t.Fatalf("error = %v, want unsupported status", err)
	}
}

func TestCollectCalendarRejectsTransportAndUntrustedURL(t *testing.T) {
	client := NewClient(&calendarGetter{err: errors.New("offline")})
	for _, requestURL := range []string{
		"http://www.nasdaqtrader.com/Trader.aspx?id=Calendar",
		"https://example.com/Trader.aspx?id=Calendar",
		"https://www.nasdaqtrader.com:8443/Trader.aspx?id=Calendar",
		"https://www.nasdaqtrader.com/Trader.aspx?id=Calendar&draft=true",
		"https://www.nasdaqtrader.com/Trader.aspx?id=Calendar#top",
	} {
		client.calendarURL = requestURL
		if _, err := client.CollectCalendar(context.Background(), CalendarRequest{Year: 2026}); err == nil {
			t.Fatalf("expected URL rejection for %q", requestURL)
		}
	}
	client.calendarURL = DefaultCalendarURL
	if _, err := client.CollectCalendar(context.Background(), CalendarRequest{Year: 2026}); err == nil || !strings.Contains(err.Error(), "request") {
		t.Fatalf("error = %v, want transport failure", err)
	}
}

func TestLiveNasdaqCalendar(t *testing.T) {
	if os.Getenv("INVS_NASDAQ_CALENDAR_LIVE") != "1" {
		t.Skip("set INVS_NASDAQ_CALENDAR_LIVE=1 to fetch the public Nasdaq calendar")
	}
	getter, err := httpx.New(httpx.Config{UserAgent: "invs-nasdaq-calendar-live-acceptance research@example.com", Timeout: 90 * time.Second, RequestsPerSecond: 1, Burst: 1, MaxAttempts: 2, InitialBackoff: 250 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewClient(getter).CollectCalendar(context.Background(), CalendarRequest{Year: 2026})
	if err != nil {
		t.Fatal(err)
	}
	if countCalendarStatus(result.Events, "closed") != 10 || countCalendarStatus(result.Events, "early_close") != 2 {
		t.Fatalf("live Nasdaq event boundary changed: %+v", result.Events)
	}
	t.Logf("bytes=%d sha256=%s events=%d open=%s close=%s", len(result.Resources[0].Bytes), result.Resources[0].SHA256, len(result.Events), result.CoreHours.OpenLocal, result.CoreHours.CloseLocal)
}
