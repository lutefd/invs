package metadata

import (
	"strings"
	"testing"
	"time"
)

func testFeatureArtifactRegistration() FeatureArtifactRegistration {
	firstDecision := time.Date(2026, 8, 1, 21, 0, 0, 0, time.UTC)
	secondDecision := firstDecision.Add(24 * time.Hour)
	return FeatureArtifactRegistration{
		ArtifactID:                 "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		ArtifactVersion:            "1.0.0",
		FeatureSet:                 "market-basic",
		FeatureSetVersion:          "1.0.0",
		RegistrySHA256:             strings.Repeat("1", 64),
		GeneratorVersion:           "python-market-basic-1.1.0",
		GitCommit:                  "unknown",
		DecisionPoints:             []FeatureArtifactDecisionPoint{{Ordinal: 1, DecisionAt: secondDecision}, {Ordinal: 0, DecisionAt: firstDecision}},
		UniverseFingerprint:        strings.Repeat("2", 64),
		Universe:                   []FeatureArtifactUniverseMember{{Ordinal: 1, SecurityID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}, {Ordinal: 0, SecurityID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc"}},
		InputFitness:               []FeatureArtifactInputFitness{{Dataset: "prices", HistoricalFitness: "installation_replay_only", AvailabilityPolicy: "conservative_receipt_time"}},
		InputFingerprint:           strings.Repeat("3", 64),
		InputRefs:                  []FeatureArtifactInputRef{{Kind: FeatureArtifactInputPart, Path: "prices/part-b", SHA256: strings.Repeat("5", 64)}, {Kind: FeatureArtifactInputManifest, Path: "prices/manifest.json", SHA256: strings.Repeat("4", 64)}},
		OutputManifestPath:         "batches/market-basic/1.0.0/batch-aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa/manifest.json",
		OutputManifestSHA256:       strings.Repeat("6", 64),
		CalendarDataSourceID:       "dddddddd-dddd-4ddd-8ddd-dddddddddddd",
		CalendarMIC:                "XNAS",
		CalendarVersion:            "nasdaq-2026",
		CalendarSessionFingerprint: strings.Repeat("7", 64),
		CalendarAvailableAt:        firstDecision.Add(-48 * time.Hour),
		DecisionClockPolicy:        "after_close_next_session",
		RowCount:                   2,
		RequestedPartitions:        2,
		AcceptedPartitions:         2,
		RejectedPartitions:         0,
		Partitions: []FeatureArtifactPartition{
			{
				SecurityID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", DecisionAt: secondDecision,
				ChildArtifactID: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", ManifestPath: "market-basic/child-2/manifest.json", ManifestSHA256: strings.Repeat("8", 64),
				PartPath: "market-basic/child-2/part-2.parquet", PartSHA256: strings.Repeat("9", 64), RowCount: 1,
			},
			{
				SecurityID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", DecisionAt: firstDecision,
				ChildArtifactID: "ffffffff-ffff-4fff-8fff-ffffffffffff", ManifestPath: "market-basic/child-1/manifest.json", ManifestSHA256: strings.Repeat("a", 64),
				PartPath: "market-basic/child-1/part-1.parquet", PartSHA256: strings.Repeat("b", 64), RowCount: 1,
			},
		},
		Status:    FeatureArtifactPublishedStatus,
		CreatedAt: firstDecision,
	}
}

func TestFeatureArtifactRegistrationNormalizesAndFingerprintsLineage(t *testing.T) {
	registration := testFeatureArtifactRegistration()
	normalized, err := registration.normalized()
	if err != nil {
		t.Fatal(err)
	}
	if !normalized.DecisionStart.Equal(registration.DecisionPoints[1].DecisionAt) || !normalized.DecisionEnd.Equal(registration.DecisionPoints[0].DecisionAt) {
		t.Fatalf("decision range = %s..%s, want schedule bounds", normalized.DecisionStart, normalized.DecisionEnd)
	}
	if normalized.InputRefs[0].Kind != FeatureArtifactInputManifest || normalized.Partitions[0].DecisionAt != registration.DecisionPoints[1].DecisionAt {
		t.Fatalf("registration was not sorted canonically: %+v", normalized)
	}
	firstDigest, err := featureArtifactRegistrationSHA256(normalized)
	if err != nil {
		t.Fatal(err)
	}

	reordered := testFeatureArtifactRegistration()
	reordered.InputRefs = append([]FeatureArtifactInputRef(nil), registration.InputRefs...)
	reordered.Partitions = append([]FeatureArtifactPartition(nil), registration.Partitions...)
	reordered.DecisionPoints = append([]FeatureArtifactDecisionPoint(nil), registration.DecisionPoints...)
	reordered.Universe = append([]FeatureArtifactUniverseMember(nil), registration.Universe...)
	reordered.DecisionPoints[0], reordered.DecisionPoints[1] = reordered.DecisionPoints[1], reordered.DecisionPoints[0]
	reordered.Universe[0], reordered.Universe[1] = reordered.Universe[1], reordered.Universe[0]
	reordered.InputRefs[0], reordered.InputRefs[1] = reordered.InputRefs[1], reordered.InputRefs[0]
	reordered.Partitions[0], reordered.Partitions[1] = reordered.Partitions[1], reordered.Partitions[0]
	normalizedAgain, err := reordered.normalized()
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := featureArtifactRegistrationSHA256(normalizedAgain)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("equivalent registration digests differ: %s != %s", firstDigest, secondDigest)
	}
}

func TestFeatureArtifactRegistrationRejectsInvalidLineage(t *testing.T) {
	cases := []struct {
		name string
		edit func(*FeatureArtifactRegistration)
		want string
	}{
		{"unsafe output path", func(r *FeatureArtifactRegistration) { r.OutputManifestPath = "../manifest.json" }, "unsafe path component"},
		{"duplicate input path", func(r *FeatureArtifactRegistration) { r.InputRefs = append(r.InputRefs, r.InputRefs[0]) }, "duplicated"},
		{"outside universe", func(r *FeatureArtifactRegistration) {
			r.Partitions[0].SecurityID = "99999999-9999-4999-8999-999999999999"
		}, "outside the universe"},
		{"row count mismatch", func(r *FeatureArtifactRegistration) { r.RowCount = 99 }, "does not match partition rows"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registration := testFeatureArtifactRegistration()
			tc.edit(&registration)
			if _, err := registration.normalized(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("normalized error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestFeatureArtifactRegistrationSQLIsIdempotent(t *testing.T) {
	for _, fragment := range []string{
		"INSERT INTO feature_artifacts",
		"ON CONFLICT (artifact_id) DO NOTHING",
		"RETURNING registration_sha256",
	} {
		if !strings.Contains(insertFeatureArtifactSQL, fragment) {
			t.Fatalf("insertFeatureArtifactSQL missing %q", fragment)
		}
	}
}
