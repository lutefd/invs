package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/luisdourado/invs/config"
	"github.com/luisdourado/invs/internal/httpx"
	"github.com/luisdourado/invs/internal/marketcalendar"
	"github.com/luisdourado/invs/internal/metadata"
	"github.com/luisdourado/invs/internal/model"
	"github.com/luisdourado/invs/internal/normalize"
	"github.com/luisdourado/invs/internal/providers"
	"github.com/luisdourado/invs/internal/providers/alfred"
	"github.com/luisdourado/invs/internal/providers/b3"
	"github.com/luisdourado/invs/internal/providers/bcb"
	"github.com/luisdourado/invs/internal/providers/cvm"
	"github.com/luisdourado/invs/internal/providers/fred"
	"github.com/luisdourado/invs/internal/providers/nyse"
	"github.com/luisdourado/invs/internal/providers/sec"
	"github.com/luisdourado/invs/internal/providers/yahoo"
	"github.com/luisdourado/invs/internal/storage"
)

type app struct {
	cfg        config.Config
	raw        storage.RawStore
	normalized normalizedStore
	http       httpGetter
	log        *slog.Logger
	metadata   metadataStore
	batchKey   string
	now        func() time.Time
}

type httpGetter interface {
	Get(context.Context, string) ([]byte, error)
}

type metadataStore interface {
	EnrichSECIssuer(context.Context, model.Issuer, string) error
	StartRun(context.Context, string, string, time.Time, metadata.RunInputs) (metadata.Run, error)
	FinalizeRun(context.Context, metadata.Run, time.Time, metadata.Metrics, []model.PriceBar, []model.EconomicObservation) error
}

type historicalTruthPublisher interface {
	PublishHistoricalTruth(context.Context, metadata.HistoricalTruthBatch) error
}

type normalizedStore interface {
	WritePrices(string, []model.PriceBar) (string, int, error)
	WriteFundamentals(string, []model.FundamentalObservation) (string, int, error)
	WriteEconomics(string, []model.EconomicObservation) (string, int, error)
	WriteFilings(string, []model.Filing) (string, int, error)
}

type operatorMetadataStore interface {
	LookupRun(context.Context, string, string, string) (metadata.Run, error)
	CancelRun(context.Context, metadata.Run, time.Time, string) error
}

type cancellationOptions struct {
	enabled bool
	source  string
	runKey  string
	runID   string
	reason  string
}

type metrics struct {
	Source                         string
	StartedAt                      time.Time
	Duration                       time.Duration
	Received, OutputRows, Rejected int
	RawObjects                     int
	RawBytes                       int64
	Manifest                       storage.RawManifest
	Cursor                         map[string]any
	RunKey                         string
}

type calendarEvidenceManifest struct {
	SchemaVersion string                     `json:"schema_version"`
	Source        string                     `json:"source"`
	MIC           string                     `json:"mic"`
	Year          int                        `json:"year"`
	CoverageStart string                     `json:"coverage_start"`
	CoverageEnd   string                     `json:"coverage_end"`
	Availability  string                     `json:"availability_policy"`
	Revision      string                     `json:"revision_policy"`
	Weekend       string                     `json:"weekend_policy"`
	Resources     []calendarEvidenceResource `json:"resources"`
}

type calendarEvidenceResource struct {
	Kind          string            `json:"kind"`
	Key           string            `json:"key"`
	URL           string            `json:"url"`
	SHA256        string            `json:"sha256"`
	FetchedAt     time.Time         `json:"fetched_at"`
	ParserVersion string            `json:"parser_version"`
	Metadata      map[string]string `json:"metadata"`
}

func canonicalTime(t time.Time) time.Time {
	return t.UTC().Truncate(time.Microsecond)
}

func (a *app) nowUTC() time.Time {
	if a.now == nil {
		return canonicalTime(time.Now())
	}
	return canonicalTime(a.now())
}

func main() {
	configPath := flag.String("config", "config/config.yaml", "configuration YAML")
	source := flag.String("source", "all", "collector source: all, sec, prices, fred, alfred, bcb, b3, b3-calendar, nyse, or cvm")
	runKey := flag.String("run-key", "", "stable batch retry key; omitted generates a unique invocation key")
	cancelRun := flag.Bool("cancel-run", false, "explicitly cancel one active orphan run")
	cancelSource := flag.String("cancel-source", "", "metadata source code for cancellation lookup, for example yahoo")
	cancelRunKey := flag.String("cancel-run-key", "", "exact ingestion run key for cancellation lookup")
	cancelRunID := flag.String("cancel-run-id", "", "ingestion run UUID for cancellation lookup")
	cancelReason := flag.String("cancel-reason", "", "non-empty operator reason for cancellation")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cancelOptions := cancellationOptions{enabled: *cancelRun, source: *cancelSource, runKey: *cancelRunKey, runID: *cancelRunID, reason: *cancelReason}
	if err := validateCancellationOptions(cancelOptions); err != nil {
		log.Error("invalid cancellation options", "error", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("configuration failed", "error", err)
		os.Exit(2)
	}
	if cancelOptions.enabled {
		metadataRepo, err := metadata.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			log.Error("metadata database unavailable", "error", err)
			os.Exit(2)
		}
		if metadataRepo == nil {
			log.Error("metadata database unavailable", "error", errors.New("DATABASE_URL is required to cancel a run"))
			os.Exit(2)
		}
		defer metadataRepo.Close()
		run, err := cancelOrphanRun(ctx, metadataRepo, cancelOptions, canonicalTime(time.Now()))
		if err != nil {
			log.Error("run cancellation failed", "error", err)
			os.Exit(1)
		}
		log.Info("run cancelled", "source", run.Source, "run_key", run.RunKey, "run_id", run.ID, "reason", strings.TrimSpace(cancelOptions.reason))
		return
	}
	httpClient, err := httpx.New(httpx.Config{UserAgent: cfg.UserAgent, Timeout: cfg.HTTP.Timeout, RequestsPerSecond: cfg.HTTP.RequestsPerSecond, Burst: cfg.HTTP.Burst, MaxAttempts: cfg.HTTP.MaxAttempts, InitialBackoff: cfg.HTTP.InitialBackoff})
	if err != nil {
		log.Error("HTTP configuration failed", "error", err)
		os.Exit(2)
	}
	raw, err := storage.NewFileRawStore(filepath.Join(cfg.DataDir, "raw"))
	if err != nil {
		log.Error("raw store failed", "error", err)
		os.Exit(2)
	}
	normalized, err := normalize.NewWriter(filepath.Join(cfg.DataDir, "normalized"))
	if err != nil {
		log.Error("normalized store failed", "error", err)
		os.Exit(2)
	}
	if err := normalized.ValidateExisting(); err != nil {
		log.Error("normalized store requires explicit legacy-data handling", "error", err)
		os.Exit(2)
	}
	metadataRepo, err := metadata.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("metadata database unavailable", "error", err)
		os.Exit(2)
	}
	if metadataRepo == nil {
		log.Error("metadata database unavailable", "error", errors.New("DATABASE_URL is required for canonical collection"))
		os.Exit(2)
	}
	defer metadataRepo.Close()
	if err := metadataRepo.SyncCatalog(ctx, cfg); err != nil {
		log.Error("metadata catalog sync failed", "error", err)
		os.Exit(2)
	}
	batchKey := *runKey
	if batchKey == "" {
		batchKey = canonicalTime(time.Now()).Format("20060102T150405.000000000Z") + "-" + uuid.NewString()
	}
	a := &app{cfg: cfg, raw: raw, normalized: normalized, http: httpClient, log: log, metadata: metadataRepo, batchKey: batchKey, now: time.Now}
	if err := a.run(ctx, *source); err != nil {
		log.Error("collection failed", "source", *source, "error", err)
		os.Exit(1)
	}
}

func validateCancellationOptions(options cancellationOptions) error {
	if !options.enabled {
		if strings.TrimSpace(options.source) != "" || strings.TrimSpace(options.runKey) != "" || strings.TrimSpace(options.runID) != "" || strings.TrimSpace(options.reason) != "" {
			return errors.New("cancellation options require --cancel-run")
		}
		return nil
	}
	if strings.TrimSpace(options.reason) == "" {
		return errors.New("--cancel-reason is required and must not be blank")
	}
	if strings.TrimSpace(options.runID) != "" {
		if strings.TrimSpace(options.source) != "" || strings.TrimSpace(options.runKey) != "" {
			return errors.New("--cancel-run-id cannot be combined with --cancel-source or --cancel-run-key")
		}
		if _, err := uuid.Parse(strings.TrimSpace(options.runID)); err != nil {
			return fmt.Errorf("--cancel-run-id must be a UUID: %w", err)
		}
		return nil
	}
	if strings.TrimSpace(options.source) == "" || strings.TrimSpace(options.runKey) == "" {
		return errors.New("--cancel-source and --cancel-run-key are required when --cancel-run-id is omitted")
	}
	return nil
}

func cancelOrphanRun(ctx context.Context, store operatorMetadataStore, options cancellationOptions, finished time.Time) (metadata.Run, error) {
	finished = canonicalTime(finished)
	if err := validateCancellationOptions(options); err != nil {
		return metadata.Run{}, err
	}
	run, err := store.LookupRun(ctx, options.source, options.runKey, options.runID)
	if err != nil {
		return metadata.Run{}, err
	}
	if err := validateRunLineage(run); err != nil {
		return metadata.Run{}, err
	}
	if err := store.CancelRun(ctx, run, finished, options.reason); err != nil {
		return metadata.Run{}, err
	}
	return run, nil
}

