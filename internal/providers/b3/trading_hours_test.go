package b3

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

func tradingHoursFixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/trading-hours.html")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestCollectTradingHoursRetainsRawAndParsesCashEquitySession(t *testing.T) {
	body := tradingHoursFixture(t)
	getter := &marketCalendarGetter{body: body}
	client := NewClient(getter)
	client.tradingHoursBaseURL = "https://b3.test/trading-hours"
	client.now = func() time.Time { return time.Date(2026, 8, 23, 20, 0, 0, 123456789, time.UTC) }

	result, err := client.CollectTradingHours(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Resources) != 1 || len(getter.requests) != 1 || getter.requests[0] != client.tradingHoursBaseURL {
		t.Fatalf("resources/requests = %+v/%+v", result.Resources, getter.requests)
	}
	resource := result.Resources[0]
	if !bytes.Equal(resource.Bytes, body) || resource.SHA256 != providers.SHA256(body) || resource.ParserVersion != TradingHoursParserVersion {
		t.Fatalf("resource = %+v", resource)
	}
	if result.CashEquity.OpenLocal != "10:00" || result.CashEquity.ContinuousTradingEnd != "16:55" || result.CashEquity.CloseLocal != "17:00" {
		t.Fatalf("cash-equity hours = %+v", result.CashEquity)
	}
}

func TestParseCashEquityHoursFailsClosed(t *testing.T) {
	if _, err := parseCashEquityHours([]byte(`<table><tr><td>Derivatives</td><td>10:00</td></tr></table>`)); err == nil {
		t.Fatal("missing cash-equity row accepted")
	}
	body := bytes.Replace(tradingHoursFixture(t), []byte("16:55</td><td>16:55</td><td>17:00"), []byte("09:00</td><td>09:00</td><td>08:00"), 1)
	if _, err := parseCashEquityHours(body); err == nil {
		t.Fatal("non-increasing cash-equity hours accepted")
	}
}

func TestLiveB3TradingHours(t *testing.T) {
	if os.Getenv("INVS_B3_TRADING_HOURS_LIVE") != "1" {
		t.Skip("set INVS_B3_TRADING_HOURS_LIVE=1 to fetch the public B3 trading-hours page")
	}
	getter, err := httpx.New(httpx.Config{
		UserAgent:         "invs-b3-trading-hours-live-acceptance research@example.com",
		Timeout:           90 * time.Second,
		RequestsPerSecond: 1,
		Burst:             1,
		MaxAttempts:       2,
		InitialBackoff:    250 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewClient(getter).CollectTradingHours(context.Background())
	if err != nil {
		containsCashMarket := false
		resourceBytes := 0
		resourceHash := ""
		if len(result.Resources) == 1 {
			resourceBytes = len(result.Resources[0].Bytes)
			resourceHash = result.Resources[0].SHA256
			containsCashMarket = strings.Contains(string(result.Resources[0].Bytes), "Cash Market")
		}
		t.Fatalf("%v (bytes=%d sha256=%s contains_cash_market=%t)", err, resourceBytes, resourceHash, containsCashMarket)
	}
	if result.CashEquity.OpenLocal != "10:00" || result.CashEquity.ContinuousTradingEnd != "16:55" || result.CashEquity.CloseLocal != "17:00" {
		t.Fatalf("live B3 cash-equity hours changed: %+v", result.CashEquity)
	}
	t.Logf("bytes=%d sha256=%s open=%s continuous_end=%s close=%s", len(result.Resources[0].Bytes), result.Resources[0].SHA256, result.CashEquity.OpenLocal, result.CashEquity.ContinuousTradingEnd, result.CashEquity.CloseLocal)
}
