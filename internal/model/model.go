// Package model defines vendor-neutral canonical domain records.
package model

import (
	"context"
	"errors"
	"math/big"
	"net/url"
	"strings"
	"time"
)

type TimePrecision string

const (
	PrecisionDate    TimePrecision = "date"
	PrecisionSecond  TimePrecision = "second"
	PrecisionUnknown TimePrecision = "unknown"
)

// Temporal records the distinct event, publication, availability and ingestion times.
// AvailableAt is the earliest safe point for point-in-time research. For SEC facts where
// only a filed date is supplied, adapters must choose a conservative fallback later than
// any plausible acceptance time on that date (SEC currently uses filed date plus 48 hours).
type Temporal struct {
	ObservedAt         time.Time
	ObservedPrecision  TimePrecision
	PublishedAt        time.Time
	PublishedPrecision TimePrecision
	AvailableAt        time.Time
	IngestedAt         time.Time
}

const SchemaVersion = "1.0.0"
const NormalizerVersion = "go-v1"

// CanonicalDecimal converts a base-10 provider lexeme to the lossless canonical
// JSON-schema form. Scientific notation is intentionally rejected.
func CanonicalDecimal(v string, nonNegative bool) (string, error) {
	if strings.ContainsAny(v, "eE+/ ") || v == "" {
		return "", errors.New("invalid decimal")
	}
	r, ok := new(big.Rat).SetString(v)
	if !ok || (nonNegative && r.Sign() < 0) {
		return "", errors.New("invalid decimal")
	}
	if strings.Contains(v, ".") {
		v = strings.TrimRight(strings.TrimRight(v, "0"), ".")
	}
	negative := strings.HasPrefix(v, "-")
	if negative {
		v = v[1:]
	}
	parts := strings.SplitN(v, ".", 2)
	integer := strings.TrimLeft(parts[0], "0")
	if integer == "" {
		integer = "0"
	}
	result := integer
	if len(parts) == 2 && parts[1] != "" {
		result += "." + parts[1]
	}
	if negative && result != "0" {
		result = "-" + result
	}
	return result, nil
}

// Provenance is source evidence carried with every canonical observation.
// DataSourceID and IngestionRunID are stamped by collector orchestration once
// PostgreSQL identities are known. RawRecordLocator is adapter-owned.
type Provenance struct {
	DataSourceID, IngestionRunID     string
	RawPayloadHash, RawRecordLocator string
	IngestedAt                       time.Time
	NormalizerVersion                string
}

type Issuer struct {
	ID, LegalName, Country, Sector, Industry string
	CIK                                      int64
}

type Security struct {
	ID, IssuerID, Type, Exchange, MIC, Currency string
	PrimaryListing                              bool
	Identifiers                                 []SecurityIdentifier
}

type SecurityIdentifier struct {
	Type, Value string
	ValidFrom   time.Time
	ValidUntil  *time.Time
}

type PriceBar struct {
	Source, SecurityID, Currency string
	Interval, PriceBasis         string
	Temporal                     Temporal
	Open, High, Low, Close       string
	Volume                       string
	RawPayloadHash               string
	Provenance                   Provenance
}

type FundamentalObservation struct {
	Source, IssuerID, SecurityID, Taxonomy, Concept, Unit string
	Temporal                                              Temporal
	PeriodStart                                           *time.Time
	PeriodEnd                                             time.Time
	Value                                                 string
	Currency                                              string
	Revision                                              int
	AccessionNumber, Form, FiscalPeriod, Frame            string
	FiscalYear                                            int
	RawPayloadHash                                        string
	Provenance                                            Provenance
}

type EconomicObservation struct {
	Source, SeriesID, Unit                   string
	Geography, Frequency, SeasonalAdjustment string
	Temporal                                 Temporal
	Value                                    string
	Revision                                 int
	VintageAt                                *time.Time
	RawPayloadHash                           string
	Provenance                               Provenance
}

