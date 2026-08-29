package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/luisdourado/invs/internal/metadata"
)

const (
	batchSchemaVersion      = "1.0.0"
	batchManifestVersion    = "1.0.0"
	batchArtifactVersion    = "1.0.0"
	featureBatchStatus      = "completed"
	featureBatchClockPolicy = "after_close_next_session"
)

var (
	featureCatalogIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)
	featureCatalogVersionPattern    = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	featureCatalogHashPattern       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	featureCatalogGitPattern        = regexp.MustCompile(`^(unknown|[0-9a-f]{40})$`)
	featureCatalogMICPattern        = regexp.MustCompile(`^[A-Z0-9]{4}$`)
	featureCatalogPartPattern       = regexp.MustCompile(`^part-[0-9a-f]{64}\.parquet$`)
)

var featureBatchNamespace = uuid.MustParse("8a88ba32-1a82-5f10-9e27-1fe0d06bf67e")

type batchManifest struct {
	SchemaVersion           string                   `json:"schema_version"`
	ManifestVersion         string                   `json:"manifest_version"`
	FeatureSet              string                   `json:"feature_set"`
	FeatureSetVersion       string                   `json:"feature_set_version"`
	RegistrySHA256          string                   `json:"registry_sha256"`
	Batch                   batchMetadata            `json:"batch"`
	CalendarPin             batchCalendarPin         `json:"calendar_pin"`
	DecisionSchedule        []string                 `json:"decision_schedule"`
	Universe                batchUniverse            `json:"universe"`
	ComputationDelaySeconds int64                    `json:"computation_delay_seconds"`
	InputFitness            []batchInputFitness      `json:"input_fitness"`
	InputFingerprint        string                   `json:"input_fingerprint"`
	SelectedInputManifests  []batchFileRef           `json:"selected_input_manifests"`
	SelectedInputParts      []batchFileRef           `json:"selected_input_parts"`
	RowCount                int64                    `json:"row_count"`
	Parts                   []batchPart              `json:"parts"`
	Rejected                []batchRejectedPartition `json:"rejected"`
	RunSummary              batchRunSummary          `json:"run_summary"`
}

type batchMetadata struct {
	BatchID          string `json:"batch_id"`
	ArtifactVersion  string `json:"artifact_version"`
	GeneratorVersion string `json:"generator_version"`
	GitCommit        string `json:"git_commit"`
	CreatedAt        string `json:"created_at"`
}

type batchCalendarPin struct {
	DataSourceID        string `json:"data_source_id"`
	MIC                 string `json:"mic"`
	CalendarVersion     string `json:"calendar_version"`
	SessionFingerprint  string `json:"session_fingerprint"`
	CalendarAvailableAt string `json:"calendar_available_at"`
	DecisionClockPolicy string `json:"decision_clock_policy"`
}

type batchUniverse struct {
	Kind        string   `json:"kind"`
	SecurityIDs []string `json:"security_ids"`
	Fingerprint string   `json:"fingerprint"`
}

type batchInputFitness struct {
	Dataset            string `json:"dataset"`
	HistoricalFitness  string `json:"historical_fitness"`
	AvailabilityPolicy string `json:"availability_policy"`
}

type batchFileRef struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type batchPart struct {
	SecurityID     string `json:"security_id"`
	DecisionAt     string `json:"decision_at"`
	ArtifactID     string `json:"artifact_id"`
	ManifestPath   string `json:"manifest_path"`
	ManifestSHA256 string `json:"manifest_sha256"`
	PartPath       string `json:"part_path"`
	PartSHA256     string `json:"part_sha256"`
	RowCount       int64  `json:"row_count"`
}

type batchRejectedPartition struct {
	SecurityID string `json:"security_id"`
	DecisionAt string `json:"decision_at"`
	Reason     string `json:"reason"`
	Detail     string `json:"detail"`
}

