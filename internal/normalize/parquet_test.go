package normalize

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luisdourado/invs/internal/model"
	"github.com/parquet-go/parquet-go"
)

func columnsOf[T any]() map[string]bool {
	m := map[string]bool{}
	for _, c := range parquet.SchemaOf(new(T)).Columns() {
		if len(c) == 1 {
			m[c[0]] = true
		}
	}
	return m
}
func TestV1PhysicalSchemasContainCanonicalColumns(t *testing.T) {
	cases := []struct {
		name string
		cols map[string]bool
		want []string
	}{{"price", columnsOf[PriceRow](), []string{"schema_version", "security_id", "interval", "price_basis", "currency", "observed_at", "published_at", "has_published_at", "open", "high", "low", "close", "volume", "has_volume", "data_source_id", "ingestion_run_id", "raw_payload_hash", "ingested_at"}}, {"fundamental", columnsOf[FundamentalRow](), []string{"schema_version", "issuer_id", "security_id", "has_security_id", "concept", "value", "has_value", "unit", "currency", "has_currency", "period_start", "has_period_start", "period_end", "fiscal_period", "observed_at", "published_at", "revision", "data_source_id", "ingestion_run_id"}}, {"economic", columnsOf[EconomicRow](), []string{"schema_version", "series_id", "geography", "value", "has_value", "unit", "frequency", "seasonal_adjustment", "has_seasonal_adjustment", "observed_at", "published_at", "revision", "data_source_id", "ingestion_run_id"}}}
	for _, tc := range cases {
		for _, name := range tc.want {
			if !tc.cols[name] {
				t.Errorf("%s missing %s", tc.name, name)
			}
		}
	}
}

const issuerID = "1b3d88f5-55b8-4dc5-a6be-2f77e9e99201"
const securityID = "469fc20f-7d4b-45bb-b827-05f8410e71aa"
const sourceID = "a4a877d1-48dd-42dc-b86e-8020a4107f69"
const runID = "a135791f-df27-4a4a-8426-6e2f59b9527a"
const rawHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func provenance(at time.Time) model.Provenance {
	return model.Provenance{DataSourceID: sourceID, IngestionRunID: runID, RawPayloadHash: rawHash, IngestedAt: at, NormalizerVersion: model.NormalizerVersion}
}
func price(at time.Time) model.PriceBar {
	return model.PriceBar{Source: "yahoo", SecurityID: securityID, Interval: "1d", PriceBasis: "raw", Currency: "USD", Temporal: model.Temporal{ObservedAt: at, PublishedAt: at.Add(time.Hour), AvailableAt: at.Add(time.Hour), IngestedAt: at.Add(2 * time.Hour), PublishedPrecision: model.PrecisionSecond}, Open: "1.000000000000000001", High: "3", Low: "1", Close: "2", Volume: "10", RawPayloadHash: rawHash, Provenance: provenance(at.Add(2 * time.Hour))}
}

func TestPriceV1LosslessIdempotentAndQueryable(t *testing.T) {
	w, _ := NewWriter(t.TempDir())
	at := time.Date(2024, 1, 2, 20, 0, 0, 0, time.UTC)
	bar := price(at)
	path, n, err := w.WritePrices(securityID, []model.PriceBar{bar, bar})
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	before, _ := os.Stat(path)
	_, n, err = w.WritePrices(securityID, []model.PriceBar{bar})
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	after, _ := os.Stat(path)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("idempotent write replaced file")
	}
	rows, err := parquet.ReadFile[PriceRow](path)
	if err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
	r := rows[0]
	if r.SchemaVersion != "1.0.0" || r.Open != "1.000000000000000001" || r.Interval != "1d" || r.PriceBasis != "raw" || r.DataSourceID != sourceID {
		t.Fatalf("row=%+v", r)
	}
	if output := os.Getenv("INVS_PARQUET_TEST_OUTPUT"); output != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(output, b, 0o640); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPriceCorrectionAtSameNaturalKeyConflicts(t *testing.T) {
	w, _ := NewWriter(t.TempDir())
	at := time.Now().UTC().Add(-3 * time.Hour)
	bar := price(at)
	if _, _, err := w.WritePrices(securityID, []model.PriceBar{bar}); err != nil {
		t.Fatal(err)
	}
	bar.Close = "2.5"
	if _, _, err := w.WritePrices(securityID, []model.PriceBar{bar}); !errors.Is(err, ErrNaturalKeyConflict) {
		t.Fatalf("got %v", err)
	}
}

