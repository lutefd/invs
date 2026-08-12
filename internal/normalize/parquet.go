package normalize

import (
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/luisdourado/invs/internal/model"
	"github.com/parquet-go/parquet-go"
)

var ErrMigrationRequired = errors.New("normalized parquet migration required")
var ErrNaturalKeyConflict = errors.New("canonical natural key conflict")

type PriceRow struct {
	SchemaVersion      string `parquet:"schema_version"`
	Source             string `parquet:"source"`
	SecurityID         string `parquet:"security_id"`
	Interval           string `parquet:"interval"`
	PriceBasis         string `parquet:"price_basis"`
	Currency           string `parquet:"currency"`
	ObservedAt         int64  `parquet:"observed_at,timestamp(microsecond:utc)"`
	ObservedPrecision  string `parquet:"observed_precision"`
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

type FundamentalRow struct {
	SchemaVersion      string `parquet:"schema_version"`
	Source             string `parquet:"source"`
	IssuerID           string `parquet:"issuer_id"`
	SecurityID         string `parquet:"security_id"`
	HasSecurityID      bool   `parquet:"has_security_id"`
	Taxonomy           string `parquet:"taxonomy"`
	Concept            string `parquet:"concept"`
	Unit               string `parquet:"unit"`
	Currency           string `parquet:"currency"`
	HasCurrency        bool   `parquet:"has_currency"`
	ObservedAt         int64  `parquet:"observed_at,timestamp(microsecond:utc)"`
	ObservedPrecision  string `parquet:"observed_precision"`
	PublishedAt        int64  `parquet:"published_at,timestamp(microsecond:utc)"`
	PublishedPrecision string `parquet:"published_precision"`
	AvailableAt        int64  `parquet:"available_at,timestamp(microsecond:utc)"`
	IngestedAt         int64  `parquet:"ingested_at,timestamp(microsecond:utc)"`
	PeriodStart        int32  `parquet:"period_start,date"`
	HasPeriodStart     bool   `parquet:"has_period_start"`
	PeriodEnd          int32  `parquet:"period_end,date"`
	Value              string `parquet:"value"`
	HasValue           bool   `parquet:"has_value"`
	Revision           int32  `parquet:"revision"`
	AccessionNumber    string `parquet:"accession_number"`
	Form               string `parquet:"form"`
	FiscalYear         int32  `parquet:"fiscal_year"`
	FiscalPeriod       string `parquet:"fiscal_period"`
	Frame              string `parquet:"frame"`
	RawPayloadHash     string `parquet:"raw_payload_hash"`
	DataSourceID       string `parquet:"data_source_id"`
	IngestionRunID     string `parquet:"ingestion_run_id"`
	RawRecordLocator   string `parquet:"raw_record_locator"`
	NormalizerVersion  string `parquet:"normalizer_version"`
}

type EconomicRow struct {
	SchemaVersion         string `parquet:"schema_version"`
	Source                string `parquet:"source"`
	SeriesID              string `parquet:"series_id"`
	Geography             string `parquet:"geography"`
	Unit                  string `parquet:"unit"`
	Frequency             string `parquet:"frequency"`
	SeasonalAdjustment    string `parquet:"seasonal_adjustment"`
	HasSeasonalAdjustment bool   `parquet:"has_seasonal_adjustment"`
	ObservedAt            int64  `parquet:"observed_at,timestamp(microsecond:utc)"`
	ObservedPrecision     string `parquet:"observed_precision"`
	PublishedAt           int64  `parquet:"published_at,timestamp(microsecond:utc)"`
	PublishedPrecision    string `parquet:"published_precision"`
	AvailableAt           int64  `parquet:"available_at,timestamp(microsecond:utc)"`
	IngestedAt            int64  `parquet:"ingested_at,timestamp(microsecond:utc)"`
	Value                 string `parquet:"value"`
	HasValue              bool   `parquet:"has_value"`
	Revision              int32  `parquet:"revision"`
	VintageAt             int64  `parquet:"vintage_at,timestamp(microsecond:utc)"`
	HasVintageAt          bool   `parquet:"has_vintage_at"`
	RawPayloadHash        string `parquet:"raw_payload_hash"`
	DataSourceID          string `parquet:"data_source_id"`
	IngestionRunID        string `parquet:"ingestion_run_id"`
	RawRecordLocator      string `parquet:"raw_record_locator"`
	NormalizerVersion     string `parquet:"normalizer_version"`
}

func (r FundamentalRow) PeriodStartTime() *time.Time {
	if !r.HasPeriodStart {
		return nil
	}
	v := time.Unix(int64(r.PeriodStart)*86400, 0).UTC()
	return &v
}
func (r EconomicRow) VintageTime() *time.Time {
	if !r.HasVintageAt {
		return nil
	}
	v := time.UnixMicro(r.VintageAt).UTC()
	return &v
}

type Writer struct {
	root      string
	gitCommit string
	ops       publicationOps
}

func NewWriter(root string) (*Writer, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0o750); err != nil {
		return nil, err
	}
	return &Writer{root: abs, gitCommit: currentGitCommit(), ops: defaultPublicationOps()}, nil
}

