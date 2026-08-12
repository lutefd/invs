package normalize

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/luisdourado/invs/internal/model"
	"github.com/parquet-go/parquet-go"
)

type PriceRow struct {
	Source             string  `parquet:"source"`
	SecurityID         string  `parquet:"security_id"`
	Currency           string  `parquet:"currency"`
	ObservedAt         int64   `parquet:"observed_at,timestamp(microsecond:utc)"`
	PublishedAt        int64   `parquet:"published_at,timestamp(microsecond:utc)"`
	PublishedPrecision string  `parquet:"published_precision"`
	AvailableAt        int64   `parquet:"available_at,timestamp(microsecond:utc)"`
	IngestedAt         int64   `parquet:"ingested_at,timestamp(microsecond:utc)"`
	Open               float64 `parquet:"open"`
	High               float64 `parquet:"high"`
	Low                float64 `parquet:"low"`
	Close              float64 `parquet:"close"`
	Volume             int64   `parquet:"volume"`
	RawPayloadHash     string  `parquet:"raw_payload_hash"`
}

type FundamentalRow struct {
	Source             string  `parquet:"source"`
	IssuerID           string  `parquet:"issuer_id"`
	Taxonomy           string  `parquet:"taxonomy"`
	Concept            string  `parquet:"concept"`
	Unit               string  `parquet:"unit"`
	ObservedAt         int64   `parquet:"observed_at,timestamp(microsecond:utc)"`
	PublishedAt        int64   `parquet:"published_at,timestamp(microsecond:utc)"`
	PublishedPrecision string  `parquet:"published_precision"`
	AvailableAt        int64   `parquet:"available_at,timestamp(microsecond:utc)"`
	IngestedAt         int64   `parquet:"ingested_at,timestamp(microsecond:utc)"`
	PeriodStart        int32   `parquet:"period_start,date"`
	HasPeriodStart     bool    `parquet:"has_period_start"`
	PeriodEnd          int32   `parquet:"period_end,date"`
	Value              float64 `parquet:"value"`
	AccessionNumber    string  `parquet:"accession_number"`
	Form               string  `parquet:"form"`
	FiscalYear         int32   `parquet:"fiscal_year"`
	FiscalPeriod       string  `parquet:"fiscal_period"`
	Frame              string  `parquet:"frame"`
	RawPayloadHash     string  `parquet:"raw_payload_hash"`
}

type EconomicRow struct {
	Source             string  `parquet:"source"`
	SeriesID           string  `parquet:"series_id"`
	Unit               string  `parquet:"unit"`
	ObservedAt         int64   `parquet:"observed_at,timestamp(microsecond:utc)"`
	PublishedAt        int64   `parquet:"published_at,timestamp(microsecond:utc)"`
	PublishedPrecision string  `parquet:"published_precision"`
	AvailableAt        int64   `parquet:"available_at,timestamp(microsecond:utc)"`
	IngestedAt         int64   `parquet:"ingested_at,timestamp(microsecond:utc)"`
	Value              float64 `parquet:"value"`
	VintageAt          int64   `parquet:"vintage_at,timestamp(microsecond:utc)"`
	HasVintageAt       bool    `parquet:"has_vintage_at"`
	RawPayloadHash     string  `parquet:"raw_payload_hash"`
}

// PeriodStartTime and VintageTime expose the physical sentinel+flag encoding
// as nullable logical values to Go consumers. DuckDB views should use
// CASE WHEN has_period_start THEN period_start END and the equivalent vintage expression.
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

type Writer struct{ root string }

func NewWriter(root string) (*Writer, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0o750); err != nil {
		return nil, err
	}
	return &Writer{root: abs}, nil
}

