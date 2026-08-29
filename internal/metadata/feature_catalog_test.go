package metadata

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func featureCatalogTestParent(requested, accepted, rejected, rows int64) featureArtifactCatalogParent {
	firstDecision := time.Date(2026, 8, 1, 21, 0, 0, 0, time.UTC)
	return featureArtifactCatalogParent{
		ArtifactID:                 "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		ArtifactVersion:            "1.0.0",
		FeatureSet:                 "market-basic",
		FeatureSetVersion:          "1.0.0",
		RegistrySHA256:             strings.Repeat("1", 64),
		GeneratorVersion:           "python-market-basic-1.1.0",
		GitCommit:                  "unknown",
		DecisionStart:              firstDecision,
		DecisionEnd:                firstDecision.Add(24 * time.Hour),
		UniverseFingerprint:        strings.Repeat("2", 64),
		InputFingerprint:           strings.Repeat("3", 64),
		OutputManifestPath:         "batches/market-basic/1.0.0/batch-aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa/manifest.json",
		OutputManifestSHA256:       strings.Repeat("4", 64),
		CalendarDataSourceID:       "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		CalendarMIC:                "XNAS",
		CalendarVersion:            "nasdaq-2026",
		CalendarSessionFingerprint: strings.Repeat("5", 64),
		CalendarAvailableAt:        firstDecision.Add(-24 * time.Hour),
		DecisionClockPolicy:        "after_close_next_session",
		RowCount:                   rows,
		RequestedPartitions:        requested,
		AcceptedPartitions:         accepted,
		RejectedPartitions:         rejected,
		Status:                     FeatureArtifactPublishedStatus,
		CreatedAt:                  firstDecision,
		RegisteredAt:               firstDecision.Add(time.Minute),
	}
}