// ValidateExisting checks every committed manifest and its listed parts before
// a collector starts network work. Legacy files are intentionally not migrated
// implicitly: their raw lineage cannot be reconstructed defensibly. Operators
// must archive/reset the normalized output and reingest from data/raw.
func (w *Writer) ValidateExisting() error {
	manifestPaths := make([]string, 0)
	parquetPaths := make([]string, 0)
	err := filepath.WalkDir(w.root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		switch entry.Name() {
		case ManifestFilename:
			manifestPaths = append(manifestPaths, path)
		case "data.parquet":
			return fmt.Errorf("legacy normalized file %s is not a committed manifest part: %w; archive/reset normalized output and reingest from preserved raw evidence", path, ErrMigrationRequired)
		default:
			if filepath.Ext(entry.Name()) == PartFilenameSuffix {
				parquetPaths = append(parquetPaths, path)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	manifestDirs := make(map[string]struct{}, len(manifestPaths))
	for _, path := range manifestPaths {
		manifestDirs[filepath.Dir(path)] = struct{}{}
	}
	for _, path := range parquetPaths {
		if _, ok := manifestDirs[filepath.Dir(path)]; !ok {
			return fmt.Errorf("uncommitted normalized Parquet file %s has no %s: %w; archive/reset normalized output and reingest from preserved raw evidence", path, ManifestFilename, ErrMigrationRequired)
		}
	}
	for _, path := range manifestPaths {
		dir := filepath.Dir(path)
		partition, err := partitionIdentityFromDir(w.root, dir)
		if err != nil {
			return fmt.Errorf("validate normalized manifest %s: %w: %v", path, ErrMigrationRequired, err)
		}
		var readErr error
		switch partition["dataset"] {
		case "prices":
			_, readErr = readCommitted[PriceRow](dir, partition)
		case "fundamentals":
			_, readErr = readCommitted[FundamentalRow](dir, partition)
		case "macroeconomics":
			_, readErr = readCommitted[EconomicRow](dir, partition)
		default:
			readErr = fmt.Errorf("unsupported normalized dataset directory %q", partition["dataset"])
		}
		if readErr != nil {
			return fmt.Errorf("validate normalized file %s: %w; archive/reset normalized output and reingest from preserved raw evidence", path, readErr)
		}
	}
	return nil
}

func (w *Writer) WritePrices(securityID string, obs []model.PriceBar) (string, int, error) {
	if _, err := uuid.Parse(securityID); err != nil {
		return "", 0, fmt.Errorf("security ID must be UUID: %w", err)
	}
	source := sourceOfPrices(obs)
	for _, o := range obs {
		if o.SecurityID != securityID || o.Source != source {
			return "", 0, errors.New("price observation/path identity mismatch")
		}
	}
	dir, err := w.partition("prices", "source="+source, "security_id="+securityID)
	if err != nil {
		return "", 0, err
	}
	in := make([]PriceRow, 0, len(obs))
	for _, o := range obs {
		r, err := priceRow(o)
		if err != nil {
			return "", 0, err
		}
		in = append(in, r)
	}
	partition := map[string]string{"dataset": "prices", "source": source, "security_id": securityID}
	existing, err := readCommitted[PriceRow](dir, partition)
	if err != nil {
		return "", 0, fmt.Errorf("read existing prices: %w", err)
	}
	for _, r := range existing {
		if r.Source != source || r.SecurityID != securityID {
			return "", 0, fmt.Errorf("read existing prices: %w: partition identity mismatch", ErrMigrationRequired)
		}
	}
	rows, err := merge(existing, in, priceKey, samePrice)
	if err != nil {
		return "", 0, err
	}
	sort.Slice(rows, func(i, j int) bool { return priceKey(rows[i]) < priceKey(rows[j]) })
	metadata, err := metadataFromRows(in, func(r PriceRow) publicationMetadata {
		return publicationMetadata{Source: r.Source, DataSourceID: r.DataSourceID, IngestionRunID: r.IngestionRunID, NormalizerVersion: r.NormalizerVersion}
	})
	if err != nil && !slices.Equal(existing, rows) {
		return "", 0, err
	}
	return publish(w, dir, partition, existing, rows, metadata)
}
func (w *Writer) WriteFundamentals(issuerID string, obs []model.FundamentalObservation) (string, int, error) {
	if _, err := uuid.Parse(issuerID); err != nil {
		return "", 0, fmt.Errorf("issuer ID must be UUID: %w", err)
	}
	dir, err := w.partition("fundamentals", "source=sec", "issuer_id="+issuerID)
	if err != nil {
		return "", 0, err
	}
	for _, o := range obs {
		if o.IssuerID != issuerID || o.Source != "sec" {
			return "", 0, errors.New("fundamental observation/path identity mismatch")
		}
	}
	in := make([]FundamentalRow, 0, len(obs))
	for _, o := range obs {
		r, err := fundamentalRow(o)
		if err != nil {
			return "", 0, err
		}
		in = append(in, r)
	}
	partition := map[string]string{"dataset": "fundamentals", "source": "sec", "issuer_id": issuerID}
	existing, err := readCommitted[FundamentalRow](dir, partition)
	if err != nil {
		return "", 0, fmt.Errorf("read existing fundamentals: %w", err)
	}
	for _, r := range existing {
		if r.Source != "sec" || r.IssuerID != issuerID {
			return "", 0, fmt.Errorf("read existing fundamentals: %w: partition identity mismatch", ErrMigrationRequired)
		}
	}
	rows, err := merge(existing, in, fundamentalKey, sameFundamental)
	if err != nil {
		return "", 0, err
	}
	sort.Slice(rows, func(i, j int) bool { return fundamentalKey(rows[i]) < fundamentalKey(rows[j]) })
	metadata, err := metadataFromRows(in, func(r FundamentalRow) publicationMetadata {
		return publicationMetadata{Source: r.Source, DataSourceID: r.DataSourceID, IngestionRunID: r.IngestionRunID, NormalizerVersion: r.NormalizerVersion}
	})
	if err != nil && !slices.Equal(existing, rows) {
		return "", 0, err
	}
	return publish(w, dir, partition, existing, rows, metadata)
}

// WriteEconomics updates obs revisions in place after a successful write so
// callers can publish the same effective revisions in their snapshots. The
// input slice is unchanged when validation, conflict detection, or publishing
// fails.
func (w *Writer) WriteEconomics(seriesID string, obs []model.EconomicObservation) (string, int, error) {
	dir, err := w.partition("macroeconomics", "source=fred", "series_id="+seriesID)
	if err != nil {
		return "", 0, err
	}
	for _, o := range obs {
		if o.SeriesID != seriesID || o.Source != "fred" {
			return "", 0, errors.New("economic observation/path identity mismatch")
		}
	}
	partition := map[string]string{"dataset": "macroeconomics", "source": "fred", "series_id": seriesID}
	existing, err := readCommitted[EconomicRow](dir, partition)
	if err != nil {
		return "", 0, fmt.Errorf("read existing macroeconomics: %w", err)
	}
	for _, r := range existing {
		if r.Source != "fred" || r.SeriesID != seriesID {
			return "", 0, fmt.Errorf("read existing macroeconomics: %w: partition identity mismatch", ErrMigrationRequired)
		}
	}
	in := make([]EconomicRow, 0, len(obs))
	effectiveRevisions := make([]int, len(obs))
	for i, o := range obs {
		r, err := economicRow(o)
		if err != nil {
			return "", 0, err
		}
		latest, found := latestEconomic(existing, r)
		if found && latest.Value == r.Value {
			if economicKey(latest) == economicKey(r) && !sameEconomic(latest, r) {
				return "", 0, fmt.Errorf("%w: %s", ErrNaturalKeyConflict, economicKey(r))
			}
			effectiveRevisions[i] = int(latest.Revision)
			continue
		}
		if found && r.Revision <= latest.Revision {
			r.Revision = latest.Revision + 1
		}
		effectiveRevisions[i] = int(r.Revision)
		in = append(in, r)
	}
	rows, err := merge(existing, in, economicKey, sameEconomic)
	if err != nil {
		return "", 0, err
	}
	sort.Slice(rows, func(i, j int) bool { return economicKey(rows[i]) < economicKey(rows[j]) })
	metadata, err := metadataFromRows(in, func(r EconomicRow) publicationMetadata {
		return publicationMetadata{Source: r.Source, DataSourceID: r.DataSourceID, IngestionRunID: r.IngestionRunID, NormalizerVersion: r.NormalizerVersion}
	})
	if err != nil && !slices.Equal(existing, rows) {
		return "", 0, err
	}
	path, n, err := publish(w, dir, partition, existing, rows, metadata)
	if err != nil {
		return "", 0, err
	}
	for i := range obs {
		obs[i].Revision = effectiveRevisions[i]
	}
	return path, n, nil
}

func priceRow(o model.PriceBar) (PriceRow, error) {
	observedAt, publishedAt, availableAt, ingestedAt, err := temporalMicros(o.Temporal, !o.Temporal.PublishedAt.IsZero())
	if err != nil {
		return PriceRow{}, err
	}
	if err := validateProvenance(o.Provenance, o.RawPayloadHash); err != nil {
		return PriceRow{}, err
	}
	if !o.Provenance.IngestedAt.Equal(o.Temporal.IngestedAt) {
		return PriceRow{}, errors.New("provenance/temporal ingested_at mismatch")
	}
	if !isCurrency(o.Currency) {
		return PriceRow{}, errors.New("invalid price currency")
	}
	if o.Source == "" {
		return PriceRow{}, errors.New("price source required")
	}
	if o.Interval != "1d" || o.PriceBasis != "raw" {
		return PriceRow{}, errors.New("price interval/basis must be 1d/raw")
	}
	if _, err := uuid.Parse(o.SecurityID); err != nil {
		return PriceRow{}, errors.New("security_id must be UUID")
	}
	open, err := model.CanonicalDecimal(o.Open, true)
	if err != nil {
		return PriceRow{}, err
	}
	high, err := model.CanonicalDecimal(o.High, true)
	if err != nil {
		return PriceRow{}, err
	}
	low, err := model.CanonicalDecimal(o.Low, true)
	if err != nil {
		return PriceRow{}, err
	}
	closeValue, err := model.CanonicalDecimal(o.Close, true)
	if err != nil {
		return PriceRow{}, err
	}
	volume := ""
	if o.Volume != "" {
		volume, err = model.CanonicalDecimal(o.Volume, true)
		if err != nil {
			return PriceRow{}, err
		}
	}
	if compareDecimal(low, high) > 0 || compareDecimal(open, low) < 0 || compareDecimal(open, high) > 0 || compareDecimal(closeValue, low) < 0 || compareDecimal(closeValue, high) > 0 {
		return PriceRow{}, errors.New("invalid OHLC invariant")
	}
	observedPrecision := normalizeObservedPrecision(o.Temporal.ObservedPrecision)
	temporal := o.Temporal
	temporal.ObservedPrecision = observedPrecision
	if err := validateTemporal(temporal, !o.Temporal.PublishedAt.IsZero()); err != nil {
		return PriceRow{}, err
	}
	r := PriceRow{SchemaVersion: model.SchemaVersion, Source: o.Source, SecurityID: o.SecurityID, Interval: o.Interval, PriceBasis: o.PriceBasis, Currency: o.Currency, ObservedAt: observedAt, ObservedPrecision: string(observedPrecision), PublishedPrecision: string(o.Temporal.PublishedPrecision), AvailableAt: availableAt, IngestedAt: ingestedAt, Open: open, High: high, Low: low, Close: closeValue, Volume: volume, HasVolume: o.Volume != ""}
	if !o.Temporal.PublishedAt.IsZero() {
		r.PublishedAt = publishedAt
		r.HasPublishedAt = true
	}
	stamp(&r.RawPayloadHash, &r.DataSourceID, &r.IngestionRunID, &r.RawRecordLocator, &r.NormalizerVersion, o.Provenance, o.RawPayloadHash)
	return r, nil
}
func fundamentalRow(o model.FundamentalObservation) (FundamentalRow, error) {
	observedAt, publishedAt, availableAt, ingestedAt, err := temporalMicros(o.Temporal, true)
	if err != nil {
		return FundamentalRow{}, err
	}
	if err := validateProvenance(o.Provenance, o.RawPayloadHash); err != nil {
		return FundamentalRow{}, err
	}
	if !o.Provenance.IngestedAt.Equal(o.Temporal.IngestedAt) {
		return FundamentalRow{}, errors.New("provenance/temporal ingested_at mismatch")
	}
	if _, err := uuid.Parse(o.IssuerID); err != nil {
		return FundamentalRow{}, errors.New("issuer_id must be UUID")
	}
	if o.SecurityID != "" {
		if _, err := uuid.Parse(o.SecurityID); err != nil {
			return FundamentalRow{}, errors.New("security_id must be UUID")
		}
	}
	if o.Currency != "" && !isCurrency(o.Currency) {
		return FundamentalRow{}, errors.New("invalid fundamental currency")
	}
	if o.Source == "" || o.Taxonomy == "" || o.Concept == "" || o.Unit == "" || !validFiscalPeriod(o.FiscalPeriod) || o.Revision < 0 {
		return FundamentalRow{}, errors.New("invalid fundamental domain fields")
	}
	if !sameUTCDay(o.PeriodEnd, o.Temporal.ObservedAt) || o.PeriodEnd.After(o.Temporal.PublishedAt) {
		return FundamentalRow{}, errors.New("invalid fundamental period temporal semantics")
	}
	value, err := model.CanonicalDecimal(o.Value, false)
	if err != nil {
		return FundamentalRow{}, err
	}
	var start int32
	hasStart := o.PeriodStart != nil
	if hasStart {
		start = days(*o.PeriodStart)
		if start > days(o.PeriodEnd) {
			return FundamentalRow{}, errors.New("period_start after period_end")
		}
	}
	observedPrecision := normalizeObservedPrecision(o.Temporal.ObservedPrecision)
	temporal := o.Temporal
	temporal.ObservedPrecision = observedPrecision
	if err := validateTemporal(temporal, true); err != nil {
		return FundamentalRow{}, err
	}
	r := FundamentalRow{SchemaVersion: model.SchemaVersion, Source: o.Source, IssuerID: o.IssuerID, SecurityID: o.SecurityID, HasSecurityID: o.SecurityID != "", Taxonomy: o.Taxonomy, Concept: o.Concept, Unit: o.Unit, Currency: o.Currency, HasCurrency: o.Currency != "", ObservedAt: observedAt, ObservedPrecision: string(observedPrecision), PublishedAt: publishedAt, PublishedPrecision: string(o.Temporal.PublishedPrecision), AvailableAt: availableAt, IngestedAt: ingestedAt, PeriodStart: start, HasPeriodStart: hasStart, PeriodEnd: days(o.PeriodEnd), Value: value, HasValue: o.Value != "", Revision: int32(o.Revision), AccessionNumber: o.AccessionNumber, Form: o.Form, FiscalYear: int32(o.FiscalYear), FiscalPeriod: o.FiscalPeriod, Frame: o.Frame}
	stamp(&r.RawPayloadHash, &r.DataSourceID, &r.IngestionRunID, &r.RawRecordLocator, &r.NormalizerVersion, o.Provenance, o.RawPayloadHash)
	return r, nil
}
func economicRow(o model.EconomicObservation) (EconomicRow, error) {
	observedAt, publishedAt, availableAt, ingestedAt, err := temporalMicros(o.Temporal, true)
	if err != nil {
		return EconomicRow{}, err
	}
	if err := validateProvenance(o.Provenance, o.RawPayloadHash); err != nil {
		return EconomicRow{}, err
	}
	if !o.Provenance.IngestedAt.Equal(o.Temporal.IngestedAt) {
		return EconomicRow{}, errors.New("provenance/temporal ingested_at mismatch")
	}
	if o.Geography == "" || !validFrequency(o.Frequency) {
		return EconomicRow{}, errors.New("invalid macro geography/frequency")
	}
	if o.Source == "" || o.SeriesID == "" || o.Unit == "" || o.Revision < 0 {
		return EconomicRow{}, errors.New("invalid economic domain fields")
	}
	value, err := model.CanonicalDecimal(o.Value, false)
	if err != nil {
		return EconomicRow{}, err
	}
	observedPrecision := normalizeObservedPrecision(o.Temporal.ObservedPrecision)
	temporal := o.Temporal
	temporal.ObservedPrecision = observedPrecision
	if err := validateTemporal(temporal, true); err != nil {
		return EconomicRow{}, err
	}
	r := EconomicRow{SchemaVersion: model.SchemaVersion, Source: o.Source, SeriesID: o.SeriesID, Geography: o.Geography, Unit: o.Unit, Frequency: o.Frequency, SeasonalAdjustment: o.SeasonalAdjustment, HasSeasonalAdjustment: o.SeasonalAdjustment != "", ObservedAt: observedAt, ObservedPrecision: string(observedPrecision), PublishedAt: publishedAt, PublishedPrecision: string(o.Temporal.PublishedPrecision), AvailableAt: availableAt, IngestedAt: ingestedAt, Value: value, HasValue: o.Value != "", Revision: int32(o.Revision)}
	if o.VintageAt != nil {
		r.VintageAt, err = micros(*o.VintageAt)
		if err != nil {
			return EconomicRow{}, fmt.Errorf("vintage_at: %w", err)
		}
		r.HasVintageAt = true
	}
	stamp(&r.RawPayloadHash, &r.DataSourceID, &r.IngestionRunID, &r.RawRecordLocator, &r.NormalizerVersion, o.Provenance, o.RawPayloadHash)
	return r, nil
}

func stamp(hash, dataSource, run, locator, normalizer *string, p model.Provenance, legacyHash string) {
	*hash = p.RawPayloadHash
	if *hash == "" {
		*hash = legacyHash
	}
	*dataSource = p.DataSourceID
	*run = p.IngestionRunID
	*locator = p.RawRecordLocator
	*normalizer = p.NormalizerVersion
}
func validateProvenance(p model.Provenance, legacyHash string) error {
	if legacyHash == "" || p.RawPayloadHash == "" || legacyHash != p.RawPayloadHash {
		return errors.New("top-level/provenance raw payload hash mismatch")
	}
	hash := p.RawPayloadHash
	if len(hash) != 64 {
		return errors.New("provenance raw_payload_hash must be SHA-256")
	}
	for _, c := range hash {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return errors.New("provenance raw_payload_hash must be lowercase hex")
		}
	}
	if _, err := uuid.Parse(p.DataSourceID); err != nil {
		return errors.New("provenance data_source_id must be UUID")
	}
	if _, err := uuid.Parse(p.IngestionRunID); err != nil {
		return errors.New("provenance ingestion_run_id must be UUID")
	}
	if p.IngestedAt.IsZero() {
		return errors.New("provenance ingested_at required")
	}
	if p.NormalizerVersion == "" {
		return errors.New("normalizer_version required")
	}
	return nil
}
func validateTemporal(t model.Temporal, publishedRequired bool) error {
	if t.ObservedAt.IsZero() || t.AvailableAt.IsZero() || t.IngestedAt.IsZero() {
		return errors.New("required temporal field is zero")
	}
	observedPrecision := normalizeObservedPrecision(t.ObservedPrecision)
	switch observedPrecision {
	case model.PrecisionDate:
		observed := t.ObservedAt.UTC()
		if observed.Hour() != 0 || observed.Minute() != 0 || observed.Second() != 0 || observed.Nanosecond() != 0 {
			return errors.New("date observed_precision requires observed_at at UTC midnight")
		}
	case model.PrecisionSecond:
		if t.ObservedAt.Nanosecond() != 0 {
			return errors.New("second observed_precision requires whole-second observed_at")
		}
	case model.PrecisionUnknown:
	default:
		return fmt.Errorf("invalid observed_precision %q", t.ObservedPrecision)
	}
	if publishedRequired && t.PublishedAt.IsZero() {
		return errors.New("published_at required")
	}
	if !t.PublishedAt.IsZero() && t.ObservedAt.After(t.PublishedAt) {
		return errors.New("observed_at after published_at")
	}
	if !t.PublishedAt.IsZero() && t.PublishedAt.After(t.AvailableAt) {
		return errors.New("published_at after available_at")
	}
	if t.AvailableAt.After(t.IngestedAt) {
		return errors.New("available_at after ingested_at")
	}
	return nil
}

func normalizeObservedPrecision(precision model.TimePrecision) model.TimePrecision {
	if precision == "" {
		return model.PrecisionUnknown
	}
	return precision
}
func isCurrency(v string) bool {
	if len(v) != 3 {
		return false
	}
	for _, r := range v {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}
func validFrequency(v string) bool {
	switch v {
	case "daily", "weekly", "monthly", "quarterly", "semiannual", "annual", "irregular":
		return true
	}
	return false
}
func validFiscalPeriod(v string) bool {
	switch v {
	case "FY", "Q1", "Q2", "Q3", "Q4", "H1", "H2", "YTD", "instant", "other":
		return true
	}
	return false
}
func sameUTCDay(a, b time.Time) bool {
	a = a.UTC()
	b = b.UTC()
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}
func sourceOfPrices(obs []model.PriceBar) string {
	if len(obs) == 0 {
		return "unknown"
	}
	return obs[0].Source
}
func temporalMicros(t model.Temporal, publishedRequired bool) (observed, published, available, ingested int64, err error) {
	if err = validateTemporal(t, publishedRequired); err != nil {
		return 0, 0, 0, 0, err
	}
	if observed, err = micros(t.ObservedAt); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("observed_at: %w", err)
	}
	if !t.PublishedAt.IsZero() {
		if published, err = micros(t.PublishedAt); err != nil {
			return 0, 0, 0, 0, fmt.Errorf("published_at: %w", err)
		}
	}
	if available, err = micros(t.AvailableAt); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("available_at: %w", err)
	}
	if ingested, err = micros(t.IngestedAt); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("ingested_at: %w", err)
	}
	return observed, published, available, ingested, nil
}
func micros(t time.Time) (int64, error) {
	if t.Nanosecond()%int(time.Microsecond) != 0 {
		return 0, errors.New("timestamp has sub-microsecond precision")
	}
	return t.UTC().UnixMicro(), nil
}
func days(t time.Time) int32 { return int32(t.UTC().Unix() / 86400) }
func priceKey(r PriceRow) string {
	return strings.Join([]string{r.Source, r.SecurityID, r.Interval, fmt.Sprint(r.ObservedAt), r.PriceBasis}, "\x1f")
}
func fundamentalKey(r FundamentalRow) string {
	return strings.Join([]string{r.Source, r.IssuerID, r.SecurityID, r.Taxonomy, r.Concept, r.Unit, fmt.Sprint(r.HasPeriodStart), fmt.Sprint(r.PeriodStart), fmt.Sprint(r.PeriodEnd), fmt.Sprint(r.PublishedAt), fmt.Sprint(r.Revision)}, "\x1f")
}
func economicKey(r EconomicRow) string {
	return strings.Join([]string{r.Source, r.SeriesID, fmt.Sprint(r.ObservedAt), fmt.Sprint(r.PublishedAt), fmt.Sprint(r.Revision)}, "\x1f")
}
func economicSeriesKey(r EconomicRow) string {
	return strings.Join([]string{r.Source, r.SeriesID, fmt.Sprint(r.ObservedAt)}, "\x1f")
}
func latestEconomic(rows []EconomicRow, r EconomicRow) (EconomicRow, bool) {
	var best EconomicRow
	ok := false
	for _, v := range rows {
		if economicSeriesKey(v) == economicSeriesKey(r) && (!ok || v.Revision > best.Revision) {
			best = v
			ok = true
		}
	}
	return best, ok
}
func samePrice(a, b PriceRow) bool {
	if a.RawPayloadHash != b.RawPayloadHash {
		return false
	}
	a.PublishedAt = b.PublishedAt
	a.AvailableAt = b.AvailableAt
	a.IngestionRunID = b.IngestionRunID
	a.IngestedAt = b.IngestedAt
	return a == b
}
func sameFundamental(a, b FundamentalRow) bool {
	a.IngestionRunID = b.IngestionRunID
	a.IngestedAt = b.IngestedAt
	return a == b
}
func sameEconomic(a, b EconomicRow) bool {
	a.IngestionRunID = b.IngestionRunID
	a.IngestedAt = b.IngestedAt
	return a == b
}
func merge[T any](existing, in []T, key func(T) string, equal func(T, T) bool) ([]T, error) {
	m := map[string]T{}
	for _, r := range existing {
		m[key(r)] = r
	}
	for _, r := range in {
		k := key(r)
		if old, ok := m[k]; ok {
			if !equal(old, r) {
				return nil, fmt.Errorf("%w: %s", ErrNaturalKeyConflict, k)
			}
			continue
		}
		m[k] = r
	}
	out := make([]T, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out, nil
}
func compareDecimal(a, b string) int {
	ra, _ := new(big.Rat).SetString(a)
	rb, _ := new(big.Rat).SetString(b)
	return ra.Cmp(rb)
}

func (w *Writer) partition(parts ...string) (string, error) {
	for _, p := range parts {
		if p == "" || p == "." || p == ".." || strings.ContainsAny(p, "/\\\x00") {
			return "", fmt.Errorf("unsafe normalized path segment %q", p)
		}
	}
	return filepath.Join(append([]string{w.root}, parts...)...), nil
}

func (w *Writer) path(parts ...string) (string, error) {
	dir, err := w.partition(parts...)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ManifestFilename), nil
}