func TestPriceChangedRawPayloadAtSameNaturalKeyConflicts(t *testing.T) {
	w, _ := NewWriter(t.TempDir())
	bar := price(time.Now().UTC().Add(-3 * time.Hour))
	if _, _, err := w.WritePrices(securityID, []model.PriceBar{bar}); err != nil {
		t.Fatal(err)
	}
	bar.RawPayloadHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	bar.Provenance.RawPayloadHash = bar.RawPayloadHash
	if _, _, err := w.WritePrices(securityID, []model.PriceBar{bar}); !errors.Is(err, ErrNaturalKeyConflict) {
		t.Fatalf("got %v", err)
	}
}

func TestSameNaturalKeyComparisonsPreserveRawPayloadIdentity(t *testing.T) {
	priceA := PriceRow{RawPayloadHash: rawHash}
	priceB := priceA
	priceB.RawPayloadHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	fundamentalA := FundamentalRow{RawPayloadHash: rawHash}
	fundamentalB := fundamentalA
	fundamentalB.RawPayloadHash = priceB.RawPayloadHash
	economicA := EconomicRow{RawPayloadHash: rawHash}
	economicB := economicA
	economicB.RawPayloadHash = priceB.RawPayloadHash
	if samePrice(priceA, priceB) || sameFundamental(fundamentalA, fundamentalB) || sameEconomic(economicA, economicB) {
		t.Fatal("raw payload identity was ignored")
	}
}

func TestWritersStoreCanonicalDecimalStrings(t *testing.T) {
	root := t.TempDir()
	w, _ := NewWriter(root)
	at := time.Date(2024, 1, 2, 20, 0, 0, 0, time.UTC)
	bar := price(at)
	bar.Open, bar.High, bar.Low, bar.Close, bar.Volume = "01.000", "03.00", "001", "02.5000", "00010.00"
	pricePath, _, err := w.WritePrices(securityID, []model.PriceBar{bar})
	if err != nil {
		t.Fatal(err)
	}
	priceRows, _ := parquet.ReadFile[PriceRow](pricePath)
	if got := []string{priceRows[0].Open, priceRows[0].High, priceRows[0].Low, priceRows[0].Close, priceRows[0].Volume}; !equalStrings(got, []string{"1", "3", "1", "2.5", "10"}) {
		t.Fatalf("price decimals=%v", got)
	}

	end := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	pub := time.Date(2024, 2, 2, 21, 0, 0, 0, time.UTC)
	fundamental := model.FundamentalObservation{Source: "sec", IssuerID: issuerID, Taxonomy: "us-gaap", Concept: "Revenue", Unit: "USD", Currency: "USD", Value: "00123.5000", PeriodEnd: end, FiscalPeriod: "FY", Temporal: model.Temporal{ObservedAt: end, PublishedAt: pub, AvailableAt: pub, IngestedAt: pub.Add(time.Hour)}, RawPayloadHash: rawHash, Provenance: provenance(pub.Add(time.Hour))}
	fundamentalPath, _, err := w.WriteFundamentals(issuerID, []model.FundamentalObservation{fundamental})
	if err != nil {
		t.Fatal(err)
	}
	fundamentalRows, _ := parquet.ReadFile[FundamentalRow](fundamentalPath)
	if fundamentalRows[0].Value != "123.5" {
		t.Fatalf("fundamental value=%q", fundamentalRows[0].Value)
	}

	economic := model.EconomicObservation{Source: "fred", SeriesID: "CANON", Geography: "US", Unit: "Index", Frequency: "monthly", Value: "0007.2500", Temporal: model.Temporal{ObservedAt: at, PublishedAt: at.Add(time.Hour), AvailableAt: at.Add(time.Hour), IngestedAt: at.Add(2 * time.Hour)}, RawPayloadHash: rawHash, Provenance: provenance(at.Add(2 * time.Hour))}
	economicPath, _, err := w.WriteEconomics("CANON", []model.EconomicObservation{economic})
	if err != nil {
		t.Fatal(err)
	}
	economicRows, _ := parquet.ReadFile[EconomicRow](economicPath)
	if economicRows[0].Value != "7.25" {
		t.Fatalf("economic value=%q", economicRows[0].Value)
	}
}

