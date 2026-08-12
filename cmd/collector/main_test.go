package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	events    []string
	completed bool
}

func (s *orderingRawStore) Put(ctx context.Context, _ string, data io.Reader, meta storage.RawMetadata) (storage.RawMetadata, error) {
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
	s.completed = true
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
	return "test/data.parquet", len(observations), nil
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
	return "test/data.parquet", len(observations), nil
}

type collectorMetadataFake struct {
	run metadata.Run
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

func (collectorMetadataFake) FinishRun(context.Context, metadata.Run, time.Time, metadata.Metrics) error {
	return nil
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

func hashPayload(payload []byte) string {
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:])
}