type batchRunSummary struct {
	RequestedPartitions int64  `json:"requested_partitions"`
	AcceptedPartitions  int64  `json:"accepted_partitions"`
	RejectedPartitions  int64  `json:"rejected_partitions"`
	RowCount            int64  `json:"row_count"`
	Status              string `json:"status"`
}

type catalogOutput struct {
	Action               string `json:"action"`
	ArtifactID           string `json:"artifact_id"`
	AlreadyPresent       bool   `json:"already_present"`
	RegistrationSHA256   string `json:"registration_sha256"`
	OutputManifestPath   string `json:"output_manifest_path"`
	OutputManifestSHA256 string `json:"output_manifest_sha256"`
	FeatureSet           string `json:"feature_set"`
	FeatureSetVersion    string `json:"feature_set_version"`
	DecisionStart        string `json:"decision_start"`
	DecisionEnd          string `json:"decision_end"`
	RowCount             int64  `json:"row_count"`
	AcceptedPartitions   int64  `json:"accepted_partitions"`
	RejectedPartitions   int64  `json:"rejected_partitions"`
}

func main() {
	var databaseURL, manifestPath, featuresRoot string
	flag.StringVar(&databaseURL, "database-url", os.Getenv("DATABASE_URL"), "PostgreSQL URL")
	flag.StringVar(&manifestPath, "manifest", "", "validated feature batch manifest path")
	flag.StringVar(&featuresRoot, "features-root", "/data/features", "feature artifact root")
	flag.Parse()

	if err := validateInputs(databaseURL, manifestPath, featuresRoot); err != nil {
		fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	registration, outputPath, outputHash, err := loadBatchRegistration(manifestPath, featuresRoot)
	if err != nil {
		fatal(err)
	}
	repository, err := metadata.Open(ctx, databaseURL)
	if err != nil {
		fatal(fmt.Errorf("open metadata database: %w", err))
	}
	defer repository.Close()
	result, err := repository.RegisterFeatureArtifact(ctx, registration)
	if err != nil {
		fatal(fmt.Errorf("register feature artifact: %w", err))
	}

	normalized := registration
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(catalogOutput{
		Action:               "cataloged",
		ArtifactID:           result.ArtifactID,
		AlreadyPresent:       result.AlreadyPresent,
		RegistrationSHA256:   result.RegistrationSHA256,
		OutputManifestPath:   outputPath,
		OutputManifestSHA256: outputHash,
		FeatureSet:           normalized.FeatureSet,
		FeatureSetVersion:    normalized.FeatureSetVersion,
		DecisionStart:        normalized.DecisionStart.UTC().Format(time.RFC3339Nano),
		DecisionEnd:          normalized.DecisionEnd.UTC().Format(time.RFC3339Nano),
		RowCount:             normalized.RowCount,
		AcceptedPartitions:   normalized.AcceptedPartitions,
		RejectedPartitions:   normalized.RejectedPartitions,
	}); err != nil {
		fatal(fmt.Errorf("encode catalog result: %w", err))
	}
}

func validateInputs(databaseURL, manifestPath, featuresRoot string) error {
	if strings.TrimSpace(databaseURL) == "" {
		return errors.New("DATABASE_URL or --database-url is required")
	}
	if strings.TrimSpace(manifestPath) == "" {
		return errors.New("--manifest is required")
	}
	if strings.TrimSpace(featuresRoot) == "" {
		return errors.New("--features-root is required")
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "invs-feature-catalog: %v\n", err)
	os.Exit(2)
}

func loadBatchRegistration(manifestPath, featuresRoot string) (metadata.FeatureArtifactRegistration, string, string, error) {
	root, manifest, relative, err := resolveManifestPath(featuresRoot, manifestPath)
	if err != nil {
		return metadata.FeatureArtifactRegistration{}, "", "", err
	}
	manifestBytes, err := os.ReadFile(manifest)
	if err != nil {
		return metadata.FeatureArtifactRegistration{}, "", "", fmt.Errorf("read batch manifest %s: %w", manifest, err)
	}
	var document batchManifest
	if err := decodeStrictJSON(manifestBytes, &document); err != nil {
		return metadata.FeatureArtifactRegistration{}, "", "", fmt.Errorf("decode batch manifest %s: %w", manifest, err)
	}
	registration, err := registrationFromBatchManifest(document, relative, sha256Bytes(manifestBytes))
	if err != nil {
		return metadata.FeatureArtifactRegistration{}, "", "", err
	}
	for index, part := range document.Parts {
		childManifest, err := resolveFeaturePath(root, part.ManifestPath)
		if err != nil {
			return metadata.FeatureArtifactRegistration{}, "", "", fmt.Errorf("batch part %d manifest: %w", index, err)
		}
		actualManifestHash, err := sha256File(childManifest)
		if err != nil {
			return metadata.FeatureArtifactRegistration{}, "", "", fmt.Errorf("batch part %d manifest: %w", index, err)
		}
		if actualManifestHash != part.ManifestSHA256 {
			return metadata.FeatureArtifactRegistration{}, "", "", fmt.Errorf("batch part %d manifest hash mismatch: expected %s, got %s", index, part.ManifestSHA256, actualManifestHash)
		}
		childPart, err := resolveFeaturePath(root, part.PartPath)
		if err != nil {
			return metadata.FeatureArtifactRegistration{}, "", "", fmt.Errorf("batch part %d output part: %w", index, err)
		}
		actualPartHash, err := sha256File(childPart)
		if err != nil {
			return metadata.FeatureArtifactRegistration{}, "", "", fmt.Errorf("batch part %d output part: %w", index, err)
		}
		if actualPartHash != part.PartSHA256 {
			return metadata.FeatureArtifactRegistration{}, "", "", fmt.Errorf("batch part %d output part hash mismatch: expected %s, got %s", index, part.PartSHA256, actualPartHash)
		}
	}
	return registration, relative, sha256Bytes(manifestBytes), nil
}

func registrationFromBatchManifest(document batchManifest, outputPath, outputHash string) (metadata.FeatureArtifactRegistration, error) {
	decisionPoints, decisionTimes, err := validateBatchManifest(document)
	if err != nil {
		return metadata.FeatureArtifactRegistration{}, err
	}
	inputRefs := make([]metadata.FeatureArtifactInputRef, 0, len(document.SelectedInputManifests)+len(document.SelectedInputParts))
	for _, item := range document.SelectedInputManifests {
		inputRefs = append(inputRefs, metadata.FeatureArtifactInputRef{Kind: metadata.FeatureArtifactInputManifest, Path: item.Path, SHA256: item.SHA256})
	}
	for _, item := range document.SelectedInputParts {
		inputRefs = append(inputRefs, metadata.FeatureArtifactInputRef{Kind: metadata.FeatureArtifactInputPart, Path: item.Path, SHA256: item.SHA256})
	}
	inputFitness := make([]metadata.FeatureArtifactInputFitness, len(document.InputFitness))
	for index, item := range document.InputFitness {
		inputFitness[index] = metadata.FeatureArtifactInputFitness{
			Dataset: item.Dataset, HistoricalFitness: item.HistoricalFitness, AvailabilityPolicy: item.AvailabilityPolicy,
		}
	}
	universe := make([]metadata.FeatureArtifactUniverseMember, len(document.Universe.SecurityIDs))
	for index, securityID := range document.Universe.SecurityIDs {
		universe[index] = metadata.FeatureArtifactUniverseMember{Ordinal: index, SecurityID: securityID}
	}
	partitions := make([]metadata.FeatureArtifactPartition, len(document.Parts))
	for index, item := range document.Parts {
		decisionAt, _ := parseCanonicalTimestamp(item.DecisionAt, fmt.Sprintf("batch parts[%d].decision_at", index))
		partitions[index] = metadata.FeatureArtifactPartition{
			SecurityID: item.SecurityID, DecisionAt: decisionAt, ChildArtifactID: item.ArtifactID,
			ManifestPath: item.ManifestPath, ManifestSHA256: item.ManifestSHA256,
			PartPath: item.PartPath, PartSHA256: item.PartSHA256, RowCount: item.RowCount,
		}
	}
	createdAt, _ := parseCanonicalTimestamp(document.Batch.CreatedAt, "batch.created_at")
	calendarAvailableAt, _ := parseCanonicalTimestamp(document.CalendarPin.CalendarAvailableAt, "calendar_pin.calendar_available_at")
	registration := metadata.FeatureArtifactRegistration{
		ArtifactID: document.Batch.BatchID, ArtifactVersion: document.Batch.ArtifactVersion,
		FeatureSet: document.FeatureSet, FeatureSetVersion: document.FeatureSetVersion,
		RegistrySHA256: document.RegistrySHA256, GeneratorVersion: document.Batch.GeneratorVersion,
		GitCommit: document.Batch.GitCommit, DecisionStart: decisionTimes[0], DecisionEnd: decisionTimes[len(decisionTimes)-1],
		DecisionPoints: decisionPoints, UniverseFingerprint: document.Universe.Fingerprint, Universe: universe,
		InputFitness: inputFitness, InputFingerprint: document.InputFingerprint, InputRefs: inputRefs,
		OutputManifestPath: outputPath, OutputManifestSHA256: outputHash,
		CalendarDataSourceID: document.CalendarPin.DataSourceID, CalendarMIC: document.CalendarPin.MIC,
		CalendarVersion: document.CalendarPin.CalendarVersion, CalendarSessionFingerprint: document.CalendarPin.SessionFingerprint,
		CalendarAvailableAt: calendarAvailableAt, DecisionClockPolicy: document.CalendarPin.DecisionClockPolicy,
		RowCount: document.RowCount, RequestedPartitions: document.RunSummary.RequestedPartitions,
		AcceptedPartitions: document.RunSummary.AcceptedPartitions, RejectedPartitions: document.RunSummary.RejectedPartitions,
		Partitions: partitions, Status: metadata.FeatureArtifactPublishedStatus, CreatedAt: createdAt,
	}
	return registration, nil
}

func validateBatchManifest(document batchManifest) ([]metadata.FeatureArtifactDecisionPoint, []time.Time, error) {
	if document.SchemaVersion != batchSchemaVersion || document.ManifestVersion != batchManifestVersion {
		return nil, nil, errors.New("batch schema or manifest version is unsupported")
	}
	if !featureCatalogIdentifierPattern.MatchString(document.FeatureSet) || !featureCatalogVersionPattern.MatchString(document.FeatureSetVersion) {
		return nil, nil, errors.New("batch feature set identity is invalid")
	}
	if err := requireHash(document.RegistrySHA256, "batch.registry_sha256"); err != nil {
		return nil, nil, err
	}
	if err := validateBatchMetadata(document.Batch); err != nil {
		return nil, nil, err
	}
	decisionPoints, decisionTimes, err := validateDecisionSchedule(document.DecisionSchedule)
	if err != nil {
		return nil, nil, err
	}
	if err := validateCalendarPin(document.CalendarPin, decisionTimes[0]); err != nil {
		return nil, nil, err
	}
	if err := validateUniverse(document.Universe); err != nil {
		return nil, nil, err
	}
	if document.ComputationDelaySeconds < 0 {
		return nil, nil, errors.New("batch computation delay must be non-negative")
	}
	if err := validateInputFitness(document.InputFitness); err != nil {
		return nil, nil, err
	}
	if err := requireHash(document.InputFingerprint, "batch.input_fingerprint"); err != nil {
		return nil, nil, err
	}
	if err := validateInputRefs(document.SelectedInputManifests, "selected_input_manifests", false); err != nil {
		return nil, nil, err
	}
	if err := validateInputRefs(document.SelectedInputParts, "selected_input_parts", true); err != nil {
		return nil, nil, err
	}
	if err := validateBatchParts(document.Parts, document.Universe.SecurityIDs, document.DecisionSchedule); err != nil {
		return nil, nil, err
	}
	if err := validateBatchRejected(document.Rejected, document.Universe.SecurityIDs, document.DecisionSchedule); err != nil {
		return nil, nil, err
	}
	if document.RowCount < 0 || document.RowCount != sumBatchRows(document.Parts) {
		return nil, nil, errors.New("batch row_count is inconsistent with output parts")
	}
	if err := validateRunSummary(document.RunSummary, document.Parts, document.Rejected, document.RowCount); err != nil {
		return nil, nil, err
	}
	if expected := universeFingerprint(document.Universe.SecurityIDs); document.Universe.Fingerprint != expected {
		return nil, nil, errors.New("batch universe fingerprint is invalid")
	}
	if expected := batchInputFingerprint(document); document.InputFingerprint != expected {
		return nil, nil, errors.New("batch input fingerprint is invalid")
	}
	expectedBatchID := uuid.NewSHA1(featureBatchNamespace, []byte(document.InputFingerprint)).String()
	if document.Batch.BatchID != expectedBatchID {
		return nil, nil, errors.New("batch identity does not match its input fingerprint")
	}
	return decisionPoints, decisionTimes, nil
}

func validateBatchMetadata(value batchMetadata) error {
	if err := requireCanonicalUUID(value.BatchID, "batch.batch_id"); err != nil {
		return err
	}
	if value.ArtifactVersion != batchArtifactVersion || value.GeneratorVersion == "" || !featureCatalogGitPattern.MatchString(value.GitCommit) {
		return errors.New("batch metadata is invalid")
	}
	_, err := parseCanonicalTimestamp(value.CreatedAt, "batch.created_at")
	return err
}

func validateDecisionSchedule(values []string) ([]metadata.FeatureArtifactDecisionPoint, []time.Time, error) {
	if len(values) == 0 {
		return nil, nil, errors.New("batch decision_schedule must not be empty")
	}
	points := make([]metadata.FeatureArtifactDecisionPoint, len(values))
	times := make([]time.Time, len(values))
	for index, value := range values {
		parsed, err := parseCanonicalTimestamp(value, fmt.Sprintf("batch decision_schedule[%d]", index))
		if err != nil {
			return nil, nil, err
		}
		if index > 0 && !parsed.After(times[index-1]) {
			return nil, nil, errors.New("batch decision_schedule must be strictly increasing")
		}
		times[index] = parsed
		points[index] = metadata.FeatureArtifactDecisionPoint{Ordinal: index, DecisionAt: parsed}
	}
	return points, times, nil
}

func validateCalendarPin(value batchCalendarPin, firstDecision time.Time) error {
	if err := requireCanonicalUUID(value.DataSourceID, "calendar_pin.data_source_id"); err != nil {
		return err
	}
	if !featureCatalogMICPattern.MatchString(value.MIC) || !featureCatalogIdentifierPattern.MatchString(value.CalendarVersion) || value.DecisionClockPolicy != featureBatchClockPolicy {
		return errors.New("calendar pin is invalid")
	}
	if err := requireHash(value.SessionFingerprint, "calendar_pin.session_fingerprint"); err != nil {
		return err
	}
	availableAt, err := parseCanonicalTimestamp(value.CalendarAvailableAt, "calendar_pin.calendar_available_at")
	if err != nil {
		return err
	}
	if availableAt.After(firstDecision) {
		return errors.New("calendar_pin.calendar_available_at is after the first decision")
	}
	return nil
}

func validateUniverse(value batchUniverse) error {
	if value.Kind != "explicit_security_list" || len(value.SecurityIDs) == 0 || !sort.StringsAreSorted(value.SecurityIDs) {
		return errors.New("batch universe is invalid")
	}
	seen := make(map[string]struct{}, len(value.SecurityIDs))
	for index, securityID := range value.SecurityIDs {
		if err := requireCanonicalUUID(securityID, fmt.Sprintf("batch universe security_ids[%d]", index)); err != nil {
			return err
		}
		if _, ok := seen[securityID]; ok {
			return errors.New("batch universe security_ids must be unique")
		}
		seen[securityID] = struct{}{}
	}
	return requireHash(value.Fingerprint, "batch.universe.fingerprint")
}

func validateInputFitness(values []batchInputFitness) error {
	if len(values) == 0 {
		return errors.New("batch input_fitness must not be empty")
	}
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if !featureCatalogIdentifierPattern.MatchString(value.Dataset) {
			return fmt.Errorf("batch input_fitness[%d].dataset is invalid", index)
		}
		if _, ok := seen[value.Dataset]; ok {
			return fmt.Errorf("batch input_fitness dataset %s is duplicated", value.Dataset)
		}
		seen[value.Dataset] = struct{}{}
		if !validHistoricalFitness(value.HistoricalFitness) || !validAvailabilityPolicy(value.AvailabilityPolicy) {
			return fmt.Errorf("batch input_fitness[%d] contains an unsupported policy", index)
		}
	}
	return nil
}

