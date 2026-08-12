// Package model defines vendor-neutral canonical domain records.
package model

import (
	"context"
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
// only a filed date is supplied, it is conservatively the start of the following UTC day.
type Temporal struct {
	ObservedAt         time.Time
	PublishedAt        time.Time
	PublishedPrecision TimePrecision
	AvailableAt        time.Time
	IngestedAt         time.Time
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
	Temporal                     Temporal
	Open, High, Low, Close       float64
	Volume                       int64
	RawPayloadHash               string
}

type FundamentalObservation struct {
	Source, IssuerID, Taxonomy, Concept, Unit  string
	Temporal                                   Temporal
	PeriodStart                                *time.Time
	PeriodEnd                                  time.Time
	Value                                      float64
	AccessionNumber, Form, FiscalPeriod, Frame string
	FiscalYear                                 int
	RawPayloadHash                             string
}

type EconomicObservation struct {
	Source, SeriesID, Unit string
	Temporal               Temporal
	Value                  float64
	VintageAt              *time.Time
	RawPayloadHash         string
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
