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

var discoveryTickerPattern = regexp.MustCompile(`^[A-Z0-9.-]{1,16}$`)

type DiscoveryRanking struct {
	SecurityID           string   `json:"security_id"`
	Rank                 *int     `json:"rank"`
	Ticker               string   `json:"ticker"`
	Sector               string   `json:"sector"`
	Candidate            bool     `json:"candidate"`
	Score                *string  `json:"score"`
	Return1M             *string  `json:"return_1m"`
	Return3M             *string  `json:"return_3m"`
	Return6M             *string  `json:"return_6m"`
	Return12M            *string  `json:"return_12m"`
	RealizedVolatility1M *string  `json:"realized_volatility_1m"`
	MaxDrawdown1M        *string  `json:"max_drawdown_1m"`
	EligibilityStatus    string   `json:"eligibility_status"`
	RejectionReasons     []string `json:"rejection_reasons"`
}

type DiscoveryIndexRegistration struct {
	DiscoveryID             string             `json:"discovery_id"`
	ModelName               string             `json:"model_name"`
	ModelVersion            string             `json:"model_version"`
	GitCommit               string             `json:"git_commit"`
	UniverseID              string             `json:"universe_id"`
	UniverseName            string             `json:"universe_name"`
	UniverseFingerprint     string             `json:"universe_fingerprint"`
	MarketSession           time.Time          `json:"market_session"`
	DecisionAt              time.Time          `json:"decision_at"`
	FeatureBatchID          string             `json:"feature_batch_id"`
	FeatureInputFingerprint string             `json:"feature_input_fingerprint"`
	CandidateCount          int                `json:"candidate_count"`
	EligibleCount           int                `json:"eligible_count"`
	RejectedCount           int                `json:"rejected_count"`
	ManifestPath            string             `json:"manifest_path"`
	ManifestSHA256          string             `json:"manifest_sha256"`
	Rankings                []DiscoveryRanking `json:"rankings"`
}

type DiscoveryIndexRegistrationResult struct {
	DiscoveryID        string
	RegistrationSHA256 string
	AlreadyPresent     bool
}

func (r DiscoveryIndexRegistration) normalized() (DiscoveryIndexRegistration, error) {
	r.DiscoveryID = strings.TrimSpace(r.DiscoveryID)
	r.ModelName = strings.TrimSpace(r.ModelName)
	r.ModelVersion = strings.TrimSpace(r.ModelVersion)
	r.GitCommit = strings.TrimSpace(r.GitCommit)
	r.UniverseID = strings.TrimSpace(r.UniverseID)
	r.UniverseName = strings.TrimSpace(r.UniverseName)
	r.UniverseFingerprint = strings.TrimSpace(r.UniverseFingerprint)
	r.FeatureBatchID = strings.TrimSpace(r.FeatureBatchID)
	r.FeatureInputFingerprint = strings.TrimSpace(r.FeatureInputFingerprint)
	r.ManifestPath = strings.TrimSpace(r.ManifestPath)
	r.ManifestSHA256 = strings.TrimSpace(r.ManifestSHA256)
	for label, value := range map[string]string{
		"discovery_id": r.DiscoveryID, "universe_id": r.UniverseID,
		"feature_batch_id": r.FeatureBatchID,
	} {
		if _, err := uuid.Parse(value); err != nil {
			return r, fmt.Errorf("%s must be a UUID: %w", label, err)
		}
	}
	if !featureArtifactIdentifierPattern.MatchString(r.ModelName) || !featureArtifactIdentifierPattern.MatchString(r.UniverseName) {
		return r, errors.New("discovery model and universe names must be slugs")
	}
	if !featureArtifactVersionPattern.MatchString(r.ModelVersion) {
		return r, errors.New("discovery model version must be semantic")
	}
	if !featureArtifactGitPattern.MatchString(r.GitCommit) {
		return r, errors.New("discovery git commit is invalid")
	}
	for label, value := range map[string]string{
		"universe_fingerprint":      r.UniverseFingerprint,
		"feature_input_fingerprint": r.FeatureInputFingerprint,
		"manifest_sha256":           r.ManifestSHA256,
	} {
		if !featureArtifactHashPattern.MatchString(value) {
			return r, fmt.Errorf("%s is not a SHA-256 digest", label)
		}
	}
	if err := requireFeatureArtifactPath(r.ManifestPath, "manifest_path"); err != nil {
		return r, err
	}
	if r.MarketSession.IsZero() || r.DecisionAt.IsZero() {
		return r, errors.New("market_session and decision_at are required")
	}
	r.MarketSession = time.Date(r.MarketSession.Year(), r.MarketSession.Month(), r.MarketSession.Day(), 0, 0, 0, 0, time.UTC)
	r.DecisionAt = r.DecisionAt.UTC()
	if r.CandidateCount < 0 || r.EligibleCount < 0 || r.RejectedCount < 0 || r.CandidateCount > r.EligibleCount {
		return r, errors.New("discovery counts are invalid")
	}
	if len(r.Rankings) != r.EligibleCount+r.RejectedCount {
		return r, errors.New("discovery row count differs from eligible plus rejected counts")
	}
	rows := append([]DiscoveryRanking(nil), r.Rankings...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].SecurityID < rows[j].SecurityID })
	seenSecurity := map[string]bool{}
	seenRank := map[int]bool{}
	candidates, eligible, rejected := 0, 0, 0
	for index := range rows {
		row := &rows[index]
		row.SecurityID = strings.TrimSpace(row.SecurityID)
		row.Ticker = strings.TrimSpace(row.Ticker)
		row.Sector = strings.TrimSpace(row.Sector)
		row.EligibilityStatus = strings.TrimSpace(row.EligibilityStatus)
		if _, err := uuid.Parse(row.SecurityID); err != nil || seenSecurity[row.SecurityID] {
			return r, fmt.Errorf("discovery row has invalid or duplicate security_id %q", row.SecurityID)
		}
		seenSecurity[row.SecurityID] = true
		if !discoveryTickerPattern.MatchString(row.Ticker) || !featureArtifactIdentifierPattern.MatchString(row.Sector) {
			return r, fmt.Errorf("discovery row %s has invalid ticker or sector", row.SecurityID)
		}
		if row.EligibilityStatus == "eligible" {
			eligible++
			if row.Rank == nil || row.Score == nil || *row.Rank <= 0 || seenRank[*row.Rank] {
				return r, fmt.Errorf("eligible discovery row %s has invalid rank or score", row.SecurityID)
			}
			seenRank[*row.Rank] = true
		} else if row.EligibilityStatus == "rejected" {
			rejected++
			if row.Rank != nil || row.Score != nil || row.Candidate {
				return r, fmt.Errorf("rejected discovery row %s has rank, score, or candidate flag", row.SecurityID)
			}
		} else {
			return r, fmt.Errorf("discovery row %s has unsupported eligibility", row.SecurityID)
		}
		if row.Candidate {
			candidates++
		}
	}
	for rank := 1; rank <= eligible; rank++ {
		if !seenRank[rank] {
			return r, errors.New("eligible discovery ranks must be contiguous")
		}
	}
	if candidates != r.CandidateCount || eligible != r.EligibleCount || rejected != r.RejectedCount {
		return r, errors.New("discovery summary counts differ from rows")
	}
	r.Rankings = rows
	return r, nil
}

