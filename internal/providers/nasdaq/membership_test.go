package nasdaq

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/luisdourado/invs/internal/httpx"
)

type fakeGetter struct {
	body []byte
	err  error
}

func (f fakeGetter) Get(context.Context, string) ([]byte, error) {
	return append([]byte(nil), f.body...), f.err
}

func TestCollectMembershipParsesExactPublicationAndChanges(t *testing.T) {
	body, err := os.ReadFile("testdata/insmed-added.html")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(fakeGetter{body: body})
	client.now = func() time.Time { return time.Date(2026, 8, 24, 1, 0, 0, 123456789, time.UTC) }
	result, err := client.CollectMembership(context.Background(), MembershipRequest{URL: "https://www.globenewswire.com/news-release/2025/12/13/3204942/6948/en/annual-changes.html"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Resources) != 1 || len(result.Events) != 4 {
		t.Fatalf("resources/events = %d/%d", len(result.Resources), len(result.Events))
	}
	var insmed *MembershipEvent
	for i := range result.Events {
		if result.Events[i].Ticker == "INSM" {
			insmed = &result.Events[i]
		}
	}
	if insmed == nil || !insmed.Member {
		t.Fatalf("INSM addition missing: %+v", result.Events)
	}
	if got := insmed.AvailableAt.Format(time.RFC3339); got != "2025-12-13T01:00:00Z" {
		t.Fatalf("available_at = %s", got)
	}
	if got := insmed.EffectiveAt.Format(time.RFC3339); got != "2025-12-22T14:30:00Z" {
		t.Fatalf("effective_at = %s", got)
	}
	if result.Resources[0].FetchedAt.Nanosecond() != 123456000 {
		t.Fatalf("fetched_at = %s", result.Resources[0].FetchedAt)
	}
}

func TestCollectMembershipRetainsRawOnParseFailure(t *testing.T) {
	client := NewClient(fakeGetter{body: []byte("<html>not a notice</html>")})
	result, err := client.CollectMembership(context.Background(), MembershipRequest{URL: "https://www.globenewswire.com/news-release/2026/01/01/example.html"})
	if err == nil || len(result.Resources) != 1 {
		t.Fatalf("error/resources = %v/%d", err, len(result.Resources))
	}
}

func TestCollectMembershipRejectsTransportAndUntrustedURL(t *testing.T) {
	client := NewClient(fakeGetter{err: errors.New("offline")})
	if _, err := client.CollectMembership(context.Background(), MembershipRequest{URL: "https://example.com/notice"}); err == nil {
		t.Fatal("expected untrusted URL error")
	}
	if _, err := client.CollectMembership(context.Background(), MembershipRequest{URL: "https://www.globenewswire.com/news-release/2026/01/01/example.html"}); err == nil {
		t.Fatal("expected transport error")
	}
}

func TestLiveNasdaqMembershipNotices(t *testing.T) {
	if os.Getenv("INVS_NASDAQ_MEMBERSHIP_LIVE") != "1" {
		t.Skip("set INVS_NASDAQ_MEMBERSHIP_LIVE=1 to fetch Nasdaq-authored membership notices")
	}
	getter, err := httpx.New(httpx.Config{
		UserAgent: "invs-nasdaq-membership-live-acceptance research@example.com",
		Timeout:   90 * time.Second, RequestsPerSecond: 1, Burst: 1,
		MaxAttempts: 2, InitialBackoff: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	urls := []string{
		"https://www.globenewswire.com/news-release/2025/12/13/3204942/6948/en/annual-changes-to-the-nasdaq-100-index.html",
		"https://www.globenewswire.com/news-release/2026/06/12/3310860/0/en/nasdaq-100-index-june-2026-quarterly-changes.html",
	}
	client := NewClient(getter)
	for _, noticeURL := range urls {
		result, collectErr := client.CollectMembership(context.Background(), MembershipRequest{URL: noticeURL})
		if collectErr != nil {
			t.Fatal(collectErr)
		}
		foundINSM := false
		for _, event := range result.Events {
			if event.Ticker == "INSM" {
				foundINSM = true
			}
		}
		if !foundINSM {
			t.Fatalf("INSM absent from %s: %+v", noticeURL, result.Events)
		}
		t.Logf("url=%s bytes=%d sha256=%s events=%d effective_at=%s available_at=%s", noticeURL, len(result.Resources[0].Bytes), result.Resources[0].SHA256, len(result.Events), result.Events[0].EffectiveAt.Format(time.RFC3339), result.Events[0].AvailableAt.Format(time.RFC3339))
	}
}
