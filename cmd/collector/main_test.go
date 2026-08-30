package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luisdourado/invs/config"
	"github.com/luisdourado/invs/internal/metadata"
	"github.com/luisdourado/invs/internal/model"
	"github.com/luisdourado/invs/internal/providers"
	"github.com/luisdourado/invs/internal/providers/b3"
	"github.com/luisdourado/invs/internal/providers/cvm"
	"github.com/luisdourado/invs/internal/storage"
)

const (
	testIssuerID   = "1b3d88f5-55b8-4dc5-a6be-2f77e9e99201"
	testSecurityID = "469fc20f-7d4b-45bb-b827-05f8410e71aa"
	testDataSource = "a4a877d1-48dd-42dc-b86e-8020a4107f69"
	testRunID      = "a135791f-df27-4a4a-8426-6e2f59b9527a"
	testRawHash    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func testRun() metadata.Run {
	return metadata.Run{DataSourceID: testDataSource, ID: testRunID, Status: "running"}
}

func assertStamped(t *testing.T, topLevelHash string, provenance model.Provenance, temporal model.Temporal, run metadata.Run) {
	t.Helper()
	if topLevelHash != testRawHash || provenance.RawPayloadHash != testRawHash {
		t.Fatalf("raw hashes = top-level %q, provenance %q; want %q", topLevelHash, provenance.RawPayloadHash, testRawHash)
	}
	if provenance.DataSourceID != run.DataSourceID || provenance.IngestionRunID != run.ID {
		t.Fatalf("run provenance = %+v, want data source %s and run %s", provenance, run.DataSourceID, run.ID)
	}
	if provenance.RawRecordLocator == "" {
		t.Fatal("raw record locator is empty")
	}
	if !provenance.IngestedAt.Equal(temporal.IngestedAt) {
		t.Fatalf("temporal/provenance ingested_at mismatch: %s != %s", temporal.IngestedAt, provenance.IngestedAt)
	}
}

func TestStampPricesAddsRunProvenance(t *testing.T) {
	ingestedAt := time.Date(2026, 8, 12, 12, 0, 0, 123000000, time.UTC)
	observations := []model.PriceBar{{
		RawPayloadHash: testRawHash,
		Temporal:       model.Temporal{IngestedAt: ingestedAt},
		Provenance:     model.Provenance{RawPayloadHash: testRawHash, RawRecordLocator: "chart/date=2026-08-11"},
	}}
	run := testRun()

	if err := stampPrices(run, testRawHash, observations); err != nil {
		t.Fatal(err)
	}
	assertStamped(t, observations[0].RawPayloadHash, observations[0].Provenance, observations[0].Temporal, run)
}

func TestStampFundamentalsAddsRunProvenance(t *testing.T) {
	ingestedAt := time.Date(2026, 8, 12, 12, 1, 0, 456000000, time.UTC)
	observations := []model.FundamentalObservation{{
		RawPayloadHash: testRawHash,
		Temporal:       model.Temporal{IngestedAt: ingestedAt},
		Provenance:     model.Provenance{RawPayloadHash: testRawHash, RawRecordLocator: "companyfacts/taxonomy=us-gaap/concept=Revenue"},
	}}
	run := testRun()

	if err := stampFundamentals(run, testRawHash, observations); err != nil {
		t.Fatal(err)
	}
	assertStamped(t, observations[0].RawPayloadHash, observations[0].Provenance, observations[0].Temporal, run)
}

func TestStampEconomicsAddsRunProvenance(t *testing.T) {
	ingestedAt := time.Date(2026, 8, 12, 12, 2, 0, 789000000, time.UTC)
	observations := []model.EconomicObservation{{
		RawPayloadHash: testRawHash,
		Temporal:       model.Temporal{IngestedAt: ingestedAt},
		Provenance:     model.Provenance{RawPayloadHash: testRawHash, RawRecordLocator: "csv/date=2026-08-11"},
	}}
	run := testRun()

	if err := stampEconomics(run, testRawHash, observations); err != nil {
		t.Fatal(err)
	}
	assertStamped(t, observations[0].RawPayloadHash, observations[0].Provenance, observations[0].Temporal, run)
}

func TestStampEconomicsUsesEachALFREDRawPageHash(t *testing.T) {
	const secondHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	ingestedAt := time.Date(2026, 8, 12, 12, 2, 0, 0, time.UTC)
	observations := []model.EconomicObservation{
		{RawPayloadHash: testRawHash, Temporal: model.Temporal{IngestedAt: ingestedAt}, Provenance: model.Provenance{RawPayloadHash: testRawHash, RawRecordLocator: "json/offset=0/observations/0"}},
		{RawPayloadHash: secondHash, Temporal: model.Temporal{IngestedAt: ingestedAt}, Provenance: model.Provenance{RawPayloadHash: secondHash, RawRecordLocator: "json/offset=100000/observations/0"}},
	}
	run := testRun()
	if err := stampEconomicsFromRawHashes(run, map[string]string{testRawHash: testRawHash, secondHash: secondHash}, observations); err != nil {
		t.Fatal(err)
	}
	if observations[0].Provenance.RawPayloadHash != testRawHash || observations[1].Provenance.RawPayloadHash != secondHash {
		t.Fatalf("page lineage collapsed: %+v", observations)
	}
	if observations[0].Provenance.IngestionRunID != run.ID || observations[1].Provenance.IngestionRunID != run.ID {
		t.Fatalf("run lineage missing: %+v", observations)
	}
	if err := stampEconomicsFromRawHashes(run, map[string]string{testRawHash: testRawHash}, observations); err == nil {
		t.Fatal("missing stored page hash accepted")
	}
}

type collectorHTTPFake struct {
	payload   []byte
	responses map[string][]byte
}

func (f collectorHTTPFake) Get(ctx context.Context, requestURL string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for marker, payload := range f.responses {
		if strings.Contains(requestURL, marker) {
			return append([]byte(nil), payload...), nil
		}
	}
	if f.payload != nil {
		return append([]byte(nil), f.payload...), nil
	}
	return nil, fmt.Errorf("unexpected collector test request %q", requestURL)
}

type orderingRawStore struct {
	events          []string
	completed       bool
	payloads        [][]byte
	rawKeys         []string
	rawMetadata     []storage.RawMetadata
	manifestPayload []byte
	manifestErr     error
}

func (s *orderingRawStore) Put(ctx context.Context, key string, data io.Reader, meta storage.RawMetadata) (storage.RawMetadata, error) {
	if err := ctx.Err(); err != nil {
		return storage.RawMetadata{}, err
	}
	b, err := io.ReadAll(data)
	if err != nil {
		return storage.RawMetadata{}, err
	}
	hash := sha256.Sum256(b)
	meta.SHA256 = hex.EncodeToString(hash[:])
	meta.Size = int64(len(b))
	if strings.HasPrefix(key, "runs/") {
		if s.manifestErr != nil {
			return storage.RawMetadata{}, s.manifestErr
		}
		s.manifestPayload = append([]byte(nil), b...)
		return meta, nil
	}
	s.completed = true
	s.payloads = append(s.payloads, append([]byte(nil), b...))
	s.rawKeys = append(s.rawKeys, key)
	s.rawMetadata = append(s.rawMetadata, meta)
	s.events = append(s.events, "raw:put:complete")
	return meta, nil
}

func (s *orderingRawStore) Get(context.Context, string) (io.ReadCloser, storage.RawMetadata, error) {
	return nil, storage.RawMetadata{}, errors.New("unexpected raw get in ordering test")
}

type orderingNormalizedStore struct {
	raw                      *orderingRawStore
	expectedRun              metadata.Run
	expectedHash             string
	expectedFilingHash       string
	expectedFilingNormalizer string
	expectedSource           string
	locatorPrefix            string
	filingLocatorPrefix      string
	zeroRows                 bool
	prices                   []model.PriceBar
	fundamentals             []model.FundamentalObservation
	economics                []model.EconomicObservation
	filings                  []model.Filing
	fx                       []model.FXObservation
}

func (s *orderingNormalizedStore) beforeWrite(kind string) error {
	if !s.raw.completed || len(s.raw.events) == 0 {
		return fmt.Errorf("%s canonical writer called before raw Put completed", kind)
	}
	s.raw.events = append(s.raw.events, "canonical:"+kind+":write")
	return nil
}

func (s *orderingNormalizedStore) inspect(source, rawHash, locator string, provenance model.Provenance, temporal model.Temporal) error {
	return s.inspectExpected(source, rawHash, locator, provenance, temporal, s.expectedHash, s.locatorPrefix, model.NormalizerVersion)
}

func (s *orderingNormalizedStore) inspectExpected(source, rawHash, locator string, provenance model.Provenance, temporal model.Temporal, expectedHash, locatorPrefix, expectedNormalizer string) error {
	if source != s.expectedSource {
		return fmt.Errorf("normalized source %q, want %q", source, s.expectedSource)
	}
	if rawHash != expectedHash || provenance.RawPayloadHash != expectedHash {
		return fmt.Errorf("normalized raw hashes = %q and %q, want %q", rawHash, provenance.RawPayloadHash, expectedHash)
	}
	if provenance.DataSourceID != s.expectedRun.DataSourceID || provenance.IngestionRunID != s.expectedRun.ID {
		return fmt.Errorf("normalized run provenance = data_source_id %q, ingestion_run_id %q; want %q, %q", provenance.DataSourceID, provenance.IngestionRunID, s.expectedRun.DataSourceID, s.expectedRun.ID)
	}
	if provenance.RawRecordLocator == "" || !strings.HasPrefix(locator, locatorPrefix) {
		return fmt.Errorf("normalized raw locator %q, want prefix %q", locator, locatorPrefix)
	}
	if provenance.IngestedAt.IsZero() || !provenance.IngestedAt.Equal(temporal.IngestedAt) {
		return fmt.Errorf("normalized ingested_at mismatch: provenance=%s temporal=%s", provenance.IngestedAt, temporal.IngestedAt)
	}
	if provenance.NormalizerVersion != expectedNormalizer {
		return fmt.Errorf("normalized version %q, want %q", provenance.NormalizerVersion, expectedNormalizer)
	}
	return nil
}

func (s *orderingNormalizedStore) WritePrices(_ string, observations []model.PriceBar) (string, int, error) {
	if err := s.beforeWrite("prices"); err != nil {
		return "", 0, err
	}
	for _, observation := range observations {
		if err := s.inspect(observation.Source, observation.RawPayloadHash, observation.Provenance.RawRecordLocator, observation.Provenance, observation.Temporal); err != nil {
			return "", 0, err
		}
	}
	s.prices = append(s.prices, observations...)
	rows := len(observations)
	if s.zeroRows {
		rows = 0
	}
	return "test/data.parquet", rows, nil
}

func (s *orderingNormalizedStore) WriteFundamentals(_ string, observations []model.FundamentalObservation) (string, int, error) {
	if err := s.beforeWrite("fundamentals"); err != nil {
		return "", 0, err
	}
	for _, observation := range observations {
		if err := s.inspect(observation.Source, observation.RawPayloadHash, observation.Provenance.RawRecordLocator, observation.Provenance, observation.Temporal); err != nil {
			return "", 0, err
		}
	}
	s.fundamentals = append(s.fundamentals, observations...)
	return "test/data.parquet", len(observations), nil
}

func (s *orderingNormalizedStore) WriteEconomics(_ string, observations []model.EconomicObservation) (string, int, error) {
	if err := s.beforeWrite("economics"); err != nil {
		return "", 0, err
	}
	for _, observation := range observations {
		if err := s.inspect(observation.Source, observation.RawPayloadHash, observation.Provenance.RawRecordLocator, observation.Provenance, observation.Temporal); err != nil {
			return "", 0, err
		}
	}
	s.economics = append(s.economics, observations...)
	rows := len(observations)
	if s.zeroRows {
		rows = 0
	}
	return "test/data.parquet", rows, nil
}

func (s *orderingNormalizedStore) WriteFilings(_ string, observations []model.Filing) (string, int, error) {
	if err := s.beforeWrite("filings"); err != nil {
		return "", 0, err
	}
	expectedHash := s.expectedHash
	if s.expectedFilingHash != "" {
		expectedHash = s.expectedFilingHash
	}
	locatorPrefix := s.locatorPrefix
	if s.filingLocatorPrefix != "" {
		locatorPrefix = s.filingLocatorPrefix
	}
	normalizer := model.NormalizerVersion
	if s.expectedFilingNormalizer != "" {
		normalizer = s.expectedFilingNormalizer
	}
	for _, observation := range observations {
		if err := s.inspectExpected(observation.Source, observation.RawPayloadHash, observation.Provenance.RawRecordLocator, observation.Provenance, observation.Temporal, expectedHash, locatorPrefix, normalizer); err != nil {
			return "", 0, err
		}
	}
	s.filings = append(s.filings, observations...)
	rows := len(observations)
	if s.zeroRows {
		rows = 0
	}
	return "test/data.parquet", rows, nil
}

func (s *orderingNormalizedStore) WriteFX(base, quote string, observations []model.FXObservation) (string, int, error) {
	if err := s.beforeWrite("fx"); err != nil {
		return "", 0, err
	}
	if base != "USD" || quote != "BRL" {
		return "", 0, fmt.Errorf("FX pair = %s-%s", base, quote)
	}
	for _, observation := range observations {
		if observation.Source != s.expectedSource || observation.RawPayloadHash != s.expectedHash || observation.Provenance.RawPayloadHash != s.expectedHash {
			return "", 0, fmt.Errorf("FX source or raw hash mismatch: %+v", observation)
		}
		if observation.Provenance.DataSourceID != s.expectedRun.DataSourceID || observation.Provenance.IngestionRunID != s.expectedRun.ID {
			return "", 0, fmt.Errorf("FX run provenance mismatch: %+v", observation.Provenance)
		}
		if !strings.HasPrefix(observation.Provenance.RawRecordLocator, s.locatorPrefix) || !observation.Provenance.IngestedAt.Equal(observation.RecordedAt) {
			return "", 0, fmt.Errorf("FX locator or receipt mismatch: %+v", observation)
		}
	}
	s.fx = append(s.fx, observations...)
	rows := len(observations)
	if s.zeroRows {
		rows = 0
	}
	return "test/data.parquet", rows, nil
}

type collectorMetadataFake struct {
	run           metadata.Run
	onFinalize    func(metadata.Metrics, []model.PriceBar, []model.EconomicObservation)
	onStart       func(time.Time)
	onStartInputs func(metadata.RunInputs)
	onFinish      func(time.Time)
	onHistorical  func(metadata.HistoricalTruthBatch)
	identityBase  bool
	finalizeError error
}

type operatorMetadataFake struct {
	run       metadata.Run
	lookedUp  bool
	cancelled bool
	reason    string
}

func (f *operatorMetadataFake) LookupRun(_ context.Context, source, runKey, runID string) (metadata.Run, error) {
	f.lookedUp = true
	if runID == "" && (source != f.run.Source || runKey != f.run.RunKey) {
		return metadata.Run{}, fmt.Errorf("unexpected source/run key %q/%q", source, runKey)
	}
	if runID != "" && runID != f.run.ID {
		return metadata.Run{}, fmt.Errorf("unexpected run ID %q", runID)
	}
	return f.run, nil
}

func (f *operatorMetadataFake) CancelRun(_ context.Context, run metadata.Run, _ time.Time, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return errors.New("operator cancellation reason is required")
	}
	if run.Status != "running" && run.Status != "queued" {
		return fmt.Errorf("run %s is already terminal with status %s", run.ID, run.Status)
	}
	f.cancelled = true
	f.reason = "operator cancellation: " + strings.TrimSpace(reason)
	f.run.Status = "cancelled"
	return nil
}

