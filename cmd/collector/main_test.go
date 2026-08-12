package main

import (
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

type collectorMetadataFake struct {
	run           metadata.Run
	onFinalize    func(metadata.Metrics, []model.PriceBar, []model.EconomicObservation)
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

func (f collectorMetadataFake) StartRun(_ context.Context, source, runKey string, startedAt time.Time) (metadata.Run, error) {
	run := f.run
	run.Source = source
	run.RunKey = runKey
	run.StartedAt = startedAt
	return run, nil
}

func (f collectorMetadataFake) FinalizeRun(_ context.Context, _ metadata.Run, _ time.Time, m metadata.Metrics, prices []model.PriceBar, macros []model.EconomicObservation) error {
	if f.onFinalize != nil {
		f.onFinalize(m, prices, macros)
	}
	return f.finalizeError
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