func discoveryRegistrationSHA256(r DiscoveryIndexRegistration) (string, error) {
	value, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:]), nil
}

func (r *Repository) RegisterDiscoveryIndex(ctx context.Context, registration DiscoveryIndexRegistration) (DiscoveryIndexRegistrationResult, error) {
	normalized, err := registration.normalized()
	if err != nil {
		return DiscoveryIndexRegistrationResult{}, err
	}
	digest, err := discoveryRegistrationSHA256(normalized)
	if err != nil {
		return DiscoveryIndexRegistrationResult{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return DiscoveryIndexRegistrationResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var stored string
	err = tx.QueryRow(ctx, `
INSERT INTO discovery_indexes (
  discovery_id, model_name, model_version, git_commit, universe_id, universe_name,
  universe_fingerprint, market_session, decision_at, feature_batch_id,
  feature_input_fingerprint, candidate_count, eligible_count, rejected_count,
  manifest_path, manifest_sha256, registration_sha256
) VALUES ($1::uuid,$2,$3,$4,$5::uuid,$6,$7,$8,$9,$10::uuid,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT (discovery_id) DO NOTHING RETURNING registration_sha256`,
		normalized.DiscoveryID, normalized.ModelName, normalized.ModelVersion, normalized.GitCommit,
		normalized.UniverseID, normalized.UniverseName, normalized.UniverseFingerprint,
		normalized.MarketSession, normalized.DecisionAt, normalized.FeatureBatchID,
		normalized.FeatureInputFingerprint, normalized.CandidateCount, normalized.EligibleCount,
		normalized.RejectedCount, normalized.ManifestPath, normalized.ManifestSHA256, digest,
	).Scan(&stored)
	already := false
	if errors.Is(err, pgx.ErrNoRows) {
		already = true
		if err = tx.QueryRow(ctx, `SELECT registration_sha256 FROM discovery_indexes WHERE discovery_id=$1::uuid`, normalized.DiscoveryID).Scan(&stored); err != nil {
			return DiscoveryIndexRegistrationResult{}, err
		}
		if stored != digest {
			return DiscoveryIndexRegistrationResult{}, errors.New("discovery index conflicts with immutable registration")
		}
	} else if err != nil {
		return DiscoveryIndexRegistrationResult{}, err
	}
	if !already {
		for _, row := range normalized.Rankings {
			_, err = tx.Exec(ctx, `
INSERT INTO discovery_rankings (
  discovery_id, security_id, rank, ticker, sector, candidate, score,
  return_1m, return_3m, return_6m, return_12m, realized_volatility_1m,
  max_drawdown_1m, eligibility_status, rejection_reasons
) VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6,$7::numeric,$8::numeric,$9::numeric,$10::numeric,$11::numeric,$12::numeric,$13::numeric,$14,$15)`,
				normalized.DiscoveryID, row.SecurityID, row.Rank, row.Ticker, row.Sector,
				row.Candidate, row.Score, row.Return1M, row.Return3M, row.Return6M,
				row.Return12M, row.RealizedVolatility1M, row.MaxDrawdown1M,
				row.EligibilityStatus, row.RejectionReasons,
			)
			if err != nil {
				return DiscoveryIndexRegistrationResult{}, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return DiscoveryIndexRegistrationResult{}, err
	}
	return DiscoveryIndexRegistrationResult{DiscoveryID: normalized.DiscoveryID, RegistrationSHA256: digest, AlreadyPresent: already}, nil
}
