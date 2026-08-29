package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const FeatureArtifactPublishedStatus = "published"

type FeatureArtifactInputKind string

const (
	FeatureArtifactInputManifest FeatureArtifactInputKind = "manifest"
	FeatureArtifactInputPart     FeatureArtifactInputKind = "part"
)

// FeatureArtifactInputRef is a selected canonical manifest or part referenced
// by a registered feature batch. The path is relative to the feature root.
type FeatureArtifactInputRef struct {
	Kind   FeatureArtifactInputKind `json:"kind"`
	Path   string                   `json:"path"`
	SHA256 string                   `json:"sha256"`
}

type FeatureArtifactDecisionPoint struct {
	Ordinal    int       `json:"ordinal"`
	DecisionAt time.Time `json:"decision_at"`
}

type FeatureArtifactUniverseMember struct {
	Ordinal    int    `json:"ordinal"`
	SecurityID string `json:"security_id"`
}

// FeatureArtifactPartition points from a dataset-level feature artifact to an
// accepted child manifest and its immutable output part. Feature values stay
// in the referenced Parquet object.
type FeatureArtifactPartition struct {
	SecurityID      string    `json:"security_id"`
	DecisionAt      time.Time `json:"decision_at"`
	ChildArtifactID string    `json:"child_artifact_id"`
	ManifestPath    string    `json:"manifest_path"`
	ManifestSHA256  string    `json:"manifest_sha256"`
	PartPath        string    `json:"part_path"`
	PartSHA256      string    `json:"part_sha256"`
	RowCount        int64     `json:"row_count"`
}

// FeatureArtifactRegistration is the normalized metadata envelope written to
// PostgreSQL after the immutable batch manifest and all listed child parts
// have been validated. The slices are canonicalized before persistence.
type FeatureArtifactRegistration struct {
	ArtifactID                 string                          `json:"artifact_id"`
	ArtifactVersion            string                          `json:"artifact_version"`
	FeatureSet                 string                          `json:"feature_set"`
	FeatureSetVersion          string                          `json:"feature_set_version"`
	RegistrySHA256             string                          `json:"registry_sha256"`
	GeneratorVersion           string                          `json:"generator_version"`
	GitCommit                  string                          `json:"git_commit"`
	DecisionStart              time.Time                       `json:"decision_start"`
	DecisionEnd                time.Time                       `json:"decision_end"`
	DecisionPoints             []FeatureArtifactDecisionPoint  `json:"decision_points"`
	UniverseFingerprint        string                          `json:"universe_fingerprint"`
	Universe                   []FeatureArtifactUniverseMember `json:"universe"`
	InputFingerprint           string                          `json:"input_fingerprint"`
	InputRefs                  []FeatureArtifactInputRef       `json:"input_refs"`
	OutputManifestPath         string                          `json:"output_manifest_path"`
	OutputManifestSHA256       string                          `json:"output_manifest_sha256"`
	CalendarDataSourceID       string                          `json:"calendar_data_source_id"`
	CalendarMIC                string                          `json:"calendar_mic"`
	CalendarVersion            string                          `json:"calendar_version"`
	CalendarSessionFingerprint string                          `json:"calendar_session_fingerprint"`
	CalendarAvailableAt        time.Time                       `json:"calendar_available_at"`
	DecisionClockPolicy        string                          `json:"decision_clock_policy"`
	RowCount                   int64                           `json:"row_count"`
	RequestedPartitions        int64                           `json:"requested_partitions"`
	AcceptedPartitions         int64                           `json:"accepted_partitions"`
	RejectedPartitions         int64                           `json:"rejected_partitions"`
	Partitions                 []FeatureArtifactPartition      `json:"partitions"`
	Status                     string                          `json:"status"`
	CreatedAt                  time.Time                       `json:"created_at"`
}

type FeatureArtifactRegistrationResult struct {
	ArtifactID         string
	RegistrationSHA256 string
	AlreadyPresent     bool
}

var (
	featureArtifactIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)
	featureArtifactVersionPattern    = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	featureArtifactHashPattern       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	featureArtifactGitPattern        = regexp.MustCompile(`^(unknown|[0-9a-f]{40})$`)
	featureArtifactMICPattern        = regexp.MustCompile(`^[A-Z0-9]{4}$`)
)