func TestCancelOrphanRunRecordsCancellationReason(t *testing.T) {
	store := &operatorMetadataFake{run: metadata.Run{ID: testRunID, DataSourceID: testDataSource, Source: "yahoo", RunKey: "orphaned-batch/prices", Status: "running"}}
	options := cancellationOptions{enabled: true, source: "yahoo", runKey: "orphaned-batch/prices", reason: "collector process disappeared"}

	if _, err := cancelOrphanRun(context.Background(), store, options, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if !store.lookedUp || !store.cancelled {
		t.Fatalf("cancellation store state = %+v, want lookup and cancellation", store)
	}
	if store.run.Status != "cancelled" || store.reason != "operator cancellation: collector process disappeared" {
		t.Fatalf("cancelled run = status %q reason %q", store.run.Status, store.reason)
	}
}

func TestCancelOrphanRunAcceptsRunIDIdentity(t *testing.T) {
	store := &operatorMetadataFake{run: metadata.Run{ID: testRunID, DataSourceID: testDataSource, Source: "sec", RunKey: "orphaned-batch/sec", Status: "running"}}
	options := cancellationOptions{enabled: true, runID: testRunID, reason: "worker exited"}

	if _, err := cancelOrphanRun(context.Background(), store, options, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if !store.cancelled {
		t.Fatal("run ID cancellation did not complete")
	}
}

func TestCancelOrphanRunCannotCancelTerminalRun(t *testing.T) {
	store := &operatorMetadataFake{run: metadata.Run{ID: testRunID, DataSourceID: testDataSource, Source: "yahoo", RunKey: "finished-batch/prices", Status: "succeeded"}}
	options := cancellationOptions{enabled: true, source: "yahoo", runKey: "finished-batch/prices", reason: "operator cleanup"}

	if _, err := cancelOrphanRun(context.Background(), store, options, time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "already terminal") {
		t.Fatalf("cancelOrphanRun error = %v, want terminal-state protection", err)
	}
	if store.cancelled {
		t.Fatal("terminal run was marked cancelled")
	}
}

func TestCancellationRejectsBlankReason(t *testing.T) {
	options := cancellationOptions{enabled: true, source: "yahoo", runKey: "orphaned-batch/prices", reason: " \t"}
	if err := validateCancellationOptions(options); err == nil || !strings.Contains(err.Error(), "cancel-reason") {
		t.Fatalf("validateCancellationOptions error = %v, want blank-reason rejection", err)
	}
}

func TestNormalCollectionFlagsRemainUnchanged(t *testing.T) {
	if err := validateCancellationOptions(cancellationOptions{}); err != nil {
		t.Fatalf("normal collection options rejected: %v", err)
	}
}

func TestCollectorRunInputBuildersCaptureEffectiveProviderRequests(t *testing.T) {
	universe := []config.Security{{
		IssuerID: "issuer-1", SecurityID: "security-1", CIK: 320193,
		YahooSymbol: "AAPL", Currency: "USD",
	}, {
		IssuerID: "issuer-2", SecurityID: "security-2", CIK: 0,
		YahooSymbol: "PETZ3.SA", Currency: "BRL",
	}}
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)

	secInputs := secRunInputs(universe)
	if secInputs.Source != "sec" || secInputs.Provider.ConfiguredUniverseCount != 1 || len(secInputs.Provider.IssuerRequests) != 1 {
		t.Fatalf("SEC run inputs = %+v", secInputs)
	}
	if got := secInputs.Provider.IssuerRequests[0]; got.IssuerID != "issuer-1" || got.SecurityID != "security-1" || got.CIK != 320193 || !reflect.DeepEqual(got.Resources, []string{"submissions", "companyfacts"}) {
		t.Fatalf("SEC issuer request = %+v", got)
	}
	if got := secEligibleUniverse(universe); len(got) != 1 || got[0].IssuerID != "issuer-1" {
		t.Fatalf("SEC eligible universe = %+v", got)
	}

	priceInputs := pricesRunInputs(universe, start, end)
	if got := priceInputs.Provider.SecurityRequests[0]; got.SecurityID != "security-1" || got.VendorSymbol != "AAPL" || got.Currency != "USD" || got.Start != "2024-01-01" || got.End != "2024-12-31" || got.Interval != "1d" || got.Events != "history" {
		t.Fatalf("Yahoo security request = %+v", got)
	}

	fredInputs := fredRunInputs([]string{" DGS10 ", "CPIAUCSL"})
	if fredInputs.Provider.ConfiguredSeriesCount != 2 || !reflect.DeepEqual(fredInputs.Provider.SeriesIDs, []string{"DGS10", "CPIAUCSL"}) || fredInputs.Provider.Vintage != "current" {
		t.Fatalf("FRED run inputs = %+v", fredInputs)
	}

	alfredInputs := alfredRunInputs([]config.ALFREDSeries{{
		ID: " CPIAUCSL ", Geography: " US ", Unit: " index ", Frequency: " monthly ",
		SeasonalAdjustment: " seasonally_adjusted ", RealtimeEnd: " 2026-08-11 ",
		ObservationStart: " 2018-01-01 ", ObservationEnd: " 2026-07-01 ",
	}})
	if alfredInputs.Provider.ConfiguredSeriesCount != 1 || alfredInputs.Provider.PageSize != 100000 || alfredInputs.Provider.OutputType != 1 || alfredInputs.Provider.Vintage != "historical_realtime_periods" {
		t.Fatalf("ALFRED run inputs = %+v", alfredInputs)
	}
	if got := alfredInputs.Provider.HistoricalSeries[0]; got.ID != "CPIAUCSL" || got.RealtimeStart != "1776-07-04" || got.RealtimeEnd != "2026-08-11" || got.ObservationStart != "2018-01-01" || got.ObservationEnd != "2026-07-01" {
		t.Fatalf("ALFRED series input = %+v", got)
	}
	encoded, err := json.Marshal(alfredInputs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "api_key") || strings.Contains(string(encoded), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatalf("ALFRED run inputs exposed credentials: %s", encoded)
	}

	bcbInputs := bcbRunInputs([]config.BCBSeries{{
		Code: " 432 ", Geography: " BR ", Unit: " percent ", Frequency: " daily ",
		SeasonalAdjustment: " not_adjusted ", Start: " 2024-01-01 ", End: " 2024-12-31 ",
	}})
	if got := bcbInputs.Provider.Series[0]; got.Code != "432" || got.Geography != "BR" || got.Unit != "percent" || got.Frequency != "daily" || got.SeasonalAdjustment != "not_adjusted" || got.Start != "2024-01-01" || got.End != "2024-12-31" {
		t.Fatalf("BCB series input = %+v", got)
	}
	ptaxInputs := ptaxRunInputs(config.PTAXProvider{Enabled: true, Start: " 2026-08-10 ", End: " 2026-08-14 "})
	if ptaxInputs.Source != "bcb_ptax" || ptaxInputs.Provider.Kind != "fx" || ptaxInputs.Provider.FXRequest == nil {
		t.Fatalf("PTAX run inputs = %+v", ptaxInputs)
	}
	if got := ptaxInputs.Provider.FXRequest; got.BaseCurrency != "USD" || got.QuoteCurrency != "BRL" || got.RateKind != "ptax_closing" || got.FixingTimezone != "America/Sao_Paulo" || got.Start != "2026-08-10" || got.End != "2026-08-14" {
		t.Fatalf("PTAX request input = %+v", got)
	}

	b3Inputs := b3RunInputs(config.B3Provider{ReportDate: "2026-08-21", Tickers: []string{"VALE3", "PETR4"}}, []config.Security{
		{SecurityID: "security-vale", Ticker: "VALE3", ISIN: "BRVALEACNOR0"},
		{SecurityID: "security-petr", Ticker: "PETR4", ISIN: "BRPETRACNPR6"},
	})
	if b3Inputs.Source != "b3" || b3Inputs.Provider.Kind != "security_master" || b3Inputs.Provider.B3ReportDate != "2026-08-21" || len(b3Inputs.Provider.B3Instruments) != 2 {
		t.Fatalf("B3 run inputs = %+v", b3Inputs)
	}
	if got := b3Inputs.Provider.B3Instruments[0]; got.Ticker != "PETR4" || got.ISIN != "BRPETRACNPR6" {
		t.Fatalf("B3 inputs were not sorted/captured exactly: %+v", b3Inputs.Provider.B3Instruments)
	}
	b3PriceProvider := config.B3HistoricalPriceProvider{Year: 2021, Start: "2021-09-06", End: "2021-09-10", Tickers: []string{"PETZ3"}}
	b3PriceSecurities := b3HistoricalPriceSecurities(b3PriceProvider, []config.Security{{SecurityID: "security-petz", Ticker: "PETZ3", ISIN: "BRPETZACNOR2", Currency: "BRL"}})
	b3PriceInputs := b3HistoricalPricesRunInputs(b3PriceProvider, b3PriceSecurities)
	if b3PriceInputs.Source != "b3_cotahist" || b3PriceInputs.Provider.Kind != "market_data" || b3PriceInputs.Provider.B3HistoricalQuoteYear != 2021 || b3PriceInputs.Provider.Vintage != "installation_receipt" || len(b3PriceInputs.Provider.SecurityRequests) != 1 {
		t.Fatalf("B3 historical price inputs = %+v", b3PriceInputs)
	}
	if got := b3PriceInputs.Provider.SecurityRequests[0]; got.SecurityID != "security-petz" || got.VendorSymbol != "PETZ3" || got.Currency != "BRL" || got.Start != "2021-09-06" || got.End != "2021-09-10" || got.Events != "closed_annual_archive" {
		t.Fatalf("B3 historical price request = %+v", got)
	}
	calendarInputs := calendarRunInputs("nyse", "XNYS", config.CalendarProvider{Enabled: true, Year: 2026, CoverageStart: "2026-01-01", CoverageEnd: "2026-12-31"})
	if calendarInputs.Source != "nyse" || calendarInputs.Provider.Kind != "market_calendar" || calendarInputs.Provider.CalendarYear != 2026 || calendarInputs.Provider.CalendarMIC != "XNYS" || calendarInputs.Provider.CalendarCoverageStart != "2026-01-01" || calendarInputs.Provider.Vintage != "current_reference_receipt_time" {
		t.Fatalf("calendar run inputs = %+v", calendarInputs)
	}
	nasdaqCalendarInputs := calendarRunInputs("nasdaq_calendar", "XNAS", config.CalendarProvider{Enabled: true, Year: 2026, CoverageStart: "2026-01-01", CoverageEnd: "2026-12-31"})
	if nasdaqCalendarInputs.Source != "nasdaq_calendar" || nasdaqCalendarInputs.Provider.Kind != "market_calendar" || nasdaqCalendarInputs.Provider.CalendarYear != 2026 || nasdaqCalendarInputs.Provider.CalendarMIC != "XNAS" || nasdaqCalendarInputs.Provider.CalendarCoverageStart != "2026-01-01" || nasdaqCalendarInputs.Provider.Vintage != "current_reference_receipt_time" {
		t.Fatalf("Nasdaq calendar run inputs = %+v", nasdaqCalendarInputs)
	}
	historicalCalendarInputs := historicalCalendarRunInputs("nasdaq_calendar", "XNAS", config.HistoricalCalendarProvider{
		Year: 2025, CoverageStart: "2025-12-24", CoverageEnd: "2025-12-25",
		RegularOpenLocal: "09:30", RegularCloseLocal: "16:00",
		Versions: []config.HistoricalCalendarVersion{{
			AvailableAt: "2024-12-13T05:00:00Z", Revision: 0,
			Resources: []config.HistoricalCalendarResource{{Kind: "annual_calendar", URL: "https://www.nasdaqtrader.com/content/technicalsupport/2025tradingcalendar.pdf", SHA256: strings.Repeat("a", 64), ContentType: "application/pdf"}},
			Events:    []config.HistoricalCalendarEvent{{Date: "2025-12-24", Status: "open", CloseLocal: "13:00", ResourceKind: "annual_calendar", SourceLocator: "calendar/date=2025-12-24"}},
		}},
	})
	if historicalCalendarInputs.Source != "nasdaq_calendar" || historicalCalendarInputs.Provider.Vintage != "historical_source_publication" || historicalCalendarInputs.Provider.CalendarRegularOpen != "09:30" || len(historicalCalendarInputs.Provider.CalendarArtifactVersions) != 1 {
		t.Fatalf("historical calendar run inputs = %+v", historicalCalendarInputs)
	}
	if got := historicalCalendarInputs.Provider.CalendarArtifactVersions[0]; got.AvailableAt != "2024-12-13T05:00:00Z" || got.Resources[0].SHA256 != strings.Repeat("a", 64) || got.Events[0].SourceLocator != "calendar/date=2025-12-24" {
		t.Fatalf("historical calendar version inputs = %+v", got)
	}
	membershipInputs := membershipRunInputs("nasdaq", config.IndexMembershipProvider{
		UniverseID: "nasdaq_100", Tickers: []string{"INSM"},
		Notices: []string{"https://www.globenewswire.com/news-release/2026/remove", "https://www.globenewswire.com/news-release/2025/add"},
	}, []config.Security{{SecurityID: "security-insm", Ticker: "INSM", MIC: "XNAS"}})
	if membershipInputs.Source != "nasdaq" || membershipInputs.Provider.Kind != "universe_membership" || membershipInputs.Provider.MembershipUniverseID != "nasdaq_100" || membershipInputs.Provider.Vintage != "historical_source_publication" {
		t.Fatalf("membership run inputs = %+v", membershipInputs)
	}
	if got := membershipInputs.Provider.MembershipNotices; !reflect.DeepEqual(got, []string{"https://www.globenewswire.com/news-release/2025/add", "https://www.globenewswire.com/news-release/2026/remove"}) {
		t.Fatalf("membership notices were not sorted: %+v", got)
	}
	if got := membershipInputs.Provider.MembershipSecurities[0]; got.SecurityID != "security-insm" || got.Ticker != "INSM" || got.MIC != "XNAS" {
		t.Fatalf("membership security input = %+v", got)
	}
	listingHistoryInputs := listingHistoryRunInputs(config.ListingHistoryProvider{
		Notices: []config.ListingHistoryNotice{{
			URL:         "https://sistemasweb.b3.com.br/PlantaoNoticias/Noticias/Detail?agencia=18&dataNoticia=2026-01-02+19%3A43%3A10&idNoticia=3192104",
			TradingName: "PETZ", Ticker: "PETZ3", ValidFrom: "2021-09-06T03:00:00Z",
		}},
	}, []config.Security{{SecurityID: "security-petz", Ticker: "PETZ3"}})
	if listingHistoryInputs.Source != "b3" || listingHistoryInputs.Provider.Kind != "security_listing_lifecycle" || listingHistoryInputs.Provider.Vintage != "historical_source_publication" || len(listingHistoryInputs.Provider.ListingHistoryNotices) != 1 {
		t.Fatalf("listing-history run inputs = %+v", listingHistoryInputs)
	}
	if got := listingHistoryInputs.Provider.ListingHistoryNotices[0]; got.SecurityID != "security-petz" || got.Ticker != "PETZ3" || got.TradingName != "PETZ" || got.ValidFrom != "2021-09-06T03:00:00Z" {
		t.Fatalf("listing-history notice input = %+v", got)
	}
	actionInputs := corporateActionRunInputs("sec", config.CorporateActionProvider{
		AvailabilityPolicy: "source_publication",
		Resources: []config.CorporateActionResource{{
			Kind: "filing", URL: "https://www.sec.gov/Archives/edgar/data/320193/action.html",
			SHA256: testRawHash, ContentType: "text/html; charset=utf-8",
		}},
		Actions: []config.CorporateActionVersion{{
			SecurityID: testSecurityID, SourceEventID: "filing/split", ActionStatus: "active",
			ActionType: "split", ObservedAt: "2020-08-31T00:00:00Z", ObservedPrecision: "date",
			PublishedAt: "2020-07-30T22:55:04Z", PublishedPrecision: "second",
			AvailableAt: "2020-07-30T22:55:04Z", EffectiveAt: "2020-08-28T00:00:00Z",
			EffectivePrecision: "date", RatioNumerator: "4", RatioDenominator: "1",
			ResourceKind: "filing", SourceLocator: "exhibit/split",
		}},
	})
	if actionInputs.Provider.CorporateActionPolicy != "source_publication" || len(actionInputs.Provider.CorporateActionResources) != 1 || len(actionInputs.Provider.CorporateActionVersions) != 1 {
		t.Fatalf("corporate-action run inputs = %+v", actionInputs)
	}

	for _, inputs := range []metadata.RunInputs{secInputs, priceInputs, fredInputs, alfredInputs, bcbInputs, ptaxInputs, b3Inputs, b3PriceInputs, calendarInputs, nasdaqCalendarInputs, historicalCalendarInputs, membershipInputs, listingHistoryInputs, actionInputs} {
		got, err := metadata.NewRunMetadata(inputs)
		if err != nil {
			t.Fatalf("NewRunMetadata(%s): %v", inputs.Source, err)
		}
		if got.RunInputs.CanonicalJSONSHA256 == "" {
			t.Fatalf("%s run input hash is empty", inputs.Source)
		}
	}
}

func TestCorporateActionHistoricalTruthBatchAppliesAvailabilityPolicy(t *testing.T) {
	fetchedAt := time.Date(2026, 8, 24, 4, 0, 0, 0, time.UTC)
	body := []byte("<html>exact action</html>")
	resource := providers.NewRawResource("filing", "action", body, fetchedAt, "text/html; charset=utf-8")
	resource.URL = "https://www.sec.gov/Archives/edgar/data/320193/action.html"
	provider := config.CorporateActionProvider{
		AvailabilityPolicy: "source_publication",
		Actions: []config.CorporateActionVersion{{
			SecurityID: testSecurityID, SourceEventID: "filing/split", Revision: 0,
			ActionStatus: "active", ActionType: "split",
			ObservedAt: "2020-08-31T00:00:00Z", ObservedPrecision: "date",
			PublishedAt: "2020-07-30T22:55:04Z", PublishedPrecision: "second",
			AvailableAt: "2020-07-30T22:55:04Z", EffectiveAt: "2020-08-28T00:00:00Z",
			EffectivePrecision: "date", RatioNumerator: "4", RatioDenominator: "1",
			ResourceKind: "filing", SourceLocator: "exhibit/split",
		}},
	}
	evidenceHash := strings.Repeat("e", 64)
	evidenceReference := "sec/corporate-action-evidence/policy=source_publication/sha256=" + evidenceHash
	batch, err := corporateActionHistoricalTruthBatch(
		testRun(), provider, []providers.RawResource{resource},
		evidenceHash, evidenceReference, fetchedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Actions) != 1 {
		t.Fatalf("actions = %+v", batch.Actions)
	}
	action := batch.Actions[0]
	if action.AvailableAt.Format(time.RFC3339) != "2020-07-30T22:55:04Z" || !action.Provenance.IngestedAt.Equal(fetchedAt) || action.Provenance.RawPayloadHash != evidenceHash || action.SourceReference != evidenceReference {
		t.Fatalf("SEC action availability/provenance = %+v", action)
	}

	provider.AvailabilityPolicy = "installation_receipt"
	provider.Actions[0].AvailableAt = ""
	batch, err = corporateActionHistoricalTruthBatch(
		testRun(), provider, []providers.RawResource{resource},
		evidenceHash, evidenceReference, fetchedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !batch.Actions[0].AvailableAt.Equal(fetchedAt) {
		t.Fatalf("installation availability = %s, want %s", batch.Actions[0].AvailableAt, fetchedAt)
	}
}

func TestMembershipHistoricalTruthBatchPublishesSourceChronology(t *testing.T) {
	addAvailable := time.Date(2025, 12, 13, 1, 0, 0, 0, time.UTC)
	removeAvailable := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
	evidence := []membershipEvidenceEvent{
		{Ticker: "INSM", Member: false, EffectiveAt: time.Date(2026, 6, 22, 13, 30, 0, 0, time.UTC), AnnouncedAt: removeAvailable, AvailableAt: removeAvailable, RecordedAt: removeAvailable, RawRecordLocator: "nasdaq-100/removal/ticker=INSM", RawPayloadHash: strings.Repeat("b", 64)},
		{Ticker: "INSM", Member: true, EffectiveAt: time.Date(2025, 12, 22, 14, 30, 0, 0, time.UTC), AnnouncedAt: addAvailable, AvailableAt: addAvailable, RecordedAt: addAvailable, RawRecordLocator: "nasdaq-100/addition/ticker=INSM", RawPayloadHash: strings.Repeat("a", 64)},
	}
	provider := config.IndexMembershipProvider{UniverseID: "nasdaq_100", Tickers: []string{"INSM"}}
	universe := []config.Security{{
		IssuerID: testIssuerID, SecurityID: testSecurityID, Ticker: "INSM",
		Exchange: "NASDAQ", MIC: "XNAS", Currency: "USD", PrimaryListing: true,
	}}
	batch, ignored, err := membershipHistoricalTruthBatch(testRun(), "nasdaq", provider, universe, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if ignored != 0 || len(batch.Identifiers) != 1 || len(batch.Listings) != 1 || len(batch.Memberships) != 2 {
		t.Fatalf("ignored/batch = %d/%+v", ignored, batch)
	}
	if batch.Identifiers[0].Value != "INSM" || batch.Identifiers[0].IdentifierScope != "XNAS" || !batch.Identifiers[0].ValidFrom.Equal(evidence[1].EffectiveAt) || batch.Identifiers[0].ValidUntil != nil {
		t.Fatalf("identifier assertion = %+v", batch.Identifiers[0])
	}
	if batch.Listings[0].IssuerID == nil || *batch.Listings[0].IssuerID != testIssuerID || batch.Listings[0].Exchange != "NASDAQ" || batch.Listings[0].Currency != "USD" || !batch.Listings[0].PrimaryListing {
		t.Fatalf("listing assertion = %+v", batch.Listings[0])
	}
	if !batch.Memberships[0].Member || batch.Memberships[0].Revision != 0 || batch.Memberships[1].Member || batch.Memberships[1].Revision != 1 {
		t.Fatalf("membership revisions = %+v", batch.Memberships)
	}
	if !batch.Memberships[0].AvailableAt.Equal(addAvailable) || !batch.Memberships[1].AvailableAt.Equal(removeAvailable) {
		t.Fatalf("membership availability = %+v", batch.Memberships)
	}
	bad := append([]membershipEvidenceEvent(nil), evidence[:1]...)
	if _, _, err := membershipHistoricalTruthBatch(testRun(), "nasdaq", provider, universe, bad); err == nil || !strings.Contains(err.Error(), "begins with a removal") {
		t.Fatalf("removal-only chronology accepted: %v", err)
	}
}

func TestB3HistoricalTruthBatchUsesExactISINMappingAndNoMembershipClaim(t *testing.T) {
	availableAt := time.Date(2026, 8, 22, 12, 34, 56, 123456000, time.UTC)
	instrument := b3.Instrument{
		ReportDate:       time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC),
		Ticker:           "PETR4",
		TradingStartDate: time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC),
		ISIN:             "BRPETRACNPR6",
		TradingCurrency:  "BRL",
		CompanyName:      "PETROLEO BRASILEIRO S.A. PETROBRAS",
		DistributionID:   "229",
		RawRecordLocator: "instruments/report_date=2026-08-21/row=4/ticker=PETR4/isin=BRPETRACNPR6",
	}
	security := config.Security{
		IssuerID:       testIssuerID,
		SecurityID:     testSecurityID,
		Ticker:         "PETR4",
		ISIN:           "BRPETRACNPR6",
		Exchange:       "B3",
		MIC:            "BVMF",
		Currency:       "BRL",
		PrimaryListing: true,
	}
	batch, err := b3HistoricalTruthBatch(testRun(), []b3.Instrument{instrument}, map[string]config.Security{"PETR4": security}, testRawHash, availableAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Identifiers) != 1 || len(batch.Listings) != 1 || len(batch.Memberships) != 0 {
		t.Fatalf("B3 historical batch = %+v", batch)
	}
	wantRevision, err := b3ReportRevision(instrument.ReportDate)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Identifiers[0].IdentifierScope != "BVMF" || batch.Identifiers[0].Revision != wantRevision || batch.Identifiers[0].RawPayloadHash != testRawHash {
		t.Fatalf("B3 identifier = %+v", batch.Identifiers[0])
	}
	if batch.Listings[0].MIC != "BVMF" || batch.Listings[0].Currency != "BRL" || batch.Listings[0].IssuerID == nil || *batch.Listings[0].IssuerID != testIssuerID {
		t.Fatalf("B3 listing = %+v", batch.Listings[0])
	}

	security.ISIN = "BRPETRACNOR9"
	if _, err := b3HistoricalTruthBatch(testRun(), []b3.Instrument{instrument}, map[string]config.Security{"PETR4": security}, testRawHash, availableAt); err == nil || !strings.Contains(err.Error(), "does not match configured ISIN") {
		t.Fatalf("mismatched configured ISIN accepted: %v", err)
	}
}

func TestCollectorPassesRunInputsToStartRun(t *testing.T) {
	payload := []byte("observation_date,DGS10\n2024-01-02,4.25\n")
	raw := &orderingRawStore{}
	run := testRun()
	var startedInputs metadata.RunInputs
	app := &app{
		cfg:        config.Config{Providers: config.Providers{FRED: config.FREDProvider{Enabled: true, Series: []string{"DGS10"}}}},
		raw:        raw,
		normalized: &orderingNormalizedStore{raw: raw, expectedRun: run, expectedHash: hashPayload(payload), expectedSource: "fred", locatorPrefix: "csv/date="},
		http:       collectorHTTPFake{responses: map[string][]byte{"fredgraph.csv": payload}},
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{
			run:           run,
			onStartInputs: func(inputs metadata.RunInputs) { startedInputs = inputs },
		},
		batchKey: "run-inputs-test",
	}

	if err := app.run(context.Background(), "fred"); err != nil {
		t.Fatal(err)
	}
	if startedInputs.Source != "fred" || startedInputs.Provider.Name != "fred" || !reflect.DeepEqual(startedInputs.Provider.SeriesIDs, []string{"DGS10"}) {
		t.Fatalf("StartRun inputs = %+v", startedInputs)
	}
}

func TestCancelOrphanRunDoesNotUseProviderOrCollectionStores(t *testing.T) {
	store := &operatorMetadataFake{run: metadata.Run{ID: testRunID, DataSourceID: testDataSource, Source: "fred", RunKey: "orphaned-batch/fred", Status: "queued"}}
	options := cancellationOptions{enabled: true, source: "fred", runKey: "orphaned-batch/fred", reason: "worker stopped before fetch"}

	if _, err := cancelOrphanRun(context.Background(), store, options, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if !store.cancelled {
		t.Fatal("cancellation did not complete")
	}
}

func (f collectorMetadataFake) EnrichSECIssuer(context.Context, model.Issuer, string) error {
	return nil
}

func (f collectorMetadataFake) StartRun(_ context.Context, source, runKey string, startedAt time.Time, inputs metadata.RunInputs) (metadata.Run, error) {
	if f.onStart != nil {
		f.onStart(startedAt)
	}
	if f.onStartInputs != nil {
		f.onStartInputs(inputs)
	}
	run := f.run
	run.Source = source
	run.RunKey = runKey
	run.StartedAt = startedAt
	return run, nil
}

func (f collectorMetadataFake) FinalizeRun(_ context.Context, _ metadata.Run, finished time.Time, m metadata.Metrics, prices []model.PriceBar, macros []model.EconomicObservation) error {
	if f.onFinish != nil {
		f.onFinish(finished)
	}
	if f.onFinalize != nil {
		f.onFinalize(m, prices, macros)
	}
	return f.finalizeError
}

func (f collectorMetadataFake) PublishHistoricalTruth(_ context.Context, batch metadata.HistoricalTruthBatch) error {
	if f.onHistorical != nil {
		f.onHistorical(batch)
	}
	return nil
}

func (f collectorMetadataFake) HistoricalIdentityBaseExists(context.Context, string, string, string, string, time.Time) (bool, error) {
	return f.identityBase, nil
}

func assertMicrosecondUTC(t *testing.T, name string, got time.Time) {
	t.Helper()
	if got.Location() != time.UTC || got.Nanosecond()%int(time.Microsecond) != 0 {
		t.Fatalf("%s = %s, want UTC microsecond precision", name, got)
	}
}

func TestCollectorUsesMicrosecondAlignedRunTimes(t *testing.T) {
	payload := []byte("observation_date,DGS10\n2024-01-02,4.25\n")
	receivedAt := time.Date(2026, 8, 12, 12, 0, 0, 987654321, time.FixedZone("BRT", -3*60*60))
	want := receivedAt.UTC().Truncate(time.Microsecond)
	run := testRun()
	raw := &orderingRawStore{}
	var startedAt, finishedAt time.Time
	app := &app{
		cfg: config.Config{Providers: config.Providers{FRED: config.FREDProvider{Enabled: true, Series: []string{"DGS10"}}}},
		raw: raw,
		normalized: &orderingNormalizedStore{
			raw: raw, expectedRun: run, expectedHash: hashPayload(payload), expectedSource: "fred", locatorPrefix: "csv/date=",
		},
		http: collectorHTTPFake{responses: map[string][]byte{"fredgraph.csv": payload}},
		log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{
			run:      run,
			onStart:  func(got time.Time) { startedAt = got },
			onFinish: func(got time.Time) { finishedAt = got },
		},
		batchKey: "timestamp-boundary-test",
		now:      func() time.Time { return receivedAt },
	}

	if err := app.run(context.Background(), "fred"); err != nil {
		t.Fatal(err)
	}
	assertMicrosecondUTC(t, "run started_at", startedAt)
	assertMicrosecondUTC(t, "run finished_at", finishedAt)
	if !startedAt.Equal(want) || !finishedAt.Equal(want) {
		t.Fatalf("run times = started %s finished %s, want %s", startedAt, finishedAt, want)
	}
}

func TestCollectorStoresRawBeforeCanonicalPublication(t *testing.T) {
	payload := []byte(`{"chart":{"result":[{"meta":{"currency":"USD","exchangeTimezoneName":"America/New_York"},"timestamp":[1719840600],"indicators":{"quote":[{"open":[10],"high":[12],"low":[9],"close":[11],"volume":[100]}]}}],"error":null}}`)
	raw := &orderingRawStore{}
	run := testRun()
	app := &app{
		cfg: config.Config{
			Universe:  []config.Security{{SecurityID: testSecurityID, YahooSymbol: "AAPL", Currency: "USD"}},
			Providers: config.Providers{Prices: config.PriceProvider{Enabled: true, Start: "2024-01-01", End: "2024-12-31"}},
		},
		raw:        raw,
		normalized: &orderingNormalizedStore{raw: raw, expectedRun: run, expectedHash: hashPayload(payload), expectedSource: "yahoo", locatorPrefix: "chart/date="},
		http:       collectorHTTPFake{payload: payload},
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata:   collectorMetadataFake{run: run},
		batchKey:   "ordering-test",
	}

	if err := app.run(context.Background(), "prices"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(raw.events, []string{"raw:put:complete", "canonical:prices:write"}) {
		t.Fatalf("publication order = %v", raw.events)
	}
	manifest := decodeTestManifest(t, raw.manifestPayload)
	if len(manifest.Entries) != 1 || manifest.Entries[0].LogicalKey == "" || manifest.Entries[0].ObjectKey == "" {
		t.Fatalf("successful raw manifest entries = %+v", manifest.Entries)
	}
}

func TestCollectorB3StoresRawBeforeHistoricalPublication(t *testing.T) {
	body, err := os.ReadFile("../../internal/providers/b3/testdata/instruments-2026-08-21.csv")
	if err != nil {
		t.Fatal(err)
	}
	tokenResponse, err := json.Marshal(map[string]any{
		"token": "fixture-token",
		"file":  map[string]string{"name": "InstrumentsConsolidatedFile_20260821_1", "extension": ".csv"},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := &orderingRawStore{}
	run := testRun()
	var published metadata.HistoricalTruthBatch
	app := &app{
		cfg: config.Config{
			Universe: []config.Security{{
				IssuerID: testIssuerID, SecurityID: testSecurityID, LegalName: "Petrobras",
				CountryCode: "BR", SecurityType: "common_stock", PrimaryListing: true, CIK: 1,
				Ticker: "PETR4", ISIN: "BRPETRACNPR6", IdentifierValidFrom: "2020-01-01",
				Exchange: "B3", MIC: "BVMF", Currency: "BRL",
			}},
			Providers: config.Providers{B3: config.B3Provider{Enabled: true, ReportDate: "2026-08-21", Tickers: []string{"PETR4"}}},
		},
		raw:  raw,
		http: collectorHTTPFake{responses: map[string][]byte{"requestname?": tokenResponse, "download/?token": body}},
		log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: run, onHistorical: func(batch metadata.HistoricalTruthBatch) {
			published = batch
		}},
		batchKey: "b3-ordering-test",
		now:      func() time.Time { return time.Date(2026, 8, 22, 12, 34, 56, 123456789, time.UTC) },
	}

	if err := app.run(context.Background(), "b3"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(raw.events, []string{"raw:put:complete"}) {
		t.Fatalf("B3 publication order = %v", raw.events)
	}
	if len(published.Identifiers) != 1 || len(published.Listings) != 1 || len(published.Memberships) != 0 {
		t.Fatalf("published B3 batch = %+v", published)
	}
	manifest := decodeTestManifest(t, raw.manifestPayload)
	if len(manifest.Entries) != 1 || manifest.Entries[0].Attributes["report_date"] != "2026-08-21" || manifest.Entries[0].Attributes["parser_version"] != "b3-v1" {
		t.Fatalf("B3 raw manifest entry = %+v", manifest.Entries)
	}
}

func TestCollectorB3CalendarPublishesExplicitLateOpenSession(t *testing.T) {
	calendarBody, err := os.ReadFile("../../internal/providers/b3/testdata/market-calendar-2026.html")
	if err != nil {
		t.Fatal(err)
	}
	hoursBody, err := os.ReadFile("../../internal/providers/b3/testdata/trading-hours.html")
	if err != nil {
		t.Fatal(err)
	}
	raw := &orderingRawStore{}
	var published metadata.HistoricalTruthBatch
	app := &app{
		cfg: config.Config{Providers: config.Providers{B3: config.B3Provider{
			Enabled:  true,
			Calendar: config.CalendarProvider{Enabled: true, Year: 2026, CoverageStart: "2026-02-18", CoverageEnd: "2026-02-18"},
		}}},
		raw: raw,
		http: collectorHTTPFake{responses: map[string][]byte{
			"trading-calendar/holidays": calendarBody,
			"trading-hours/equities":    hoursBody,
		}},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: testRun(), onHistorical: func(batch metadata.HistoricalTruthBatch) {
			published = batch
		}},
		batchKey: "b3-calendar-test",
		now:      func() time.Time { return time.Date(2026, 8, 23, 20, 0, 0, 123456789, time.UTC) },
	}
	if err := app.run(context.Background(), "b3-calendar"); err != nil {
		t.Fatal(err)
	}
	if len(raw.events) != 3 {
		t.Fatalf("raw publication events = %v", raw.events)
	}
	if len(published.Calendars) != 1 || len(published.Sessions) != 1 {
		t.Fatalf("published calendar = %+v", published)
	}
	session := published.Sessions[0]
	if session.SessionStatus != "open" || session.OpenAt == nil || session.CloseAt == nil || session.OpenAt.Format(time.RFC3339) != "2026-02-18T16:00:00Z" || session.CloseAt.Format(time.RFC3339) != "2026-02-18T20:00:00Z" {
		t.Fatalf("B3 late-open session = %+v", session)
	}
	if published.Calendars[0].RawPayloadHash != session.RawPayloadHash || !strings.Contains(published.Calendars[0].SourceReference, "calendar-evidence") {
		t.Fatalf("calendar evidence lineage = %+v/%+v", published.Calendars[0], session)
	}
}

func TestCollectorNYSECalendarPublishesHolidayAndEarlyClose(t *testing.T) {
	body, err := os.ReadFile("../../internal/providers/nyse/testdata/hours-calendars-2026.html")
	if err != nil {
		t.Fatal(err)
	}
	raw := &orderingRawStore{}
	var published metadata.HistoricalTruthBatch
	app := &app{
		cfg:  config.Config{Providers: config.Providers{NYSE: config.CalendarProvider{Enabled: true, Year: 2026, CoverageStart: "2026-11-26", CoverageEnd: "2026-11-27"}}},
		raw:  raw,
		http: collectorHTTPFake{payload: body},
		log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: testRun(), onHistorical: func(batch metadata.HistoricalTruthBatch) {
			published = batch
		}},
		batchKey: "nyse-calendar-test",
		now:      func() time.Time { return time.Date(2026, 8, 23, 20, 0, 0, 123456789, time.UTC) },
	}
	if err := app.run(context.Background(), "nyse"); err != nil {
		t.Fatal(err)
	}
	if len(raw.events) != 2 || len(published.Calendars) != 1 || len(published.Sessions) != 2 {
		t.Fatalf("raw/published = %v/%+v", raw.events, published)
	}
	if published.Sessions[0].SessionStatus != "closed" || published.Sessions[1].SessionStatus != "open" || !published.Sessions[1].IsEarlyClose || published.Sessions[1].CloseAt == nil || published.Sessions[1].CloseAt.Format(time.RFC3339) != "2026-11-27T18:00:00Z" {
		t.Fatalf("NYSE sessions = %+v", published.Sessions)
	}
}

func TestCollectorNasdaqCalendarPublishesHolidayAndEarlyClose(t *testing.T) {
	body, err := os.ReadFile("../../internal/providers/nasdaq/testdata/calendar-2026.html")
	if err != nil {
		t.Fatal(err)
	}
	raw := &orderingRawStore{}
	var published metadata.HistoricalTruthBatch
	app := &app{
		cfg:  config.Config{Providers: config.Providers{NasdaqCalendar: config.CalendarProvider{Enabled: true, Year: 2026, CoverageStart: "2026-11-26", CoverageEnd: "2026-11-27"}}},
		raw:  raw,
		http: collectorHTTPFake{payload: body},
		log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: testRun(), onHistorical: func(batch metadata.HistoricalTruthBatch) {
			published = batch
		}},
		batchKey: "nasdaq-calendar-test",
		now:      func() time.Time { return time.Date(2026, 8, 23, 20, 0, 0, 123456789, time.UTC) },
	}
	if err := app.run(context.Background(), "nasdaq-calendar"); err != nil {
		t.Fatal(err)
	}
	if len(raw.events) != 2 || len(published.Calendars) != 1 || len(published.Sessions) != 2 {
		t.Fatalf("raw/published = %v/%+v", raw.events, published)
	}
	if published.Sessions[0].SessionStatus != "closed" || published.Sessions[1].SessionStatus != "open" || !published.Sessions[1].IsEarlyClose || published.Sessions[1].CloseAt == nil || published.Sessions[1].CloseAt.Format(time.RFC3339) != "2026-11-27T18:00:00Z" {
		t.Fatalf("Nasdaq sessions = %+v", published.Sessions)
	}
}

func TestCollectorHistoricalCalendarPublishesFullCorrectionVersionsAtomically(t *testing.T) {
	originalBody := []byte("%PDF-1.7\noriginal B3 calendar")
	correctionBody := []byte("%PDF-1.7\ncorrected B3 calendar")
	originalURL := "https://www.b3.com.br/data/files/AA/original.pdf"
	correctionURL := "https://www.b3.com.br/data/files/BB/correction.pdf"
	events := []config.HistoricalCalendarEvent{
		{Date: "2026-02-16", Status: "closed", ResourceKind: "calendar_circular", SourceLocator: "page=1/date=2026-02-16"},
		{Date: "2026-02-17", Status: "closed", ResourceKind: "calendar_circular", SourceLocator: "page=1/date=2026-02-17"},
		{Date: "2026-02-18", Status: "open", OpenLocal: "13:00", CloseLocal: "18:00", ResourceKind: "calendar_circular", SourceLocator: "page=1/date=2026-02-18"},
	}
	provider := config.HistoricalCalendarProvider{
		Enabled: true, Year: 2026, CoverageStart: "2026-02-16", CoverageEnd: "2026-02-18",
		RegularOpenLocal: "10:00", RegularCloseLocal: "17:00",
		Versions: []config.HistoricalCalendarVersion{
			{
				AvailableAt: "2025-12-05T03:00:00Z", Revision: 0,
				Resources: []config.HistoricalCalendarResource{{Kind: "calendar_circular", URL: originalURL, SHA256: hashPayload(originalBody), ContentType: "application/pdf"}},
				Events:    append([]config.HistoricalCalendarEvent(nil), events...),
			},
			{
				AvailableAt: "2026-01-09T03:00:00Z", Revision: 1,
				Resources: []config.HistoricalCalendarResource{{Kind: "calendar_circular", URL: correctionURL, SHA256: hashPayload(correctionBody), ContentType: "application/pdf"}},
				Events:    append([]config.HistoricalCalendarEvent(nil), events...),
			},
		},
	}
	raw := &orderingRawStore{}
	var published metadata.HistoricalTruthBatch
	var started metadata.RunInputs
	app := &app{
		cfg: config.Config{Providers: config.Providers{B3CalendarHistory: provider}},
		raw: raw,
		http: collectorHTTPFake{responses: map[string][]byte{
			"original.pdf": originalBody, "correction.pdf": correctionBody,
		}},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: testRun(), onStartInputs: func(inputs metadata.RunInputs) {
			started = inputs
		}, onHistorical: func(batch metadata.HistoricalTruthBatch) {
			published = batch
		}},
		batchKey: "b3-calendar-history-test",
		now:      func() time.Time { return time.Date(2026, 8, 24, 3, 0, 0, 123456789, time.UTC) },
	}
	if err := app.run(context.Background(), "b3-calendar-history"); err != nil {
		t.Fatal(err)
	}
	if started.Source != "b3_calendar" || started.Provider.CalendarMIC != "BVMF" || len(started.Provider.CalendarArtifactVersions) != 2 {
		t.Fatalf("run inputs = %+v", started)
	}
	if len(raw.events) != 4 || len(published.Calendars) != 2 || len(published.Sessions) != 6 {
		t.Fatalf("raw/published = %v/%+v", raw.events, published)
	}
	if published.Calendars[0].CalendarVersion == published.Calendars[1].CalendarVersion || published.Calendars[0].RawPayloadHash == published.Calendars[1].RawPayloadHash {
		t.Fatalf("correction versions did not retain distinct evidence: %+v", published.Calendars)
	}
	if published.Calendars[0].SessionFingerprint != published.Calendars[1].SessionFingerprint {
		t.Fatalf("non-equity correction changed the listed-equity session fingerprint: %+v", published.Calendars)
	}
	for versionIndex := range 2 {
		session := published.Sessions[versionIndex*3+2]
		if session.Revision != versionIndex || session.OpenAt == nil || session.CloseAt == nil || session.OpenAt.Format(time.RFC3339) != "2026-02-18T16:00:00Z" || session.CloseAt.Format(time.RFC3339) != "2026-02-18T21:00:00Z" {
			t.Fatalf("corrected late-open session %d = %+v", versionIndex, session)
		}
	}
	manifest := decodeTestManifest(t, raw.manifestPayload)
	if len(manifest.Entries) != 4 || manifest.Entries[0].LogicalKey == manifest.Entries[2].LogicalKey {
		t.Fatalf("historical calendar raw manifest = %+v", manifest.Entries)
	}
}

func TestCollectorHistoricalCalendarRetainsHashMismatchWithoutPublication(t *testing.T) {
	body := []byte("%PDF-1.7\nunexpected")
	provider := config.HistoricalCalendarProvider{
		Enabled: true, Year: 2025, CoverageStart: "2025-12-24", CoverageEnd: "2025-12-25",
		RegularOpenLocal: "09:30", RegularCloseLocal: "16:00",
		Versions: []config.HistoricalCalendarVersion{{
			AvailableAt: "2024-12-13T05:00:00Z", Revision: 0,
			Resources: []config.HistoricalCalendarResource{{Kind: "annual_calendar", URL: "https://www.nasdaqtrader.com/content/technicalsupport/2025tradingcalendar.pdf", SHA256: strings.Repeat("a", 64), ContentType: "application/pdf"}},
		}},
	}
	raw := &orderingRawStore{}
	published := false
	app := &app{
		cfg: config.Config{Providers: config.Providers{NasdaqCalendarHistory: provider}},
		raw: raw, http: collectorHTTPFake{payload: body}, log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: testRun(), onHistorical: func(metadata.HistoricalTruthBatch) { published = true }},
		batchKey: "nasdaq-calendar-mismatch-test",
		now:      func() time.Time { return time.Date(2026, 8, 24, 3, 0, 0, 0, time.UTC) },
	}
	err := app.run(context.Background(), "nasdaq-calendar-history")
	if err == nil || !strings.Contains(err.Error(), "does not match pinned") {
		t.Fatalf("hash mismatch error = %v", err)
	}
	if published || len(raw.events) != 1 {
		t.Fatalf("hash mismatch publication/raw = %t/%v", published, raw.events)
	}
	manifest := decodeTestManifest(t, raw.manifestPayload)
	if len(manifest.Entries) != 1 || manifest.Entries[0].SHA256 != hashPayload(body) {
		t.Fatalf("hash mismatch evidence manifest = %+v", manifest.Entries)
	}
}

