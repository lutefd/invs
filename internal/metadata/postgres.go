package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luisdourado/invs/config"
	"github.com/luisdourado/invs/internal/model"
)

type Repository struct{ pool *pgxpool.Pool }
type Run struct {
	ID, DataSourceID, Source, RunKey, Status string
	StartedAt                                time.Time
	Skip                                     bool
}
type Metrics struct {
	Received, Written, Rejected, RawPayloads int64
	RawBytes                                 int64
	Cursor                                   map[string]any
	Err                                      error
}

const upsertMarketPriceSnapshotSQL = `
INSERT INTO market_price_snapshots (
	data_source_id, security_id, ingestion_run_id, schema_version, interval,
	price_basis, currency, observed_at, published_at, available_at, ingested_at,
	published_precision, open_value, high_value, low_value, close_value,
	volume_value, raw_payload_hash
) VALUES (
	$1, $2, $3, $4, $5,
	$6, $7, $8, $9, $10, $11,
	$12, $13, $14, $15, $16,
	$17, $18
)
ON CONFLICT (data_source_id, security_id) DO UPDATE SET
	ingestion_run_id = EXCLUDED.ingestion_run_id,
	schema_version = EXCLUDED.schema_version,
	interval = EXCLUDED.interval,
	price_basis = EXCLUDED.price_basis,
	currency = EXCLUDED.currency,
	observed_at = EXCLUDED.observed_at,
	published_at = EXCLUDED.published_at,
	available_at = EXCLUDED.available_at,
	ingested_at = EXCLUDED.ingested_at,
	published_precision = EXCLUDED.published_precision,
	open_value = EXCLUDED.open_value,
	high_value = EXCLUDED.high_value,
	low_value = EXCLUDED.low_value,
	close_value = EXCLUDED.close_value,
	volume_value = EXCLUDED.volume_value,
	raw_payload_hash = EXCLUDED.raw_payload_hash,
	projected_at = clock_timestamp()
WHERE (
	EXCLUDED.observed_at,
	EXCLUDED.available_at,
	EXCLUDED.ingested_at,
	EXCLUDED.raw_payload_hash COLLATE "C"
) > (
	market_price_snapshots.observed_at,
	market_price_snapshots.available_at,
	market_price_snapshots.ingested_at,
	market_price_snapshots.raw_payload_hash COLLATE "C"
)`

const upsertMacroObservationSnapshotSQL = `
INSERT INTO macro_observation_snapshots (
	data_source_id, ingestion_run_id, schema_version, series_id, geography,
	unit, frequency, seasonal_adjustment, observed_at, published_at, available_at,
	ingested_at, published_precision, value, revision, vintage_at, raw_payload_hash
) VALUES (
	$1, $2, $3, $4, $5,
	$6, $7, $8, $9, $10, $11,
	$12, $13, $14, $15, $16, $17
)
ON CONFLICT (data_source_id, series_id) DO UPDATE SET
	ingestion_run_id = EXCLUDED.ingestion_run_id,
	schema_version = EXCLUDED.schema_version,
	geography = EXCLUDED.geography,
	unit = EXCLUDED.unit,
	frequency = EXCLUDED.frequency,
	seasonal_adjustment = EXCLUDED.seasonal_adjustment,
	observed_at = EXCLUDED.observed_at,
	published_at = EXCLUDED.published_at,
	available_at = EXCLUDED.available_at,
	ingested_at = EXCLUDED.ingested_at,
	published_precision = EXCLUDED.published_precision,
	value = EXCLUDED.value,
	revision = EXCLUDED.revision,
	vintage_at = EXCLUDED.vintage_at,
	raw_payload_hash = EXCLUDED.raw_payload_hash,
	projected_at = clock_timestamp()
WHERE (
	EXCLUDED.observed_at,
	EXCLUDED.revision,
	EXCLUDED.available_at,
	EXCLUDED.ingested_at,
	EXCLUDED.raw_payload_hash COLLATE "C"
) > (
	macro_observation_snapshots.observed_at,
	macro_observation_snapshots.revision,
	macro_observation_snapshots.available_at,
	macro_observation_snapshots.ingested_at,
	macro_observation_snapshots.raw_payload_hash COLLATE "C"
)`

const finishRunSQL = `
UPDATE ingestion_runs
SET status=$2::ingestion_run_status,
	finished_at=$3,
	records_received=$4,
	records_written=$5,
	records_rejected=$6,
	raw_payload_count=$7,
	raw_bytes=$8,
	error_message=$9,
	cursor=$10::jsonb
WHERE id=$1 AND status IN ('running','queued')`

type source struct {
	code, name, kind, baseURL string
	enabled                   bool
}