func metadataFromRows[T any](rows []T, metadata func(T) publicationMetadata) (publicationMetadata, error) {
	if len(rows) == 0 {
		return publicationMetadata{}, errors.New("manifest metadata requires at least one input row")
	}
	want := metadata(rows[0])
	if want.Source == "" || want.DataSourceID == "" || want.IngestionRunID == "" || want.NormalizerVersion == "" {
		return publicationMetadata{}, errors.New("manifest metadata fields required")
	}
	for _, row := range rows[1:] {
		got := metadata(row)
		if got != want {
			return publicationMetadata{}, errors.New("manifest metadata differs within publication")
		}
	}
	return want, nil
}

func readCommitted[T any](dir string, expectedPartition map[string]string) ([]T, error) {
	legacyPath := filepath.Join(dir, "data.parquet")
	if _, err := os.Stat(legacyPath); err == nil {
		return nil, fmt.Errorf("%w: legacy normalized file %s is not a committed manifest part", ErrMigrationRequired, legacyPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	manifestPath := filepath.Join(dir, ManifestFilename)
	manifest, present, err := readManifestIfPresent(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMigrationRequired, err)
	}
	if !present {
		files, err := parquetFilesInDirectory(dir)
		if err != nil {
			return nil, err
		}
		if len(files) != 0 {
			return nil, fmt.Errorf("%w: normalized Parquet files in %s have no committed %s", ErrMigrationRequired, dir, ManifestFilename)
		}
		return nil, nil
	}
	if err := samePartitionIdentity(manifest.Partition, expectedPartition); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMigrationRequired, err)
	}
	rows := make([]T, 0, manifest.RowCount)
	seen := make(map[string]struct{}, manifest.RowCount)
	for _, part := range manifest.Parts {
		partPath := filepath.Join(dir, part.Path)
		actualHash, err := sha256File(partPath)
		if err != nil {
			return nil, fmt.Errorf("%w: read committed part %s: %v", ErrMigrationRequired, partPath, err)
		}
		if actualHash != part.SHA256 {
			return nil, fmt.Errorf("%w: sha256 mismatch for committed part %s", ErrMigrationRequired, partPath)
		}
		partRows, err := readV1[T](partPath)
		if err != nil {
			return nil, err
		}
		if len(partRows) != part.RowCount {
			return nil, fmt.Errorf("%w: manifest row_count %d does not match %s row count %d", ErrMigrationRequired, part.RowCount, partPath, len(partRows))
		}
		for _, row := range partRows {
			key, err := validateExistingRow(row)
			if err != nil {
				return nil, fmt.Errorf("%w: invalid committed row in %s: %v", ErrMigrationRequired, partPath, err)
			}
			if _, ok := seen[key]; ok {
				return nil, fmt.Errorf("%w: duplicate natural key in committed parts: %s", ErrMigrationRequired, key)
			}
			seen[key] = struct{}{}
			if err := validateRowPartition(row, expectedPartition); err != nil {
				return nil, fmt.Errorf("%w: committed partition mismatch in %s: %v", ErrMigrationRequired, partPath, err)
			}
		}
		rows = append(rows, partRows...)
	}
	if len(rows) != manifest.RowCount {
		return nil, fmt.Errorf("%w: manifest row_count %d does not match committed row count %d", ErrMigrationRequired, manifest.RowCount, len(rows))
	}
	return rows, nil
}

