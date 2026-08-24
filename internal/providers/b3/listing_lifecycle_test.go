package b3

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/luisdourado/invs/internal/httpx"
)

const petzLifecycleURL = "https://sistemasweb.b3.com.br/PlantaoNoticias/Noticias/Detail?agencia=18&dataNoticia=2026-01-02+19%3A43%3A10&idNoticia=3192104"

func TestCollectListingLifecycleParsesPETZCessation(t *testing.T) {
	body, err := os.ReadFile("testdata/petz-listing-cessation.html")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(indexMembershipGetter{body: body})
	client.now = func() time.Time { return time.Date(2026, 8, 24, 2, 3, 4, 567890123, time.UTC) }
	result, err := client.CollectListingLifecycle(context.Background(), ListingLifecycleRequest{URL: petzLifecycleURL})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Resources) != 1 || len(result.Events) != 1 {
		t.Fatalf("resources/events = %d/%d", len(result.Resources), len(result.Events))
	}
	event := result.Events[0]
	if event.TradingName != "PETZ" || event.EventKind != "trading_cessation" || event.Reason != "incorporation" || event.SourceNoticeID != "3192104" {
		t.Fatalf("event identity = %+v", event)
	}
	if got := event.EffectiveAt.Format(time.RFC3339); got != "2026-01-05T03:00:00Z" {
		t.Fatalf("effective_at = %s", got)
	}
	if got := event.AvailableAt.Format(time.RFC3339); got != "2026-01-02T22:43:10Z" {
		t.Fatalf("available_at = %s", got)
	}
	if result.Resources[0].ParserVersion != ListingLifecycleParserVersion || result.Resources[0].ParserMetadata["notice_id"] != "3192104" {
		t.Fatalf("resource metadata = %+v", result.Resources[0])
	}
}

func TestCollectListingLifecycleRetainsRawOnParseFailure(t *testing.T) {
	client := NewClient(indexMembershipGetter{body: []byte("<html>not a lifecycle notice</html>")})
	result, err := client.CollectListingLifecycle(context.Background(), ListingLifecycleRequest{URL: petzLifecycleURL})
	if err == nil || len(result.Resources) != 1 {
		t.Fatalf("error/resources = %v/%d", err, len(result.Resources))
	}
}

func TestCollectListingLifecycleRejectsUntrustedOrAmbiguousURL(t *testing.T) {
	client := NewClient(indexMembershipGetter{})
	for _, candidate := range []string{
		"https://example.com/PlantaoNoticias/Noticias/Detail?agencia=18&dataNoticia=2026-01-02+19%3A43%3A10&idNoticia=3192104",
		"https://sistemasweb.b3.com.br/PlantaoNoticias/Noticias/Detail?idNoticia=3192104",
		petzLifecycleURL + "&extra=1",
	} {
		if _, err := client.CollectListingLifecycle(context.Background(), ListingLifecycleRequest{URL: candidate}); err == nil {
			t.Fatalf("untrusted/ambiguous URL accepted: %s", candidate)
		}
	}
}

func TestLiveB3ListingLifecycleNotice(t *testing.T) {
	if os.Getenv("INVS_B3_LISTING_LIFECYCLE_LIVE") != "1" {
		t.Skip("set INVS_B3_LISTING_LIFECYCLE_LIVE=1 to fetch the official B3 lifecycle notice")
	}
	getter, err := httpx.New(httpx.Config{
		UserAgent: "invs-b3-listing-lifecycle-live-acceptance research@example.com",
		Timeout:   90 * time.Second, RequestsPerSecond: 1, Burst: 1,
		MaxAttempts: 2, InitialBackoff: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewClient(getter).CollectListingLifecycle(context.Background(), ListingLifecycleRequest{URL: petzLifecycleURL})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 1 || result.Events[0].TradingName != "PETZ" {
		t.Fatalf("events = %+v", result.Events)
	}
	t.Logf("bytes=%d sha256=%s effective_at=%s available_at=%s", len(result.Resources[0].Bytes), result.Resources[0].SHA256, result.Events[0].EffectiveAt.Format(time.RFC3339), result.Events[0].AvailableAt.Format(time.RFC3339))
}
