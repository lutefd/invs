package metadata

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luisdourado/invs/internal/model"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		m    Metrics
		want string
	}{{Metrics{}, "succeeded"}, {Metrics{Rejected: 1}, "partial"}, {Metrics{Err: errors.New("x")}, "failed"}, {Metrics{Err: errors.New("x"), RawPayloads: 1}, "partial"}, {Metrics{Err: context.Canceled}, "cancelled"}}
	for _, c := range cases {
		if got := classify(c.m); got != c.want {
			t.Fatalf("got %s want %s", got, c.want)
		}
	}
}

func TestPriceSnapshotOrdering(t *testing.T) {
	baseAt := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	base := model.PriceBar{
		RawPayloadHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Temporal: model.Temporal{
			ObservedAt:  baseAt,
			AvailableAt: baseAt.Add(2 * time.Hour),
			IngestedAt:  baseAt.Add(3 * time.Hour),
		},
	}
	cases := []struct {
		name      string
		candidate model.PriceBar
		want      bool
	}{
		{
			name: "newer observed timestamp",
			candidate: func() model.PriceBar {
				v := base
				v.Temporal.ObservedAt = baseAt.Add(24 * time.Hour)
				return v
			}(),
			want: true,
		},
		{
			name: "newer availability when observation ties",
			candidate: func() model.PriceBar {
				v := base
				v.Temporal.AvailableAt = baseAt.Add(4 * time.Hour)
				return v
			}(),
			want: true,
		},
		{
			name: "newer ingestion when earlier fields tie",
			candidate: func() model.PriceBar {
				v := base
				v.Temporal.IngestedAt = baseAt.Add(4 * time.Hour)
				return v
			}(),
			want: true,
		},
		{
			name: "hash breaks exact timestamp tie",
			candidate: func() model.PriceBar {
				v := base
				v.RawPayloadHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
				return v
			}(),
			want: true,
		},
		{
			name:      "older candidate loses",
			candidate: func() model.PriceBar { v := base; v.Temporal.ObservedAt = baseAt.Add(-time.Hour); return v }(),
			want:      false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := priceSnapshotIsNewer(tc.candidate, base); got != tc.want {
				t.Fatalf("priceSnapshotIsNewer() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMacroSnapshotOrdering(t *testing.T) {
	baseAt := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	base := model.EconomicObservation{
		RawPayloadHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Revision:       1,
		Temporal: model.Temporal{
			ObservedAt:  baseAt,
			AvailableAt: baseAt.Add(2 * time.Hour),
			IngestedAt:  baseAt.Add(3 * time.Hour),
		},
	}
	cases := []struct {
		name      string
		candidate model.EconomicObservation
		want      bool
	}{
		{
			name: "newer observation wins over revision",
			candidate: func() model.EconomicObservation {
				v := base
				v.Temporal.ObservedAt = baseAt.Add(24 * time.Hour)
				v.Revision = 0
				return v
			}(),
			want: true,
		},
		{
			name: "newer revision when observation ties",
			candidate: func() model.EconomicObservation {
				v := base
				v.Revision = 2
				return v
			}(),
			want: true,
		},
		{
			name: "newer availability when observation and revision tie",
			candidate: func() model.EconomicObservation {
				v := base
				v.Temporal.AvailableAt = baseAt.Add(4 * time.Hour)
				return v
			}(),
			want: true,
		},
		{
			name: "older observation loses",
			candidate: func() model.EconomicObservation {
				v := base
				v.Temporal.ObservedAt = baseAt.Add(-24 * time.Hour)
				v.Revision = 99
				return v
			}(),
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := macroSnapshotIsNewer(tc.candidate, base); got != tc.want {
				t.Fatalf("macroSnapshotIsNewer() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidateSnapshotLineage(t *testing.T) {
	run := Run{ID: "run-1", DataSourceID: "source-1"}
	price := model.PriceBar{Provenance: model.Provenance{DataSourceID: run.DataSourceID, IngestionRunID: run.ID}}
	macro := model.EconomicObservation{Provenance: model.Provenance{DataSourceID: run.DataSourceID, IngestionRunID: run.ID}}
	if err := validateSnapshotLineage(run, []model.PriceBar{price}, []model.EconomicObservation{macro}); err != nil {
		t.Fatalf("matching lineage returned error: %v", err)
	}

	price.Provenance.IngestionRunID = "other-run"
	if err := validateSnapshotLineage(run, []model.PriceBar{price}, nil); err == nil {
		t.Fatal("mismatched price lineage returned nil error")
	}
}
