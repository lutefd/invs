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

func TestCanonicalDecimalIsLosslessAndSchemaCompatible(t *testing.T) {
	cases := map[string]string{"001.2300": "1.23", ".5": "0.5", "1.": "1", "-0.00": "0", "12345678901234567890.12345678901234567890": "12345678901234567890.1234567890123456789"}
	for in, want := range cases {
		got, err := CanonicalDecimal(in, false)
		if err != nil || got != want {
			t.Fatalf("%q => %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "1e3", "1/2", "+1", "NaN"} {
		if _, err := CanonicalDecimal(bad, false); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if _, err := CanonicalDecimal("-1", true); err == nil {
		t.Fatal("accepted negative nonnegative decimal")
	}
}

func TestFilingAllowsUnknownCVMPublicationWithoutInferringAvailability(t *testing.T) {
	ingested := time.Date(2026, 8, 12, 15, 4, 5, 123456000, time.UTC)
	periodEnd := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
	filing := Filing{
		ID:               "1b3d88f5-55b8-4dc5-a6be-2f77e9e99201",
		Source:           "cvm",
		IssuerID:         "469fc20f-7d4b-45bb-b827-05f8410e71aa",
		SourceDocumentID: "cvm-ipe:1023:12345:v2",
		DocumentURL:      "https://dados.cvm.gov.br/dados/CIA_ABERTA/DOC/IPE/DADOS/ipe.zip",
		AccessionNumber:  "12345",
		FormType:         "cvm_ipe",
		FilingDate:       time.Date(2026, 1, 7, 0, 0, 0, 0, time.UTC),
		PeriodEnd:        &periodEnd,
		Temporal: Temporal{
			ObservedAt:         periodEnd,
			ObservedPrecision:  PrecisionDate,
			PublishedPrecision: PrecisionUnknown,
			AvailableAt:        ingested,
			IngestedAt:         ingested,
		},
	}
	if err := filing.Validate(); err != nil {
		t.Fatal(err)
	}
	filing.Temporal.PublishedPrecision = PrecisionDate
	if err := filing.Validate(); err == nil {
		t.Fatal("accepted a date precision marker without published_at")
	}
}
