package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/luisdourado/invs/internal/metadata"
)

func TestLoadBackfillRegistrationValidatesPinnedCopiesWithoutReadingOrigins(t *testing.T) {
	root, manifestPath, sourceManifestPath, sourcePartPath, _ := writeBackfillFixture(t)
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(sourceManifestPath))); !os.IsNotExist(err) {
		t.Fatalf("source manifest unexpectedly exists or stat failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(sourcePartPath))); !os.IsNotExist(err) {
		t.Fatalf("source part unexpectedly exists or stat failed: %v", err)
	}
	registration, counts, err := loadBackfillRegistration(manifestPath, root)
	if err != nil {
		t.Fatal(err)
	}
	if registration.ManifestPath != strings.TrimPrefix(manifestPath, root+string(filepath.Separator)) {
		t.Fatalf("manifest path = %q", registration.ManifestPath)
	}
	if counts.Sessions != 1 || counts.Rankings != 1 || counts.PriceBars != 1 {
		t.Fatalf("counts = %+v", counts)
	}
}

func TestLoadBackfillRegistrationRejectsPinnedPartHashMismatch(t *testing.T) {
	root, manifestPath, _, _, pinnedPart := writeBackfillFixture(t)
	if err := os.WriteFile(pinnedPart, []byte("changed parquet bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadBackfillRegistration(manifestPath, root); err == nil || !strings.Contains(err.Error(), "archived price part hash mismatch") {
		t.Fatalf("got %v", err)
	}
}

func TestArtifactPathsRejectTraversalAndSymlinkEscape(t *testing.T) {
	if err := validateRelativePath("backfills/../secret"); err == nil {
		t.Fatal("expected traversal rejection")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "artifact"), []byte("evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "artifact"), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := readArtifact(root, "escape"); err == nil || !strings.Contains(err.Error(), "escapes discovery-root") {
		t.Fatalf("got %v", err)
	}
}

func writeBackfillFixture(t *testing.T) (root, manifestPath, sourceManifestPath, sourcePartPath, pinnedPartPath string) {
	t.Helper()
	root = t.TempDir()
	fingerprint := strings.Repeat("a", 64)
	runID := uuid.NewSHA1(uuid.MustParse("dfd74827-a6cb-5a1a-821d-22bbd8a06d5e"), []byte(fingerprint)).String()
	base := filepath.Join("backfills", runID)
	profilePath := filepath.ToSlash(filepath.Join(base, "inputs/profile.yaml"))
	calendarPath := filepath.ToSlash(filepath.Join(base, "inputs/calendar-snapshot.json"))
	archiveManifestPath := filepath.ToSlash(filepath.Join(base, "inputs/manifests", strings.Repeat("2", 64)+".json"))
	pinnedPartPath = filepath.Join(root, filepath.FromSlash(filepath.ToSlash(filepath.Join(base, "inputs/normalized/prices/part.parquet"))))
	sourceManifestPath = "normalized/prices/manifest.json"
	sourcePartPath = "normalized/prices/part.parquet"
	profileBytes := []byte("profile snapshot\n")
	calendarBytes, err := json.Marshal(map[string]any{
		"manifest": map[string]any{
			"data_source_id": "30000000-0000-4000-8000-000000000001", "mic": "XNAS",
			"calendar_version": "nasdaq-2026", "session_fingerprint": strings.Repeat("e", 64),
			"session_count": 1, "available_at": "2026-09-20T12:00:00Z",
		},
		"sessions": []any{map[string]string{"session_date": "2026-09-21", "session_status": "open"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	partBytes := []byte("pinned parquet bytes\n")
	partHash := hashBytes(partBytes)
	archiveBytes, err := json.Marshal(map[string]any{"parts": []any{map[string]string{"path": "part.parquet", "sha256": partHash}}})
	if err != nil {
		t.Fatal(err)
	}
	archiveHash := hashBytes(archiveBytes)
	for relative, content := range map[string][]byte{
		profilePath: profileBytes, calendarPath: calendarBytes,
		archiveManifestPath: archiveBytes,
		filepath.ToSlash(filepath.Join(base, "inputs/normalized/prices/part.parquet")): partBytes,
	} {
		fullPath := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	featureScore := decodeBackfillNumber(t, `"70.0"`)
	open := decodeBackfillValue(t, `10`)
	high := decodeBackfillValue(t, `12`)
	low := decodeBackfillValue(t, `9`)
	closeValue := decodeBackfillValue(t, `11`)
	manifest := metadata.DiscoveryBackfillManifest{
		SchemaVersion:       metadata.DiscoveryBackfillSchemaVersion,
		GeneratorVersion:    metadata.DiscoveryBackfillGeneratorVersion,
		ReconstructionRunID: runID,
		ObservationMode:     "retrospective_current_universe", UniverseBasis: "current_profile_membership_snapshot",
		ReconstructedAt: "2026-09-22T18:00:00Z", InputFingerprint: fingerprint,
		Profile: metadata.DiscoveryBackfillProfile{
			UniverseID: "20000000-0000-4000-8000-000000000001", Name: "test-universe",
			MembershipAsOf: "2026-09-21", Fingerprint: strings.Repeat("b", 64),
			ProfileSHA256: hashBytes(profileBytes), Size: 1, SnapshotPath: profilePath,
		},
		Model: metadata.DiscoveryBackfillModel{
			Name: "momentum-risk-rank", Version: "1.0.0", GitCommit: "unknown",
			Weights: map[string]string{"return_3m": "0.35"},
		},
		Range: metadata.DiscoveryBackfillRange{StartDate: "2026-09-21", EndDate: "2026-09-21"},
		Calendar: metadata.DiscoveryBackfillCalendar{
			DataSourceID: "30000000-0000-4000-8000-000000000001", MIC: "XNAS",
			CalendarVersion: "nasdaq-2026", SessionCount: 1, SessionFingerprint: strings.Repeat("e", 64),
			AvailableAt: "2026-09-20T12:00:00Z", SnapshotSHA256: hashBytes(calendarBytes), SnapshotPath: calendarPath,
		},
		PriceInputs: metadata.DiscoveryBackfillPriceInputs{
			Source: "yahoo", Interval: "1d", PriceBasis: "split_adjusted", Currency: "USD",
			HistoricalFitness: "installation_replay_only", AvailabilityPolicy: "current_vintage_with_preserved_receipt_times",
			InputFingerprint: strings.Repeat("1", 64), SelectedRowCount: 1,
			Manifests: []metadata.DiscoveryBackfillManifestRef{{
				Path: archiveManifestPath, SourcePath: sourceManifestPath, SHA256: archiveHash,
				Parts: []metadata.DiscoveryBackfillPartRef{{
					Path:       filepath.ToSlash(filepath.Join(base, "inputs/normalized/prices/part.parquet")),
					SourcePath: sourcePartPath, SHA256: partHash,
				}},
			}},
		},
		Sessions: []metadata.DiscoveryBackfillSession{{
			IndexID: "40000000-0000-4000-8000-000000000001", MarketSession: "2026-09-21",
			DecisionAt: "2026-09-21T20:00:00Z", CandidateCount: 1, EligibleCount: 1,
			PriceInputFingerprint: strings.Repeat("4", 64),
			Rankings: []metadata.DiscoveryBackfillRanking{{
				Rank: intRef(1), SecurityID: "50000000-0000-4000-8000-000000000001", Ticker: "AAA",
				Sector: "technology", Candidate: true, Score: featureScore,
				Features: metadata.DiscoveryBackfillFeatures{
					Return1M: decodeBackfillNumber(t, `"0.1"`), Return3M: decodeBackfillNumber(t, `"0.2"`),
					Return6M: decodeBackfillNumber(t, `"0.3"`), Return12M: decodeBackfillNumber(t, `"0.4"`),
					RealizedVolatility1M: decodeBackfillNumber(t, `"0.05"`), MaxDrawdown1M: decodeBackfillNumber(t, `"0.02"`),
				},
				Eligibility: metadata.DiscoveryBackfillEligibility{Status: "eligible", Reasons: []string{}},
			}},
			PriceBars: []metadata.DiscoveryBackfillPriceBar{{
				SecurityID: "50000000-0000-4000-8000-000000000001", Ticker: "AAA",
				ObservedAt: "2026-09-21T20:00:00Z", AvailableAt: "2026-09-21T20:01:00Z", IngestedAt: "2026-09-21T20:02:00Z",
				Source: "yahoo", Interval: "1d", PriceBasis: "split_adjusted", Currency: "USD",
				Open: open, High: high, Low: low, Close: closeValue,
				RawPayloadHash: strings.Repeat("5", 64), DataSourceID: "60000000-0000-4000-8000-000000000001",
				IngestionRunID: "70000000-0000-4000-8000-000000000001", RawRecordLocator: "prices/AAA/2026-09-21",
				ManifestPath: archiveManifestPath, SourceManifestPath: sourceManifestPath, ManifestSHA256: archiveHash,
				PartPath:       filepath.ToSlash(filepath.Join(base, "inputs/normalized/prices/part.parquet")),
				SourcePartPath: sourcePartPath, PartSHA256: partHash,
			}},
		}},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath = filepath.Join(root, filepath.FromSlash(filepath.ToSlash(filepath.Join(base, "manifest.json"))))
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, append(manifestBytes, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, manifestPath, sourceManifestPath, sourcePartPath, pinnedPartPath
}

func decodeBackfillNumber(t *testing.T, encoded string) *metadata.DiscoveryBackfillNumber {
	t.Helper()
	var value metadata.DiscoveryBackfillNumber
	if err := json.Unmarshal([]byte(encoded), &value); err != nil {
		t.Fatal(err)
	}
	return &value
}

func decodeBackfillValue(t *testing.T, encoded string) metadata.DiscoveryBackfillNumber {
	t.Helper()
	var value metadata.DiscoveryBackfillNumber
	if err := json.Unmarshal([]byte(encoded), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func intRef(value int) *int { return &value }

func hashBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
