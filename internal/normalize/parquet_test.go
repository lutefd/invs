package normalize

import (
	"os"
	"testing"
	"time"

	"github.com/luisdourado/invs/internal/model"
	"github.com/parquet-go/parquet-go"
)

func TestWritePricesIsIdempotentAndQueryable(t *testing.T) {
	w, _ := NewWriter(t.TempDir())
	at := time.Date(2024, 1, 2, 21, 0, 0, 0, time.UTC)
	bar := model.PriceBar{Source: "yahoo", SecurityID: "security-1", Currency: "USD", Temporal: model.Temporal{ObservedAt: at, PublishedAt: at, PublishedPrecision: model.PrecisionSecond, AvailableAt: at, IngestedAt: at.Add(time.Hour)}, Open: 1, High: 3, Low: 1, Close: 2, Volume: 10, RawPayloadHash: "abc"}
	path, n, err := w.WritePrices("security-1", []model.PriceBar{bar, bar})
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	before, _ := os.Stat(path)
	path2, n, err := w.WritePrices("security-1", []model.PriceBar{bar})
	if err != nil || n != 0 || path2 != path {
		t.Fatalf("second n=%d err=%v", n, err)
	}
	after, _ := os.Stat(path)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("idempotent write replaced file")
	}
	rows, err := parquet.ReadFile[PriceRow](path)
	if err != nil || len(rows) != 1 || rows[0].SecurityID != "security-1" || rows[0].ObservedAt != at.UnixMicro() {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

func TestWriteFundamentalsDeduplicatesSemanticIdentity(t *testing.T) {
	w, _ := NewWriter(t.TempDir())
	end := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	filed := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	o := model.FundamentalObservation{Source: "sec", IssuerID: "issuer-1", Taxonomy: "us-gaap", Concept: "Revenue", Unit: "USD", Value: 10, PeriodEnd: end, AccessionNumber: "a", Temporal: model.Temporal{ObservedAt: end, PublishedAt: filed, PublishedPrecision: model.PrecisionDate, AvailableAt: filed.AddDate(0, 0, 1), IngestedAt: filed.AddDate(0, 0, 2)}}
	path, n, err := w.WriteFundamentals("issuer-1", []model.FundamentalObservation{o, o})
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	rows, err := parquet.ReadFile[FundamentalRow](path)
	if err != nil || len(rows) != 1 || rows[0].AvailableAt != filed.AddDate(0, 0, 1).UnixMicro() {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if rows[0].PeriodStartTime() != nil {
		t.Fatal("absent physical period-start sentinel must be exposed as logical NULL")
	}
}

func TestPhysicalSentinelsExposeLogicalNulls(t *testing.T) {
	if (FundamentalRow{PeriodStart: 0, HasPeriodStart: false}).PeriodStartTime() != nil {
		t.Fatal("period start should be NULL")
	}
	if (EconomicRow{VintageAt: 0, HasVintageAt: false}).VintageTime() != nil {
		t.Fatal("vintage should be NULL")
	}
	want := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	d := days(want)
	gotDate := (FundamentalRow{PeriodStart: d, HasPeriodStart: true}).PeriodStartTime()
	gotVintage := (EconomicRow{VintageAt: want.UnixMicro(), HasVintageAt: true}).VintageTime()
	if gotDate == nil || !gotDate.Equal(want) || gotVintage == nil || !gotVintage.Equal(want) {
		t.Fatalf("date=%v vintage=%v", gotDate, gotVintage)
	}
}