func (w *Writer) WritePrices(securityID string, observations []model.PriceBar) (string, int, error) {
	if !safeSegment(securityID) {
		return "", 0, fmt.Errorf("unsafe security ID %q", securityID)
	}
	source := "unknown"
	if len(observations) > 0 {
		source = observations[0].Source
	}
	if !safeSegment(source) {
		return "", 0, fmt.Errorf("unsafe price source %q", source)
	}
	path, err := w.path("prices", "source="+source, "security_id="+securityID)
	if err != nil {
		return "", 0, err
	}
	incoming := make([]PriceRow, 0, len(observations))
	for _, o := range observations {
		if o.Volume < 0 || o.Open < 0 || o.High < 0 || o.Low < 0 || o.Close < 0 || math.IsNaN(o.Open) || math.IsNaN(o.High) || math.IsNaN(o.Low) || math.IsNaN(o.Close) || math.IsInf(o.Open, 0) || math.IsInf(o.High, 0) || math.IsInf(o.Low, 0) || math.IsInf(o.Close, 0) || o.Low > o.High || o.Open < o.Low || o.Open > o.High || o.Close < o.Low || o.Close > o.High {
			return "", 0, fmt.Errorf("invalid price bar for %s at %s", o.SecurityID, o.Temporal.ObservedAt)
		}
		incoming = append(incoming, priceRow(o))
	}
	existing, err := readIfExists[PriceRow](path)
	if err != nil {
		return "", 0, fmt.Errorf("read existing prices: %w", err)
	}
	byKey := map[string]PriceRow{}
	for _, r := range existing {
		byKey[priceKey(r)] = r
	}
	for _, r := range incoming {
		k := priceKey(r)
		if old, ok := byKey[k]; ok && samePrice(old, r) {
			continue
		}
		byKey[k] = r
	}
	rows := values(byKey)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].SecurityID != rows[j].SecurityID {
			return rows[i].SecurityID < rows[j].SecurityID
		}
		return rows[i].ObservedAt < rows[j].ObservedAt
	})
	if slices.Equal(existing, rows) {
		return path, 0, nil
	}
	changed, err := writeAtomic(path, rows)
	if err != nil {
		return "", 0, err
	}
	if !changed {
		return path, 0, nil
	}
	return path, len(rows), nil
}

func (w *Writer) WriteFundamentals(issuerID string, observations []model.FundamentalObservation) (string, int, error) {
	if !safeSegment(issuerID) {
		return "", 0, fmt.Errorf("unsafe issuer ID %q", issuerID)
	}
	path, err := w.path("fundamentals", "source=sec", "issuer_id="+issuerID)
	if err != nil {
		return "", 0, err
	}
	incoming := make([]FundamentalRow, 0, len(observations))
	for _, o := range observations {
		if math.IsNaN(o.Value) || math.IsInf(o.Value, 0) {
			return "", 0, fmt.Errorf("non-finite fundamental value")
		}
		incoming = append(incoming, fundamentalRow(o))
	}
	existing, err := readIfExists[FundamentalRow](path)
	if err != nil {
		return "", 0, fmt.Errorf("read existing fundamentals: %w", err)
	}
	byKey := map[string]FundamentalRow{}
	for _, r := range existing {
		byKey[fundamentalKey(r)] = r
	}
	for _, r := range incoming {
		k := fundamentalKey(r)
		if old, ok := byKey[k]; ok && sameFundamental(old, r) {
			continue
		}
		byKey[k] = r
	}
	rows := values(byKey)
	sort.Slice(rows, func(i, j int) bool {
		return fundamentalKey(rows[i]) < fundamentalKey(rows[j])
	})
	if slices.Equal(existing, rows) {
		return path, 0, nil
	}
	changed, err := writeAtomic(path, rows)
	if err != nil {
		return "", 0, err
	}
	if !changed {
		return path, 0, nil
	}
	return path, len(rows), nil
}

