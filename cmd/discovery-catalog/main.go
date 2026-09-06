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
	"strings"
	"time"

	"github.com/luisdourado/invs/internal/metadata"
)

type discoveryDocument struct {
	SchemaVersion string `json:"schema_version"`
	DiscoveryID   string `json:"discovery_id"`
	Model         struct {
		Name      string            `json:"name"`
		Version   string            `json:"version"`
		GitCommit string            `json:"git_commit"`
		Weights   map[string]string `json:"weights"`
	} `json:"model"`
	Universe struct {
		UniverseID     string `json:"universe_id"`
		Name           string `json:"name"`
		MembershipAsOf string `json:"membership_as_of"`
		Fingerprint    string `json:"fingerprint"`
		Size           int    `json:"size"`
	} `json:"universe"`
	MarketSession string `json:"market_session"`
	DecisionAt    string `json:"decision_at"`
	FeatureBatch  struct {
		BatchID           string           `json:"batch_id"`
		FeatureSet        string           `json:"feature_set"`
		FeatureSetVersion string           `json:"feature_set_version"`
		InputFingerprint  string           `json:"input_fingerprint"`
		ManifestPath      string           `json:"manifest_path"`
		ManifestSHA256    string           `json:"manifest_sha256"`
		InputFitness      []map[string]any `json:"input_fitness"`
	} `json:"feature_batch"`
	Policy struct {
		CandidateCount   int    `json:"candidate_count"`
		Purpose          string `json:"purpose"`
		AutomaticTrading bool   `json:"automatic_trading"`
	} `json:"policy"`
	Summary struct {
		EligibleCount  int `json:"eligible_count"`
		RejectedCount  int `json:"rejected_count"`
		CandidateCount int `json:"candidate_count"`
	} `json:"summary"`
	Rows []struct {
		Rank       *int    `json:"rank"`
		SecurityID string  `json:"security_id"`
		Ticker     string  `json:"ticker"`
		Sector     string  `json:"sector"`
		Candidate  bool    `json:"candidate"`
		Score      *string `json:"score"`
		Features   struct {
			Return1M             *string `json:"return_1m"`
			Return3M             *string `json:"return_3m"`
			Return6M             *string `json:"return_6m"`
			Return12M            *string `json:"return_12m"`
			RealizedVolatility1M *string `json:"realized_volatility_1m"`
			MaxDrawdown1M        *string `json:"max_drawdown_1m"`
		} `json:"features"`
		ComponentPercentiles map[string]*string `json:"component_percentiles"`
		Eligibility          struct {
			Status  string   `json:"status"`
			Reasons []string `json:"reasons"`
		} `json:"eligibility"`
	} `json:"rows"`
}

