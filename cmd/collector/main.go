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
	"github.com/luisdourado/invs/internal/providers/actionartifact"
	"github.com/luisdourado/invs/internal/providers/alfred"
	"github.com/luisdourado/invs/internal/providers/b3"
	"github.com/luisdourado/invs/internal/providers/bcb"
	"github.com/luisdourado/invs/internal/providers/calendarartifact"
	"github.com/luisdourado/invs/internal/providers/cvm"
	"github.com/luisdourado/invs/internal/providers/fred"
	"github.com/luisdourado/invs/internal/providers/nasdaq"
	"github.com/luisdourado/invs/internal/providers/nyse"
	"github.com/luisdourado/invs/internal/providers/ptax"
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

type historicalIdentityBaseChecker interface {
	HistoricalIdentityBaseExists(context.Context, string, string, string, string, time.Time) (bool, error)
}

type normalizedStore interface {
	WritePrices(string, []model.PriceBar) (string, int, error)
	WriteFundamentals(string, []model.FundamentalObservation) (string, int, error)
	WriteEconomics(string, []model.EconomicObservation) (string, int, error)
	WriteFilings(string, []model.Filing) (string, int, error)
	WriteFX(string, string, []model.FXObservation) (string, int, error)
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

type historicalCalendarEvidenceManifest struct {
	SchemaVersion     string                               `json:"schema_version"`
	Source            string                               `json:"source"`
	MIC               string                               `json:"mic"`
	ExchangeTimezone  string                               `json:"exchange_timezone"`
	Year              int                                  `json:"year"`
	CoverageStart     string                               `json:"coverage_start"`
	CoverageEnd       string                               `json:"coverage_end"`
	RegularOpenLocal  string                               `json:"regular_open_local"`
	RegularCloseLocal string                               `json:"regular_close_local"`
	AvailableAt       string                               `json:"available_at"`
	Revision          int                                  `json:"revision"`
	WeekendPolicy     string                               `json:"weekend_policy"`
	AdmissionPolicy   string                               `json:"admission_policy"`
	Resources         []historicalCalendarEvidenceResource `json:"resources"`
	Events            []historicalCalendarEvidenceEvent    `json:"events"`
}

type historicalCalendarEvidenceResource struct {
	Kind          string            `json:"kind"`
	Key           string            `json:"key"`
	URL           string            `json:"url"`
	SHA256        string            `json:"sha256"`
	ContentType   string            `json:"content_type"`
	ParserVersion string            `json:"parser_version"`
	Metadata      map[string]string `json:"metadata"`
}

type historicalCalendarEvidenceEvent struct {
	Date          string `json:"date"`
	Status        string `json:"status"`
	OpenLocal     string `json:"open_local,omitempty"`
	CloseLocal    string `json:"close_local,omitempty"`
	ResourceKind  string `json:"resource_kind"`
	SourceLocator string `json:"source_locator"`
}

type membershipEvidenceEvent struct {
	Ticker           string
	Member           bool
	EffectiveAt      time.Time
	AnnouncedAt      time.Time
	AvailableAt      time.Time
	RawRecordLocator string
	RawPayloadHash   string
	RecordedAt       time.Time
}

type membershipNoticeCollector func(context.Context, string) ([]providers.RawResource, []membershipEvidenceEvent, error)

type listingLifecycleEvidence struct {
	Ticker           string
	TradingName      string
	ValidFrom        time.Time
	ValidUntil       time.Time
	AvailableAt      time.Time
	RecordedAt       time.Time
	RawRecordLocator string
	RawPayloadHash   string
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
	source := flag.String("source", "all", "collector source: all, sec, sec-actions, prices, fred, alfred, bcb, ptax, b3, b3-prices, b3-action-replay, b3-calendar, b3-calendar-history, b3-membership, b3-listing-history, nasdaq-calendar, nasdaq-calendar-history, nasdaq-membership, nyse, or cvm")
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
	valid := map[string]bool{"all": true, "sec": true, "sec-actions": true, "prices": true, "fred": true, "alfred": true, "bcb": true, "ptax": true, "b3": true, "b3-prices": true, "b3-action-replay": true, "b3-calendar": true, "b3-calendar-history": true, "b3-membership": true, "b3-listing-history": true, "nasdaq-calendar": true, "nasdaq-calendar-history": true, "nasdaq-membership": true, "nyse": true, "cvm": true}
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
	if source == "ptax" && !a.cfg.Providers.PTAX.Enabled {
		return errors.New("PTAX provider is disabled")
	}
	if source == "b3" && !a.cfg.Providers.B3.Enabled {
		return errors.New("B3 provider is disabled")
	}
	if source == "b3-prices" && !a.cfg.Providers.B3HistoricalPrices.Enabled {
		return errors.New("B3 historical prices provider is disabled")
	}
	if source == "b3-calendar" && (!a.cfg.Providers.B3.Enabled || !a.cfg.Providers.B3.Calendar.Enabled) {
		return errors.New("B3 calendar provider is disabled")
	}
	if source == "b3-calendar-history" && !a.cfg.Providers.B3CalendarHistory.Enabled {
		return errors.New("B3 historical calendar provider is disabled")
	}
	if source == "nasdaq-calendar-history" && !a.cfg.Providers.NasdaqCalendarHistory.Enabled {
		return errors.New("Nasdaq historical calendar provider is disabled")
	}
	if source == "nasdaq-calendar" && !a.cfg.Providers.NasdaqCalendar.Enabled {
		return errors.New("Nasdaq calendar provider is disabled")
	}
	if source == "sec-actions" && !a.cfg.Providers.SECActionHistory.Enabled {
		return errors.New("SEC action-history provider is disabled")
	}
	if source == "b3-action-replay" && !a.cfg.Providers.B3ActionReplay.Enabled {
		return errors.New("B3 action-replay provider is disabled")
	}
	if source == "nyse" && !a.cfg.Providers.NYSE.Enabled {
		return errors.New("NYSE calendar provider is disabled")
	}
	if source == "b3-membership" && !a.cfg.Providers.B3Membership.Enabled {
		return errors.New("B3 membership provider is disabled")
	}
	if source == "b3-listing-history" && !a.cfg.Providers.B3ListingHistory.Enabled {
		return errors.New("B3 listing-history provider is disabled")
	}
	if source == "nasdaq-membership" && !a.cfg.Providers.NasdaqMembership.Enabled {
		return errors.New("Nasdaq membership provider is disabled")
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
	if (source == "all" || source == "ptax") && a.cfg.Providers.PTAX.Enabled {
		if err := a.collectPTAX(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if (source == "all" || source == "b3") && a.cfg.Providers.B3.Enabled {
		if err := a.collectB3(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if (source == "all" || source == "b3-prices") && a.cfg.Providers.B3HistoricalPrices.Enabled {
		if err := a.collectB3HistoricalPrices(ctx); err != nil {
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
	if (source == "all" || source == "nasdaq-calendar") && a.cfg.Providers.NasdaqCalendar.Enabled {
		if err := a.collectNasdaqCalendar(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if (source == "all" || source == "b3-calendar-history") && a.cfg.Providers.B3CalendarHistory.Enabled {
		if err := a.collectHistoricalCalendar(ctx, "b3_calendar", "b3-calendar-history", "BVMF", "America/Sao_Paulo", a.cfg.Providers.B3CalendarHistory); err != nil {
			errs = append(errs, err)
		}
	}
	if (source == "all" || source == "nasdaq-calendar-history") && a.cfg.Providers.NasdaqCalendarHistory.Enabled {
		if err := a.collectHistoricalCalendar(ctx, "nasdaq_calendar", "nasdaq-calendar-history", "XNAS", "America/New_York", a.cfg.Providers.NasdaqCalendarHistory); err != nil {
			errs = append(errs, err)
		}
	}
	if (source == "all" || source == "sec-actions") && a.cfg.Providers.SECActionHistory.Enabled {
		if err := a.collectCorporateActionArtifacts(ctx, "sec", "sec-actions", a.cfg.Providers.SECActionHistory); err != nil {
			errs = append(errs, err)
		}
	}
	if (source == "all" || source == "b3-action-replay") && a.cfg.Providers.B3ActionReplay.Enabled {
		if err := a.collectCorporateActionArtifacts(ctx, "b3", "b3-action-replay", a.cfg.Providers.B3ActionReplay); err != nil {
			errs = append(errs, err)
		}
	}
	if (source == "all" || source == "b3-membership") && a.cfg.Providers.B3Membership.Enabled {
		if err := a.collectB3Membership(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if (source == "all" || source == "b3-listing-history") && a.cfg.Providers.B3ListingHistory.Enabled {
		if err := a.collectB3ListingHistory(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if (source == "all" || source == "nasdaq-membership") && a.cfg.Providers.NasdaqMembership.Enabled {
		if err := a.collectNasdaqMembership(ctx); err != nil {
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
	eligibleUniverse := secEligibleUniverse(a.cfg.Universe)
	run, skip, err := a.start(ctx, &m, secRunInputs(eligibleUniverse))
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	var errs []error
	for _, s := range eligibleUniverse {
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
		if err := stampFilings(run, rawHashes["submissions"], r.Filings); err != nil {
			errs = append(errs, err)
			continue
		}
		fundamentalPath, fundamentalRows, fundamentalErr := a.normalized.WriteFundamentals(s.IssuerID, r.Facts)
		if fundamentalErr != nil {
			errs = append(errs, fmt.Errorf("SEC issuer %s fundamentals: %w", s.IssuerID, fundamentalErr))
		} else {
			m.OutputRows += fundamentalRows
			m.Cursor["last_issuer_id"] = s.IssuerID
			a.log.Info("normalized dataset", "source", "sec", "dataset", "fundamentals", "issuer_id", s.IssuerID, "path", fundamentalPath, "rows", len(r.Facts), "rows_changed", fundamentalRows, "legal_name", r.Issuer.LegalName)
		}
		filingPath, filingRows, filingErr := a.normalized.WriteFilings(s.IssuerID, r.Filings)
		if filingErr != nil {
			errs = append(errs, fmt.Errorf("SEC issuer %s filings: %w", s.IssuerID, filingErr))
		} else {
			m.OutputRows += filingRows
			m.Cursor["last_filing_issuer_id"] = s.IssuerID
			a.log.Info("normalized dataset", "source", "sec", "dataset", "filings", "issuer_id", s.IssuerID, "path", filingPath, "rows", len(r.Filings), "rows_changed", filingRows)
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

func (a *app) collectB3HistoricalPrices(ctx context.Context) error {
	provider := a.cfg.Providers.B3HistoricalPrices
	start, err := time.Parse(time.DateOnly, provider.Start)
	if err != nil {
		return fmt.Errorf("b3_historical_prices.start: %w", err)
	}
	end, err := time.Parse(time.DateOnly, provider.End)
	if err != nil {
		return fmt.Errorf("b3_historical_prices.end: %w", err)
	}
	securities := b3HistoricalPriceSecurities(provider, a.cfg.Universe)
	m := metrics{Source: "b3_cotahist", StartedAt: a.nowUTC(), Cursor: map[string]any{
		"provider": "b3_cotahist", "kind": "historical_quotes", "year": provider.Year,
		"historical_fitness": "installation_replay_receipt_time",
	}}
	run, skip, runErr := a.start(ctx, &m, b3HistoricalPricesRunInputs(provider, securities))
	if runErr != nil {
		return runErr
	}
	if skip {
		return nil
	}
	requestSecurities := make([]b3.HistoricalQuoteSecurity, 0, len(securities))
	for _, security := range securities {
		requestSecurities = append(requestSecurities, b3.HistoricalQuoteSecurity{
			SecurityID: security.SecurityID, Ticker: security.Ticker, ISIN: security.ISIN, Currency: security.Currency,
		})
	}
	result, collectErr := b3.NewClient(a.http).CollectHistoricalQuotes(ctx, b3.HistoricalQuotesRequest{
		Year: provider.Year, Start: start, End: end, Securities: requestSecurities,
	})
	m.Received += result.RecordsReceived
	m.Rejected += result.RecordsRejected
	m.Cursor["duplicates"] = result.Duplicates
	var errs []error
	if collectErr != nil {
		errs = append(errs, collectErr)
	}
	var resource providers.RawResource
	if len(result.Resources) != 1 {
		errs = append(errs, fmt.Errorf("B3 historical quotes returned %d downloaded resources, want 1", len(result.Resources)))
	} else {
		resource = result.Resources[0]
		fetchedAt := resourceFetchedAt(resource, m.StartedAt)
		key := rawKey("marketdata", "b3_cotahist", fmt.Sprintf("year-%d", provider.Year), resource.Bytes, fetchedAt, "zip")
		_, storeErr := a.storeRaw(ctx, &m, key, resource.Bytes, storage.RawMetadata{
			Source: "b3_cotahist", ContentType: resource.ContentType, FetchedAt: fetchedAt,
			Attributes: map[string]string{
				"year": strconv.Itoa(provider.Year), "start": provider.Start, "end": provider.End,
				"availability_policy": "installation_receipt", "price_basis": "raw",
			},
		}, fmt.Sprintf("b3_cotahist/historical_quotes/year/%d", provider.Year), "b3_cotahist", resource.SHA256)
		if storeErr != nil {
			errs = append(errs, storeErr)
		}
	}
	var snapshots []model.PriceBar
	if collectErr == nil && len(result.Resources) == 1 && len(errs) == 0 {
		for _, security := range securities {
			bars := result.Bars[security.SecurityID]
			if len(bars) == 0 {
				errs = append(errs, fmt.Errorf("B3 historical quotes security %s has no bounded bars", security.SecurityID))
				continue
			}
			if err := stampPrices(run, resource.SHA256, bars); err != nil {
				errs = append(errs, err)
				continue
			}
			path, rows, writeErr := a.normalized.WritePrices(security.SecurityID, bars)
			if writeErr != nil {
				errs = append(errs, writeErr)
				continue
			}
			snapshots = append(snapshots, bars...)
			m.OutputRows += rows
			m.Cursor["last_security_id"] = security.SecurityID
			a.log.Info("normalized dataset", "source", "b3_cotahist", "security_id", security.SecurityID, "path", path, "rows", len(bars), "rows_changed", rows)
		}
	}
	if m.Rejected > 0 {
		errs = append(errs, fmt.Errorf("%d records rejected", m.Rejected))
	}
	finalErr := errors.Join(errs...)
	return errors.Join(finalErr, a.finish(ctx, run, m, finalErr, snapshots, nil))
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

func (a *app) collectPTAX(ctx context.Context) error {
	provider := a.cfg.Providers.PTAX
	start, err := time.Parse(time.DateOnly, strings.TrimSpace(provider.Start))
	if err != nil {
		return fmt.Errorf("PTAX start: %w", err)
	}
	end, err := time.Parse(time.DateOnly, strings.TrimSpace(provider.End))
	if err != nil {
		return fmt.Errorf("PTAX end: %w", err)
	}
	m := metrics{
		Source: "bcb_ptax", RunKey: "ptax", StartedAt: a.nowUTC(),
		Cursor: map[string]any{
			"provider": "bcb_ptax", "pair": "USD-BRL", "rate_kind": "ptax_closing",
			"start": start.Format(time.DateOnly), "end": end.Format(time.DateOnly),
		},
	}
	run, skip, err := a.start(ctx, &m, ptaxRunInputs(provider))
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	result, collectErr := ptax.NewClient(a.http).Collect(ctx, ptax.Request{Start: start, End: end})
	m.Received = result.RecordsReceived
	m.Cursor["records_received"] = result.RecordsReceived
	m.Cursor["rows"] = len(result.Observations)
	var rawHash, rawPath string
	var rawErr error
	if len(result.Resources) != 1 {
		rawErr = fmt.Errorf("PTAX returned %d downloaded resources, want 1", len(result.Resources))
	} else {
		resource := result.Resources[0]
		rawPath = rawKey("bcb_ptax", "closing", "USD-BRL", resource.Bytes, resourceFetchedAt(resource, m.StartedAt), "json")
		rawHash, rawErr = a.storeRaw(ctx, &m, rawPath, resource.Bytes, storage.RawMetadata{
			Source: "bcb_ptax", ContentType: resource.ContentType,
			FetchedAt: resourceFetchedAt(resource, m.StartedAt), Attributes: ptaxRawAttributes(provider),
		}, ptaxLogicalKey(provider), "bcb_ptax", resource.SHA256)
		if rawErr == nil {
			m.Cursor["raw_payload_hash"] = rawHash
			m.Cursor["raw_object_key"] = rawPath
		}
	}
	if collectErr != nil || rawErr != nil {
		err = errors.Join(collectErr, rawErr)
		return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
	}
	if err := stampFX(run, rawHash, result.Observations); err != nil {
		return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
	}
	path, n, writeErr := a.normalized.WriteFX("USD", "BRL", result.Observations)
	if writeErr == nil {
		m.OutputRows += n
		m.Cursor["canonical_path"] = path
		a.log.Info("normalized dataset", "source", "bcb_ptax", "pair", "USD-BRL", "path", path, "rows", len(result.Observations))
	}
	return errors.Join(writeErr, a.finish(ctx, run, m, writeErr, nil, nil))
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

func (a *app) collectNasdaqCalendar(ctx context.Context) error {
	provider := a.cfg.Providers.NasdaqCalendar
	m := metrics{
		Source: "nasdaq_calendar", RunKey: "nasdaq-calendar", StartedAt: a.nowUTC(),
		Cursor: map[string]any{"provider": "nasdaq_calendar", "kind": "market_calendar", "year": provider.Year, "historical_fitness": "current_reference_receipt_time"},
	}
	run, skip, err := a.start(ctx, &m, calendarRunInputs("nasdaq_calendar", "XNAS", provider))
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	result, collectErr := nasdaq.NewClient(a.http).CollectCalendar(ctx, nasdaq.CalendarRequest{Year: provider.Year})
	if storeErr := a.storeCalendarResources(ctx, &m, "nasdaq_calendar", provider.Year, result.Resources); storeErr != nil {
		collectErr = errors.Join(collectErr, fmt.Errorf("Nasdaq calendar raw evidence: %w", storeErr))
	}
	if collectErr != nil {
		return errors.Join(collectErr, a.finish(ctx, run, m, collectErr, nil, nil))
	}
	m.Received = len(result.Events)
	coverageStart, coverageEnd, err := calendarCoverage(provider, "America/New_York")
	if err != nil {
		return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
	}
	evidenceHash, evidenceReference, availableAt, err := a.storeCalendarEvidence(ctx, &m, "nasdaq_calendar", "XNAS", provider, result.Resources)
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
			compileErr := fmt.Errorf("Nasdaq calendar event %s has unsupported status %q", event.Date.Format(time.DateOnly), event.Status)
			return errors.Join(compileErr, a.finish(ctx, run, m, compileErr, nil, nil))
		}
		events = append(events, compiled)
	}
	version := calendarVersion("xnas", provider.Year, evidenceHash)
	batch, err := marketcalendar.Compile(marketcalendar.Definition{
		DataSourceID: run.DataSourceID, MIC: "XNAS", ExchangeTimezone: "America/New_York",
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

func (a *app) collectCorporateActionArtifacts(ctx context.Context, sourceCode, selector string, provider config.CorporateActionProvider) error {
	m := metrics{
		Source: sourceCode, RunKey: selector, StartedAt: a.nowUTC(),
		Cursor: map[string]any{
			"provider": sourceCode, "kind": "corporate_actions",
			"availability_policy": provider.AvailabilityPolicy,
		},
	}
	run, skip, err := a.start(ctx, &m, corporateActionRunInputs(sourceCode, provider))
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	requests := make([]actionartifact.ResourceRequest, 0, len(provider.Resources))
	for _, resource := range provider.Resources {
		requests = append(requests, actionartifact.ResourceRequest{
			Kind: resource.Kind, URL: resource.URL,
			ExpectedSHA256: resource.SHA256, ContentType: resource.ContentType,
		})
	}
	result, collectErr := actionartifact.NewClient(a.http).Collect(ctx, sourceCode, requests)
	storeErr := a.storeCorporateActionResources(ctx, &m, sourceCode, result.Resources)
	if collectErr = errors.Join(collectErr, storeErr); collectErr != nil {
		return errors.Join(collectErr, a.finish(ctx, run, m, collectErr, nil, nil))
	}
	m.Received = len(result.Resources) + len(provider.Actions)
	evidenceHash, evidenceReference, evidenceReceipt, evidenceErr := a.storeCorporateActionEvidence(
		ctx, &m, sourceCode, provider, result.Resources,
	)
	if evidenceErr != nil {
		return errors.Join(evidenceErr, a.finish(ctx, run, m, evidenceErr, nil, nil))
	}
	batch, compileErr := corporateActionHistoricalTruthBatch(
		run, provider, result.Resources, evidenceHash, evidenceReference, evidenceReceipt,
	)
	if compileErr != nil {
		return errors.Join(compileErr, a.finish(ctx, run, m, compileErr, nil, nil))
	}
	publisher, ok := a.metadata.(historicalTruthPublisher)
	if !ok {
		err := errors.New("canonical corporate-action publication requires a historical-truth metadata repository")
		return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
	}
	if err := publisher.PublishHistoricalTruth(ctx, batch); err != nil {
		return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
	}
	m.OutputRows = len(batch.Actions)
	m.Cursor["status"] = "canonical_published"
	m.Cursor["action_versions"] = len(batch.Actions)
	return a.finish(ctx, run, m, nil, nil, nil)
}

type corporateActionEvidenceResource struct {
	Kind          string            `json:"kind"`
	Key           string            `json:"key"`
	URL           string            `json:"url"`
	SHA256        string            `json:"sha256"`
	ContentType   string            `json:"content_type"`
	FetchedAt     time.Time         `json:"fetched_at"`
	ParserVersion string            `json:"parser_version"`
	Metadata      map[string]string `json:"metadata"`
}

type corporateActionEvidenceManifest struct {
	SchemaVersion      string                                 `json:"schema_version"`
	Source             string                                 `json:"source"`
	AvailabilityPolicy string                                 `json:"availability_policy"`
	Resources          []corporateActionEvidenceResource      `json:"resources"`
	Actions            []metadata.CorporateActionVersionInput `json:"actions"`
}

func (a *app) storeCorporateActionResources(ctx context.Context, m *metrics, source string, resources []providers.RawResource) error {
	for _, resource := range resources {
		fetchedAt := resourceFetchedAt(resource, m.StartedAt)
		extension := "html"
		if resource.ContentType == "application/zip" {
			extension = "zip"
		}
		attributes := make(map[string]string, len(resource.ParserMetadata)+2)
		for key, value := range resource.ParserMetadata {
			attributes[key] = value
		}
		attributes["source_url"] = resource.URL
		attributes["adapter_sha256"] = resource.SHA256
		key := rawKey(source, "corporate-actions", resource.Kind, resource.Bytes, fetchedAt, extension)
		logicalKey := fmt.Sprintf("%s/corporate-actions/resource=%s", source, resource.Kind)
		if _, err := a.storeRaw(ctx, m, key, resource.Bytes, storage.RawMetadata{
			Source: source, ContentType: resource.ContentType, FetchedAt: fetchedAt,
			Attributes: attributes,
		}, logicalKey, source+"/corporate-actions/"+resource.Kind, resource.SHA256); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) storeCorporateActionEvidence(ctx context.Context, m *metrics, source string, provider config.CorporateActionProvider, resources []providers.RawResource) (string, string, time.Time, error) {
	inputs := corporateActionRunInputs(source, provider)
	manifest := corporateActionEvidenceManifest{
		SchemaVersion: "1", Source: source,
		AvailabilityPolicy: provider.AvailabilityPolicy,
		Resources:          make([]corporateActionEvidenceResource, 0, len(resources)),
		Actions:            inputs.Provider.CorporateActionVersions,
	}
	receipt := m.StartedAt
	for _, resource := range resources {
		fetchedAt := resourceFetchedAt(resource, m.StartedAt)
		if fetchedAt.After(receipt) {
			receipt = fetchedAt
		}
		manifest.Resources = append(manifest.Resources, corporateActionEvidenceResource{
			Kind: resource.Kind, Key: resource.Key, URL: resource.URL,
			SHA256: resource.SHA256, ContentType: resource.ContentType,
			FetchedAt: fetchedAt, ParserVersion: resource.ParserVersion,
			Metadata: resource.ParserMetadata,
		})
	}
	sort.Slice(manifest.Resources, func(i, j int) bool {
		return manifest.Resources[i].Kind < manifest.Resources[j].Kind
	})
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("encode corporate-action evidence: %w", err)
	}
	hash := providers.SHA256(encoded)
	logicalKey := fmt.Sprintf("%s/corporate-action-evidence/policy=%s", source, provider.AvailabilityPolicy)
	key := rawKey(source, "corporate-action-evidence", provider.AvailabilityPolicy, encoded, receipt, "json")
	storedHash, err := a.storeRaw(ctx, m, key, encoded, storage.RawMetadata{
		Source: source, ContentType: "application/json", FetchedAt: receipt,
		Attributes: map[string]string{
			"availability_policy":  provider.AvailabilityPolicy,
			"resource_count":       strconv.Itoa(len(resources)),
			"action_version_count": strconv.Itoa(len(provider.Actions)),
		},
	}, logicalKey, source+"/corporate-action-evidence", hash)
	if err != nil {
		return "", "", time.Time{}, err
	}
	return storedHash, logicalKey + "/sha256=" + storedHash, receipt, nil
}

func corporateActionHistoricalTruthBatch(run metadata.Run, provider config.CorporateActionProvider, resources []providers.RawResource, evidenceHash, evidenceReference string, evidenceReceipt time.Time) (metadata.HistoricalTruthBatch, error) {
	decodedHash, hashErr := hex.DecodeString(evidenceHash)
	if hashErr != nil || len(decodedHash) != sha256.Size || strings.TrimSpace(evidenceReference) == "" || evidenceReceipt.IsZero() {
		return metadata.HistoricalTruthBatch{}, errors.New("corporate-action evidence manifest is incomplete")
	}
	byKind := make(map[string]providers.RawResource, len(resources))
	for _, resource := range resources {
		byKind[resource.Kind] = resource
	}
	batch := metadata.HistoricalTruthBatch{Actions: make([]metadata.CorporateActionVersion, 0, len(provider.Actions))}
	for index, configured := range provider.Actions {
		_, exists := byKind[configured.ResourceKind]
		if !exists {
			return metadata.HistoricalTruthBatch{}, fmt.Errorf("corporate action %d references missing resource %q", index, configured.ResourceKind)
		}
		observedAt, err := time.Parse(time.RFC3339, configured.ObservedAt)
		if err != nil {
			return metadata.HistoricalTruthBatch{}, fmt.Errorf("corporate action %d observed_at: %w", index, err)
		}
		publishedAt, err := time.Parse(time.RFC3339, configured.PublishedAt)
		if err != nil {
			return metadata.HistoricalTruthBatch{}, fmt.Errorf("corporate action %d published_at: %w", index, err)
		}
		effectiveAt, err := time.Parse(time.RFC3339, configured.EffectiveAt)
		if err != nil {
			return metadata.HistoricalTruthBatch{}, fmt.Errorf("corporate action %d effective_at: %w", index, err)
		}
		availableAt := evidenceReceipt
		if provider.AvailabilityPolicy == "source_publication" {
			availableAt, err = time.Parse(time.RFC3339, configured.AvailableAt)
			if err != nil {
				return metadata.HistoricalTruthBatch{}, fmt.Errorf("corporate action %d available_at: %w", index, err)
			}
		}
		normalizer := actionartifact.ParserVersion
		locator := fmt.Sprintf(
			"actions/%d/resource=%s/source_locator=%s",
			index, configured.ResourceKind, configured.SourceLocator,
		)
		batch.Actions = append(batch.Actions, metadata.CorporateActionVersion{
			SchemaVersion: metadata.CorporateActionSchemaVersion,
			ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(strings.Join([]string{
				"corporate-action", run.DataSourceID, configured.SourceEventID,
				strconv.Itoa(configured.Revision),
			}, "/"))).String(),
			SecurityID: configured.SecurityID, SourceEventID: configured.SourceEventID,
			Revision: configured.Revision, ActionStatus: configured.ActionStatus,
			ActionType: configured.ActionType, ObservedAt: observedAt,
			ObservedPrecision: configured.ObservedPrecision, PublishedAt: publishedAt,
			PublishedPrecision: configured.PublishedPrecision, AvailableAt: availableAt,
			EffectiveAt: effectiveAt, EffectivePrecision: configured.EffectivePrecision,
			RecordDate:       optionalStringPointer(configured.RecordDate),
			PaymentDate:      optionalStringPointer(configured.PaymentDate),
			RatioNumerator:   optionalStringPointer(configured.RatioNumerator),
			RatioDenominator: optionalStringPointer(configured.RatioDenominator),
			CashAmount:       optionalStringPointer(configured.CashAmount),
			Currency:         optionalStringPointer(configured.Currency),
			TargetSecurityID: optionalStringPointer(configured.TargetSecurityID),
			SourceReference:  evidenceReference,
			RecordedAt:       evidenceReceipt,
			Provenance: metadata.CorporateActionProvenance{
				DataSourceID: run.DataSourceID, IngestionRunID: run.ID,
				RawPayloadHash: evidenceHash, RawRecordLocator: &locator,
				IngestedAt: evidenceReceipt, NormalizerVersion: &normalizer,
			},
		})
	}
	if err := metadata.ValidateHistoricalTruthBatch(batch); err != nil {
		return metadata.HistoricalTruthBatch{}, err
	}
	return batch, nil
}

func optionalStringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
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

func (a *app) collectHistoricalCalendar(ctx context.Context, sourceCode, selector, mic, timezone string, provider config.HistoricalCalendarProvider) error {
	m := metrics{
		Source: sourceCode, RunKey: selector, StartedAt: a.nowUTC(),
		Cursor: map[string]any{
			"provider": sourceCode, "kind": "market_calendar", "mic": mic,
			"year": provider.Year, "historical_fitness": "official_artifact_publication_time",
		},
	}
	run, skip, err := a.start(ctx, &m, historicalCalendarRunInputs(sourceCode, mic, provider))
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	client := calendarartifact.NewClient(a.http)
	coverageStart, coverageEnd, err := calendarCoverage(config.CalendarProvider{
		Year: provider.Year, CoverageStart: provider.CoverageStart, CoverageEnd: provider.CoverageEnd,
	}, timezone)
	if err != nil {
		return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
	}
	batch := metadata.HistoricalTruthBatch{}
	versions := make([]map[string]any, 0, len(provider.Versions))
	for _, configuredVersion := range provider.Versions {
		request := calendarartifact.Request{
			Source: sourceCode, MIC: mic, Year: provider.Year, Revision: configuredVersion.Revision,
			Resources: make([]calendarartifact.ResourceRequest, 0, len(configuredVersion.Resources)),
		}
		for _, resource := range configuredVersion.Resources {
			request.Resources = append(request.Resources, calendarartifact.ResourceRequest{
				Kind: resource.Kind, URL: resource.URL, ExpectedSHA256: resource.SHA256, ContentType: resource.ContentType,
			})
		}
		result, collectErr := client.Collect(ctx, request)
		storeErr := a.storeHistoricalCalendarResources(ctx, &m, sourceCode, provider.Year, configuredVersion.Revision, result.Resources)
		if collectErr = errors.Join(collectErr, storeErr); collectErr != nil {
			return errors.Join(collectErr, a.finish(ctx, run, m, collectErr, nil, nil))
		}
		m.Received += len(result.Resources) + len(configuredVersion.Events)

		evidenceHash, evidenceReference, evidenceErr := a.storeHistoricalCalendarEvidence(
			ctx, &m, sourceCode, mic, timezone, provider, configuredVersion, result.Resources,
		)
		if evidenceErr != nil {
			return errors.Join(evidenceErr, a.finish(ctx, run, m, evidenceErr, nil, nil))
		}
		availableAt, parseErr := time.Parse(time.RFC3339, configuredVersion.AvailableAt)
		if parseErr != nil {
			return errors.Join(parseErr, a.finish(ctx, run, m, parseErr, nil, nil))
		}
		events := make([]marketcalendar.Event, 0, len(configuredVersion.Events))
		for _, event := range configuredVersion.Events {
			date, dateErr := time.Parse(time.DateOnly, event.Date)
			if dateErr != nil {
				return errors.Join(dateErr, a.finish(ctx, run, m, dateErr, nil, nil))
			}
			events = append(events, marketcalendar.Event{
				Date: date, Status: event.Status, OpenLocal: event.OpenLocal, CloseLocal: event.CloseLocal,
				SourceReference: event.ResourceKind + "/" + event.SourceLocator,
			})
		}
		marketcalendar.SortEvents(events)
		version := calendarVersion(strings.ToLower(mic), provider.Year, evidenceHash)
		compiled, compileErr := marketcalendar.Compile(marketcalendar.Definition{
			DataSourceID: run.DataSourceID, MIC: mic, ExchangeTimezone: timezone,
			CalendarVersion: version, CoverageStart: coverageStart, CoverageEnd: coverageEnd,
			RegularOpenLocal: provider.RegularOpenLocal, RegularCloseLocal: provider.RegularCloseLocal,
			AvailableAt: availableAt, RecordedAt: m.StartedAt, SourceReference: evidenceReference,
			RawPayloadHash: evidenceHash, Revision: configuredVersion.Revision, Events: events,
		})
		if compileErr != nil {
			return errors.Join(compileErr, a.finish(ctx, run, m, compileErr, nil, nil))
		}
		batch.Calendars = append(batch.Calendars, compiled.Calendars...)
		batch.Sessions = append(batch.Sessions, compiled.Sessions...)
		versions = append(versions, map[string]any{
			"revision": configuredVersion.Revision, "available_at": availableAt,
			"calendar_version": version, "session_fingerprint": compiled.Calendars[0].SessionFingerprint,
		})
	}
	if err := a.publishCalendar(ctx, batch); err != nil {
		return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
	}
	m.OutputRows = len(batch.Calendars) + len(batch.Sessions)
	m.Cursor["status"] = "canonical_published"
	m.Cursor["versions"] = versions
	m.Cursor["calendar_versions"] = len(batch.Calendars)
	m.Cursor["session_rows"] = len(batch.Sessions)
	return a.finish(ctx, run, m, nil, nil, nil)
}

func (a *app) storeHistoricalCalendarResources(ctx context.Context, m *metrics, source string, year, revision int, resources []providers.RawResource) error {
	for _, resource := range resources {
		attributes := make(map[string]string, len(resource.ParserMetadata)+4)
		for key, value := range resource.ParserMetadata {
			attributes[key] = value
		}
		attributes["source_url"] = resource.URL
		attributes["parser_version"] = resource.ParserVersion
		attributes["adapter_sha256"] = resource.SHA256
		attributes["calendar_revision"] = strconv.Itoa(revision)
		fetchedAt := resourceFetchedAt(resource, m.StartedAt)
		extension := "html"
		if resource.ContentType == "application/pdf" {
			extension = "pdf"
		}
		key := rawKey(source, "calendar-history", fmt.Sprintf("year-%d-revision-%03d-%s", year, revision, resource.Kind), resource.Bytes, fetchedAt, extension)
		logicalKey := fmt.Sprintf("%s/calendar-history/year=%d/revision=%03d/resource=%s", source, year, revision, resource.Kind)
		if _, err := a.storeRaw(ctx, m, key, resource.Bytes, storage.RawMetadata{
			Source: source, ContentType: resource.ContentType, FetchedAt: fetchedAt, Attributes: attributes,
		}, logicalKey, source+"/calendar-history/"+resource.Kind, resource.SHA256); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) storeHistoricalCalendarEvidence(ctx context.Context, m *metrics, source, mic, timezone string, provider config.HistoricalCalendarProvider, version config.HistoricalCalendarVersion, resources []providers.RawResource) (string, string, error) {
	manifest := historicalCalendarEvidenceManifest{
		SchemaVersion: "1", Source: source, MIC: mic, ExchangeTimezone: timezone, Year: provider.Year,
		CoverageStart: provider.CoverageStart, CoverageEnd: provider.CoverageEnd,
		RegularOpenLocal: provider.RegularOpenLocal, RegularCloseLocal: provider.RegularCloseLocal,
		AvailableAt: version.AvailableAt, Revision: version.Revision,
		WeekendPolicy:   "Saturday and Sunday are materialized as explicit closed rows",
		AdmissionPolicy: "immutable official artifacts verified by exact SHA-256; declarative exceptions are source-located",
		Resources:       make([]historicalCalendarEvidenceResource, 0, len(resources)),
		Events:          make([]historicalCalendarEvidenceEvent, 0, len(version.Events)),
	}
	for _, resource := range resources {
		manifest.Resources = append(manifest.Resources, historicalCalendarEvidenceResource{
			Kind: resource.Kind, Key: resource.Key, URL: resource.URL, SHA256: resource.SHA256,
			ContentType: resource.ContentType, ParserVersion: resource.ParserVersion, Metadata: resource.ParserMetadata,
		})
	}
	for _, event := range version.Events {
		manifest.Events = append(manifest.Events, historicalCalendarEvidenceEvent{
			Date: event.Date, Status: event.Status, OpenLocal: event.OpenLocal, CloseLocal: event.CloseLocal,
			ResourceKind: event.ResourceKind, SourceLocator: event.SourceLocator,
		})
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", "", fmt.Errorf("encode historical calendar evidence manifest: %w", err)
	}
	hash := providers.SHA256(encoded)
	availableAt, err := time.Parse(time.RFC3339, version.AvailableAt)
	if err != nil {
		return "", "", err
	}
	logicalKey := fmt.Sprintf("%s/calendar-history-evidence/year=%d/mic=%s/revision=%03d", source, provider.Year, mic, version.Revision)
	key := rawKey(source, "calendar-history-evidence", fmt.Sprintf("year-%d-%s-revision-%03d", provider.Year, strings.ToLower(mic), version.Revision), encoded, availableAt, "json")
	storedHash, err := a.storeRaw(ctx, m, key, encoded, storage.RawMetadata{
		Source: source, ContentType: "application/json", FetchedAt: m.StartedAt,
		Attributes: map[string]string{
			"mic": mic, "year": strconv.Itoa(provider.Year), "revision": strconv.Itoa(version.Revision),
			"available_at": version.AvailableAt, "historical_fitness": "official_artifact_publication_time",
		},
	}, logicalKey, source+"/calendar-history-evidence", hash)
	if err != nil {
		return "", "", err
	}
	return storedHash, logicalKey + "/sha256=" + storedHash, nil
}

func (a *app) collectNasdaqMembership(ctx context.Context) error {
	client := nasdaq.NewClient(a.http)
	collect := func(ctx context.Context, noticeURL string) ([]providers.RawResource, []membershipEvidenceEvent, error) {
		result, err := client.CollectMembership(ctx, nasdaq.MembershipRequest{URL: noticeURL})
		events := make([]membershipEvidenceEvent, 0, len(result.Events))
		for _, event := range result.Events {
			events = append(events, membershipEvidenceEvent{
				Ticker: event.Ticker, Member: event.Member, EffectiveAt: event.EffectiveAt,
				AnnouncedAt: event.AnnouncedAt, AvailableAt: event.AvailableAt,
				RawRecordLocator: event.RawRecordLocator,
			})
		}
		return result.Resources, events, err
	}
	return a.collectIndexMembership(ctx, "nasdaq", "nasdaq-membership", a.cfg.Providers.NasdaqMembership, collect)
}

func (a *app) collectB3Membership(ctx context.Context) error {
	client := b3.NewClient(a.http)
	collect := func(ctx context.Context, noticeURL string) ([]providers.RawResource, []membershipEvidenceEvent, error) {
		result, err := client.CollectIndexMembership(ctx, b3.IndexMembershipRequest{URL: noticeURL})
		events := make([]membershipEvidenceEvent, 0, len(result.Events))
		for _, event := range result.Events {
			events = append(events, membershipEvidenceEvent{
				Ticker: event.Ticker, Member: event.Member, EffectiveAt: event.EffectiveAt,
				AnnouncedAt: event.AnnouncedAt, AvailableAt: event.AvailableAt,
				RawRecordLocator: event.RawRecordLocator,
			})
		}
		return result.Resources, events, err
	}
	return a.collectIndexMembership(ctx, "b3", "b3-membership", a.cfg.Providers.B3Membership, collect)
}

func (a *app) collectIndexMembership(ctx context.Context, source, runKey string, provider config.IndexMembershipProvider, collect membershipNoticeCollector) error {
	m := metrics{
		Source: source, RunKey: runKey, StartedAt: a.nowUTC(),
		Cursor: map[string]any{
			"provider": source, "kind": "universe_membership", "universe_id": provider.UniverseID,
			"notice_count": len(provider.Notices), "historical_fitness": "source_publication_time",
		},
	}
	run, skip, err := a.start(ctx, &m, membershipRunInputs(source, provider, a.cfg.Universe))
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	var collectErrs []error
	var evidence []membershipEvidenceEvent
	for index, noticeURL := range provider.Notices {
		resources, events, collectErr := collect(ctx, noticeURL)
		if len(resources) != 1 {
			collectErrs = append(collectErrs, fmt.Errorf("%s membership notice %d returned %d resources, want 1", source, index, len(resources)))
		} else {
			resource := resources[0]
			fetchedAt := resourceFetchedAt(resource, m.StartedAt)
			key := rawKey(source, "membership", fmt.Sprintf("%s-%03d", provider.UniverseID, index), resource.Bytes, fetchedAt, "html")
			attributes := map[string]string{
				"kind": resource.Kind, "universe_id": provider.UniverseID,
				"notice_index": strconv.Itoa(index), "url": resource.URL,
				"parser_version": resource.ParserVersion,
			}
			logicalKey := fmt.Sprintf("%s/index-membership/universe=%s/notice=%03d", source, provider.UniverseID, index)
			storedHash, storeErr := a.storeRaw(ctx, &m, key, resource.Bytes, storage.RawMetadata{
				Source: source, ContentType: resource.ContentType, FetchedAt: fetchedAt, Attributes: attributes,
			}, logicalKey, source+"/index-membership", resource.SHA256)
			if storeErr != nil {
				collectErrs = append(collectErrs, fmt.Errorf("%s membership raw notice %d: %w", source, index, storeErr))
			} else {
				for eventIndex := range events {
					events[eventIndex].RawPayloadHash = storedHash
					events[eventIndex].RecordedAt = m.StartedAt
				}
				evidence = append(evidence, events...)
			}
		}
		if collectErr != nil {
			collectErrs = append(collectErrs, collectErr)
		}
		m.Received += len(events)
	}
	if collectErr := errors.Join(collectErrs...); collectErr != nil {
		return errors.Join(collectErr, a.finish(ctx, run, m, collectErr, nil, nil))
	}
	batch, ignored, err := membershipHistoricalTruthBatch(run, source, provider, a.cfg.Universe, evidence)
	if err != nil {
		return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
	}
	publisher, ok := a.metadata.(historicalTruthPublisher)
	if !ok {
		err = errors.New("canonical membership publication requires a historical-truth metadata repository")
		return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
	}
	if err = publisher.PublishHistoricalTruth(ctx, batch); err != nil {
		err = fmt.Errorf("membership historical truth publication: %w", err)
		return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
	}
	m.OutputRows = len(batch.Identifiers) + len(batch.Listings) + len(batch.Memberships)
	m.Cursor["status"] = "canonical_published"
	m.Cursor["identifier_rows"] = len(batch.Identifiers)
	m.Cursor["listing_rows"] = len(batch.Listings)
	m.Cursor["membership_rows"] = len(batch.Memberships)
	m.Cursor["unconfigured_events_ignored"] = ignored
	return a.finish(ctx, run, m, nil, nil, nil)
}

func membershipHistoricalTruthBatch(run metadata.Run, source string, provider config.IndexMembershipProvider, universe []config.Security, evidence []membershipEvidenceEvent) (metadata.HistoricalTruthBatch, int, error) {
	allowed := make(map[string]struct{}, len(provider.Tickers))
	for _, ticker := range provider.Tickers {
		allowed[ticker] = struct{}{}
	}
	securityByTicker := make(map[string]config.Security, len(provider.Tickers))
	for _, security := range universe {
		if _, requested := allowed[security.Ticker]; !requested {
			continue
		}
		if _, duplicate := securityByTicker[security.Ticker]; duplicate {
			return metadata.HistoricalTruthBatch{}, 0, fmt.Errorf("%s membership ticker %s has ambiguous configured security mappings", source, security.Ticker)
		}
		securityByTicker[security.Ticker] = security
	}
	byTicker := make(map[string][]membershipEvidenceEvent, len(provider.Tickers))
	ignored := 0
	for _, event := range evidence {
		if _, requested := allowed[event.Ticker]; !requested {
			ignored++
			continue
		}
		byTicker[event.Ticker] = append(byTicker[event.Ticker], event)
	}
	batch := metadata.HistoricalTruthBatch{}
	for _, ticker := range provider.Tickers {
		security, exists := securityByTicker[ticker]
		if !exists {
			return metadata.HistoricalTruthBatch{}, ignored, fmt.Errorf("%s membership ticker %s has no configured security mapping", source, ticker)
		}
		events := byTicker[ticker]
		if len(events) == 0 {
			return metadata.HistoricalTruthBatch{}, ignored, fmt.Errorf("%s membership ticker %s has no source event", source, ticker)
		}
		sort.Slice(events, func(i, j int) bool {
			if !events[i].EffectiveAt.Equal(events[j].EffectiveAt) {
				return events[i].EffectiveAt.Before(events[j].EffectiveAt)
			}
			return events[i].AvailableAt.Before(events[j].AvailableAt)
		})
		if !events[0].Member {
			return metadata.HistoricalTruthBatch{}, ignored, fmt.Errorf("%s membership ticker %s begins with a removal", source, ticker)
		}
		first := events[0]
		if security.IssuerID == "" || security.Exchange == "" || security.MIC == "" || security.Currency == "" {
			return metadata.HistoricalTruthBatch{}, ignored, fmt.Errorf("%s membership ticker %s has incomplete identity/listing mapping", source, ticker)
		}
		identitySourceReference := source + "/" + first.RawRecordLocator
		identityKey := strings.Join([]string{
			"identifier", run.DataSourceID, security.SecurityID, "ticker", ticker,
			security.MIC, first.EffectiveAt.UTC().Format(time.RFC3339Nano),
		}, "/")
		listingKey := strings.Join([]string{
			"listing", run.DataSourceID, security.SecurityID, security.MIC,
			first.EffectiveAt.UTC().Format(time.RFC3339Nano),
		}, "/")
		issuerID := security.IssuerID
		batch.Identifiers = append(batch.Identifiers, metadata.SecurityIdentifierVersion{
			SchemaVersion: metadata.HistoricalSchemaVersion,
			ID:            uuid.NewSHA1(uuid.NameSpaceURL, []byte(identityKey)).String(),
			SecurityID:    security.SecurityID, IdentifierType: "ticker", Value: ticker,
			NormalizedValue: ticker, IdentifierScope: security.MIC,
			ValidFrom: canonicalTime(first.EffectiveAt), AvailableAt: canonicalTime(first.AvailableAt),
			SourceReference: identitySourceReference, RecordedAt: canonicalTime(first.RecordedAt),
			DataSourceID: run.DataSourceID, RawPayloadHash: first.RawPayloadHash,
			Revision: 0, IsPrimary: security.PrimaryListing,
		})
		batch.Listings = append(batch.Listings, metadata.SecurityListingVersion{
			SchemaVersion: metadata.HistoricalSchemaVersion,
			ID:            uuid.NewSHA1(uuid.NameSpaceURL, []byte(listingKey)).String(),
			SecurityID:    security.SecurityID, IssuerID: &issuerID,
			Exchange: security.Exchange, MIC: security.MIC, Currency: security.Currency,
			PrimaryListing: security.PrimaryListing, ValidFrom: canonicalTime(first.EffectiveAt),
			AvailableAt: canonicalTime(first.AvailableAt), SourceReference: identitySourceReference,
			RecordedAt: canonicalTime(first.RecordedAt), DataSourceID: run.DataSourceID,
			RawPayloadHash: first.RawPayloadHash, Revision: 0,
		})
		for revision, event := range events {
			if revision > 0 {
				previous := events[revision-1]
				if previous.Member == event.Member {
					return metadata.HistoricalTruthBatch{}, ignored, fmt.Errorf("%s membership ticker %s repeats member=%t without a transition", source, ticker, event.Member)
				}
				if !event.EffectiveAt.After(previous.EffectiveAt) || !event.AvailableAt.After(previous.AvailableAt) {
					return metadata.HistoricalTruthBatch{}, ignored, fmt.Errorf("%s membership ticker %s events are not strictly chronological", source, ticker)
				}
			}
			if event.RawPayloadHash == "" || event.RawRecordLocator == "" || event.AvailableAt.IsZero() || event.EffectiveAt.IsZero() {
				return metadata.HistoricalTruthBatch{}, ignored, fmt.Errorf("%s membership ticker %s has incomplete source evidence", source, ticker)
			}
			sourceReference := source + "/" + event.RawRecordLocator
			id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(strings.Join([]string{
				"membership", run.DataSourceID, provider.UniverseID, security.SecurityID,
				event.EffectiveAt.UTC().Format(time.RFC3339Nano), strconv.FormatBool(event.Member),
			}, "/"))).String()
			announcedAt := canonicalTime(event.AnnouncedAt)
			batch.Memberships = append(batch.Memberships, metadata.UniverseMembership{
				SchemaVersion: metadata.HistoricalSchemaVersion, ID: id,
				UniverseID: provider.UniverseID, SecurityID: security.SecurityID,
				Member: event.Member, ValidFrom: canonicalTime(event.EffectiveAt),
				AnnouncedAt: &announcedAt, AvailableAt: canonicalTime(event.AvailableAt),
				SourceReference: sourceReference, RecordedAt: canonicalTime(event.RecordedAt),
				DataSourceID: run.DataSourceID, RawPayloadHash: event.RawPayloadHash,
				Revision: revision,
			})
		}
	}
	return batch, ignored, nil
}

func (a *app) collectB3ListingHistory(ctx context.Context) error {
	provider := a.cfg.Providers.B3ListingHistory
	m := metrics{
		Source: "b3", RunKey: "b3-listing-history", StartedAt: a.nowUTC(),
		Cursor: map[string]any{
			"provider": "b3", "kind": "security_listing_lifecycle",
			"notice_count": len(provider.Notices), "historical_fitness": "source_publication_time",
		},
	}
	run, skip, err := a.start(ctx, &m, listingHistoryRunInputs(provider, a.cfg.Universe))
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	client := b3.NewClient(a.http)
	var collectErrs []error
	evidence := make([]listingLifecycleEvidence, 0, len(provider.Notices))
	for index, notice := range provider.Notices {
		result, collectErr := client.CollectListingLifecycle(ctx, b3.ListingLifecycleRequest{URL: notice.URL})
		if len(result.Resources) != 1 {
			collectErrs = append(collectErrs, fmt.Errorf("B3 listing-history notice %d returned %d resources, want 1", index, len(result.Resources)))
		} else {
			resource := result.Resources[0]
			fetchedAt := resourceFetchedAt(resource, m.StartedAt)
			key := rawKey("b3", "listing-lifecycle", fmt.Sprintf("notice-%03d-%s", index, notice.Ticker), resource.Bytes, fetchedAt, "html")
			logicalKey := fmt.Sprintf("b3/listing-history/ticker=%s/notice=%03d", notice.Ticker, index)
			storedHash, storeErr := a.storeRaw(ctx, &m, key, resource.Bytes, storage.RawMetadata{
				Source: "b3", ContentType: resource.ContentType, FetchedAt: fetchedAt,
				Attributes: map[string]string{
					"kind": resource.Kind, "ticker": notice.Ticker, "trading_name": notice.TradingName,
					"notice_index": strconv.Itoa(index), "url": resource.URL,
					"parser_version": resource.ParserVersion,
				},
			}, logicalKey, "b3/listing-history", resource.SHA256)
			if storeErr != nil {
				collectErrs = append(collectErrs, fmt.Errorf("B3 listing-history raw notice %d: %w", index, storeErr))
			} else if len(result.Events) == 1 {
				event := result.Events[0]
				validFrom, parseErr := time.Parse(time.RFC3339, notice.ValidFrom)
				if parseErr != nil {
					collectErrs = append(collectErrs, fmt.Errorf("B3 listing-history notice %d valid_from: %w", index, parseErr))
				} else if event.TradingName != notice.TradingName {
					collectErrs = append(collectErrs, fmt.Errorf("B3 listing-history notice %d trading name %s does not match configured %s", index, event.TradingName, notice.TradingName))
				} else {
					evidence = append(evidence, listingLifecycleEvidence{
						Ticker: notice.Ticker, TradingName: event.TradingName,
						ValidFrom: validFrom.UTC(), ValidUntil: event.EffectiveAt,
						AvailableAt: event.AvailableAt, RecordedAt: m.StartedAt,
						RawRecordLocator: event.RawRecordLocator, RawPayloadHash: storedHash,
					})
				}
			} else {
				collectErrs = append(collectErrs, fmt.Errorf("B3 listing-history notice %d returned %d events, want 1", index, len(result.Events)))
			}
		}
		if collectErr != nil {
			collectErrs = append(collectErrs, collectErr)
		}
		m.Received += len(result.Events)
	}
	if collectErr := errors.Join(collectErrs...); collectErr != nil {
		return errors.Join(collectErr, a.finish(ctx, run, m, collectErr, nil, nil))
	}
	batch, err := listingHistoryBatch(run, a.cfg.Universe, evidence)
	if err != nil {
		return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
	}
	checker, ok := a.metadata.(historicalIdentityBaseChecker)
	if !ok {
		err = errors.New("B3 listing-history publication requires historical identity base checks")
		return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
	}
	for index, identifier := range batch.Identifiers {
		exists, checkErr := checker.HistoricalIdentityBaseExists(ctx, run.DataSourceID, identifier.SecurityID, identifier.Value, identifier.IdentifierScope, identifier.ValidFrom)
		if checkErr != nil {
			err = fmt.Errorf("B3 listing-history base check %d: %w", index, checkErr)
			return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
		}
		if !exists {
			err = fmt.Errorf("B3 listing-history ticker %s has no exact open-ended identifier/listing base", identifier.Value)
			return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
		}
	}
	publisher, ok := a.metadata.(historicalTruthPublisher)
	if !ok {
		err = errors.New("canonical B3 listing-history publication requires a historical-truth metadata repository")
		return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
	}
	if err = publisher.PublishHistoricalTruth(ctx, batch); err != nil {
		err = fmt.Errorf("B3 listing-history publication: %w", err)
		return errors.Join(err, a.finish(ctx, run, m, err, nil, nil))
	}
	m.OutputRows = len(batch.Identifiers) + len(batch.Listings)
	m.Cursor["status"] = "canonical_published"
	m.Cursor["identifier_corrections"] = len(batch.Identifiers)
	m.Cursor["listing_corrections"] = len(batch.Listings)
	return a.finish(ctx, run, m, nil, nil, nil)
}

func listingHistoryBatch(run metadata.Run, universe []config.Security, evidence []listingLifecycleEvidence) (metadata.HistoricalTruthBatch, error) {
	requested := make(map[string]struct{}, len(evidence))
	for _, event := range evidence {
		requested[event.Ticker] = struct{}{}
	}
	byTicker := make(map[string]config.Security, len(requested))
	for _, security := range universe {
		if _, wanted := requested[security.Ticker]; !wanted {
			continue
		}
		if _, duplicate := byTicker[security.Ticker]; duplicate {
			return metadata.HistoricalTruthBatch{}, fmt.Errorf("B3 listing-history ticker %s has ambiguous universe mappings", security.Ticker)
		}
		byTicker[security.Ticker] = security
	}
	batch := metadata.HistoricalTruthBatch{}
	for _, event := range evidence {
		security, exists := byTicker[event.Ticker]
		if !exists {
			return metadata.HistoricalTruthBatch{}, fmt.Errorf("B3 listing-history ticker %s has no universe mapping", event.Ticker)
		}
		if event.TradingName == "" || !event.ValidUntil.After(event.ValidFrom) || event.AvailableAt.IsZero() || event.RecordedAt.Before(event.AvailableAt) || event.RawRecordLocator == "" || event.RawPayloadHash == "" {
			return metadata.HistoricalTruthBatch{}, fmt.Errorf("B3 listing-history ticker %s has incomplete lifecycle evidence", event.Ticker)
		}
		validUntil := canonicalTime(event.ValidUntil)
		sourceReference := "b3/" + event.RawRecordLocator
		identifierID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(strings.Join([]string{
			"identifier-correction", run.DataSourceID, security.SecurityID, "ticker", event.Ticker,
			security.MIC, event.ValidFrom.UTC().Format(time.RFC3339Nano), validUntil.Format(time.RFC3339Nano), "1",
		}, "/"))).String()
		listingID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(strings.Join([]string{
			"listing-correction", run.DataSourceID, security.SecurityID, security.MIC,
			event.ValidFrom.UTC().Format(time.RFC3339Nano), validUntil.Format(time.RFC3339Nano), "1",
		}, "/"))).String()
		issuerID := security.IssuerID
		batch.Identifiers = append(batch.Identifiers, metadata.SecurityIdentifierVersion{
			SchemaVersion: metadata.HistoricalSchemaVersion, ID: identifierID,
			SecurityID: security.SecurityID, IdentifierType: "ticker", Value: event.Ticker,
			NormalizedValue: event.Ticker, IdentifierScope: security.MIC,
			ValidFrom: canonicalTime(event.ValidFrom), ValidUntil: &validUntil,
			AvailableAt: canonicalTime(event.AvailableAt), SourceReference: sourceReference,
			RecordedAt: canonicalTime(event.RecordedAt), DataSourceID: run.DataSourceID,
			RawPayloadHash: event.RawPayloadHash, Revision: 1, IsPrimary: security.PrimaryListing,
		})
		batch.Listings = append(batch.Listings, metadata.SecurityListingVersion{
			SchemaVersion: metadata.HistoricalSchemaVersion, ID: listingID,
			SecurityID: security.SecurityID, IssuerID: &issuerID,
			Exchange: security.Exchange, MIC: security.MIC, Currency: security.Currency,
			PrimaryListing: security.PrimaryListing, ValidFrom: canonicalTime(event.ValidFrom),
			ValidUntil: &validUntil, AvailableAt: canonicalTime(event.AvailableAt),
			SourceReference: sourceReference, RecordedAt: canonicalTime(event.RecordedAt),
			DataSourceID: run.DataSourceID, RawPayloadHash: event.RawPayloadHash, Revision: 1,
		})
	}
	return batch, nil
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

func ptaxRawAttributes(provider config.PTAXProvider) map[string]string {
	return map[string]string{
		"provider_format": "odata-json", "base_currency": "USD", "quote_currency": "BRL",
		"rate_kind": "ptax_closing", "fixing_timezone": "America/Sao_Paulo",
		"start": strings.TrimSpace(provider.Start), "end": strings.TrimSpace(provider.End),
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

func ptaxLogicalKey(provider config.PTAXProvider) string {
	return "bcb_ptax/closing/USD-BRL/start/" + strings.TrimSpace(provider.Start) + "/end/" + strings.TrimSpace(provider.End)
}

func secRunInputs(universe []config.Security) metadata.RunInputs {
	eligibleUniverse := secEligibleUniverse(universe)
	requests := make([]metadata.IssuerRequest, 0, len(eligibleUniverse))
	for _, security := range eligibleUniverse {
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
			ConfiguredUniverseCount: len(eligibleUniverse),
			IssuerRequests:          requests,
		},
	}
}

func secEligibleUniverse(universe []config.Security) []config.Security {
	eligible := make([]config.Security, 0, len(universe))
	for _, security := range universe {
		if security.CIK > 0 {
			eligible = append(eligible, security)
		}
	}
	return eligible
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

func b3HistoricalPriceSecurities(provider config.B3HistoricalPriceProvider, universe []config.Security) []config.Security {
	byTicker := make(map[string]config.Security, len(universe))
	for _, security := range universe {
		byTicker[strings.TrimSpace(security.Ticker)] = security
	}
	tickers := append([]string(nil), provider.Tickers...)
	sort.Strings(tickers)
	securities := make([]config.Security, 0, len(tickers))
	for _, ticker := range tickers {
		securities = append(securities, byTicker[strings.TrimSpace(ticker)])
	}
	return securities
}

func b3HistoricalPricesRunInputs(provider config.B3HistoricalPriceProvider, securities []config.Security) metadata.RunInputs {
	requests := make([]metadata.SecurityRequest, 0, len(securities))
	for _, security := range securities {
		requests = append(requests, metadata.SecurityRequest{
			SecurityID: security.SecurityID, VendorSymbol: security.Ticker, Currency: security.Currency,
			Start: provider.Start, End: provider.End, Interval: "1d", Events: "closed_annual_archive",
		})
	}
	return metadata.RunInputs{
		SchemaVersion: metadata.RunInputsSchemaVersion,
		Source:        "b3_cotahist",
		Provider: metadata.ProviderInputs{
			Name: "b3_cotahist", Kind: "market_data", ConfiguredUniverseCount: len(requests),
			SecurityRequests: requests, Format: "cotahist_fixed_width_zip", Vintage: "installation_receipt",
			B3HistoricalQuoteYear: provider.Year,
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

func ptaxRunInputs(provider config.PTAXProvider) metadata.RunInputs {
	return metadata.RunInputs{
		SchemaVersion: metadata.RunInputsSchemaVersion,
		Source:        "bcb_ptax",
		Provider: metadata.ProviderInputs{
			Name: "bcb_ptax", Kind: "fx", Format: "odata-json", Vintage: "source_bulletin_timestamp",
			FXRequest: &metadata.FXRequestInput{
				BaseCurrency: "USD", QuoteCurrency: "BRL", RateKind: "ptax_closing",
				FixingTimezone: "America/Sao_Paulo", Start: strings.TrimSpace(provider.Start), End: strings.TrimSpace(provider.End),
			},
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

func historicalCalendarRunInputs(source, mic string, provider config.HistoricalCalendarProvider) metadata.RunInputs {
	versions := make([]metadata.CalendarArtifactInput, 0, len(provider.Versions))
	for _, version := range provider.Versions {
		configured := metadata.CalendarArtifactInput{
			AvailableAt: strings.TrimSpace(version.AvailableAt), Revision: version.Revision,
			Resources: make([]metadata.CalendarArtifactResourceInput, 0, len(version.Resources)),
			Events:    make([]metadata.CalendarArtifactEventInput, 0, len(version.Events)),
		}
		for _, resource := range version.Resources {
			configured.Resources = append(configured.Resources, metadata.CalendarArtifactResourceInput{
				Kind: strings.TrimSpace(resource.Kind), URL: strings.TrimSpace(resource.URL),
				SHA256: strings.TrimSpace(resource.SHA256), ContentType: strings.TrimSpace(resource.ContentType),
			})
		}
		for _, event := range version.Events {
			configured.Events = append(configured.Events, metadata.CalendarArtifactEventInput{
				Date: strings.TrimSpace(event.Date), Status: strings.TrimSpace(event.Status),
				OpenLocal: strings.TrimSpace(event.OpenLocal), CloseLocal: strings.TrimSpace(event.CloseLocal),
				ResourceKind: strings.TrimSpace(event.ResourceKind), SourceLocator: strings.TrimSpace(event.SourceLocator),
			})
		}
		versions = append(versions, configured)
	}
	return metadata.RunInputs{
		SchemaVersion: metadata.RunInputsSchemaVersion,
		Source:        source,
		Provider: metadata.ProviderInputs{
			Name: source, Kind: "market_calendar", CalendarYear: provider.Year, CalendarMIC: mic,
			CalendarCoverageStart: strings.TrimSpace(provider.CoverageStart), CalendarCoverageEnd: strings.TrimSpace(provider.CoverageEnd),
			CalendarRegularOpen: strings.TrimSpace(provider.RegularOpenLocal), CalendarRegularClose: strings.TrimSpace(provider.RegularCloseLocal),
			CalendarArtifactVersions: versions, Format: "official_artifacts_with_declarative_transcription",
			Vintage: "historical_source_publication",
		},
	}
}

func corporateActionRunInputs(source string, provider config.CorporateActionProvider) metadata.RunInputs {
	resources := make([]metadata.CorporateActionResourceInput, 0, len(provider.Resources))
	for _, resource := range provider.Resources {
		resources = append(resources, metadata.CorporateActionResourceInput{
			Kind: resource.Kind, URL: resource.URL, SHA256: resource.SHA256,
			ContentType: resource.ContentType,
		})
	}
	versions := make([]metadata.CorporateActionVersionInput, 0, len(provider.Actions))
	for _, action := range provider.Actions {
		versions = append(versions, metadata.CorporateActionVersionInput{
			SecurityID: action.SecurityID, SourceEventID: action.SourceEventID,
			Revision: action.Revision, ActionStatus: action.ActionStatus,
			ActionType: action.ActionType, ObservedAt: action.ObservedAt,
			ObservedPrecision: action.ObservedPrecision, PublishedAt: action.PublishedAt,
			PublishedPrecision: action.PublishedPrecision, AvailableAt: action.AvailableAt,
			EffectiveAt: action.EffectiveAt, EffectivePrecision: action.EffectivePrecision,
			RecordDate: action.RecordDate, PaymentDate: action.PaymentDate,
			RatioNumerator: action.RatioNumerator, RatioDenominator: action.RatioDenominator,
			CashAmount: action.CashAmount, Currency: action.Currency,
			TargetSecurityID: action.TargetSecurityID, ResourceKind: action.ResourceKind,
			SourceLocator: action.SourceLocator,
		})
	}
	return metadata.RunInputs{
		SchemaVersion: metadata.RunInputsSchemaVersion, Source: source,
		Provider: metadata.ProviderInputs{
			Name: source + "-corporate-actions", Kind: "corporate_actions",
			CorporateActionPolicy:    provider.AvailabilityPolicy,
			CorporateActionResources: resources, CorporateActionVersions: versions,
			Format:  "exact_artifacts_with_source_located_transcription",
			Vintage: provider.AvailabilityPolicy,
		},
	}
}

func membershipRunInputs(source string, provider config.IndexMembershipProvider, universe []config.Security) metadata.RunInputs {
	tickers := append([]string(nil), provider.Tickers...)
	notices := append([]string(nil), provider.Notices...)
	sort.Strings(tickers)
	sort.Strings(notices)
	byTicker := make(map[string]config.Security, len(universe))
	for _, security := range universe {
		byTicker[security.Ticker] = security
	}
	configured := make([]metadata.MembershipSecurityInput, 0, len(tickers))
	for _, ticker := range tickers {
		security := byTicker[ticker]
		configured = append(configured, metadata.MembershipSecurityInput{
			SecurityID: security.SecurityID, Ticker: ticker, MIC: security.MIC,
		})
	}
	return metadata.RunInputs{
		SchemaVersion: metadata.RunInputsSchemaVersion,
		Source:        source,
		Provider: metadata.ProviderInputs{
			Name: source, Kind: "universe_membership", ConfiguredUniverseCount: len(configured),
			MembershipUniverseID: provider.UniverseID, MembershipNotices: notices,
			MembershipSecurities: configured, Format: "html", Vintage: "historical_source_publication",
		},
	}
}

func listingHistoryRunInputs(provider config.ListingHistoryProvider, universe []config.Security) metadata.RunInputs {
	notices := append([]config.ListingHistoryNotice(nil), provider.Notices...)
	sort.Slice(notices, func(i, j int) bool {
		if notices[i].Ticker != notices[j].Ticker {
			return notices[i].Ticker < notices[j].Ticker
		}
		return notices[i].URL < notices[j].URL
	})
	byTicker := make(map[string]config.Security, len(universe))
	for _, security := range universe {
		byTicker[security.Ticker] = security
	}
	inputs := make([]metadata.ListingHistoryInput, 0, len(notices))
	for _, notice := range notices {
		inputs = append(inputs, metadata.ListingHistoryInput{
			SecurityID: byTicker[notice.Ticker].SecurityID, Ticker: notice.Ticker,
			TradingName: notice.TradingName, ValidFrom: notice.ValidFrom, NoticeURL: notice.URL,
		})
	}
	return metadata.RunInputs{
		SchemaVersion: metadata.RunInputsSchemaVersion,
		Source:        "b3",
		Provider: metadata.ProviderInputs{
			Name: "b3", Kind: "security_listing_lifecycle", ConfiguredUniverseCount: len(inputs),
			ListingHistoryNotices: inputs, Format: "html", Vintage: "historical_source_publication",
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

func stampFilings(run metadata.Run, rawHash string, filings []model.Filing) error {
	for i := range filings {
		if err := stampProvenance(run, rawHash, &filings[i].RawPayloadHash, &filings[i].Provenance, filings[i].Temporal); err != nil {
			return fmt.Errorf("filing %d: %w", i, err)
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

func stampFX(run metadata.Run, rawHash string, observations []model.FXObservation) error {
	for i := range observations {
		temporal := model.Temporal{IngestedAt: observations[i].RecordedAt}
		if err := stampProvenance(run, rawHash, &observations[i].RawPayloadHash, &observations[i].Provenance, temporal); err != nil {
			return fmt.Errorf("FX observation %d: %w", i, err)
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