func TestFundamentalNullablePhysicalFieldsAndConflict(t *testing.T) {
	w, _ := NewWriter(t.TempDir())
	end := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	pub := time.Date(2024, 2, 2, 21, 0, 0, 0, time.UTC)
	o := model.FundamentalObservation{Source: "sec", IssuerID: issuerID, Taxonomy: "us-gaap", Concept: "Revenue", Unit: "USD", Currency: "USD", Value: "123.50", Revision: 0, PeriodEnd: end, AccessionNumber: "a", FiscalPeriod: "FY", Temporal: model.Temporal{ObservedAt: end, PublishedAt: pub, AvailableAt: pub, IngestedAt: pub.Add(time.Hour)}, RawPayloadHash: rawHash, Provenance: provenance(pub.Add(time.Hour))}
	path, n, err := w.WriteFundamentals(issuerID, []model.FundamentalObservation{o})
	if err != nil || n != 1 {
		t.Fatal(err)
	}
	rows, _ := parquet.ReadFile[FundamentalRow](path)
	if rows[0].HasSecurityID || rows[0].PeriodStartTime() != nil || !rows[0].HasCurrency || rows[0].Value != "123.5" {
		t.Fatalf("row=%+v", rows[0])
	}
	o.Value = "124"
	if _, _, err := w.WriteFundamentals(issuerID, []model.FundamentalObservation{o}); !errors.Is(err, ErrNaturalKeyConflict) {
		t.Fatalf("got %v", err)
	}
}

func TestFundamentalNaturalKeyIncludesTaxonomyAndUnit(t *testing.T) {
	w, _ := NewWriter(t.TempDir())
	end := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	pub := time.Date(2024, 2, 2, 21, 0, 0, 0, time.UTC)
	base := model.FundamentalObservation{Source: "sec", IssuerID: issuerID, Taxonomy: "us-gaap", Concept: "EntityPublicFloat", Unit: "USD", Currency: "USD", Value: "100", PeriodEnd: end, FiscalPeriod: "FY", Temporal: model.Temporal{ObservedAt: end, PublishedAt: pub, AvailableAt: pub, IngestedAt: pub.Add(time.Hour)}, RawPayloadHash: rawHash, Provenance: provenance(pub.Add(time.Hour))}
	otherTaxonomy := base
	otherTaxonomy.Taxonomy = "dei"
	otherUnit := base
	otherUnit.Unit = "shares"
	path, n, err := w.WriteFundamentals(issuerID, []model.FundamentalObservation{base, otherTaxonomy, otherUnit})
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	rows, err := parquet.ReadFile[FundamentalRow](path)
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
}

func TestMacroRevisionsNoOpUnchangedAndPreserveABA(t *testing.T) {
	w, _ := NewWriter(t.TempDir())
	observed := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	pub := observed.Add(24 * time.Hour)
	base := model.EconomicObservation{Source: "fred", SeriesID: "X", Geography: "US", Frequency: "monthly", Unit: "Index", Value: "1", Temporal: model.Temporal{ObservedAt: observed, PublishedAt: pub, AvailableAt: pub, IngestedAt: pub.Add(time.Hour)}, RawPayloadHash: rawHash, Provenance: provenance(pub.Add(time.Hour))}
	path, n, err := w.WriteEconomics("X", []model.EconomicObservation{base})
	if err != nil || n != 1 {
		t.Fatal(err)
	}
	same := base
	same.Temporal.PublishedAt = pub.Add(time.Hour)
	same.Temporal.AvailableAt = same.Temporal.PublishedAt
	same.Temporal.IngestedAt = same.Temporal.PublishedAt.Add(time.Hour)
	same.Provenance.IngestedAt = same.Temporal.IngestedAt
	if _, n, err = w.WriteEconomics("X", []model.EconomicObservation{same}); err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	b := same
	b.Value = "2"
	if _, _, err = w.WriteEconomics("X", []model.EconomicObservation{b}); err != nil {
		t.Fatal(err)
	}
	a2 := b
	a2.Value = "1"
	a2.Temporal.PublishedAt = a2.Temporal.PublishedAt.Add(time.Hour)
	a2.Temporal.AvailableAt = a2.Temporal.PublishedAt
	a2.Temporal.IngestedAt = a2.Temporal.PublishedAt.Add(time.Hour)
	a2.Provenance.IngestedAt = a2.Temporal.IngestedAt
	if _, _, err = w.WriteEconomics("X", []model.EconomicObservation{a2}); err != nil {
		t.Fatal(err)
	}
	rows, _ := parquet.ReadFile[EconomicRow](path)
	if len(rows) != 3 || rows[0].Revision != 0 || rows[1].Revision != 1 || rows[2].Revision != 2 {
		t.Fatalf("rows=%+v", rows)
	}
}

