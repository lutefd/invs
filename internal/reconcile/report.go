// Package reconcile implements read-only checks across the durable metadata
// and immutable filesystem layers. It reports inconsistencies for an operator;
// it never cancels runs, deletes evidence, or rewrites manifests.
package reconcile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/luisdourado/invs/internal/normalize"
	"github.com/luisdourado/invs/internal/storage"
)

const ReportVersion = 1

// Roots identifies the immutable and derived roots inspected by a report.
// DataRoot is only used to make paths portable and to resolve feature lineage.
type Roots struct {
	DataRoot       string `json:"data_root"`
	RawRoot        string `json:"raw_root"`
	NormalizedRoot string `json:"normalized_root"`
	FeatureRoot    string `json:"feature_root"`
}

// RunRecord is the small metadata shape needed by filesystem reconciliation.
// cmd/reconcile maps the PostgreSQL repository projection into this package so
// the scanner remains independently testable.
type RunRecord struct {
	ID, Source, RunKey, Status, RawPayloadManifestHash string
}

type Report struct {
	Version     int       `json:"version"`
	GeneratedAt time.Time `json:"generated_at"`
	Roots       Roots     `json:"roots"`
	Summary     Summary   `json:"summary"`

	QueuedOrRunning                       []RunFinding     `json:"queued_or_running"`
	RawManifestsWithoutTerminalRun        []RawFinding     `json:"raw_manifests_without_terminal_run"`
	RawManifestErrors                     []PathFinding    `json:"raw_manifest_errors"`
	RawManifestHashMismatches             []HashFinding    `json:"raw_manifest_hash_mismatches"`
	InvalidNormalizedManifests            []PathFinding    `json:"invalid_normalized_manifests"`
	UnlistedNormalizedParts               []PathFinding    `json:"unlisted_normalized_parts"`
	MissingNormalizedParts                []HashFinding    `json:"missing_normalized_parts"`
	NormalizedPartHashMismatches          []HashFinding    `json:"normalized_part_hash_mismatches"`
	FeatureArtifactErrors                 []PathFinding    `json:"feature_artifact_errors"`
	FeatureArtifactsWithUnavailableInputs []FeatureFinding `json:"feature_artifacts_with_unavailable_inputs"`
}

type Summary struct {
	TotalIssues                           int `json:"total_issues"`
	QueuedOrRunning                       int `json:"queued_or_running"`
	RawManifestsWithoutTerminalRun        int `json:"raw_manifests_without_terminal_run"`
	RawManifestErrors                     int `json:"raw_manifest_errors"`
	RawManifestHashMismatches             int `json:"raw_manifest_hash_mismatches"`
	InvalidNormalizedManifests            int `json:"invalid_normalized_manifests"`
	UnlistedNormalizedParts               int `json:"unlisted_normalized_parts"`
	MissingNormalizedParts                int `json:"missing_normalized_parts"`
	NormalizedPartHashMismatches          int `json:"normalized_part_hash_mismatches"`
	FeatureArtifactErrors                 int `json:"feature_artifact_errors"`
	FeatureArtifactsWithUnavailableInputs int `json:"feature_artifacts_with_unavailable_inputs"`
}

type RunFinding struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	RunKey string `json:"run_key"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type RawFinding struct {
	Path   string `json:"path"`
	Source string `json:"source"`
	RunID  string `json:"run_id"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type PathFinding struct {
	Path   string `json:"path"`
	Detail string `json:"detail"`
}

type HashFinding struct {
	Path     string `json:"path"`
	Expected string `json:"expected_sha256"`
	Actual   string `json:"actual_sha256,omitempty"`
	Detail   string `json:"detail"`
}

type FeatureFinding struct {
	Path   string         `json:"path"`
	Detail string         `json:"detail"`
	Inputs []InputFinding `json:"inputs"`
}

type InputFinding struct {
	Kind     string `json:"kind"`
	Path     string `json:"path"`
	Expected string `json:"expected_sha256"`
	Actual   string `json:"actual_sha256,omitempty"`
	Detail   string `json:"detail"`
}