func main() {
	var databaseURL, manifestPath, discoveryRoot, featuresRoot string
	flag.StringVar(&databaseURL, "database-url", os.Getenv("DATABASE_URL"), "PostgreSQL URL")
	flag.StringVar(&manifestPath, "manifest", "", "discovery index manifest")
	flag.StringVar(&discoveryRoot, "discovery-root", "/data/research/discovery", "discovery artifact root")
	flag.StringVar(&featuresRoot, "features-root", "/data/features", "feature artifact root")
	flag.Parse()
	if databaseURL == "" || manifestPath == "" {
		fatal(errors.New("DATABASE_URL and --manifest are required"))
	}
	root, err := filepath.Abs(discoveryRoot)
	if err != nil {
		fatal(err)
	}
	manifest, err := filepath.Abs(manifestPath)
	if err != nil {
		fatal(err)
	}
	relative, err := filepath.Rel(root, manifest)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		fatal(errors.New("discovery manifest must be below discovery-root"))
	}
	content, err := os.ReadFile(manifest)
	if err != nil {
		fatal(err)
	}
	var document discoveryDocument
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		fatal(fmt.Errorf("decode discovery manifest: %w", err))
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		fatal(errors.New("discovery manifest has trailing JSON content"))
	}
	if document.SchemaVersion != "1.0.0" || document.Model.Name != "momentum-risk-rank" || document.Model.Version != "1.0.0" {
		fatal(errors.New("unsupported discovery contract"))
	}
	if document.FeatureBatch.FeatureSet != "market-momentum" || document.FeatureBatch.FeatureSetVersion != "1.0.0" {
		fatal(errors.New("discovery feature batch contract is unsupported"))
	}
	featureRoot, err := filepath.Abs(featuresRoot)
	if err != nil {
		fatal(err)
	}
	featureManifest := filepath.Join(featureRoot, filepath.FromSlash(document.FeatureBatch.ManifestPath))
	featureRelative, err := filepath.Rel(featureRoot, featureManifest)
	if err != nil || featureRelative == "." || strings.HasPrefix(featureRelative, ".."+string(filepath.Separator)) || filepath.IsAbs(featureRelative) {
		fatal(errors.New("discovery feature manifest escapes features-root"))
	}
	featureContent, err := os.ReadFile(featureManifest)
	if err != nil {
		fatal(fmt.Errorf("read discovery feature manifest: %w", err))
	}
	featureDigest := sha256.Sum256(featureContent)
	if hex.EncodeToString(featureDigest[:]) != document.FeatureBatch.ManifestSHA256 {
		fatal(errors.New("discovery feature manifest hash mismatch"))
	}
	var featureDocument struct {
		FeatureSet        string `json:"feature_set"`
		FeatureSetVersion string `json:"feature_set_version"`
		InputFingerprint  string `json:"input_fingerprint"`
		Batch             struct {
			BatchID string `json:"batch_id"`
		} `json:"batch"`
	}
	if err := json.Unmarshal(featureContent, &featureDocument); err != nil || featureDocument.FeatureSet != document.FeatureBatch.FeatureSet || featureDocument.FeatureSetVersion != document.FeatureBatch.FeatureSetVersion || featureDocument.InputFingerprint != document.FeatureBatch.InputFingerprint || featureDocument.Batch.BatchID != document.FeatureBatch.BatchID {
		fatal(errors.New("discovery feature reference differs from the referenced batch"))
	}
	marketSession, err := time.Parse("2006-01-02", document.MarketSession)
	if err != nil {
		fatal(err)
	}
	decisionAt, err := time.Parse(time.RFC3339Nano, document.DecisionAt)
	if err != nil {
		fatal(err)
	}
	digest := sha256.Sum256(content)
	registration := metadata.DiscoveryIndexRegistration{
		DiscoveryID: document.DiscoveryID, ModelName: document.Model.Name,
		ModelVersion: document.Model.Version, GitCommit: document.Model.GitCommit,
		UniverseID: document.Universe.UniverseID, UniverseName: document.Universe.Name,
		UniverseFingerprint: document.Universe.Fingerprint, MarketSession: marketSession,
		DecisionAt: decisionAt, FeatureBatchID: document.FeatureBatch.BatchID,
		FeatureInputFingerprint: document.FeatureBatch.InputFingerprint,
		CandidateCount:          document.Summary.CandidateCount, EligibleCount: document.Summary.EligibleCount,
		RejectedCount: document.Summary.RejectedCount, ManifestPath: filepath.ToSlash(relative),
		ManifestSHA256: hex.EncodeToString(digest[:]),
	}
	if document.Policy.CandidateCount != document.Summary.CandidateCount || document.Policy.AutomaticTrading || document.Policy.Purpose != "research_watchlist_only" {
		fatal(errors.New("discovery policy and summary are inconsistent"))
	}
	for _, row := range document.Rows {
		registration.Rankings = append(registration.Rankings, metadata.DiscoveryRanking{
			SecurityID: row.SecurityID, Rank: row.Rank, Ticker: row.Ticker, Sector: row.Sector,
			Candidate: row.Candidate, Score: row.Score, Return1M: row.Features.Return1M,
			Return3M: row.Features.Return3M, Return6M: row.Features.Return6M,
			Return12M: row.Features.Return12M, RealizedVolatility1M: row.Features.RealizedVolatility1M,
			MaxDrawdown1M: row.Features.MaxDrawdown1M, EligibilityStatus: row.Eligibility.Status,
			RejectionReasons: row.Eligibility.Reasons,
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repository, err := metadata.Open(ctx, databaseURL)
	if err != nil {
		fatal(err)
	}
	defer repository.Close()
	result, err := repository.RegisterDiscoveryIndex(ctx, registration)
	if err != nil {
		fatal(err)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"action": "cataloged", "discovery_id": result.DiscoveryID,
		"registration_sha256": result.RegistrationSHA256,
		"already_present":     result.AlreadyPresent, "manifest_path": filepath.ToSlash(relative),
		"candidate_count": document.Summary.CandidateCount,
	})
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "invs-discovery-catalog: %v\n", err)
	os.Exit(2)
}
