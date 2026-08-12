// Package model defines vendor-neutral canonical domain records.
package model

import (
	"context"
	"errors"
	"math/big"
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

type Filing struct {
	Source, IssuerID, AccessionNumber, Form, PrimaryDocument string
	FiledDate                                                time.Time
	AcceptedAt                                               *time.Time
	IngestedAt                                               time.Time
	RawPayloadHash                                           string
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