func samePartitionIdentity(got, want map[string]string) error {
	if len(got) != len(want) {
		return fmt.Errorf("partition identity differs: got %v want %v", got, want)
	}
	for key, value := range want {
		if got[key] != value {
			return fmt.Errorf("partition identity differs for %q: got %q want %q", key, got[key], value)
		}
	}
	return nil
}

func validateRowPartition[T any](row T, partition map[string]string) error {
	source := partition["source"]
	switch r := any(row).(type) {
	case PriceRow:
		if r.Source != source || r.SecurityID != partition["security_id"] {
			return errors.New("price row does not match partition identity")
		}
	case FundamentalRow:
		if r.Source != source || r.IssuerID != partition["issuer_id"] {
			return errors.New("fundamental row does not match partition identity")
		}
	case EconomicRow:
		if r.Source != source || r.SeriesID != partition["series_id"] {
			return errors.New("economic row does not match partition identity")
		}
	default:
		return errors.New("unsupported normalized row type")
	}
	return nil
}
func readV1[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	pf, err := parquet.OpenFile(f, st.Size())
	if err != nil {
		return nil, err
	}
	if err := validatePhysicalSchema[T](pf.Schema(), path); err != nil {
		return nil, err
	}
	rows, err := parquet.Read[T](f, st.Size())
	if err != nil {
		return nil, fmt.Errorf("%w: cannot decode %s: %v", ErrMigrationRequired, path, err)
	}
	seen := make(map[string]struct{}, len(rows))
	for i := range rows {
		defaultStoredObservedPrecision(&rows[i])
		r := rows[i]
		key, err := validateExistingRow(r)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid row in %s: %v", ErrMigrationRequired, path, err)
		}
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("%w: duplicate natural key in %s: %s", ErrMigrationRequired, path, key)
		}
		seen[key] = struct{}{}
	}
	return rows, nil
}

