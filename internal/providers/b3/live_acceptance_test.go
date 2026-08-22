package b3

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/luisdourado/invs/internal/httpx"
)

// TestLiveInstrumentsConsolidated is opt-in so the normal unit suite remains
// deterministic and does not depend on B3 availability. It is the executable
// live acceptance used by the B3 source-admission report.
func TestLiveInstrumentsConsolidated(t *testing.T) {
	if os.Getenv("INVS_B3_LIVE") != "1" {
		t.Skip("set INVS_B3_LIVE=1 to run the bounded B3 live acceptance")
	}
	reportDateText := strings.TrimSpace(os.Getenv("B3_REPORT_DATE"))
	if reportDateText == "" {
		t.Fatal("B3_REPORT_DATE is required for the live acceptance")
	}
	reportDate, err := time.Parse(time.DateOnly, reportDateText)
	if err != nil {
		t.Fatalf("B3_REPORT_DATE: %v", err)
	}
	tickerText := strings.TrimSpace(os.Getenv("B3_TICKERS"))
	if tickerText == "" {
		tickerText = "PETR4,VALE3"
	}
	tickers := strings.Split(tickerText, ",")
	getter, err := httpx.New(httpx.Config{
		UserAgent:         "invs-b3-live-acceptance research@example.com",
		Timeout:           90 * time.Second,
		RequestsPerSecond: 1,
		Burst:             1,
		MaxAttempts:       2,
		InitialBackoff:    250 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewClient(getter).Collect(context.Background(), Request{ReportDate: reportDate, Tickers: tickers})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Resources) != 1 || len(result.Instruments) != len(tickers) {
		t.Fatalf("live result resources=%d instruments=%d requested=%d", len(result.Resources), len(result.Instruments), len(tickers))
	}
	for _, instrument := range result.Instruments {
		if instrument.ReportDate.Format(time.DateOnly) != reportDateText || instrument.TradingCurrency != "BRL" || instrument.ISIN == "" || instrument.CompanyName == "" {
			t.Fatalf("incomplete live instrument: %+v", instrument)
		}
		t.Logf("ticker=%s isin=%s company=%q trading_start=%s distribution_id=%s", instrument.Ticker, instrument.ISIN, instrument.CompanyName, instrument.TradingStartDate.Format(time.DateOnly), instrument.DistributionID)
	}
	t.Logf("report_date=%s file=%s bytes=%d sha256=%s records_received=%d", reportDateText, result.Resources[0].Key, len(result.Resources[0].Bytes), result.Resources[0].SHA256, result.RecordsReceived)
}