const insertFeatureArtifactSQL = `
INSERT INTO feature_artifacts (
    artifact_id, artifact_version, feature_set, feature_set_version,
    registry_sha256, generator_version, git_commit, decision_start, decision_end,
    universe_fingerprint, input_fingerprint, output_manifest_path,
    output_manifest_sha256, calendar_data_source_id, calendar_mic,
    calendar_version, calendar_session_fingerprint, calendar_available_at,
    decision_clock_policy, row_count, requested_partitions, accepted_partitions,
    rejected_partitions, status, created_at, registration_sha256
) VALUES (
    $1::uuid, $2, $3, $4,
    $5, $6, $7, $8, $9,
    $10, $11, $12,
    $13, $14::uuid, $15,
    $16, $17, $18,
    $19, $20, $21, $22,
    $23, $24, $25, $26
)
ON CONFLICT (artifact_id) DO NOTHING
RETURNING registration_sha256`

const selectFeatureArtifactRegistrationSQL = `
SELECT registration_sha256
FROM feature_artifacts
WHERE artifact_id = $1::uuid`

func (r FeatureArtifactRegistration) normalized() (FeatureArtifactRegistration, error) {
	r.ArtifactID = strings.TrimSpace(r.ArtifactID)
	r.ArtifactVersion = strings.TrimSpace(r.ArtifactVersion)
	r.FeatureSet = strings.TrimSpace(r.FeatureSet)
	r.FeatureSetVersion = strings.TrimSpace(r.FeatureSetVersion)
	r.RegistrySHA256 = strings.TrimSpace(r.RegistrySHA256)
	r.GeneratorVersion = strings.TrimSpace(r.GeneratorVersion)
	r.GitCommit = strings.TrimSpace(r.GitCommit)
	r.UniverseFingerprint = strings.TrimSpace(r.UniverseFingerprint)
	r.InputFingerprint = strings.TrimSpace(r.InputFingerprint)
	r.OutputManifestPath = strings.TrimSpace(r.OutputManifestPath)
	r.OutputManifestSHA256 = strings.TrimSpace(r.OutputManifestSHA256)
	r.CalendarDataSourceID = strings.TrimSpace(r.CalendarDataSourceID)
	r.CalendarMIC = strings.TrimSpace(r.CalendarMIC)
	r.CalendarVersion = strings.TrimSpace(r.CalendarVersion)
	r.CalendarSessionFingerprint = strings.TrimSpace(r.CalendarSessionFingerprint)
	r.DecisionClockPolicy = strings.TrimSpace(r.DecisionClockPolicy)
	r.Status = strings.TrimSpace(r.Status)
	if r.Status == "" {
		r.Status = FeatureArtifactPublishedStatus
	}

	if err := requireFeatureArtifactUUID(r.ArtifactID, "artifact_id"); err != nil {
		return FeatureArtifactRegistration{}, err
	}
	if err := requireFeatureArtifactVersion(r.ArtifactVersion, "artifact_version"); err != nil {
		return FeatureArtifactRegistration{}, err
	}
	if err := requireFeatureArtifactIdentifier(r.FeatureSet, "feature_set"); err != nil {
		return FeatureArtifactRegistration{}, err
	}
	if err := requireFeatureArtifactVersion(r.FeatureSetVersion, "feature_set_version"); err != nil {
		return FeatureArtifactRegistration{}, err
	}
	for _, item := range []struct {
		value string
		label string
	}{
		{r.RegistrySHA256, "registry_sha256"},
		{r.UniverseFingerprint, "universe_fingerprint"},
		{r.InputFingerprint, "input_fingerprint"},
		{r.OutputManifestSHA256, "output_manifest_sha256"},
		{r.CalendarSessionFingerprint, "calendar_session_fingerprint"},
	} {
		if err := requireFeatureArtifactHash(item.value, item.label); err != nil {
			return FeatureArtifactRegistration{}, err
		}
	}
	if r.GeneratorVersion == "" {
		return FeatureArtifactRegistration{}, errors.New("generator_version is required")
	}
	if !featureArtifactGitPattern.MatchString(r.GitCommit) {
		return FeatureArtifactRegistration{}, fmt.Errorf("git_commit %q is not a lower-case SHA-1 or 'unknown'", r.GitCommit)
	}
	if err := requireFeatureArtifactPath(r.OutputManifestPath, "output_manifest_path"); err != nil {
		return FeatureArtifactRegistration{}, err
	}
	if err := requireFeatureArtifactUUID(r.CalendarDataSourceID, "calendar_data_source_id"); err != nil {
		return FeatureArtifactRegistration{}, err
	}
	if !featureArtifactMICPattern.MatchString(r.CalendarMIC) {
		return FeatureArtifactRegistration{}, fmt.Errorf("calendar_mic %q is invalid", r.CalendarMIC)
	}
	if err := requireFeatureArtifactIdentifier(r.CalendarVersion, "calendar_version"); err != nil {
		return FeatureArtifactRegistration{}, err
	}
	if r.DecisionClockPolicy == "" {
		return FeatureArtifactRegistration{}, errors.New("decision_clock_policy is required")
	}
	if r.Status != "published" && r.Status != "invalid" && r.Status != "orphaned" {
		return FeatureArtifactRegistration{}, fmt.Errorf("status %q is unsupported", r.Status)
	}
	if r.CreatedAt.IsZero() {
		return FeatureArtifactRegistration{}, errors.New("created_at is required")
	}
	if r.RowCount < 0 {
		return FeatureArtifactRegistration{}, errors.New("row_count must be non-negative")
	}
	if r.RequestedPartitions <= 0 {
		return FeatureArtifactRegistration{}, errors.New("requested_partitions must be positive")
	}
	if r.AcceptedPartitions < 0 || r.RejectedPartitions < 0 {
		return FeatureArtifactRegistration{}, errors.New("partition counts must be non-negative")
	}
	if r.RequestedPartitions != r.AcceptedPartitions+r.RejectedPartitions {
		return FeatureArtifactRegistration{}, errors.New("requested_partitions must equal accepted plus rejected partitions")
	}
	if r.AcceptedPartitions != int64(len(r.Partitions)) {
		return FeatureArtifactRegistration{}, errors.New("accepted_partitions does not match partitions")
	}

	points := make([]FeatureArtifactDecisionPoint, len(r.DecisionPoints))
	copy(points, r.DecisionPoints)
	if len(points) == 0 {
		return FeatureArtifactRegistration{}, errors.New("decision_points must not be empty")
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Ordinal < points[j].Ordinal })
	for index := range points {
		if points[index].Ordinal != index {
			return FeatureArtifactRegistration{}, fmt.Errorf("decision_points ordinal %d is not canonical", points[index].Ordinal)
		}
		if points[index].DecisionAt.IsZero() {
			return FeatureArtifactRegistration{}, fmt.Errorf("decision_points[%d].decision_at is required", index)
		}
		points[index].DecisionAt = points[index].DecisionAt.UTC()
		if index > 0 && !points[index].DecisionAt.After(points[index-1].DecisionAt) {
			return FeatureArtifactRegistration{}, errors.New("decision_points must be strictly increasing")
		}
	}
	r.DecisionPoints = points
	r.DecisionStart = points[0].DecisionAt
	r.DecisionEnd = points[len(points)-1].DecisionAt

	if r.CalendarAvailableAt.IsZero() {
		return FeatureArtifactRegistration{}, errors.New("calendar_available_at is required")
	}
	r.CalendarAvailableAt = r.CalendarAvailableAt.UTC()
	if r.CalendarAvailableAt.After(r.DecisionStart) {
		return FeatureArtifactRegistration{}, errors.New("calendar_available_at is after decision_start")
	}
	r.CreatedAt = r.CreatedAt.UTC()

	universe := make([]FeatureArtifactUniverseMember, len(r.Universe))
	copy(universe, r.Universe)
	if len(universe) == 0 {
		return FeatureArtifactRegistration{}, errors.New("universe must not be empty")
	}
	sort.Slice(universe, func(i, j int) bool { return universe[i].Ordinal < universe[j].Ordinal })
	universeIDs := make(map[string]struct{}, len(universe))
	for index := range universe {
		if universe[index].Ordinal != index {
			return FeatureArtifactRegistration{}, fmt.Errorf("universe ordinal %d is not canonical", universe[index].Ordinal)
		}
		if err := requireFeatureArtifactUUID(universe[index].SecurityID, fmt.Sprintf("universe[%d].security_id", index)); err != nil {
			return FeatureArtifactRegistration{}, err
		}
		if _, exists := universeIDs[universe[index].SecurityID]; exists {
			return FeatureArtifactRegistration{}, fmt.Errorf("universe security %s is duplicated", universe[index].SecurityID)
		}
		universeIDs[universe[index].SecurityID] = struct{}{}
	}
	r.Universe = universe

	inputRefs := make([]FeatureArtifactInputRef, len(r.InputRefs))
	copy(inputRefs, r.InputRefs)
	for index := range inputRefs {
		inputRefs[index].Kind = FeatureArtifactInputKind(strings.TrimSpace(string(inputRefs[index].Kind)))
		inputRefs[index].Path = strings.TrimSpace(inputRefs[index].Path)
		inputRefs[index].SHA256 = strings.TrimSpace(inputRefs[index].SHA256)
		if inputRefs[index].Kind != FeatureArtifactInputManifest && inputRefs[index].Kind != FeatureArtifactInputPart {
			return FeatureArtifactRegistration{}, fmt.Errorf("input_refs[%d].kind %q is unsupported", index, inputRefs[index].Kind)
		}
		if err := requireFeatureArtifactPath(inputRefs[index].Path, fmt.Sprintf("input_refs[%d].path", index)); err != nil {
			return FeatureArtifactRegistration{}, err
		}
		if err := requireFeatureArtifactHash(inputRefs[index].SHA256, fmt.Sprintf("input_refs[%d].sha256", index)); err != nil {
			return FeatureArtifactRegistration{}, err
		}
	}
	sort.Slice(inputRefs, func(i, j int) bool {
		if inputRefs[i].Kind != inputRefs[j].Kind {
			return inputRefs[i].Kind < inputRefs[j].Kind
		}
		if inputRefs[i].Path != inputRefs[j].Path {
			return inputRefs[i].Path < inputRefs[j].Path
		}
		return inputRefs[i].SHA256 < inputRefs[j].SHA256
	})
	for index := 1; index < len(inputRefs); index++ {
		if inputRefs[index].Kind == inputRefs[index-1].Kind && inputRefs[index].Path == inputRefs[index-1].Path {
			return FeatureArtifactRegistration{}, fmt.Errorf("input reference %s/%s is duplicated or has conflicting hashes", inputRefs[index].Kind, inputRefs[index].Path)
		}
	}
	r.InputRefs = inputRefs

	partitions := make([]FeatureArtifactPartition, len(r.Partitions))
	copy(partitions, r.Partitions)
	pointSet := make(map[time.Time]struct{}, len(points))
	for _, point := range points {
		pointSet[point.DecisionAt] = struct{}{}
	}
	partitionKeys := make(map[string]struct{}, len(partitions))
	childIDs := make(map[string]struct{}, len(partitions))
	var partitionRows int64
	for index := range partitions {
		partitions[index].SecurityID = strings.TrimSpace(partitions[index].SecurityID)
		partitions[index].ChildArtifactID = strings.TrimSpace(partitions[index].ChildArtifactID)
		partitions[index].ManifestPath = strings.TrimSpace(partitions[index].ManifestPath)
		partitions[index].ManifestSHA256 = strings.TrimSpace(partitions[index].ManifestSHA256)
		partitions[index].PartPath = strings.TrimSpace(partitions[index].PartPath)
		partitions[index].PartSHA256 = strings.TrimSpace(partitions[index].PartSHA256)
		partitions[index].DecisionAt = partitions[index].DecisionAt.UTC()
		if err := requireFeatureArtifactUUID(partitions[index].SecurityID, fmt.Sprintf("partitions[%d].security_id", index)); err != nil {
			return FeatureArtifactRegistration{}, err
		}
		if _, exists := universeIDs[partitions[index].SecurityID]; !exists {
			return FeatureArtifactRegistration{}, fmt.Errorf("partitions[%d] security %s is outside the universe", index, partitions[index].SecurityID)
		}
		if _, exists := pointSet[partitions[index].DecisionAt]; !exists {
			return FeatureArtifactRegistration{}, fmt.Errorf("partitions[%d] decision_at is outside the decision schedule", index)
		}
		if err := requireFeatureArtifactUUID(partitions[index].ChildArtifactID, fmt.Sprintf("partitions[%d].child_artifact_id", index)); err != nil {
			return FeatureArtifactRegistration{}, err
		}
		if err := requireFeatureArtifactPath(partitions[index].ManifestPath, fmt.Sprintf("partitions[%d].manifest_path", index)); err != nil {
			return FeatureArtifactRegistration{}, err
		}
		if err := requireFeatureArtifactPath(partitions[index].PartPath, fmt.Sprintf("partitions[%d].part_path", index)); err != nil {
			return FeatureArtifactRegistration{}, err
		}
		if err := requireFeatureArtifactHash(partitions[index].ManifestSHA256, fmt.Sprintf("partitions[%d].manifest_sha256", index)); err != nil {
			return FeatureArtifactRegistration{}, err
		}
		if err := requireFeatureArtifactHash(partitions[index].PartSHA256, fmt.Sprintf("partitions[%d].part_sha256", index)); err != nil {
			return FeatureArtifactRegistration{}, err
		}
		if partitions[index].RowCount <= 0 {
			return FeatureArtifactRegistration{}, fmt.Errorf("partitions[%d].row_count must be positive", index)
		}
		partitionRows += partitions[index].RowCount
		key := partitions[index].SecurityID + "\x00" + partitions[index].DecisionAt.Format(time.RFC3339Nano)
		if _, exists := partitionKeys[key]; exists {
			return FeatureArtifactRegistration{}, fmt.Errorf("partition %s is duplicated", key)
		}
		partitionKeys[key] = struct{}{}
		if _, exists := childIDs[partitions[index].ChildArtifactID]; exists {
			return FeatureArtifactRegistration{}, fmt.Errorf("child artifact %s is duplicated", partitions[index].ChildArtifactID)
		}
		childIDs[partitions[index].ChildArtifactID] = struct{}{}
	}
	sort.Slice(partitions, func(i, j int) bool {
		if !partitions[i].DecisionAt.Equal(partitions[j].DecisionAt) {
			return partitions[i].DecisionAt.Before(partitions[j].DecisionAt)
		}
		return partitions[i].SecurityID < partitions[j].SecurityID
	})
	if partitionRows != r.RowCount {
		return FeatureArtifactRegistration{}, fmt.Errorf("row_count %d does not match partition rows %d", r.RowCount, partitionRows)
	}
	r.Partitions = partitions

	return r, nil
}

