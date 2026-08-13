package normalize

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	}{{"price", columnsOf[PriceRow](), []string{"schema_version", "security_id", "interval", "price_basis", "currency", "observed_at", "observed_precision", "published_at", "has_published_at", "open", "high", "low", "close", "volume", "has_volume", "data_source_id", "ingestion_run_id", "raw_payload_hash", "ingested_at"}}, {"fundamental", columnsOf[FundamentalRow](), []string{"schema_version", "issuer_id", "security_id", "has_security_id", "concept", "value", "has_value", "unit", "currency", "has_currency", "period_start", "has_period_start", "period_end", "fiscal_period", "observed_at", "observed_precision", "published_at", "revision", "data_source_id", "ingestion_run_id"}}, {"economic", columnsOf[EconomicRow](), []string{"schema_version", "series_id", "geography", "value", "has_value", "unit", "frequency", "seasonal_adjustment", "has_seasonal_adjustment", "observed_at", "observed_precision", "published_at", "revision", "data_source_id", "ingestion_run_id"}}}
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
	at = at.Truncate(time.Microsecond)
	return model.PriceBar{Source: "yahoo", SecurityID: securityID, Interval: "1d", PriceBasis: "raw", Currency: "USD", Temporal: model.Temporal{ObservedAt: at, PublishedAt: at.Add(time.Hour), AvailableAt: at.Add(time.Hour), IngestedAt: at.Add(2 * time.Hour), PublishedPrecision: model.PrecisionSecond}, Open: "1.000000000000000001", High: "3", Low: "1", Close: "2", Volume: "10", RawPayloadHash: rawHash, Provenance: provenance(at.Add(2 * time.Hour))}
}

func TestObservedPrecisionIsValidatedAndDefaultsToUnknown(t *testing.T) {
	bar := price(time.Date(2024, 1, 2, 20, 0, 0, 0, time.UTC))
	row, err := priceRow(bar)
	if err != nil {
		t.Fatal(err)
	}
	if row.ObservedPrecision != string(model.PrecisionUnknown) {
		t.Fatalf("observed_precision=%q want unknown", row.ObservedPrecision)
	}
	bar.Temporal.ObservedPrecision = model.TimePrecision("millisecond")
	if _, err := priceRow(bar); err == nil || !strings.Contains(err.Error(), "observed_precision") {
		t.Fatalf("invalid observed precision error=%v", err)
	}
	bar = price(time.Date(2024, 1, 2, 20, 0, 0, 123000000, time.UTC))
	bar.Temporal.ObservedPrecision = model.PrecisionSecond
	if _, err := priceRow(bar); err == nil || !strings.Contains(err.Error(), "whole-second") {
		t.Fatalf("inconsistent observed precision error=%v", err)
	}
}

func TestObservedPrecisionPhysicalTypeIsUTF8(t *testing.T) {
	column, ok := parquet.SchemaOf(new(PriceRow)).Lookup("observed_precision")
	if !ok || column.Node.Type().Kind() != parquet.ByteArray || column.Node.Type().LogicalType() == nil || column.Node.Type().LogicalType().UTF8 == nil {
		t.Fatalf("observed_precision schema=%v, want UTF8 BYTE_ARRAY", column.Node)
	}
}

type legacyPriceRow struct {
	SchemaVersion      string `parquet:"schema_version"`
	Source             string `parquet:"source"`
	SecurityID         string `parquet:"security_id"`
	Interval           string `parquet:"interval"`
	PriceBasis         string `parquet:"price_basis"`
	Currency           string `parquet:"currency"`
	ObservedAt         int64  `parquet:"observed_at,timestamp(microsecond:utc)"`
	PublishedAt        int64  `parquet:"published_at,timestamp(microsecond:utc)"`
	HasPublishedAt     bool   `parquet:"has_published_at"`
	PublishedPrecision string `parquet:"published_precision"`
	AvailableAt        int64  `parquet:"available_at,timestamp(microsecond:utc)"`
	IngestedAt         int64  `parquet:"ingested_at,timestamp(microsecond:utc)"`
	Open               string `parquet:"open"`
	High               string `parquet:"high"`
	Low                string `parquet:"low"`
	Close              string `parquet:"close"`
	Volume             string `parquet:"volume"`
	HasVolume          bool   `parquet:"has_volume"`
	RawPayloadHash     string `parquet:"raw_payload_hash"`
	DataSourceID       string `parquet:"data_source_id"`
	IngestionRunID     string `parquet:"ingestion_run_id"`
	RawRecordLocator   string `parquet:"raw_record_locator"`
	NormalizerVersion  string `parquet:"normalizer_version"`
}