func (w *Writer) WriteEconomics(seriesID string, observations []model.EconomicObservation) (string, int, error) {
	if !safeSegment(seriesID) {
		return "", 0, fmt.Errorf("unsafe series ID %q", seriesID)
	}
	path, err := w.path("macroeconomics", "source=fred", "series_id="+seriesID)
	if err != nil {
		return "", 0, err
	}
	incoming := make([]EconomicRow, 0, len(observations))
	for _, o := range observations {
		if math.IsNaN(o.Value) || math.IsInf(o.Value, 0) {
			return "", 0, fmt.Errorf("non-finite economic value")
		}
		incoming = append(incoming, economicRow(o))
	}
	existing, err := readIfExists[EconomicRow](path)
	if err != nil {
		return "", 0, fmt.Errorf("read existing macroeconomics: %w", err)
	}
	byKey := map[string]EconomicRow{}
	for _, r := range existing {
		byKey[economicKey(r)] = r
	}
	for _, r := range incoming {
		k := economicKey(r)
		if old, ok := byKey[k]; ok && sameEconomic(old, r) {
			continue
		}
		byKey[k] = r
	}
	rows := values(byKey)
	sort.Slice(rows, func(i, j int) bool { return rows[i].ObservedAt < rows[j].ObservedAt })
	if slices.Equal(existing, rows) {
		return path, 0, nil
	}
	changed, err := writeAtomic(path, rows)
	if err != nil {
		return "", 0, err
	}
	if !changed {
		return path, 0, nil
	}
	return path, len(rows), nil
}