func TestCollectorNasdaqMembershipPublishesAddRemoveChronology(t *testing.T) {
	added := []byte(`<html><body><p>December 12, 2025 20:00 ET | Source: Nasdaq, Inc.</p><p>NEW YORK, Dec. 12, 2025 (GLOBE NEWSWIRE) -- effective prior to market open on Monday, December 22, 2025.</p><p>The following two companies will be added to the Index: Insmed Incorporated (Nasdaq: INSM), Western Digital Corp. (Nasdaq: WDC).</p><p>As a result of the reconstitution, the following one companies will be removed from the Index: Biogen Inc. (Nasdaq: BIIB).</p><p>For information about the companies.</p></body></html>`)
	removed := []byte(`<html><body><p>June 11, 2026 20:00 ET | Source: Nasdaq, Inc.</p><p>NEW YORK, June 11, 2026 (GLOBE NEWSWIRE) -- effective prior to market open on Monday, June 22, 2026.</p><p>The following one companies will be added to the Index: Astera Labs, Inc. (Nasdaq: ALAB).</p><p>The following one companies will be removed from the Index: Insmed Incorporated (Nasdaq: INSM).</p><p>For additional information.</p></body></html>`)
	addURL := "https://www.globenewswire.com/news-release/2025/12/13/add.html"
	removeURL := "https://www.globenewswire.com/news-release/2026/06/12/remove.html"
	raw := &orderingRawStore{}
	var published metadata.HistoricalTruthBatch
	var started metadata.RunInputs
	app := &app{
		cfg: config.Config{
			Providers: config.Providers{NasdaqMembership: config.IndexMembershipProvider{
				Enabled: true, UniverseID: "nasdaq_100", Tickers: []string{"INSM"}, Notices: []string{addURL, removeURL},
			}},
			Universe: []config.Security{{
				IssuerID: testIssuerID, SecurityID: testSecurityID, Ticker: "INSM",
				Exchange: "NASDAQ", MIC: "XNAS", Currency: "USD", PrimaryListing: true,
			}},
		},
		raw: raw,
		http: collectorHTTPFake{responses: map[string][]byte{
			"/2025/12/13/": added,
			"/2026/06/12/": removed,
		}},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: testRun(), onStartInputs: func(inputs metadata.RunInputs) {
			started = inputs
		}, onHistorical: func(batch metadata.HistoricalTruthBatch) {
			published = batch
		}},
		batchKey: "nasdaq-membership-test",
		now:      func() time.Time { return time.Date(2026, 8, 24, 1, 2, 3, 123456789, time.UTC) },
	}
	if err := app.run(context.Background(), "nasdaq-membership"); err != nil {
		t.Fatal(err)
	}
	if started.Source != "nasdaq" || started.Provider.MembershipUniverseID != "nasdaq_100" {
		t.Fatalf("run inputs = %+v", started)
	}
	if len(raw.events) != 2 || len(published.Identifiers) != 1 || len(published.Listings) != 1 || len(published.Memberships) != 2 {
		t.Fatalf("raw/published = %v/%+v", raw.events, published)
	}
	if !published.Memberships[0].Member || published.Memberships[1].Member || published.Memberships[0].Revision != 0 || published.Memberships[1].Revision != 1 {
		t.Fatalf("published chronology = %+v", published.Memberships)
	}
	if published.Memberships[0].SecurityID != testSecurityID || published.Memberships[0].RawPayloadHash != hashPayload(added) || published.Memberships[1].RawPayloadHash != hashPayload(removed) {
		t.Fatalf("membership lineage = %+v", published.Memberships)
	}
	manifest := decodeTestManifest(t, raw.manifestPayload)
	if len(manifest.Entries) != 2 || manifest.Entries[0].Attributes["universe_id"] != "nasdaq_100" {
		t.Fatalf("membership raw manifest = %+v", manifest.Entries)
	}
}

