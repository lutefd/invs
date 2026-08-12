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
	"github.com/luisdourado/invs/internal/providers/fred"
	"github.com/luisdourado/invs/internal/providers/sec"
	"github.com/luisdourado/invs/internal/providers/yahoo"
	"github.com/luisdourado/invs/internal/storage"
)

type app struct {
	cfg        config.Config
	raw        storage.RawStore
	normalized *normalize.Writer
	http       *httpx.Client
	log        *slog.Logger
	metadata   *metadata.Repository
	batchKey   string
}
type metrics struct {
	Source                         string
	StartedAt                      time.Time
	Duration                       time.Duration
	Received, OutputRows, Rejected int
	RawObjects                     int
	RawBytes                       int64
	Cursor                         map[string]any
	RunKey                         string
}

func main() {
	configPath := flag.String("config", "config/config.yaml", "configuration YAML")
	source := flag.String("source", "all", "collector source: all, sec, prices, or fred")
	runKey := flag.String("run-key", "", "stable batch retry key; omitted generates a unique invocation key")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("configuration failed", "error", err)
		os.Exit(2)
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
	metadataRepo, err := metadata.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("metadata database unavailable", "error", err)
		os.Exit(2)
	}
	if metadataRepo != nil {
		defer metadataRepo.Close()
		if err := metadataRepo.SyncCatalog(ctx, cfg); err != nil {
			log.Error("metadata catalog sync failed", "error", err)
			os.Exit(2)
		}
	}
	batchKey := *runKey
	if batchKey == "" {
		batchKey = time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + uuid.NewString()
	}
	a := &app{cfg: cfg, raw: raw, normalized: normalized, http: httpClient, log: log, metadata: metadataRepo, batchKey: batchKey}
	if err := a.run(ctx, *source); err != nil {
		log.Error("collection failed", "source", *source, "error", err)
		os.Exit(1)
	}
}

func (a *app) run(ctx context.Context, source string) error {
	valid := map[string]bool{"all": true, "sec": true, "prices": true, "fred": true}
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
	return errors.Join(errs...)
}

func (a *app) collectSEC(ctx context.Context) error {
	c := sec.NewClient(a.http)
	m := metrics{Source: "sec", StartedAt: time.Now().UTC(), Cursor: map[string]any{}}
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
			errs = append(errs, fmt.Errorf("%s: %w", s.IssuerID, err))
			continue
		}
		m.Received += r.RecordsReceived
		m.Rejected += r.RecordsRejected
		rawOK := true
		for _, d := range r.Raw {
			key := rawKey("sec", d.Kind, fmt.Sprintf("cik-%010d", s.CIK), d.Data, m.StartedAt, "json")
			if stored, err := a.raw.Put(ctx, key, bytes.NewReader(d.Data), storage.RawMetadata{Source: "sec", ContentType: "application/json", FetchedAt: m.StartedAt, Attributes: map[string]string{"issuer_id": s.IssuerID, "cik": fmt.Sprint(s.CIK), "kind": d.Kind}}); err != nil {
				errs = append(errs, err)
				rawOK = false
			} else {
				m.RawObjects++
				m.RawBytes += stored.Size
			}
		}
		if !rawOK {
			continue
		}
		if err := a.metadata.EnrichSECIssuer(ctx, r.Issuer, r.StateOfIncorporation); err != nil {
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
	return errors.Join(collectErr, a.finish(ctx, run, m, collectErr))
}

