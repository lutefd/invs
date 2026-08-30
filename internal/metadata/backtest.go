package metadata

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const BacktestSchemaVersion = "1.0.0"

type BacktestExperiment struct {
	ExperimentID          string          `json:"experiment_id"`
	ExperimentSHA256      string          `json:"experiment_sha256"`
	SchemaVersion         string          `json:"schema_version"`
	StrategyName          string          `json:"strategy_name"`
	StrategyVersion       string          `json:"strategy_version"`
	StrategyGitCommit     string          `json:"strategy_git_commit"`
	StrategyParameters    json.RawMessage `json:"strategy_parameters"`
	UniverseID            string          `json:"universe_id"`
	UniverseVersion       string          `json:"universe_version"`
	MembershipFingerprint string          `json:"membership_fingerprint"`
	BaseCurrency          string          `json:"base_currency"`
	ReportingCurrency     string          `json:"reporting_currency,omitempty"`
	StartDate             string          `json:"start_date"`
	EndDate               string          `json:"end_date"`
	ValidationPartition   string          `json:"validation_partition"`
	BenchmarkSecurityID   string          `json:"benchmark_security_id"`
	BenchmarkCurrency     string          `json:"benchmark_currency"`
	DecisionPolicy        string          `json:"decision_policy"`
	CostPolicyVersion     string          `json:"cost_policy_version"`
	RebalanceFrequency    string          `json:"rebalance_frequency"`
	InputFingerprint      string          `json:"input_fingerprint"`
	SpecPath              string          `json:"spec_path"`
	EngineVersion         string          `json:"engine_version"`
	CreatedAt             time.Time       `json:"created_at,omitempty"`
	RegisteredAt          time.Time       `json:"registered_at,omitempty"`
}

type BacktestExperimentInput struct {
	Ordinal           int       `json:"ordinal,omitempty"`
	Kind              string    `json:"kind"`
	ArtifactID        string    `json:"artifact_id"`
	Path              string    `json:"path"`
	SHA256            string    `json:"sha256"`
	AvailableAt       time.Time `json:"available_at"`
	HistoricalFitness string    `json:"historical_fitness"`
}

type BacktestExperimentRegistration struct {
	Experiment BacktestExperiment        `json:"experiment"`
	Inputs     []BacktestExperimentInput `json:"inputs"`
}

type BacktestExperimentRegistrationResult struct {
	ExperimentID       string `json:"experiment_id"`
	RegistrationSHA256 string `json:"registration_sha256"`
	AlreadyPresent     bool   `json:"already_present"`
}

type BacktestRunRegistration struct {
	RunID             string          `json:"run_id"`
	ExperimentID      string          `json:"experiment_id"`
	Attempt           int             `json:"attempt"`
	EngineVersion     string          `json:"engine_version"`
	Environment       json.RawMessage `json:"environment"`
	EnvironmentSHA256 string          `json:"environment_sha256,omitempty"`
	HoldoutAttempted  bool            `json:"holdout_attempted"`
	StartedAt         time.Time       `json:"started_at"`
	CreatedAt         time.Time       `json:"created_at,omitempty"`
	RecordHash        string          `json:"record_hash,omitempty"`
}

type BacktestRunStartResult struct {
	RunID          string `json:"run_id"`
	ExperimentID   string `json:"experiment_id"`
	Attempt        int    `json:"attempt"`
	RecordHash     string `json:"record_hash"`
	StartEventHash string `json:"start_event_hash"`
	AlreadyPresent bool   `json:"already_present"`
}

type BacktestRunEvent struct {
	RunID                string    `json:"run_id"`
	Sequence             int       `json:"sequence"`
	Status               string    `json:"status"`
	EventAt              time.Time `json:"event_at"`
	ResultID             string    `json:"result_id,omitempty"`
	ResultManifestPath   string    `json:"result_manifest_path,omitempty"`
	ResultManifestSHA256 string    `json:"result_manifest_sha256,omitempty"`
	FailureCode          string    `json:"failure_code,omitempty"`
	FailureMessage       string    `json:"failure_message,omitempty"`
	RecordHash           string    `json:"record_hash,omitempty"`
}

type BacktestRunEventResult struct {
	RunID          string `json:"run_id"`
	Sequence       int    `json:"sequence"`
	RecordHash     string `json:"record_hash"`
	AlreadyPresent bool   `json:"already_present"`
}

type BacktestRunReport struct {
	RunID                string          `json:"run_id"`
	ExperimentID         string          `json:"experiment_id"`
	Attempt              int             `json:"attempt"`
	EngineVersion        string          `json:"engine_version"`
	Environment          json.RawMessage `json:"environment"`
	EnvironmentSHA256    string          `json:"environment_sha256"`
	HoldoutAttempted     bool            `json:"holdout_attempted"`
	StartedAt            time.Time       `json:"started_at"`
	CreatedAt            time.Time       `json:"created_at"`
	Status               string          `json:"status"`
	LastEventSequence    int             `json:"last_event_sequence"`
	LastEventAt          time.Time       `json:"last_event_at"`
	ResultID             string          `json:"result_id,omitempty"`
	ResultManifestPath   string          `json:"result_manifest_path,omitempty"`
	ResultManifestSHA256 string          `json:"result_manifest_sha256,omitempty"`
	FailureCode          string          `json:"failure_code,omitempty"`
	FailureMessage       string          `json:"failure_message,omitempty"`
}

type BacktestExperimentReport struct {
	SchemaVersion string                    `json:"schema_version"`
	Experiment    BacktestExperiment        `json:"experiment"`
	Inputs        []BacktestExperimentInput `json:"inputs"`
	Runs          []BacktestRunReport       `json:"runs"`
}

var (
	backtestIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)
	backtestVersionPattern    = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	backtestGitPattern        = regexp.MustCompile(`^(unknown|[0-9a-f]{40})$`)
	backtestCurrencyPattern   = regexp.MustCompile(`^[A-Z]{3}$`)
	backtestDatePattern       = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	backtestHashPattern       = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

const insertBacktestExperimentSQL = `
INSERT INTO backtest_experiments (
    experiment_id, experiment_sha256, schema_version, strategy_name,
    strategy_version, strategy_git_commit, strategy_parameters, universe_id,
    universe_version, membership_fingerprint, base_currency, reporting_currency,
    start_date, end_date, validation_partition, benchmark_security_id,
    benchmark_currency, decision_policy, cost_policy_version, rebalance_frequency,
    input_fingerprint, spec_path, engine_version, created_at, registration_sha256
) VALUES (
    $1::uuid, $2, $3, $4,
    $5, $6, $7::jsonb, $8::uuid,
    $9, $10, $11, NULLIF($12, ''),
    $13::date, $14::date, $15, $16::uuid,
    $17, $18, $19, $20,
    $21, $22, $23, $24, $25
)
ON CONFLICT (experiment_id) DO NOTHING
RETURNING registration_sha256`