// Reconcile performs all checks without creating, deleting, or changing any
// file or database row. Missing roots are treated as empty roots, which keeps
// the report useful during a clean restore before the first collection.
func Reconcile(ctx context.Context, roots Roots, runs []RunRecord, now time.Time) (Report, error) {
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	resolved, err := resolveRoots(roots)
	if err != nil {
		return Report{}, err
	}
	report := Report{
		Version:                               ReportVersion,
		GeneratedAt:                           now.UTC(),
		Roots:                                 resolved,
		QueuedOrRunning:                       make([]RunFinding, 0),
		RawManifestsWithoutTerminalRun:        make([]RawFinding, 0),
		RawManifestErrors:                     make([]PathFinding, 0),
		RawManifestHashMismatches:             make([]HashFinding, 0),
		InvalidNormalizedManifests:            make([]PathFinding, 0),
		UnlistedNormalizedParts:               make([]PathFinding, 0),
		MissingNormalizedParts:                make([]HashFinding, 0),
		NormalizedPartHashMismatches:          make([]HashFinding, 0),
		FeatureArtifactErrors:                 make([]PathFinding, 0),
		FeatureArtifactsWithUnavailableInputs: make([]FeatureFinding, 0),
	}

	for _, run := range runs {
		if run.Status == "queued" || run.Status == "running" {
			report.QueuedOrRunning = append(report.QueuedOrRunning, RunFinding{
				ID: run.ID, Source: run.Source, RunKey: run.RunKey, Status: run.Status,
				Detail: "active run requires operator review; no automatic cancellation is performed",
			})
		}
	}
	if err := scanRaw(ctx, &report, resolved, runs); err != nil {
		return Report{}, err
	}
	if err := scanNormalized(&report, resolved); err != nil {
		return Report{}, err
	}
	if err := scanFeatures(&report, resolved); err != nil {
		return Report{}, err
	}
	sortReport(&report)
	report.Summary = report.summary()
	return report, nil
}

func resolveRoots(roots Roots) (Roots, error) {
	dataRoot := roots.DataRoot
	if strings.TrimSpace(dataRoot) == "" {
		dataRoot = "data"
	}
	abs, err := filepath.Abs(dataRoot)
	if err != nil {
		return Roots{}, fmt.Errorf("resolve data root: %w", err)
	}
	resolveChild := func(value, fallback string) (string, error) {
		if strings.TrimSpace(value) == "" {
			value = filepath.Join(abs, fallback)
		}
		return filepath.Abs(value)
	}
	raw, err := resolveChild(roots.RawRoot, "raw")
	if err != nil {
		return Roots{}, fmt.Errorf("resolve raw root: %w", err)
	}
	normalized, err := resolveChild(roots.NormalizedRoot, "normalized")
	if err != nil {
		return Roots{}, fmt.Errorf("resolve normalized root: %w", err)
	}
	features, err := resolveChild(roots.FeatureRoot, "features")
	if err != nil {
		return Roots{}, fmt.Errorf("resolve feature root: %w", err)
	}
	return Roots{DataRoot: abs, RawRoot: raw, NormalizedRoot: normalized, FeatureRoot: features}, nil
}

