package nyse

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/luisdourado/invs/internal/httpx"
	"github.com/luisdourado/invs/internal/providers"
)

type getterFake struct {
	body     []byte
	requests []string
}

func (g *getterFake) Get(_ context.Context, requestURL string) ([]byte, error) {
	g.requests = append(g.requests, requestURL)
	return append([]byte(nil), g.body...), nil
}

func calendarFixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/hours-calendars-2026.html")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestCollectCalendarRetainsRawAndParses2026(t *testing.T) {
	body := calendarFixture(t)
	getter := &getterFake{body: body}
	client := NewClient(getter)
	client.calendarURL = "https://nyse.test/hours-calendars"
	client.now = func() time.Time { return time.Date(2026, 8, 23, 20, 0, 0, 123456789, time.UTC) }
	result, err := client.CollectCalendar(context.Background(), CalendarRequest{Year: 2026})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Resources) != 1 || len(getter.requests) != 1 || getter.requests[0] != client.calendarURL {
		t.Fatalf("resources/requests = %+v/%+v", result.Resources, getter.requests)
	}
	resource := result.Resources[0]
	if !bytes.Equal(resource.Bytes, body) || resource.SHA256 != providers.SHA256(body) || resource.ParserVersion != CalendarParserVersion {
		t.Fatalf("resource = %+v", resource)
	}
	if result.CoreHours.OpenLocal != "09:30" || result.CoreHours.CloseLocal != "16:00" {
		t.Fatalf("core hours = %+v", result.CoreHours)
	}
	if len(result.Events) != 12 || countStatus(result.Events, "closed") != 10 || countStatus(result.Events, "early_close") != 2 {
		t.Fatalf("events = %+v", result.Events)
	}
	if result.Events[0].Date.Format(time.DateOnly) != "2026-01-01" || result.Events[0].Name != "New Year’s Day" || result.Events[0].Status != "closed" {
		t.Fatalf("first event = %+v", result.Events[0])
	}
	if last := result.Events[len(result.Events)-1]; last.Date.Format(time.DateOnly) != "2026-12-25" || last.Status != "closed" {
		t.Fatalf("last event = %+v", last)
	}
	if resource.ParserMetadata["closed_dates"] != "10" || resource.ParserMetadata["early_close_dates"] != "2" {
		t.Fatalf("metadata = %+v", resource.ParserMetadata)
	}
}

func TestParseCalendarFailsClosed(t *testing.T) {
	if _, _, err := parseCalendar([]byte(`<p>Core Trading Session: 9:30 a.m. to 4:00 p.m. ET</p>`), 2026); err == nil || !strings.Contains(err.Error(), "holiday table") {
		t.Fatalf("error = %v, want missing holiday table", err)
	}
	broken := bytes.Replace(calendarFixture(t), []byte("Core Trading Session: 9:30 a.m. to 4:00 p.m. ET"), []byte("Core Trading Session unavailable"), 1)
	if _, _, err := parseCalendar(broken, 2026); err == nil || !strings.Contains(err.Error(), "core trading hours") {
		t.Fatalf("error = %v, want missing core hours", err)
	}
}

func TestLiveNYSECalendar(t *testing.T) {
	if os.Getenv("INVS_NYSE_CALENDAR_LIVE") != "1" {
		t.Skip("set INVS_NYSE_CALENDAR_LIVE=1 to fetch the public NYSE calendar")
	}
	getter, err := httpx.New(httpx.Config{UserAgent: "invs-nyse-calendar-live-acceptance research@example.com", Timeout: 90 * time.Second, RequestsPerSecond: 1, Burst: 1, MaxAttempts: 2, InitialBackoff: 250 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewClient(getter).CollectCalendar(context.Background(), CalendarRequest{Year: 2026})
	if err != nil {
		t.Fatal(err)
	}
	if countStatus(result.Events, "closed") != 10 || countStatus(result.Events, "early_close") != 2 {
		t.Fatalf("live NYSE event boundary changed: %+v", result.Events)
	}
	t.Logf("bytes=%d sha256=%s events=%d open=%s close=%s", len(result.Resources[0].Bytes), result.Resources[0].SHA256, len(result.Events), result.CoreHours.OpenLocal, result.CoreHours.CloseLocal)
}
