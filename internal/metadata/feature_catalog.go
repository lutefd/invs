package metadata

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const FeatureArtifactCatalogReportVersion = 1

// FeatureArtifactCatalogFilter limits a read-only catalog report. Empty fields
// mean that the corresponding dimension is not filtered.
type FeatureArtifactCatalogFilter struct {
	ArtifactID        string `json:"artifact_id"`
	FeatureSet        string `json:"feature_set"`
	FeatureSetVersion string `json:"feature_set_version"`
}

type FeatureArtifactDecisionCoverage struct {
	DecisionAt            time.Time `json:"decision_at"`
	ExpectedPartitions    int64     `json:"expected_partitions"`
	AcceptedPartitions    int64     `json:"accepted_partitions"`
	UnaccountedPartitions int64     `json:"unaccounted_partitions"`
	AcceptedRowCount      int64     `json:"accepted_row_count"`
}

// FeatureArtifactCatalogArtifact is a read model assembled from the parent
// registration and its normalized lineage tables. Feature values are not
// included; callers follow the recorded Parquet paths and hashes.
type FeatureArtifactCatalogArtifact struct {
	ArtifactID                 string                            `json:"artifact_id"`
	ArtifactVersion            string                            `json:"artifact_version"`
	FeatureSet                 string                            `json:"feature_set"`
	FeatureSetVersion          string                            `json:"feature_set_version"`
	RegistrySHA256             string                            `json:"registry_sha256"`
	GeneratorVersion           string                            `json:"generator_version"`
	GitCommit                  string                            `json:"git_commit"`
	DecisionStart              time.Time                         `json:"decision_start"`
	DecisionEnd                time.Time                         `json:"decision_end"`
	UniverseFingerprint        string                            `json:"universe_fingerprint"`
	InputFingerprint           string                            `json:"input_fingerprint"`
	OutputManifestPath         string                            `json:"output_manifest_path"`
	OutputManifestSHA256       string                            `json:"output_manifest_sha256"`
	CalendarDataSourceID       string                            `json:"calendar_data_source_id"`
	CalendarMIC                string                            `json:"calendar_mic"`
	CalendarVersion            string                            `json:"calendar_version"`
	CalendarSessionFingerprint string                            `json:"calendar_session_fingerprint"`
	CalendarAvailableAt        time.Time                         `json:"calendar_available_at"`
	DecisionClockPolicy        string                            `json:"decision_clock_policy"`
	RowCount                   int64                             `json:"row_count"`
	RequestedPartitions        int64                             `json:"requested_partitions"`
	AcceptedPartitions         int64                             `json:"accepted_partitions"`
	RejectedPartitions         int64                             `json:"rejected_partitions"`
	Status                     string                            `json:"status"`
	CreatedAt                  time.Time                         `json:"created_at"`
	RegisteredAt               time.Time                         `json:"registered_at"`
	DecisionPointCount         int64                             `json:"decision_point_count"`
	UniverseCount              int64                             `json:"universe_count"`
	InputReferenceCount        int64                             `json:"input_reference_count"`
	InputFitnessCount          int64                             `json:"input_fitness_count"`
	ExpectedPartitions         int64                             `json:"expected_partitions"`
	CoverageStatus             string                            `json:"coverage_status"`
	PartitionGridConsistent    bool                              `json:"partition_grid_consistent"`
	RowCountConsistent         bool                              `json:"row_count_consistent"`
	Issues                     []string                          `json:"issues"`
	DecisionCoverage           []FeatureArtifactDecisionCoverage `json:"decision_coverage"`
	DecisionPoints             []FeatureArtifactDecisionPoint    `json:"decision_points"`
	Universe                   []FeatureArtifactUniverseMember   `json:"universe"`
	InputFitness               []FeatureArtifactInputFitness     `json:"input_fitness"`
	InputRefs                  []FeatureArtifactInputRef         `json:"input_refs"`
	Partitions                 []FeatureArtifactPartition        `json:"partitions"`
}

