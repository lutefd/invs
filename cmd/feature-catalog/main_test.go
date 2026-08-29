package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func validBatchManifestForTest() batchManifest {
	firstDecision := "2026-08-01T21:00:00Z"
	secondDecision := "2026-08-02T21:00:00Z"
	partHash := strings.Repeat("b", 64)
	manifestHash := strings.Repeat("c", 64)
	document := batchManifest{
		SchemaVersion: batchSchemaVersion, ManifestVersion: batchManifestVersion,
		FeatureSet: "market-basic", FeatureSetVersion: "1.0.0", RegistrySHA256: strings.Repeat("1", 64),
		Batch: batchMetadata{ArtifactVersion: batchArtifactVersion, GeneratorVersion: "python-market-basic-1.1.0", GitCommit: "unknown", CreatedAt: firstDecision},
		CalendarPin: batchCalendarPin{
			DataSourceID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", MIC: "XNAS", CalendarVersion: "nasdaq-2026",
			SessionFingerprint: strings.Repeat("2", 64), CalendarAvailableAt: "2026-07-30T21:00:00Z", DecisionClockPolicy: featureBatchClockPolicy,
		},
		DecisionSchedule: []string{firstDecision, secondDecision},
		Universe: batchUniverse{Kind: "explicit_security_list", SecurityIDs: []string{
			"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222",
		}},
		ComputationDelaySeconds: 0,
		InputFitness:            []batchInputFitness{{Dataset: "prices", HistoricalFitness: "installation_replay_only", AvailabilityPolicy: "conservative_receipt_time"}},
		SelectedInputManifests:  []batchFileRef{{Path: "normalized/prices/source=yahoo/security_id=11111111-1111-4111-8111-111111111111/manifest.json", SHA256: strings.Repeat("3", 64)}},
		SelectedInputParts:      []batchFileRef{{Path: "part-" + strings.Repeat("4", 64) + ".parquet", SHA256: strings.Repeat("4", 64)}},
		RowCount:                1,
		Parts: []batchPart{{
			SecurityID: "11111111-1111-4111-8111-111111111111", DecisionAt: firstDecision,
			ArtifactID: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", ManifestPath: "batches/market-basic/1.0.0/child/manifest.json", ManifestSHA256: manifestHash,
			PartPath: "market-basic/1.0.0/child/part-" + partHash + ".parquet", PartSHA256: partHash, RowCount: 1,
		}},
		Rejected: []batchRejectedPartition{{
			SecurityID: "22222222-2222-4222-8222-222222222222", DecisionAt: secondDecision,
			Reason: "input_rejected", Detail: "no eligible price rows",
		}},
		RunSummary: batchRunSummary{RequestedPartitions: 2, AcceptedPartitions: 1, RejectedPartitions: 1, RowCount: 1, Status: featureBatchStatus},
	}
	document.Universe.Fingerprint = universeFingerprint(document.Universe.SecurityIDs)
	document.InputFingerprint = batchInputFingerprint(document)
	document.Batch.BatchID = uuid.NewSHA1(featureBatchNamespace, []byte(document.InputFingerprint)).String()
	return document
}

func TestRegistrationFromBatchManifestValidatesContract(t *testing.T) {
	document := validBatchManifestForTest()
	if got := document.Universe.Fingerprint; got != "ac24c4eb9105ff64d207a3a8b5f5cbca9f5c688361e225a4d4fcc27ea5ab9163" {
		t.Fatalf("universe fingerprint = %s", got)
	}
	if got := document.InputFingerprint; got != "1bac4b02e112979b683c99471945743d8235729fffc850f0bd2b3c496dec7e97" {
		t.Fatalf("input fingerprint = %s", got)
	}
	registration, err := registrationFromBatchManifest(document, "batches/market-basic/1.0.0/batch-"+document.Batch.BatchID+"/manifest.json", strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if registration.ArtifactID != document.Batch.BatchID || registration.DecisionStart.Format(time.RFC3339Nano) != document.DecisionSchedule[0] || registration.DecisionEnd.Format(time.RFC3339Nano) != document.DecisionSchedule[1] {
		t.Fatalf("registration identity/range = %+v", registration)
	}
	if len(registration.InputFitness) != 1 || registration.InputFitness[0].HistoricalFitness != "installation_replay_only" {
		t.Fatalf("registration input fitness = %+v", registration.InputFitness)
	}
	if len(registration.Universe) != len(document.Universe.SecurityIDs) || registration.Universe[0].SecurityID != document.Universe.SecurityIDs[0] {
		t.Fatalf("registration universe = %+v", registration.Universe)
	}
}

func TestValidateBatchManifestRejectsFingerprintMutation(t *testing.T) {
	document := validBatchManifestForTest()
	document.InputFingerprint = strings.Repeat("f", 64)
	if _, _, err := validateBatchManifest(document); err == nil || !strings.Contains(err.Error(), "input fingerprint") {
		t.Fatalf("validateBatchManifest error = %v, want input fingerprint failure", err)
	}
}

func TestValidateBatchManifestRejectsOutOfScopeRejection(t *testing.T) {
	document := validBatchManifestForTest()
	document.Rejected[0].SecurityID = "99999999-9999-4999-8999-999999999999"
	if _, _, err := validateBatchManifest(document); err == nil || !strings.Contains(err.Error(), "outside the universe") {
		t.Fatalf("validateBatchManifest error = %v, want universe boundary failure", err)
	}
}

func TestDecodeStrictJSONRejectsDuplicateKeys(t *testing.T) {
	var target map[string]any
	if err := decodeStrictJSON([]byte(`{"value":1,"value":2}`), &target); err == nil || !strings.Contains(err.Error(), "duplicate JSON key") {
		t.Fatalf("decodeStrictJSON error = %v, want duplicate key failure", err)
	}
}

func TestLoadBatchRegistrationVerifiesListedOutputHashes(t *testing.T) {
	root := t.TempDir()
	document := validBatchManifestForTest()
	childManifestPath := filepath.Join(root, filepath.FromSlash(document.Parts[0].ManifestPath))
	childPartPath := filepath.Join(root, filepath.FromSlash(document.Parts[0].PartPath))
	if err := os.MkdirAll(filepath.Dir(childManifestPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(childPartPath), 0o750); err != nil {
		t.Fatal(err)
	}
	childManifest := []byte("child manifest")
	childPart := []byte("child parquet placeholder")
	document.Parts[0].ManifestSHA256 = digestForTest(childManifest)
	document.Parts[0].PartSHA256 = digestForTest(childPart)
	if err := os.WriteFile(childManifestPath, childManifest, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(childPartPath, childPart, 0o640); err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "batches", "market-basic", "1.0.0", "batch-"+document.Batch.BatchID, "manifest.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	registration, relative, outputHash, err := loadBatchRegistration(manifestPath, root)
	if err != nil {
		t.Fatal(err)
	}
	if registration.OutputManifestSHA256 != outputHash || relative != filepath.ToSlash(filepath.Join("batches", "market-basic", "1.0.0", "batch-"+document.Batch.BatchID, "manifest.json")) {
		t.Fatalf("loaded output identity = %s, %s, %s", registration.OutputManifestSHA256, outputHash, relative)
	}

	if err := os.WriteFile(childPartPath, append(childPart, '!'), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := loadBatchRegistration(manifestPath, root); err == nil || !strings.Contains(err.Error(), "output part hash mismatch") {
		t.Fatalf("tampered load error = %v, want output part hash mismatch", err)
	}
}

func digestForTest(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
