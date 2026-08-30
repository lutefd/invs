package metadata

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func testBacktestRegistration() BacktestExperimentRegistration {
	availableAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	return BacktestExperimentRegistration{
		Experiment: BacktestExperiment{
			ExperimentID:          "10000000-0000-4000-8000-000000000001",
			ExperimentSHA256:      strings.Repeat("1", 64),
			SchemaVersion:         BacktestSchemaVersion,
			StrategyName:          "equal_weight",
			StrategyVersion:       "1.0.0",
			StrategyGitCommit:     "unknown",
			StrategyParameters:    json.RawMessage(`{"rebalance_frequency":"monthly"}`),
			UniverseID:            "10000000-0000-4000-8000-000000000002",
			UniverseVersion:       "1.0.0",
			MembershipFingerprint: strings.Repeat("2", 64),
			BaseCurrency:          "USD",
			ReportingCurrency:     "BRL",
			StartDate:             "2026-01-01",
			EndDate:               "2026-12-31",
			ValidationPartition:   "validation",
			BenchmarkSecurityID:   "10000000-0000-4000-8000-000000000003",
			BenchmarkCurrency:     "USD",
			DecisionPolicy:        "after_close_next_session_open",
			CostPolicyVersion:     "1.0.0",
			RebalanceFrequency:    "monthly",
			SpecPath:              "experiments/equal-weight/spec.json",
			EngineVersion:         "python-backtest-1.0.0",
		},
		Inputs: []BacktestExperimentInput{
			{Kind: "membership", ArtifactID: "10000000-0000-4000-8000-000000000103", Path: "membership/manifest.json", SHA256: strings.Repeat("5", 64), AvailableAt: availableAt, HistoricalFitness: "backtest_safe"},
			{Kind: "prices", ArtifactID: "10000000-0000-4000-8000-000000000101", Path: "prices/manifest.json", SHA256: strings.Repeat("3", 64), AvailableAt: availableAt, HistoricalFitness: "backtest_safe"},
			{Kind: "calendar", ArtifactID: "10000000-0000-4000-8000-000000000102", Path: "calendar/manifest.json", SHA256: strings.Repeat("4", 64), AvailableAt: availableAt, HistoricalFitness: "backtest_safe"},
		},
	}
}

func TestBacktestExperimentRegistrationNormalizesAndFingerprintsInputs(t *testing.T) {
	registration := testBacktestRegistration()
	normalized, err := registration.normalized()
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Inputs[0].Kind != "calendar" || normalized.Inputs[1].Kind != "membership" || normalized.Inputs[2].Kind != "prices" {
		t.Fatalf("inputs were not sorted canonically: %+v", normalized.Inputs)
	}
	for index, input := range normalized.Inputs {
		if input.Ordinal != index {
			t.Fatalf("input ordinal = %d, want %d", input.Ordinal, index)
		}
	}
	if !backtestHashPattern.MatchString(normalized.Experiment.InputFingerprint) {
		t.Fatalf("input fingerprint = %q", normalized.Experiment.InputFingerprint)
	}
	firstHash, err := backtestExperimentRegistrationSHA256(normalized)
	if err != nil {
		t.Fatal(err)
	}

	reordered := testBacktestRegistration()
	reordered.Experiment.CreatedAt = time.Now().UTC()
	reordered.Inputs[0], reordered.Inputs[2] = reordered.Inputs[2], reordered.Inputs[0]
	normalizedAgain, err := reordered.normalized()
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := backtestExperimentRegistrationSHA256(normalizedAgain)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatalf("equivalent registrations differ: %s != %s", firstHash, secondHash)
	}
}