func validatePhysicalSchema[T any](actual *parquet.Schema, path string) error {
	expected := parquet.SchemaOf(new(T))
	for _, column := range expected.Columns() {
		if len(column) != 1 {
			continue
		}
		name := column[0]
		actualColumn, present := actual.Lookup(name)
		if !present {
			if name == "observed_precision" {
				continue
			}
			return fmt.Errorf("%w: %s has no %s column", ErrMigrationRequired, path, name)
		}
		expectedColumn, ok := expected.Lookup(name)
		actualNode := strings.TrimSpace(actualColumn.Node.String())
		if index := strings.Index(actualNode, ":"); index >= 0 {
			actualNode = strings.TrimSpace(actualNode[index+1:])
		}
		expectedNode := strings.TrimSpace(expectedColumn.Node.String())
		if !ok || actualNode != expectedNode {
			return fmt.Errorf("%w: %s has incompatible %s column type: got %s want %s", ErrMigrationRequired, path, name, actualColumn.Node.String(), expectedColumn.Node.String())
		}
	}
	return nil
}

func defaultStoredObservedPrecision[T any](row *T) {
	switch r := any(row).(type) {
	case *PriceRow:
		if r.ObservedPrecision == "" {
			r.ObservedPrecision = string(model.PrecisionUnknown)
		}
	case *FundamentalRow:
		if r.ObservedPrecision == "" {
			r.ObservedPrecision = string(model.PrecisionUnknown)
		}
	case *EconomicRow:
		if r.ObservedPrecision == "" {
			r.ObservedPrecision = string(model.PrecisionUnknown)
		}
	}
}