func TestCollectorB3ListingHistoryPublishesIntervalCorrections(t *testing.T) {
	body := []byte(`<!doctype html><html><body><h1>PETZ (PETZ-NM) - Fato Relevante - 02/01/26 (N)</h1><p>Alteracao no valor por acao da parcela em dinheiro.</p><p>A partir de 05/01/2026, as acoes da companhia deixam de ser negociadas em razao de sua incorporacao pela Cobasi Investimentos S.A.</p></body></html>`)
	noticeURL := "https://sistemasweb.b3.com.br/PlantaoNoticias/Noticias/Detail?agencia=18&dataNoticia=2026-01-02+19%3A43%3A10&idNoticia=3192104"
	raw := &orderingRawStore{}
	var published metadata.HistoricalTruthBatch
	var started metadata.RunInputs
	app := &app{
		cfg: config.Config{
			Providers: config.Providers{B3ListingHistory: config.ListingHistoryProvider{
				Enabled: true, Notices: []config.ListingHistoryNotice{{
					URL: noticeURL, TradingName: "PETZ", Ticker: "PETZ3", ValidFrom: "2021-09-06T03:00:00Z",
				}},
			}},
			Universe: []config.Security{{
				IssuerID: testIssuerID, SecurityID: testSecurityID, Ticker: "PETZ3",
				Exchange: "B3", MIC: "BVMF", Currency: "BRL", PrimaryListing: true,
			}},
		},
		raw: raw, http: collectorHTTPFake{payload: body},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{
			run: testRun(), identityBase: true,
			onStartInputs: func(inputs metadata.RunInputs) { started = inputs },
			onHistorical:  func(batch metadata.HistoricalTruthBatch) { published = batch },
		},
		batchKey: "b3-listing-history-test",
		now:      func() time.Time { return time.Date(2026, 8, 24, 2, 0, 0, 123456789, time.UTC) },
	}
	if err := app.run(context.Background(), "b3-listing-history"); err != nil {
		t.Fatal(err)
	}
	if started.Source != "b3" || started.Provider.Kind != "security_listing_lifecycle" {
		t.Fatalf("run inputs = %+v", started)
	}
	if len(raw.events) != 1 || len(published.Identifiers) != 1 || len(published.Listings) != 1 {
		t.Fatalf("raw/published = %v/%+v", raw.events, published)
	}
	identifier, listing := published.Identifiers[0], published.Listings[0]
	if identifier.Revision != 1 || identifier.ValidUntil == nil || identifier.ValidUntil.Format(time.RFC3339) != "2026-01-05T03:00:00Z" || identifier.RawPayloadHash != hashPayload(body) {
		t.Fatalf("identifier correction = %+v", identifier)
	}
	if listing.Revision != 1 || listing.ValidUntil == nil || !listing.ValidUntil.Equal(*identifier.ValidUntil) || !listing.RecordedAt.Equal(time.Date(2026, 8, 24, 2, 0, 0, 123456000, time.UTC)) {
		t.Fatalf("listing correction = %+v", listing)
	}
	manifest := decodeTestManifest(t, raw.manifestPayload)
	if len(manifest.Entries) != 1 || manifest.Entries[0].Attributes["ticker"] != "PETZ3" || manifest.Entries[0].Attributes["trading_name"] != "PETZ" {
		t.Fatalf("listing-history manifest = %+v", manifest.Entries)
	}
}

