package b3

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/luisdourado/invs/internal/httpx"
)

type indexMembershipGetter struct {
	body []byte
}

func (f indexMembershipGetter) Get(context.Context, string) ([]byte, error) {
	return append([]byte(nil), f.body...), nil
}

func TestCollectIndexMembershipParsesEntryAndRemoval(t *testing.T) {
	body, err := os.ReadFile("testdata/ibovespa-2025.html")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(indexMembershipGetter{body: body})
	client.now = func() time.Time { return time.Date(2026, 8, 24, 1, 2, 3, 456789123, time.UTC) }
	result, err := client.CollectIndexMembership(context.Background(), IndexMembershipRequest{URL: "https://www.b3.com.br/pt_br/noticias/notice.htm"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Resources) != 1 || len(result.Events) != 4 {
		t.Fatalf("resources/events = %d/%d: %+v", len(result.Resources), len(result.Events), result.Events)
	}
	var petz *IndexMembershipEvent
	for i := range result.Events {
		if result.Events[i].Ticker == "PETZ3" {
			petz = &result.Events[i]
		}
	}
	if petz == nil || petz.Member {
		t.Fatalf("PETZ3 removal missing: %+v", result.Events)
	}
	if got := petz.AvailableAt.Format(time.RFC3339); got != "2025-09-02T03:00:00Z" {
		t.Fatalf("available_at = %s", got)
	}
	if got := petz.EffectiveAt.Format(time.RFC3339); got != "2025-09-01T03:00:00Z" {
		t.Fatalf("effective_at = %s", got)
	}
	if got := petz.CoverageEnd.Format(time.RFC3339); got != "2026-01-03T03:00:00Z" {
		t.Fatalf("coverage_end = %s", got)
	}
}

func TestParseIndexMembershipSupportsPortugueseLongDates(t *testing.T) {
	body, err := os.ReadFile("testdata/ibovespa-2021.html")
	if err != nil {
		t.Fatal(err)
	}
	events, err := parseIndexMembership(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].Ticker != "PETZ3" || !events[1].Member {
		t.Fatalf("events = %+v", events)
	}
	if got := events[1].EffectiveAt.Format(time.RFC3339); got != "2021-09-06T03:00:00Z" {
		t.Fatalf("effective_at = %s", got)
	}
}

func TestCollectIndexMembershipRetainsRawOnParseFailure(t *testing.T) {
	client := NewClient(indexMembershipGetter{body: []byte("<html>not a notice</html>")})
	result, err := client.CollectIndexMembership(context.Background(), IndexMembershipRequest{URL: "https://www.b3.com.br/pt_br/noticias/notice.htm"})
	if err == nil || len(result.Resources) != 1 {
		t.Fatalf("error/resources = %v/%d", err, len(result.Resources))
	}
}

func TestCollectIndexMembershipRejectsUntrustedURL(t *testing.T) {
	client := NewClient(indexMembershipGetter{})
	if _, err := client.CollectIndexMembership(context.Background(), IndexMembershipRequest{URL: "https://example.com/notice"}); err == nil {
		t.Fatal("expected untrusted URL error")
	}
}

func TestLiveB3IndexMembershipNotices(t *testing.T) {
	if os.Getenv("INVS_B3_MEMBERSHIP_LIVE") != "1" {
		t.Skip("set INVS_B3_MEMBERSHIP_LIVE=1 to fetch official B3 membership notices")
	}
	getter, err := httpx.New(httpx.Config{
		UserAgent: "invs-b3-membership-live-acceptance research@example.com",
		Timeout:   90 * time.Second, RequestsPerSecond: 1, Burst: 1,
		MaxAttempts: 2, InitialBackoff: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	urls := []string{
		"https://www.b3.com.br/pt_br/noticias/b3-divulga-nova-carteira-de-ibovespa-b3-e-demais-indices.htm",
		"https://www.b3.com.br/pt_br/noticias/ibovespa-8AA8D0CD9851B974019905C93D246CB2.htm",
	}
	client := NewClient(getter)
	for _, noticeURL := range urls {
		result, collectErr := client.CollectIndexMembership(context.Background(), IndexMembershipRequest{URL: noticeURL})
		if collectErr != nil {
			t.Fatal(collectErr)
		}
		foundPETZ := false
		for _, event := range result.Events {
			if event.Ticker == "PETZ3" {
				foundPETZ = true
			}
		}
		if !foundPETZ {
			t.Fatalf("PETZ3 absent from %s: %+v", noticeURL, result.Events)
		}
		t.Logf("url=%s bytes=%d sha256=%s events=%d effective_at=%s available_at=%s", noticeURL, len(result.Resources[0].Bytes), result.Resources[0].SHA256, len(result.Events), result.Events[0].EffectiveAt.Format(time.RFC3339), result.Events[0].AvailableAt.Format(time.RFC3339))
	}
}