func TestPreV1FailsClosedWithoutOverwrite(t *testing.T) {
	root := t.TempDir()
	w, _ := NewWriter(root)
	path := filepath.Join(root, "prices", "source=yahoo", "security_id="+securityID, "data.parquet")
	_ = os.MkdirAll(filepath.Dir(path), 0o750)
	type legacy struct {
		Source string `parquet:"source"`
	}
	if err := parquet.WriteFile(path, []legacy{{"yahoo"}}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	_, _, err := w.WritePrices(securityID, []model.PriceBar{price(time.Now().UTC().Add(-3 * time.Hour))})
	if !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("got %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("legacy file overwritten")
	}
}

func TestIncompleteV1PhysicalSchemaFailsClosed(t *testing.T) {
	root := t.TempDir()
	w, _ := NewWriter(root)
	path := pricePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	type incompleteV1 struct {
		SchemaVersion string `parquet:"schema_version"`
		Source        string `parquet:"source"`
	}
	if err := parquet.WriteFile(path, []incompleteV1{{SchemaVersion: model.SchemaVersion, Source: "yahoo"}}); err != nil {
		t.Fatal(err)
	}
	_, _, err := w.WritePrices(securityID, []model.PriceBar{price(time.Now().UTC().Add(-3 * time.Hour))})
	if !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("got %v", err)
	}
}

func TestExistingDuplicateNaturalKeysFailClosed(t *testing.T) {
	root := t.TempDir()
	w, _ := NewWriter(root)
	bar := price(time.Now().UTC().Add(-3 * time.Hour))
	row, err := priceRow(bar)
	if err != nil {
		t.Fatal(err)
	}
	path := pricePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := parquet.WriteFile(path, []PriceRow{row, row}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.WritePrices(securityID, []model.PriceBar{bar}); !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("got %v", err)
	}
}

func TestInvalidExistingV1RowFailsClosed(t *testing.T) {
	root := t.TempDir()
	w, _ := NewWriter(root)
	bar := price(time.Now().UTC().Add(-3 * time.Hour))
	row, err := priceRow(bar)
	if err != nil {
		t.Fatal(err)
	}
	row.Open = "01.000"
	path := pricePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := parquet.WriteFile(path, []PriceRow{row}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.WritePrices(securityID, []model.PriceBar{bar}); !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("got %v", err)
	}
}

func TestExistingPartitionIdentityMismatchFailsClosed(t *testing.T) {
	root := t.TempDir()
	w, _ := NewWriter(root)
	bar := price(time.Now().UTC().Add(-3 * time.Hour))
	row, err := priceRow(bar)
	if err != nil {
		t.Fatal(err)
	}
	row.Source = "other"
	path := pricePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := parquet.WriteFile(path, []PriceRow{row}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.WritePrices(securityID, []model.PriceBar{bar}); !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("got %v", err)
	}
}

func TestTemporalAndProvenanceValidation(t *testing.T) {
	bar := price(time.Now().UTC().Add(-3 * time.Hour))
	bar.Provenance.DataSourceID = ""
	if _, err := priceRow(bar); err == nil {
		t.Fatal("missing provenance accepted")
	}
	bar = price(time.Now().UTC().Add(-3 * time.Hour))
	bar.Temporal.ObservedAt = bar.Temporal.PublishedAt.Add(time.Hour)
	if _, err := priceRow(bar); err == nil {
		t.Fatal("future observation accepted")
	}
	bar = price(time.Now().UTC().Add(-3 * time.Hour))
	bar.Provenance.RawPayloadHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := priceRow(bar); err == nil {
		t.Fatal("mismatched top-level/provenance hash accepted")
	}

	end := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	start := end.Add(24 * time.Hour)
	pub := end.Add(48 * time.Hour)
	fundamental := model.FundamentalObservation{Source: "sec", IssuerID: issuerID, Taxonomy: "us-gaap", Concept: "Revenue", Unit: "USD", Currency: "USD", Value: "1", PeriodStart: &start, PeriodEnd: end, FiscalPeriod: "FY", Temporal: model.Temporal{ObservedAt: end, PublishedAt: pub, AvailableAt: pub, IngestedAt: pub.Add(time.Hour)}, RawPayloadHash: rawHash, Provenance: provenance(pub.Add(time.Hour))}
	if _, err := fundamentalRow(fundamental); err == nil {
		t.Fatal("period_start after period_end accepted")
	}
}

func pricePath(root string) string {
	return filepath.Join(root, "prices", "source=yahoo", "security_id="+securityID, "data.parquet")
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