func TestCollectorB3ListingHistoryRejectsMissingBase(t *testing.T) {
	body := []byte(`<!doctype html><html><body><h1>PETZ (PETZ-NM) - Fato Relevante - 02/01/26 (N)</h1><p>A partir de 05/01/2026, as acoes da companhia deixam de ser negociadas em razao de sua incorporacao pela Cobasi Investimentos S.A.</p></body></html>`)
	app := &app{
		cfg: config.Config{
			Providers: config.Providers{B3ListingHistory: config.ListingHistoryProvider{
				Enabled: true, Notices: []config.ListingHistoryNotice{{
					URL:         "https://sistemasweb.b3.com.br/PlantaoNoticias/Noticias/Detail?agencia=18&dataNoticia=2026-01-02+19%3A43%3A10&idNoticia=3192104",
					TradingName: "PETZ", Ticker: "PETZ3", ValidFrom: "2021-09-06T03:00:00Z",
				}},
			}},
			Universe: []config.Security{{IssuerID: testIssuerID, SecurityID: testSecurityID, Ticker: "PETZ3", Exchange: "B3", MIC: "BVMF", Currency: "BRL", PrimaryListing: true}},
		},
		raw: &orderingRawStore{}, http: collectorHTTPFake{payload: body},
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: testRun(), identityBase: false},
		batchKey: "b3-listing-history-missing-base-test",
		now:      func() time.Time { return time.Date(2026, 8, 24, 2, 0, 0, 0, time.UTC) },
	}
	if err := app.run(context.Background(), "b3-listing-history"); err == nil || !strings.Contains(err.Error(), "no exact open-ended identifier/listing base") {
		t.Fatalf("missing base error = %v", err)
	}
}

func TestCollectorSECStoresRawBeforeNormalizedWrite(t *testing.T) {
	secSubmissions := []byte(`{"cik":"0000000001","sic":"3571","name":"Example Corp","stateOfIncorporation":"DE","sicDescription":"Widgets","filings":{"recent":{"accessionNumber":["0001"],"filingDate":["2024-02-02"],"reportDate":["2023-12-31"],"acceptanceDateTime":["2024-02-02T21:03:04.000Z"],"form":["10-K"],"primaryDocument":["example.htm"]}}}`)
	secFacts := []byte(`{"cik":1,"facts":{"us-gaap":{"Revenue":{"label":"Revenue","units":{"USD":[{"start":"2023-01-01","end":"2023-12-31","val":123.5,"accn":"0001","fy":2023,"fp":"FY","form":"10-K","filed":"2024-02-02"}]}}}}}`)
	raw := &orderingRawStore{}
	run := testRun()
	app := &app{
		cfg: config.Config{
			Universe:  []config.Security{{IssuerID: testIssuerID, CIK: 1}},
			Providers: config.Providers{SEC: config.EnabledProvider{Enabled: true}},
		},
		raw: raw,
		normalized: &orderingNormalizedStore{
			raw:                      raw,
			expectedRun:              run,
			expectedHash:             hashPayload(secFacts),
			expectedFilingHash:       hashPayload(secSubmissions),
			expectedFilingNormalizer: "sec-submissions-v1",
			expectedSource:           "sec",
			locatorPrefix:            "companyfacts/",
			filingLocatorPrefix:      "filings/recent/",
		},
		http: collectorHTTPFake{responses: map[string][]byte{
			"submissions/CIK0000000001.json":  secSubmissions,
			"companyfacts/CIK0000000001.json": secFacts,
		}},
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: run},
		batchKey: "sec-test",
	}

	if err := app.run(context.Background(), "sec"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(raw.events, []string{"raw:put:complete", "raw:put:complete", "canonical:fundamentals:write", "canonical:filings:write"}) {
		t.Fatalf("publication order = %v", raw.events)
	}
	if got := len(app.normalized.(*orderingNormalizedStore).fundamentals); got != 1 {
		t.Fatalf("fundamental observations = %d, want 1", got)
	}
	filings := app.normalized.(*orderingNormalizedStore).filings
	if len(filings) != 1 || filings[0].SourceDocumentID != "0001" || filings[0].FormType != "10-K" || filings[0].PeriodEnd == nil {
		t.Fatalf("canonical SEC filings = %+v", filings)
	}
}

func TestCollectorFREDStoresRawBeforeNormalizedWrite(t *testing.T) {
	fredPayload := []byte("observation_date,DGS10\n2024-01-02,4.25\n")
	raw := &orderingRawStore{}
	run := testRun()
	app := &app{
		cfg: config.Config{
			Providers: config.Providers{FRED: config.FREDProvider{Enabled: true, Series: []string{"DGS10"}}},
		},
		raw: raw,
		normalized: &orderingNormalizedStore{
			raw:            raw,
			expectedRun:    run,
			expectedHash:   hashPayload(fredPayload),
			expectedSource: "fred",
			locatorPrefix:  "csv/date=",
		},
		http:     collectorHTTPFake{responses: map[string][]byte{"fredgraph.csv": fredPayload}},
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: run},
		batchKey: "fred-test",
	}

	if err := app.run(context.Background(), "fred"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(raw.events, []string{"raw:put:complete", "canonical:economics:write"}) {
		t.Fatalf("publication order = %v", raw.events)
	}
	if got := len(app.normalized.(*orderingNormalizedStore).economics); got != 1 {
		t.Fatalf("economic observations = %d, want 1", got)
	}
}

func TestCollectorALFREDStoresRawPagesBeforeHistoricalPublication(t *testing.T) {
	payload := []byte(`{"output_type":1,"count":2,"offset":0,"limit":100000,"observations":[{"realtime_start":"2024-02-13","realtime_end":"2024-03-10","date":"2024-01-01","value":"308.417"},{"realtime_start":"2024-03-11","realtime_end":"2026-08-11","date":"2024-01-01","value":"308.491"}]}`)
	raw := &orderingRawStore{}
	run := testRun()
	var finalized metadata.Metrics
	var snapshots []model.EconomicObservation
	app := &app{
		cfg: config.Config{
			FREDAPIKey: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Providers: config.Providers{ALFRED: config.ALFREDProvider{Enabled: true, Series: []config.ALFREDSeries{{
				ID: "CPIAUCSL", Geography: "US", Unit: "index", Frequency: "monthly",
				SeasonalAdjustment: "seasonally_adjusted", RealtimeEnd: "2026-08-11",
				ObservationStart: "2024-01-01", ObservationEnd: "2024-01-01",
			}}}},
		},
		raw: raw,
		normalized: &orderingNormalizedStore{
			raw: raw, expectedRun: run, expectedHash: hashPayload(payload), expectedSource: "alfred", locatorPrefix: "json/offset=",
		},
		http: collectorHTTPFake{payload: payload},
		log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: run, onFinalize: func(m metadata.Metrics, _ []model.PriceBar, macros []model.EconomicObservation) {
			finalized = m
			snapshots = append(snapshots, macros...)
			raw.events = append(raw.events, "metadata:finalize")
		}},
		batchKey: "alfred-ordering-test",
		now:      func() time.Time { return time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC) },
	}

	if err := app.run(context.Background(), "alfred"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(raw.events, []string{"raw:put:complete", "canonical:economics:write", "metadata:finalize"}) {
		t.Fatalf("ALFRED publication order = %v", raw.events)
	}
	if len(raw.rawKeys) != 1 || !strings.HasPrefix(raw.rawKeys[0], "alfred/series/") || !strings.Contains(raw.rawKeys[0], "/CPIAUCSL-offset-0/") {
		t.Fatalf("ALFRED raw object keys = %v", raw.rawKeys)
	}
	attributes := raw.rawMetadata[0].Attributes
	if attributes["series_id"] != "CPIAUCSL" || attributes["realtime_start"] != "1776-07-04" || attributes["realtime_end"] != "2026-08-11" || attributes["output_type"] != "1" || attributes["offset"] != "0" {
		t.Fatalf("ALFRED raw attributes = %+v", attributes)
	}
	for key, value := range attributes {
		if strings.Contains(strings.ToLower(key), "api_key") || strings.Contains(value, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
			t.Fatalf("ALFRED raw metadata exposed credentials: %s=%q", key, value)
		}
	}
	manifest := decodeTestManifest(t, raw.manifestPayload)
	if len(manifest.Entries) != 1 || !strings.Contains(manifest.Entries[0].LogicalKey, "/realtime/1776-07-04/2026-08-11/") {
		t.Fatalf("ALFRED raw manifest entries = %+v", manifest.Entries)
	}
	if len(snapshots) != 2 || snapshots[0].Revision != 0 || snapshots[1].Revision != 1 || snapshots[0].Source != "alfred" {
		t.Fatalf("ALFRED snapshots = %+v", snapshots)
	}
	if finalized.Written != 2 || finalized.RawPayloads != 1 || finalized.Cursor["last_series_id"] != "CPIAUCSL" {
		t.Fatalf("ALFRED final metrics = %+v", finalized)
	}
}

func TestCollectorALFREDRetainsRawPageOnParseFailure(t *testing.T) {
	payload := []byte(`{"output_type":1,"count":1,"offset":0,"limit":100000,"observations":[`)
	raw := &orderingRawStore{}
	run := testRun()
	var finalized metadata.Metrics
	app := &app{
		cfg: config.Config{
			FREDAPIKey: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Providers: config.Providers{ALFRED: config.ALFREDProvider{Enabled: true, Series: []config.ALFREDSeries{{
				ID: "CPIAUCSL", Geography: "US", Unit: "index", Frequency: "monthly", RealtimeEnd: "2026-08-11",
			}}}},
		},
		raw:        raw,
		normalized: &orderingNormalizedStore{raw: raw, expectedRun: run, expectedSource: "alfred", locatorPrefix: "json/offset="},
		http:       collectorHTTPFake{payload: payload},
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: run, onFinalize: func(m metadata.Metrics, _ []model.PriceBar, macros []model.EconomicObservation) {
			finalized = m
			if len(macros) != 0 {
				t.Errorf("parse failure finalized %d macro snapshots", len(macros))
			}
			raw.events = append(raw.events, "metadata:finalize")
		}},
		batchKey: "alfred-parse-error-test",
	}

	if err := app.run(context.Background(), "alfred"); err == nil {
		t.Fatal("expected ALFRED parse error")
	}
	if len(raw.payloads) != 1 || string(raw.payloads[0]) != string(payload) {
		t.Fatalf("persisted ALFRED raw payload = %q", raw.payloads)
	}
	if !reflect.DeepEqual(raw.events, []string{"raw:put:complete", "metadata:finalize"}) || finalized.RawPayloads != 1 || finalized.Written != 0 {
		t.Fatalf("ALFRED parse-error state events=%v metrics=%+v", raw.events, finalized)
	}
}

