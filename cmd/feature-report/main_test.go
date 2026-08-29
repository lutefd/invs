package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/luisdourado/invs/internal/metadata"
)

func TestPrintTextIncludesCoverageAndLineage(t *testing.T) {
	report := metadata.FeatureArtifactCatalogReport{
		Version:     metadata.FeatureArtifactCatalogReportVersion,
		GeneratedAt: time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC),
		Summary: metadata.FeatureArtifactCatalogSummary{
			ArtifactCount: 1, CompleteArtifacts: 1, AcceptedPartitions: 1,
			CatalogedPartitions: 1, RowCount: 1,
		},
		Artifacts: []metadata.FeatureArtifactCatalogArtifact{{
			ArtifactID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", FeatureSet: "market-basic", FeatureSetVersion: "1.0.0",
			Status: "published", CoverageStatus: "complete", DecisionPointCount: 1, UniverseCount: 1,
			AcceptedPartitions: 1, RowCount: 1, OutputManifestPath: "batches/manifest.json", OutputManifestSHA256: strings.Repeat("a", 64),
			DecisionCoverage: []metadata.FeatureArtifactDecisionCoverage{{
				DecisionAt: time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC), ExpectedPartitions: 1, AcceptedPartitions: 1, AcceptedRowCount: 1,
			}},
			InputFitness: []metadata.FeatureArtifactInputFitness{{Dataset: "prices", HistoricalFitness: "installation_replay_only", AvailabilityPolicy: "conservative_receipt_time"}},
			InputRefs:    []metadata.FeatureArtifactInputRef{{Kind: metadata.FeatureArtifactInputManifest, Path: "normalized/prices/manifest.json", SHA256: strings.Repeat("b", 64)}},
			Partitions:   []metadata.FeatureArtifactPartition{{SecurityID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", DecisionAt: time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC), ChildArtifactID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", PartPath: "child/part.parquet", RowCount: 1}},
		}},
	}
	var output bytes.Buffer
	printText(&output, report)
	text := output.String()
	for _, fragment := range []string{"coverage=complete", "decision=2026-08-29T12:00:00Z", "input_fitness=prices", "input=manifest", "partition=bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("text report missing %q: %s", fragment, text)
		}
	}
}