type FeatureArtifactCatalogSummary struct {
	ArtifactCount         int   `json:"artifact_count"`
	CompleteArtifacts     int   `json:"complete_artifacts"`
	PartialArtifacts      int   `json:"partial_artifacts"`
	EmptyArtifacts        int   `json:"empty_artifacts"`
	InconsistentArtifacts int   `json:"inconsistent_artifacts"`
	DecisionPointCount    int64 `json:"decision_point_count"`
	UniverseMemberCount   int64 `json:"universe_member_count"`
	InputReferenceCount   int64 `json:"input_reference_count"`
	InputFitnessCount     int64 `json:"input_fitness_count"`
	RequestedPartitions   int64 `json:"requested_partitions"`
	AcceptedPartitions    int64 `json:"accepted_partitions"`
	CatalogedPartitions   int64 `json:"cataloged_partitions"`
	RejectedPartitions    int64 `json:"rejected_partitions"`
	RowCount              int64 `json:"row_count"`
}

type FeatureArtifactCatalogReport struct {
	Version     int                              `json:"version"`
	GeneratedAt time.Time                        `json:"generated_at"`
	Filters     FeatureArtifactCatalogFilter     `json:"filters"`
	Summary     FeatureArtifactCatalogSummary    `json:"summary"`
	Artifacts   []FeatureArtifactCatalogArtifact `json:"artifacts"`
}

type featureArtifactCatalogParent struct {
	ArtifactID                 string
	ArtifactVersion            string
	FeatureSet                 string
	FeatureSetVersion          string
	RegistrySHA256             string
	GeneratorVersion           string
	GitCommit                  string
	DecisionStart              time.Time
	DecisionEnd                time.Time
	UniverseFingerprint        string
	InputFingerprint           string
	OutputManifestPath         string
	OutputManifestSHA256       string
	CalendarDataSourceID       string
	CalendarMIC                string
	CalendarVersion            string
	CalendarSessionFingerprint string
	CalendarAvailableAt        time.Time
	DecisionClockPolicy        string
	RowCount                   int64
	RequestedPartitions        int64
	AcceptedPartitions         int64
	RejectedPartitions         int64
	Status                     string
	CreatedAt                  time.Time
	RegisteredAt               time.Time
}

type featureArtifactCatalogDetails struct {
	DecisionPoints []FeatureArtifactDecisionPoint
	Universe       []FeatureArtifactUniverseMember
	InputFitness   []FeatureArtifactInputFitness
	InputRefs      []FeatureArtifactInputRef
	Partitions     []FeatureArtifactPartition
}

const featureArtifactCatalogParentSelect = `
SELECT
    fa.artifact_id::text, fa.artifact_version, fa.feature_set, fa.feature_set_version,
    fa.registry_sha256, fa.generator_version, fa.git_commit,
    fa.decision_start, fa.decision_end, fa.universe_fingerprint, fa.input_fingerprint,
    fa.output_manifest_path, fa.output_manifest_sha256,
    fa.calendar_data_source_id::text, fa.calendar_mic, fa.calendar_version,
    fa.calendar_session_fingerprint, fa.calendar_available_at,
    fa.decision_clock_policy, fa.row_count, fa.requested_partitions,
    fa.accepted_partitions, fa.rejected_partitions, fa.status,
    fa.created_at, fa.registered_at
FROM feature_artifacts fa`