func validateInputRefs(values []batchFileRef, label string, contentNamedPart bool) error {
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if err := requireSafeRelativePath(value.Path, fmt.Sprintf("batch %s[%d].path", label, index)); err != nil {
			return err
		}
		if contentNamedPart && !featureCatalogPartPattern.MatchString(filepath.Base(value.Path)) {
			return fmt.Errorf("batch %s[%d].path is not content-named", label, index)
		}
		if err := requireHash(value.SHA256, fmt.Sprintf("batch %s[%d].sha256", label, index)); err != nil {
			return err
		}
		key := value.Path + "\x00" + value.SHA256
		if _, ok := seen[key]; ok {
			return fmt.Errorf("batch %s contains duplicates", label)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateBatchParts(values []batchPart, securityIDs, decisionSchedule []string) error {
	securitySet := make(map[string]struct{}, len(securityIDs))
	for _, value := range securityIDs {
		securitySet[value] = struct{}{}
	}
	decisionSet := make(map[string]struct{}, len(decisionSchedule))
	for _, value := range decisionSchedule {
		decisionSet[value] = struct{}{}
	}
	seen := make(map[string]struct{}, len(values))
	children := make(map[string]struct{}, len(values))
	for index, value := range values {
		if err := requireCanonicalUUID(value.SecurityID, fmt.Sprintf("batch parts[%d].security_id", index)); err != nil {
			return err
		}
		if _, ok := securitySet[value.SecurityID]; !ok {
			return fmt.Errorf("batch parts[%d] security is outside the universe", index)
		}
		if _, ok := decisionSet[value.DecisionAt]; !ok {
			return fmt.Errorf("batch parts[%d] decision is outside the schedule", index)
		}
		if _, err := parseCanonicalTimestamp(value.DecisionAt, fmt.Sprintf("batch parts[%d].decision_at", index)); err != nil {
			return err
		}
		if err := requireCanonicalUUID(value.ArtifactID, fmt.Sprintf("batch parts[%d].artifact_id", index)); err != nil {
			return err
		}
		for field, path := range map[string]string{"manifest_path": value.ManifestPath, "part_path": value.PartPath} {
			if err := requireSafeRelativePath(path, fmt.Sprintf("batch parts[%d].%s", index, field)); err != nil {
				return err
			}
		}
		if err := requireHash(value.ManifestSHA256, fmt.Sprintf("batch parts[%d].manifest_sha256", index)); err != nil {
			return err
		}
		if err := requireHash(value.PartSHA256, fmt.Sprintf("batch parts[%d].part_sha256", index)); err != nil {
			return err
		}
		if value.RowCount < 0 {
			return fmt.Errorf("batch parts[%d].row_count must be non-negative", index)
		}
		key := value.SecurityID + "\x00" + value.DecisionAt
		if _, ok := seen[key]; ok {
			return errors.New("batch parts contain duplicate partitions")
		}
		seen[key] = struct{}{}
		if _, ok := children[value.ArtifactID]; ok {
			return errors.New("batch parts contain duplicate child artifacts")
		}
		children[value.ArtifactID] = struct{}{}
	}
	return nil
}

func validateBatchRejected(values []batchRejectedPartition, securityIDs, decisionSchedule []string) error {
	securitySet := make(map[string]struct{}, len(securityIDs))
	for _, value := range securityIDs {
		securitySet[value] = struct{}{}
	}
	decisionSet := make(map[string]struct{}, len(decisionSchedule))
	for _, value := range decisionSchedule {
		decisionSet[value] = struct{}{}
	}
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if err := requireCanonicalUUID(value.SecurityID, fmt.Sprintf("batch rejected[%d].security_id", index)); err != nil {
			return err
		}
		if _, ok := securitySet[value.SecurityID]; !ok {
			return fmt.Errorf("batch rejected[%d] security is outside the universe", index)
		}
		if _, ok := decisionSet[value.DecisionAt]; !ok {
			return fmt.Errorf("batch rejected[%d] decision is outside the schedule", index)
		}
		if _, err := parseCanonicalTimestamp(value.DecisionAt, fmt.Sprintf("batch rejected[%d].decision_at", index)); err != nil {
			return err
		}
		if !featureCatalogIdentifierPattern.MatchString(value.Reason) || value.Detail == "" {
			return fmt.Errorf("batch rejected[%d] is invalid", index)
		}
		key := value.SecurityID + "\x00" + value.DecisionAt
		if _, ok := seen[key]; ok {
			return errors.New("batch rejected contains duplicate partitions")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateRunSummary(value batchRunSummary, parts []batchPart, rejected []batchRejectedPartition, rowCount int64) error {
	if value.Status != featureBatchStatus || value.RequestedPartitions != int64(len(parts)+len(rejected)) || value.AcceptedPartitions != int64(len(parts)) || value.RejectedPartitions != int64(len(rejected)) || value.RowCount != rowCount {
		return errors.New("batch run_summary is inconsistent")
	}
	if value.RequestedPartitions <= 0 || value.AcceptedPartitions < 0 || value.RejectedPartitions < 0 || value.RowCount < 0 {
		return errors.New("batch run_summary contains invalid counts")
	}
	return nil
}

func sumBatchRows(parts []batchPart) int64 {
	var total int64
	for _, part := range parts {
		total += part.RowCount
	}
	return total
}

func universeFingerprint(securityIDs []string) string {
	digest := sha256BytesMust(map[string]any{"kind": "explicit_security_list", "security_ids": securityIDs})
	return digest
}

func batchInputFingerprint(document batchManifest) string {
	manifests := append([]batchFileRef(nil), document.SelectedInputManifests...)
	parts := append([]batchFileRef(nil), document.SelectedInputParts...)
	sort.Slice(manifests, func(i, j int) bool { return fileRefLess(manifests[i], manifests[j]) })
	sort.Slice(parts, func(i, j int) bool { return fileRefLess(parts[i], parts[j]) })
	calendar := map[string]any{
		"data_source_id":        document.CalendarPin.DataSourceID,
		"mic":                   document.CalendarPin.MIC,
		"calendar_version":      document.CalendarPin.CalendarVersion,
		"session_fingerprint":   document.CalendarPin.SessionFingerprint,
		"calendar_available_at": document.CalendarPin.CalendarAvailableAt,
		"decision_clock_policy": document.CalendarPin.DecisionClockPolicy,
	}
	selectedManifests := make([]map[string]string, len(manifests))
	for index, item := range manifests {
		selectedManifests[index] = map[string]string{"path": item.Path, "sha256": item.SHA256}
	}
	selectedParts := make([]map[string]string, len(parts))
	for index, item := range parts {
		selectedParts[index] = map[string]string{"path": item.Path, "sha256": item.SHA256}
	}
	payload := map[string]any{
		"calendar_pin":              calendar,
		"computation_delay_seconds": document.ComputationDelaySeconds,
		"decision_schedule":         document.DecisionSchedule,
		"feature_set":               document.FeatureSet,
		"feature_set_version":       document.FeatureSetVersion,
		"registry_sha256":           document.RegistrySHA256,
		"selected_input_manifests":  selectedManifests,
		"selected_input_parts":      selectedParts,
		"universe": map[string]any{
			"kind":         document.Universe.Kind,
			"security_ids": document.Universe.SecurityIDs,
			"fingerprint":  document.Universe.Fingerprint,
		},
	}
	return sha256BytesMust(payload)
}

func fileRefLess(left, right batchFileRef) bool {
	if left.Path != right.Path {
		return left.Path < right.Path
	}
	return left.SHA256 < right.SHA256
}

func validHistoricalFitness(value string) bool {
	return value == "backtest_safe" || value == "current_research_only" || value == "installation_replay_only" || value == "unsupported"
}

func validAvailabilityPolicy(value string) bool {
	return value == "exact_publication" || value == "source_declared" || value == "conservative_receipt_time" || value == "current_snapshot" || value == "unknown"
}

func requireCanonicalUUID(value, label string) error {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.String() != value {
		return fmt.Errorf("%s must be a lower-case canonical UUID", label)
	}
	return nil
}

func requireHash(value, label string) error {
	if !featureCatalogHashPattern.MatchString(value) {
		return fmt.Errorf("%s must be a lower-case SHA-256", label)
	}
	return nil
}

func requireSafeRelativePath(value, label string) error {
	if value == "" || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "\\") || strings.ContainsAny(value, "\\\x00") {
		return fmt.Errorf("%s must be a safe relative path", label)
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return fmt.Errorf("%s contains an unsafe path component", label)
		}
	}
	return nil
}

func parseCanonicalTimestamp(value, label string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.UTC().Format(time.RFC3339Nano) != value {
		return time.Time{}, fmt.Errorf("%s must be canonical UTC", label)
	}
	return parsed.UTC(), nil
}

func sha256Bytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func sha256BytesMust(value any) string {
	canonical, err := canonicalJSON(value)
	if err != nil {
		panic(err)
	}
	return sha256Bytes(canonical)
}

func canonicalJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func resolveManifestPath(featuresRoot, manifestPath string) (string, string, string, error) {
	root, err := filepath.Abs(featuresRoot)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve features root: %w", err)
	}
	manifest, err := filepath.Abs(manifestPath)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve batch manifest: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve features root %s: %w", featuresRoot, err)
	}
	manifest, err = filepath.EvalSymlinks(manifest)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve batch manifest %s: %w", manifestPath, err)
	}
	if filepath.Base(manifest) != "manifest.json" {
		return "", "", "", errors.New("batch manifest filename must be manifest.json")
	}
	relative, err := filepath.Rel(root, manifest)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", "", "", errors.New("batch manifest must be inside features root")
	}
	return root, manifest, filepath.ToSlash(relative), nil
}

func resolveFeaturePath(root, relative string) (string, error) {
	if err := requireSafeRelativePath(relative, "feature path"); err != nil {
		return "", err
	}
	candidate := filepath.Join(root, filepath.FromSlash(relative))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", relative, err)
	}
	actualRelative, err := filepath.Rel(root, resolved)
	if err != nil || actualRelative == ".." || strings.HasPrefix(actualRelative, ".."+string(filepath.Separator)) || filepath.IsAbs(actualRelative) {
		return "", fmt.Errorf("feature path %s escapes features root", relative)
	}
	return resolved, nil
}

func decodeStrictJSON(data []byte, target any) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := walkJSON(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func walkJSON(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = struct{}{}
			if err := walkJSON(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := walkJSON(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}