func (a *app) collectPrices(ctx context.Context) error {
	c := yahoo.NewClient(a.http)
	m := metrics{Source: "yahoo", StartedAt: time.Now().UTC(), Cursor: map[string]any{}}
	start, err := time.Parse("2006-01-02", a.cfg.Providers.Prices.Start)
	if err != nil {
		return fmt.Errorf("prices.start: %w", err)
	}
	end := time.Now().UTC()
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
	for _, s := range a.cfg.Universe {
		if s.YahooSymbol == "" {
			m.Rejected++
			continue
		}
		r, err := c.Collect(ctx, model.HistoricalPriceRequest{SecurityID: s.SecurityID, VendorSymbol: s.YahooSymbol, Currency: s.Currency, Start: start, End: end})
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.SecurityID, err))
			continue
		}
		m.Received += r.RecordsReceived
		m.Rejected += r.RecordsRejected
		key := rawKey("marketdata", "yahoo", s.SecurityID, r.Raw, m.StartedAt, "json")
		if stored, err := a.raw.Put(ctx, key, bytes.NewReader(r.Raw), storage.RawMetadata{Source: "yahoo", ContentType: "application/json", FetchedAt: m.StartedAt, Attributes: map[string]string{"security_id": s.SecurityID, "vendor_symbol": s.YahooSymbol}}); err != nil {
			errs = append(errs, err)
			continue
		} else {
			m.RawObjects++
			m.RawBytes += stored.Size
		}
		path, n, err := a.normalized.WritePrices(s.SecurityID, r.Bars)
		if err != nil {
			errs = append(errs, err)
		} else {
			m.OutputRows += n
			m.Cursor["last_security_id"] = s.SecurityID
			a.log.Info("normalized dataset", "source", "yahoo", "security_id", s.SecurityID, "path", path, "rows", len(r.Bars))
		}
	}
	if m.Rejected > 0 {
		errs = append(errs, fmt.Errorf("%d records rejected", m.Rejected))
	}
	collectErr := errors.Join(errs...)
	return errors.Join(collectErr, a.finish(ctx, run, m, collectErr))
}

func (a *app) collectFRED(ctx context.Context) error {
	c := fred.NewClient(a.http)
	m := metrics{Source: "fred", StartedAt: time.Now().UTC(), Cursor: map[string]any{}}
	run, skip, err := a.start(ctx, &m)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	var errs []error
	for _, series := range a.cfg.Providers.FRED.Series {
		r, err := c.Collect(ctx, series)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		m.Received += r.RecordsReceived
		m.Rejected += r.RecordsRejected
		key := rawKey("fred", "series", series, r.Raw, m.StartedAt, "csv")
		if stored, err := a.raw.Put(ctx, key, bytes.NewReader(r.Raw), storage.RawMetadata{Source: "fred", ContentType: "text/csv", FetchedAt: m.StartedAt, Attributes: map[string]string{"series_id": series, "vintage": "current"}}); err != nil {
			errs = append(errs, err)
			continue
		} else {
			m.RawObjects++
			m.RawBytes += stored.Size
		}
		path, n, err := a.normalized.WriteEconomics(series, r.Observations)
		if err != nil {
			errs = append(errs, err)
		} else {
			m.OutputRows += n
			m.Cursor["last_series_id"] = series
			a.log.Info("normalized dataset", "source", "fred", "series_id", series, "path", path, "rows", len(r.Observations))
		}
	}
	if m.Rejected > 0 {
		errs = append(errs, fmt.Errorf("%d records rejected", m.Rejected))
	}
	collectErr := errors.Join(errs...)
	return errors.Join(collectErr, a.finish(ctx, run, m, collectErr))
}

func (a *app) start(ctx context.Context, m *metrics) (metadata.Run, bool, error) {
	run, err := a.metadata.StartRun(ctx, m.Source, a.batchKey+"/"+m.Source, m.StartedAt)
	if err != nil {
		return metadata.Run{}, false, err
	}
	if run.Skip {
		a.log.Info("collector run already terminal; skipping", "source", m.Source, "run_key", run.RunKey, "status", run.Status)
		return run, true, nil
	}
	return run, false, nil
}
func (a *app) finish(ctx context.Context, run metadata.Run, m metrics, err error) error {
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
	if dbErr := a.metadata.FinishRun(finishCtx, run, time.Now().UTC(), metadata.Metrics{Received: int64(m.Received), Written: int64(m.OutputRows), Rejected: int64(m.Rejected), RawPayloads: int64(m.RawObjects), RawBytes: m.RawBytes, Cursor: m.Cursor, Err: err}); dbErr != nil {
		a.log.Error("persist collector run failed", "source", m.Source, "error", dbErr)
		return dbErr
	}
	a.log.Info("collector run", "source", m.Source, "status", status, "started_at", m.StartedAt, "duration_seconds", m.Duration.Seconds(), "records_received", m.Received, "output_rows_changed", m.OutputRows, "records_rejected", m.Rejected, "raw_objects", m.RawObjects)
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