func requireFeatureArtifactUUID(value, label string) error {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.String() != value {
		return fmt.Errorf("%s must be a lower-case canonical UUID", label)
	}
	return nil
}

func requireFeatureArtifactVersion(value, label string) error {
	if !featureArtifactVersionPattern.MatchString(value) {
		return fmt.Errorf("%s %q is not a semantic version", label, value)
	}
	return nil
}

func requireFeatureArtifactIdentifier(value, label string) error {
	if !featureArtifactIdentifierPattern.MatchString(value) {
		return fmt.Errorf("%s %q is invalid", label, value)
	}
	return nil
}

func requireFeatureArtifactHash(value, label string) error {
	if !featureArtifactHashPattern.MatchString(value) {
		return fmt.Errorf("%s must be a lower-case SHA-256", label)
	}
	return nil
}

func requireFeatureArtifactPath(value, label string) error {
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

func featureArtifactRegistrationSHA256(registration FeatureArtifactRegistration) (string, error) {
	canonical, err := json.Marshal(registration)
	if err != nil {
		return "", fmt.Errorf("canonicalize feature artifact registration: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

// RegisterFeatureArtifact records a validated immutable batch registration.
// Repeating the same complete registration is a no-op; reusing an artifact ID
// for different metadata or lineage is rejected without changing the catalog.
func (r *Repository) RegisterFeatureArtifact(ctx context.Context, registration FeatureArtifactRegistration) (FeatureArtifactRegistrationResult, error) {
	if r == nil {
		return FeatureArtifactRegistrationResult{}, errors.New("PostgreSQL metadata repository is required to register a feature artifact")
	}
	normalized, err := registration.normalized()
	if err != nil {
		return FeatureArtifactRegistrationResult{}, err
	}
	if normalized.Status != FeatureArtifactPublishedStatus {
		return FeatureArtifactRegistrationResult{}, fmt.Errorf("feature artifact registration status must be %q", FeatureArtifactPublishedStatus)
	}
	registrationSHA256, err := featureArtifactRegistrationSHA256(normalized)
	if err != nil {
		return FeatureArtifactRegistrationResult{}, err
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return FeatureArtifactRegistrationResult{}, err
	}
	defer tx.Rollback(ctx)

	var insertedSHA256 string
	err = tx.QueryRow(ctx, insertFeatureArtifactSQL,
		normalized.ArtifactID,
		normalized.ArtifactVersion,
		normalized.FeatureSet,
		normalized.FeatureSetVersion,
		normalized.RegistrySHA256,
		normalized.GeneratorVersion,
		normalized.GitCommit,
		normalized.DecisionStart,
		normalized.DecisionEnd,
		normalized.UniverseFingerprint,
		normalized.InputFingerprint,
		normalized.OutputManifestPath,
		normalized.OutputManifestSHA256,
		normalized.CalendarDataSourceID,
		normalized.CalendarMIC,
		normalized.CalendarVersion,
		normalized.CalendarSessionFingerprint,
		normalized.CalendarAvailableAt,
		normalized.DecisionClockPolicy,
		normalized.RowCount,
		normalized.RequestedPartitions,
		normalized.AcceptedPartitions,
		normalized.RejectedPartitions,
		normalized.Status,
		normalized.CreatedAt,
		registrationSHA256,
	).Scan(&insertedSHA256)
	if err == nil {
		if insertedSHA256 != registrationSHA256 {
			return FeatureArtifactRegistrationResult{}, errors.New("database returned an unexpected feature artifact registration hash")
		}
		if err := insertFeatureArtifactLineage(ctx, tx, normalized); err != nil {
			return FeatureArtifactRegistrationResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return FeatureArtifactRegistrationResult{}, err
		}
		return FeatureArtifactRegistrationResult{
			ArtifactID:         normalized.ArtifactID,
			RegistrationSHA256: registrationSHA256,
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return FeatureArtifactRegistrationResult{}, err
	}
	var existingSHA256 string
	if err := tx.QueryRow(ctx, selectFeatureArtifactRegistrationSQL, normalized.ArtifactID).Scan(&existingSHA256); err != nil {
		return FeatureArtifactRegistrationResult{}, err
	}
	if existingSHA256 != registrationSHA256 {
		return FeatureArtifactRegistrationResult{}, fmt.Errorf("feature artifact %s conflicts with existing registration", normalized.ArtifactID)
	}
	if err := tx.Commit(ctx); err != nil {
		return FeatureArtifactRegistrationResult{}, err
	}
	return FeatureArtifactRegistrationResult{
		ArtifactID:         normalized.ArtifactID,
		RegistrationSHA256: registrationSHA256,
		AlreadyPresent:     true,
	}, nil
}

func insertFeatureArtifactLineage(ctx context.Context, tx pgx.Tx, registration FeatureArtifactRegistration) error {
	for _, point := range registration.DecisionPoints {
		if _, err := tx.Exec(ctx, `INSERT INTO feature_artifact_decision_points(artifact_id,ordinal,decision_at) VALUES($1::uuid,$2,$3)`, registration.ArtifactID, point.Ordinal, point.DecisionAt); err != nil {
			return fmt.Errorf("insert feature artifact decision point %d: %w", point.Ordinal, err)
		}
	}
	for _, member := range registration.Universe {
		if _, err := tx.Exec(ctx, `INSERT INTO feature_artifact_universe_members(artifact_id,ordinal,security_id) VALUES($1::uuid,$2,$3::uuid)`, registration.ArtifactID, member.Ordinal, member.SecurityID); err != nil {
			return fmt.Errorf("insert feature artifact universe member %s: %w", member.SecurityID, err)
		}
	}
	for _, input := range registration.InputRefs {
		if _, err := tx.Exec(ctx, `INSERT INTO feature_artifact_input_refs(artifact_id,input_kind,path,sha256) VALUES($1::uuid,$2,$3,$4)`, registration.ArtifactID, input.Kind, input.Path, input.SHA256); err != nil {
			return fmt.Errorf("insert feature artifact input %s: %w", input.Path, err)
		}
	}
	for _, partition := range registration.Partitions {
		if _, err := tx.Exec(ctx, `INSERT INTO feature_artifact_partitions(artifact_id,security_id,decision_at,child_artifact_id,manifest_path,manifest_sha256,part_path,part_sha256,row_count) VALUES($1::uuid,$2::uuid,$3,$4::uuid,$5,$6,$7,$8,$9)`, registration.ArtifactID, partition.SecurityID, partition.DecisionAt, partition.ChildArtifactID, partition.ManifestPath, partition.ManifestSHA256, partition.PartPath, partition.PartSHA256, partition.RowCount); err != nil {
			return fmt.Errorf("insert feature artifact partition %s/%s: %w", partition.SecurityID, partition.DecisionAt.Format(time.RFC3339Nano), err)
		}
	}
	return nil
}
