package ptax

import (
	"bytes"
	"context"
	"net/url"
	"strings"
	"testing"
	"time"
)

type fakeGetter struct {
	body  []byte
	url   string
	calls int
}

func (getter *fakeGetter) Get(_ context.Context, requestURL string) ([]byte, error) {
	getter.calls++
	getter.url = requestURL
	return getter.body, nil
}

func TestCollectBuildsPeriodURLAndPublishesExactBulletin(t *testing.T) {
	body := []byte(`{"@odata.context":"official","value":[{"cotacaoCompra":5.22300,"cotacaoVenda":5.22360,"dataHoraCotacao":"2026-08-14 13:10:22.94166"}]}`)
	getter := &fakeGetter{body: body}
	client := NewClient(getter)
	client.baseURL = "https://example.test/root/"
	client.now = func() time.Time { return time.Date(2026, 8, 14, 17, 0, 0, 987654321, time.UTC) }

	result, err := client.Collect(context.Background(), Request{
		Start: time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(getter.url)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/root/"+endpoint {
		t.Fatalf("path=%q", parsed.Path)
	}
	if parsed.Query().Get("@dataInicial") != "'08-10-2026'" || parsed.Query().Get("@dataFinalCotacao") != "'08-14-2026'" || parsed.Query().Get("$format") != "json" {
		t.Fatalf("query=%v", parsed.Query())
	}
	if result.RecordsReceived != 1 || len(result.Observations) != 1 {
		t.Fatalf("result=%+v", result)
	}
	observation := result.Observations[0]
	if observation.Source != "bcb_ptax" || observation.BaseCurrency != "USD" || observation.QuoteCurrency != "BRL" || observation.RateKind != "ptax_closing" {
		t.Fatalf("identity=%+v", observation)
	}
	if observation.BuyRate != "5.223" || observation.SellRate != "5.2236" {
		t.Fatalf("rates=%s/%s", observation.BuyRate, observation.SellRate)
	}
	wantFixing := time.Date(2026, 8, 14, 16, 10, 22, 941660000, time.UTC)
	if !observation.FixingAt.Equal(wantFixing) || !observation.AvailableAt.Equal(wantFixing) || !observation.PublishedAt.Equal(wantFixing) {
		t.Fatalf("source timing=%+v", observation)
	}
	if observation.RecordedAt.Nanosecond()%int(time.Microsecond) != 0 || !observation.RecordedAt.Equal(time.Date(2026, 8, 14, 17, 0, 0, 987654000, time.UTC)) {
		t.Fatalf("recorded_at=%s", observation.RecordedAt)
	}
	if !bytes.Equal(result.Raw, body) || result.SHA256 == "" || len(result.Resources) != 1 {
		t.Fatalf("raw result=%+v", result)
	}
	if observation.ID == "" || observation.SourceRecordID != "ptax-closing/USD-BRL/2026-08-14T13:10:22.94166-03:00" {
		t.Fatalf("source identity=%+v", observation)
	}
}

func TestCollectRetainsRawOnInvalidResponse(t *testing.T) {
	cases := map[string]string{
		"unknown field":  `{"@odata.context":"official","value":[],"extra":1}`,
		"buy above sell": `{"@odata.context":"official","value":[{"cotacaoCompra":5.3,"cotacaoVenda":5.2,"dataHoraCotacao":"2026-08-14 13:10:22"}]}`,
		"future row":     `{"@odata.context":"official","value":[{"cotacaoCompra":5.2,"cotacaoVenda":5.3,"dataHoraCotacao":"2026-08-15 13:10:22"}]}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			getter := &fakeGetter{body: []byte(payload)}
			client := NewClient(getter)
			client.now = func() time.Time { return time.Date(2026, 8, 14, 17, 0, 0, 0, time.UTC) }
			result, err := client.Collect(context.Background(), Request{
				Start: time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC),
				End:   time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC),
			})
			if err == nil {
				t.Fatal("invalid response accepted")
			}
			if !bytes.Equal(result.Raw, []byte(payload)) || result.SHA256 == "" || len(result.Resources) != 1 {
				t.Fatalf("raw not retained: %+v", result)
			}
		})
	}
}

func TestRequestValidationPrecedesHTTP(t *testing.T) {
	getter := &fakeGetter{}
	client := NewClient(getter)
	cases := []Request{
		{},
		{Start: time.Date(1984, 11, 27, 0, 0, 0, 0, time.UTC), End: time.Date(1984, 11, 28, 0, 0, 0, 0, time.UTC)},
		{Start: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{Start: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
		{Start: time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC), End: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
	}
	for _, request := range cases {
		if _, err := client.Collect(context.Background(), request); err == nil {
			t.Fatalf("invalid request accepted: %+v", request)
		}
	}
	if getter.calls != 0 {
		t.Fatalf("HTTP calls=%d", getter.calls)
	}
}

func TestParseRejectsTrailingJSON(t *testing.T) {
	_, _, err := parse([]byte(`{"@odata.context":"official","value":[]} {}`), time.Now().UTC())
	if err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("err=%v", err)
	}
}
