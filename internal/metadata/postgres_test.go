package metadata

import (
	"context"
	"errors"
	"strings"
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

func TestFinalizeRunSQLPersistsRawManifestHash(t *testing.T) {
	if !strings.Contains(finishRunSQL, "raw_payload_manifest_hash=$10") {
		t.Fatal("finishRunSQL does not persist raw_payload_manifest_hash")
	}
	if got := nullableManifestHash("abc"); got != "abc" {
		t.Fatalf("nullableManifestHash(non-empty) = %#v", got)
	}
	if got := nullableManifestHash(""); got != nil {
		t.Fatalf("nullableManifestHash(empty) = %#v, want nil", got)
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

func TestCollapseLatestMacrosLargeHistoricalBatch(t *testing.T) {
	const historicalRows = 17090
	run := Run{ID: "run-1", DataSourceID: "source-1"}
	baseAt := time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	macros := make([]model.EconomicObservation, 0, historicalRows+5)
	for i := 0; i < historicalRows; i++ {
		revision := 0
		if i == 0 {
			// A high revision on an old observation must not beat a newer
			// observation for the same series.
			revision = 999
		}
		macros = append(macros, testMacroObservation(run, "series-a", baseAt.AddDate(0, 0, i), revision))
	}

	latestAt := baseAt.AddDate(0, 0, historicalRows-1)
	macros = append(macros,
		// Same observation revisions are ordered after observation time.
		testMacroObservation(run, "series-a", latestAt, 2),
		testMacroObservation(run, "series-a", latestAt, 3),
		// Even a larger revision on an older observation must lose.
		testMacroObservation(run, "series-a", latestAt.AddDate(0, 0, -1), 1000),
		testMacroObservation(run, "series-b", baseAt, 1),
		testMacroObservation(run, "series-b", baseAt, 2),
	)

	reduced := collapseLatestMacros(macros)
	if len(reduced) != 2 {
		t.Fatalf("collapseLatestMacros returned %d candidates, want one per series (2)", len(reduced))
	}
	winners := make(map[string]model.EconomicObservation, len(reduced))
	for _, candidate := range reduced {
		winners[candidate.SeriesID] = candidate
	}
	if got := winners["series-a"]; got.Revision != 3 || !got.Temporal.ObservedAt.Equal(latestAt) {
		t.Fatalf("series-a winner = observation %s revision %d, want observation %s revision 3", got.Temporal.ObservedAt, got.Revision, latestAt)
	}
	if got := winners["series-b"]; got.Revision != 2 {
		t.Fatalf("series-b winner revision = %d, want 2", got.Revision)
	}
}

func TestPrepareFinalizationSnapshotsValidatesBeforeCollapse(t *testing.T) {
	run := Run{ID: "run-1", DataSourceID: "source-1"}
	valid := testMacroObservation(run, "series-a", time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC), 2)
	olderMismatched := testMacroObservation(run, "series-a", valid.Temporal.ObservedAt.Add(-24*time.Hour), 0)
	olderMismatched.Provenance.DataSourceID = "other-source"

	prices, macros, err := prepareFinalizationSnapshots(run, nil, []model.EconomicObservation{valid, olderMismatched})
	if err == nil {
		t.Fatal("prepareFinalizationSnapshots accepted a mismatched candidate that would be collapsed")
	}
	if prices != nil || macros != nil {
		t.Fatalf("failed preparation returned snapshots: prices=%v macros=%v", prices, macros)
	}

	prices, macros, err = prepareFinalizationSnapshots(run, nil, []model.EconomicObservation{valid, testMacroObservation(run, "series-a", valid.Temporal.ObservedAt, 3)})
	if err != nil {
		t.Fatalf("matching lineage returned error: %v", err)
	}
	if len(prices) != 0 || len(macros) != 1 || macros[0].Revision != 3 {
		t.Fatalf("prepared snapshots = %d prices, %d macros revision %d; want 0 prices, 1 macro revision 3", len(prices), len(macros), macros[0].Revision)
	}
}

func TestOperatorCancellationMessageRequiresIntent(t *testing.T) {
	if _, err := operatorCancellationMessage(" \t"); err == nil {
		t.Fatal("blank cancellation reason returned nil error")
	}
	message, err := operatorCancellationMessage("acceptance run was orphaned")
	if err != nil {
		t.Fatalf("operatorCancellationMessage returned error: %v", err)
	}
	if message == nil || *message != "operator cancellation: acceptance run was orphaned" {
		t.Fatalf("cancellation message = %v", message)
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

func testMacroObservation(run Run, seriesID string, observedAt time.Time, revision int) model.EconomicObservation {
	return model.EconomicObservation{
		SeriesID:       seriesID,
		Revision:       revision,
		RawPayloadHash: strings.Repeat("a", 64),
		Temporal: model.Temporal{
			ObservedAt:  observedAt,
			AvailableAt: observedAt.Add(time.Hour),
			IngestedAt:  observedAt.Add(2 * time.Hour),
		},
		Provenance: model.Provenance{
			DataSourceID:   run.DataSourceID,
			IngestionRunID: run.ID,
		},
	}
}