const selectBacktestExperimentRegistrationSQL = `
SELECT registration_sha256
FROM backtest_experiments
WHERE experiment_id = $1::uuid`

const insertBacktestRunSQL = `
INSERT INTO backtest_runs (
    run_id, experiment_id, attempt, engine_version, environment,
    environment_sha256, holdout_attempted, started_at, created_at, record_hash
) VALUES (
    $1::uuid, $2::uuid, $3, $4, $5::jsonb,
    $6, $7, $8, $9, $10
)
ON CONFLICT (run_id) DO NOTHING
RETURNING record_hash`

const insertBacktestRunEventSQL = `
INSERT INTO backtest_run_events (
    run_id, sequence, status, event_at, result_id,
    result_manifest_path, result_manifest_sha256, failure_code,
    failure_message, record_hash
) VALUES (
    $1::uuid, $2, $3, $4, NULLIF($5, '')::uuid,
    NULLIF($6, ''), NULLIF($7, ''), NULLIF($8, ''),
    NULLIF($9, ''), $10
)
RETURNING record_hash`

func requireBacktestUUID(value, label string) error {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.String() != value {
		return fmt.Errorf("%s must be a lower-case canonical UUID", label)
	}
	return nil
}

func requireBacktestHash(value, label string) error {
	if !backtestHashPattern.MatchString(value) {
		return fmt.Errorf("%s must be a lower-case SHA-256", label)
	}
	return nil
}

func requireBacktestIdentifier(value, label string) error {
	if !backtestIdentifierPattern.MatchString(value) {
		return fmt.Errorf("%s %q is invalid", label, value)
	}
	return nil
}

func requireBacktestVersion(value, label string) error {
	if !backtestVersionPattern.MatchString(value) {
		return fmt.Errorf("%s %q is not a semantic version", label, value)
	}
	return nil
}

func requireBacktestPath(value, label string) error {
	if value == "" || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "\\") || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("%s must be a safe relative path", label)
	}
	for _, component := range strings.Split(strings.ReplaceAll(value, "\\", "/"), "/") {
		if component == "" || component == "." || component == ".." {
			return fmt.Errorf("%s contains an unsafe path component", label)
		}
	}
	return nil
}

