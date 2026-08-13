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
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luisdourado/invs/config"
	"github.com/luisdourado/invs/internal/metadata"
	"github.com/luisdourado/invs/internal/model"
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
	raw            *orderingRawStore
	expectedRun    metadata.Run
	expectedHash   string
	expectedSource string
	locatorPrefix  string
	zeroRows       bool
	prices         []model.PriceBar
	fundamentals   []model.FundamentalObservation
	economics      []model.EconomicObservation
	filings        []model.Filing
}

func (s *orderingNormalizedStore) beforeWrite(kind string) error {
	if !s.raw.completed || len(s.raw.events) == 0 || s.raw.events[len(s.raw.events)-1] != "raw:put:complete" {
		return fmt.Errorf("%s canonical writer called before the latest raw Put completed", kind)
	}
	s.raw.events = append(s.raw.events, "canonical:"+kind+":write")
	return nil
}

func (s *orderingNormalizedStore) inspect(source, rawHash, locator string, provenance model.Provenance, temporal model.Temporal) error {
	if source != s.expectedSource {
		return fmt.Errorf("normalized source %q, want %q", source, s.expectedSource)
	}
	if rawHash != s.expectedHash || provenance.RawPayloadHash != s.expectedHash {
		return fmt.Errorf("normalized raw hashes = %q and %q, want %q", rawHash, provenance.RawPayloadHash, s.expectedHash)
	}
	if provenance.DataSourceID != s.expectedRun.DataSourceID || provenance.IngestionRunID != s.expectedRun.ID {
		return fmt.Errorf("normalized run provenance = data_source_id %q, ingestion_run_id %q; want %q, %q", provenance.DataSourceID, provenance.IngestionRunID, s.expectedRun.DataSourceID, s.expectedRun.ID)
	}
	if provenance.RawRecordLocator == "" || !strings.HasPrefix(locator, s.locatorPrefix) {
		return fmt.Errorf("normalized raw locator %q, want prefix %q", locator, s.locatorPrefix)
	}
	if provenance.IngestedAt.IsZero() || !provenance.IngestedAt.Equal(temporal.IngestedAt) {
		return fmt.Errorf("normalized ingested_at mismatch: provenance=%s temporal=%s", provenance.IngestedAt, temporal.IngestedAt)
	}
	if provenance.NormalizerVersion != model.NormalizerVersion {
		return fmt.Errorf("normalized version %q, want %q", provenance.NormalizerVersion, model.NormalizerVersion)
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
	for _, observation := range observations {
		if err := s.inspect(observation.Source, observation.RawPayloadHash, observation.Provenance.RawRecordLocator, observation.Provenance, observation.Temporal); err != nil {
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

type collectorMetadataFake struct {
	run           metadata.Run
	onFinalize    func(metadata.Metrics, []model.PriceBar, []model.EconomicObservation)
	onStart       func(time.Time)
	onStartInputs func(metadata.RunInputs)
	onFinish      func(time.Time)
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

	for _, inputs := range []metadata.RunInputs{secInputs, priceInputs, fredInputs, alfredInputs, bcbInputs} {
		got, err := metadata.NewRunMetadata(inputs)
		if err != nil {
			t.Fatalf("NewRunMetadata(%s): %v", inputs.Source, err)
		}
		if got.RunInputs.CanonicalJSONSHA256 == "" {
			t.Fatalf("%s run input hash is empty", inputs.Source)
		}
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

func TestCollectorSECStoresRawBeforeNormalizedWrite(t *testing.T) {
	secSubmissions := []byte(`{"cik":"0000000001","sic":"3571","name":"Example Corp","stateOfIncorporation":"DE","sicDescription":"Widgets","filings":{"recent":{"accessionNumber":["0001"],"filingDate":["2024-02-02"],"acceptanceDateTime":["2024-02-02T21:03:04.000Z"],"form":["10-K"],"primaryDocument":["example.htm"]}}}`)
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
			raw:            raw,
			expectedRun:    run,
			expectedHash:   hashPayload(secFacts),
			expectedSource: "sec",
			locatorPrefix:  "companyfacts/",
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
	if !reflect.DeepEqual(raw.events, []string{"raw:put:complete", "raw:put:complete", "canonical:fundamentals:write"}) {
		t.Fatalf("publication order = %v", raw.events)
	}
	if got := len(app.normalized.(*orderingNormalizedStore).fundamentals); got != 1 {
		t.Fatalf("fundamental observations = %d, want 1", got)
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