func (w *Writer) path(parts ...string) (string, error) {
	for _, p := range parts {
		if p == "" || p == "." || p == ".." || strings.ContainsAny(p, "/\\\x00") {
			return "", fmt.Errorf("unsafe normalized path segment %q", p)
		}
	}
	return filepath.Join(append([]string{w.root}, append(parts, "data.parquet")...)...), nil
}
func safeSegment(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

func micros(t time.Time) int64 { return t.UTC().UnixMicro() }
func days(t time.Time) int32   { return int32(t.UTC().Unix() / 86400) }
func priceRow(o model.PriceBar) PriceRow {
	return PriceRow{Source: o.Source, SecurityID: o.SecurityID, Currency: o.Currency, ObservedAt: micros(o.Temporal.ObservedAt), PublishedAt: micros(o.Temporal.PublishedAt), PublishedPrecision: string(o.Temporal.PublishedPrecision), AvailableAt: micros(o.Temporal.AvailableAt), IngestedAt: micros(o.Temporal.IngestedAt), Open: o.Open, High: o.High, Low: o.Low, Close: o.Close, Volume: o.Volume, RawPayloadHash: o.RawPayloadHash}
}
func fundamentalRow(o model.FundamentalObservation) FundamentalRow {
	var start int32
	hasStart := false
	if o.PeriodStart != nil {
		start = days(*o.PeriodStart)
		hasStart = true
	}
	return FundamentalRow{Source: o.Source, IssuerID: o.IssuerID, Taxonomy: o.Taxonomy, Concept: o.Concept, Unit: o.Unit, ObservedAt: micros(o.Temporal.ObservedAt), PublishedAt: micros(o.Temporal.PublishedAt), PublishedPrecision: string(o.Temporal.PublishedPrecision), AvailableAt: micros(o.Temporal.AvailableAt), IngestedAt: micros(o.Temporal.IngestedAt), PeriodStart: start, HasPeriodStart: hasStart, PeriodEnd: days(o.PeriodEnd), Value: o.Value, AccessionNumber: o.AccessionNumber, Form: o.Form, FiscalYear: int32(o.FiscalYear), FiscalPeriod: o.FiscalPeriod, Frame: o.Frame, RawPayloadHash: o.RawPayloadHash}
}
func economicRow(o model.EconomicObservation) EconomicRow {
	var vintage int64
	hasVintage := false
	if o.VintageAt != nil {
		vintage = micros(*o.VintageAt)
		hasVintage = true
	}
	return EconomicRow{Source: o.Source, SeriesID: o.SeriesID, Unit: o.Unit, ObservedAt: micros(o.Temporal.ObservedAt), PublishedAt: micros(o.Temporal.PublishedAt), PublishedPrecision: string(o.Temporal.PublishedPrecision), AvailableAt: micros(o.Temporal.AvailableAt), IngestedAt: micros(o.Temporal.IngestedAt), Value: o.Value, VintageAt: vintage, HasVintageAt: hasVintage, RawPayloadHash: o.RawPayloadHash}
}

func priceKey(r PriceRow) string {
	return fmt.Sprintf("%s\x1f%s\x1f%d\x1f%d", r.Source, r.SecurityID, r.ObservedAt, r.PublishedAt)
}
func fundamentalKey(r FundamentalRow) string {
	return strings.Join([]string{r.Source, r.IssuerID, r.Taxonomy, r.Concept, r.Unit, r.AccessionNumber, fmt.Sprint(r.HasPeriodStart), fmt.Sprint(r.PeriodStart), fmt.Sprint(r.PeriodEnd), r.Frame, fmt.Sprint(r.PublishedAt)}, "\x1f")
}
func economicKey(r EconomicRow) string {
	return fmt.Sprintf("%s\x1f%s\x1f%d\x1f%d\x1f%d\x1f%.17g", r.Source, r.SeriesID, r.ObservedAt, r.PublishedAt, r.VintageAt, r.Value)
}
func samePrice(a, b PriceRow) bool {
	return a.Source == b.Source && a.SecurityID == b.SecurityID && a.Currency == b.Currency && a.ObservedAt == b.ObservedAt && a.PublishedAt == b.PublishedAt && a.Open == b.Open && a.High == b.High && a.Low == b.Low && a.Close == b.Close && a.Volume == b.Volume
}
func sameFundamental(a, b FundamentalRow) bool {
	return a.Source == b.Source && a.IssuerID == b.IssuerID && a.Taxonomy == b.Taxonomy && a.Concept == b.Concept && a.Unit == b.Unit && a.ObservedAt == b.ObservedAt && a.PublishedAt == b.PublishedAt && a.AvailableAt == b.AvailableAt && a.PeriodStart == b.PeriodStart && a.HasPeriodStart == b.HasPeriodStart && a.PeriodEnd == b.PeriodEnd && a.Value == b.Value && a.AccessionNumber == b.AccessionNumber && a.Form == b.Form && a.FiscalYear == b.FiscalYear && a.FiscalPeriod == b.FiscalPeriod && a.Frame == b.Frame
}
func sameEconomic(a, b EconomicRow) bool {
	return a.Source == b.Source && a.SeriesID == b.SeriesID && a.Unit == b.Unit && a.ObservedAt == b.ObservedAt && a.Value == b.Value
}
func values[K comparable, V any](m map[K]V) []V {
	r := make([]V, 0, len(m))
	for _, v := range m {
		r = append(r, v)
	}
	return r
}
func readIfExists[T any](path string) ([]T, error) {
	rows, err := parquet.ReadFile[T](path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return rows, err
}
func writeAtomic[T any](path string, rows []T) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".parquet-*")
	if err != nil {
		return false, err
	}
	name := tmp.Name()
	tmp.Close()
	defer os.Remove(name)
	if err := parquet.WriteFile(name, rows, parquet.Compression(&parquet.Snappy)); err != nil {
		return false, fmt.Errorf("write parquet: %w", err)
	}
	f, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	err = f.Sync()
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return false, err
	}
	newBytes, err := os.ReadFile(name)
	if err != nil {
		return false, err
	}
	if oldBytes, readErr := os.ReadFile(path); readErr == nil && sha256.Sum256(oldBytes) == sha256.Sum256(newBytes) && bytes.Equal(oldBytes, newBytes) {
		return false, nil
	}
	if err := os.Chmod(name, 0o640); err != nil {
		return false, err
	}
	if err := os.Rename(name, path); err != nil {
		return false, err
	}
	return true, nil
}