var sources = []source{{"sec", "SEC EDGAR", "fundamentals", "https://data.sec.gov", true}, {"yahoo", "Yahoo Finance", "market_data", "https://query1.finance.yahoo.com", true}, {"fred", "Federal Reserve Economic Data", "macro", "https://fred.stlouisfed.org", true}}

func Open(ctx context.Context, databaseURL string) (*Repository, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, nil
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("configure PostgreSQL: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect PostgreSQL: %w", err)
	}
	return &Repository{pool: pool}, nil
}
func (r *Repository) Close() {
	if r != nil {
		r.pool.Close()
	}
}

func (r *Repository) SyncCatalog(ctx context.Context, cfg config.Config) error {
	if r == nil {
		return nil
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, s := range sources {
		enabled := map[string]bool{"sec": cfg.Providers.SEC.Enabled, "yahoo": cfg.Providers.Prices.Enabled, "fred": cfg.Providers.FRED.Enabled}[s.code]
		_, err = tx.Exec(ctx, `INSERT INTO data_sources(code,name,source_kind,base_url,enabled) VALUES($1,$2,$3,$4,$5) ON CONFLICT(code) DO UPDATE SET name=excluded.name,source_kind=excluded.source_kind,base_url=excluded.base_url,enabled=excluded.enabled,updated_at=now()`, s.code, s.name, s.kind, s.baseURL, enabled)
		if err != nil {
			return fmt.Errorf("upsert data source %s: %w", s.code, err)
		}
	}
	for _, s := range cfg.Universe {
		identifierValidFrom, parseErr := time.Parse("2006-01-02", s.IdentifierValidFrom)
		if parseErr != nil {
			return fmt.Errorf("identifier valid_from %s: %w", s.SecurityID, parseErr)
		}
		cik := fmt.Sprintf("%010d", s.CIK)
		_, err = tx.Exec(ctx, `INSERT INTO issuers(id,legal_name,country_code,cik,metadata) VALUES($1,$2,$3,$4,'{}') ON CONFLICT(id) DO UPDATE SET legal_name=excluded.legal_name,country_code=excluded.country_code,cik=excluded.cik,updated_at=now()`, s.IssuerID, s.LegalName, s.CountryCode, cik)
		if err != nil {
			return fmt.Errorf("upsert issuer %s: %w", s.IssuerID, err)
		}
		_, err = tx.Exec(ctx, `INSERT INTO securities(id,issuer_id,name,security_type,exchange_mic,exchange_name,currency,primary_listing,metadata) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8,'{}') ON CONFLICT(id) DO UPDATE SET issuer_id=excluded.issuer_id,name=excluded.name,security_type=excluded.security_type,exchange_mic=excluded.exchange_mic,exchange_name=excluded.exchange_name,currency=excluded.currency,primary_listing=excluded.primary_listing,updated_at=now()`, s.SecurityID, s.IssuerID, s.LegalName, s.SecurityType, s.MIC, s.Exchange, s.Currency, s.PrimaryListing)
		if err != nil {
			return fmt.Errorf("upsert security %s: %w", s.SecurityID, err)
		}
		if err = syncIdentifier(ctx, tx, s.SecurityID, "ticker", s.Ticker, s.MIC, "yahoo", identifierValidFrom, true); err != nil {
			return err
		}
		if err = syncIdentifier(ctx, tx, s.SecurityID, "vendor_symbol", s.YahooSymbol, "yahoo", "yahoo", identifierValidFrom, false); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func syncIdentifier(ctx context.Context, tx pgx.Tx, securityID, kind, value, scope, source string, at time.Time, primary bool) error {
	if value == "" {
		return nil
	}
	normalized := strings.ToUpper(value)
	var currentID, currentValue string
	var currentFrom time.Time
	err := tx.QueryRow(ctx, `SELECT id::text,normalized_value,valid_from FROM security_identifiers WHERE security_id=$1 AND identifier_type=$2 AND identifier_scope=$3 AND valid_until IS NULL FOR UPDATE`, securityID, kind, scope).Scan(&currentID, &currentValue, &currentFrom)
	if err == nil && currentValue == normalized {
		if at.Before(currentFrom) {
			_, err = tx.Exec(ctx, `UPDATE security_identifiers SET valid_from=$2 WHERE id=$1`, currentID, at.UTC())
		}
		return err
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil {
		if !at.After(currentFrom) {
			return fmt.Errorf("new %s identifier validity must follow current version", kind)
		}
		if _, err = tx.Exec(ctx, `UPDATE security_identifiers SET valid_until=$2 WHERE id=$1`, currentID, at); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO security_identifiers(security_id,identifier_type,value,normalized_value,identifier_scope,valid_from,is_primary,data_source_id) SELECT $1,$2,$3,$4,$5,$6,$7,id FROM data_sources WHERE code=$8`, securityID, kind, value, normalized, scope, at, primary, source)
	return err
}

func (r *Repository) EnrichSECIssuer(ctx context.Context, issuer model.Issuer, state string) error {
	if r == nil {
		return nil
	}
	meta, _ := json.Marshal(map[string]any{"legal_name": issuer.LegalName, "sic_description": issuer.Industry, "state_of_incorporation": state, "observed_at": time.Now().UTC()})
	_, err := r.pool.Exec(ctx, `UPDATE issuers SET industry=COALESCE(industry,NULLIF($2,'')),metadata=jsonb_set(metadata,'{sec}',COALESCE(metadata->'sec','{}'::jsonb)||$3::jsonb,true),updated_at=now() WHERE id=$1`, issuer.ID, issuer.Industry, meta)
	return err
}

func (r *Repository) StartRun(ctx context.Context, source, runKey string, started time.Time) (Run, error) {
	if r == nil {
		return Run{}, errors.New("PostgreSQL metadata repository is required for canonical collection")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback(ctx)
	var run Run
	err = tx.QueryRow(ctx, `INSERT INTO ingestion_runs(data_source_id,run_key,status,started_at,metadata) SELECT id,$2::text,'running',$3::timestamptz,jsonb_build_object('collector_source',$1::text) FROM data_sources WHERE code=$1::text ON CONFLICT(data_source_id,run_key) DO NOTHING RETURNING id::text,data_source_id::text,status::text,started_at`, source, runKey, started.UTC()).Scan(&run.ID, &run.DataSourceID, &run.Status, &run.StartedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT r.id::text,r.data_source_id::text,r.status::text,r.started_at FROM ingestion_runs r JOIN data_sources s ON s.id=r.data_source_id WHERE s.code=$1 AND r.run_key=$2 FOR UPDATE`, source, runKey).Scan(&run.ID, &run.DataSourceID, &run.Status, &run.StartedAt)
		if err != nil {
			return Run{}, err
		}
		if run.Status == "running" || run.Status == "queued" {
			return Run{}, fmt.Errorf("run key %q for %s is already active", runKey, source)
		}
		if run.Status != "succeeded" {
			return Run{}, fmt.Errorf("run key %q for %s already finished as %s; use a new run key", runKey, source, run.Status)
		}
		run.Skip = true
	}
	if err != nil {
		return Run{}, err
	}
	run.Source, run.RunKey = source, runKey
	if err = tx.Commit(ctx); err != nil {
		return Run{}, err
	}
	return run, nil
}

func classify(m Metrics) string {
	if errors.Is(m.Err, context.Canceled) {
		return "cancelled"
	}
	if m.Err != nil {
		if m.Written > 0 || m.RawPayloads > 0 {
			return "partial"
		}
		return "failed"
	}
	if m.Rejected > 0 {
		return "partial"
	}
	return "succeeded"
}

// priceSnapshotIsNewer defines the price projection's total precedence order.
// A candidate wins by newer observed_at, then available_at, then ingested_at;
// if all three timestamps are equal, the lexicographically larger raw payload
// hash wins. published_at is retained as data but is not a precedence field.
func priceSnapshotIsNewer(candidate, current model.PriceBar) bool {
	if c := compareTime(candidate.Temporal.ObservedAt, current.Temporal.ObservedAt); c != 0 {
		return c > 0
	}
	if c := compareTime(candidate.Temporal.AvailableAt, current.Temporal.AvailableAt); c != 0 {
		return c > 0
	}
	if c := compareTime(candidate.Temporal.IngestedAt, current.Temporal.IngestedAt); c != 0 {
		return c > 0
	}
	return snapshotRawPayloadHash(candidate.RawPayloadHash, candidate.Provenance.RawPayloadHash) > snapshotRawPayloadHash(current.RawPayloadHash, current.Provenance.RawPayloadHash)
}

// macroSnapshotIsNewer defines the macro projection's total precedence order.
// A candidate wins by newer observed_at, then higher revision, then newer
// available_at and ingested_at; a larger raw payload hash breaks any remaining
// tie. This keeps an old observation from displacing a newer series value while
// still allowing same-observation revisions to advance the projection.
func macroSnapshotIsNewer(candidate, current model.EconomicObservation) bool {
	if c := compareTime(candidate.Temporal.ObservedAt, current.Temporal.ObservedAt); c != 0 {
		return c > 0
	}
	if candidate.Revision != current.Revision {
		return candidate.Revision > current.Revision
	}
	if c := compareTime(candidate.Temporal.AvailableAt, current.Temporal.AvailableAt); c != 0 {
		return c > 0
	}
	if c := compareTime(candidate.Temporal.IngestedAt, current.Temporal.IngestedAt); c != 0 {
		return c > 0
	}
	return snapshotRawPayloadHash(candidate.RawPayloadHash, candidate.Provenance.RawPayloadHash) > snapshotRawPayloadHash(current.RawPayloadHash, current.Provenance.RawPayloadHash)
}

func compareTime(a, b time.Time) int {
	if a.After(b) {
		return 1
	}
	if a.Before(b) {
		return -1
	}
	return 0
}

func snapshotRawPayloadHash(topLevel, provenance string) string {
	if topLevel != "" {
		return topLevel
	}
	return provenance
}

func snapshotBelongsToRun(run Run, p model.Provenance) bool {
	return p.DataSourceID == run.DataSourceID && p.IngestionRunID == run.ID
}

func validateSnapshotLineage(run Run, prices []model.PriceBar, macros []model.EconomicObservation) error {
	for i, candidate := range prices {
		if !snapshotBelongsToRun(run, candidate.Provenance) {
			return fmt.Errorf("price snapshot %d provenance does not match run %s/%s", i, run.DataSourceID, run.ID)
		}
	}
	for i, candidate := range macros {
		if !snapshotBelongsToRun(run, candidate.Provenance) {
			return fmt.Errorf("macro snapshot %d provenance does not match run %s/%s", i, run.DataSourceID, run.ID)
		}
	}
	return nil
}

func runErrorMessage(err error) *string {
	if err == nil {
		return nil
	}
	v := err.Error()
	if len(v) > 4000 {
		v = v[:4000]
	}
	return &v
}

// FinalizeRun atomically publishes accepted latest-only snapshots and moves an
// active ingestion run to its terminal status. Price candidates are ordered by
// observed_at, available_at, ingested_at, and raw_payload_hash. Macro candidates
// are ordered by observed_at, revision, available_at, ingested_at, and
// raw_payload_hash. In both cases the larger value at the first differing field
// wins, so older candidates cannot replace the current projection.
func (r *Repository) FinalizeRun(ctx context.Context, run Run, finished time.Time, m Metrics, prices []model.PriceBar, macros []model.EconomicObservation) error {
	if r == nil {
		return nil
	}
	if err := validateSnapshotLineage(run, prices, macros); err != nil {
		return err
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for i, candidate := range prices {
		_, err = tx.Exec(ctx, upsertMarketPriceSnapshotSQL,
			run.DataSourceID,
			candidate.SecurityID,
			run.ID,
			model.SchemaVersion,
			candidate.Interval,
			candidate.PriceBasis,
			candidate.Currency,
			candidate.Temporal.ObservedAt.UTC(),
			candidate.Temporal.PublishedAt.UTC(),
			candidate.Temporal.AvailableAt.UTC(),
			candidate.Temporal.IngestedAt.UTC(),
			string(candidate.Temporal.PublishedPrecision),
			candidate.Open,
			candidate.High,
			candidate.Low,
			candidate.Close,
			candidate.Volume,
			snapshotRawPayloadHash(candidate.RawPayloadHash, candidate.Provenance.RawPayloadHash),
		)
		if err != nil {
			return fmt.Errorf("upsert price snapshot %d: %w", i, err)
		}
	}

	for i, candidate := range macros {
		var seasonalAdjustment any
		if candidate.SeasonalAdjustment != "" {
			seasonalAdjustment = candidate.SeasonalAdjustment
		}
		var vintageAt any
		if candidate.VintageAt != nil {
			vintageAt = candidate.VintageAt.UTC()
		}
		_, err = tx.Exec(ctx, upsertMacroObservationSnapshotSQL,
			run.DataSourceID,
			run.ID,
			model.SchemaVersion,
			candidate.SeriesID,
			candidate.Geography,
			candidate.Unit,
			candidate.Frequency,
			seasonalAdjustment,
			candidate.Temporal.ObservedAt.UTC(),
			candidate.Temporal.PublishedAt.UTC(),
			candidate.Temporal.AvailableAt.UTC(),
			candidate.Temporal.IngestedAt.UTC(),
			string(candidate.Temporal.PublishedPrecision),
			candidate.Value,
			candidate.Revision,
			vintageAt,
			snapshotRawPayloadHash(candidate.RawPayloadHash, candidate.Provenance.RawPayloadHash),
		)
		if err != nil {
			return fmt.Errorf("upsert macro snapshot %d: %w", i, err)
		}
	}

	cursor, _ := json.Marshal(m.Cursor)
	tag, err := tx.Exec(ctx, finishRunSQL, run.ID, classify(m), finished.UTC(), m.Received, m.Written, m.Rejected, m.RawPayloads, m.RawBytes, runErrorMessage(m.Err), cursor)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("run %s is no longer active", run.ID)
	}
	return tx.Commit(ctx)
}

func (r *Repository) FinishRun(ctx context.Context, run Run, finished time.Time, m Metrics) error {
	return r.FinalizeRun(ctx, run, finished, m, nil, nil)
}