func TestCollectorBCBStoresRawBeforeCanonicalWrite(t *testing.T) {
	payload := []byte("\"data\";\"valor\"\r\n\"01/01/2024\";\"14,25\"\r\n")
	raw := &orderingRawStore{}
	run := testRun()
	var finalized metadata.Metrics
	var snapshots []model.EconomicObservation
	app := &app{
		cfg: config.Config{Providers: config.Providers{BCB: config.BCBProvider{Enabled: true, Series: []config.BCBSeries{{
			Code: "432", Geography: "BR", Unit: "percent", Frequency: "daily", SeasonalAdjustment: "not_adjusted",
		}}}}},
		raw:        raw,
		normalized: &orderingNormalizedStore{raw: raw, expectedRun: run, expectedHash: hashPayload(payload), expectedSource: "bcb", locatorPrefix: "csv/date="},
		http:       collectorHTTPFake{responses: map[string][]byte{"bcdata.sgs.432": payload}},
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: run, onFinalize: func(m metadata.Metrics, _ []model.PriceBar, macros []model.EconomicObservation) {
			finalized = m
			snapshots = append(snapshots, macros...)
			raw.events = append(raw.events, "metadata:finalize")
		}},
		batchKey: "bcb-ordering-test",
	}

	if err := app.run(context.Background(), "bcb"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(raw.events, []string{"raw:put:complete", "canonical:economics:write", "metadata:finalize"}) {
		t.Fatalf("BCB publication order = %v", raw.events)
	}
	if len(raw.rawKeys) != 1 || !strings.HasPrefix(raw.rawKeys[0], "bcb/series/") || !strings.Contains(raw.rawKeys[0], "/432/") {
		t.Fatalf("BCB raw object keys = %v", raw.rawKeys)
	}
	if len(raw.rawMetadata) != 1 {
		t.Fatalf("BCB raw metadata count = %d, want 1", len(raw.rawMetadata))
	}
	if got := raw.rawMetadata[0].Attributes; got["series_code"] != "432" || got["provider_format"] != "sgs-csv" || got["geography"] != "BR" || got["frequency"] != "daily" {
		t.Fatalf("BCB raw attributes = %+v", got)
	}
	manifest := decodeTestManifest(t, raw.manifestPayload)
	if len(manifest.Entries) != 1 || manifest.Entries[0].Attributes["series_code"] != "432" {
		t.Fatalf("BCB raw manifest entries = %+v", manifest.Entries)
	}
	if len(snapshots) != 1 || snapshots[0].Source != "bcb" || snapshots[0].Temporal.ObservedPrecision != model.PrecisionDate {
		t.Fatalf("BCB snapshots = %+v", snapshots)
	}
	if finalized.Cursor["provider"] != "bcb" || finalized.Cursor["last_series_code"] != "432" || finalized.Cursor["last_accepted_series_code"] != "432" {
		t.Fatalf("BCB cursor = %+v", finalized.Cursor)
	}
	if finalized.Cursor["series_processed"] != 1 || finalized.Cursor["series_accepted"] != 1 {
		t.Fatalf("BCB cursor counts = %+v", finalized.Cursor)
	}
}

func TestCollectorBCBRetainsRawOnParseFailureBeforeFinalization(t *testing.T) {
	payload := []byte("\"wrong\";\"valor\"\r\n\"01/01/2024\";\"14,25\"\r\n")
	raw := &orderingRawStore{}
	run := testRun()
	var finalized metadata.Metrics
	app := &app{
		cfg:        config.Config{Providers: config.Providers{BCB: config.BCBProvider{Enabled: true, Series: []config.BCBSeries{{Code: "432", Geography: "BR", Unit: "percent", Frequency: "daily"}}}}},
		raw:        raw,
		normalized: &orderingNormalizedStore{raw: raw, expectedRun: run, expectedSource: "bcb", locatorPrefix: "csv/date="},
		http:       collectorHTTPFake{payload: payload},
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: run, onFinalize: func(m metadata.Metrics, prices []model.PriceBar, macros []model.EconomicObservation) {
			finalized = m
			raw.events = append(raw.events, "metadata:finalize")
			if len(prices) != 0 || len(macros) != 0 {
				t.Errorf("finalized snapshots = %d prices, %d macros; want none", len(prices), len(macros))
			}
		}},
		batchKey: "bcb-parse-error-test",
	}

	if err := app.run(context.Background(), "bcb"); err == nil {
		t.Fatal("expected BCB schema error")
	}
	if len(raw.payloads) != 1 || string(raw.payloads[0]) != string(payload) {
		t.Fatalf("persisted BCB raw payload = %q, want %q", raw.payloads, payload)
	}
	if !reflect.DeepEqual(raw.events, []string{"raw:put:complete", "metadata:finalize"}) {
		t.Fatalf("BCB parse-error publication order = %v", raw.events)
	}
	if got := len(decodeTestManifest(t, raw.manifestPayload).Entries); got != 1 {
		t.Fatalf("BCB parse-error manifest entries = %d, want 1", got)
	}
	if finalized.Err == nil || finalized.RawPayloads != 1 {
		t.Fatalf("BCB parse-error final metrics = %+v", finalized)
	}
}

func TestCollectorBCBPartialRunFinalizesAcceptedSeries(t *testing.T) {
	goodPayload := []byte("\"data\";\"valor\"\r\n\"01/01/2024\";\"14,25\"\r\n")
	badPayload := []byte("\"wrong\";\"valor\"\r\n\"01/01/2024\";\"14,25\"\r\n")
	raw := &orderingRawStore{}
	run := testRun()
	var snapshots []model.EconomicObservation
	app := &app{
		cfg: config.Config{Providers: config.Providers{BCB: config.BCBProvider{Enabled: true, Series: []config.BCBSeries{
			{Code: "432", Geography: "BR", Unit: "percent", Frequency: "daily"},
			{Code: "433", Geography: "BR", Unit: "percent", Frequency: "daily"},
		}}}},
		raw:        raw,
		normalized: &orderingNormalizedStore{raw: raw, expectedRun: run, expectedHash: hashPayload(goodPayload), expectedSource: "bcb", locatorPrefix: "csv/date="},
		http: collectorHTTPFake{responses: map[string][]byte{
			"bcdata.sgs.432": goodPayload,
			"bcdata.sgs.433": badPayload,
		}},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: run, onFinalize: func(_ metadata.Metrics, _ []model.PriceBar, macros []model.EconomicObservation) {
			snapshots = append(snapshots, macros...)
			raw.events = append(raw.events, "metadata:finalize")
		}},
		batchKey: "bcb-partial-test",
	}

	if err := app.run(context.Background(), "bcb"); err == nil {
		t.Fatal("expected partial BCB collection error")
	}
	if len(snapshots) != 1 || snapshots[0].SeriesID != "432" {
		t.Fatalf("finalized BCB snapshots = %+v, want only accepted series 432", snapshots)
	}
	if !reflect.DeepEqual(raw.events, []string{"raw:put:complete", "canonical:economics:write", "raw:put:complete", "metadata:finalize"}) {
		t.Fatalf("BCB partial publication order = %v", raw.events)
	}
	if got := len(decodeTestManifest(t, raw.manifestPayload).Entries); got != 2 {
		t.Fatalf("BCB partial manifest entries = %d, want 2", got)
	}
}

func TestCollectorBCBEmptyAcceptedRowsStillFinalize(t *testing.T) {
	payload := []byte("\"data\";\"valor\"\r\n")
	raw := &orderingRawStore{}
	run := testRun()
	var finalized metadata.Metrics
	var snapshots []model.EconomicObservation
	app := &app{
		cfg:        config.Config{Providers: config.Providers{BCB: config.BCBProvider{Enabled: true, Series: []config.BCBSeries{{Code: "432", Geography: "BR", Unit: "percent", Frequency: "daily"}}}}},
		raw:        raw,
		normalized: &orderingNormalizedStore{raw: raw, expectedRun: run, expectedHash: hashPayload(payload), expectedSource: "bcb", locatorPrefix: "csv/date=", zeroRows: true},
		http:       collectorHTTPFake{payload: payload},
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: run, onFinalize: func(m metadata.Metrics, _ []model.PriceBar, macros []model.EconomicObservation) {
			finalized = m
			snapshots = append(snapshots, macros...)
		}},
		batchKey: "bcb-empty-test",
	}

	if err := app.run(context.Background(), "bcb"); err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 0 || finalized.Written != 0 {
		t.Fatalf("BCB empty finalization snapshots=%d metrics=%+v", len(snapshots), finalized)
	}
	if got := len(decodeTestManifest(t, raw.manifestPayload).Entries); got != 1 {
		t.Fatalf("BCB empty manifest entries = %d, want 1", got)
	}
	if !reflect.DeepEqual(raw.events, []string{"raw:put:complete", "canonical:economics:write"}) {
		t.Fatalf("BCB empty publication order = %v", raw.events)
	}
}

func TestCollectorValidatesBCBSourceSelection(t *testing.T) {
	app := &app{}
	if err := app.run(context.Background(), "unknown"); err == nil || !strings.Contains(err.Error(), "unknown source") {
		t.Fatalf("unknown source error = %v", err)
	}
	if err := app.run(context.Background(), "bcb"); err == nil || !strings.Contains(err.Error(), "BCB provider is disabled") {
		t.Fatalf("disabled BCB error = %v", err)
	}
	if err := app.run(context.Background(), "ptax"); err == nil || !strings.Contains(err.Error(), "PTAX provider is disabled") {
		t.Fatalf("disabled PTAX error = %v", err)
	}
	if err := app.run(context.Background(), "b3-prices"); err == nil || !strings.Contains(err.Error(), "B3 historical prices provider is disabled") {
		t.Fatalf("disabled B3 historical prices error = %v", err)
	}
}

func TestCollectorB3HistoricalPricesRetainsRawOnParseFailure(t *testing.T) {
	payload := []byte("not a ZIP archive")
	raw := &orderingRawStore{}
	run := testRun()
	var finalized metadata.Metrics
	app := &app{
		cfg: config.Config{
			Providers: config.Providers{B3HistoricalPrices: config.B3HistoricalPriceProvider{
				Enabled: true, Year: 2021, Start: "2021-09-06", End: "2021-09-10", Tickers: []string{"PETZ3"},
			}},
			Universe: []config.Security{{SecurityID: testSecurityID, Ticker: "PETZ3", ISIN: "BRPETZACNOR2", Currency: "BRL"}},
		},
		raw: raw, normalized: &orderingNormalizedStore{raw: raw},
		http: collectorHTTPFake{payload: payload},
		log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: run, onStartInputs: func(inputs metadata.RunInputs) {
			if inputs.Source != "b3_cotahist" || inputs.Provider.B3HistoricalQuoteYear != 2021 {
				t.Errorf("B3 historical price run inputs = %+v", inputs)
			}
		}, onFinalize: func(m metadata.Metrics, prices []model.PriceBar, macros []model.EconomicObservation) {
			finalized = m
			if len(prices) != 0 || len(macros) != 0 {
				t.Errorf("failed B3 price run finalized canonical rows")
			}
		}},
		batchKey: "b3-price-raw-first-test",
		now:      func() time.Time { return time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC) },
	}

	err := app.run(context.Background(), "b3-prices")
	if err == nil || !strings.Contains(err.Error(), "open ZIP") {
		t.Fatalf("error = %v, want ZIP parse failure", err)
	}
	if len(raw.payloads) != 1 || !bytes.Equal(raw.payloads[0], payload) {
		t.Fatalf("downloaded ZIP evidence was not retained: %#v", raw.payloads)
	}
	if finalized.RawPayloads != 1 || finalized.Written != 0 || finalized.Err == nil || finalized.RawPayloadManifestHash == "" {
		t.Fatalf("finalized metrics = %+v", finalized)
	}
}

func TestCollectorPTAXStoresRawBeforeCanonicalWrite(t *testing.T) {
	payload := []byte(`{"@odata.context":"official","value":[{"cotacaoCompra":5.22300,"cotacaoVenda":5.22360,"dataHoraCotacao":"2026-08-14 13:10:22.94166"}]}`)
	raw := &orderingRawStore{}
	run := testRun()
	normalized := &orderingNormalizedStore{raw: raw, expectedRun: run, expectedHash: hashPayload(payload), expectedSource: "bcb_ptax", locatorPrefix: "value/"}
	var finalized metadata.Metrics
	app := &app{
		cfg: config.Config{Providers: config.Providers{PTAX: config.PTAXProvider{Enabled: true, Start: "2026-08-10", End: "2026-08-14"}}},
		raw: raw, normalized: normalized, http: collectorHTTPFake{payload: payload},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: run, onStartInputs: func(inputs metadata.RunInputs) {
			if inputs.Source != "bcb_ptax" || inputs.Provider.FXRequest == nil {
				t.Errorf("PTAX run inputs = %+v", inputs)
			}
		}, onFinalize: func(m metadata.Metrics, prices []model.PriceBar, macros []model.EconomicObservation) {
			finalized = m
			if len(prices) != 0 || len(macros) != 0 {
				t.Errorf("PTAX finalized latest snapshots: prices=%d macros=%d", len(prices), len(macros))
			}
			raw.events = append(raw.events, "metadata:finalize")
		}},
		batchKey: "ptax-ordering-test",
		now:      func() time.Time { return time.Date(2026, 8, 14, 17, 0, 0, 0, time.UTC) },
	}

	if err := app.run(context.Background(), "ptax"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(raw.events, []string{"raw:put:complete", "canonical:fx:write", "metadata:finalize"}) {
		t.Fatalf("PTAX publication order = %v", raw.events)
	}
	if len(normalized.fx) != 1 || normalized.fx[0].SellRate != "5.2236" || normalized.fx[0].AvailableAt != normalized.fx[0].FixingAt {
		t.Fatalf("PTAX canonical rows = %+v", normalized.fx)
	}
	if len(raw.rawKeys) != 1 || !strings.HasPrefix(raw.rawKeys[0], "bcb_ptax/closing/") || !strings.Contains(raw.rawKeys[0], "/USD-BRL/") {
		t.Fatalf("PTAX raw keys = %v", raw.rawKeys)
	}
	if got := raw.rawMetadata[0].Attributes; got["provider_format"] != "odata-json" || got["rate_kind"] != "ptax_closing" || got["start"] != "2026-08-10" || got["end"] != "2026-08-14" {
		t.Fatalf("PTAX raw attributes = %+v", got)
	}
	manifest := decodeTestManifest(t, raw.manifestPayload)
	if len(manifest.Entries) != 1 || manifest.Entries[0].LogicalKey != "bcb_ptax/closing/USD-BRL/start/2026-08-10/end/2026-08-14" {
		t.Fatalf("PTAX raw manifest = %+v", manifest.Entries)
	}
	if finalized.Written != 1 || finalized.RawPayloads != 1 || finalized.Cursor["pair"] != "USD-BRL" || finalized.Cursor["canonical_path"] != "test/data.parquet" {
		t.Fatalf("PTAX final metrics = %+v", finalized)
	}
}

func TestCollectorPTAXRetainsRawOnParseFailure(t *testing.T) {
	payload := []byte(`{"@odata.context":"official","value":[],"unexpected":true}`)
	raw := &orderingRawStore{}
	run := testRun()
	var finalized metadata.Metrics
	app := &app{
		cfg: config.Config{Providers: config.Providers{PTAX: config.PTAXProvider{Enabled: true, Start: "2026-08-10", End: "2026-08-14"}}},
		raw: raw, normalized: &orderingNormalizedStore{raw: raw}, http: collectorHTTPFake{payload: payload},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: run, onFinalize: func(m metadata.Metrics, _ []model.PriceBar, _ []model.EconomicObservation) {
			finalized = m
			raw.events = append(raw.events, "metadata:finalize")
		}},
		batchKey: "ptax-parse-error-test",
		now:      func() time.Time { return time.Date(2026, 8, 14, 17, 0, 0, 0, time.UTC) },
	}

	if err := app.run(context.Background(), "ptax"); err == nil {
		t.Fatal("expected PTAX parse error")
	}
	if len(raw.payloads) != 1 || !bytes.Equal(raw.payloads[0], payload) || !reflect.DeepEqual(raw.events, []string{"raw:put:complete", "metadata:finalize"}) {
		t.Fatalf("PTAX retained state payloads=%q events=%v", raw.payloads, raw.events)
	}
	if finalized.RawPayloads != 1 || finalized.Written != 0 || finalized.Err == nil {
		t.Fatalf("PTAX parse failure metrics = %+v", finalized)
	}
}