// ListFeatureArtifactCatalog reads the catalog and all registered lineage in a
// single read-only repeatable-read transaction. It never reads feature values.
func (r *Repository) ListFeatureArtifactCatalog(ctx context.Context, filter FeatureArtifactCatalogFilter) ([]FeatureArtifactCatalogArtifact, error) {
	if r == nil {
		return nil, errors.New("PostgreSQL metadata repository is required for feature catalog reporting")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalized, err := normalizeFeatureArtifactCatalogFilter(filter)
	if err != nil {
		return nil, err
	}
	query, args := featureArtifactCatalogParentQuery(normalized)
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	parents, err := queryFeatureArtifactCatalogParents(ctx, tx, query, args)
	if err != nil {
		return nil, err
	}
	artifacts := make([]FeatureArtifactCatalogArtifact, 0, len(parents))
	for _, parent := range parents {
		details, err := queryFeatureArtifactCatalogDetails(ctx, tx, parent.ArtifactID)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, summarizeFeatureArtifactCatalogArtifact(parent, details))
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return artifacts, nil
}

func normalizeFeatureArtifactCatalogFilter(filter FeatureArtifactCatalogFilter) (FeatureArtifactCatalogFilter, error) {
	filter.ArtifactID = strings.TrimSpace(filter.ArtifactID)
	filter.FeatureSet = strings.TrimSpace(filter.FeatureSet)
	filter.FeatureSetVersion = strings.TrimSpace(filter.FeatureSetVersion)
	if filter.ArtifactID != "" {
		if err := requireFeatureArtifactUUID(filter.ArtifactID, "artifact_id"); err != nil {
			return FeatureArtifactCatalogFilter{}, err
		}
	}
	if filter.FeatureSet != "" {
		if err := requireFeatureArtifactIdentifier(filter.FeatureSet, "feature_set"); err != nil {
			return FeatureArtifactCatalogFilter{}, err
		}
	}
	if filter.FeatureSetVersion != "" {
		if err := requireFeatureArtifactVersion(filter.FeatureSetVersion, "feature_set_version"); err != nil {
			return FeatureArtifactCatalogFilter{}, err
		}
	}
	return filter, nil
}

func featureArtifactCatalogParentQuery(filter FeatureArtifactCatalogFilter) (string, []any) {
	query := featureArtifactCatalogParentSelect
	args := make([]any, 0, 3)
	conditions := make([]string, 0, 3)
	if filter.ArtifactID != "" {
		args = append(args, filter.ArtifactID)
		conditions = append(conditions, fmt.Sprintf("fa.artifact_id = $%d::uuid", len(args)))
	}
	if filter.FeatureSet != "" {
		args = append(args, filter.FeatureSet)
		conditions = append(conditions, fmt.Sprintf("fa.feature_set = $%d", len(args)))
	}
	if filter.FeatureSetVersion != "" {
		args = append(args, filter.FeatureSetVersion)
		conditions = append(conditions, fmt.Sprintf("fa.feature_set_version = $%d", len(args)))
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY fa.feature_set, fa.feature_set_version, fa.decision_start, fa.artifact_id"
	return query, args
}

func queryFeatureArtifactCatalogParents(ctx context.Context, tx pgx.Tx, query string, args []any) ([]featureArtifactCatalogParent, error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	parents := make([]featureArtifactCatalogParent, 0)
	for rows.Next() {
		var parent featureArtifactCatalogParent
		if err := rows.Scan(
			&parent.ArtifactID, &parent.ArtifactVersion, &parent.FeatureSet, &parent.FeatureSetVersion,
			&parent.RegistrySHA256, &parent.GeneratorVersion, &parent.GitCommit,
			&parent.DecisionStart, &parent.DecisionEnd, &parent.UniverseFingerprint, &parent.InputFingerprint,
			&parent.OutputManifestPath, &parent.OutputManifestSHA256,
			&parent.CalendarDataSourceID, &parent.CalendarMIC, &parent.CalendarVersion,
			&parent.CalendarSessionFingerprint, &parent.CalendarAvailableAt,
			&parent.DecisionClockPolicy, &parent.RowCount, &parent.RequestedPartitions,
			&parent.AcceptedPartitions, &parent.RejectedPartitions, &parent.Status,
			&parent.CreatedAt, &parent.RegisteredAt,
		); err != nil {
			return nil, err
		}
		parents = append(parents, parent)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return parents, nil
}

func queryFeatureArtifactCatalogDetails(ctx context.Context, tx pgx.Tx, artifactID string) (featureArtifactCatalogDetails, error) {
	details := featureArtifactCatalogDetails{
		DecisionPoints: make([]FeatureArtifactDecisionPoint, 0),
		Universe:       make([]FeatureArtifactUniverseMember, 0),
		InputFitness:   make([]FeatureArtifactInputFitness, 0),
		InputRefs:      make([]FeatureArtifactInputRef, 0),
		Partitions:     make([]FeatureArtifactPartition, 0),
	}
	rows, err := tx.Query(ctx, `SELECT ordinal, decision_at FROM feature_artifact_decision_points WHERE artifact_id=$1::uuid ORDER BY ordinal`, artifactID)
	if err != nil {
		return details, err
	}
	for rows.Next() {
		var point FeatureArtifactDecisionPoint
		if err := rows.Scan(&point.Ordinal, &point.DecisionAt); err != nil {
			rows.Close()
			return details, err
		}
		point.DecisionAt = point.DecisionAt.UTC()
		details.DecisionPoints = append(details.DecisionPoints, point)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return details, err
	}
	rows.Close()

	rows, err = tx.Query(ctx, `SELECT ordinal, security_id::text FROM feature_artifact_universe_members WHERE artifact_id=$1::uuid ORDER BY ordinal`, artifactID)
	if err != nil {
		return details, err
	}
	for rows.Next() {
		var member FeatureArtifactUniverseMember
		if err := rows.Scan(&member.Ordinal, &member.SecurityID); err != nil {
			rows.Close()
			return details, err
		}
		details.Universe = append(details.Universe, member)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return details, err
	}
	rows.Close()

	rows, err = tx.Query(ctx, `SELECT input_kind, path, sha256 FROM feature_artifact_input_refs WHERE artifact_id=$1::uuid ORDER BY input_kind, path`, artifactID)
	if err != nil {
		return details, err
	}
	for rows.Next() {
		var input FeatureArtifactInputRef
		var kind string
		if err := rows.Scan(&kind, &input.Path, &input.SHA256); err != nil {
			rows.Close()
			return details, err
		}
		input.Kind = FeatureArtifactInputKind(kind)
		details.InputRefs = append(details.InputRefs, input)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return details, err
	}
	rows.Close()

	rows, err = tx.Query(ctx, `SELECT dataset, historical_fitness, availability_policy FROM feature_artifact_input_fitness WHERE artifact_id=$1::uuid ORDER BY dataset`, artifactID)
	if err != nil {
		return details, err
	}
	for rows.Next() {
		var fitness FeatureArtifactInputFitness
		if err := rows.Scan(&fitness.Dataset, &fitness.HistoricalFitness, &fitness.AvailabilityPolicy); err != nil {
			rows.Close()
			return details, err
		}
		details.InputFitness = append(details.InputFitness, fitness)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return details, err
	}
	rows.Close()

	rows, err = tx.Query(ctx, `SELECT security_id::text, decision_at, child_artifact_id::text, manifest_path, manifest_sha256, part_path, part_sha256, row_count FROM feature_artifact_partitions WHERE artifact_id=$1::uuid ORDER BY decision_at, security_id`, artifactID)
	if err != nil {
		return details, err
	}
	for rows.Next() {
		var partition FeatureArtifactPartition
		if err := rows.Scan(
			&partition.SecurityID, &partition.DecisionAt, &partition.ChildArtifactID,
			&partition.ManifestPath, &partition.ManifestSHA256, &partition.PartPath,
			&partition.PartSHA256, &partition.RowCount,
		); err != nil {
			rows.Close()
			return details, err
		}
		partition.DecisionAt = partition.DecisionAt.UTC()
		details.Partitions = append(details.Partitions, partition)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return details, err
	}
	rows.Close()
	return details, nil
}

func summarizeFeatureArtifactCatalogArtifact(parent featureArtifactCatalogParent, details featureArtifactCatalogDetails) FeatureArtifactCatalogArtifact {
	artifact := FeatureArtifactCatalogArtifact{
		ArtifactID:                 parent.ArtifactID,
		ArtifactVersion:            parent.ArtifactVersion,
		FeatureSet:                 parent.FeatureSet,
		FeatureSetVersion:          parent.FeatureSetVersion,
		RegistrySHA256:             parent.RegistrySHA256,
		GeneratorVersion:           parent.GeneratorVersion,
		GitCommit:                  parent.GitCommit,
		DecisionStart:              parent.DecisionStart.UTC(),
		DecisionEnd:                parent.DecisionEnd.UTC(),
		UniverseFingerprint:        parent.UniverseFingerprint,
		InputFingerprint:           parent.InputFingerprint,
		OutputManifestPath:         parent.OutputManifestPath,
		OutputManifestSHA256:       parent.OutputManifestSHA256,
		CalendarDataSourceID:       parent.CalendarDataSourceID,
		CalendarMIC:                parent.CalendarMIC,
		CalendarVersion:            parent.CalendarVersion,
		CalendarSessionFingerprint: parent.CalendarSessionFingerprint,
		CalendarAvailableAt:        parent.CalendarAvailableAt.UTC(),
		DecisionClockPolicy:        parent.DecisionClockPolicy,
		RowCount:                   parent.RowCount,
		RequestedPartitions:        parent.RequestedPartitions,
		AcceptedPartitions:         parent.AcceptedPartitions,
		RejectedPartitions:         parent.RejectedPartitions,
		Status:                     parent.Status,
		CreatedAt:                  parent.CreatedAt.UTC(),
		RegisteredAt:               parent.RegisteredAt.UTC(),
		DecisionPoints:             details.DecisionPoints,
		Universe:                   details.Universe,
		InputFitness:               details.InputFitness,
		InputRefs:                  details.InputRefs,
		Partitions:                 details.Partitions,
		Issues:                     make([]string, 0),
		DecisionCoverage:           make([]FeatureArtifactDecisionCoverage, 0),
	}
	artifact.DecisionPointCount = int64(len(artifact.DecisionPoints))
	artifact.UniverseCount = int64(len(artifact.Universe))
	artifact.InputReferenceCount = int64(len(artifact.InputRefs))
	artifact.InputFitnessCount = int64(len(artifact.InputFitness))
	artifact.ExpectedPartitions = artifact.DecisionPointCount * artifact.UniverseCount
	artifact.PartitionGridConsistent = artifact.ExpectedPartitions == artifact.RequestedPartitions
	artifact.RowCountConsistent = artifact.AcceptedPartitions == int64(len(artifact.Partitions))

	if len(artifact.DecisionPoints) == 0 {
		appendFeatureArtifactCatalogIssue(&artifact, "catalog has no decision points")
	}
	if len(artifact.Universe) == 0 {
		appendFeatureArtifactCatalogIssue(&artifact, "catalog has no universe members")
	}
	if len(artifact.InputFitness) == 0 {
		appendFeatureArtifactCatalogIssue(&artifact, "catalog has no input-fitness labels")
	}
	if !artifact.PartitionGridConsistent {
		appendFeatureArtifactCatalogIssue(&artifact, "requested_partitions does not equal decision_points multiplied by universe members")
	}
	if artifact.RequestedPartitions != artifact.AcceptedPartitions+artifact.RejectedPartitions {
		appendFeatureArtifactCatalogIssue(&artifact, "partition counts are inconsistent")
	}

	decisionIndexes := make(map[time.Time]int, len(artifact.DecisionPoints))
	for index, point := range artifact.DecisionPoints {
		decisionIndexes[point.DecisionAt.UTC()] = index
		artifact.DecisionCoverage = append(artifact.DecisionCoverage, FeatureArtifactDecisionCoverage{
			DecisionAt:         point.DecisionAt.UTC(),
			ExpectedPartitions: artifact.UniverseCount,
		})
	}
	securityIndexes := make(map[string]struct{}, len(artifact.Universe))
	for _, member := range artifact.Universe {
		if _, exists := securityIndexes[member.SecurityID]; exists {
			appendFeatureArtifactCatalogIssue(&artifact, fmt.Sprintf("universe security %s is duplicated", member.SecurityID))
		}
		securityIndexes[member.SecurityID] = struct{}{}
	}
	var partitionRows int64
	partitionKeys := make(map[string]struct{}, len(artifact.Partitions))
	for _, partition := range artifact.Partitions {
		partition.DecisionAt = partition.DecisionAt.UTC()
		if _, exists := securityIndexes[partition.SecurityID]; !exists {
			appendFeatureArtifactCatalogIssue(&artifact, fmt.Sprintf("partition security %s is outside the universe", partition.SecurityID))
		}
		decisionIndex, exists := decisionIndexes[partition.DecisionAt]
		if !exists {
			appendFeatureArtifactCatalogIssue(&artifact, fmt.Sprintf("partition decision_at %s is outside the schedule", partition.DecisionAt.Format(time.RFC3339Nano)))
		} else {
			coverage := &artifact.DecisionCoverage[decisionIndex]
			coverage.AcceptedPartitions++
			coverage.AcceptedRowCount += partition.RowCount
		}
		key := partition.SecurityID + "\x00" + partition.DecisionAt.Format(time.RFC3339Nano)
		if _, exists := partitionKeys[key]; exists {
			appendFeatureArtifactCatalogIssue(&artifact, fmt.Sprintf("partition %s is duplicated", key))
		}
		partitionKeys[key] = struct{}{}
		partitionRows += partition.RowCount
	}
	if partitionRows != artifact.RowCount {
		artifact.RowCountConsistent = false
		appendFeatureArtifactCatalogIssue(&artifact, "row_count does not equal the sum of cataloged partition rows")
	}
	for index := range artifact.DecisionCoverage {
		coverage := &artifact.DecisionCoverage[index]
		if coverage.ExpectedPartitions >= coverage.AcceptedPartitions {
			coverage.UnaccountedPartitions = coverage.ExpectedPartitions - coverage.AcceptedPartitions
		}
	}
	if !artifact.RowCountConsistent {
		appendFeatureArtifactCatalogIssue(&artifact, "accepted partition lineage is not row-count consistent")
	}
	sort.Strings(artifact.Issues)
	if len(artifact.Issues) > 0 {
		artifact.CoverageStatus = "inconsistent"
	} else if artifact.AcceptedPartitions == 0 {
		artifact.CoverageStatus = "empty"
	} else if artifact.AcceptedPartitions == artifact.RequestedPartitions {
		artifact.CoverageStatus = "complete"
	} else {
		artifact.CoverageStatus = "partial"
	}
	return artifact
}

func appendFeatureArtifactCatalogIssue(artifact *FeatureArtifactCatalogArtifact, issue string) {
	for _, existing := range artifact.Issues {
		if existing == issue {
			return
		}
	}
	artifact.Issues = append(artifact.Issues, issue)
}

// BuildFeatureArtifactCatalogReport assembles a stable read-only report from
// catalog rows. The caller supplies the timestamp so tests and operators can
// keep report generation deterministic when needed.
func BuildFeatureArtifactCatalogReport(generatedAt time.Time, filter FeatureArtifactCatalogFilter, artifacts []FeatureArtifactCatalogArtifact) FeatureArtifactCatalogReport {
	if generatedAt.IsZero() {
		generatedAt = time.Now().UTC()
	} else {
		generatedAt = generatedAt.UTC()
	}
	ordered := make([]FeatureArtifactCatalogArtifact, len(artifacts))
	copy(ordered, artifacts)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].FeatureSet != ordered[j].FeatureSet {
			return ordered[i].FeatureSet < ordered[j].FeatureSet
		}
		if ordered[i].FeatureSetVersion != ordered[j].FeatureSetVersion {
			return ordered[i].FeatureSetVersion < ordered[j].FeatureSetVersion
		}
		return ordered[i].ArtifactID < ordered[j].ArtifactID
	})
	summary := FeatureArtifactCatalogSummary{ArtifactCount: len(ordered)}
	for _, artifact := range ordered {
		summary.DecisionPointCount += artifact.DecisionPointCount
		summary.UniverseMemberCount += artifact.UniverseCount
		summary.InputReferenceCount += artifact.InputReferenceCount
		summary.InputFitnessCount += artifact.InputFitnessCount
		summary.RequestedPartitions += artifact.RequestedPartitions
		summary.AcceptedPartitions += artifact.AcceptedPartitions
		summary.CatalogedPartitions += int64(len(artifact.Partitions))
		summary.RejectedPartitions += artifact.RejectedPartitions
		summary.RowCount += artifact.RowCount
		switch artifact.CoverageStatus {
		case "complete":
			summary.CompleteArtifacts++
		case "partial":
			summary.PartialArtifacts++
		case "empty":
			summary.EmptyArtifacts++
		case "inconsistent":
			summary.InconsistentArtifacts++
		}
	}
	return FeatureArtifactCatalogReport{
		Version:     FeatureArtifactCatalogReportVersion,
		GeneratedAt: generatedAt,
		Filters:     filter,
		Summary:     summary,
		Artifacts:   ordered,
	}
}
