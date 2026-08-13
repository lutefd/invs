package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/luisdourado/invs/internal/storage"
)

func TestReconcileReportsActiveRunAndFilesystemDrift(t *testing.T) {
	root := t.TempDir()
	dataRoot := filepath.Join(root, "data")
	if err := os.MkdirAll(dataRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	rawStore, err := storage.NewFileRawStore(filepath.Join(dataRoot, "raw"))
	if err != nil {
		t.Fatal(err)
	}
	runID := uuid.NewString()
	rawManifest := storage.NewRawManifest("yahoo", runID)
	if _, _, err := storage.PublishRawManifest(context.Background(), rawStore, rawManifest, time.Now()); err != nil {
		t.Fatal(err)
	}

	normalizedDir := filepath.Join(dataRoot, "normalized", "prices", "source=yahoo", "security_id="+uuid.NewString())
	if err := os.MkdirAll(normalizedDir, 0o750); err != nil {
		t.Fatal(err)
	}
	missingHash := stringsRepeat("a", 64)
	manifest := map[string]any{
		"manifest_version":   1,
		"schema_version":     "1.0.0",
		"normalizer_version": "test",
		"git_commit":         "unknown",
		"source":             "yahoo",
		"data_source_id":     uuid.NewString(),
		"ingestion_run_id":   uuid.NewString(),
		"partition":          map[string]string{"dataset": "prices", "source": "yahoo", "security_id": uuid.NewString()},
		"row_count":          1,
		"parts":              []map[string]any{{"path": "part-" + missingHash + ".parquet", "sha256": missingHash, "row_count": 1}},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(normalizedDir, "manifest.json"), manifestBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(normalizedDir, "part-unlisted.parquet"), []byte("unlisted"), 0o640); err != nil {
		t.Fatal(err)
	}

	report, err := Reconcile(context.Background(), Roots{DataRoot: dataRoot}, []RunRecord{{ID: runID, Source: "yahoo", RunKey: "daily", Status: "running"}}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.QueuedOrRunning) != 1 {
		t.Fatalf("active runs = %d, want 1", len(report.QueuedOrRunning))
	}
	if len(report.RawManifestsWithoutTerminalRun) != 1 {
		t.Fatalf("raw manifests without terminal run = %d, want 1", len(report.RawManifestsWithoutTerminalRun))
	}
	if len(report.MissingNormalizedParts) != 1 || len(report.UnlistedNormalizedParts) != 1 {
		t.Fatalf("normalized findings = missing %d unlisted %d, want 1 each", len(report.MissingNormalizedParts), len(report.UnlistedNormalizedParts))
	}
	if report.Summary.TotalIssues != 4 {
		t.Fatalf("total issues = %d, want 4", report.Summary.TotalIssues)
	}
}

func TestReconcileResolvesFeatureLineage(t *testing.T) {
	root := t.TempDir()
	dataRoot := filepath.Join(root, "data")
	normalizedDir := filepath.Join(dataRoot, "normalized", "prices", "source=yahoo", "security_id="+uuid.NewString())
	featureDir := filepath.Join(dataRoot, "features", "market-basic", "1.0.0", "artifact-test")
	if err := os.MkdirAll(normalizedDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(featureDir, 0o750); err != nil {
		t.Fatal(err)
	}
	partBytes := []byte("canonical")
	hash := sha256.Sum256(partBytes)
	partHash := hex.EncodeToString(hash[:])
	partName := "part-" + partHash + ".parquet"
	if err := os.WriteFile(filepath.Join(normalizedDir, partName), partBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"manifest_version":   1,
		"schema_version":     "1.0.0",
		"normalizer_version": "test",
		"git_commit":         "unknown",
		"source":             "yahoo",
		"data_source_id":     uuid.NewString(),
		"ingestion_run_id":   uuid.NewString(),
		"partition":          map[string]string{"dataset": "prices", "source": "yahoo", "security_id": uuid.NewString()},
		"row_count":          1,
		"parts":              []map[string]any{{"path": partName, "sha256": partHash, "row_count": 1}},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestHash := sha256.Sum256(manifestBytes)
	manifestHashText := hex.EncodeToString(manifestHash[:])
	manifestPath := filepath.Join(normalizedDir, "manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	outputBytes := []byte("feature")
	outputHash := sha256.Sum256(outputBytes)
	outputHashText := hex.EncodeToString(outputHash[:])
	if err := os.WriteFile(filepath.Join(featureDir, "part-"+outputHashText+".parquet"), outputBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	inputPath, err := filepath.Rel(dataRoot, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	featureManifest := map[string]any{
		"schema_version":      "1.0.0",
		"manifest_version":    "1.0.0",
		"feature_set":         "market-basic",
		"feature_set_version": "1.0.0",
		"feature_names":       []string{"close", "return_1d", "range_1d", "volume"},
		"artifact": map[string]string{
			"artifact_id":       uuid.NewString(),
			"artifact_version":  "1.0.0",
			"generator_version": "test",
			"git_commit":        "unknown",
			"created_at":        "2026-01-01T00:00:00Z",
		},
		"decision_at":               "2026-01-01T00:00:00Z",
		"input_available_at":        "2026-01-01T00:00:00Z",
		"computation_delay_seconds": 0,
		"available_at":              "2026-01-01T00:00:00Z",
		"input_fingerprint":         stringsRepeat("b", 64),
		"selected_input_manifests":  []map[string]string{{"path": filepath.ToSlash(inputPath), "sha256": manifestHashText}},
		"selected_input_parts":      []map[string]string{{"path": partName, "sha256": partHash}},
		"row_count":                 1,
		"parts":                     []map[string]any{{"path": "part-" + outputHashText + ".parquet", "sha256": outputHashText, "row_count": 1}},
	}
	featureBytes, err := json.Marshal(featureManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(featureDir, "manifest.json"), featureBytes, 0o640); err != nil {
		t.Fatal(err)
	}

	report, err := Reconcile(context.Background(), Roots{DataRoot: dataRoot}, nil, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.TotalIssues != 0 {
		t.Fatalf("clean lineage report has %d issues: %+v", report.Summary.TotalIssues, report)
	}

	if err := os.Remove(filepath.Join(normalizedDir, partName)); err != nil {
		t.Fatal(err)
	}
	report, err = Reconcile(context.Background(), Roots{DataRoot: dataRoot}, nil, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.FeatureArtifactsWithUnavailableInputs) != 1 {
		t.Fatalf("unavailable feature artifacts = %d, want 1", len(report.FeatureArtifactsWithUnavailableInputs))
	}
}

func stringsRepeat(value string, count int) string {
	result := ""
	for range count {
		result += value
	}
	return result
}