func normalizeBacktestObject(value json.RawMessage, label string) (json.RawMessage, error) {
	if len(bytes.TrimSpace(value)) == 0 {
		return nil, fmt.Errorf("%s must be a non-empty JSON object", label)
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	decoded, err := decodeBacktestJSONValue(decoder)
	if err != nil {
		return nil, fmt.Errorf("%s must be valid JSON: %w", label, err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("%s must contain one JSON value", label)
		}
		return nil, fmt.Errorf("%s has trailing JSON: %w", label, err)
	}
	object, ok := decoded.(map[string]any)
	if !ok || len(object) == 0 {
		return nil, fmt.Errorf("%s must be a non-empty JSON object", label)
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("canonicalize %s: %w", label, err)
	}
	return canonical, nil
}

func decodeBacktestJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch delimiter := token.(type) {
	case json.Delim:
		switch delimiter {
		case '{':
			object := map[string]any{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, errors.New("object key is not a string")
				}
				if _, exists := object[key]; exists {
					return nil, fmt.Errorf("duplicate JSON key %q", key)
				}
				item, err := decodeBacktestJSONValue(decoder)
				if err != nil {
					return nil, err
				}
				object[key] = item
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return object, nil
		case '[':
			items := []any{}
			for decoder.More() {
				item, err := decodeBacktestJSONValue(decoder)
				if err != nil {
					return nil, err
				}
				items = append(items, item)
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return items, nil
		default:
			return nil, fmt.Errorf("unexpected JSON delimiter %q", delimiter)
		}
	default:
		return token, nil
	}
}

func canonicalBacktestHash(value any, label string) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("canonicalize %s: %w", label, err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

type backtestInputIdentity struct {
	Kind              string    `json:"kind"`
	ArtifactID        string    `json:"artifact_id"`
	Path              string    `json:"path"`
	SHA256            string    `json:"sha256"`
	AvailableAt       time.Time `json:"available_at"`
	HistoricalFitness string    `json:"historical_fitness"`
}

func backtestInputFingerprint(inputs []BacktestExperimentInput) (string, error) {
	identities := make([]backtestInputIdentity, len(inputs))
	for index, input := range inputs {
		identities[index] = backtestInputIdentity{
			Kind: input.Kind, ArtifactID: input.ArtifactID, Path: input.Path,
			SHA256: input.SHA256, AvailableAt: input.AvailableAt.UTC(),
			HistoricalFitness: input.HistoricalFitness,
		}
	}
	return canonicalBacktestHash(identities, "backtest input references")
}

type backtestExperimentIdentity struct {
	ExperimentID          string                  `json:"experiment_id"`
	ExperimentSHA256      string                  `json:"experiment_sha256"`
	SchemaVersion         string                  `json:"schema_version"`
	StrategyName          string                  `json:"strategy_name"`
	StrategyVersion       string                  `json:"strategy_version"`
	StrategyGitCommit     string                  `json:"strategy_git_commit"`
	StrategyParameters    json.RawMessage         `json:"strategy_parameters"`
	UniverseID            string                  `json:"universe_id"`
	UniverseVersion       string                  `json:"universe_version"`
	MembershipFingerprint string                  `json:"membership_fingerprint"`
	BaseCurrency          string                  `json:"base_currency"`
	ReportingCurrency     string                  `json:"reporting_currency,omitempty"`
	StartDate             string                  `json:"start_date"`
	EndDate               string                  `json:"end_date"`
	ValidationPartition   string                  `json:"validation_partition"`
	BenchmarkSecurityID   string                  `json:"benchmark_security_id"`
	BenchmarkCurrency     string                  `json:"benchmark_currency"`
	DecisionPolicy        string                  `json:"decision_policy"`
	CostPolicyVersion     string                  `json:"cost_policy_version"`
	RebalanceFrequency    string                  `json:"rebalance_frequency"`
	InputFingerprint      string                  `json:"input_fingerprint"`
	SpecPath              string                  `json:"spec_path"`
	EngineVersion         string                  `json:"engine_version"`
	Inputs                []backtestInputIdentity `json:"inputs"`
}

func (registration BacktestExperimentRegistration) normalized() (BacktestExperimentRegistration, error) {
	experiment := registration.Experiment
	experiment.ExperimentID = strings.TrimSpace(experiment.ExperimentID)
	experiment.ExperimentSHA256 = strings.TrimSpace(experiment.ExperimentSHA256)
	experiment.SchemaVersion = strings.TrimSpace(experiment.SchemaVersion)
	experiment.StrategyName = strings.TrimSpace(experiment.StrategyName)
	experiment.StrategyVersion = strings.TrimSpace(experiment.StrategyVersion)
	experiment.StrategyGitCommit = strings.TrimSpace(experiment.StrategyGitCommit)
	experiment.UniverseID = strings.TrimSpace(experiment.UniverseID)
	experiment.UniverseVersion = strings.TrimSpace(experiment.UniverseVersion)
	experiment.MembershipFingerprint = strings.TrimSpace(experiment.MembershipFingerprint)
	experiment.BaseCurrency = strings.TrimSpace(experiment.BaseCurrency)
	experiment.ReportingCurrency = strings.TrimSpace(experiment.ReportingCurrency)
	experiment.StartDate = strings.TrimSpace(experiment.StartDate)
	experiment.EndDate = strings.TrimSpace(experiment.EndDate)
	experiment.ValidationPartition = strings.TrimSpace(experiment.ValidationPartition)
	experiment.BenchmarkSecurityID = strings.TrimSpace(experiment.BenchmarkSecurityID)
	experiment.BenchmarkCurrency = strings.TrimSpace(experiment.BenchmarkCurrency)
	experiment.DecisionPolicy = strings.TrimSpace(experiment.DecisionPolicy)
	experiment.CostPolicyVersion = strings.TrimSpace(experiment.CostPolicyVersion)
	experiment.RebalanceFrequency = strings.TrimSpace(experiment.RebalanceFrequency)
	experiment.InputFingerprint = strings.TrimSpace(experiment.InputFingerprint)
	experiment.SpecPath = strings.TrimSpace(experiment.SpecPath)
	experiment.EngineVersion = strings.TrimSpace(experiment.EngineVersion)
	if experiment.SchemaVersion == "" {
		experiment.SchemaVersion = BacktestSchemaVersion
	}
	if err := requireBacktestUUID(experiment.ExperimentID, "experiment.experiment_id"); err != nil {
		return BacktestExperimentRegistration{}, err
	}
	if err := requireBacktestHash(experiment.ExperimentSHA256, "experiment.experiment_sha256"); err != nil {
		return BacktestExperimentRegistration{}, err
	}
	if experiment.SchemaVersion != BacktestSchemaVersion {
		return BacktestExperimentRegistration{}, fmt.Errorf("experiment.schema_version %q is unsupported", experiment.SchemaVersion)
	}
	if err := requireBacktestIdentifier(experiment.StrategyName, "experiment.strategy_name"); err != nil {
		return BacktestExperimentRegistration{}, err
	}
	if err := requireBacktestVersion(experiment.StrategyVersion, "experiment.strategy_version"); err != nil {
		return BacktestExperimentRegistration{}, err
	}
	if !backtestGitPattern.MatchString(experiment.StrategyGitCommit) {
		return BacktestExperimentRegistration{}, fmt.Errorf("experiment.strategy_git_commit %q is invalid", experiment.StrategyGitCommit)
	}
	parameters, err := normalizeBacktestObject(experiment.StrategyParameters, "experiment.strategy_parameters")
	if err != nil {
		return BacktestExperimentRegistration{}, err
	}
	experiment.StrategyParameters = parameters
	if err := requireBacktestUUID(experiment.UniverseID, "experiment.universe_id"); err != nil {
		return BacktestExperimentRegistration{}, err
	}
	if err := requireBacktestVersion(experiment.UniverseVersion, "experiment.universe_version"); err != nil {
		return BacktestExperimentRegistration{}, err
	}
	if err := requireBacktestHash(experiment.MembershipFingerprint, "experiment.membership_fingerprint"); err != nil {
		return BacktestExperimentRegistration{}, err
	}
	if !backtestCurrencyPattern.MatchString(experiment.BaseCurrency) {
		return BacktestExperimentRegistration{}, fmt.Errorf("experiment.base_currency %q is invalid", experiment.BaseCurrency)
	}
	if experiment.ReportingCurrency != "" && !backtestCurrencyPattern.MatchString(experiment.ReportingCurrency) {
		return BacktestExperimentRegistration{}, fmt.Errorf("experiment.reporting_currency %q is invalid", experiment.ReportingCurrency)
	}
	for _, item := range []struct{ value, label string }{
		{experiment.StartDate, "experiment.start_date"}, {experiment.EndDate, "experiment.end_date"},
	} {
		if !backtestDatePattern.MatchString(item.value) {
			return BacktestExperimentRegistration{}, fmt.Errorf("%s must be an ISO date", item.label)
		}
		if _, err := time.Parse("2006-01-02", item.value); err != nil {
			return BacktestExperimentRegistration{}, fmt.Errorf("%s must be an ISO date: %w", item.label, err)
		}
	}
	if experiment.StartDate > experiment.EndDate {
		return BacktestExperimentRegistration{}, errors.New("experiment.start_date must not be after experiment.end_date")
	}
	if err := requireBacktestIdentifier(experiment.ValidationPartition, "experiment.validation_partition"); err != nil {
		return BacktestExperimentRegistration{}, err
	}
	if err := requireBacktestUUID(experiment.BenchmarkSecurityID, "experiment.benchmark_security_id"); err != nil {
		return BacktestExperimentRegistration{}, err
	}
	if !backtestCurrencyPattern.MatchString(experiment.BenchmarkCurrency) {
		return BacktestExperimentRegistration{}, fmt.Errorf("experiment.benchmark_currency %q is invalid", experiment.BenchmarkCurrency)
	}
	if experiment.DecisionPolicy != "after_close_next_session_open" {
		return BacktestExperimentRegistration{}, fmt.Errorf("experiment.decision_policy %q is unsupported", experiment.DecisionPolicy)
	}
	if err := requireBacktestVersion(experiment.CostPolicyVersion, "experiment.cost_policy_version"); err != nil {
		return BacktestExperimentRegistration{}, err
	}
	if experiment.RebalanceFrequency != "once" && experiment.RebalanceFrequency != "daily" && experiment.RebalanceFrequency != "monthly" {
		return BacktestExperimentRegistration{}, fmt.Errorf("experiment.rebalance_frequency %q is unsupported", experiment.RebalanceFrequency)
	}
	if err := requireBacktestPath(experiment.SpecPath, "experiment.spec_path"); err != nil {
		return BacktestExperimentRegistration{}, err
	}
	if experiment.EngineVersion == "" {
		return BacktestExperimentRegistration{}, errors.New("experiment.engine_version is required")
	}

	inputs := append([]BacktestExperimentInput(nil), registration.Inputs...)
	if len(inputs) < 3 {
		return BacktestExperimentRegistration{}, errors.New("experiment inputs must include at least prices, calendar, and membership")
	}
	sort.Slice(inputs, func(i, j int) bool {
		if inputs[i].Kind != inputs[j].Kind {
			return inputs[i].Kind < inputs[j].Kind
		}
		return inputs[i].ArtifactID < inputs[j].ArtifactID
	})
	seenKinds := make(map[string]struct{}, len(inputs))
	for index := range inputs {
		inputs[index].Kind = strings.TrimSpace(inputs[index].Kind)
		inputs[index].ArtifactID = strings.TrimSpace(inputs[index].ArtifactID)
		inputs[index].Path = strings.TrimSpace(inputs[index].Path)
		inputs[index].SHA256 = strings.TrimSpace(inputs[index].SHA256)
		inputs[index].HistoricalFitness = strings.TrimSpace(inputs[index].HistoricalFitness)
		if inputs[index].HistoricalFitness == "" {
			inputs[index].HistoricalFitness = "backtest_safe"
		}
		if _, ok := map[string]struct{}{"prices": {}, "calendar": {}, "membership": {}, "corporate_actions": {}, "fx": {}, "feature": {}, "macro": {}}[inputs[index].Kind]; !ok {
			return BacktestExperimentRegistration{}, fmt.Errorf("experiment.inputs[%d].kind %q is unsupported", index, inputs[index].Kind)
		}
		if _, exists := seenKinds[inputs[index].Kind]; exists {
			return BacktestExperimentRegistration{}, fmt.Errorf("experiment.inputs kind %s is duplicated", inputs[index].Kind)
		}
		seenKinds[inputs[index].Kind] = struct{}{}
		if err := requireBacktestUUID(inputs[index].ArtifactID, fmt.Sprintf("experiment.inputs[%d].artifact_id", index)); err != nil {
			return BacktestExperimentRegistration{}, err
		}
		if err := requireBacktestPath(inputs[index].Path, fmt.Sprintf("experiment.inputs[%d].path", index)); err != nil {
			return BacktestExperimentRegistration{}, err
		}
		if err := requireBacktestHash(inputs[index].SHA256, fmt.Sprintf("experiment.inputs[%d].sha256", index)); err != nil {
			return BacktestExperimentRegistration{}, err
		}
		if inputs[index].HistoricalFitness != "backtest_safe" {
			return BacktestExperimentRegistration{}, fmt.Errorf("experiment.inputs[%d] is not backtest_safe", index)
		}
		if inputs[index].AvailableAt.IsZero() {
			return BacktestExperimentRegistration{}, fmt.Errorf("experiment.inputs[%d].available_at is required", index)
		}
		inputs[index].AvailableAt = inputs[index].AvailableAt.UTC()
		inputs[index].Ordinal = index
	}
	if _, ok := seenKinds["prices"]; !ok {
		return BacktestExperimentRegistration{}, errors.New("experiment.inputs must include prices")
	}
	if _, ok := seenKinds["calendar"]; !ok {
		return BacktestExperimentRegistration{}, errors.New("experiment.inputs must include calendar")
	}
	if _, ok := seenKinds["membership"]; !ok {
		return BacktestExperimentRegistration{}, errors.New("experiment.inputs must include membership")
	}
	computedInputFingerprint, err := backtestInputFingerprint(inputs)
	if err != nil {
		return BacktestExperimentRegistration{}, err
	}
	if experiment.InputFingerprint != "" && experiment.InputFingerprint != computedInputFingerprint {
		return BacktestExperimentRegistration{}, errors.New("experiment.input_fingerprint does not match input references")
	}
	experiment.InputFingerprint = computedInputFingerprint
	if experiment.CreatedAt.IsZero() {
		experiment.CreatedAt = time.Now().UTC()
	} else {
		experiment.CreatedAt = experiment.CreatedAt.UTC()
	}
	return BacktestExperimentRegistration{Experiment: experiment, Inputs: inputs}, nil
}

func backtestExperimentRegistrationSHA256(registration BacktestExperimentRegistration) (string, error) {
	identity := backtestExperimentIdentity{
		ExperimentID:          registration.Experiment.ExperimentID,
		ExperimentSHA256:      registration.Experiment.ExperimentSHA256,
		SchemaVersion:         registration.Experiment.SchemaVersion,
		StrategyName:          registration.Experiment.StrategyName,
		StrategyVersion:       registration.Experiment.StrategyVersion,
		StrategyGitCommit:     registration.Experiment.StrategyGitCommit,
		StrategyParameters:    registration.Experiment.StrategyParameters,
		UniverseID:            registration.Experiment.UniverseID,
		UniverseVersion:       registration.Experiment.UniverseVersion,
		MembershipFingerprint: registration.Experiment.MembershipFingerprint,
		BaseCurrency:          registration.Experiment.BaseCurrency,
		ReportingCurrency:     registration.Experiment.ReportingCurrency,
		StartDate:             registration.Experiment.StartDate,
		EndDate:               registration.Experiment.EndDate,
		ValidationPartition:   registration.Experiment.ValidationPartition,
		BenchmarkSecurityID:   registration.Experiment.BenchmarkSecurityID,
		BenchmarkCurrency:     registration.Experiment.BenchmarkCurrency,
		DecisionPolicy:        registration.Experiment.DecisionPolicy,
		CostPolicyVersion:     registration.Experiment.CostPolicyVersion,
		RebalanceFrequency:    registration.Experiment.RebalanceFrequency,
		InputFingerprint:      registration.Experiment.InputFingerprint,
		SpecPath:              registration.Experiment.SpecPath,
		EngineVersion:         registration.Experiment.EngineVersion,
		Inputs:                make([]backtestInputIdentity, len(registration.Inputs)),
	}
	for index, input := range registration.Inputs {
		identity.Inputs[index] = backtestInputIdentity{
			Kind: input.Kind, ArtifactID: input.ArtifactID, Path: input.Path,
			SHA256: input.SHA256, AvailableAt: input.AvailableAt.UTC(),
			HistoricalFitness: input.HistoricalFitness,
		}
	}
	return canonicalBacktestHash(identity, "backtest experiment registration")
}

func normalizeBacktestRunRegistration(run BacktestRunRegistration) (BacktestRunRegistration, error) {
	run.RunID = strings.TrimSpace(run.RunID)
	run.ExperimentID = strings.TrimSpace(run.ExperimentID)
	run.EngineVersion = strings.TrimSpace(run.EngineVersion)
	if err := requireBacktestUUID(run.RunID, "run_id"); err != nil {
		return BacktestRunRegistration{}, err
	}
	if err := requireBacktestUUID(run.ExperimentID, "experiment_id"); err != nil {
		return BacktestRunRegistration{}, err
	}
	if run.Attempt <= 0 {
		return BacktestRunRegistration{}, errors.New("attempt must be positive")
	}
	if run.EngineVersion == "" {
		return BacktestRunRegistration{}, errors.New("engine_version is required")
	}
	environment, err := normalizeBacktestObject(run.Environment, "environment")
	if err != nil {
		return BacktestRunRegistration{}, err
	}
	run.Environment = environment
	computedEnvironmentSHA256, err := canonicalBacktestHash(json.RawMessage(environment), "environment")
	if err != nil {
		return BacktestRunRegistration{}, err
	}
	if run.EnvironmentSHA256 != "" && run.EnvironmentSHA256 != computedEnvironmentSHA256 {
		return BacktestRunRegistration{}, errors.New("environment_sha256 does not match environment")
	}
	run.EnvironmentSHA256 = computedEnvironmentSHA256
	if run.StartedAt.IsZero() {
		return BacktestRunRegistration{}, errors.New("started_at is required")
	}
	run.StartedAt = run.StartedAt.UTC()
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now().UTC()
	} else {
		run.CreatedAt = run.CreatedAt.UTC()
	}
	recordHash, err := backtestRunRecordHash(run)
	if err != nil {
		return BacktestRunRegistration{}, err
	}
	if run.RecordHash != "" && run.RecordHash != recordHash {
		return BacktestRunRegistration{}, errors.New("record_hash does not match run registration")
	}
	run.RecordHash = recordHash
	return run, nil
}

type backtestRunIdentity struct {
	RunID             string          `json:"run_id"`
	ExperimentID      string          `json:"experiment_id"`
	Attempt           int             `json:"attempt"`
	EngineVersion     string          `json:"engine_version"`
	Environment       json.RawMessage `json:"environment"`
	EnvironmentSHA256 string          `json:"environment_sha256"`
	HoldoutAttempted  bool            `json:"holdout_attempted"`
	StartedAt         time.Time       `json:"started_at"`
}

func backtestRunRecordHash(run BacktestRunRegistration) (string, error) {
	return canonicalBacktestHash(backtestRunIdentity{
		RunID: run.RunID, ExperimentID: run.ExperimentID, Attempt: run.Attempt,
		EngineVersion: run.EngineVersion, Environment: run.Environment,
		EnvironmentSHA256: run.EnvironmentSHA256, HoldoutAttempted: run.HoldoutAttempted,
		StartedAt: run.StartedAt.UTC(),
	}, "backtest run registration")
}

func normalizeBacktestRunEvent(event BacktestRunEvent) (BacktestRunEvent, error) {
	event.RunID = strings.TrimSpace(event.RunID)
	event.Status = strings.TrimSpace(event.Status)
	event.ResultID = strings.TrimSpace(event.ResultID)
	event.ResultManifestPath = strings.TrimSpace(event.ResultManifestPath)
	event.ResultManifestSHA256 = strings.TrimSpace(event.ResultManifestSHA256)
	event.FailureCode = strings.TrimSpace(event.FailureCode)
	event.FailureMessage = strings.TrimSpace(event.FailureMessage)
	if err := requireBacktestUUID(event.RunID, "run_id"); err != nil {
		return BacktestRunEvent{}, err
	}
	if event.Sequence < 0 {
		return BacktestRunEvent{}, errors.New("sequence must be non-negative")
	}
	if event.Status != "running" && event.Status != "completed" && event.Status != "failed" && event.Status != "cancelled" {
		return BacktestRunEvent{}, fmt.Errorf("status %q is unsupported", event.Status)
	}
	if event.EventAt.IsZero() {
		return BacktestRunEvent{}, errors.New("event_at is required")
	}
	event.EventAt = event.EventAt.UTC()
	if event.ResultID != "" {
		if err := requireBacktestUUID(event.ResultID, "result_id"); err != nil {
			return BacktestRunEvent{}, err
		}
	}
	if (event.ResultManifestPath == "") != (event.ResultManifestSHA256 == "") {
		return BacktestRunEvent{}, errors.New("result manifest path and hash must be provided together")
	}
	if event.ResultManifestPath != "" {
		if err := requireBacktestPath(event.ResultManifestPath, "result_manifest_path"); err != nil {
			return BacktestRunEvent{}, err
		}
		if err := requireBacktestHash(event.ResultManifestSHA256, "result_manifest_sha256"); err != nil {
			return BacktestRunEvent{}, err
		}
	}
	if event.Status == "completed" {
		if event.ResultID == "" || event.ResultManifestPath == "" {
			return BacktestRunEvent{}, errors.New("completed event requires result identity and manifest")
		}
		if event.FailureCode != "" || event.FailureMessage != "" {
			return BacktestRunEvent{}, errors.New("completed event cannot contain failure details")
		}
	} else if event.Status == "running" {
		if event.ResultID != "" || event.ResultManifestPath != "" || event.FailureCode != "" || event.FailureMessage != "" {
			return BacktestRunEvent{}, errors.New("running event cannot contain result or failure details")
		}
	} else {
		if event.ResultID != "" || event.ResultManifestPath != "" || event.FailureCode == "" || event.FailureMessage == "" {
			return BacktestRunEvent{}, errors.New("terminal failure event requires failure details and no result")
		}
	}
	recordHash, err := backtestRunEventRecordHash(event)
	if err != nil {
		return BacktestRunEvent{}, err
	}
	if event.RecordHash != "" && event.RecordHash != recordHash {
		return BacktestRunEvent{}, errors.New("record_hash does not match run event")
	}
	event.RecordHash = recordHash
	return event, nil
}

type backtestRunEventIdentity struct {
	RunID                string    `json:"run_id"`
	Sequence             int       `json:"sequence"`
	Status               string    `json:"status"`
	EventAt              time.Time `json:"event_at"`
	ResultID             string    `json:"result_id,omitempty"`
	ResultManifestPath   string    `json:"result_manifest_path,omitempty"`
	ResultManifestSHA256 string    `json:"result_manifest_sha256,omitempty"`
	FailureCode          string    `json:"failure_code,omitempty"`
	FailureMessage       string    `json:"failure_message,omitempty"`
}

func backtestRunEventRecordHash(event BacktestRunEvent) (string, error) {
	return canonicalBacktestHash(backtestRunEventIdentity{
		RunID: event.RunID, Sequence: event.Sequence, Status: event.Status,
		EventAt: event.EventAt.UTC(), ResultID: event.ResultID,
		ResultManifestPath:   event.ResultManifestPath,
		ResultManifestSHA256: event.ResultManifestSHA256,
		FailureCode:          event.FailureCode, FailureMessage: event.FailureMessage,
	}, "backtest run event")
}

func (r *Repository) RegisterBacktestExperiment(ctx context.Context, registration BacktestExperimentRegistration) (BacktestExperimentRegistrationResult, error) {
	if err := requireResearchRepository(r); err != nil {
		return BacktestExperimentRegistrationResult{}, err
	}
	normalized, err := registration.normalized()
	if err != nil {
		return BacktestExperimentRegistrationResult{}, err
	}
	registrationSHA256, err := backtestExperimentRegistrationSHA256(normalized)
	if err != nil {
		return BacktestExperimentRegistrationResult{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return BacktestExperimentRegistrationResult{}, err
	}
	defer tx.Rollback(ctx)
	var insertedSHA256 string
	err = tx.QueryRow(ctx, insertBacktestExperimentSQL,
		normalized.Experiment.ExperimentID,
		normalized.Experiment.ExperimentSHA256,
		normalized.Experiment.SchemaVersion,
		normalized.Experiment.StrategyName,
		normalized.Experiment.StrategyVersion,
		normalized.Experiment.StrategyGitCommit,
		normalized.Experiment.StrategyParameters,
		normalized.Experiment.UniverseID,
		normalized.Experiment.UniverseVersion,
		normalized.Experiment.MembershipFingerprint,
		normalized.Experiment.BaseCurrency,
		normalized.Experiment.ReportingCurrency,
		normalized.Experiment.StartDate,
		normalized.Experiment.EndDate,
		normalized.Experiment.ValidationPartition,
		normalized.Experiment.BenchmarkSecurityID,
		normalized.Experiment.BenchmarkCurrency,
		normalized.Experiment.DecisionPolicy,
		normalized.Experiment.CostPolicyVersion,
		normalized.Experiment.RebalanceFrequency,
		normalized.Experiment.InputFingerprint,
		normalized.Experiment.SpecPath,
		normalized.Experiment.EngineVersion,
		normalized.Experiment.CreatedAt,
		registrationSHA256,
	).Scan(&insertedSHA256)
	if err == nil {
		if insertedSHA256 != registrationSHA256 {
			return BacktestExperimentRegistrationResult{}, errors.New("database returned an unexpected backtest registration hash")
		}
		if err := insertBacktestExperimentInputs(ctx, tx, normalized); err != nil {
			return BacktestExperimentRegistrationResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return BacktestExperimentRegistrationResult{}, err
		}
		return BacktestExperimentRegistrationResult{
			ExperimentID: normalized.Experiment.ExperimentID, RegistrationSHA256: registrationSHA256,
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return BacktestExperimentRegistrationResult{}, err
	}
	var existingSHA256 string
	if err := tx.QueryRow(ctx, selectBacktestExperimentRegistrationSQL, normalized.Experiment.ExperimentID).Scan(&existingSHA256); err != nil {
		return BacktestExperimentRegistrationResult{}, err
	}
	if existingSHA256 != registrationSHA256 {
		return BacktestExperimentRegistrationResult{}, fmt.Errorf("backtest experiment %s conflicts with existing registration", normalized.Experiment.ExperimentID)
	}
	if err := backtestExperimentInputsMatch(ctx, tx, normalized); err != nil {
		return BacktestExperimentRegistrationResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BacktestExperimentRegistrationResult{}, err
	}
	return BacktestExperimentRegistrationResult{
		ExperimentID: normalized.Experiment.ExperimentID, RegistrationSHA256: registrationSHA256, AlreadyPresent: true,
	}, nil
}

func insertBacktestExperimentInputs(ctx context.Context, tx pgx.Tx, registration BacktestExperimentRegistration) error {
	for _, input := range registration.Inputs {
		if _, err := tx.Exec(ctx, `
INSERT INTO backtest_experiment_inputs(
    experiment_id, ordinal, input_kind, artifact_id, path, sha256, available_at, historical_fitness
) VALUES($1::uuid,$2,$3,$4::uuid,$5,$6,$7,$8)`,
			registration.Experiment.ExperimentID, input.Ordinal, input.Kind, input.ArtifactID,
			input.Path, input.SHA256, input.AvailableAt, input.HistoricalFitness,
		); err != nil {
			return fmt.Errorf("insert backtest input %s: %w", input.Kind, err)
		}
	}
	return nil
}

func backtestExperimentInputsMatch(ctx context.Context, tx pgx.Tx, registration BacktestExperimentRegistration) error {
	rows, err := tx.Query(ctx, `
SELECT ordinal, input_kind, artifact_id::text, path, sha256, available_at, historical_fitness
FROM backtest_experiment_inputs
WHERE experiment_id = $1::uuid
ORDER BY ordinal`, registration.Experiment.ExperimentID)
	if err != nil {
		return fmt.Errorf("query existing backtest inputs: %w", err)
	}
	defer rows.Close()
	index := 0
	for rows.Next() {
		if index >= len(registration.Inputs) {
			return fmt.Errorf("backtest experiment %s has conflicting input lineage", registration.Experiment.ExperimentID)
		}
		var actual BacktestExperimentInput
		if err := rows.Scan(&actual.Ordinal, &actual.Kind, &actual.ArtifactID, &actual.Path, &actual.SHA256, &actual.AvailableAt, &actual.HistoricalFitness); err != nil {
			return fmt.Errorf("scan existing backtest input: %w", err)
		}
		expected := registration.Inputs[index]
		actual.AvailableAt = actual.AvailableAt.UTC()
		if actual != expected {
			return fmt.Errorf("backtest experiment %s has conflicting input lineage", registration.Experiment.ExperimentID)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate existing backtest inputs: %w", err)
	}
	if index != len(registration.Inputs) {
		return fmt.Errorf("backtest experiment %s has incomplete input lineage", registration.Experiment.ExperimentID)
	}
	return nil
}

func (r *Repository) StartBacktestRun(ctx context.Context, run BacktestRunRegistration) (BacktestRunStartResult, error) {
	if err := requireResearchRepository(r); err != nil {
		return BacktestRunStartResult{}, err
	}
	normalized, err := normalizeBacktestRunRegistration(run)
	if err != nil {
		return BacktestRunStartResult{}, err
	}
	startEvent, err := normalizeBacktestRunEvent(BacktestRunEvent{
		RunID: normalized.RunID, Sequence: 0, Status: "running", EventAt: normalized.StartedAt,
	})
	if err != nil {
		return BacktestRunStartResult{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return BacktestRunStartResult{}, err
	}
	defer tx.Rollback(ctx)
	var insertedHash string
	err = tx.QueryRow(ctx, insertBacktestRunSQL,
		normalized.RunID, normalized.ExperimentID, normalized.Attempt,
		normalized.EngineVersion, normalized.Environment, normalized.EnvironmentSHA256,
		normalized.HoldoutAttempted, normalized.StartedAt, normalized.CreatedAt, normalized.RecordHash,
	).Scan(&insertedHash)
	if err == nil {
		if insertedHash != normalized.RecordHash {
			return BacktestRunStartResult{}, errors.New("database returned an unexpected backtest run hash")
		}
		if _, err := tx.Exec(ctx, insertBacktestRunEventSQL,
			startEvent.RunID, startEvent.Sequence, startEvent.Status, startEvent.EventAt,
			startEvent.ResultID, startEvent.ResultManifestPath, startEvent.ResultManifestSHA256,
			startEvent.FailureCode, startEvent.FailureMessage, startEvent.RecordHash,
		); err != nil {
			return BacktestRunStartResult{}, fmt.Errorf("insert backtest run start event: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return BacktestRunStartResult{}, err
		}
		return BacktestRunStartResult{
			RunID: normalized.RunID, ExperimentID: normalized.ExperimentID, Attempt: normalized.Attempt,
			RecordHash: normalized.RecordHash, StartEventHash: startEvent.RecordHash,
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return BacktestRunStartResult{}, err
	}
	var existingHash string
	if err := tx.QueryRow(ctx, `SELECT record_hash FROM backtest_runs WHERE run_id = $1::uuid`, normalized.RunID).Scan(&existingHash); err != nil {
		return BacktestRunStartResult{}, err
	}
	if existingHash != normalized.RecordHash {
		return BacktestRunStartResult{}, fmt.Errorf("backtest run %s conflicts with existing registration", normalized.RunID)
	}
	var existingStartHash string
	if err := tx.QueryRow(ctx, `SELECT record_hash FROM backtest_run_events WHERE run_id = $1::uuid AND sequence = 0`, normalized.RunID).Scan(&existingStartHash); err != nil {
		return BacktestRunStartResult{}, err
	}
	if existingStartHash != startEvent.RecordHash {
		return BacktestRunStartResult{}, fmt.Errorf("backtest run %s has conflicting start event", normalized.RunID)
	}
	if err := tx.Commit(ctx); err != nil {
		return BacktestRunStartResult{}, err
	}
	return BacktestRunStartResult{
		RunID: normalized.RunID, ExperimentID: normalized.ExperimentID, Attempt: normalized.Attempt,
		RecordHash: normalized.RecordHash, StartEventHash: startEvent.RecordHash, AlreadyPresent: true,
	}, nil
}

func (r *Repository) AppendBacktestRunEvent(ctx context.Context, event BacktestRunEvent) (BacktestRunEventResult, error) {
	if err := requireResearchRepository(r); err != nil {
		return BacktestRunEventResult{}, err
	}
	normalized, err := normalizeBacktestRunEvent(event)
	if err != nil {
		return BacktestRunEventResult{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return BacktestRunEventResult{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "backtest-run:"+normalized.RunID); err != nil {
		return BacktestRunEventResult{}, fmt.Errorf("lock backtest run event stream: %w", err)
	}
	var startedAt time.Time
	if err := tx.QueryRow(ctx, `SELECT started_at FROM backtest_runs WHERE run_id = $1::uuid`, normalized.RunID).Scan(&startedAt); err != nil {
		return BacktestRunEventResult{}, fmt.Errorf("load backtest run %s: %w", normalized.RunID, err)
	}
	if normalized.EventAt.Before(startedAt.UTC()) {
		return BacktestRunEventResult{}, errors.New("event_at cannot precede run start")
	}
	var existingHash string
	err = tx.QueryRow(ctx, `SELECT record_hash FROM backtest_run_events WHERE run_id = $1::uuid AND sequence = $2`, normalized.RunID, normalized.Sequence).Scan(&existingHash)
	if err == nil {
		if existingHash != normalized.RecordHash {
			return BacktestRunEventResult{}, fmt.Errorf("backtest run %s event %d conflicts with existing event", normalized.RunID, normalized.Sequence)
		}
		if err := tx.Commit(ctx); err != nil {
			return BacktestRunEventResult{}, err
		}
		return BacktestRunEventResult{RunID: normalized.RunID, Sequence: normalized.Sequence, RecordHash: normalized.RecordHash, AlreadyPresent: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return BacktestRunEventResult{}, err
	}
	var expectedSequence int
	if err := tx.QueryRow(ctx, `SELECT coalesce(max(sequence), -1) + 1 FROM backtest_run_events WHERE run_id = $1::uuid`, normalized.RunID).Scan(&expectedSequence); err != nil {
		return BacktestRunEventResult{}, err
	}
	if normalized.Sequence != expectedSequence {
		return BacktestRunEventResult{}, fmt.Errorf("backtest run event sequence must be %d, got %d", expectedSequence, normalized.Sequence)
	}
	var terminalStatus string
	err = tx.QueryRow(ctx, `SELECT status FROM backtest_run_events WHERE run_id = $1::uuid AND status IN ('completed', 'failed', 'cancelled') LIMIT 1`, normalized.RunID).Scan(&terminalStatus)
	if err == nil {
		return BacktestRunEventResult{}, fmt.Errorf("backtest run %s already has terminal event %s", normalized.RunID, terminalStatus)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return BacktestRunEventResult{}, err
	}
	if _, err := tx.Exec(ctx, insertBacktestRunEventSQL,
		normalized.RunID, normalized.Sequence, normalized.Status, normalized.EventAt,
		normalized.ResultID, normalized.ResultManifestPath, normalized.ResultManifestSHA256,
		normalized.FailureCode, normalized.FailureMessage, normalized.RecordHash,
	); err != nil {
		return BacktestRunEventResult{}, fmt.Errorf("insert backtest run event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return BacktestRunEventResult{}, err
	}
	return BacktestRunEventResult{RunID: normalized.RunID, Sequence: normalized.Sequence, RecordHash: normalized.RecordHash}, nil
}

func (r *Repository) GetBacktestExperimentReport(ctx context.Context, experimentID string) (BacktestExperimentReport, error) {
	if err := requireResearchRepository(r); err != nil {
		return BacktestExperimentReport{}, err
	}
	if err := requireBacktestUUID(experimentID, "experiment_id"); err != nil {
		return BacktestExperimentReport{}, err
	}
	var report BacktestExperimentReport
	var parameters []byte
	err := r.pool.QueryRow(ctx, `
SELECT experiment_id::text, experiment_sha256, schema_version, strategy_name,
       strategy_version, strategy_git_commit, strategy_parameters,
       universe_id::text, universe_version, membership_fingerprint,
       base_currency, coalesce(reporting_currency, ''), start_date::text, end_date::text,
       validation_partition, benchmark_security_id::text, benchmark_currency,
       decision_policy, cost_policy_version, rebalance_frequency, input_fingerprint,
       spec_path, engine_version, created_at, registered_at
FROM backtest_experiments
WHERE experiment_id = $1::uuid`, experimentID).Scan(
		&report.Experiment.ExperimentID, &report.Experiment.ExperimentSHA256,
		&report.Experiment.SchemaVersion, &report.Experiment.StrategyName,
		&report.Experiment.StrategyVersion, &report.Experiment.StrategyGitCommit,
		&parameters, &report.Experiment.UniverseID, &report.Experiment.UniverseVersion,
		&report.Experiment.MembershipFingerprint, &report.Experiment.BaseCurrency,
		&report.Experiment.ReportingCurrency, &report.Experiment.StartDate,
		&report.Experiment.EndDate, &report.Experiment.ValidationPartition,
		&report.Experiment.BenchmarkSecurityID, &report.Experiment.BenchmarkCurrency,
		&report.Experiment.DecisionPolicy, &report.Experiment.CostPolicyVersion,
		&report.Experiment.RebalanceFrequency, &report.Experiment.InputFingerprint,
		&report.Experiment.SpecPath, &report.Experiment.EngineVersion,
		&report.Experiment.CreatedAt, &report.Experiment.RegisteredAt,
	)
	if err != nil {
		return BacktestExperimentReport{}, fmt.Errorf("load backtest experiment %s: %w", experimentID, err)
	}
	report.Experiment.StrategyParameters = append(json.RawMessage(nil), parameters...)
	report.SchemaVersion = report.Experiment.SchemaVersion
	report.Inputs = []BacktestExperimentInput{}
	inputRows, err := r.pool.Query(ctx, `
SELECT ordinal, input_kind, artifact_id::text, path, sha256, available_at, historical_fitness
FROM backtest_experiment_inputs
WHERE experiment_id = $1::uuid
ORDER BY ordinal`, experimentID)
	if err != nil {
		return BacktestExperimentReport{}, fmt.Errorf("load backtest experiment inputs: %w", err)
	}
	for inputRows.Next() {
		var input BacktestExperimentInput
		if err := inputRows.Scan(&input.Ordinal, &input.Kind, &input.ArtifactID, &input.Path, &input.SHA256, &input.AvailableAt, &input.HistoricalFitness); err != nil {
			inputRows.Close()
			return BacktestExperimentReport{}, fmt.Errorf("scan backtest experiment input: %w", err)
		}
		input.AvailableAt = input.AvailableAt.UTC()
		report.Inputs = append(report.Inputs, input)
	}
	if err := inputRows.Err(); err != nil {
		inputRows.Close()
		return BacktestExperimentReport{}, fmt.Errorf("iterate backtest experiment inputs: %w", err)
	}
	inputRows.Close()
	report.Runs = []BacktestRunReport{}
	runRows, err := r.pool.Query(ctx, `
SELECT run.run_id::text, run.experiment_id::text, run.attempt, run.engine_version,
       run.environment, run.environment_sha256, run.holdout_attempted,
       run.started_at, run.created_at,
       coalesce(status.status, ''), coalesce(status.sequence, -1), status.event_at,
       coalesce(status.result_id::text, ''), coalesce(status.result_manifest_path, ''),
       coalesce(status.result_manifest_sha256, ''), coalesce(status.failure_code, ''),
       coalesce(status.failure_message, '')
FROM backtest_runs run
LEFT JOIN backtest_run_status status ON status.run_id = run.run_id
WHERE run.experiment_id = $1::uuid
ORDER BY run.attempt, run.run_id`, experimentID)
	if err != nil {
		return BacktestExperimentReport{}, fmt.Errorf("load backtest runs: %w", err)
	}
	for runRows.Next() {
		var run BacktestRunReport
		var environment []byte
		var lastEventAt *time.Time
		if err := runRows.Scan(
			&run.RunID, &run.ExperimentID, &run.Attempt, &run.EngineVersion,
			&environment, &run.EnvironmentSHA256, &run.HoldoutAttempted,
			&run.StartedAt, &run.CreatedAt, &run.Status, &run.LastEventSequence,
			&lastEventAt, &run.ResultID, &run.ResultManifestPath, &run.ResultManifestSHA256,
			&run.FailureCode, &run.FailureMessage,
		); err != nil {
			runRows.Close()
			return BacktestExperimentReport{}, fmt.Errorf("scan backtest run: %w", err)
		}
		run.Environment = append(json.RawMessage(nil), environment...)
		if lastEventAt != nil {
			run.LastEventAt = lastEventAt.UTC()
		}
		run.StartedAt = run.StartedAt.UTC()
		run.CreatedAt = run.CreatedAt.UTC()
		report.Runs = append(report.Runs, run)
	}
	if err := runRows.Err(); err != nil {
		runRows.Close()
		return BacktestExperimentReport{}, fmt.Errorf("iterate backtest runs: %w", err)
	}
	runRows.Close()
	return report, nil
}