// Filing is canonical metadata for one source document. SourceDocumentID is
// the source-owned identity used for idempotent publication; it must include
// any source version component that distinguishes a resubmission. The source
// strings are intentionally not normalized beyond the contract's required
// presence checks. Complete source fidelity remains in the raw payload.
//
// The fields below the canonical contract are retained for the SEC adapter's
// existing result API. They are compatibility fields only; canonical writers
// use the explicit v1 fields above them and never infer AvailableAt from
// FiledDate, PeriodEnd, or any other observation date.
type Filing struct {
	ID                     string
	Source                 string
	IssuerID               string
	SourceDocumentID       string
	DocumentURL            string
	AccessionNumber        string
	FormType               string
	Category               string
	DocumentType           string
	Species                string
	Subject                string
	PresentationType       string
	PrimaryDocument        string
	AmendsSourceDocumentID string
	FilingDate             time.Time
	PeriodEnd              *time.Time
	Temporal               Temporal
	EffectiveAt            *time.Time
	Provenance             Provenance

	// Deprecated adapter compatibility fields. New providers should populate
	// SourceDocumentID, FormType, FilingDate, Temporal, and Provenance.
	Form           string
	FiledDate      time.Time
	AcceptedAt     *time.Time
	IngestedAt     time.Time
	RawPayloadHash string
}

// Validate checks the provider-neutral filing contract before physical
// normalization. UUID and SHA-256 checks belong to the storage boundary where
// those identifiers are serialized; this method deliberately does not invent
// values for missing temporal fields.
func (f Filing) Validate() error {
	if f.Source == "" {
		return errors.New("filing source required")
	}
	if f.IssuerID == "" {
		return errors.New("filing issuer_id required")
	}
	if f.SourceDocumentID == "" {
		return errors.New("filing source_document_id required")
	}
	if f.FormType == "" {
		return errors.New("filing form_type required")
	}
	if f.FilingDate.IsZero() {
		return errors.New("filing_date required")
	}
	if f.DocumentURL == "" {
		return errors.New("filing document_url required")
	}
	u, err := url.Parse(f.DocumentURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return errors.New("filing document_url must be an absolute URL")
	}
	if f.Temporal.AvailableAt.IsZero() || f.Temporal.IngestedAt.IsZero() {
		return errors.New("filing available_at and ingested_at required")
	}
	if f.Temporal.AvailableAt.After(f.Temporal.IngestedAt) {
		return errors.New("filing available_at after ingested_at")
	}
	if err := validateFilingPrecision(f.Temporal.ObservedAt, f.Temporal.ObservedPrecision, "observed"); err != nil {
		return err
	}
	if err := validateFilingPrecision(f.Temporal.PublishedAt, f.Temporal.PublishedPrecision, "published"); err != nil {
		return err
	}
	if !f.Temporal.PublishedAt.IsZero() && f.Temporal.PublishedAt.After(f.Temporal.AvailableAt) {
		return errors.New("filing published_at after available_at")
	}
	if !f.Temporal.ObservedAt.IsZero() && !f.Temporal.PublishedAt.IsZero() && f.Temporal.ObservedAt.After(f.Temporal.PublishedAt) {
		return errors.New("filing observed_at after published_at")
	}
	if f.EffectiveAt != nil && f.EffectiveAt.IsZero() {
		return errors.New("filing effective_at must be non-zero when present")
	}
	return nil
}

func validateFilingPrecision(at time.Time, precision TimePrecision, field string) error {
	if precision == "" {
		precision = PrecisionUnknown
	}
	if precision != PrecisionDate && precision != PrecisionSecond && precision != PrecisionUnknown {
		return errors.New("filing " + field + "_precision is invalid")
	}
	if at.IsZero() {
		if precision != PrecisionUnknown {
			return errors.New("filing " + field + "_precision must be unknown when " + field + "_at is absent")
		}
		return nil
	}
	utc := at.UTC()
	switch precision {
	case PrecisionDate:
		if utc.Hour() != 0 || utc.Minute() != 0 || utc.Second() != 0 || utc.Nanosecond() != 0 {
			return errors.New("filing " + field + "_precision=date requires UTC midnight")
		}
	case PrecisionSecond:
		if utc.Nanosecond() != 0 {
			return errors.New("filing " + field + "_precision=second requires whole-second time")
		}
	}
	return nil
}

type CorporateAction struct {
	Source, SecurityID, Type, Currency string
	Temporal                           Temporal
	EffectiveAt                        time.Time
	Value                              float64
	RawPayloadHash                     string
}

type DataSource struct{ ID, Name, BaseURL string }

type IngestionRun struct {
	ID, Source, Status                               string
	StartedAt, FinishedAt                            time.Time
	RecordsReceived, RecordsWritten, RecordsRejected int64
	Error                                            string
}

type HistoricalPriceRequest struct {
	SecurityID, VendorSymbol, Currency string
	Start, End                         time.Time
}

type PriceProvider interface {
	HistoricalPrices(ctx context.Context, req HistoricalPriceRequest) ([]PriceBar, []byte, error)
}