func validateExistingRow[T any](row T) (string, error) {
	switch r := any(row).(type) {
	case PriceRow:
		return priceKey(r), validateStoredPrice(r)
	case FundamentalRow:
		return fundamentalKey(r), validateStoredFundamental(r)
	case EconomicRow:
		return economicKey(r), validateStoredEconomic(r)
	default:
		return "", errors.New("unsupported normalized row type")
	}
}

func validateStoredPrice(r PriceRow) error {
	if err := validateStoredCommon(r.SchemaVersion, r.Source, r.RawPayloadHash, r.DataSourceID, r.IngestionRunID, r.NormalizerVersion); err != nil {
		return err
	}
	if _, err := uuid.Parse(r.SecurityID); err != nil {
		return errors.New("security_id must be UUID")
	}
	if r.Interval != "1d" || r.PriceBasis != "raw" || !isCurrency(r.Currency) {
		return errors.New("invalid price domain fields")
	}
	if r.HasPublishedAt != (r.PublishedAt != 0) {
		return errors.New("published_at presence mismatch")
	}
	temporal := storedTemporal(r.ObservedAt, r.PublishedAt, r.ObservedPrecision, r.PublishedPrecision, r.AvailableAt, r.IngestedAt, r.HasPublishedAt)
	if err := validateTemporal(temporal, false); err != nil {
		return err
	}
	if r.HasVolume != (r.Volume != "") {
		return errors.New("volume presence mismatch")
	}
	for _, v := range []string{r.Open, r.High, r.Low, r.Close} {
		if err := validateCanonicalDecimal(v, true); err != nil {
			return err
		}
	}
	if r.HasVolume {
		if err := validateCanonicalDecimal(r.Volume, true); err != nil {
			return err
		}
	}
	if compareDecimal(r.Low, r.High) > 0 || compareDecimal(r.Open, r.Low) < 0 || compareDecimal(r.Open, r.High) > 0 || compareDecimal(r.Close, r.Low) < 0 || compareDecimal(r.Close, r.High) > 0 {
		return errors.New("invalid OHLC invariant")
	}
	return nil
}