func TestCollectorCVMStoresRawBeforeFilingPublication(t *testing.T) {
	metadataPayload := []byte("Campo: Assunto\nDescricao: documento IPE\n")
	ipePayload := collectorCVMIPERow("000123")
	archivePayload := collectorCVMArchive(t, ipePayload)
	run := testRun()
	raw := &orderingRawStore{}
	var finalized metadata.Metrics
	app := &app{
		cfg: config.Config{
			Universe:  []config.Security{{IssuerID: testIssuerID, SecurityID: testSecurityID, CVMCode: "000123"}},
			Providers: config.Providers{CVM: config.CVMProvider{Enabled: true, IPE: config.CVMIPEConfig{Years: []int{2026}}}},
		},
		raw: raw,
		normalized: &orderingNormalizedStore{
			raw: raw, expectedRun: run, expectedHash: hashPayload(archivePayload), expectedSource: "cvm_ipe", locatorPrefix: "zip/year=2026/", zeroRows: true,
		},
		http: collectorHTTPFake{responses: map[string][]byte{
			cvm.DefaultIPEMetadataURL:                   metadataPayload,
			fmt.Sprintf(cvm.DefaultIPEArchiveURL, 2026): archivePayload,
		}},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: run, onFinalize: func(m metadata.Metrics, _ []model.PriceBar, _ []model.EconomicObservation) {
			finalized = m
			raw.events = append(raw.events, "metadata:finalize")
		}},
		batchKey: "cvm-ordering-test",
	}

	if err := app.run(context.Background(), "cvm"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(raw.events, []string{"raw:put:complete", "raw:put:complete", "canonical:filings:write", "metadata:finalize"}) {
		t.Fatalf("CVM publication order = %v", raw.events)
	}
	filings := app.normalized.(*orderingNormalizedStore).filings
	if len(filings) != 1 {
		t.Fatalf("canonical filings = %d, want 1", len(filings))
	}
	if finalized.Written != 0 {
		t.Fatalf("finalized changed rows = %d, want zero-row idempotent publication", finalized.Written)
	}
	filing := filings[0]
	if filing.Source != "cvm_ipe" || filing.IssuerID != testIssuerID || filing.SourceDocumentID != "cvm-ipe:000123:0000000000000001:v01" {
		t.Fatalf("CVM filing identity = %+v", filing)
	}
	if filing.DocumentURL == "" || filing.FormType != "cvm_ipe" || filing.AccessionNumber != "0000000000000001" {
		t.Fatalf("CVM filing source fields = %+v", filing)
	}
	if !filing.Temporal.PublishedAt.IsZero() || filing.Temporal.PublishedPrecision != model.PrecisionUnknown {
		t.Fatalf("CVM publication semantics = %+v", filing.Temporal)
	}
	if filing.PeriodEnd == nil || filing.Temporal.ObservedPrecision != model.PrecisionDate {
		t.Fatalf("CVM observed semantics = %+v", filing)
	}
	if filing.RawPayloadHash != hashPayload(archivePayload) || filing.Provenance.RawPayloadHash != hashPayload(archivePayload) {
		t.Fatalf("CVM filing raw hashes = %q and %q, want %q", filing.RawPayloadHash, filing.Provenance.RawPayloadHash, hashPayload(archivePayload))
	}
	if filing.Provenance.DataSourceID != run.DataSourceID || filing.Provenance.IngestionRunID != run.ID || filing.Provenance.RawRecordLocator == "" || !filing.Provenance.IngestedAt.Equal(filing.Temporal.IngestedAt) {
		t.Fatalf("CVM filing provenance = %+v, want run %s/%s", filing.Provenance, run.DataSourceID, run.ID)
	}
	if len(raw.rawMetadata) != 2 {
		t.Fatalf("CVM raw metadata count = %d, want 2", len(raw.rawMetadata))
	}
	for _, stored := range raw.rawMetadata {
		if stored.Attributes["source_url"] == "" || stored.Attributes["parser_version"] != cvm.ParserVersion || stored.Attributes["adapter_sha256"] != stored.SHA256 {
			t.Fatalf("CVM raw attributes = %+v", stored.Attributes)
		}
	}
	manifest := decodeTestManifest(t, raw.manifestPayload)
	if len(manifest.Entries) != 2 || manifest.Entries[1].Attributes["resource_kind"] == "" {
		t.Fatalf("CVM raw manifest entries = %+v", manifest.Entries)
	}
}

func TestCollectorCVMRetainsEveryReturnedRawResourceOnParseFailure(t *testing.T) {
	metadataPayload := []byte("Campo: Assunto\nDescricao: documento IPE\n")
	badArchive := []byte("not a zip")
	run := testRun()
	raw := &orderingRawStore{}
	app := &app{
		cfg:        config.Config{Providers: config.Providers{CVM: config.CVMProvider{Enabled: true, IPE: config.CVMIPEConfig{Years: []int{2026}}}}},
		raw:        raw,
		normalized: &orderingNormalizedStore{raw: raw, expectedRun: run, expectedSource: "cvm_ipe", locatorPrefix: "zip/year=2026/"},
		http: collectorHTTPFake{responses: map[string][]byte{
			cvm.DefaultIPEMetadataURL:                   metadataPayload,
			fmt.Sprintf(cvm.DefaultIPEArchiveURL, 2026): badArchive,
		}},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: run, onFinalize: func(m metadata.Metrics, prices []model.PriceBar, macros []model.EconomicObservation) {
			raw.events = append(raw.events, "metadata:finalize")
			if len(prices) != 0 || len(macros) != 0 {
				t.Errorf("finalized snapshots = %d prices, %d macros; want none", len(prices), len(macros))
			}
			if m.RawPayloads != 2 {
				t.Errorf("finalized raw payloads = %d, want 2", m.RawPayloads)
			}
		}},
		batchKey: "cvm-parse-error-test",
	}

	if err := app.run(context.Background(), "cvm"); err == nil {
		t.Fatal("expected CVM parse error")
	}
	if len(raw.payloads) != 2 || !bytes.Equal(raw.payloads[0], metadataPayload) || !bytes.Equal(raw.payloads[1], badArchive) {
		t.Fatalf("CVM raw payloads = %q, want metadata and bad archive", raw.payloads)
	}
	if !reflect.DeepEqual(raw.events, []string{"raw:put:complete", "raw:put:complete", "metadata:finalize"}) {
		t.Fatalf("CVM parse-error publication order = %v", raw.events)
	}
	if got := len(decodeTestManifest(t, raw.manifestPayload).Entries); got != 2 {
		t.Fatalf("CVM parse-error manifest entries = %d, want 2", got)
	}
}

func TestCollectorCVMIgnoresUnconfiguredRowsAndPreservesConfiguredRows(t *testing.T) {
	metadataPayload := []byte("Campo: Assunto\nDescricao: documento IPE\n")
	ipePayload := collectorCVMIPERows("000123", "999999")
	archivePayload := collectorCVMArchive(t, ipePayload)
	run := testRun()
	raw := &orderingRawStore{}
	var finalized metadata.Metrics
	var logs bytes.Buffer
	app := &app{
		cfg: config.Config{
			Universe:  []config.Security{{IssuerID: testIssuerID, SecurityID: testSecurityID, CVMCode: "000123"}},
			Providers: config.Providers{CVM: config.CVMProvider{Enabled: true, IPE: config.CVMIPEConfig{Years: []int{2026}}}},
		},
		raw:        raw,
		normalized: &orderingNormalizedStore{raw: raw, expectedRun: run, expectedHash: hashPayload(archivePayload), expectedSource: "cvm_ipe", locatorPrefix: "zip/year=2026/", zeroRows: true},
		http: collectorHTTPFake{responses: map[string][]byte{
			cvm.DefaultIPEMetadataURL:                   metadataPayload,
			fmt.Sprintf(cvm.DefaultIPEArchiveURL, 2026): archivePayload,
		}},
		log: slog.New(slog.NewTextHandler(&logs, nil)),
		metadata: collectorMetadataFake{run: run, onFinalize: func(m metadata.Metrics, _ []model.PriceBar, _ []model.EconomicObservation) {
			finalized = m
			raw.events = append(raw.events, "metadata:finalize")
		}},
		batchKey: "cvm-unconfigured-test",
	}

	if err := app.run(context.Background(), "cvm"); err != nil {
		t.Fatalf("unconfigured CVM row error = %v", err)
	}
	filings := app.normalized.(*orderingNormalizedStore).filings
	if len(filings) != 1 || filings[0].IssuerID != testIssuerID {
		t.Fatalf("configured CVM filings = %+v, want one successful configured filing", filings)
	}
	if finalized.Received != 2 || finalized.Rejected != 0 || finalized.Written != 0 || finalized.RawPayloads != 2 {
		t.Fatalf("unconfigured CVM metrics = %+v", finalized)
	}
	if finalized.Err != nil {
		t.Fatalf("unconfigured CVM finalization error = %v", finalized.Err)
	}
	if finalized.Cursor["ipe_rows_matched"] != 1 || finalized.Cursor["ipe_rows_unconfigured"] != 1 || finalized.Cursor["ipe_rows_ignored"] != 1 || finalized.Cursor["ipe_rows_ambiguous"] != 0 {
		t.Fatalf("unconfigured CVM cursor = %+v", finalized.Cursor)
	}
	if !strings.Contains(logs.String(), "status=success") {
		t.Fatalf("unconfigured CVM run log = %q", logs.String())
	}
}

func TestCollectorCVMRejectsAmbiguousConfiguredCodeSafely(t *testing.T) {
	metadataPayload := []byte("Campo: Assunto\nDescricao: documento IPE\n")
	ipePayload := collectorCVMIPERow("000123")
	archivePayload := collectorCVMArchive(t, ipePayload)
	run := testRun()
	raw := &orderingRawStore{}
	var finalized metadata.Metrics
	var logs bytes.Buffer
	app := &app{
		cfg: config.Config{
			Universe: []config.Security{
				{IssuerID: testIssuerID, SecurityID: testSecurityID, CVMCode: "000123"},
				{IssuerID: "c7d2b89a-6a89-44f7-88d5-3f6c0f7d7b40", SecurityID: "3f7e3b1c-9e4f-4d65-b11a-3cc4e4c0c5e5", CVMCode: "000123"},
			},
			Providers: config.Providers{CVM: config.CVMProvider{Enabled: true, IPE: config.CVMIPEConfig{Years: []int{2026}}}},
		},
		raw:        raw,
		normalized: &orderingNormalizedStore{raw: raw, expectedRun: run, expectedHash: hashPayload(archivePayload), expectedSource: "cvm_ipe", locatorPrefix: "zip/year=2026/"},
		http: collectorHTTPFake{responses: map[string][]byte{
			cvm.DefaultIPEMetadataURL:                   metadataPayload,
			fmt.Sprintf(cvm.DefaultIPEArchiveURL, 2026): archivePayload,
		}},
		log: slog.New(slog.NewTextHandler(&logs, nil)),
		metadata: collectorMetadataFake{run: run, onFinalize: func(m metadata.Metrics, _ []model.PriceBar, _ []model.EconomicObservation) {
			finalized = m
			raw.events = append(raw.events, "metadata:finalize")
		}},
		batchKey: "cvm-ambiguous-code-test",
	}

	if err := app.run(context.Background(), "cvm"); err == nil || !strings.Contains(err.Error(), "ambiguous duplicate configured CVM-code mappings") {
		t.Fatalf("ambiguous CVM error = %v", err)
	}
	if len(app.normalized.(*orderingNormalizedStore).filings) != 0 {
		t.Fatal("ambiguous CVM row was published")
	}
	if finalized.Received != 1 || finalized.Rejected != 1 || finalized.Written != 0 || finalized.RawPayloads != 2 {
		t.Fatalf("ambiguous CVM metrics = %+v", finalized)
	}
	if finalized.Err == nil || finalized.Cursor["ipe_rows_ambiguous"] != 1 || finalized.Cursor["ipe_rows_unconfigured"] != 0 || finalized.Cursor["ipe_rows_ignored"] != 0 {
		t.Fatalf("ambiguous CVM finalization = %+v", finalized)
	}
	if !strings.Contains(logs.String(), "status=partial") {
		t.Fatalf("ambiguous CVM run log = %q", logs.String())
	}
}

func TestCollectorCVMPreservesSuccessfulRowsWhenProviderRejectsRows(t *testing.T) {
	metadataPayload := []byte("Campo: Assunto\nDescricao: documento IPE\n")
	ipePayload := append(collectorCVMIPERow("000123"), []byte("malformed\n")...)
	archivePayload := collectorCVMArchive(t, ipePayload)
	run := testRun()
	raw := &orderingRawStore{}
	var finalized metadata.Metrics
	var logs bytes.Buffer
	app := &app{
		cfg: config.Config{
			Universe:  []config.Security{{IssuerID: testIssuerID, SecurityID: testSecurityID, CVMCode: "000123"}},
			Providers: config.Providers{CVM: config.CVMProvider{Enabled: true, IPE: config.CVMIPEConfig{Years: []int{2026}}}},
		},
		raw:        raw,
		normalized: &orderingNormalizedStore{raw: raw, expectedRun: run, expectedHash: hashPayload(archivePayload), expectedSource: "cvm_ipe", locatorPrefix: "zip/year=2026/"},
		http: collectorHTTPFake{responses: map[string][]byte{
			cvm.DefaultIPEMetadataURL:                   metadataPayload,
			fmt.Sprintf(cvm.DefaultIPEArchiveURL, 2026): archivePayload,
		}},
		log: slog.New(slog.NewTextHandler(&logs, nil)),
		metadata: collectorMetadataFake{run: run, onFinalize: func(m metadata.Metrics, _ []model.PriceBar, _ []model.EconomicObservation) {
			finalized = m
			raw.events = append(raw.events, "metadata:finalize")
		}},
		batchKey: "cvm-source-rejected-test",
	}

	if err := app.run(context.Background(), "cvm"); err == nil || !strings.Contains(err.Error(), "CVM source records rejected") {
		t.Fatalf("provider-rejected CVM error = %v", err)
	}
	if len(app.normalized.(*orderingNormalizedStore).filings) != 1 {
		t.Fatal("successful CVM row was not published")
	}
	if finalized.Received != 2 || finalized.Rejected != 1 || finalized.Written != 1 || finalized.RawPayloads != 2 {
		t.Fatalf("provider-rejected CVM metrics = %+v", finalized)
	}
	if finalized.Err == nil || finalized.Cursor["records_rejected"] != 1 {
		t.Fatalf("provider-rejected CVM finalization = %+v", finalized)
	}
	if !strings.Contains(logs.String(), "status=partial") {
		t.Fatalf("provider-rejected CVM run log = %q", logs.String())
	}
}

func TestCollectorCVMRetainsCADAsExplicitIngestionOnly(t *testing.T) {
	cadPayload := collectorCVMCADPayload()
	run := testRun()
	raw := &orderingRawStore{}
	var finalized metadata.Metrics
	app := &app{
		cfg:        config.Config{Providers: config.Providers{CVM: config.CVMProvider{Enabled: true, CAD: true}}},
		raw:        raw,
		normalized: &orderingNormalizedStore{raw: raw, expectedRun: run, expectedSource: "cvm_ipe", locatorPrefix: "zip/year="},
		http:       collectorHTTPFake{responses: map[string][]byte{cvm.DefaultCADURL: cadPayload}},
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: run, onFinalize: func(m metadata.Metrics, _ []model.PriceBar, _ []model.EconomicObservation) {
			finalized = m
			raw.events = append(raw.events, "metadata:finalize")
		}},
		batchKey: "cvm-cad-ingestion-only-test",
	}

	if err := app.run(context.Background(), "cvm"); err == nil || !strings.Contains(err.Error(), "current snapshot is ingestion-only") {
		t.Fatalf("CAD publication error = %v", err)
	}
	if len(raw.payloads) != 1 || !bytes.Equal(raw.payloads[0], cadPayload) {
		t.Fatalf("CAD raw payload = %q, want untouched payload", raw.payloads)
	}
	if len(app.normalized.(*orderingNormalizedStore).filings) != 0 || finalized.Rejected != 1 || finalized.RawPayloads != 1 {
		t.Fatalf("CAD publication state = filings=%d metrics=%+v", len(app.normalized.(*orderingNormalizedStore).filings), finalized)
	}
}

