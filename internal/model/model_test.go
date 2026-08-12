package model

import (
	"testing"
	"time"
)

func TestSecurityMappingDoesNotUseTickerAsIdentity(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	s := Security{ID: "security-1", IssuerID: "issuer-1", Identifiers: []SecurityIdentifier{
		{Type: "ticker", Value: "META", ValidFrom: start},
	}}
	if s.ID == s.Identifiers[0].Value {
		t.Fatal("internal ID must be independent from ticker")
	}
	if s.IssuerID == "" || s.ID == "" {
		t.Fatal("stable internal mapping required")
	}
}

func TestTemporalSemanticsAreDistinct(t *testing.T) {
	observed := time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC)
	published := time.Date(2024, 5, 2, 0, 0, 0, 0, time.UTC)
	available := published.AddDate(0, 0, 1)
	ingested := time.Date(2024, 5, 5, 12, 0, 0, 0, time.UTC)
	tm := Temporal{ObservedAt: observed, PublishedAt: published, PublishedPrecision: PrecisionDate, AvailableAt: available, IngestedAt: ingested}
	if !tm.ObservedAt.Before(tm.PublishedAt) || !tm.PublishedAt.Before(tm.AvailableAt) || !tm.AvailableAt.Before(tm.IngestedAt) {
		t.Fatalf("unexpected temporal ordering: %+v", tm)
	}
}