func featureCatalogTestDetails(partitions int) featureArtifactCatalogDetails {
	firstDecision := time.Date(2026, 8, 1, 21, 0, 0, 0, time.UTC)
	secondDecision := firstDecision.Add(24 * time.Hour)
	details := featureArtifactCatalogDetails{
		DecisionPoints: []FeatureArtifactDecisionPoint{
			{Ordinal: 0, DecisionAt: firstDecision},
			{Ordinal: 1, DecisionAt: secondDecision},
		},
		Universe: []FeatureArtifactUniverseMember{
			{Ordinal: 0, SecurityID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc"},
			{Ordinal: 1, SecurityID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd"},
		},
		InputFitness: []FeatureArtifactInputFitness{{
			Dataset: "prices", HistoricalFitness: "installation_replay_only", AvailabilityPolicy: "conservative_receipt_time",
		}},
		InputRefs: []FeatureArtifactInputRef{{
			Kind: FeatureArtifactInputManifest, Path: "normalized/prices/manifest.json", SHA256: strings.Repeat("6", 64),
		}},
	}
	allPartitions := []FeatureArtifactPartition{
		{SecurityID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", DecisionAt: firstDecision, ChildArtifactID: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", ManifestPath: "child-1/manifest.json", ManifestSHA256: strings.Repeat("7", 64), PartPath: "child-1/part.parquet", PartSHA256: strings.Repeat("8", 64), RowCount: 1},
		{SecurityID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", DecisionAt: firstDecision, ChildArtifactID: "ffffffff-ffff-4fff-8fff-ffffffffffff", ManifestPath: "child-2/manifest.json", ManifestSHA256: strings.Repeat("9", 64), PartPath: "child-2/part.parquet", PartSHA256: strings.Repeat("a", 64), RowCount: 1},
		{SecurityID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", DecisionAt: secondDecision, ChildArtifactID: "11111111-1111-4111-8111-111111111111", ManifestPath: "child-3/manifest.json", ManifestSHA256: strings.Repeat("b", 64), PartPath: "child-3/part.parquet", PartSHA256: strings.Repeat("c", 64), RowCount: 1},
		{SecurityID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", DecisionAt: secondDecision, ChildArtifactID: "22222222-2222-4222-8222-222222222222", ManifestPath: "child-4/manifest.json", ManifestSHA256: strings.Repeat("d", 64), PartPath: "child-4/part.parquet", PartSHA256: strings.Repeat("e", 64), RowCount: 1},
	}
	details.Partitions = allPartitions[:partitions]
	return details
}

func TestSummarizeFeatureArtifactCatalogArtifactReportsCompleteCoverage(t *testing.T) {
	artifact := summarizeFeatureArtifactCatalogArtifact(
		featureCatalogTestParent(4, 4, 0, 4),
		featureCatalogTestDetails(4),
	)
	if artifact.CoverageStatus != "complete" || len(artifact.Issues) != 0 {
		t.Fatalf("coverage = %s, issues = %v", artifact.CoverageStatus, artifact.Issues)
	}
	if artifact.ExpectedPartitions != 4 || !artifact.PartitionGridConsistent || !artifact.RowCountConsistent {
		t.Fatalf("coverage consistency = %+v", artifact)
	}
	if len(artifact.DecisionCoverage) != 2 || artifact.DecisionCoverage[0].AcceptedPartitions != 2 || artifact.DecisionCoverage[1].UnaccountedPartitions != 0 {
		t.Fatalf("decision coverage = %+v", artifact.DecisionCoverage)
	}
}

func TestSummarizeFeatureArtifactCatalogArtifactReportsPartialCoverage(t *testing.T) {
	artifact := summarizeFeatureArtifactCatalogArtifact(
		featureCatalogTestParent(4, 3, 1, 3),
		featureCatalogTestDetails(3),
	)
	if artifact.CoverageStatus != "partial" {
		t.Fatalf("coverage = %s, want partial", artifact.CoverageStatus)
	}
	if artifact.DecisionCoverage[1].UnaccountedPartitions != 1 {
		t.Fatalf("unaccounted second decision partitions = %d", artifact.DecisionCoverage[1].UnaccountedPartitions)
	}
}

func TestSummarizeFeatureArtifactCatalogArtifactReportsInconsistency(t *testing.T) {
	parent := featureCatalogTestParent(3, 1, 2, 2)
	artifact := summarizeFeatureArtifactCatalogArtifact(parent, featureCatalogTestDetails(1))
	if artifact.CoverageStatus != "inconsistent" {
		t.Fatalf("coverage = %s, want inconsistent", artifact.CoverageStatus)
	}
	if artifact.PartitionGridConsistent || artifact.RowCountConsistent {
		t.Fatalf("consistency flags = %+v", artifact)
	}
	if len(artifact.Issues) < 2 {
		t.Fatalf("issues = %v, want grid and row-count findings", artifact.Issues)
	}
}

func TestBuildFeatureArtifactCatalogReportSortsAndAggregates(t *testing.T) {
	first := summarizeFeatureArtifactCatalogArtifact(featureCatalogTestParent(4, 4, 0, 4), featureCatalogTestDetails(4))
	secondParent := featureCatalogTestParent(4, 3, 1, 3)
	secondParent.ArtifactID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	second := summarizeFeatureArtifactCatalogArtifact(secondParent, featureCatalogTestDetails(3))
	report := BuildFeatureArtifactCatalogReport(
		time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC),
		FeatureArtifactCatalogFilter{FeatureSet: "market-basic"},
		[]FeatureArtifactCatalogArtifact{second, first},
	)
	if report.Version != FeatureArtifactCatalogReportVersion || !report.GeneratedAt.Equal(time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("report metadata = %+v", report)
	}
	if report.Artifacts[0].ArtifactID != first.ArtifactID || report.Summary.ArtifactCount != 2 {
		t.Fatalf("report ordering/summary = %+v", report)
	}
	if report.Summary.CompleteArtifacts != 1 || report.Summary.PartialArtifacts != 1 || report.Summary.RejectedPartitions != 1 || report.Summary.RowCount != 7 {
		t.Fatalf("report summary = %+v", report.Summary)
	}
}

func TestBuildFeatureArtifactCatalogReportUsesEmptyArrays(t *testing.T) {
	report := BuildFeatureArtifactCatalogReport(time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC), FeatureArtifactCatalogFilter{}, []FeatureArtifactCatalogArtifact{})
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"artifacts":null`) {
		t.Fatalf("empty report encoded a null artifacts value: %s", encoded)
	}
}

func TestFeatureArtifactCatalogFilterAndQuery(t *testing.T) {
	filter, err := normalizeFeatureArtifactCatalogFilter(FeatureArtifactCatalogFilter{
		ArtifactID:        "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA",
		FeatureSet:        " market-basic ",
		FeatureSetVersion: " 1.0.0 ",
	})
	if err == nil || !strings.Contains(err.Error(), "lower-case canonical UUID") {
		t.Fatalf("uppercase filter error = %v", err)
	}
	filter, err = normalizeFeatureArtifactCatalogFilter(FeatureArtifactCatalogFilter{
		ArtifactID:        "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		FeatureSet:        " market-basic ",
		FeatureSetVersion: " 1.0.0 ",
	})
	if err != nil {
		t.Fatal(err)
	}
	query, args := featureArtifactCatalogParentQuery(filter)
	if len(args) != 3 || !strings.Contains(query, "fa.artifact_id = $1::uuid") || !strings.Contains(query, "fa.feature_set = $2") || !strings.Contains(query, "fa.feature_set_version = $3") {
		t.Fatalf("query = %s args = %#v", query, args)
	}
	if strings.Contains(strings.ToUpper(query), "INSERT") || strings.Contains(strings.ToUpper(query), "UPDATE") || strings.Contains(strings.ToUpper(query), "DELETE") {
		t.Fatalf("catalog query is not read-only: %s", query)
	}
}