func scanRaw(ctx context.Context, report *Report, roots Roots, runs []RunRecord) error {
	manifestRoot := filepath.Join(roots.RawRoot, "runs")
	if _, err := os.Stat(manifestRoot); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat raw manifest root: %w", err)
	}
	store, err := storage.OpenFileRawStore(roots.RawRoot)
	if err != nil {
		return fmt.Errorf("open raw store for reconciliation: %w", err)
	}
	runByKey := make(map[string]RunRecord, len(runs))
	for _, run := range runs {
		runByKey[run.Source+"\x00"+run.ID] = run
	}
	return filepath.WalkDir(manifestRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() != storageManifestFilename {
			return nil
		}
		rel, err := filepath.Rel(roots.RawRoot, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		display := displayPath(path, roots.DataRoot)
		manifest, hash, loadErr := storage.LoadAndVerifyRawManifest(ctx, store, key)
		if loadErr != nil {
			report.RawManifestErrors = append(report.RawManifestErrors, PathFinding{Path: display, Detail: loadErr.Error()})
			return nil
		}
		run, ok := runByKey[manifest.Source+"\x00"+manifest.IngestionRunID]
		if !ok || !isTerminal(run.Status) {
			status := "missing metadata run"
			if ok {
				status = run.Status
			}
			report.RawManifestsWithoutTerminalRun = append(report.RawManifestsWithoutTerminalRun, RawFinding{
				Path: display, Source: manifest.Source, RunID: manifest.IngestionRunID,
				Status: status, Detail: "raw evidence exists without a terminal PostgreSQL ingestion run",
			})
		}
		if ok && run.RawPayloadManifestHash != "" && run.RawPayloadManifestHash != hash {
			report.RawManifestHashMismatches = append(report.RawManifestHashMismatches, HashFinding{
				Path: display, Expected: run.RawPayloadManifestHash, Actual: hash,
				Detail: "PostgreSQL raw_payload_manifest_hash differs from the verified manifest bytes",
			})
		}
		return nil
	})
}

const storageManifestFilename = "manifest.json"

func scanNormalized(report *Report, roots Roots) error {
	if _, err := os.Stat(roots.NormalizedRoot); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat normalized root: %w", err)
	}
	manifestPaths := make([]string, 0)
	parquetPaths := make([]string, 0)
	if err := filepath.WalkDir(roots.NormalizedRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		switch entry.Name() {
		case normalize.ManifestFilename:
			manifestPaths = append(manifestPaths, path)
		default:
			if filepath.Ext(entry.Name()) == normalize.PartFilenameSuffix {
				parquetPaths = append(parquetPaths, path)
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("scan normalized root: %w", err)
	}
	listed := make(map[string]struct{})
	for _, manifestPath := range manifestPaths {
		manifest, err := normalize.ReadManifest(manifestPath)
		if err != nil {
			report.InvalidNormalizedManifests = append(report.InvalidNormalizedManifests, PathFinding{Path: displayPath(manifestPath, roots.DataRoot), Detail: err.Error()})
			continue
		}
		for _, part := range manifest.Parts {
			partPath := filepath.Join(filepath.Dir(manifestPath), part.Path)
			listed[partPath] = struct{}{}
			actual, err := hashFile(partPath)
			if errors.Is(err, os.ErrNotExist) {
				report.MissingNormalizedParts = append(report.MissingNormalizedParts, HashFinding{
					Path: displayPath(partPath, roots.DataRoot), Expected: part.SHA256,
					Detail: "manifest-listed normalized part is missing",
				})
				continue
			}
			if err != nil {
				report.MissingNormalizedParts = append(report.MissingNormalizedParts, HashFinding{
					Path: displayPath(partPath, roots.DataRoot), Expected: part.SHA256,
					Detail: "manifest-listed normalized part cannot be read: " + err.Error(),
				})
				continue
			}
			if actual != part.SHA256 {
				report.NormalizedPartHashMismatches = append(report.NormalizedPartHashMismatches, HashFinding{
					Path: displayPath(partPath, roots.DataRoot), Expected: part.SHA256, Actual: actual,
					Detail: "normalized part hash differs from its manifest",
				})
			}
		}
	}
	for _, path := range parquetPaths {
		if _, ok := listed[path]; !ok {
			report.UnlistedNormalizedParts = append(report.UnlistedNormalizedParts, PathFinding{
				Path:   displayPath(path, roots.DataRoot),
				Detail: "Parquet file is not listed by any valid normalized manifest",
			})
		}
	}
	return nil
}

type featureInput struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type featurePart struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	RowCount int    `json:"row_count"`
}