func legacyPriceRowFrom(r PriceRow) legacyPriceRow {
	return legacyPriceRow{
		SchemaVersion: r.SchemaVersion, Source: r.Source, SecurityID: r.SecurityID, Interval: r.Interval, PriceBasis: r.PriceBasis, Currency: r.Currency,
		ObservedAt: r.ObservedAt, PublishedAt: r.PublishedAt, HasPublishedAt: r.HasPublishedAt, PublishedPrecision: r.PublishedPrecision,
		AvailableAt: r.AvailableAt, IngestedAt: r.IngestedAt, Open: r.Open, High: r.High, Low: r.Low, Close: r.Close, Volume: r.Volume,
		HasVolume: r.HasVolume, RawPayloadHash: r.RawPayloadHash, DataSourceID: r.DataSourceID, IngestionRunID: r.IngestionRunID,
		RawRecordLocator: r.RawRecordLocator, NormalizerVersion: r.NormalizerVersion,
	}
}

func TestExistingV1PartWithoutObservedPrecisionDefaultsToUnknown(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Dir(pricePath(root))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	row, err := priceRow(price(time.Date(2024, 1, 2, 20, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	partTemp := filepath.Join(dir, "legacy.parquet")
	if err := parquet.WriteFile(partTemp, []legacyPriceRow{legacyPriceRowFrom(row)}); err != nil {
		t.Fatal(err)
	}
	hash, err := sha256File(partTemp)
	if err != nil {
		t.Fatal(err)
	}
	partPath := filepath.Join(dir, contentPartFilename(hash))
	if err := os.Rename(partTemp, partPath); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{ManifestVersion: ManifestVersion, SchemaVersion: model.SchemaVersion, NormalizerVersion: row.NormalizerVersion, GitCommit: UnknownGitCommit, Source: row.Source, DataSourceID: row.DataSourceID, IngestionRunID: row.IngestionRunID, Partition: map[string]string{"dataset": "prices", "source": row.Source, "security_id": row.SecurityID}, RowCount: 1, Parts: []ManifestPart{{Path: filepath.Base(partPath), SHA256: hash, RowCount: 1}}}
	if err := writeManifest(filepath.Join(dir, ManifestFilename), manifest, defaultPublicationOps()); err != nil {
		t.Fatal(err)
	}
	got, err := readCommitted[PriceRow](dir, manifest.Partition)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ObservedPrecision != string(model.PrecisionUnknown) {
		t.Fatalf("rows=%+v want one unknown observed precision", got)
	}
}

func TestParquetRejectsSubMicrosecondTimestamps(t *testing.T) {
	at := time.Date(2024, 1, 2, 20, 0, 0, 123456000, time.UTC)
	cases := []struct {
		name  string
		input func() error
	}{
		{"observed_at", func() error {
			bar := price(at)
			bar.Temporal.ObservedAt = at.Add(time.Nanosecond)
			_, err := priceRow(bar)
			return err
		}},
		{"published_at", func() error {
			bar := price(at)
			bar.Temporal.PublishedAt = at.Add(time.Nanosecond)
			_, err := priceRow(bar)
			return err
		}},
		{"available_at", func() error {
			bar := price(at)
			bar.Temporal.AvailableAt = at.Add(time.Hour + time.Nanosecond)
			_, err := priceRow(bar)
			return err
		}},
		{"ingested_at", func() error {
			bar := price(at)
			bar.Temporal.IngestedAt = at.Add(2*time.Hour + time.Nanosecond)
			_, err := priceRow(bar)
			return err
		}},
		{"vintage_at", func() error {
			vintage := at.Add(3*time.Hour + time.Nanosecond)
			o := model.EconomicObservation{Source: "fred", SeriesID: "X", Geography: "US", Frequency: "monthly", Unit: "Index", Value: "1", VintageAt: &vintage, Temporal: model.Temporal{ObservedAt: at, PublishedAt: at.Add(time.Hour), AvailableAt: at.Add(time.Hour), IngestedAt: at.Add(2 * time.Hour)}, RawPayloadHash: rawHash, Provenance: provenance(at.Add(2 * time.Hour))}
			_, err := economicRow(o)
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.input()
			if err == nil || !strings.Contains(err.Error(), "sub-microsecond") {
				t.Fatalf("err=%v, want sub-microsecond precision rejection", err)
			}
		})
	}
}

func TestParquetExactMicrosecondTimestampsRoundTrip(t *testing.T) {
	at := time.Date(2024, 1, 2, 20, 0, 0, 123456000, time.UTC)
	bar := price(at)
	w, err := NewWriter(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pricePath, _, err := w.WritePrices(securityID, []model.PriceBar{bar})
	if err != nil {
		t.Fatal(err)
	}
	priceRows := rowsFromManifest[PriceRow](t, pricePath)
	if len(priceRows) != 1 {
		t.Fatalf("price rows=%d, want 1", len(priceRows))
	}
	storedPrice := priceRows[0]
	if storedPrice.ObservedPrecision != string(model.PrecisionUnknown) {
		t.Fatalf("observed precision=%q want unknown", storedPrice.ObservedPrecision)
	}
	priceWant := []time.Time{bar.Temporal.ObservedAt, bar.Temporal.PublishedAt, bar.Temporal.AvailableAt, bar.Temporal.IngestedAt}
	priceGot := []time.Time{time.UnixMicro(storedPrice.ObservedAt).UTC(), time.UnixMicro(storedPrice.PublishedAt).UTC(), time.UnixMicro(storedPrice.AvailableAt).UTC(), time.UnixMicro(storedPrice.IngestedAt).UTC()}
	for i := range priceWant {
		if !priceGot[i].Equal(priceWant[i]) {
			t.Fatalf("price timestamp %d: got %s want %s", i, priceGot[i], priceWant[i])
		}
	}

	vintage := at.Add(3 * time.Hour)
	economic := model.EconomicObservation{Source: "fred", SeriesID: "X", Geography: "US", Frequency: "monthly", Unit: "Index", Value: "1", VintageAt: &vintage, Temporal: bar.Temporal, RawPayloadHash: rawHash, Provenance: bar.Provenance}
	economicPath, _, err := w.WriteEconomics("X", []model.EconomicObservation{economic})
	if err != nil {
		t.Fatal(err)
	}
	economicRows := rowsFromManifest[EconomicRow](t, economicPath)
	if len(economicRows) != 1 {
		t.Fatalf("economic rows=%d, want 1", len(economicRows))
	}
	if got := economicRows[0].VintageTime(); got == nil || !got.Equal(vintage) {
		t.Fatalf("vintage timestamp: got %v want %s", got, vintage)
	}
}

func TestDateOnlyPeriodFieldsRemainDayPrecision(t *testing.T) {
	periodEnd := time.Date(2023, 12, 31, 23, 59, 59, 123456789, time.UTC)
	pub := time.Date(2024, 1, 2, 21, 0, 0, 0, time.UTC)
	o := model.FundamentalObservation{Source: "sec", IssuerID: issuerID, Taxonomy: "us-gaap", Concept: "Revenue", Unit: "USD", Value: "1", PeriodEnd: periodEnd, FiscalPeriod: "FY", Temporal: model.Temporal{ObservedAt: time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC), PublishedAt: pub, AvailableAt: pub, IngestedAt: pub.Add(time.Hour)}, RawPayloadHash: rawHash, Provenance: provenance(pub.Add(time.Hour))}
	r, err := fundamentalRow(o)
	if err != nil {
		t.Fatal(err)
	}
	if r.PeriodEnd != days(periodEnd) {
		t.Fatalf("period_end=%d want %d", r.PeriodEnd, days(periodEnd))
	}
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
	rows := rowsFromManifest[PriceRow](t, path)
	if len(rows) != 1 {
		t.Fatalf("rows=%d", len(rows))
	}
	r := rows[0]
	if r.SchemaVersion != "1.0.0" || r.Open != "1.000000000000000001" || r.Interval != "1d" || r.PriceBasis != "raw" || r.DataSourceID != sourceID {
		t.Fatalf("row=%+v", r)
	}
	if output := os.Getenv("INVS_PARQUET_TEST_OUTPUT"); output != "" {
		b, err := os.ReadFile(manifestPartPath(t, path))
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

func TestPriceSameRawPayloadWithDifferentTimingIsNoOp(t *testing.T) {
	w, _ := NewWriter(t.TempDir())
	at := time.Date(2024, 1, 2, 20, 0, 0, 0, time.UTC)
	original := price(at)
	path, n, err := w.WritePrices(securityID, []model.PriceBar{original})
	if err != nil || n != 1 {
		t.Fatalf("initial n=%d err=%v", n, err)
	}
	want := rowsFromManifest[PriceRow](t, path)
	if len(want) != 1 {
		t.Fatalf("initial rows=%d", len(want))
	}

	retry := original
	retry.Temporal.PublishedAt = at.Add(3 * time.Hour)
	retry.Temporal.AvailableAt = at.Add(4 * time.Hour)
	retry.Temporal.IngestedAt = at.Add(5 * time.Hour)
	retry.Provenance.IngestionRunID = "b2468ace-1357-4bdf-9024-6e2f59b9527a"
	retry.Provenance.IngestedAt = retry.Temporal.IngestedAt

	gotPath, n, err := w.WritePrices(securityID, []model.PriceBar{retry})
	if err != nil || n != 0 {
		t.Fatalf("retry path=%q n=%d err=%v", gotPath, n, err)
	}
	got := rowsFromManifest[PriceRow](t, gotPath)
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("canonical row changed: got=%+v want=%+v", got, want)
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
	priceRows := rowsFromManifest[PriceRow](t, pricePath)
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
	fundamentalRows := rowsFromManifest[FundamentalRow](t, fundamentalPath)
	if fundamentalRows[0].Value != "123.5" {
		t.Fatalf("fundamental value=%q", fundamentalRows[0].Value)
	}

	economic := model.EconomicObservation{Source: "fred", SeriesID: "CANON", Geography: "US", Unit: "Index", Frequency: "monthly", Value: "0007.2500", Temporal: model.Temporal{ObservedAt: at, PublishedAt: at.Add(time.Hour), AvailableAt: at.Add(time.Hour), IngestedAt: at.Add(2 * time.Hour)}, RawPayloadHash: rawHash, Provenance: provenance(at.Add(2 * time.Hour))}
	economicPath, _, err := w.WriteEconomics("CANON", []model.EconomicObservation{economic})
	if err != nil {
		t.Fatal(err)
	}
	economicRows := rowsFromManifest[EconomicRow](t, economicPath)
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
	rows := rowsFromManifest[FundamentalRow](t, path)
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
	rows := rowsFromManifest[FundamentalRow](t, path)
	if len(rows) != 3 {
		t.Fatalf("rows=%d", len(rows))
	}
}

func TestFundamentalNaturalKeySeparatesSECFilingIdentity(t *testing.T) {
	root := t.TempDir()
	w, _ := NewWriter(root)
	end := time.Date(2008, 9, 27, 0, 0, 0, 0, time.UTC)
	published := time.Date(2010, 1, 27, 0, 0, 0, 0, time.UTC)
	first := model.FundamentalObservation{Source: "sec", IssuerID: issuerID, Taxonomy: "us-gaap", Concept: "CashAndCashEquivalentsAtCarryingValue", Unit: "USD", Currency: "USD", Value: "11875000000", PeriodEnd: end, AccessionNumber: "0001193125-10-012085", Form: "10-Q", FiscalYear: 2010, FiscalPeriod: "Q1", Temporal: model.Temporal{ObservedAt: end, PublishedAt: published, AvailableAt: published, IngestedAt: published.Add(time.Hour)}, RawPayloadHash: rawHash, Provenance: provenance(published.Add(time.Hour))}
	second := first
	second.AccessionNumber = "0001193125-10-012091"
	second.Form = "10-K/A"
	second.FiscalYear = 2009
	second.FiscalPeriod = "FY"
	second.Frame = "CY2008Q3I"

	path, n, err := w.WriteFundamentals(issuerID, []model.FundamentalObservation{first, second})
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	rows := rowsFromManifest[FundamentalRow](t, path)
	if len(rows) != 2 {
		t.Fatalf("rows=%d want 2", len(rows))
	}
	if rows[0].AccessionNumber == rows[1].AccessionNumber || rows[0].Frame == rows[1].Frame {
		t.Fatalf("filing identity was not preserved: %+v", rows)
	}
	if rows[0].PublishedAt != rows[1].PublishedAt || rows[0].PeriodEnd != rows[1].PeriodEnd {
		t.Fatalf("test facts do not share the colliding dimensions: %+v", rows)
	}
}

func TestFundamentalExactSECRetryIsIdempotent(t *testing.T) {
	root := t.TempDir()
	w, _ := NewWriter(root)
	end := time.Date(2008, 9, 27, 0, 0, 0, 0, time.UTC)
	published := time.Date(2010, 1, 27, 0, 0, 0, 0, time.UTC)
	fact := model.FundamentalObservation{Source: "sec", IssuerID: issuerID, Taxonomy: "us-gaap", Concept: "CashAndCashEquivalentsAtCarryingValue", Unit: "USD", Currency: "USD", Value: "11875000000", PeriodEnd: end, AccessionNumber: "0001193125-10-012091", Form: "10-K/A", FiscalYear: 2009, FiscalPeriod: "FY", Frame: "CY2008Q3I", Temporal: model.Temporal{ObservedAt: end, PublishedAt: published, AvailableAt: published, IngestedAt: published.Add(time.Hour)}, RawPayloadHash: rawHash, Provenance: provenance(published.Add(time.Hour))}
	path, n, err := w.WriteFundamentals(issuerID, []model.FundamentalObservation{fact})
	if err != nil || n != 1 {
		t.Fatalf("initial n=%d err=%v", n, err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	retry := fact
	retry.Temporal.IngestedAt = published.Add(2 * time.Hour)
	retry.Provenance.IngestedAt = retry.Temporal.IngestedAt
	retry.Provenance.IngestionRunID = "b2468ace-1357-4bdf-9024-6e2f59b9527a"
	gotPath, n, err := w.WriteFundamentals(issuerID, []model.FundamentalObservation{retry})
	if err != nil || n != 0 || gotPath != path {
		t.Fatalf("retry path=%q n=%d err=%v", gotPath, n, err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("idempotent retry replaced manifest")
	}
	rows := rowsFromManifest[FundamentalRow](t, path)
	if len(rows) != 1 || rows[0].AccessionNumber != fact.AccessionNumber || rows[0].Frame != fact.Frame {
		t.Fatalf("retry duplicated or changed fact: %+v", rows)
	}
}

func TestMacroRevisionsNoOpUnchangedAndPreserveABA(t *testing.T) {
	w, _ := NewWriter(t.TempDir())
	observed := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	pub := observed.Add(24 * time.Hour)
	base := model.EconomicObservation{Source: "fred", SeriesID: "X", Geography: "US", Frequency: "monthly", Unit: "Index", Value: "1", Temporal: model.Temporal{ObservedAt: observed, PublishedAt: pub, AvailableAt: pub, IngestedAt: pub.Add(time.Hour)}, RawPayloadHash: rawHash, Provenance: provenance(pub.Add(time.Hour))}
	first := []model.EconomicObservation{base}
	path, n, err := w.WriteEconomics("X", first)
	if err != nil || n != 1 {
		t.Fatal(err)
	}
	if first[0].Revision != 0 {
		t.Fatalf("initial input revision=%d, want 0", first[0].Revision)
	}
	same := base
	same.Temporal.PublishedAt = pub.Add(time.Hour)
	same.Temporal.AvailableAt = same.Temporal.PublishedAt
	same.Temporal.IngestedAt = same.Temporal.PublishedAt.Add(time.Hour)
	same.Provenance.IngestedAt = same.Temporal.IngestedAt
	sameInput := []model.EconomicObservation{same}
	if _, n, err = w.WriteEconomics("X", sameInput); err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if sameInput[0].Revision != 0 {
		t.Fatalf("unchanged input revision=%d, want 0", sameInput[0].Revision)
	}
	b := same
	b.Value = "2"
	changedInput := []model.EconomicObservation{b}
	if _, _, err = w.WriteEconomics("X", changedInput); err != nil {
		t.Fatal(err)
	}
	if changedInput[0].Revision != 1 {
		t.Fatalf("changed input revision=%d, want 1", changedInput[0].Revision)
	}
	duplicate := changedInput[0]
	duplicate.Revision = 0
	duplicateInput := []model.EconomicObservation{duplicate}
	if _, n, err = w.WriteEconomics("X", duplicateInput); err != nil || n != 0 {
		t.Fatalf("duplicate n=%d err=%v", n, err)
	}
	if duplicateInput[0].Revision != 1 {
		t.Fatalf("duplicate input revision=%d, want 1", duplicateInput[0].Revision)
	}
	a2 := b
	a2.Value = "1"
	a2.Temporal.PublishedAt = a2.Temporal.PublishedAt.Add(time.Hour)
	a2.Temporal.AvailableAt = a2.Temporal.PublishedAt
	a2.Temporal.IngestedAt = a2.Temporal.PublishedAt.Add(time.Hour)
	a2.Provenance.IngestedAt = a2.Temporal.IngestedAt
	abaInput := []model.EconomicObservation{a2}
	if _, _, err = w.WriteEconomics("X", abaInput); err != nil {
		t.Fatal(err)
	}
	if abaInput[0].Revision != 2 {
		t.Fatalf("ABA input revision=%d, want 2", abaInput[0].Revision)
	}
	rows := rowsFromManifest[EconomicRow](t, path)
	if len(rows) != 3 || rows[0].Revision != 0 || rows[1].Revision != 1 || rows[2].Revision != 2 {
		t.Fatalf("rows=%+v", rows)
	}
}

func TestWriteEconomicsSupportsBCBPartitionIdentity(t *testing.T) {
	root := t.TempDir()
	w, _ := NewWriter(root)
	observed := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	published := observed.Add(24 * time.Hour)
	fact := model.EconomicObservation{Source: "bcb", SeriesID: "432", Geography: "BR", Unit: "percent", Frequency: "monthly", Value: "1.25", Temporal: model.Temporal{ObservedAt: observed, PublishedAt: published, AvailableAt: published, IngestedAt: published.Add(time.Hour)}, RawPayloadHash: rawHash, Provenance: provenance(published.Add(time.Hour))}
	path, n, err := w.WriteEconomics(fact.SeriesID, []model.EconomicObservation{fact})
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	wantPath := filepath.Join(root, "macroeconomics", "source=bcb", "series_id=432", ManifestFilename)
	if path != wantPath {
		t.Fatalf("path=%q want %q", path, wantPath)
	}
	manifest, err := ReadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Source != "bcb" || manifest.Partition["source"] != "bcb" || manifest.Partition["series_id"] != fact.SeriesID {
		t.Fatalf("manifest identity=%+v source=%q", manifest.Partition, manifest.Source)
	}
	rows := rowsFromManifest[EconomicRow](t, path)
	if len(rows) != 1 || rows[0].Source != "bcb" || rows[0].SeriesID != fact.SeriesID {
		t.Fatalf("rows=%+v", rows)
	}
	if _, n, err := w.WriteEconomics(fact.SeriesID, []model.EconomicObservation{fact}); err != nil || n != 0 {
		t.Fatalf("retry n=%d err=%v", n, err)
	}
	if err := w.ValidateExisting(); err != nil {
		t.Fatalf("ValidateExisting: %v", err)
	}
}

func TestWriteEconomicsPreservesALFREDHistoricalVintageIdentity(t *testing.T) {
	root := t.TempDir()
	w, _ := NewWriter(root)
	observed := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	makeVintage := func(day int, value string, revision int) model.EconomicObservation {
		published := time.Date(2020, time.February, day, 0, 0, 0, 0, time.UTC)
		vintage := published
		ingested := time.Date(2020, time.March, 1, 0, 0, 0, 0, time.UTC)
		return model.EconomicObservation{
			Source: "alfred", SeriesID: "CPIAUCSL", Geography: "US", Unit: "index",
			Frequency: "monthly", Value: value, Revision: revision, VintageAt: &vintage,
			Temporal: model.Temporal{
				ObservedAt: observed, ObservedPrecision: model.PrecisionDate,
				PublishedAt: published, PublishedPrecision: model.PrecisionDate,
				AvailableAt: published.Add(36 * time.Hour), IngestedAt: ingested,
			},
			RawPayloadHash: rawHash, Provenance: provenance(ingested),
		}
	}

	input := []model.EconomicObservation{
		makeVintage(1, "1", 0), makeVintage(8, "2", 1), makeVintage(15, "1", 2),
	}
	path, n, err := w.WriteEconomics("CPIAUCSL", input)
	if err != nil || n != 3 {
		t.Fatalf("initial write n=%d err=%v", n, err)
	}
	if input[0].Revision != 0 || input[1].Revision != 1 || input[2].Revision != 2 {
		t.Fatalf("initial revisions changed: %+v", input)
	}

	retry := append([]model.EconomicObservation(nil), input...)
	for i := range retry {
		retry[i].Temporal.IngestedAt = retry[i].Temporal.IngestedAt.Add(time.Hour)
		retry[i].Provenance.IngestedAt = retry[i].Temporal.IngestedAt
		retry[i].Provenance.IngestionRunID = "b2468024-ce38-4b4b-9135-7f3e60ca6318"
	}
	if _, n, err := w.WriteEconomics("CPIAUCSL", retry); err != nil || n != 0 {
		t.Fatalf("retry n=%d err=%v", n, err)
	}
	rows := rowsFromManifest[EconomicRow](t, path)
	if len(rows) != 3 || rows[0].Revision != 0 || rows[1].Revision != 1 || rows[2].Revision != 2 || rows[0].Value != "1" || rows[1].Value != "2" || rows[2].Value != "1" {
		t.Fatalf("historical vintages drifted: %+v", rows)
	}
	if got := filepath.Join(root, "macroeconomics", "source=alfred", "series_id=CPIAUCSL", ManifestFilename); path != got {
		t.Fatalf("path=%q want %q", path, got)
	}
}

func TestWriteEconomicsPreservesALFREDMissingVintage(t *testing.T) {
	w, _ := NewWriter(t.TempDir())
	observed := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	published := time.Date(2020, 2, 1, 0, 0, 0, 0, time.UTC)
	vintage := published
	ingested := published.Add(48 * time.Hour)
	fact := model.EconomicObservation{
		Source: "alfred", SeriesID: "X", Geography: "US", Unit: "index", Frequency: "monthly",
		Revision: 0, VintageAt: &vintage,
		Temporal: model.Temporal{
			ObservedAt: observed, ObservedPrecision: model.PrecisionDate,
			PublishedAt: published, PublishedPrecision: model.PrecisionDate,
			AvailableAt: published.Add(36 * time.Hour), IngestedAt: ingested,
		},
		RawPayloadHash: rawHash, Provenance: provenance(ingested),
	}
	path, n, err := w.WriteEconomics("X", []model.EconomicObservation{fact})
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	rows := rowsFromManifest[EconomicRow](t, path)
	if len(rows) != 1 || rows[0].HasValue || rows[0].Value != "" {
		t.Fatalf("missing vintage not preserved: %+v", rows)
	}
}

func TestWriteEconomicsEmptyInputIsNoOp(t *testing.T) {
	root := t.TempDir()
	w, _ := NewWriter(root)
	path, n, err := w.WriteEconomics("432", nil)
	if err != nil || path != "" || n != 0 {
		t.Fatalf("path=%q n=%d err=%v, want empty no-op", path, n, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("normalized root entries=%v, want none", entries)
	}
	if err := w.ValidateExisting(); err != nil {
		t.Fatalf("ValidateExisting: %v", err)
	}
}

func TestMacroRevisionInvalidAndConflictingInputsFailClosed(t *testing.T) {
	w, _ := NewWriter(t.TempDir())
	observed := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	pub := observed.Add(24 * time.Hour)
	base := model.EconomicObservation{Source: "fred", SeriesID: "X", Geography: "US", Frequency: "monthly", Unit: "Index", Value: "1", Temporal: model.Temporal{ObservedAt: observed, PublishedAt: pub, AvailableAt: pub, IngestedAt: pub.Add(time.Hour)}, RawPayloadHash: rawHash, Provenance: provenance(pub.Add(time.Hour))}
	path, _, err := w.WriteEconomics("X", []model.EconomicObservation{base})
	if err != nil {
		t.Fatal(err)
	}

	invalid := base
	invalid.Geography = ""
	invalidInput := []model.EconomicObservation{invalid}
	if _, _, err := w.WriteEconomics("X", invalidInput); err == nil {
		t.Fatal("invalid economic observation accepted")
	}
	if invalidInput[0].Revision != 0 {
		t.Fatalf("invalid input revision=%d, want unchanged 0", invalidInput[0].Revision)
	}

	conflict := base
	conflict.RawPayloadHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	conflict.Provenance.RawPayloadHash = conflict.RawPayloadHash
	conflictInput := []model.EconomicObservation{conflict}
	if _, _, err := w.WriteEconomics("X", conflictInput); !errors.Is(err, ErrNaturalKeyConflict) {
		t.Fatalf("conflict error=%v", err)
	}
	if conflictInput[0].Revision != 0 {
		t.Fatalf("conflicting input revision=%d, want unchanged 0", conflictInput[0].Revision)
	}

	rows := rowsFromManifest[EconomicRow](t, path)
	if len(rows) != 1 || rows[0].Revision != 0 || rows[0].Value != "1" {
		t.Fatalf("rows changed after failed input: %+v", rows)
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
	if err := w.ValidateExisting(); !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("ValidateExisting error = %v", err)
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

func TestValidateExistingAcceptsCanonicalFiles(t *testing.T) {
	root := t.TempDir()
	w, _ := NewWriter(root)
	if _, _, err := w.WritePrices(securityID, []model.PriceBar{price(time.Now().UTC().Add(-3 * time.Hour))}); err != nil {
		t.Fatal(err)
	}
	if err := w.ValidateExisting(); err != nil {
		t.Fatal(err)
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

func manifestPartPath(t *testing.T, manifestPath string) string {
	t.Helper()
	manifest, err := ReadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Parts) != 1 {
		t.Fatalf("parts=%d", len(manifest.Parts))
	}
	return filepath.Join(filepath.Dir(manifestPath), manifest.Parts[0].Path)
}

func rowsFromManifest[T any](t *testing.T, manifestPath string) []T {
	t.Helper()
	manifest, err := ReadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]T, 0, manifest.RowCount)
	for _, part := range manifest.Parts {
		partRows, err := parquet.ReadFile[T](filepath.Join(filepath.Dir(manifestPath), part.Path))
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, partRows...)
	}
	return rows
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
