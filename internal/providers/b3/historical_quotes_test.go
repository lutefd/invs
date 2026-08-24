package b3

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/luisdourado/invs/internal/model"
	"github.com/luisdourado/invs/internal/providers"
)

type historicalQuotesGetter struct {
	body     []byte
	requests []string
}

func (f *historicalQuotesGetter) Get(_ context.Context, requestURL string) ([]byte, error) {
	f.requests = append(f.requests, requestURL)
	return append([]byte(nil), f.body...), nil
}

func cotahistLine(recordType string) []byte {
	line := bytes.Repeat([]byte{' '}, 245)
	copy(line[0:2], recordType)
	return line
}

func putCOTAHIST(line []byte, start, end int, value string, zeroPad bool) {
	width := end - start
	if len(value) > width {
		panic("test field exceeds COTAHIST width")
	}
	fill := byte(' ')
	if zeroPad {
		fill = '0'
	}
	for index := start; index < end; index++ {
		line[index] = fill
	}
	copy(line[end-len(value):end], value)
}

func cotahistFixture(t *testing.T, isin string) []byte {
	t.Helper()
	header := cotahistLine("00")
	putCOTAHIST(header, 2, 15, "COTAHIST.2021", false)
	putCOTAHIST(header, 15, 23, "BOVESPA", false)
	putCOTAHIST(header, 23, 31, "20211231", true)

	record := cotahistLine("01")
	putCOTAHIST(record, 2, 10, "20210903", true)
	putCOTAHIST(record, 10, 12, "02", true)
	putCOTAHIST(record, 12, 24, "PETZ3", false)
	putCOTAHIST(record, 24, 27, "010", true)
	putCOTAHIST(record, 27, 39, "PETZ", false)
	putCOTAHIST(record, 39, 49, "ON NM", false)
	putCOTAHIST(record, 52, 56, "R$", false)
	putCOTAHIST(record, 56, 69, "2200", true)
	putCOTAHIST(record, 69, 82, "2300", true)
	putCOTAHIST(record, 82, 95, "2100", true)
	putCOTAHIST(record, 95, 108, "2225", true)
	putCOTAHIST(record, 108, 121, "2250", true)
	putCOTAHIST(record, 147, 152, "12", true)
	putCOTAHIST(record, 152, 170, "12345", true)
	putCOTAHIST(record, 170, 188, "27776250", true)
	putCOTAHIST(record, 210, 217, "1", true)
	putCOTAHIST(record, 230, 242, isin, false)
	putCOTAHIST(record, 242, 245, "100", true)

	trailer := cotahistLine("99")
	putCOTAHIST(trailer, 2, 15, "COTAHIST.2021", false)
	putCOTAHIST(trailer, 15, 23, "BOVESPA", false)
	putCOTAHIST(trailer, 23, 31, "20211231", true)
	putCOTAHIST(trailer, 31, 42, "3", true)

	var body bytes.Buffer
	writer := zip.NewWriter(&body)
	member, err := writer.Create("COTAHIST_A2021.TXT")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range [][]byte{header, record, trailer} {
		if _, err := fmt.Fprintf(member, "%s\r\n", line); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func TestCollectHistoricalQuotesPublishesExactRawCashBar(t *testing.T) {
	body := cotahistFixture(t, "BRPETZACNOR2")
	getter := &historicalQuotesGetter{body: body}
	client := NewClient(getter)
	client.historicalQuotesBaseURL = "https://b3.test/SerHist/"
	receivedAt := time.Date(2026, 8, 24, 6, 7, 8, 987654321, time.FixedZone("BRT", -3*60*60))
	client.now = func() time.Time { return receivedAt }

	result, err := client.CollectHistoricalQuotes(context.Background(), HistoricalQuotesRequest{
		Year: 2021, Start: time.Date(2021, 9, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2021, 9, 30, 0, 0, 0, 0, time.UTC),
		Securities: []HistoricalQuoteSecurity{{
			SecurityID: "fcb3f84d-e8e8-46ad-aace-70027962523f",
			Ticker:     "PETZ3", ISIN: "BRPETZACNOR2", Currency: "BRL",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(getter.requests) != 1 || getter.requests[0] != "https://b3.test/SerHist/COTAHIST_A2021.ZIP" {
		t.Fatalf("requests = %#v", getter.requests)
	}
	if len(result.Resources) != 1 || result.Resources[0].SHA256 != providers.SHA256(body) || result.Resources[0].ParserVersion != HistoricalQuotesParserVersion {
		t.Fatalf("resource = %+v", result.Resources)
	}
	if result.RecordsReceived != 1 || result.RecordsRejected != 0 || result.Duplicates != 0 {
		t.Fatalf("stats = received %d rejected %d duplicates %d", result.RecordsReceived, result.RecordsRejected, result.Duplicates)
	}
	bars := result.Bars["fcb3f84d-e8e8-46ad-aace-70027962523f"]
	if len(bars) != 1 {
		t.Fatalf("bars = %+v", result.Bars)
	}
	bar := bars[0]
	if bar.Source != "b3_cotahist" || bar.PriceBasis != "raw" || bar.Open != "22" || bar.High != "23" || bar.Low != "21" || bar.Close != "22.5" || bar.Volume != "12345" {
		t.Fatalf("bar = %+v", bar)
	}
	if !bar.Temporal.ObservedAt.Equal(time.Date(2021, 9, 3, 0, 0, 0, 0, time.UTC)) || bar.Temporal.ObservedPrecision != model.PrecisionDate || !bar.Temporal.PublishedAt.IsZero() {
		t.Fatalf("source time = %+v", bar.Temporal)
	}
	wantReceipt := receivedAt.UTC().Truncate(time.Microsecond)
	if !bar.Temporal.AvailableAt.Equal(wantReceipt) || !bar.Temporal.IngestedAt.Equal(wantReceipt) {
		t.Fatalf("receipt time = %+v want %s", bar.Temporal, wantReceipt)
	}
	if !strings.Contains(bar.Provenance.RawRecordLocator, "line=2/date=2021-09-03/ticker=PETZ3/isin=BRPETZACNOR2") {
		t.Fatalf("raw locator = %q", bar.Provenance.RawRecordLocator)
	}
}

func TestCollectHistoricalQuotesRetainsRawOnParseFailure(t *testing.T) {
	body := cotahistFixture(t, "BRWRONG00000")
	getter := &historicalQuotesGetter{body: body}
	client := NewClient(getter)
	client.now = func() time.Time { return time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC) }
	result, err := client.CollectHistoricalQuotes(context.Background(), HistoricalQuotesRequest{
		Year: 2021, Start: time.Date(2021, 9, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2021, 9, 30, 0, 0, 0, 0, time.UTC),
		Securities: []HistoricalQuoteSecurity{{
			SecurityID: "fcb3f84d-e8e8-46ad-aace-70027962523f",
			Ticker:     "PETZ3", ISIN: "BRPETZACNOR2", Currency: "BRL",
		}},
	})
	if err == nil || result.RecordsRejected != 1 {
		t.Fatalf("result = %+v err = %v", result, err)
	}
	if len(result.Resources) != 1 || !bytes.Equal(result.Resources[0].Bytes, body) {
		t.Fatalf("raw ZIP was not retained on failure")
	}
}

func TestHistoricalQuotePriceHonorsQuotationFactor(t *testing.T) {
	got, err := cotahistPrice([]byte("0000000123456"), 1000)
	if err != nil || got != "1.23456" {
		t.Fatalf("price = %q err = %v", got, err)
	}
}

func TestHistoricalQuotesRejectsOpenAnnualFile(t *testing.T) {
	getter := &historicalQuotesGetter{}
	client := NewClient(getter)
	client.now = func() time.Time { return time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC) }
	_, err := client.CollectHistoricalQuotes(context.Background(), HistoricalQuotesRequest{
		Year: 2026, Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		Securities: []HistoricalQuoteSecurity{{
			SecurityID: "fcb3f84d-e8e8-46ad-aace-70027962523f",
			Ticker:     "PETZ3", ISIN: "BRPETZACNOR2", Currency: "BRL",
		}},
	})
	if err == nil || len(getter.requests) != 0 {
		t.Fatalf("err = %v requests = %#v", err, getter.requests)
	}
}