type featureArtifactMetadata struct {
	ArtifactID       string `json:"artifact_id"`
	ArtifactVersion  string `json:"artifact_version"`
	GeneratorVersion string `json:"generator_version"`
	GitCommit        string `json:"git_commit"`
	CreatedAt        string `json:"created_at"`
}

type featureManifest struct {
	SchemaVersion           string                  `json:"schema_version"`
	ManifestVersion         string                  `json:"manifest_version"`
	FeatureSet              string                  `json:"feature_set"`
	FeatureSetVersion       string                  `json:"feature_set_version"`
	FeatureNames            []string                `json:"feature_names"`
	Artifact                featureArtifactMetadata `json:"artifact"`
	DecisionAt              string                  `json:"decision_at"`
	InputAvailableAt        string                  `json:"input_available_at"`
	ComputationDelaySeconds int                     `json:"computation_delay_seconds"`
	AvailableAt             string                  `json:"available_at"`
	InputFingerprint        string                  `json:"input_fingerprint"`
	SelectedInputManifests  []featureInput          `json:"selected_input_manifests"`
	SelectedInputParts      []featureInput          `json:"selected_input_parts"`
	RowCount                int                     `json:"row_count"`
	Parts                   []featurePart           `json:"parts"`
}

func scanFeatures(report *Report, roots Roots) error {
	if _, err := os.Stat(roots.FeatureRoot); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat feature root: %w", err)
	}
	return filepath.WalkDir(roots.FeatureRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() != "manifest.json" {
			return nil
		}
		manifest, err := readFeatureManifest(path)
		if err != nil {
			report.FeatureArtifactErrors = append(report.FeatureArtifactErrors, PathFinding{Path: displayPath(path, roots.DataRoot), Detail: err.Error()})
			return nil
		}
		for _, part := range manifest.Parts {
			partPath, pathErr := safeRelativeChild(filepath.Dir(path), part.Path)
			if pathErr != nil {
				report.FeatureArtifactErrors = append(report.FeatureArtifactErrors, PathFinding{Path: displayPath(path, roots.DataRoot), Detail: pathErr.Error()})
				continue
			}
			actual, hashErr := hashFile(partPath)
			if hashErr != nil {
				report.FeatureArtifactErrors = append(report.FeatureArtifactErrors, PathFinding{Path: displayPath(partPath, roots.DataRoot), Detail: "feature manifest-listed part is missing or unreadable: " + hashErr.Error()})
				continue
			}
			if actual != part.SHA256 {
				report.FeatureArtifactErrors = append(report.FeatureArtifactErrors, PathFinding{Path: displayPath(partPath, roots.DataRoot), Detail: fmt.Sprintf("feature part hash differs from manifest: expected %s got %s", part.SHA256, actual)})
			}
		}
		listed := map[string]struct{}{"manifest.json": {}}
		for _, part := range manifest.Parts {
			listed[part.Path] = struct{}{}
		}
		entries, readDirErr := os.ReadDir(filepath.Dir(path))
		if readDirErr != nil {
			report.FeatureArtifactErrors = append(report.FeatureArtifactErrors, PathFinding{Path: displayPath(path, roots.DataRoot), Detail: "cannot inspect feature artifact directory: " + readDirErr.Error()})
		} else {
			for _, entry := range entries {
				if _, ok := listed[entry.Name()]; !ok {
					report.FeatureArtifactErrors = append(report.FeatureArtifactErrors, PathFinding{Path: displayPath(filepath.Join(filepath.Dir(path), entry.Name()), roots.DataRoot), Detail: "feature artifact contains an unlisted file or directory"})
				}
			}
		}
		inputs := unavailableFeatureInputs(manifest, roots)
		if len(inputs) > 0 {
			report.FeatureArtifactsWithUnavailableInputs = append(report.FeatureArtifactsWithUnavailableInputs, FeatureFinding{
				Path:   displayPath(path, roots.DataRoot),
				Detail: "feature artifact lineage cannot be resolved to available normalized manifests and parts",
				Inputs: inputs,
			})
		}
		return nil
	})
}

