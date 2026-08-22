package b3

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/luisdourado/invs/internal/providers"
)

type getterFake struct {
	fileBody []byte
	requests []string
}

func (f *getterFake) Get(_ context.Context, requestURL string) ([]byte, error) {
	f.requests = append(f.requests, requestURL)
	if strings.Contains(requestURL, "download/requestname") {
		body, _ := json.Marshal(map[string]any{
			"redirectUrl": "~/download?token=token+value/==",
			"token":       "token+value/==",
			"file":        map[string]string{"name": "InstrumentsConsolidatedFile_20260821_1", "extension": ".csv"},
		})
		return body, nil
	}
	if strings.Contains(requestURL, "download/") {
		return append([]byte(nil), f.fileBody...), nil
	}
	return nil, os.ErrNotExist
}

func fixtureBody(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/instruments-2026-08-21.csv")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func testClient(t *testing.T, body []byte) (*Client, *getterFake) {
	t.Helper()
	fake := &getterFake{fileBody: body}
	client := NewClient(fake)
	client.apiBaseURL = "https://b3.test/api/"
	client.now = func() time.Time {
		return time.Date(2026, 8, 22, 12, 34, 56, 123456789, time.FixedZone("BRT", -3*60*60))
	}
	return client, fake
}

func TestCollectParsesExactEquityShareRowsAndRetainsRawResource(t *testing.T) {
	client, fake := testClient(t, fixtureBody(t))
	result, err := client.Collect(context.Background(), Request{
		ReportDate: time.Date(2026, 8, 21, 17, 0, 0, 0, time.FixedZone("BRT", -3*60*60)),
		Tickers:    []string{"VALE3", "PETR4"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) != 2 || !strings.Contains(fake.requests[0], "fileName=InstrumentsConsolidated") || !strings.Contains(fake.requests[0], "date=2026-08-21") {
		t.Fatalf("requests = %#v", fake.requests)
	}
	if len(result.Resources) != 1 {
		t.Fatalf("resources = %d, want 1", len(result.Resources))
	}
	resource := result.Resources[0]
	if resource.Kind != ResourceKind || resource.Key != "InstrumentsConsolidatedFile_20260821_1.csv" || resource.ParserVersion != ParserVersion {
		t.Fatalf("resource = %+v", resource)
	}
	if resource.SHA256 != providers.SHA256(fixtureBody(t)) || resource.ParserMetadata["report_date"] != "2026-08-21" {
		t.Fatalf("resource metadata = %+v", resource)
	}
	if result.RecordsReceived != 3 || result.RecordsRejected != 0 || result.Duplicates != 0 {
		t.Fatalf("stats = %+v", result.Stats)
	}
	if len(result.Instruments) != 2 || result.Instruments[0].Ticker != "PETR4" || result.Instruments[1].Ticker != "VALE3" {
		t.Fatalf("instruments = %+v", result.Instruments)
	}
	petr := result.Instruments[0]
	if petr.ISIN != "BRPETRACNPR6" || petr.CompanyName != "PETROLEO BRASILEIRO S.A. PETROBRAS" || petr.TradingCurrency != "BRL" {
		t.Fatalf("PETR4 identity = %+v", petr)
	}
	if !petr.TradingStartDate.Equal(time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)) || petr.TradingEndDate != nil {
		t.Fatalf("PETR4 trading interval = %s/%v", petr.TradingStartDate, petr.TradingEndDate)
	}
	if petr.RawRecordLocator != "instruments/report_date=2026-08-21/row=4/ticker=PETR4/isin=BRPETRACNPR6" {
		t.Fatalf("raw locator = %q", petr.RawRecordLocator)
	}
}

func TestCollectRejectsMissingExactTicker(t *testing.T) {
	client, _ := testClient(t, fixtureBody(t))
	result, err := client.Collect(context.Background(), Request{
		ReportDate: time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC),
		Tickers:    []string{"PETR4", "ITUB4"},
	})
	if err == nil || !strings.Contains(err.Error(), "ITUB4") {
		t.Fatalf("error = %v, want missing exact ticker", err)
	}
	if len(result.Resources) != 1 {
		t.Fatalf("raw resource count = %d, want 1 on parse failure", len(result.Resources))
	}
}

func TestCollectRejectsNonFinalFileAndPreservesDownloadedBytes(t *testing.T) {
	body := []byte("Status do Arquivo: Processando\n")
	client, _ := testClient(t, body)
	result, err := client.Collect(context.Background(), Request{
		ReportDate: time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC),
		Tickers:    []string{"PETR4"},
	})
	if err == nil || !strings.Contains(err.Error(), "not final") {
		t.Fatalf("error = %v, want non-final status error", err)
	}
	if len(result.Resources) != 1 || string(result.Resources[0].Bytes) != string(body) {
		t.Fatalf("downloaded bytes were not retained: %+v", result.Resources)
	}
}

func TestNormalizeRequestRejectsAmbiguousTickerList(t *testing.T) {
	for name, request := range map[string]Request{
		"missing date":    {Tickers: []string{"PETR4"}},
		"missing tickers": {ReportDate: time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)},
		"duplicate":       {ReportDate: time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC), Tickers: []string{"PETR4", "petr4"}},
		"unsafe":          {ReportDate: time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC), Tickers: []string{"PETR/4"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := normalizeRequest(request); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
}