func (a *app) run(ctx context.Context, source string) error {
	valid := map[string]bool{"all": true, "sec": true, "prices": true, "fred": true, "alfred": true, "bcb": true, "b3": true, "b3-calendar": true, "nyse": true, "cvm": true}
	if !valid[source] {
		return fmt.Errorf("unknown source %q", source)
	}
	if source == "sec" && !a.cfg.Providers.SEC.Enabled {
		return errors.New("SEC provider is disabled")
	}
	if source == "prices" && !a.cfg.Providers.Prices.Enabled {
		return errors.New("prices provider is disabled")
	}
	if source == "fred" && !a.cfg.Providers.FRED.Enabled {
		return errors.New("FRED provider is disabled")
	}
	if source == "alfred" && !a.cfg.Providers.ALFRED.Enabled {
		return errors.New("ALFRED provider is disabled")
	}
	if source == "bcb" && !a.cfg.Providers.BCB.Enabled {
		return errors.New("BCB provider is disabled")
	}
	if source == "b3" && !a.cfg.Providers.B3.Enabled {
		return errors.New("B3 provider is disabled")
	}
	if source == "b3-calendar" && (!a.cfg.Providers.B3.Enabled || !a.cfg.Providers.B3.Calendar.Enabled) {
		return errors.New("B3 calendar provider is disabled")
	}
	if source == "nyse" && !a.cfg.Providers.NYSE.Enabled {
		return errors.New("NYSE calendar provider is disabled")
	}
	if source == "cvm" && !a.cfg.Providers.CVM.Enabled {
		return errors.New("CVM provider is disabled")
	}
	var errs []error
	if (source == "all" || source == "sec") && a.cfg.Providers.SEC.Enabled {
		if err := a.collectSEC(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if (source == "all" || source == "prices") && a.cfg.Providers.Prices.Enabled {
		if err := a.collectPrices(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if (source == "all" || source == "fred") && a.cfg.Providers.FRED.Enabled {
		if err := a.collectFRED(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if (source == "all" || source == "alfred") && a.cfg.Providers.ALFRED.Enabled {
		if err := a.collectALFRED(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if (source == "all" || source == "bcb") && a.cfg.Providers.BCB.Enabled {
		if err := a.collectBCB(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if (source == "all" || source == "b3") && a.cfg.Providers.B3.Enabled {
		if err := a.collectB3(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if (source == "all" || source == "b3" || source == "b3-calendar") && a.cfg.Providers.B3.Enabled && a.cfg.Providers.B3.Calendar.Enabled {
		if err := a.collectB3Calendar(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if (source == "all" || source == "nyse") && a.cfg.Providers.NYSE.Enabled {
		if err := a.collectNYSECalendar(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if (source == "all" || source == "cvm") && a.cfg.Providers.CVM.Enabled {
		if err := a.collectCVM(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (a *app) collectSEC(ctx context.Context) error {
	c := sec.NewClient(a.http)
	m := metrics{Source: "sec", StartedAt: a.nowUTC(), Cursor: map[string]any{}}
	run, skip, err := a.start(ctx, &m, secRunInputs(a.cfg.Universe))
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	var errs []error
	for _, s := range a.cfg.Universe {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		r, err := c.CollectCompany(ctx, s.IssuerID, s.CIK)
		if err != nil {
			for _, d := range r.Resources {
				key := rawKey("sec", d.Kind, fmt.Sprintf("cik-%010d", s.CIK), d.Bytes, resourceFetchedAt(d, m.StartedAt), "json")
				if _, putErr := a.storeRaw(ctx, &m, key, d.Bytes, storage.RawMetadata{Source: "sec", ContentType: d.ContentType, FetchedAt: resourceFetchedAt(d, m.StartedAt), Attributes: map[string]string{"issuer_id": s.IssuerID, "cik": fmt.Sprint(s.CIK), "kind": d.Kind}}, fmt.Sprintf("sec/%s/cik-%010d", d.Kind, s.CIK), "sec/"+d.Kind, d.SHA256); putErr != nil {
					errs = append(errs, putErr)
				}
			}
			errs = append(errs, fmt.Errorf("%s: %w", s.IssuerID, err))
			continue
		}
		m.Received += r.RecordsReceived
		m.Rejected += r.RecordsRejected
		rawOK := true
		rawHashes := map[string]string{}
		for _, d := range r.Resources {
			key := rawKey("sec", d.Kind, fmt.Sprintf("cik-%010d", s.CIK), d.Bytes, resourceFetchedAt(d, m.StartedAt), "json")
			storedHash, putErr := a.storeRaw(ctx, &m, key, d.Bytes, storage.RawMetadata{Source: "sec", ContentType: d.ContentType, FetchedAt: resourceFetchedAt(d, m.StartedAt), Attributes: map[string]string{"issuer_id": s.IssuerID, "cik": fmt.Sprint(s.CIK), "kind": d.Kind}}, fmt.Sprintf("sec/%s/cik-%010d", d.Kind, s.CIK), "sec/"+d.Kind, d.SHA256)
			if putErr != nil {
				errs = append(errs, putErr)
				rawOK = false
			} else {
				rawHashes[d.Kind] = storedHash
			}
		}
		if !rawOK {
			continue
		}
		if err := a.metadata.EnrichSECIssuer(ctx, r.Issuer, r.StateOfIncorporation); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := stampFundamentals(run, rawHashes["companyfacts"], r.Facts); err != nil {
			errs = append(errs, err)
			continue
		}
		path, n, err := a.normalized.WriteFundamentals(s.IssuerID, r.Facts)
		if err != nil {
			errs = append(errs, err)
		} else {
			m.OutputRows += n
			m.Cursor["last_issuer_id"] = s.IssuerID
			a.log.Info("normalized dataset", "source", "sec", "issuer_id", s.IssuerID, "path", path, "rows", len(r.Facts), "legal_name", r.Issuer.LegalName, "filings", len(r.Filings))
		}
	}
	if m.Rejected > 0 {
		errs = append(errs, fmt.Errorf("%d records rejected", m.Rejected))
	}
	collectErr := errors.Join(errs...)
	return errors.Join(collectErr, a.finish(ctx, run, m, collectErr, nil, nil))
}

func (a *app) collectPrices(ctx context.Context) error {
	c := yahoo.NewClient(a.http)
	m := metrics{Source: "yahoo", StartedAt: a.nowUTC(), Cursor: map[string]any{}}
	start, err := time.Parse("2006-01-02", a.cfg.Providers.Prices.Start)
	if err != nil {
		return fmt.Errorf("prices.start: %w", err)
	}
	end := a.nowUTC()
	if a.cfg.Providers.Prices.End != "" {
		end, err = time.Parse("2006-01-02", a.cfg.Providers.Prices.End)
		if err != nil {
			return fmt.Errorf("prices.end: %w", err)
		}
	}
	run, skip, runErr := a.start(ctx, &m, pricesRunInputs(a.cfg.Universe, start, end))
	if runErr != nil {
		return runErr
	}
	if skip {
		return nil
	}
	var errs []error
	var snapshots []model.PriceBar
	for _, s := range a.cfg.Universe {
		if s.YahooSymbol == "" {
			m.Rejected++
			continue
		}
		r, err := c.Collect(ctx, model.HistoricalPriceRequest{SecurityID: s.SecurityID, VendorSymbol: s.YahooSymbol, Currency: s.Currency, Start: start, End: end})
		if err != nil {
			for _, resource := range r.Resources {
				key := rawKey("marketdata", "yahoo", s.SecurityID, resource.Bytes, resourceFetchedAt(resource, m.StartedAt), "json")
				if _, putErr := a.storeRaw(ctx, &m, key, resource.Bytes, storage.RawMetadata{Source: "yahoo", ContentType: resource.ContentType, FetchedAt: resourceFetchedAt(resource, m.StartedAt), Attributes: map[string]string{"security_id": s.SecurityID, "vendor_symbol": s.YahooSymbol}}, "yahoo/price/security/"+s.SecurityID+"/vendor/"+s.YahooSymbol, "yahoo", resource.SHA256); putErr != nil {
					errs = append(errs, putErr)
				}
			}
			errs = append(errs, fmt.Errorf("%s: %w", s.SecurityID, err))
			continue
		}
		m.Received += r.RecordsReceived
		m.Rejected += r.RecordsRejected
		if len(r.Resources) != 1 {
			errs = append(errs, fmt.Errorf("yahoo security %s returned %d downloaded resources, want 1", s.SecurityID, len(r.Resources)))
			continue
		}
		resource := r.Resources[0]
		key := rawKey("marketdata", "yahoo", s.SecurityID, resource.Bytes, resourceFetchedAt(resource, m.StartedAt), "json")
		_, putErr := a.storeRaw(ctx, &m, key, resource.Bytes, storage.RawMetadata{Source: "yahoo", ContentType: resource.ContentType, FetchedAt: resourceFetchedAt(resource, m.StartedAt), Attributes: map[string]string{"security_id": s.SecurityID, "vendor_symbol": s.YahooSymbol}}, "yahoo/price/security/"+s.SecurityID+"/vendor/"+s.YahooSymbol, "yahoo", resource.SHA256)
		if putErr != nil {
			errs = append(errs, putErr)
			continue
		}
		if err := stampPrices(run, resource.SHA256, r.Bars); err != nil {
			errs = append(errs, err)
			continue
		}
		path, n, err := a.normalized.WritePrices(s.SecurityID, r.Bars)
		if err != nil {
			errs = append(errs, err)
		} else {
			snapshots = append(snapshots, r.Bars...)
			m.OutputRows += n
			m.Cursor["last_security_id"] = s.SecurityID
			a.log.Info("normalized dataset", "source", "yahoo", "security_id", s.SecurityID, "path", path, "rows", len(r.Bars))
		}
	}
	if m.Rejected > 0 {
		errs = append(errs, fmt.Errorf("%d records rejected", m.Rejected))
	}
	collectErr := errors.Join(errs...)
	return errors.Join(collectErr, a.finish(ctx, run, m, collectErr, snapshots, nil))
}

func (a *app) collectFRED(ctx context.Context) error {
	c := fred.NewClient(a.http)
	m := metrics{Source: "fred", StartedAt: a.nowUTC(), Cursor: map[string]any{}}
	run, skip, err := a.start(ctx, &m, fredRunInputs(a.cfg.Providers.FRED.Series))
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	var errs []error
	var snapshots []model.EconomicObservation
	for _, series := range a.cfg.Providers.FRED.Series {
		r, err := c.Collect(ctx, series)
		if err != nil {
			for _, resource := range r.Resources {
				key := rawKey("fred", "series", series, resource.Bytes, resourceFetchedAt(resource, m.StartedAt), "csv")
				if _, putErr := a.storeRaw(ctx, &m, key, resource.Bytes, storage.RawMetadata{Source: "fred", ContentType: resource.ContentType, FetchedAt: resourceFetchedAt(resource, m.StartedAt), Attributes: map[string]string{"series_id": series, "vintage": "current"}}, "fred/series/"+series+"/vintage/current", "fred", resource.SHA256); putErr != nil {
					errs = append(errs, putErr)
				}
			}
			errs = append(errs, err)
			continue
		}
		m.Received += r.RecordsReceived
		m.Rejected += r.RecordsRejected
		if len(r.Resources) != 1 {
			errs = append(errs, fmt.Errorf("FRED series %s returned %d downloaded resources, want 1", series, len(r.Resources)))
			continue
		}
		resource := r.Resources[0]
		key := rawKey("fred", "series", series, resource.Bytes, resourceFetchedAt(resource, m.StartedAt), "csv")
		_, putErr := a.storeRaw(ctx, &m, key, resource.Bytes, storage.RawMetadata{Source: "fred", ContentType: resource.ContentType, FetchedAt: resourceFetchedAt(resource, m.StartedAt), Attributes: map[string]string{"series_id": series, "vintage": "current"}}, "fred/series/"+series+"/vintage/current", "fred", resource.SHA256)
		if putErr != nil {
			errs = append(errs, putErr)
			continue
		}
		if err := stampEconomics(run, resource.SHA256, r.Observations); err != nil {
			errs = append(errs, err)
			continue
		}
		path, n, err := a.normalized.WriteEconomics(series, r.Observations)
		if err != nil {
			errs = append(errs, err)
		} else {
			snapshots = append(snapshots, r.Observations...)
			m.OutputRows += n
			m.Cursor["last_series_id"] = series
			a.log.Info("normalized dataset", "source", "fred", "series_id", series, "path", path, "rows", len(r.Observations))
		}
	}
	if m.Rejected > 0 {
		errs = append(errs, fmt.Errorf("%d records rejected", m.Rejected))
	}
	collectErr := errors.Join(errs...)
	return errors.Join(collectErr, a.finish(ctx, run, m, collectErr, nil, snapshots))
}

func (a *app) collectALFRED(ctx context.Context) error {
	c := alfred.NewClient(a.http, a.cfg.FREDAPIKey)
	configured := a.cfg.Providers.ALFRED.Series
	seriesCursor := make(map[string]any, len(configured))
	m := metrics{
		Source:    "alfred",
		StartedAt: a.nowUTC(),
		Cursor: map[string]any{
			"provider":         "alfred",
			"series_total":     len(configured),
			"series_completed": 0,
			"series":           seriesCursor,
		},
	}
	run, skip, err := a.start(ctx, &m, alfredRunInputs(configured))
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	var errs []error
	var snapshots []model.EconomicObservation
	for _, item := range configured {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		seriesState := map[string]any{"status": "collecting", "raw_pages": 0}
		seriesCursor[item.ID] = seriesState
		result, collectErr := c.Collect(ctx, alfred.Series{
			ID: item.ID, Geography: item.Geography, Unit: item.Unit,
			Frequency: item.Frequency, SeasonalAdjustment: item.SeasonalAdjustment,
			RealtimeEnd: item.RealtimeEnd, ObservationStart: item.ObservationStart,
			ObservationEnd: item.ObservationEnd,
		})
		m.Received += result.RecordsReceived
		m.Rejected += result.RecordsRejected
		seriesState["records_received"] = result.RecordsReceived
		seriesState["records_rejected"] = result.RecordsRejected
		seriesState["records_missing"] = result.RecordsMissing
		seriesState["raw_pages"] = len(result.Pages)

		storedByAdapterHash := make(map[string]string, len(result.Pages))
		rawOK := true
		if len(result.Resources) != len(result.Pages) {
			errs = append(errs, fmt.Errorf("ALFRED series %s returned %d downloaded resources for %d pages", item.ID, len(result.Resources), len(result.Pages)))
			rawOK = false
		}
		for index, page := range result.Pages {
			if index >= len(result.Resources) {
				continue
			}
			resource := result.Resources[index]
			key := rawKey("alfred", "series", fmt.Sprintf("%s-offset-%d", item.ID, page.Offset), resource.Bytes, resourceFetchedAt(resource, page.FetchedAt), "json")
			storedHash, putErr := a.storeRaw(ctx, &m, key, resource.Bytes, storage.RawMetadata{
				Source: "alfred", ContentType: resource.ContentType, FetchedAt: resourceFetchedAt(resource, page.FetchedAt),
				Attributes: alfredRawAttributes(item, page),
			}, alfredLogicalKey(item, page.Offset), "alfred", resource.SHA256)
			if putErr != nil {
				errs = append(errs, fmt.Errorf("ALFRED series %s raw offset %d: %w", item.ID, page.Offset, putErr))
				rawOK = false
				continue
			}
			storedByAdapterHash[resource.SHA256] = storedHash
		}
		if collectErr != nil {
			seriesState["status"] = "failed"
			seriesState["error"] = collectErr.Error()
			errs = append(errs, collectErr)
			continue
		}
		if !rawOK {
			seriesState["status"] = "failed"
			continue
		}
		if err := stampEconomicsFromRawHashes(run, storedByAdapterHash, result.Observations); err != nil {
			seriesState["status"] = "failed"
			errs = append(errs, fmt.Errorf("ALFRED series %s provenance: %w", item.ID, err))
			continue
		}
		path, n, writeErr := a.normalized.WriteEconomics(item.ID, result.Observations)
		if writeErr != nil {
			seriesState["status"] = "failed"
			errs = append(errs, fmt.Errorf("ALFRED series %s normalize: %w", item.ID, writeErr))
			continue
		}
		snapshots = append(snapshots, result.Observations...)
		m.OutputRows += n
		m.Cursor["last_series_id"] = item.ID
		m.Cursor["series_completed"] = m.Cursor["series_completed"].(int) + 1
		seriesState["status"] = "completed"
		seriesState["output_rows_changed"] = n
		a.log.Info("normalized dataset", "source", "alfred", "series_id", item.ID, "path", path, "rows", len(result.Observations), "raw_pages", len(result.Pages))
	}
	if m.Rejected > 0 {
		errs = append(errs, fmt.Errorf("%d records rejected", m.Rejected))
	}
	collectErr := errors.Join(errs...)
	return errors.Join(collectErr, a.finish(ctx, run, m, collectErr, nil, snapshots))
}

func (a *app) collectBCB(ctx context.Context) error {
	c := bcb.NewClient(a.http)
	configuredSeries := a.cfg.Providers.BCB.Series
	seriesCursor := map[string]any{}
	m := metrics{
		Source:    "bcb",
		StartedAt: a.nowUTC(),
		Cursor: map[string]any{
			"provider":         "bcb",
			"series_total":     len(configuredSeries),
			"series_processed": 0,
			"series_accepted":  0,
			"series":           seriesCursor,
		},
	}
	run, skip, err := a.start(ctx, &m, bcbRunInputs(configuredSeries))
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	var errs []error
	var snapshots []model.EconomicObservation
	processedSeries, acceptedSeries := 0, 0
	for _, configured := range configuredSeries {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}

		code := strings.TrimSpace(configured.Code)
		m.Cursor["last_series_code"] = code
		result, collectErr := c.Collect(ctx, bcb.Series{
			Code:               configured.Code,
			Geography:          configured.Geography,
			Unit:               configured.Unit,
			Frequency:          configured.Frequency,
			SeasonalAdjustment: configured.SeasonalAdjustment,
			Start:              configured.Start,
			End:                configured.End,
		})
		m.Received += result.RecordsReceived
		m.Rejected += result.RecordsRejected
		processedSeries++
		m.Cursor["series_processed"] = processedSeries

		seriesState := map[string]any{
			"code":             code,
			"status":           "fetch_failed",
			"records_received": result.RecordsReceived,
			"records_rejected": result.RecordsRejected,
			"records_missing":  result.RecordsMissing,
			"rows":             len(result.Observations),
		}
		var rawHash, rawKeyValue string
		var rawErr error
		if len(result.Resources) > 1 {
			rawErr = fmt.Errorf("expected one downloaded resource, got %d", len(result.Resources))
		} else if len(result.Resources) == 1 {
			resource := result.Resources[0]
			rawKeyValue = rawKey("bcb", "series", code, resource.Bytes, resourceFetchedAt(resource, m.StartedAt), "csv")
			rawHash, rawErr = a.storeRaw(ctx, &m, rawKeyValue, resource.Bytes, storage.RawMetadata{
				Source:      "bcb",
				ContentType: resource.ContentType,
				FetchedAt:   resourceFetchedAt(resource, m.StartedAt),
				Attributes:  bcbRawAttributes(configured),
			}, bcbLogicalKey(configured), "bcb", resource.SHA256)
			if rawErr == nil {
				seriesState["raw_payload_hash"] = rawHash
				seriesState["raw_object_key"] = rawKeyValue
			}
		}

		if collectErr != nil {
			if rawErr != nil {
				seriesState["status"] = "raw_store_failed"
			} else if len(result.Resources) == 1 {
				seriesState["status"] = "parse_failed"
			}
			seriesCursor[code] = seriesState
			if rawErr != nil {
				errs = append(errs, fmt.Errorf("BCB series %s raw payload: %w", code, rawErr))
			}
			errs = append(errs, fmt.Errorf("BCB series %s: %w", code, collectErr))
			continue
		}
		if rawErr != nil {
			seriesState["status"] = "raw_store_failed"
			seriesCursor[code] = seriesState
			errs = append(errs, fmt.Errorf("BCB series %s raw payload: %w", code, rawErr))
			continue
		}
		if len(result.Resources) != 1 {
			seriesState["status"] = "invalid_result"
			seriesCursor[code] = seriesState
			errs = append(errs, fmt.Errorf("BCB series %s returned no raw payload hash", code))
			continue
		}
		if err := stampEconomics(run, rawHash, result.Observations); err != nil {
			seriesState["status"] = "provenance_failed"
			seriesCursor[code] = seriesState
			errs = append(errs, fmt.Errorf("BCB series %s: %w", code, err))
			continue
		}
		path, n, writeErr := a.normalized.WriteEconomics(code, result.Observations)
		if writeErr != nil {
			seriesState["status"] = "canonical_write_failed"
			seriesCursor[code] = seriesState
			errs = append(errs, fmt.Errorf("BCB series %s: %w", code, writeErr))
			continue
		}
		seriesState["status"] = "accepted"
		seriesState["canonical_path"] = path
		acceptedSeries++
		m.Cursor["series_accepted"] = acceptedSeries
		m.Cursor["last_accepted_series_code"] = code
		seriesCursor[code] = seriesState
		snapshots = append(snapshots, result.Observations...)
		m.OutputRows += n
		m.Cursor["last_series_code"] = code
		a.log.Info("normalized dataset", "source", "bcb", "series_id", code, "path", path, "rows", len(result.Observations))
	}
	if m.Rejected > 0 {
		errs = append(errs, fmt.Errorf("%d records rejected", m.Rejected))
	}
	collectErr := errors.Join(errs...)
	return errors.Join(collectErr, a.finish(ctx, run, m, collectErr, nil, snapshots))
}

func (a *app) collectB3(ctx context.Context) error {
	provider := a.cfg.Providers.B3
	reportDate, err := time.Parse(time.DateOnly, strings.TrimSpace(provider.ReportDate))
	if err != nil {
		return fmt.Errorf("B3 report_date: %w", err)
	}
	securityByTicker := make(map[string]config.Security, len(provider.Tickers))
	for _, security := range a.cfg.Universe {
		securityByTicker[strings.TrimSpace(security.Ticker)] = security
	}
	m := metrics{
		Source:    "b3",
		StartedAt: a.nowUTC(),
		Cursor: map[string]any{
			"provider":                       "b3",
			"report_date":                    reportDate.Format(time.DateOnly),
			"requested_tickers":              append([]string(nil), provider.Tickers...),
			"canonical_publication_policy":   "instrument_identifier_and_listing_only",
			"universe_memberships_published": false,
		},
	}
	run, skip, err := a.start(ctx, &m, b3RunInputs(provider, a.cfg.Universe))
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	client := b3.NewClient(a.http)
	result, collectErr := client.Collect(ctx, b3.Request{ReportDate: reportDate, Tickers: provider.Tickers})
	m.Received += result.RecordsReceived
	m.Rejected += result.RecordsRejected
	m.Cursor["records_received"] = result.RecordsReceived
	m.Cursor["records_rejected"] = result.RecordsRejected
	m.Cursor["resources_returned"] = len(result.Resources)

	var rawHash string
	var rawErr error
	if len(result.Resources) != 1 {
		rawErr = fmt.Errorf("B3 instruments returned %d downloaded resources, want 1", len(result.Resources))
	} else {
		resource := result.Resources[0]
		resourceFetched := resourceFetchedAt(resource, m.StartedAt)
		attributes := make(map[string]string, len(resource.ParserMetadata)+4)
		for key, value := range resource.ParserMetadata {
			attributes[key] = value
		}
		attributes["source_url"] = resource.ParserMetadata["request_url"]
		attributes["parser_version"] = resource.ParserVersion
		attributes["adapter_sha256"] = resource.SHA256
		key := rawKey("b3", "instruments", "report-date-"+reportDate.Format(time.DateOnly), resource.Bytes, resourceFetched, "csv")
		rawHash, rawErr = a.storeRaw(ctx, &m, key, resource.Bytes, storage.RawMetadata{
			Source:      "b3",
			ContentType: resource.ContentType,
			FetchedAt:   resourceFetched,
			Attributes:  attributes,
		}, "b3/instruments/report_date="+reportDate.Format(time.DateOnly)+"/file="+resource.Key, "b3", resource.SHA256)
		if rawErr == nil {
			m.Cursor["raw_payload_hash"] = rawHash
			m.Cursor["raw_object_key"] = key
		}
	}
	if collectErr != nil {
		m.Cursor["status"] = "source_rejected"
		if rawErr != nil {
			collectErr = errors.Join(collectErr, fmt.Errorf("B3 raw payload: %w", rawErr))
		}
		return errors.Join(collectErr, a.finish(ctx, run, m, collectErr, nil, nil))
	}
	if rawErr != nil {
		m.Cursor["status"] = "raw_store_failed"
		collectErr = fmt.Errorf("B3 raw payload: %w", rawErr)
		return errors.Join(collectErr, a.finish(ctx, run, m, collectErr, nil, nil))
	}

	publisher, ok := a.metadata.(historicalTruthPublisher)
	if !ok {
		collectErr = errors.New("B3 canonical publication requires a historical-truth metadata repository")
		m.Cursor["status"] = "canonical_publication_unavailable"
		return errors.Join(collectErr, a.finish(ctx, run, m, collectErr, nil, nil))
	}
	resource := result.Resources[0]
	batch, err := b3HistoricalTruthBatch(run, result.Instruments, securityByTicker, rawHash, resourceFetchedAt(resource, m.StartedAt))
	if err != nil {
		m.Cursor["status"] = "canonical_validation_failed"
		collectErr = err
		return errors.Join(collectErr, a.finish(ctx, run, m, collectErr, nil, nil))
	}
	if err := publisher.PublishHistoricalTruth(ctx, batch); err != nil {
		m.Cursor["status"] = "canonical_publication_failed"
		collectErr = fmt.Errorf("B3 historical truth publication: %w", err)
		return errors.Join(collectErr, a.finish(ctx, run, m, collectErr, nil, nil))
	}
	m.OutputRows = len(batch.Identifiers) + len(batch.Listings)
	m.Cursor["status"] = "canonical_published"
	m.Cursor["identifier_rows"] = len(batch.Identifiers)
	m.Cursor["listing_rows"] = len(batch.Listings)
	m.Cursor["revision_policy"] = "B3 report-date ordinal; source does not expose a correction revision"
	return a.finish(ctx, run, m, nil, nil, nil)
}

func b3HistoricalTruthBatch(run metadata.Run, instruments []b3.Instrument, securityByTicker map[string]config.Security, rawHash string, availableAt time.Time) (metadata.HistoricalTruthBatch, error) {
	availableAt = canonicalTime(availableAt)
	if availableAt.IsZero() {
		return metadata.HistoricalTruthBatch{}, errors.New("B3 historical truth requires a non-zero receipt time")
	}
	batch := metadata.HistoricalTruthBatch{
		Identifiers: make([]metadata.SecurityIdentifierVersion, 0, len(instruments)),
		Listings:    make([]metadata.SecurityListingVersion, 0, len(instruments)),
	}
	for _, instrument := range instruments {
		security, exists := securityByTicker[instrument.Ticker]
		if !exists {
			return metadata.HistoricalTruthBatch{}, fmt.Errorf("B3 ticker %s has no exact configured security mapping", instrument.Ticker)
		}
		if strings.ToUpper(strings.TrimSpace(security.ISIN)) != instrument.ISIN {
			return metadata.HistoricalTruthBatch{}, fmt.Errorf("B3 ticker %s ISIN %s does not match configured ISIN %s", instrument.Ticker, instrument.ISIN, security.ISIN)
		}
		if security.Exchange != "B3" || security.MIC != "BVMF" || security.Currency != "BRL" {
			return metadata.HistoricalTruthBatch{}, fmt.Errorf("B3 ticker %s has an invalid configured B3 listing mapping", instrument.Ticker)
		}
		revision, err := b3ReportRevision(instrument.ReportDate)
		if err != nil {
			return metadata.HistoricalTruthBatch{}, fmt.Errorf("B3 ticker %s: %w", instrument.Ticker, err)
		}
		validFrom := canonicalTime(instrument.TradingStartDate)
		validUntil := instrument.TradingEndDate
		sourceReference := fmt.Sprintf("b3/instruments/report_date=%s/distribution_id=%s/ticker=%s/isin=%s", instrument.ReportDate.Format(time.DateOnly), nonEmptyOr(instrument.DistributionID, "unspecified"), instrument.Ticker, instrument.ISIN)
		identifierID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("b3/identifier/"+run.DataSourceID+"/"+security.SecurityID+"/"+sourceReference)).String()
		listingID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("b3/listing/"+run.DataSourceID+"/"+security.SecurityID+"/"+sourceReference)).String()
		batch.Identifiers = append(batch.Identifiers, metadata.SecurityIdentifierVersion{
			SchemaVersion:   metadata.HistoricalSchemaVersion,
			ID:              identifierID,
			SecurityID:      security.SecurityID,
			IdentifierType:  "ticker",
			Value:           instrument.Ticker,
			NormalizedValue: strings.ToUpper(instrument.Ticker),
			IdentifierScope: "BVMF",
			ValidFrom:       validFrom,
			ValidUntil:      validUntil,
			AvailableAt:     availableAt,
			SourceReference: sourceReference + "/field=TckrSymb",
			RecordedAt:      availableAt,
			DataSourceID:    run.DataSourceID,
			RawPayloadHash:  rawHash,
			Revision:        revision,
			IsPrimary:       true,
		})
		issuerID := security.IssuerID
		batch.Listings = append(batch.Listings, metadata.SecurityListingVersion{
			SchemaVersion:   metadata.HistoricalSchemaVersion,
			ID:              listingID,
			SecurityID:      security.SecurityID,
			IssuerID:        &issuerID,
			Exchange:        security.Exchange,
			MIC:             security.MIC,
			Currency:        instrument.TradingCurrency,
			PrimaryListing:  security.PrimaryListing,
			ValidFrom:       validFrom,
			ValidUntil:      validUntil,
			AvailableAt:     availableAt,
			SourceReference: sourceReference + "/fields=CrpnNm,TradgCcy,TradgStartDt,TradgEndDt",
			RecordedAt:      availableAt,
			DataSourceID:    run.DataSourceID,
			RawPayloadHash:  rawHash,
			Revision:        revision,
		})
	}
	return batch, nil
}

func (a *app) collectB3Calendar(ctx context.Context) error {
	provider := a.cfg.Providers.B3.Calendar
	m := metrics{
		Source: "b3", RunKey: "b3-calendar", StartedAt: a.nowUTC(),
		Cursor: map[string]any{"provider": "b3", "kind": "market_calendar", "year": provider.Year, "historical_fitness": "current_reference_receipt_time"},
	}
	run, skip, err := a.start(ctx, &m, calendarRunInputs("b3", "BVMF", provider))
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	client := b3.NewClient(a.http)
	calendarResult, calendarErr := client.CollectMarketCalendar(ctx, b3.MarketCalendarRequest{Year: provider.Year})
	hoursResult, hoursErr := client.CollectTradingHours(ctx)
	resources := append([]providers.RawResource(nil), calendarResult.Resources...)
	resources = append(resources, hoursResult.Resources...)
	if storeErr := a.storeCalendarResources(ctx, &m, "b3", provider.Year, resources); storeErr != nil {
		collectErr := fmt.Errorf("B3 calendar raw evidence: %w", storeErr)
		return errors.Join(collectErr, a.finish(ctx, run, m, collectErr, nil, nil))
	}
	if collectErr := errors.Join(calendarErr, hoursErr); collectErr != nil {
		return errors.Join(collectErr, a.finish(ctx, run, m, collectErr, nil, nil))
	}
	m.Received = len(calendarResult.Events) + 1
	coverageStart, coverageEnd, err := calendarCoverage(provider, "America/Sao_Paulo")
	if err != nil {
		return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
	}
	evidenceHash, evidenceReference, availableAt, err := a.storeCalendarEvidence(ctx, &m, "b3", "BVMF", provider, resources)
	if err != nil {
		return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
	}
	events := make([]marketcalendar.Event, 0, len(calendarResult.Events))
	for _, event := range calendarResult.Events {
		compiled := marketcalendar.Event{Date: event.Date, Status: "notice", SourceReference: event.RawRecordLocator}
		if event.IsClosed {
			compiled.Status = "closed"
		}
		if event.IsSpecialHours {
			if event.SpecialOpenLocal == "" {
				compileErr := fmt.Errorf("B3 special-hours event %s has no parsed opening time", event.Date.Format(time.DateOnly))
				return errors.Join(compileErr, a.finish(ctx, run, m, compileErr, nil, nil))
			}
			compiled.Status = "open"
			compiled.OpenLocal = event.SpecialOpenLocal
		}
		events = append(events, compiled)
	}
	version := calendarVersion("bvmf", provider.Year, evidenceHash)
	batch, err := marketcalendar.Compile(marketcalendar.Definition{
		DataSourceID: run.DataSourceID, MIC: "BVMF", ExchangeTimezone: "America/Sao_Paulo",
		CalendarVersion: version, CoverageStart: coverageStart, CoverageEnd: coverageEnd,
		RegularOpenLocal: hoursResult.CashEquity.OpenLocal, RegularCloseLocal: hoursResult.CashEquity.CloseLocal,
		AvailableAt: availableAt, RecordedAt: availableAt, SourceReference: evidenceReference,
		RawPayloadHash: evidenceHash, Revision: calendarRevision(availableAt), Events: events,
	})
	if err != nil {
		return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
	}
	if err := a.publishCalendar(ctx, batch); err != nil {
		return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
	}
	m.OutputRows = len(batch.Calendars) + len(batch.Sessions)
	m.Cursor["status"] = "canonical_published"
	m.Cursor["calendar_version"] = version
	m.Cursor["session_fingerprint"] = batch.Calendars[0].SessionFingerprint
	m.Cursor["session_rows"] = len(batch.Sessions)
	return a.finish(ctx, run, m, nil, nil, nil)
}

func (a *app) collectNYSECalendar(ctx context.Context) error {
	provider := a.cfg.Providers.NYSE
	m := metrics{
		Source: "nyse", StartedAt: a.nowUTC(),
		Cursor: map[string]any{"provider": "nyse", "kind": "market_calendar", "year": provider.Year, "historical_fitness": "current_reference_receipt_time"},
	}
	run, skip, err := a.start(ctx, &m, calendarRunInputs("nyse", "XNYS", provider))
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	result, collectErr := nyse.NewClient(a.http).CollectCalendar(ctx, nyse.CalendarRequest{Year: provider.Year})
	if storeErr := a.storeCalendarResources(ctx, &m, "nyse", provider.Year, result.Resources); storeErr != nil {
		collectErr = errors.Join(collectErr, fmt.Errorf("NYSE calendar raw evidence: %w", storeErr))
	}
	if collectErr != nil {
		return errors.Join(collectErr, a.finish(ctx, run, m, collectErr, nil, nil))
	}
	m.Received = len(result.Events)
	coverageStart, coverageEnd, err := calendarCoverage(provider, "America/New_York")
	if err != nil {
		return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
	}
	evidenceHash, evidenceReference, availableAt, err := a.storeCalendarEvidence(ctx, &m, "nyse", "XNYS", provider, result.Resources)
	if err != nil {
		return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
	}
	events := make([]marketcalendar.Event, 0, len(result.Events))
	for _, event := range result.Events {
		compiled := marketcalendar.Event{Date: event.Date, SourceReference: event.RawRecordLocator}
		switch event.Status {
		case "closed":
			compiled.Status = "closed"
		case "early_close":
			compiled.Status = "open"
			compiled.CloseLocal = event.SpecialCloseLocal
		default:
			compileErr := fmt.Errorf("NYSE calendar event %s has unsupported status %q", event.Date.Format(time.DateOnly), event.Status)
			return errors.Join(compileErr, a.finish(ctx, run, m, compileErr, nil, nil))
		}
		events = append(events, compiled)
	}
	version := calendarVersion("xnys", provider.Year, evidenceHash)
	batch, err := marketcalendar.Compile(marketcalendar.Definition{
		DataSourceID: run.DataSourceID, MIC: "XNYS", ExchangeTimezone: "America/New_York",
		CalendarVersion: version, CoverageStart: coverageStart, CoverageEnd: coverageEnd,
		RegularOpenLocal: result.CoreHours.OpenLocal, RegularCloseLocal: result.CoreHours.CloseLocal,
		AvailableAt: availableAt, RecordedAt: availableAt, SourceReference: evidenceReference,
		RawPayloadHash: evidenceHash, Revision: calendarRevision(availableAt), Events: events,
	})
	if err != nil {
		return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
	}
	if err := a.publishCalendar(ctx, batch); err != nil {
		return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
	}
	m.OutputRows = len(batch.Calendars) + len(batch.Sessions)
	m.Cursor["status"] = "canonical_published"
	m.Cursor["calendar_version"] = version
	m.Cursor["session_fingerprint"] = batch.Calendars[0].SessionFingerprint
	m.Cursor["session_rows"] = len(batch.Sessions)
	return a.finish(ctx, run, m, nil, nil, nil)
}

func (a *app) publishCalendar(ctx context.Context, batch metadata.HistoricalTruthBatch) error {
	publisher, ok := a.metadata.(historicalTruthPublisher)
	if !ok {
		return errors.New("canonical calendar publication requires a historical-truth metadata repository")
	}
	if err := publisher.PublishHistoricalTruth(ctx, batch); err != nil {
		return fmt.Errorf("calendar historical truth publication: %w", err)
	}
	return nil
}

func (a *app) storeCalendarResources(ctx context.Context, m *metrics, source string, year int, resources []providers.RawResource) error {
	for _, resource := range resources {
		attributes := make(map[string]string, len(resource.ParserMetadata)+3)
		for key, value := range resource.ParserMetadata {
			attributes[key] = value
		}
		attributes["source_url"] = resource.URL
		attributes["parser_version"] = resource.ParserVersion
		attributes["adapter_sha256"] = resource.SHA256
		extension := strings.TrimPrefix(filepath.Ext(resource.Key), ".")
		if extension == "" {
			extension = "html"
		}
		fetchedAt := resourceFetchedAt(resource, m.StartedAt)
		key := rawKey(source, "calendar", fmt.Sprintf("year-%d-%s", year, resource.Kind), resource.Bytes, fetchedAt, extension)
		logicalKey := fmt.Sprintf("%s/calendar/year=%d/resource=%s", source, year, resource.Kind)
		if _, err := a.storeRaw(ctx, m, key, resource.Bytes, storage.RawMetadata{Source: source, ContentType: resource.ContentType, FetchedAt: fetchedAt, Attributes: attributes}, logicalKey, source+"/calendar/"+resource.Kind, resource.SHA256); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) storeCalendarEvidence(ctx context.Context, m *metrics, source, mic string, provider config.CalendarProvider, resources []providers.RawResource) (string, string, time.Time, error) {
	availableAt := m.StartedAt
	manifest := calendarEvidenceManifest{
		SchemaVersion: "1", Source: source, MIC: mic, Year: provider.Year,
		CoverageStart: provider.CoverageStart, CoverageEnd: provider.CoverageEnd,
		Availability: "receipt_time; source page exposes no historical publication timestamp",
		Revision:     "new retained source-evidence hash creates a new immutable calendar version",
		Weekend:      "Saturday and Sunday are materialized as explicit closed rows",
		Resources:    make([]calendarEvidenceResource, 0, len(resources)),
	}
	for _, resource := range resources {
		fetchedAt := resourceFetchedAt(resource, m.StartedAt)
		if fetchedAt.After(availableAt) {
			availableAt = fetchedAt
		}
		manifest.Resources = append(manifest.Resources, calendarEvidenceResource{
			Kind: resource.Kind, Key: resource.Key, URL: resource.URL, SHA256: resource.SHA256,
			FetchedAt: fetchedAt, ParserVersion: resource.ParserVersion, Metadata: resource.ParserMetadata,
		})
	}
	sort.Slice(manifest.Resources, func(i, j int) bool {
		if manifest.Resources[i].Kind != manifest.Resources[j].Kind {
			return manifest.Resources[i].Kind < manifest.Resources[j].Kind
		}
		return manifest.Resources[i].Key < manifest.Resources[j].Key
	})
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("encode calendar evidence manifest: %w", err)
	}
	hash := providers.SHA256(encoded)
	logicalKey := fmt.Sprintf("%s/calendar-evidence/year=%d/mic=%s", source, provider.Year, mic)
	key := rawKey(source, "calendar-evidence", fmt.Sprintf("year-%d-%s", provider.Year, strings.ToLower(mic)), encoded, availableAt, "json")
	storedHash, err := a.storeRaw(ctx, m, key, encoded, storage.RawMetadata{
		Source: source, ContentType: "application/json", FetchedAt: availableAt,
		Attributes: map[string]string{"mic": mic, "year": strconv.Itoa(provider.Year), "coverage_start": provider.CoverageStart, "coverage_end": provider.CoverageEnd, "historical_fitness": "current_reference_receipt_time"},
	}, logicalKey, source+"/calendar-evidence", hash)
	if err != nil {
		return "", "", time.Time{}, err
	}
	return storedHash, logicalKey + "/sha256=" + storedHash, availableAt, nil
}

func calendarCoverage(provider config.CalendarProvider, timezone string) (time.Time, time.Time, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	parse := func(value string) (time.Time, error) {
		date, err := time.Parse(time.DateOnly, strings.TrimSpace(value))
		if err != nil {
			return time.Time{}, err
		}
		return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, location), nil
	}
	start, err := parse(provider.CoverageStart)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("calendar coverage start: %w", err)
	}
	end, err := parse(provider.CoverageEnd)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("calendar coverage end: %w", err)
	}
	return start, end, nil
}

func calendarVersion(prefix string, year int, evidenceHash string) string {
	return fmt.Sprintf("%s_%d_%s", prefix, year, evidenceHash[:12])
}

func calendarRevision(availableAt time.Time) int {
	return int(availableAt.UTC().Unix() / int64(24*time.Hour/time.Second))
}

func b3ReportRevision(reportDate time.Time) (int, error) {
	epoch := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	date := time.Date(reportDate.UTC().Year(), reportDate.UTC().Month(), reportDate.UTC().Day(), 0, 0, 0, 0, time.UTC)
	if date.Before(epoch) {
		return 0, errors.New("report date precedes revision epoch")
	}
	return int(date.Sub(epoch) / (24 * time.Hour)), nil
}

func nonEmptyOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func (a *app) collectCVM(ctx context.Context) error {
	provider := a.cfg.Providers.CVM
	years := append([]int(nil), provider.IPE.Years...)
	sort.Ints(years)
	c := cvm.NewClient(a.http)
	m := metrics{
		Source:    "cvm",
		StartedAt: a.nowUTC(),
		Cursor: map[string]any{
			"provider":           "cvm",
			"cad_enabled":        provider.CAD,
			"ipe_years":          years,
			"cad_policy":         "current_snapshot_ingestion_only",
			"filing_source":      "cvm_ipe",
			"resources_returned": 0,
		},
	}
	run, skip, err := a.start(ctx, &m, cvmRunInputs(provider, a.cfg.Universe))
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	result, collectErr := c.Collect(ctx, cvm.Request{IncludeCAD: provider.CAD, IPEYears: years})
	m.Received += result.RecordsReceived
	m.Rejected += result.RecordsRejected
	m.Cursor["resources_returned"] = len(result.Resources)
	m.Cursor["records_received"] = result.RecordsReceived
	m.Cursor["records_rejected"] = result.RecordsRejected
	m.Cursor["ipe_rows_returned"] = len(result.IPE)
	m.Cursor["cad_rows_returned"] = len(result.CAD)

	resourceHashes, rawErr := a.storeCVMResources(ctx, &m, result.Resources)
	if rawErr != nil {
		collectErr = errors.Join(collectErr, rawErr)
	}
	if collectErr != nil {
		m.Cursor["canonical_publication"] = "skipped_due_to_source_or_raw_error"
		if provider.CAD {
			a.log.Info("CVM CAD current snapshot retained as raw evidence; canonical publication skipped", "rows", len(result.CAD), "policy", "ingestion_only")
		}
		return errors.Join(collectErr, a.finish(ctx, run, m, collectErr, nil, nil))
	}

	var errs []error
	if provider.CAD {
		// The current CAD extract is not versioned issuer history. There is no
		// honest latest-only CAD projection in the existing metadata boundary,
		// so retain its raw evidence and make the non-publication explicit.
		m.Rejected += len(result.CAD)
		m.Cursor["cad_status"] = "ingestion_only_not_published"
		m.Cursor["cad_rows_not_published"] = len(result.CAD)
		a.log.Info("CVM CAD current snapshot retained as raw evidence; canonical publication skipped", "rows", len(result.CAD), "policy", "ingestion_only")
		errs = append(errs, fmt.Errorf("CVM CAD current snapshot is ingestion-only; %d rows were not published", len(result.CAD)))
	}

	byCode, ambiguousCodes := cvmIssuerMappings(a.cfg.Universe)
	byIssuer := make(map[string][]model.Filing)
	matched, unconfigured, ambiguous := 0, 0, 0
	for _, row := range result.IPE {
		if ambiguousCodes[row.CVMCode] {
			ambiguous++
			continue
		}
		security, ok := byCode[row.CVMCode]
		if !ok {
			// The IPE archive is global, so rows outside the configured
			// universe are expected and intentionally ignored. Only an exact
			// configured CVM-code mapping may select an issuer.
			unconfigured++
			continue
		}
		rawHash, hashErr := cvmRawHashForRow(row, resourceHashes)
		if hashErr != nil {
			m.Rejected++
			errs = append(errs, hashErr)
			continue
		}
		filing, filingErr := cvmFiling(security.IssuerID, row, rawHash, run)
		if filingErr != nil {
			m.Rejected++
			errs = append(errs, fmt.Errorf("CVM IPE %s: %w", row.SourceDocumentID, filingErr))
			continue
		}
		byIssuer[security.IssuerID] = append(byIssuer[security.IssuerID], filing)
		matched++
	}
	if ambiguous > 0 {
		m.Rejected += ambiguous
		errs = append(errs, fmt.Errorf("%d CVM IPE rows had ambiguous duplicate configured CVM-code mappings", ambiguous))
	}
	m.Cursor["ipe_rows_matched"] = matched
	m.Cursor["ipe_rows_unconfigured"] = unconfigured
	m.Cursor["ipe_rows_ignored"] = unconfigured
	m.Cursor["ipe_rows_ambiguous"] = ambiguous
	m.Cursor["issuers_with_filings"] = len(byIssuer)

	issuerIDs := make([]string, 0, len(byIssuer))
	for issuerID := range byIssuer {
		issuerIDs = append(issuerIDs, issuerID)
	}
	sort.Strings(issuerIDs)
	for _, issuerID := range issuerIDs {
		filings := byIssuer[issuerID]
		path, n, writeErr := a.normalized.WriteFilings(issuerID, filings)
		if writeErr != nil {
			errs = append(errs, fmt.Errorf("CVM IPE issuer %s: %w", issuerID, writeErr))
			continue
		}
		m.OutputRows += n
		m.Cursor["last_issuer_id"] = issuerID
		a.log.Info("normalized dataset", "source", "cvm_ipe", "issuer_id", issuerID, "path", path, "rows", len(filings), "rows_changed", n)
	}
	if result.RecordsRejected > 0 {
		errs = append(errs, fmt.Errorf("%d CVM source records rejected", result.RecordsRejected))
	}
	collectErr = errors.Join(errs...)
	return errors.Join(collectErr, a.finish(ctx, run, m, collectErr, nil, nil))
}

func (a *app) storeCVMResources(ctx context.Context, m *metrics, resources []cvm.RawResource) (map[string]string, error) {
	hashes := make(map[string]string, len(resources))
	var errs []error
	for _, resource := range resources {
		attributes := cvmRawAttributes(resource)
		objectKey := rawKey("cvm", string(resource.Kind), resource.Key, resource.Bytes, m.StartedAt, cvmResourceExtension(resource))
		storedHash, err := a.storeRaw(ctx, m, objectKey, resource.Bytes, storage.RawMetadata{
			Source:      "cvm",
			ContentType: resource.ContentType,
			FetchedAt:   m.StartedAt,
			Attributes:  attributes,
		}, cvmLogicalKey(resource), "cvm", resource.SHA256)
		if err != nil {
			errs = append(errs, fmt.Errorf("CVM resource %s: %w", resource.Key, err))
			continue
		}
		hashes[resource.Key] = storedHash
	}
	return hashes, errors.Join(errs...)
}

func cvmRawAttributes(resource cvm.RawResource) map[string]string {
	attributes := map[string]string{
		"resource_key":   resource.Key,
		"resource_kind":  string(resource.Kind),
		"source_url":     resource.URL,
		"parser_version": resource.ParserVersion,
		"adapter_sha256": resource.SHA256,
		"content_type":   resource.ContentType,
	}
	if resource.Year != 0 {
		attributes["year"] = strconv.Itoa(resource.Year)
	}
	for key, value := range resource.ParserMetadata {
		attributes[key] = value
	}
	return attributes
}

func cvmLogicalKey(resource cvm.RawResource) string {
	return resource.Key
}

func cvmResourceExtension(resource cvm.RawResource) string {
	switch resource.Kind {
	case cvm.ResourceCAD:
		return "csv"
	case cvm.ResourceIPEMetadata:
		return "txt"
	case cvm.ResourceIPEArchive:
		return "zip"
	default:
		return "bin"
	}
}

func cvmIssuerMappings(universe []config.Security) (map[string]config.Security, map[string]bool) {
	byCode := make(map[string]config.Security)
	ambiguous := make(map[string]bool)
	for _, security := range universe {
		code := strings.TrimSpace(security.CVMCode)
		if code == "" {
			continue
		}
		if ambiguous[code] {
			continue
		}
		if _, exists := byCode[code]; exists {
			delete(byCode, code)
			ambiguous[code] = true
			continue
		}
		byCode[code] = security
	}
	return byCode, ambiguous
}

func cvmRunInputs(provider config.CVMProvider, universe []config.Security) metadata.RunInputs {
	years := append([]int(nil), provider.IPE.Years...)
	sort.Ints(years)
	resources := make([]string, 0, len(years)+1)
	if provider.CAD {
		resources = append(resources, "cad")
	}
	for _, year := range years {
		resources = append(resources, fmt.Sprintf("ipe:%04d", year))
	}
	requests := make([]metadata.IssuerRequest, 0, len(universe))
	for _, security := range universe {
		requestResources := append([]string(nil), resources...)
		requestResources = append(requestResources, "cvm_code:"+strings.TrimSpace(security.CVMCode))
		requests = append(requests, metadata.IssuerRequest{
			IssuerID:   security.IssuerID,
			SecurityID: security.SecurityID,
			CIK:        security.CIK,
			Resources:  requestResources,
		})
	}
	return metadata.RunInputs{
		SchemaVersion: metadata.RunInputsSchemaVersion,
		Source:        "cvm",
		Provider: metadata.ProviderInputs{
			Name:                    "cvm",
			Kind:                    "filings",
			ConfiguredUniverseCount: len(universe),
			IssuerRequests:          requests,
			Format:                  "cad_csv+ipe_metadata_zip",
			Vintage:                 "current",
		},
	}
}

func cvmRawHashForRow(row cvm.IPERow, resourceHashes map[string]string) (string, error) {
	prefix := "zip/year="
	if !strings.HasPrefix(row.RawRecordLocator, prefix) {
		return "", fmt.Errorf("CVM IPE %s has invalid raw record locator %q", row.SourceDocumentID, row.RawRecordLocator)
	}
	rest := strings.TrimPrefix(row.RawRecordLocator, prefix)
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 {
		return "", fmt.Errorf("CVM IPE %s has invalid raw record locator %q", row.SourceDocumentID, row.RawRecordLocator)
	}
	year, err := strconv.Atoi(rest[:slash])
	if err != nil {
		return "", fmt.Errorf("CVM IPE %s has invalid raw record locator %q: %w", row.SourceDocumentID, row.RawRecordLocator, err)
	}
	key := fmt.Sprintf("cvm/ipe/year=%04d", year)
	hash := resourceHashes[key]
	if hash == "" {
		return "", fmt.Errorf("CVM IPE %s has no stored raw resource for %s", row.SourceDocumentID, key)
	}
	return hash, nil
}

func cvmFiling(issuerID string, row cvm.IPERow, rawHash string, run metadata.Run) (model.Filing, error) {
	if strings.TrimSpace(row.DownloadURL) == "" {
		return model.Filing{}, errors.New("document URL is required for canonical publication")
	}
	ingestedAt := canonicalTime(row.IngestedAt)
	temporal := model.Temporal{
		AvailableAt:        canonicalTime(row.AvailableAt),
		IngestedAt:         ingestedAt,
		PublishedPrecision: model.PrecisionUnknown,
		ObservedPrecision:  model.TimePrecision(row.ObservedPrecision),
	}
	if row.ObservedAt != nil {
		temporal.ObservedAt = canonicalTime(*row.ObservedAt)
	}
	if temporal.ObservedPrecision == "" {
		temporal.ObservedPrecision = model.PrecisionUnknown
	}
	filing := model.Filing{
		ID:               uuid.NewSHA1(uuid.NameSpaceURL, []byte("cvm_ipe:"+issuerID+":"+row.SourceDocumentID)).String(),
		Source:           "cvm_ipe",
		IssuerID:         issuerID,
		SourceDocumentID: row.SourceDocumentID,
		DocumentURL:      row.DownloadURL,
		AccessionNumber:  row.AccessionNumber,
		FormType:         row.FormType,
		Category:         row.Category,
		DocumentType:     row.Type,
		Species:          row.Species,
		Subject:          row.Subject,
		PresentationType: row.PresentationType,
		FilingDate:       canonicalTime(row.DeliveryDate),
		PeriodEnd:        row.ReferenceDate,
		Temporal:         temporal,
		RawPayloadHash:   rawHash,
		Provenance:       model.Provenance{RawPayloadHash: rawHash, RawRecordLocator: row.RawRecordLocator, IngestedAt: ingestedAt, NormalizerVersion: model.NormalizerVersion},
	}
	if err := stampProvenance(run, rawHash, &filing.RawPayloadHash, &filing.Provenance, filing.Temporal); err != nil {
		return model.Filing{}, err
	}
	if err := filing.Validate(); err != nil {
		return model.Filing{}, err
	}
	return filing, nil
}

func bcbRawAttributes(series config.BCBSeries) map[string]string {
	return map[string]string{
		"provider_format":     "sgs-csv",
		"series_code":         strings.TrimSpace(series.Code),
		"geography":           strings.TrimSpace(series.Geography),
		"unit":                strings.TrimSpace(series.Unit),
		"frequency":           strings.TrimSpace(series.Frequency),
		"seasonal_adjustment": strings.TrimSpace(series.SeasonalAdjustment),
		"start":               strings.TrimSpace(series.Start),
		"end":                 strings.TrimSpace(series.End),
		"vintage":             "current",
	}
}

func alfredRawAttributes(series config.ALFREDSeries, page alfred.RawPage) map[string]string {
	return map[string]string{
		"provider_format":     "fred-json",
		"series_id":           strings.TrimSpace(series.ID),
		"geography":           strings.TrimSpace(series.Geography),
		"unit":                strings.TrimSpace(series.Unit),
		"frequency":           strings.TrimSpace(series.Frequency),
		"seasonal_adjustment": strings.TrimSpace(series.SeasonalAdjustment),
		"realtime_start":      alfred.EarliestRealtimeStart,
		"realtime_end":        strings.TrimSpace(series.RealtimeEnd),
		"observation_start":   strings.TrimSpace(series.ObservationStart),
		"observation_end":     strings.TrimSpace(series.ObservationEnd),
		"output_type":         strconv.Itoa(alfred.OutputType),
		"page_size":           strconv.Itoa(alfred.PageLimit),
		"offset":              strconv.Itoa(page.Offset),
		"response_count":      strconv.Itoa(page.Count),
		"response_limit":      strconv.Itoa(page.Limit),
		"vintage":             "historical_realtime_periods",
	}
}

func alfredLogicalKey(series config.ALFREDSeries, offset int) string {
	observationStart := strings.TrimSpace(series.ObservationStart)
	if observationStart == "" {
		observationStart = "beginning"
	}
	observationEnd := strings.TrimSpace(series.ObservationEnd)
	if observationEnd == "" {
		observationEnd = "latest"
	}
	return fmt.Sprintf(
		"alfred/series/%s/realtime/%s/%s/observations/%s/%s/output-type/%d/offset/%d",
		strings.TrimSpace(series.ID), alfred.EarliestRealtimeStart, strings.TrimSpace(series.RealtimeEnd),
		observationStart, observationEnd, alfred.OutputType, offset,
	)
}

func bcbLogicalKey(series config.BCBSeries) string {
	start, end := strings.TrimSpace(series.Start), strings.TrimSpace(series.End)
	if start == "" {
		start = "beginning"
	}
	if end == "" {
		end = "current"
	}
	return "bcb/series/" + strings.TrimSpace(series.Code) + "/start/" + start + "/end/" + end
}

func secRunInputs(universe []config.Security) metadata.RunInputs {
	requests := make([]metadata.IssuerRequest, 0, len(universe))
	for _, security := range universe {
		requests = append(requests, metadata.IssuerRequest{
			IssuerID:   security.IssuerID,
			SecurityID: security.SecurityID,
			CIK:        security.CIK,
			Resources:  []string{"submissions", "companyfacts"},
		})
	}
	return metadata.RunInputs{
		SchemaVersion: metadata.RunInputsSchemaVersion,
		Source:        "sec",
		Provider: metadata.ProviderInputs{
			Name:                    "sec",
			Kind:                    "fundamentals",
			ConfiguredUniverseCount: len(universe),
			IssuerRequests:          requests,
		},
	}
}

func pricesRunInputs(universe []config.Security, start, end time.Time) metadata.RunInputs {
	requests := make([]metadata.SecurityRequest, 0, len(universe))
	for _, security := range universe {
		requests = append(requests, metadata.SecurityRequest{
			SecurityID:   security.SecurityID,
			VendorSymbol: security.YahooSymbol,
			Currency:     security.Currency,
			Start:        start.UTC().Format("2006-01-02"),
			End:          end.UTC().Format("2006-01-02"),
			Interval:     "1d",
			Events:       "history",
		})
	}
	return metadata.RunInputs{
		SchemaVersion: metadata.RunInputsSchemaVersion,
		Source:        "yahoo",
		Provider: metadata.ProviderInputs{
			Name:                    "yahoo",
			Kind:                    "market_data",
			ConfiguredUniverseCount: len(universe),
			SecurityRequests:        requests,
		},
	}
}

func fredRunInputs(series []string) metadata.RunInputs {
	seriesIDs := make([]string, 0, len(series))
	for _, seriesID := range series {
		seriesIDs = append(seriesIDs, strings.TrimSpace(seriesID))
	}
	return metadata.RunInputs{
		SchemaVersion: metadata.RunInputsSchemaVersion,
		Source:        "fred",
		Provider: metadata.ProviderInputs{
			Name:                  "fred",
			Kind:                  "macro",
			ConfiguredSeriesCount: len(seriesIDs),
			SeriesIDs:             seriesIDs,
			Format:                "csv",
			Vintage:               "current",
		},
	}
}

func alfredRunInputs(series []config.ALFREDSeries) metadata.RunInputs {
	configured := make([]metadata.ALFREDSeriesInput, 0, len(series))
	for _, item := range series {
		configured = append(configured, metadata.ALFREDSeriesInput{
			ID: strings.TrimSpace(item.ID), Geography: strings.TrimSpace(item.Geography),
			Unit: strings.TrimSpace(item.Unit), Frequency: strings.TrimSpace(item.Frequency),
			SeasonalAdjustment: strings.TrimSpace(item.SeasonalAdjustment),
			RealtimeStart:      alfred.EarliestRealtimeStart, RealtimeEnd: strings.TrimSpace(item.RealtimeEnd),
			ObservationStart: strings.TrimSpace(item.ObservationStart), ObservationEnd: strings.TrimSpace(item.ObservationEnd),
		})
	}
	return metadata.RunInputs{
		SchemaVersion: metadata.RunInputsSchemaVersion,
		Source:        "alfred",
		Provider: metadata.ProviderInputs{
			Name: "alfred", Kind: "macro", ConfiguredSeriesCount: len(configured),
			HistoricalSeries: configured, Format: "json", Vintage: "historical_realtime_periods",
			PageSize: alfred.PageLimit, OutputType: alfred.OutputType,
		},
	}
}

func bcbRunInputs(series []config.BCBSeries) metadata.RunInputs {
	configured := make([]metadata.BCBSeriesInput, 0, len(series))
	for _, item := range series {
		configured = append(configured, metadata.BCBSeriesInput{
			Code:               strings.TrimSpace(item.Code),
			Geography:          strings.TrimSpace(item.Geography),
			Unit:               strings.TrimSpace(item.Unit),
			Frequency:          strings.TrimSpace(item.Frequency),
			SeasonalAdjustment: strings.TrimSpace(item.SeasonalAdjustment),
			Start:              strings.TrimSpace(item.Start),
			End:                strings.TrimSpace(item.End),
		})
	}
	return metadata.RunInputs{
		SchemaVersion: metadata.RunInputsSchemaVersion,
		Source:        "bcb",
		Provider: metadata.ProviderInputs{
			Name:                  "bcb",
			Kind:                  "macro",
			ConfiguredSeriesCount: len(configured),
			Series:                configured,
			Format:                "csv",
			Vintage:               "current",
		},
	}
}

func b3RunInputs(provider config.B3Provider, universe []config.Security) metadata.RunInputs {
	byTicker := make(map[string]config.Security, len(universe))
	for _, security := range universe {
		byTicker[strings.TrimSpace(security.Ticker)] = security
	}
	tickers := append([]string(nil), provider.Tickers...)
	sort.Strings(tickers)
	instruments := make([]metadata.B3InstrumentInput, 0, len(tickers))
	for _, ticker := range tickers {
		security := byTicker[strings.TrimSpace(ticker)]
		instruments = append(instruments, metadata.B3InstrumentInput{
			SecurityID: security.SecurityID,
			Ticker:     strings.TrimSpace(ticker),
			ISIN:       strings.TrimSpace(security.ISIN),
		})
	}
	return metadata.RunInputs{
		SchemaVersion: metadata.RunInputsSchemaVersion,
		Source:        "b3",
		Provider: metadata.ProviderInputs{
			Name:                    "b3",
			Kind:                    "security_master",
			ConfiguredUniverseCount: len(instruments),
			B3ReportDate:            strings.TrimSpace(provider.ReportDate),
			B3Instruments:           instruments,
			Format:                  "instruments_consolidated_csv",
			Vintage:                 "report_date_snapshot",
		},
	}
}

func calendarRunInputs(source, mic string, provider config.CalendarProvider) metadata.RunInputs {
	return metadata.RunInputs{
		SchemaVersion: metadata.RunInputsSchemaVersion,
		Source:        source,
		Provider: metadata.ProviderInputs{
			Name: source, Kind: "market_calendar", CalendarYear: provider.Year,
			CalendarMIC: mic, CalendarCoverageStart: strings.TrimSpace(provider.CoverageStart),
			CalendarCoverageEnd: strings.TrimSpace(provider.CoverageEnd), Format: "html",
			Vintage: "current_reference_receipt_time",
		},
	}
}

func (a *app) start(ctx context.Context, m *metrics, inputs metadata.RunInputs) (metadata.Run, bool, error) {
	if a.metadata == nil {
		return metadata.Run{}, false, errors.New("PostgreSQL metadata repository is required for canonical collection")
	}
	m.StartedAt = canonicalTime(m.StartedAt)
	runKeyPart := m.Source
	if strings.TrimSpace(m.RunKey) != "" {
		runKeyPart = strings.TrimSpace(m.RunKey)
	}
	run, err := a.metadata.StartRun(ctx, m.Source, a.batchKey+"/"+runKeyPart, m.StartedAt, inputs)
	if err != nil {
		return metadata.Run{}, false, err
	}
	if err := validateRunLineage(run); err != nil {
		return metadata.Run{}, false, err
	}
	if run.Skip {
		a.log.Info("collector run already terminal; skipping", "source", m.Source, "run_key", run.RunKey, "status", run.Status)
		return run, true, nil
	}
	m.Manifest = storage.NewRawManifest(m.Source, run.ID)
	return run, false, nil
}

func validateRunLineage(run metadata.Run) error {
	if _, err := uuid.Parse(run.DataSourceID); err != nil {
		return fmt.Errorf("run data_source_id must be UUID: %w", err)
	}
	if _, err := uuid.Parse(run.ID); err != nil {
		return fmt.Errorf("run ID must be UUID: %w", err)
	}
	return nil
}

func validateStoredHash(source, expected, stored string) error {
	if expected == "" {
		return fmt.Errorf("%s adapter returned an empty raw SHA-256", source)
	}
	if expected != stored {
		return fmt.Errorf("%s raw SHA-256 mismatch: adapter=%s stored=%s", source, expected, stored)
	}
	return nil
}

func resourceFetchedAt(resource providers.RawResource, fallback time.Time) time.Time {
	if resource.FetchedAt.IsZero() {
		return canonicalTime(fallback)
	}
	return canonicalTime(resource.FetchedAt)
}

func (a *app) storeRaw(ctx context.Context, m *metrics, key string, data []byte, meta storage.RawMetadata, logicalKey, source, expectedHash string) (string, error) {
	stored, err := a.raw.Put(ctx, key, bytes.NewReader(data), meta)
	if err != nil {
		return "", err
	}
	m.RawObjects++
	m.RawBytes += stored.Size
	if err := m.Manifest.AddRawManifestEntry(logicalKey, key, stored); err != nil {
		return "", err
	}
	if err := validateStoredHash(source, expectedHash, stored.SHA256); err != nil {
		return "", err
	}
	return stored.SHA256, nil
}

func stampProvenance(run metadata.Run, rawHash string, topLevelHash *string, p *model.Provenance, temporal model.Temporal) error {
	if rawHash == "" || *topLevelHash != rawHash || p.RawPayloadHash != rawHash {
		return errors.New("adapter/raw-store payload hash mismatch")
	}
	if p.RawRecordLocator == "" {
		return errors.New("adapter raw record locator is required")
	}
	if temporal.IngestedAt.IsZero() {
		return errors.New("temporal ingested_at is required")
	}
	p.DataSourceID = run.DataSourceID
	p.IngestionRunID = run.ID
	p.RawPayloadHash = rawHash
	p.IngestedAt = temporal.IngestedAt
	return nil
}

func stampPrices(run metadata.Run, rawHash string, observations []model.PriceBar) error {
	for i := range observations {
		if err := stampProvenance(run, rawHash, &observations[i].RawPayloadHash, &observations[i].Provenance, observations[i].Temporal); err != nil {
			return fmt.Errorf("price observation %d: %w", i, err)
		}
	}
	return nil
}

func stampFundamentals(run metadata.Run, rawHash string, observations []model.FundamentalObservation) error {
	for i := range observations {
		if err := stampProvenance(run, rawHash, &observations[i].RawPayloadHash, &observations[i].Provenance, observations[i].Temporal); err != nil {
			return fmt.Errorf("fundamental observation %d: %w", i, err)
		}
	}
	return nil
}

func stampEconomics(run metadata.Run, rawHash string, observations []model.EconomicObservation) error {
	for i := range observations {
		if err := stampProvenance(run, rawHash, &observations[i].RawPayloadHash, &observations[i].Provenance, observations[i].Temporal); err != nil {
			return fmt.Errorf("economic observation %d: %w", i, err)
		}
	}
	return nil
}

func stampEconomicsFromRawHashes(run metadata.Run, storedByAdapterHash map[string]string, observations []model.EconomicObservation) error {
	for i := range observations {
		adapterHash := observations[i].RawPayloadHash
		storedHash := storedByAdapterHash[adapterHash]
		if storedHash == "" {
			return fmt.Errorf("economic observation %d has no stored raw page for %s", i, adapterHash)
		}
		if err := stampProvenance(run, storedHash, &observations[i].RawPayloadHash, &observations[i].Provenance, observations[i].Temporal); err != nil {
			return fmt.Errorf("economic observation %d: %w", i, err)
		}
	}
	return nil
}

func (a *app) finish(ctx context.Context, run metadata.Run, m metrics, err error, prices []model.PriceBar, macros []model.EconomicObservation) error {
	m.StartedAt = canonicalTime(m.StartedAt)
	m.Duration = time.Since(m.StartedAt)
	status := "success"
	if errors.Is(err, context.Canceled) {
		status = "cancelled"
	} else if err != nil {
		if m.OutputRows > 0 || m.RawObjects > 0 {
			status = "partial"
		} else {
			status = "failed"
		}
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	manifestHash, manifestKey, manifestErr := storage.PublishRawManifest(finishCtx, a.raw, m.Manifest, m.StartedAt)
	if manifestErr != nil {
		a.log.Error("publish raw run manifest failed", "source", m.Source, "error", manifestErr)
		return manifestErr
	}
	if dbErr := a.metadata.FinalizeRun(finishCtx, run, a.nowUTC(), metadata.Metrics{Received: int64(m.Received), Written: int64(m.OutputRows), Rejected: int64(m.Rejected), RawPayloads: int64(m.RawObjects), RawBytes: m.RawBytes, RawPayloadManifestHash: manifestHash, Cursor: m.Cursor, Err: err}, prices, macros); dbErr != nil {
		a.log.Error("persist collector run failed", "source", m.Source, "error", dbErr)
		return dbErr
	}
	a.log.Info("collector run", "source", m.Source, "status", status, "started_at", m.StartedAt, "duration_seconds", m.Duration.Seconds(), "records_received", m.Received, "output_rows_changed", m.OutputRows, "records_rejected", m.Rejected, "raw_objects", m.RawObjects, "raw_manifest_key", manifestKey, "raw_manifest_hash", manifestHash)
	return nil
}
func rawKey(parts1, parts2, entity string, b []byte, at time.Time, ext string) string {
	h := sha256.Sum256(b)
	hash := hex.EncodeToString(h[:])
	clean := func(v string) string {
		return strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
				return r
			}
			return '-'
		}, v)
	}
	return filepath.Join(clean(parts1), clean(parts2), at.UTC().Format("2006/01/02"), clean(entity), hash+"."+ext)
}