func readFeatureManifest(path string) (featureManifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return featureManifest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	var manifest featureManifest
	if err := decoder.Decode(&manifest); err != nil {
		return featureManifest{}, fmt.Errorf("decode feature manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return featureManifest{}, errors.New("feature manifest contains multiple JSON values")
		}
		return featureManifest{}, fmt.Errorf("decode feature manifest trailing data: %w", err)
	}
	if manifest.SchemaVersion != "1.0.0" || manifest.ManifestVersion != "1.0.0" || manifest.FeatureSet != "market-basic" || manifest.FeatureSetVersion != "1.0.0" {
		return featureManifest{}, errors.New("unsupported feature manifest version or feature set")
	}
	if len(manifest.FeatureNames) != 4 || manifest.FeatureNames[0] != "close" || manifest.FeatureNames[1] != "return_1d" || manifest.FeatureNames[2] != "range_1d" || manifest.FeatureNames[3] != "volume" {
		return featureManifest{}, errors.New("feature manifest feature_names do not match market-basic v1")
	}
	if _, err := uuid.Parse(manifest.Artifact.ArtifactID); err != nil || manifest.Artifact.ArtifactVersion != "1.0.0" || manifest.Artifact.GeneratorVersion == "" || !validGitCommit(manifest.Artifact.GitCommit) || !validUTCTimestamp(manifest.Artifact.CreatedAt) {
		return featureManifest{}, errors.New("feature manifest artifact metadata is incomplete")
	}
	if !validUTCTimestamp(manifest.DecisionAt) || !validUTCTimestamp(manifest.InputAvailableAt) || !validUTCTimestamp(manifest.AvailableAt) || manifest.ComputationDelaySeconds < 0 || !validSHA256(manifest.InputFingerprint) {
		return featureManifest{}, errors.New("feature manifest timing or input fingerprint is invalid")
	}
	if len(manifest.SelectedInputManifests) == 0 || len(manifest.SelectedInputParts) == 0 || len(manifest.Parts) == 0 || manifest.RowCount < 0 {
		return featureManifest{}, errors.New("feature manifest must contain selected inputs and output parts")
	}
	seenManifests := make(map[string]struct{}, len(manifest.SelectedInputManifests))
	for _, input := range manifest.SelectedInputManifests {
		if _, err := safeDataPath(".", input.Path); err != nil || !validSHA256(input.SHA256) {
			return featureManifest{}, fmt.Errorf("invalid selected input manifest %q", input.Path)
		}
		key := input.Path + "\x00" + input.SHA256
		if _, ok := seenManifests[key]; ok {
			return featureManifest{}, errors.New("duplicate selected input manifest")
		}
		seenManifests[key] = struct{}{}
	}
	seenParts := make(map[string]struct{}, len(manifest.SelectedInputParts))
	for _, input := range manifest.SelectedInputParts {
		if !validContentPart(input.Path, input.SHA256) {
			return featureManifest{}, fmt.Errorf("invalid selected input part %q", input.Path)
		}
		key := input.Path + "\x00" + input.SHA256
		if _, ok := seenParts[key]; ok {
			return featureManifest{}, errors.New("duplicate selected input part")
		}
		seenParts[key] = struct{}{}
	}
	seenOutputParts := make(map[string]struct{}, len(manifest.Parts))
	rowCount := 0
	for _, part := range manifest.Parts {
		if part.RowCount < 0 || !validContentPart(part.Path, part.SHA256) {
			return featureManifest{}, fmt.Errorf("invalid feature output part %q", part.Path)
		}
		if _, ok := seenOutputParts[part.Path]; ok {
			return featureManifest{}, errors.New("duplicate feature output part")
		}
		seenOutputParts[part.Path] = struct{}{}
		rowCount += part.RowCount
	}
	if rowCount != manifest.RowCount {
		return featureManifest{}, errors.New("feature manifest row_count does not equal output part row counts")
	}
	return manifest, nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func validContentPart(path, hash string) bool {
	return validSHA256(hash) && path == "part-"+hash+".parquet"
}

func validGitCommit(value string) bool {
	if value == "unknown" {
		return true
	}
	if len(value) != 40 {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func validUTCTimestamp(value string) bool {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && strings.HasSuffix(value, "Z") && parsed.UTC().Format(time.RFC3339Nano) == value
}

func unavailableFeatureInputs(manifest featureManifest, roots Roots) []InputFinding {
	findings := make([]InputFinding, 0)
	selectedManifests := make([]struct {
		path     string
		manifest normalize.Manifest
		dir      string
	}, 0, len(manifest.SelectedInputManifests))
	for _, input := range manifest.SelectedInputManifests {
		path, err := safeDataPath(roots.DataRoot, input.Path)
		if err != nil {
			findings = append(findings, InputFinding{Kind: "manifest", Path: input.Path, Expected: input.SHA256, Detail: err.Error()})
			continue
		}
		actual, hashErr := hashFile(path)
		if errors.Is(hashErr, os.ErrNotExist) {
			findings = append(findings, InputFinding{Kind: "manifest", Path: input.Path, Expected: input.SHA256, Detail: "selected normalized manifest is missing"})
			continue
		}
		if hashErr != nil {
			findings = append(findings, InputFinding{Kind: "manifest", Path: input.Path, Expected: input.SHA256, Detail: "selected normalized manifest is unreadable: " + hashErr.Error()})
			continue
		}
		if actual != input.SHA256 {
			findings = append(findings, InputFinding{Kind: "manifest", Path: input.Path, Expected: input.SHA256, Actual: actual, Detail: "selected normalized manifest hash differs from feature lineage"})
			continue
		}
		normalizedManifest, readErr := normalize.ReadManifest(path)
		if readErr != nil {
			findings = append(findings, InputFinding{Kind: "manifest", Path: input.Path, Expected: input.SHA256, Detail: "selected normalized manifest is structurally invalid: " + readErr.Error()})
			continue
		}
		selectedManifests = append(selectedManifests, struct {
			path     string
			manifest normalize.Manifest
			dir      string
		}{path: input.Path, manifest: normalizedManifest, dir: filepath.Dir(path)})
	}

	for _, input := range manifest.SelectedInputParts {
		matched := false
		for _, selected := range selectedManifests {
			for _, part := range selected.manifest.Parts {
				if part.Path != input.Path || part.SHA256 != input.SHA256 {
					continue
				}
				matched = true
				partPath := filepath.Join(selected.dir, part.Path)
				actual, hashErr := hashFile(partPath)
				if errors.Is(hashErr, os.ErrNotExist) {
					findings = append(findings, InputFinding{Kind: "part", Path: input.Path, Expected: input.SHA256, Detail: "selected normalized part is missing"})
				} else if hashErr != nil {
					findings = append(findings, InputFinding{Kind: "part", Path: input.Path, Expected: input.SHA256, Detail: "selected normalized part is unreadable: " + hashErr.Error()})
				} else if actual != input.SHA256 {
					findings = append(findings, InputFinding{Kind: "part", Path: input.Path, Expected: input.SHA256, Actual: actual, Detail: "selected normalized part hash differs from lineage"})
				}
			}
		}
		if !matched {
			findings = append(findings, InputFinding{Kind: "part", Path: input.Path, Expected: input.SHA256, Detail: "selected part is not listed by any selected normalized manifest"})
		}
	}
	return findings
}

func safeRelativeChild(dir, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) || strings.ContainsAny(name, "/\\\x00") || name == "." || name == ".." {
		return "", fmt.Errorf("unsafe relative child path %q", name)
	}
	joined := filepath.Join(dir, name)
	if filepath.Dir(joined) != filepath.Clean(dir) {
		return "", fmt.Errorf("relative child path %q escapes its directory", name)
	}
	return joined, nil
}

func safeDataPath(root, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) || strings.ContainsAny(relative, "\\\x00") {
		return "", fmt.Errorf("unsafe data-relative path %q", relative)
	}
	for _, component := range strings.Split(relative, "/") {
		if component == "" || component == "." || component == ".." {
			return "", fmt.Errorf("data-relative path %q contains an unsafe component", relative)
		}
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("data-relative path %q escapes data root", relative)
	}
	return filepath.Join(root, clean), nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func displayPath(path, dataRoot string) string {
	rel, err := filepath.Rel(dataRoot, path)
	if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

func isTerminal(status string) bool {
	switch status {
	case "succeeded", "partial", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func (r Report) summary() Summary {
	summary := Summary{
		QueuedOrRunning:                       len(r.QueuedOrRunning),
		RawManifestsWithoutTerminalRun:        len(r.RawManifestsWithoutTerminalRun),
		RawManifestErrors:                     len(r.RawManifestErrors),
		RawManifestHashMismatches:             len(r.RawManifestHashMismatches),
		InvalidNormalizedManifests:            len(r.InvalidNormalizedManifests),
		UnlistedNormalizedParts:               len(r.UnlistedNormalizedParts),
		MissingNormalizedParts:                len(r.MissingNormalizedParts),
		NormalizedPartHashMismatches:          len(r.NormalizedPartHashMismatches),
		FeatureArtifactErrors:                 len(r.FeatureArtifactErrors),
		FeatureArtifactsWithUnavailableInputs: len(r.FeatureArtifactsWithUnavailableInputs),
	}
	summary.TotalIssues = summary.QueuedOrRunning + summary.RawManifestsWithoutTerminalRun +
		summary.RawManifestErrors + summary.RawManifestHashMismatches +
		summary.InvalidNormalizedManifests + summary.UnlistedNormalizedParts +
		summary.MissingNormalizedParts + summary.NormalizedPartHashMismatches +
		summary.FeatureArtifactErrors + summary.FeatureArtifactsWithUnavailableInputs
	return summary
}

func sortReport(report *Report) {
	sort.Slice(report.QueuedOrRunning, func(i, j int) bool { return report.QueuedOrRunning[i].ID < report.QueuedOrRunning[j].ID })
	sort.Slice(report.RawManifestsWithoutTerminalRun, func(i, j int) bool {
		return report.RawManifestsWithoutTerminalRun[i].Path < report.RawManifestsWithoutTerminalRun[j].Path
	})
	sort.Slice(report.RawManifestErrors, func(i, j int) bool { return report.RawManifestErrors[i].Path < report.RawManifestErrors[j].Path })
	sort.Slice(report.RawManifestHashMismatches, func(i, j int) bool {
		return report.RawManifestHashMismatches[i].Path < report.RawManifestHashMismatches[j].Path
	})
	sort.Slice(report.InvalidNormalizedManifests, func(i, j int) bool {
		return report.InvalidNormalizedManifests[i].Path < report.InvalidNormalizedManifests[j].Path
	})
	sort.Slice(report.UnlistedNormalizedParts, func(i, j int) bool {
		return report.UnlistedNormalizedParts[i].Path < report.UnlistedNormalizedParts[j].Path
	})
	sort.Slice(report.MissingNormalizedParts, func(i, j int) bool {
		return report.MissingNormalizedParts[i].Path < report.MissingNormalizedParts[j].Path
	})
	sort.Slice(report.NormalizedPartHashMismatches, func(i, j int) bool {
		return report.NormalizedPartHashMismatches[i].Path < report.NormalizedPartHashMismatches[j].Path
	})
	sort.Slice(report.FeatureArtifactErrors, func(i, j int) bool {
		return report.FeatureArtifactErrors[i].Path < report.FeatureArtifactErrors[j].Path
	})
	sort.Slice(report.FeatureArtifactsWithUnavailableInputs, func(i, j int) bool {
		return report.FeatureArtifactsWithUnavailableInputs[i].Path < report.FeatureArtifactsWithUnavailableInputs[j].Path
	})
}
