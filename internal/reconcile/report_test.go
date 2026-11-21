package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	featureDocument := map[string]any{
		"schema_version":      "1.1.0",
		"manifest_version":    "1.1.0",
		"feature_set":         "market-basic",
		"feature_set_version": "1.0.0",
		"feature_names":       []string{"close", "return_1d", "range_1d", "volume"},
		"artifact": map[string]string{
			"artifact_id":       uuid.NewString(),
			"artifact_version":  "1.1.0",
			"generator_version": "test",
			"git_commit":        "unknown",
			"created_at":        "2026-01-01T00:00:00Z",
		},
		"calendar_pin": map[string]string{
			"data_source_id":        "11111111-1111-4111-8111-111111111111",
			"mic":                   "XNAS",
			"calendar_version":      "xnas_2026_fixture",
			"session_fingerprint":   stringsRepeat("e", 64),
			"calendar_available_at": "2025-12-31T00:00:00Z",
			"decision_clock_policy": "after_close_next_session",
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
	canonicalBytes, err := json.Marshal(featureDocument)
	if err != nil {
		t.Fatal(err)
	}
	var canonicalManifest featureManifest
	if err := json.Unmarshal(canonicalBytes, &canonicalManifest); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := featureInputFingerprint(canonicalManifest)
	if err != nil {
		t.Fatal(err)
	}
	featureDocument["input_fingerprint"] = fingerprint
	featureBytes, err := json.Marshal(featureDocument)
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
	if report.Summary.Status != "ok" {
		t.Fatalf("clean lineage status = %q, want ok", report.Summary.Status)
	}

	// Advance the mutable manifest pointer while preserving the exact bytes
	// pinned by the feature artifact, as the normalized writer now does.
	archiveDir := filepath.Join(normalizedDir, ".manifest-history")
	if err := os.MkdirAll(archiveDir, 0o750); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(archiveDir, manifestHashText+".json")
	if err := os.WriteFile(archivePath, manifestBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	laterBytes := []byte("later append")
	laterDigest := sha256.Sum256(laterBytes)
	laterHash := hex.EncodeToString(laterDigest[:])
	laterName := "part-" + laterHash + ".parquet"
	if err := os.WriteFile(filepath.Join(normalizedDir, laterName), laterBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	currentParts := append(manifest["parts"].([]map[string]any), map[string]any{
		"path": laterName, "sha256": laterHash, "row_count": 1,
	})
	manifest["parts"] = currentParts
	manifest["row_count"] = 2
	advancedManifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, advancedManifestBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	report, err = Reconcile(context.Background(), Roots{DataRoot: dataRoot}, nil, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.TotalIssues != 0 || len(report.FeatureArtifactsWithUnavailableInputs) != 0 {
		t.Fatalf("exact historical manifest archive should preserve lineage: %+v", report)
	}
	if report.Summary.FeatureArtifactLineageAdvisories != 0 || report.Summary.Status != "ok" {
		t.Fatalf("archived lineage should not be degraded: %+v", report.Summary)
	}

	// A present but corrupt archive is evidence of tampering and must not be
	// hidden by the permissive legacy fallback.
	if err := os.WriteFile(archivePath, []byte(`{"tampered":true}`), 0o640); err != nil {
		t.Fatal(err)
	}
	report, err = Reconcile(context.Background(), Roots{DataRoot: dataRoot}, nil, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.FeatureArtifactsWithUnavailableInputs) != 1 || report.Summary.FeatureArtifactLineageAdvisories != 0 {
		t.Fatalf("corrupt historical archive must remain an issue: %+v", report)
	}
	if err := os.WriteFile(archivePath, manifestBytes, 0o640); err != nil {
		t.Fatal(err)
	}

	// Legacy pre-archive manifests may use the current valid append-only
	// pointer only when every selected part remains listed and hash-valid.
	if err := os.Remove(archivePath); err != nil {
		t.Fatal(err)
	}
	report, err = Reconcile(context.Background(), Roots{DataRoot: dataRoot}, nil, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.TotalIssues != 0 || len(report.FeatureArtifactsWithUnavailableInputs) != 0 {
		t.Fatalf("valid legacy append-only fallback should remain non-fatal: %+v", report)
	}
	if report.Summary.Status != "degraded" || report.Summary.FeatureArtifactLineageAdvisories != 1 {
		t.Fatalf("legacy fallback must surface degraded status and advisory: %+v", report.Summary)
	}
	if len(report.FeatureArtifactLineageAdvisories) != 1 || !strings.Contains(report.FeatureArtifactLineageAdvisories[0].Detail, "append-only") {
		t.Fatalf("missing fallback advisory detail: %+v", report.FeatureArtifactLineageAdvisories)
	}

	// The fallback never masks a selected part that has been modified.
	if err := os.WriteFile(filepath.Join(normalizedDir, partName), []byte("tampered"), 0o640); err != nil {
		t.Fatal(err)
	}
	report, err = Reconcile(context.Background(), Roots{DataRoot: dataRoot}, nil, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.FeatureArtifactsWithUnavailableInputs) != 1 || len(report.NormalizedPartHashMismatches) != 1 {
		t.Fatalf("tampered selected part should remain visible: %+v", report)
	}
	if report.Summary.FeatureArtifactLineageAdvisories != 0 {
		t.Fatalf("failed fallback must not be reported as an accepted advisory: %+v", report.Summary)
	}

	if err := os.WriteFile(filepath.Join(normalizedDir, partName), partBytes, 0o640); err != nil {
		t.Fatal(err)
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
	if report.Summary.MissingNormalizedParts != 1 {
		t.Fatalf("missing selected part findings = %d, want 1", report.Summary.MissingNormalizedParts)
	}
}

func stringsRepeat(value string, count int) string {
	result := ""
	for range count {
		result += value
	}
	return result
}

func TestReadFeatureManifestSupportsPinnedV11AndLegacyV1(t *testing.T) {
	fixturePath := "../../schemas/feature-manifest.fixture.json"
	if _, err := readFeatureManifest(fixturePath); err != nil {
		t.Fatalf("pinned 1.1 fixture: %v", err)
	}
	body, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(body, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy["schema_version"] = "1.0.0"
	legacy["manifest_version"] = "1.0.0"
	delete(legacy, "calendar_pin")
	artifact := legacy["artifact"].(map[string]any)
	artifact["artifact_version"] = "1.0.0"
	legacyPath := filepath.Join(t.TempDir(), "manifest.json")
	legacyBytes, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, legacyBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readFeatureManifest(legacyPath); err != nil {
		t.Fatalf("legacy 1.0 fixture: %v", err)
	}
}

func TestReadFeatureManifestSupportsMarketMomentum(t *testing.T) {
	body, err := os.ReadFile("../../schemas/feature-manifest.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest featureManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.FeatureSet = "market-momentum"
	manifest.FeatureNames = []string{
		"return_1m", "return_3m", "return_6m", "return_12m",
		"realized_volatility_1m", "max_drawdown_1m",
	}
	manifest.InputFingerprint, err = featureInputFingerprint(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "manifest.json")
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readFeatureManifest(path); err != nil {
		t.Fatalf("market-momentum fixture: %v", err)
	}
}

func TestReconcileLeavesBatchManifestsToBatchValidator(t *testing.T) {
	dataRoot := t.TempDir()
	batchDir := filepath.Join(dataRoot, "features", "batches", "market-basic", "1.0.0", "batch-test")
	if err := os.MkdirAll(batchDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(batchDir, "manifest.json"), []byte(`{"batch":"envelope"}`), 0o640); err != nil {
		t.Fatal(err)
	}

	report, err := Reconcile(context.Background(), Roots{DataRoot: dataRoot}, nil, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.FeatureArtifactErrors != 0 {
		t.Fatalf("batch manifest was decoded as child artifact: %+v", report.FeatureArtifactErrors)
	}
}