func validateStoredFundamental(r FundamentalRow) error {
	if err := validateStoredCommon(r.SchemaVersion, r.Source, r.RawPayloadHash, r.DataSourceID, r.IngestionRunID, r.NormalizerVersion); err != nil {
		return err
	}
	if _, err := uuid.Parse(r.IssuerID); err != nil {
		return errors.New("issuer_id must be UUID")
	}
	if r.HasSecurityID != (r.SecurityID != "") {
		return errors.New("security_id presence mismatch")
	}
	if r.HasSecurityID {
		if _, err := uuid.Parse(r.SecurityID); err != nil {
			return errors.New("security_id must be UUID")
		}
	}
	if r.Taxonomy == "" || r.Concept == "" || r.Unit == "" || !validFiscalPeriod(r.FiscalPeriod) || r.Revision < 0 {
		return errors.New("invalid fundamental domain fields")
	}
	if r.HasCurrency != (r.Currency != "") || r.HasCurrency && !isCurrency(r.Currency) {
		return errors.New("invalid fundamental currency presence")
	}
	if r.HasValue != (r.Value != "") {
		return errors.New("fundamental value presence mismatch")
	}
	if r.HasValue {
		if err := validateCanonicalDecimal(r.Value, false); err != nil {
			return err
		}
	}
	if !r.HasPeriodStart && r.PeriodStart != 0 {
		return errors.New("period_start presence mismatch")
	}
	if r.HasPeriodStart && r.PeriodStart > r.PeriodEnd {
		return errors.New("period_start after period_end")
	}
	temporal := storedTemporal(r.ObservedAt, r.PublishedAt, r.ObservedPrecision, r.PublishedPrecision, r.AvailableAt, r.IngestedAt, true)
	if err := validateTemporal(temporal, true); err != nil {
		return err
	}
	periodEnd := time.Unix(int64(r.PeriodEnd)*86400, 0).UTC()
	if !sameUTCDay(periodEnd, temporal.ObservedAt) || periodEnd.After(temporal.PublishedAt) {
		return errors.New("invalid fundamental period temporal semantics")
	}
	return nil
}