func TestCollectorCVMSkipsTerminalRetryAfterCapturingRunInputs(t *testing.T) {
	run := testRun()
	run.Skip = true
	raw := &orderingRawStore{}
	var started metadata.RunInputs
	app := &app{
		cfg: config.Config{
			Universe:  []config.Security{{IssuerID: testIssuerID, SecurityID: testSecurityID, CVMCode: "000123"}},
			Providers: config.Providers{CVM: config.CVMProvider{Enabled: true, CAD: true, IPE: config.CVMIPEConfig{Years: []int{2026, 2025}}}},
		},
		raw:        raw,
		normalized: &orderingNormalizedStore{raw: raw, expectedRun: run},
		http:       collectorHTTPFake{},
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: run, onStartInputs: func(inputs metadata.RunInputs) {
			started = inputs
		}},
		batchKey: "cvm-retry-test",
	}

	if err := app.run(context.Background(), "cvm"); err != nil {
		t.Fatal(err)
	}
	if started.Source != "cvm" || started.Provider.Name != "cvm" || started.Provider.Kind != "filings" || started.Provider.Format != "cad_csv+ipe_metadata_zip" {
		t.Fatalf("CVM retry run inputs = %+v", started)
	}
	if len(started.Provider.IssuerRequests) != 1 || !reflect.DeepEqual(started.Provider.IssuerRequests[0].Resources, []string{"cad", "ipe:2025", "ipe:2026", "cvm_code:000123"}) {
		t.Fatalf("CVM effective request resources = %+v", started.Provider.IssuerRequests)
	}
	if len(raw.payloads) != 0 || len(raw.events) != 0 {
		t.Fatalf("terminal CVM retry performed work: payloads=%d events=%v", len(raw.payloads), raw.events)
	}
}

func TestCollectorFinalizesSnapshotsWhenWriteAddsNoRows(t *testing.T) {
	t.Run("yahoo", func(t *testing.T) {
		payload := []byte(`{"chart":{"result":[{"meta":{"currency":"USD","exchangeTimezoneName":"America/New_York"},"timestamp":[1719840600],"indicators":{"quote":[{"open":[10],"high":[12],"low":[9],"close":[11],"volume":[100]}]}}],"error":null}}`)
		raw := &orderingRawStore{}
		run := testRun()
		var written int64
		var snapshots []model.PriceBar
		app := &app{
			cfg: config.Config{
				Universe:  []config.Security{{SecurityID: testSecurityID, YahooSymbol: "AAPL", Currency: "USD"}},
				Providers: config.Providers{Prices: config.PriceProvider{Enabled: true, Start: "2024-01-01", End: "2024-12-31"}},
			},
			raw: raw,
			normalized: &orderingNormalizedStore{
				raw: raw, expectedRun: run, expectedHash: hashPayload(payload), expectedSource: "yahoo", locatorPrefix: "chart/date=", zeroRows: true,
			},
			http: collectorHTTPFake{payload: payload},
			log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
			metadata: collectorMetadataFake{run: run, onFinalize: func(m metadata.Metrics, prices []model.PriceBar, macros []model.EconomicObservation) {
				written = m.Written
				snapshots = append(snapshots, prices...)
				if len(macros) != 0 {
					t.Errorf("macro snapshots = %d, want none", len(macros))
				}
			}},
			batchKey: "zero-row-yahoo-test",
		}

		if err := app.run(context.Background(), "prices"); err != nil {
			t.Fatal(err)
		}
		if written != 0 {
			t.Fatalf("finalized written rows = %d, want 0", written)
		}
		if len(snapshots) != 1 || snapshots[0].SecurityID != testSecurityID {
			t.Fatalf("finalized price snapshots = %+v, want one snapshot for %s", snapshots, testSecurityID)
		}
	})

	t.Run("fred", func(t *testing.T) {
		payload := []byte("observation_date,DGS10\n2024-01-02,4.25\n")
		raw := &orderingRawStore{}
		run := testRun()
		var written int64
		var snapshots []model.EconomicObservation
		app := &app{
			cfg: config.Config{
				Providers: config.Providers{FRED: config.FREDProvider{Enabled: true, Series: []string{"DGS10"}}},
			},
			raw: raw,
			normalized: &orderingNormalizedStore{
				raw: raw, expectedRun: run, expectedHash: hashPayload(payload), expectedSource: "fred", locatorPrefix: "csv/date=", zeroRows: true,
			},
			http: collectorHTTPFake{responses: map[string][]byte{"fredgraph.csv": payload}},
			log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
			metadata: collectorMetadataFake{run: run, onFinalize: func(m metadata.Metrics, prices []model.PriceBar, macros []model.EconomicObservation) {
				written = m.Written
				snapshots = append(snapshots, macros...)
				if len(prices) != 0 {
					t.Errorf("price snapshots = %d, want none", len(prices))
				}
			}},
			batchKey: "zero-row-fred-test",
		}

		if err := app.run(context.Background(), "fred"); err != nil {
			t.Fatal(err)
		}
		if written != 0 {
			t.Fatalf("finalized written rows = %d, want 0", written)
		}
		if len(snapshots) != 1 || snapshots[0].SeriesID != "DGS10" {
			t.Fatalf("finalized economic snapshots = %+v, want one snapshot for DGS10", snapshots)
		}
	})
}

func TestCollectorPartialRunFinalizesSuccessfulEntityCandidates(t *testing.T) {
	goodPayload := []byte(`{"chart":{"result":[{"meta":{"currency":"USD","exchangeTimezoneName":"America/New_York"},"timestamp":[1719840600],"indicators":{"quote":[{"open":[10],"high":[12],"low":[9],"close":[11],"volume":[100]}]}}],"error":null}}`)
	badPayload := []byte(`{"chart":{"result":[],"error":null}}`)
	secondSecurityID := "7f3c1f6b-42dc-4d0a-9c1b-8d8a3c5f2b11"
	raw := &orderingRawStore{}
	run := testRun()
	var snapshots []model.PriceBar
	app := &app{
		cfg: config.Config{
			Universe: []config.Security{
				{SecurityID: testSecurityID, YahooSymbol: "AAPL", Currency: "USD"},
				{SecurityID: secondSecurityID, YahooSymbol: "MSFT", Currency: "USD"},
			},
			Providers: config.Providers{Prices: config.PriceProvider{Enabled: true, Start: "2024-01-01", End: "2024-12-31"}},
		},
		raw: raw,
		normalized: &orderingNormalizedStore{
			raw: raw, expectedRun: run, expectedHash: hashPayload(goodPayload), expectedSource: "yahoo", locatorPrefix: "chart/date=",
		},
		http: collectorHTTPFake{responses: map[string][]byte{"AAPL": goodPayload, "MSFT": badPayload}},
		log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: run, onFinalize: func(_ metadata.Metrics, prices []model.PriceBar, _ []model.EconomicObservation) {
			snapshots = append(snapshots, prices...)
			raw.events = append(raw.events, "metadata:finalize")
		}},
		batchKey: "partial-yahoo-test",
	}

	if err := app.run(context.Background(), "prices"); err == nil {
		t.Fatal("expected partial collection error")
	}
	if len(snapshots) != 1 || snapshots[0].SecurityID != testSecurityID {
		t.Fatalf("finalized price snapshots = %+v, want only successful entity %s", snapshots, testSecurityID)
	}
	wantEvents := []string{"raw:put:complete", "canonical:prices:write", "raw:put:complete", "metadata:finalize"}
	if !reflect.DeepEqual(raw.events, wantEvents) {
		t.Fatalf("publication order = %v, want %v", raw.events, wantEvents)
	}
	if got := len(decodeTestManifest(t, raw.manifestPayload).Entries); got != 2 {
		t.Fatalf("partial raw manifest entries = %d, want 2", got)
	}
}

func TestCollectorPersistsProviderRawOnParseErrorBeforeFinalization(t *testing.T) {
	t.Run("yahoo schema", func(t *testing.T) {
		payload := []byte(`{"chart":{"result":[],"error":null}}`)
		raw := &orderingRawStore{}
		run := testRun()
		app := &app{
			cfg: config.Config{
				Universe:  []config.Security{{SecurityID: testSecurityID, YahooSymbol: "AAPL", Currency: "USD"}},
				Providers: config.Providers{Prices: config.PriceProvider{Enabled: true, Start: "2024-01-01", End: "2024-12-31"}},
			},
			raw:        raw,
			normalized: &orderingNormalizedStore{raw: raw, expectedRun: run, expectedSource: "yahoo", locatorPrefix: "chart/date="},
			http:       collectorHTTPFake{payload: payload},
			log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
			metadata: collectorMetadataFake{run: run, onFinalize: func(_ metadata.Metrics, prices []model.PriceBar, macros []model.EconomicObservation) {
				raw.events = append(raw.events, "metadata:finalize")
				if len(prices) != 0 || len(macros) != 0 {
					t.Errorf("finalized snapshots = %d prices, %d macros; want none", len(prices), len(macros))
				}
			}},
			batchKey: "parse-error-yahoo-test",
		}

		if err := app.run(context.Background(), "prices"); err == nil {
			t.Fatal("expected Yahoo schema error")
		}
		if len(raw.payloads) != 1 || string(raw.payloads[0]) != string(payload) {
			t.Fatalf("persisted Yahoo raw payload = %q, want %q", raw.payloads, payload)
		}
		if got := len(decodeTestManifest(t, raw.manifestPayload).Entries); got != 1 {
			t.Fatalf("Yahoo parse-error manifest entries = %d, want 1", got)
		}
		if !reflect.DeepEqual(raw.events, []string{"raw:put:complete", "metadata:finalize"}) {
			t.Fatalf("Yahoo publication order = %v", raw.events)
		}
	})

	t.Run("fred schema", func(t *testing.T) {
		payload := []byte("wrong_column,DGS10\n2024-01-02,4.25\n")
		raw := &orderingRawStore{}
		run := testRun()
		app := &app{
			cfg:        config.Config{Providers: config.Providers{FRED: config.FREDProvider{Enabled: true, Series: []string{"DGS10"}}}},
			raw:        raw,
			normalized: &orderingNormalizedStore{raw: raw, expectedRun: run, expectedSource: "fred", locatorPrefix: "csv/date="},
			http:       collectorHTTPFake{responses: map[string][]byte{"fredgraph.csv": payload}},
			log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
			metadata: collectorMetadataFake{run: run, onFinalize: func(_ metadata.Metrics, prices []model.PriceBar, macros []model.EconomicObservation) {
				raw.events = append(raw.events, "metadata:finalize")
				if len(prices) != 0 || len(macros) != 0 {
					t.Errorf("finalized snapshots = %d prices, %d macros; want none", len(prices), len(macros))
				}
			}},
			batchKey: "parse-error-fred-test",
		}

		if err := app.run(context.Background(), "fred"); err == nil {
			t.Fatal("expected FRED schema error")
		}
		if len(raw.payloads) != 1 || string(raw.payloads[0]) != string(payload) {
			t.Fatalf("persisted FRED raw payload = %q, want %q", raw.payloads, payload)
		}
		if got := len(decodeTestManifest(t, raw.manifestPayload).Entries); got != 1 {
			t.Fatalf("FRED parse-error manifest entries = %d, want 1", got)
		}
		if !reflect.DeepEqual(raw.events, []string{"raw:put:complete", "metadata:finalize"}) {
			t.Fatalf("FRED publication order = %v", raw.events)
		}
	})
}

func collectorCVMIPERow(code string) []byte {
	return collectorCVMIPERows(code)
}

func collectorCVMIPERows(codes ...string) []byte {
	const header = "CNPJ_Companhia;Nome_Companhia;Codigo_CVM;Data_Referencia;Categoria;Tipo;Especie;Assunto;Data_Entrega;Tipo_Apresentacao;Protocolo_Entrega;Versao;Link_Download\n"
	var builder strings.Builder
	builder.WriteString(header)
	for i, code := range codes {
		protocol := fmt.Sprintf("%016d", i+1)
		builder.WriteString(fmt.Sprintf("12.345.678/0001-90;AÇÚCAR S.A.;%s;2025-12-31;FRE;Comunicado;Comunicado ao mercado;Distribuição;2026-01-05;AP;%s;01;https://www.rad.cvm.gov.br/ENET/frmDownloadDocumento.aspx?Tela=ext&numProtocolo=%d\n", code, protocol, i+1))
	}
	return []byte(builder.String())
}

func collectorCVMCADPayload() []byte {
	header := "CNPJ_CIA;DENOM_SOCIAL;DENOM_COMERC;DT_REG;DT_CONST;DT_CANCEL;MOTIVO_CANCEL;SIT;DT_INI_SIT;CD_CVM;SETOR_ATIV;TP_MERC;CATEG_REG;DT_INI_CATEG;SIT_EMISSOR;DT_INI_SIT_EMISSOR;CONTROLE_ACIONARIO;TP_ENDER;LOGRADOURO;COMPL;BAIRRO;MUN;UF;PAIS;CEP;DDD_TEL;TEL;DDD_FAX;FAX;EMAIL;TP_RESP;RESP;DT_INI_RESP;LOGRADOURO_RESP;COMPL_RESP;BAIRRO_RESP;MUN_RESP;UF_RESP;PAIS_RESP;CEP_RESP;DDD_TEL_RESP;TEL_RESP;DDD_FAX_RESP;FAX_RESP;EMAIL_RESP;CNPJ_AUDITOR;AUDITOR"
	fields := make([]string, len(strings.Split(header, ";")))
	fields[0] = "12.345.678/0001-90"
	fields[1] = "AÇÚCAR S.A."
	fields[3] = "2020-01-02"
	fields[7] = "ATIVO"
	fields[9] = "000123"
	return []byte(header + "\n" + strings.Join(fields, ";") + "\n")
}

func collectorCVMArchive(t *testing.T, csvPayload []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	entry, err := archive.Create("ipe_cia_aberta_2026.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(csvPayload); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func hashPayload(payload []byte) string {
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:])
}

func decodeTestManifest(t *testing.T, payload []byte) storage.RawManifest {
	t.Helper()
	if len(payload) == 0 {
		t.Fatal("raw manifest was not published")
	}
	var manifest storage.RawManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		t.Fatalf("decode raw manifest: %v", err)
	}
	return manifest
}

func TestCollectorPublishesEmptyManifestBeforeFinalization(t *testing.T) {
	raw := &orderingRawStore{}
	var finalized metadata.Metrics
	run := testRun()
	app := &app{
		cfg: config.Config{Providers: config.Providers{Prices: config.PriceProvider{Enabled: true, Start: "2024-01-01", End: "2024-12-31"}}},
		raw: raw, normalized: &orderingNormalizedStore{raw: raw, expectedRun: run},
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata: collectorMetadataFake{run: run, onFinalize: func(m metadata.Metrics, _ []model.PriceBar, _ []model.EconomicObservation) { finalized = m }},
		batchKey: "empty-manifest-test",
	}

	if err := app.run(context.Background(), "prices"); err != nil {
		t.Fatal(err)
	}
	manifest := decodeTestManifest(t, raw.manifestPayload)
	if len(manifest.Entries) != 0 {
		t.Fatalf("empty manifest entries = %d", len(manifest.Entries))
	}
	if finalized.RawPayloadManifestHash != hashPayload(raw.manifestPayload) {
		t.Fatalf("finalized manifest hash = %q, want %q", finalized.RawPayloadManifestHash, hashPayload(raw.manifestPayload))
	}
}

func TestCollectorDoesNotFinalizeWhenManifestPublicationFails(t *testing.T) {
	raw := &orderingRawStore{manifestErr: errors.New("manifest store unavailable")}
	finalized := false
	run := testRun()
	app := &app{
		cfg:        config.Config{Providers: config.Providers{Prices: config.PriceProvider{Enabled: true, Start: "2024-01-01", End: "2024-12-31"}}},
		raw:        raw,
		normalized: &orderingNormalizedStore{raw: raw, expectedRun: run},
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		metadata:   collectorMetadataFake{run: run, onFinalize: func(metadata.Metrics, []model.PriceBar, []model.EconomicObservation) { finalized = true }},
		batchKey:   "manifest-failure-test",
	}

	if err := app.run(context.Background(), "prices"); err == nil {
		t.Fatal("manifest publication failure returned nil")
	}
	if finalized {
		t.Fatal("finalization ran after manifest publication failure")
	}
}
