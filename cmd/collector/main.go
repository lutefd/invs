package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/luisdourado/invs/config"
	"github.com/luisdourado/invs/internal/httpx"
	"github.com/luisdourado/invs/internal/metadata"
	"github.com/luisdourado/invs/internal/model"
	"github.com/luisdourado/invs/internal/normalize"
	"github.com/luisdourado/invs/internal/providers/bcb"
	"github.com/luisdourado/invs/internal/providers/fred"
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
	StartRun(context.Context, string, string, time.Time) (metadata.Run, error)
	FinalizeRun(context.Context, metadata.Run, time.Time, metadata.Metrics, []model.PriceBar, []model.EconomicObservation) error
}

type normalizedStore interface {
	WritePrices(string, []model.PriceBar) (string, int, error)
	WriteFundamentals(string, []model.FundamentalObservation) (string, int, error)
	WriteEconomics(string, []model.EconomicObservation) (string, int, error)
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
	source := flag.String("source", "all", "collector source: all, sec, prices, fred, or bcb")
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
	valid := map[string]bool{"all": true, "sec": true, "prices": true, "fred": true, "bcb": true}
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
	if source == "bcb" && !a.cfg.Providers.BCB.Enabled {
		return errors.New("BCB provider is disabled")
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
	if (source == "all" || source == "bcb") && a.cfg.Providers.BCB.Enabled {
		if err := a.collectBCB(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (a *app) collectSEC(ctx context.Context) error {
	c := sec.NewClient(a.http)
	m := metrics{Source: "sec", StartedAt: a.nowUTC(), Cursor: map[string]any{}}
	run, skip, err := a.start(ctx, &m)
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
			for _, d := range r.Raw {
				key := rawKey("sec", d.Kind, fmt.Sprintf("cik-%010d", s.CIK), d.Data, m.StartedAt, "json")
				if _, putErr := a.storeRaw(ctx, &m, key, d.Data, storage.RawMetadata{Source: "sec", ContentType: "application/json", FetchedAt: m.StartedAt, Attributes: map[string]string{"issuer_id": s.IssuerID, "cik": fmt.Sprint(s.CIK), "kind": d.Kind}}, fmt.Sprintf("sec/%s/cik-%010d", d.Kind, s.CIK), "sec/"+d.Kind, d.SHA256); putErr != nil {
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
		for _, d := range r.Raw {
			key := rawKey("sec", d.Kind, fmt.Sprintf("cik-%010d", s.CIK), d.Data, m.StartedAt, "json")
			storedHash, putErr := a.storeRaw(ctx, &m, key, d.Data, storage.RawMetadata{Source: "sec", ContentType: "application/json", FetchedAt: m.StartedAt, Attributes: map[string]string{"issuer_id": s.IssuerID, "cik": fmt.Sprint(s.CIK), "kind": d.Kind}}, fmt.Sprintf("sec/%s/cik-%010d", d.Kind, s.CIK), "sec/"+d.Kind, d.SHA256)
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
	run, skip, runErr := a.start(ctx, &m)
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
			if len(r.Raw) > 0 {
				key := rawKey("marketdata", "yahoo", s.SecurityID, r.Raw, m.StartedAt, "json")
				if _, putErr := a.storeRaw(ctx, &m, key, r.Raw, storage.RawMetadata{Source: "yahoo", ContentType: "application/json", FetchedAt: m.StartedAt, Attributes: map[string]string{"security_id": s.SecurityID, "vendor_symbol": s.YahooSymbol}}, "yahoo/price/security/"+s.SecurityID+"/vendor/"+s.YahooSymbol, "yahoo", r.SHA256); putErr != nil {
					errs = append(errs, putErr)
				}
			}
			errs = append(errs, fmt.Errorf("%s: %w", s.SecurityID, err))
			continue
		}
		m.Received += r.RecordsReceived
		m.Rejected += r.RecordsRejected
		key := rawKey("marketdata", "yahoo", s.SecurityID, r.Raw, m.StartedAt, "json")
		_, putErr := a.storeRaw(ctx, &m, key, r.Raw, storage.RawMetadata{Source: "yahoo", ContentType: "application/json", FetchedAt: m.StartedAt, Attributes: map[string]string{"security_id": s.SecurityID, "vendor_symbol": s.YahooSymbol}}, "yahoo/price/security/"+s.SecurityID+"/vendor/"+s.YahooSymbol, "yahoo", r.SHA256)
		if putErr != nil {
			errs = append(errs, putErr)
			continue
		}
		if err := stampPrices(run, r.SHA256, r.Bars); err != nil {
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
	run, skip, err := a.start(ctx, &m)
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
			if len(r.Raw) > 0 {
				key := rawKey("fred", "series", series, r.Raw, m.StartedAt, "csv")
				if _, putErr := a.storeRaw(ctx, &m, key, r.Raw, storage.RawMetadata{Source: "fred", ContentType: "text/csv", FetchedAt: m.StartedAt, Attributes: map[string]string{"series_id": series, "vintage": "current"}}, "fred/series/"+series+"/vintage/current", "fred", r.SHA256); putErr != nil {
					errs = append(errs, putErr)
				}
			}
			errs = append(errs, err)
			continue
		}
		m.Received += r.RecordsReceived
		m.Rejected += r.RecordsRejected
		key := rawKey("fred", "series", series, r.Raw, m.StartedAt, "csv")
		_, putErr := a.storeRaw(ctx, &m, key, r.Raw, storage.RawMetadata{Source: "fred", ContentType: "text/csv", FetchedAt: m.StartedAt, Attributes: map[string]string{"series_id": series, "vintage": "current"}}, "fred/series/"+series+"/vintage/current", "fred", r.SHA256)
		if putErr != nil {
			errs = append(errs, putErr)
			continue
		}
		if err := stampEconomics(run, r.SHA256, r.Observations); err != nil {
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
	run, skip, err := a.start(ctx, &m)
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
		if result.SHA256 != "" {
			rawKeyValue = rawKey("bcb", "series", code, result.Raw, m.StartedAt, "csv")
			rawHash, rawErr = a.storeRaw(ctx, &m, rawKeyValue, result.Raw, storage.RawMetadata{
				Source:      "bcb",
				ContentType: "text/csv",
				FetchedAt:   m.StartedAt,
				Attributes:  bcbRawAttributes(configured),
			}, bcbLogicalKey(configured), "bcb", result.SHA256)
			if rawErr == nil {
				seriesState["raw_payload_hash"] = rawHash
				seriesState["raw_object_key"] = rawKeyValue
			}
		}

		if collectErr != nil {
			if rawErr != nil {
				seriesState["status"] = "raw_store_failed"
			} else if result.SHA256 != "" {
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
		if result.SHA256 == "" {
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

func (a *app) start(ctx context.Context, m *metrics) (metadata.Run, bool, error) {
	if a.metadata == nil {
		return metadata.Run{}, false, errors.New("PostgreSQL metadata repository is required for canonical collection")
	}
	m.StartedAt = canonicalTime(m.StartedAt)
	run, err := a.metadata.StartRun(ctx, m.Source, a.batchKey+"/"+m.Source, m.StartedAt)
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