func validateStoredEconomic(r EconomicRow) error {
	if err := validateStoredCommon(r.SchemaVersion, r.Source, r.RawPayloadHash, r.DataSourceID, r.IngestionRunID, r.NormalizerVersion); err != nil {
		return err
	}
	if r.SeriesID == "" || r.Geography == "" || r.Unit == "" || !validFrequency(r.Frequency) || r.Revision < 0 {
		return errors.New("invalid economic domain fields")
	}
	if r.HasSeasonalAdjustment != (r.SeasonalAdjustment != "") {
		return errors.New("seasonal_adjustment presence mismatch")
	}
	if r.HasValue != (r.Value != "") {
		return errors.New("economic value presence mismatch")
	}
	if r.HasValue {
		if err := validateCanonicalDecimal(r.Value, false); err != nil {
			return err
		}
	}
	if r.HasVintageAt != (r.VintageAt != 0) {
		return errors.New("vintage_at presence mismatch")
	}
	return validateTemporal(storedTemporal(r.ObservedAt, r.PublishedAt, r.ObservedPrecision, r.PublishedPrecision, r.AvailableAt, r.IngestedAt, true), true)
}

func validateStoredCommon(schemaVersion, source, hash, dataSourceID, runID, normalizerVersion string) error {
	if schemaVersion != model.SchemaVersion {
		return fmt.Errorf("unsupported schema_version %q", schemaVersion)
	}
	if source == "" {
		return errors.New("source required")
	}
	if len(hash) != 64 {
		return errors.New("raw_payload_hash must be SHA-256")
	}
	for _, c := range hash {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return errors.New("raw_payload_hash must be lowercase hex")
		}
	}
	if _, err := uuid.Parse(dataSourceID); err != nil {
		return errors.New("data_source_id must be UUID")
	}
	if _, err := uuid.Parse(runID); err != nil {
		return errors.New("ingestion_run_id must be UUID")
	}
	if normalizerVersion == "" {
		return errors.New("normalizer_version required")
	}
	return nil
}

func validateCanonicalDecimal(v string, nonNegative bool) error {
	canonical, err := model.CanonicalDecimal(v, nonNegative)
	if err != nil {
		return err
	}
	if canonical != v {
		return fmt.Errorf("non-canonical decimal %q", v)
	}
	return nil
}

func storedTemporal(observed, published int64, observedPrecision, publishedPrecision string, available, ingested int64, hasPublished bool) model.Temporal {
	t := model.Temporal{
		ObservedAt:         time.UnixMicro(observed).UTC(),
		ObservedPrecision:  normalizeObservedPrecision(model.TimePrecision(observedPrecision)),
		PublishedPrecision: model.TimePrecision(publishedPrecision),
		AvailableAt:        time.UnixMicro(available).UTC(),
		IngestedAt:         time.UnixMicro(ingested).UTC(),
	}
	if hasPublished {
		t.PublishedAt = time.UnixMicro(published).UTC()
	}
	return t
}
func publish[T comparable](w *Writer, dir string, partition map[string]string, existing, rows []T, metadata publicationMetadata) (string, int, error) {
	manifestPath := filepath.Join(dir, ManifestFilename)
	if slices.Equal(existing, rows) {
		return manifestPath, 0, nil
	}
	part, err := writePart(dir, rows, w.publicationOps())
	if err != nil {
		return "", 0, err
	}
	manifest := Manifest{
		ManifestVersion:   ManifestVersion,
		SchemaVersion:     model.SchemaVersion,
		NormalizerVersion: metadata.NormalizerVersion,
		GitCommit:         w.gitCommitValue(),
		Source:            metadata.Source,
		DataSourceID:      metadata.DataSourceID,
		IngestionRunID:    metadata.IngestionRunID,
		Partition:         clonePartition(partition),
		RowCount:          len(rows),
		Parts:             []ManifestPart{part},
	}
	if err := validateManifest(manifest); err != nil {
		return "", 0, err
	}
	if err := writeManifest(manifestPath, manifest, w.publicationOps()); err != nil {
		return "", 0, err
	}
	return manifestPath, len(rows), nil
}

func (w *Writer) gitCommitValue() string {
	if validGitCommit(w.gitCommit) {
		return w.gitCommit
	}
	return currentGitCommit()
}

func clonePartition(partition map[string]string) map[string]string {
	clone := make(map[string]string, len(partition))
	for key, value := range partition {
		clone[key] = value
	}
	return clone
}

func writePart[T any](dir string, rows []T, ops publicationOps) (ManifestPart, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return ManifestPart{}, err
	}
	tmp, err := os.CreateTemp(dir, ".part-*")
	if err != nil {
		return ManifestPart{}, err
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Close(); err != nil {
		return ManifestPart{}, err
	}
	if err := parquet.WriteFile(tmpName, rows, parquet.Compression(&parquet.Snappy)); err != nil {
		return ManifestPart{}, err
	}
	if err := os.Chmod(tmpName, 0o640); err != nil {
		return ManifestPart{}, err
	}
	if err := ops.syncFile(tmpName); err != nil {
		return ManifestPart{}, err
	}
	hash, err := sha256File(tmpName)
	if err != nil {
		return ManifestPart{}, err
	}
	part := ManifestPart{Path: contentPartFilename(hash), SHA256: hash, RowCount: len(rows)}
	partPath := filepath.Join(dir, part.Path)
	if existingHash, statErr := sha256File(partPath); statErr == nil {
		if existingHash != hash {
			return ManifestPart{}, fmt.Errorf("immutable part path %s contains different content", partPath)
		}
		removeTemp = true
		if err := ops.syncFile(partPath); err != nil {
			return ManifestPart{}, err
		}
		return part, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return ManifestPart{}, statErr
	}
	if err := ops.rename(tmpName, partPath); err != nil {
		return ManifestPart{}, err
	}
	removeTemp = false
	fileErr := ops.syncFile(partPath)
	dirErr := ops.syncDir(dir)
	if fileErr != nil {
		return ManifestPart{}, fileErr
	}
	if dirErr != nil {
		return ManifestPart{}, dirErr
	}
	return part, nil
}