func TestBacktestExperimentRegistrationRejectsUnsafeOrIncompleteInputs(t *testing.T) {
	cases := []struct {
		name string
		edit func(*BacktestExperimentRegistration)
		want string
	}{
		{"unsafe spec path", func(r *BacktestExperimentRegistration) { r.Experiment.SpecPath = "../spec.json" }, "unsafe path"},
		{"unsupported fitness", func(r *BacktestExperimentRegistration) { r.Inputs[0].HistoricalFitness = "current_snapshot" }, "not backtest_safe"},
		{"missing membership", func(r *BacktestExperimentRegistration) { r.Inputs[0].Kind = "macro" }, "include membership"},
		{"invalid date", func(r *BacktestExperimentRegistration) { r.Experiment.StartDate = "2026-02-30" }, "ISO date"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registration := testBacktestRegistration()
			tc.edit(&registration)
			if _, err := registration.normalized(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("normalized error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestBacktestJSONObjectsRejectDuplicateKeysAndCanonicalize(t *testing.T) {
	canonical, err := normalizeBacktestObject(json.RawMessage(` { "z": 2, "a": 1 } `), "parameters")
	if err != nil || string(canonical) != `{"a":1,"z":2}` {
		t.Fatalf("canonical object = %s, %v", canonical, err)
	}
	if _, err := normalizeBacktestObject(json.RawMessage(`{"a":1,"a":2}`), "parameters"); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate object error = %v", err)
	}
}

func TestBacktestRunAndEventHashesExcludeOperationalCreationTime(t *testing.T) {
	startedAt := time.Date(2026, 1, 2, 21, 0, 0, 0, time.UTC)
	run := BacktestRunRegistration{
		RunID:            "20000000-0000-4000-8000-000000000001",
		ExperimentID:     "10000000-0000-4000-8000-000000000001",
		Attempt:          1,
		EngineVersion:    "python-backtest-1.0.0",
		Environment:      json.RawMessage(`{"python":"3.12","platform":"linux"}`),
		HoldoutAttempted: true,
		StartedAt:        startedAt,
	}
	normalized, err := normalizeBacktestRunRegistration(run)
	if err != nil {
		t.Fatal(err)
	}
	if !backtestHashPattern.MatchString(normalized.EnvironmentSHA256) || !backtestHashPattern.MatchString(normalized.RecordHash) {
		t.Fatalf("normalized run hashes = %+v", normalized)
	}
	recreated := normalized
	recreated.CreatedAt = normalized.CreatedAt.Add(time.Hour)
	recreatedHash, err := backtestRunRecordHash(recreated)
	if err != nil || recreatedHash != normalized.RecordHash {
		t.Fatalf("creation time changed run hash: %s != %s (%v)", recreatedHash, normalized.RecordHash, err)
	}

	event, err := normalizeBacktestRunEvent(BacktestRunEvent{
		RunID: "20000000-0000-4000-8000-000000000001", Sequence: 1, Status: "completed",
		EventAt: startedAt.Add(time.Hour), ResultID: "30000000-0000-4000-8000-000000000001",
		ResultManifestPath: "results/result-1/manifest.json", ResultManifestSHA256: strings.Repeat("9", 64),
	})
	if err != nil || !backtestHashPattern.MatchString(event.RecordHash) {
		t.Fatalf("normalized completion event = %+v, %v", event, err)
	}
}

func TestBacktestRunEventRejectsInvalidStatusPayloads(t *testing.T) {
	base := BacktestRunEvent{RunID: "20000000-0000-4000-8000-000000000001", Sequence: 1, EventAt: time.Now().UTC()}
	cases := []struct {
		name   string
		status string
		edit   func(*BacktestRunEvent)
	}{
		{"completed without result", "completed", nil},
		{"running with failure", "running", func(event *BacktestRunEvent) { event.FailureMessage = "crashed" }},
		{"failed without message", "failed", func(event *BacktestRunEvent) { event.FailureCode = "worker_error" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := base
			event.Status = tc.status
			if tc.edit != nil {
				tc.edit(&event)
			}
			if _, err := normalizeBacktestRunEvent(event); err == nil {
				t.Fatal("invalid event payload was accepted")
			}
		})
	}
}

func TestBacktestMetadataSQLIsImmutableAndIdempotent(t *testing.T) {
	for _, fragment := range []string{
		"INSERT INTO backtest_experiments",
		"ON CONFLICT (experiment_id) DO NOTHING",
		"RETURNING registration_sha256",
	} {
		if !strings.Contains(insertBacktestExperimentSQL, fragment) {
			t.Fatalf("insertBacktestExperimentSQL missing %q", fragment)
		}
	}
	for _, fragment := range []string{
		"INSERT INTO backtest_runs",
		"ON CONFLICT (run_id) DO NOTHING",
		"INSERT INTO backtest_run_events",
	} {
		if !strings.Contains(insertBacktestRunSQL+insertBacktestRunEventSQL, fragment) {
			t.Fatalf("backtest SQL missing %q", fragment)
		}
	}
}
