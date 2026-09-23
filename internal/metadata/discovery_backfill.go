package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	DiscoveryBackfillSchemaVersion      = "1.0.0"
	DiscoveryBackfillGeneratorVersion   = "python-retrospective-discovery-1.0.1"
	discoveryBackfillObservationMode    = "retrospective_current_universe"
	discoveryBackfillUniverseBasis      = "current_profile_membership_snapshot"
	discoveryBackfillInputFitness       = "installation_replay_only"
	discoveryBackfillAvailabilityPolicy = "current_vintage_with_preserved_receipt_times"
	discoveryBackfillMissingBarReason   = "missing_price_bar_for_market_session"
)

var (
	discoveryBackfillHashPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	discoveryBackfillTickerPattern   = regexp.MustCompile(`^[A-Z0-9.-]{1,16}$`)
	discoveryBackfillCurrencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
	discoveryBackfillGitPattern      = regexp.MustCompile(`^(unknown|[0-9a-f]{40})$`)
	discoveryBackfillNumberPattern   = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)
	discoveryBackfillNamespace       = uuid.MustParse("dfd74827-a6cb-5a1a-821d-22bbd8a06d5e")
)

// DiscoveryBackfillNumber preserves JSON numeric values for PostgreSQL numeric
// columns. The producer writes feature metrics as strings and OHLC values as
// JSON numbers, so both representations are accepted and normalized to text.
type DiscoveryBackfillNumber struct {
	text  string
	valid bool
}

func (n *DiscoveryBackfillNumber) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "null" {
		n.text, n.valid = "", false
		return nil
	}
	var value string
	if strings.HasPrefix(trimmed, `"`) {
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
	} else {
		value = trimmed
	}
	if !discoveryBackfillNumberPattern.MatchString(value) {
		return fmt.Errorf("%q is not a finite decimal", value)
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
		return fmt.Errorf("%q is not a finite decimal", value)
	}
	n.text, n.valid = value, true
	return nil
}

func (n DiscoveryBackfillNumber) MarshalJSON() ([]byte, error) {
	if !n.valid {
		return []byte("null"), nil
	}
	return json.Marshal(n.text)
}

func (n DiscoveryBackfillNumber) sqlText() *string {
	if !n.valid {
		return nil
	}
	value := n.text
	return &value
}

func nullableBackfillNumberSQLText(n *DiscoveryBackfillNumber) *string {
	if n == nil {
		return nil
	}
	return n.sqlText()
}

type DiscoveryBackfillManifest struct {
	SchemaVersion       string                       `json:"schema_version"`
	GeneratorVersion    string                       `json:"generator_version"`
	ReconstructionRunID string                       `json:"reconstruction_run_id"`
	ObservationMode     string                       `json:"observation_mode"`
	UniverseBasis       string                       `json:"universe_basis"`
	ReconstructedAt     string                       `json:"reconstructed_at"`
	InputFingerprint    string                       `json:"input_fingerprint"`
	Profile             DiscoveryBackfillProfile     `json:"profile"`
	Model               DiscoveryBackfillModel       `json:"model"`
	Range               DiscoveryBackfillRange       `json:"range"`
	Calendar            DiscoveryBackfillCalendar    `json:"calendar"`
	PriceInputs         DiscoveryBackfillPriceInputs `json:"price_inputs"`
	Sessions            []DiscoveryBackfillSession   `json:"sessions"`
}

type DiscoveryBackfillProfile struct {
	UniverseID     string `json:"universe_id"`
	Name           string `json:"name"`
	MembershipAsOf string `json:"membership_as_of"`
	Fingerprint    string `json:"fingerprint"`
	ProfileSHA256  string `json:"profile_sha256"`
	Size           int    `json:"size"`
	SnapshotPath   string `json:"snapshot_path"`
}

type DiscoveryBackfillModel struct {
	Name      string            `json:"name"`
	Version   string            `json:"version"`
	GitCommit string            `json:"git_commit"`
	Weights   map[string]string `json:"weights"`
}

type DiscoveryBackfillRange struct {
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

type DiscoveryBackfillCalendar struct {
	DataSourceID       string `json:"data_source_id"`
	MIC                string `json:"mic"`
	CalendarVersion    string `json:"calendar_version"`
	SessionCount       int    `json:"session_count"`
	SessionFingerprint string `json:"session_fingerprint"`
	AvailableAt        string `json:"available_at"`
	SnapshotSHA256     string `json:"snapshot_sha256"`
	SnapshotPath       string `json:"snapshot_path"`
}

type DiscoveryBackfillPriceInputs struct {
	Source             string                         `json:"source"`
	Interval           string                         `json:"interval"`
	PriceBasis         string                         `json:"price_basis"`
	Currency           string                         `json:"currency"`
	HistoricalFitness  string                         `json:"historical_fitness"`
	AvailabilityPolicy string                         `json:"availability_policy"`
	InputFingerprint   string                         `json:"input_fingerprint"`
	SelectedRowCount   int                            `json:"selected_row_count"`
	Manifests          []DiscoveryBackfillManifestRef `json:"manifests"`
}

type DiscoveryBackfillManifestRef struct {
	Path       string                     `json:"path"`
	SourcePath string                     `json:"source_path"`
	SHA256     string                     `json:"sha256"`
	Parts      []DiscoveryBackfillPartRef `json:"parts"`
}

type DiscoveryBackfillPartRef struct {
	Path       string `json:"path"`
	SourcePath string `json:"source_path"`
	SHA256     string `json:"sha256"`
}

type DiscoveryBackfillSession struct {
	IndexID               string                      `json:"index_id"`
	MarketSession         string                      `json:"market_session"`
	DecisionAt            string                      `json:"decision_at"`
	CandidateCount        int                         `json:"candidate_count"`
	EligibleCount         int                         `json:"eligible_count"`
	RejectedCount         int                         `json:"rejected_count"`
	PriceInputFingerprint string                      `json:"price_input_fingerprint"`
	Rankings              []DiscoveryBackfillRanking  `json:"rankings"`
	PriceBars             []DiscoveryBackfillPriceBar `json:"price_bars"`
}

type DiscoveryBackfillRanking struct {
	Rank        *int                         `json:"rank"`
	SecurityID  string                       `json:"security_id"`
	Ticker      string                       `json:"ticker"`
	Sector      string                       `json:"sector"`
	Candidate   bool                         `json:"candidate"`
	Score       *DiscoveryBackfillNumber     `json:"score"`
	Features    DiscoveryBackfillFeatures    `json:"features"`
	Eligibility DiscoveryBackfillEligibility `json:"eligibility"`
}

type DiscoveryBackfillFeatures struct {
	Return1M             *DiscoveryBackfillNumber `json:"return_1m"`
	Return3M             *DiscoveryBackfillNumber `json:"return_3m"`
	Return6M             *DiscoveryBackfillNumber `json:"return_6m"`
	Return12M            *DiscoveryBackfillNumber `json:"return_12m"`
	RealizedVolatility1M *DiscoveryBackfillNumber `json:"realized_volatility_1m"`
	MaxDrawdown1M        *DiscoveryBackfillNumber `json:"max_drawdown_1m"`
}

type DiscoveryBackfillEligibility struct {
	Status  string   `json:"status"`
	Reasons []string `json:"reasons"`
}

type DiscoveryBackfillPriceBar struct {
	SecurityID         string                   `json:"security_id"`
	Ticker             string                   `json:"ticker"`
	ObservedAt         string                   `json:"observed_at"`
	AvailableAt        string                   `json:"available_at"`
	IngestedAt         string                   `json:"ingested_at"`
	Source             string                   `json:"source"`
	Interval           string                   `json:"interval"`
	PriceBasis         string                   `json:"price_basis"`
	Currency           string                   `json:"currency"`
	Open               DiscoveryBackfillNumber  `json:"open"`
	High               DiscoveryBackfillNumber  `json:"high"`
	Low                DiscoveryBackfillNumber  `json:"low"`
	Close              DiscoveryBackfillNumber  `json:"close"`
	Volume             *DiscoveryBackfillNumber `json:"volume"`
	RawPayloadHash     string                   `json:"raw_payload_hash"`
	DataSourceID       string                   `json:"data_source_id"`
	IngestionRunID     string                   `json:"ingestion_run_id"`
	RawRecordLocator   string                   `json:"raw_record_locator"`
	ManifestPath       string                   `json:"manifest_path"`
	SourceManifestPath string                   `json:"source_manifest_path"`
	ManifestSHA256     string                   `json:"manifest_sha256"`
	PartPath           string                   `json:"part_path"`
	SourcePartPath     string                   `json:"source_part_path"`
	PartSHA256         string                   `json:"part_sha256"`
}

// DiscoveryBackfillRegistration adds the validated location and exact-byte
// digest of the top-level manifest to its decoded contents.
type DiscoveryBackfillRegistration struct {
	Manifest       DiscoveryBackfillManifest `json:"manifest"`
	ManifestPath   string                    `json:"manifest_path"`
	ManifestSHA256 string                    `json:"manifest_sha256"`
}

type DiscoveryBackfillRegistrationResult struct {
	ReconstructionRunID string
	RegistrationSHA256  string
	AlreadyPresent      bool
}

func (r DiscoveryBackfillRegistration) normalized() (DiscoveryBackfillRegistration, error) {
	r.ManifestPath = strings.TrimSpace(r.ManifestPath)
	r.ManifestSHA256 = strings.TrimSpace(r.ManifestSHA256)
	if err := requireBackfillPath(r.ManifestPath, "manifest_path"); err != nil {
		return r, err
	}
	if err := requireBackfillHash(r.ManifestSHA256, "manifest_sha256"); err != nil {
		return r, err
	}
	m := &r.Manifest
	if m.SchemaVersion != DiscoveryBackfillSchemaVersion {
		return r, fmt.Errorf("unsupported retrospective backfill schema_version %q", m.SchemaVersion)
	}
	if m.GeneratorVersion != DiscoveryBackfillGeneratorVersion {
		return r, fmt.Errorf("unsupported retrospective backfill generator_version %q", m.GeneratorVersion)
	}
	if m.ObservationMode != discoveryBackfillObservationMode || m.UniverseBasis != discoveryBackfillUniverseBasis {
		return r, errors.New("retrospective backfill observation labels are invalid")
	}
	var err error
	if m.ReconstructionRunID, err = normalizeBackfillUUID(m.ReconstructionRunID, "reconstruction_run_id"); err != nil {
		return r, err
	}
	if err := requireBackfillHash(m.InputFingerprint, "input_fingerprint"); err != nil {
		return r, err
	}
	expectedID := uuid.NewSHA1(discoveryBackfillNamespace, []byte(m.InputFingerprint)).String()
	if m.ReconstructionRunID != expectedID {
		return r, errors.New("reconstruction_run_id does not match input_fingerprint")
	}
	if m.ReconstructedAt, err = normalizeBackfillTimestamp(m.ReconstructedAt, "reconstructed_at"); err != nil {
		return r, err
	}
	if m.Profile.UniverseID, err = normalizeBackfillUUID(m.Profile.UniverseID, "profile.universe_id"); err != nil {
		return r, err
	}
	m.Profile.Name = strings.TrimSpace(m.Profile.Name)
	if !featureArtifactIdentifierPattern.MatchString(m.Profile.Name) {
		return r, errors.New("profile.name must be a slug")
	}
	if m.Profile.MembershipAsOf, err = normalizeBackfillDate(m.Profile.MembershipAsOf, "profile.membership_as_of"); err != nil {
		return r, err
	}
	if err := requireBackfillHash(m.Profile.Fingerprint, "profile.fingerprint"); err != nil {
		return r, err
	}
	if err := requireBackfillHash(m.Profile.ProfileSHA256, "profile.profile_sha256"); err != nil {
		return r, err
	}
	if m.Profile.Size <= 0 {
		return r, errors.New("profile.size must be positive")
	}
	if err := requireBackfillPath(m.Profile.SnapshotPath, "profile.snapshot_path"); err != nil {
		return r, err
	}
	m.Model.Name = strings.TrimSpace(m.Model.Name)
	m.Model.Version = strings.TrimSpace(m.Model.Version)
	m.Model.GitCommit = strings.TrimSpace(m.Model.GitCommit)
	if !featureArtifactIdentifierPattern.MatchString(m.Model.Name) || !featureArtifactVersionPattern.MatchString(m.Model.Version) || !discoveryBackfillGitPattern.MatchString(m.Model.GitCommit) {
		return r, errors.New("model name, version, or git_commit is invalid")
	}
	if len(m.Model.Weights) == 0 {
		return r, errors.New("model.weights must not be empty")
	}
	for name, text := range m.Model.Weights {
		if !featureArtifactIdentifierPattern.MatchString(name) || !validBackfillNumber(text) {
			return r, fmt.Errorf("model weight %q is invalid", name)
		}
		m.Model.Weights[name] = strings.TrimSpace(text)
	}
	if m.Range.StartDate, err = normalizeBackfillDate(m.Range.StartDate, "range.start_date"); err != nil {
		return r, err
	}
	if m.Range.EndDate, err = normalizeBackfillDate(m.Range.EndDate, "range.end_date"); err != nil {
		return r, err
	}
	periodStart, _ := time.Parse("2006-01-02", m.Range.StartDate)
	periodEnd, _ := time.Parse("2006-01-02", m.Range.EndDate)
	if periodStart.After(periodEnd) {
		return r, errors.New("range.start_date must not be after range.end_date")
	}
	if m.Calendar.DataSourceID, err = normalizeBackfillUUID(m.Calendar.DataSourceID, "calendar.data_source_id"); err != nil {
		return r, err
	}
	if m.Calendar.MIC != "XNAS" {
		return r, errors.New("calendar.mic must be XNAS")
	}
	m.Calendar.CalendarVersion = strings.TrimSpace(m.Calendar.CalendarVersion)
	if !featureArtifactIdentifierPattern.MatchString(m.Calendar.CalendarVersion) {
		return r, errors.New("calendar.calendar_version is invalid")
	}
	if m.Calendar.SessionCount <= 0 {
		return r, errors.New("calendar.session_count must be positive")
	}
	if err := requireBackfillHash(m.Calendar.SessionFingerprint, "calendar.session_fingerprint"); err != nil {
		return r, err
	}
	if m.Calendar.AvailableAt, err = normalizeBackfillTimestamp(m.Calendar.AvailableAt, "calendar.available_at"); err != nil {
		return r, err
	}
	if err := requireBackfillHash(m.Calendar.SnapshotSHA256, "calendar.snapshot_sha256"); err != nil {
		return r, err
	}
	if err := requireBackfillPath(m.Calendar.SnapshotPath, "calendar.snapshot_path"); err != nil {
		return r, err
	}
	p := &m.PriceInputs
	p.Source = strings.TrimSpace(p.Source)
	p.Interval = strings.TrimSpace(p.Interval)
	p.PriceBasis = strings.TrimSpace(p.PriceBasis)
	p.Currency = strings.TrimSpace(p.Currency)
	if !featureArtifactIdentifierPattern.MatchString(p.Source) || p.Interval == "" || strings.ContainsAny(p.Interval, " /\\\x00") {
		return r, errors.New("price_inputs source or interval is invalid")
	}
	if p.PriceBasis != "raw" && p.PriceBasis != "split_adjusted" && p.PriceBasis != "total_return_adjusted" {
		return r, errors.New("price_inputs.price_basis is invalid")
	}
	if !discoveryBackfillCurrencyPattern.MatchString(p.Currency) {
		return r, errors.New("price_inputs.currency must be three uppercase letters")
	}
	if p.HistoricalFitness != discoveryBackfillInputFitness || p.AvailabilityPolicy != discoveryBackfillAvailabilityPolicy {
		return r, errors.New("price_inputs historical fitness labels are invalid")
	}
	if err := requireBackfillHash(p.InputFingerprint, "price_inputs.input_fingerprint"); err != nil {
		return r, err
	}
	if p.SelectedRowCount < 0 || len(p.Manifests) == 0 {
		return r, errors.New("price_inputs selected_row_count or manifests are invalid")
	}
	manifestByPath := make(map[string]DiscoveryBackfillManifestRef, len(p.Manifests))
	sourcePathSeen := map[string]bool{}
	for i := range p.Manifests {
		ref := &p.Manifests[i]
		if err := requireBackfillPath(ref.Path, "price_inputs.manifests.path"); err != nil {
			return r, err
		}
		if err := requireBackfillPath(ref.SourcePath, "price_inputs.manifests.source_path"); err != nil {
			return r, err
		}
		if err := requireBackfillHash(ref.SHA256, "price_inputs.manifests.sha256"); err != nil {
			return r, err
		}
		if _, ok := manifestByPath[ref.Path]; ok || sourcePathSeen[ref.SourcePath] {
			return r, errors.New("price_inputs manifests contain duplicate paths")
		}
		if len(ref.Parts) == 0 {
			return r, fmt.Errorf("price_inputs manifest %s has no parts", ref.Path)
		}
		partByPath := map[string]DiscoveryBackfillPartRef{}
		for j := range ref.Parts {
			part := &ref.Parts[j]
			if err := requireBackfillPath(part.Path, "price_inputs.manifests.parts.path"); err != nil {
				return r, err
			}
			if err := requireBackfillPath(part.SourcePath, "price_inputs.manifests.parts.source_path"); err != nil {
				return r, err
			}
			if err := requireBackfillHash(part.SHA256, "price_inputs.manifests.parts.sha256"); err != nil {
				return r, err
			}
			if _, ok := partByPath[part.Path]; ok {
				return r, fmt.Errorf("price_inputs manifest %s repeats part %s", ref.Path, part.Path)
			}
			partByPath[part.Path] = *part
		}
		sort.Slice(ref.Parts, func(a, b int) bool { return ref.Parts[a].Path < ref.Parts[b].Path })
		manifestByPath[ref.Path] = *ref
		sourcePathSeen[ref.SourcePath] = true
	}
	if len(m.Sessions) == 0 {
		return r, errors.New("sessions must not be empty")
	}
	indexIDs := map[string]bool{}
	lastSession := ""
	for i := range m.Sessions {
		session := &m.Sessions[i]
		if session.IndexID, err = normalizeBackfillUUID(session.IndexID, "session.index_id"); err != nil {
			return r, err
		}
		if indexIDs[session.IndexID] {
			return r, errors.New("sessions contain duplicate index_id")
		}
		indexIDs[session.IndexID] = true
		if session.MarketSession, err = normalizeBackfillDate(session.MarketSession, "session.market_session"); err != nil {
			return r, err
		}
		if lastSession != "" && session.MarketSession <= lastSession {
			return r, errors.New("sessions must be unique and date ordered")
		}
		lastSession = session.MarketSession
		if session.MarketSession < m.Range.StartDate || session.MarketSession > m.Range.EndDate {
			return r, fmt.Errorf("session %s is outside the requested range", session.MarketSession)
		}
		if session.DecisionAt, err = normalizeBackfillTimestamp(session.DecisionAt, "session.decision_at"); err != nil {
			return r, err
		}
		if err := requireBackfillHash(session.PriceInputFingerprint, "session.price_input_fingerprint"); err != nil {
			return r, err
		}
		if session.CandidateCount < 0 || session.EligibleCount < 0 || session.RejectedCount < 0 || session.CandidateCount > session.EligibleCount {
			return r, fmt.Errorf("session %s has invalid counts", session.MarketSession)
		}
		if len(session.Rankings) != session.EligibleCount+session.RejectedCount || len(session.Rankings) != m.Profile.Size {
			return r, fmt.Errorf("session %s counts differ from ranking rows", session.MarketSession)
		}
		seenSecurity, seenRanks := map[string]bool{}, map[int]bool{}
		candidates, eligible, rejected := 0, 0, 0
		for j := range session.Rankings {
			row := &session.Rankings[j]
			if row.SecurityID, err = normalizeBackfillUUID(row.SecurityID, "ranking.security_id"); err != nil {
				return r, err
			}
			if seenSecurity[row.SecurityID] {
				return r, fmt.Errorf("session %s contains duplicate ranking security %s", session.MarketSession, row.SecurityID)
			}
			seenSecurity[row.SecurityID] = true
			row.Ticker = strings.TrimSpace(row.Ticker)
			row.Sector = strings.TrimSpace(row.Sector)
			if !discoveryBackfillTickerPattern.MatchString(row.Ticker) || !featureArtifactIdentifierPattern.MatchString(row.Sector) {
				return r, fmt.Errorf("ranking %s ticker or sector is invalid", row.SecurityID)
			}
			row.Eligibility.Status = strings.TrimSpace(row.Eligibility.Status)
			if row.Eligibility.Reasons == nil {
				row.Eligibility.Reasons = []string{}
			}
			switch row.Eligibility.Status {
			case "eligible":
				eligible++
				if row.Rank == nil || *row.Rank <= 0 || row.Score == nil || !row.Score.valid || seenRanks[*row.Rank] {
					return r, fmt.Errorf("eligible ranking %s has invalid rank or score", row.SecurityID)
				}
				if !backfillFeaturesPresent(row.Features) {
					return r, fmt.Errorf("eligible ranking %s has missing feature values", row.SecurityID)
				}
				seenRanks[*row.Rank] = true
			case "rejected":
				rejected++
				if row.Rank != nil || row.Score != nil || row.Candidate {
					return r, fmt.Errorf("rejected ranking %s has rank, score, or candidate flag", row.SecurityID)
				}
			default:
				return r, fmt.Errorf("ranking %s has unsupported eligibility status", row.SecurityID)
			}
			if row.Candidate {
				candidates++
			}
		}
		for rank := 1; rank <= eligible; rank++ {
			if !seenRanks[rank] {
				return r, fmt.Errorf("session %s ranks are not contiguous", session.MarketSession)
			}
		}
		if candidates != session.CandidateCount || eligible != session.EligibleCount || rejected != session.RejectedCount {
			return r, fmt.Errorf("session %s summary counts differ from ranking rows", session.MarketSession)
		}
		sort.Slice(session.Rankings, func(a, b int) bool { return session.Rankings[a].SecurityID < session.Rankings[b].SecurityID })
		if len(session.PriceBars) > m.Profile.Size {
			return r, fmt.Errorf("session %s has more price bars than profile securities", session.MarketSession)
		}
		seenPrice, rankedPrice := map[string]bool{}, map[string]string{}
		for _, row := range session.Rankings {
			rankedPrice[row.SecurityID] = row.Ticker
		}
		for j := range session.PriceBars {
			bar := &session.PriceBars[j]
			if bar.SecurityID, err = normalizeBackfillUUID(bar.SecurityID, "price_bar.security_id"); err != nil {
				return r, err
			}
			if seenPrice[bar.SecurityID] || rankedPrice[bar.SecurityID] == "" {
				return r, fmt.Errorf("session %s has duplicate or unranked price bar %s", session.MarketSession, bar.SecurityID)
			}
			seenPrice[bar.SecurityID] = true
			bar.Ticker = strings.TrimSpace(bar.Ticker)
			if !discoveryBackfillTickerPattern.MatchString(bar.Ticker) {
				return r, fmt.Errorf("price bar %s has invalid ticker", bar.SecurityID)
			}
			if rankedPrice[bar.SecurityID] != bar.Ticker {
				return r, fmt.Errorf("price bar %s ticker differs from its ranking", bar.SecurityID)
			}
			if bar.Source != p.Source || bar.Interval != p.Interval || bar.PriceBasis != p.PriceBasis || bar.Currency != p.Currency {
				return r, fmt.Errorf("price bar %s disagrees with price_inputs", bar.SecurityID)
			}
			if bar.ObservedAt, err = normalizeBackfillTimestamp(bar.ObservedAt, "price_bar.observed_at"); err != nil {
				return r, err
			}
			if bar.AvailableAt, err = normalizeBackfillTimestamp(bar.AvailableAt, "price_bar.available_at"); err != nil {
				return r, err
			}
			if bar.IngestedAt, err = normalizeBackfillTimestamp(bar.IngestedAt, "price_bar.ingested_at"); err != nil {
				return r, err
			}
			observed, _ := time.Parse(time.RFC3339Nano, bar.ObservedAt)
			available, _ := time.Parse(time.RFC3339Nano, bar.AvailableAt)
			ingested, _ := time.Parse(time.RFC3339Nano, bar.IngestedAt)
			if observed.After(available) || available.After(ingested) {
				return r, fmt.Errorf("price bar %s has invalid observation and receipt times", bar.SecurityID)
			}
			for _, item := range []struct {
				number DiscoveryBackfillNumber
				name   string
			}{{bar.Open, "open"}, {bar.High, "high"}, {bar.Low, "low"}, {bar.Close, "close"}} {
				if !item.number.valid || !validNonnegativeBackfillNumber(item.number.text) {
					return r, fmt.Errorf("price bar %s has invalid %s value", bar.SecurityID, item.name)
				}
			}
			open, _ := strconv.ParseFloat(bar.Open.text, 64)
			high, _ := strconv.ParseFloat(bar.High.text, 64)
			low, _ := strconv.ParseFloat(bar.Low.text, 64)
			closeValue, _ := strconv.ParseFloat(bar.Close.text, 64)
			if low > math.Min(open, closeValue) || high < math.Max(open, closeValue) || low > high {
				return r, fmt.Errorf("price bar %s violates OHLC ordering", bar.SecurityID)
			}
			if bar.Volume != nil && (!bar.Volume.valid || !validNonnegativeBackfillNumber(bar.Volume.text)) {
				return r, fmt.Errorf("price bar %s has invalid volume", bar.SecurityID)
			}
			if err := requireBackfillHash(bar.RawPayloadHash, "price_bar.raw_payload_hash"); err != nil {
				return r, err
			}
			if bar.DataSourceID, err = normalizeBackfillUUID(bar.DataSourceID, "price_bar.data_source_id"); err != nil {
				return r, err
			}
			if bar.IngestionRunID, err = normalizeBackfillUUID(bar.IngestionRunID, "price_bar.ingestion_run_id"); err != nil {
				return r, err
			}
			if strings.TrimSpace(bar.RawRecordLocator) == "" {
				return r, fmt.Errorf("price bar %s raw_record_locator is required", bar.SecurityID)
			}
			bar.RawRecordLocator = strings.TrimSpace(bar.RawRecordLocator)
			if err := requireBackfillPath(bar.ManifestPath, "price_bar.manifest_path"); err != nil {
				return r, err
			}
			if err := requireBackfillPath(bar.SourceManifestPath, "price_bar.source_manifest_path"); err != nil {
				return r, err
			}
			if err := requireBackfillHash(bar.ManifestSHA256, "price_bar.manifest_sha256"); err != nil {
				return r, err
			}
			if err := requireBackfillPath(bar.PartPath, "price_bar.part_path"); err != nil {
				return r, err
			}
			if err := requireBackfillPath(bar.SourcePartPath, "price_bar.source_part_path"); err != nil {
				return r, err
			}
			if err := requireBackfillHash(bar.PartSHA256, "price_bar.part_sha256"); err != nil {
				return r, err
			}
			ref, ok := manifestByPath[bar.ManifestPath]
			if !ok || ref.SourcePath != bar.SourceManifestPath || ref.SHA256 != bar.ManifestSHA256 {
				return r, fmt.Errorf("price bar %s does not reference its archived price manifest", bar.SecurityID)
			}
			partFound := false
			for _, part := range ref.Parts {
				if part.Path == bar.PartPath && part.SourcePath == bar.SourcePartPath && part.SHA256 == bar.PartSHA256 {
					partFound = true
					break
				}
			}
			if !partFound {
				return r, fmt.Errorf("price bar %s does not reference a selected manifest part", bar.SecurityID)
			}
		}
		for _, row := range session.Rankings {
			hasMissingBarReason := false
			for _, reason := range row.Eligibility.Reasons {
				if reason == discoveryBackfillMissingBarReason {
					hasMissingBarReason = true
					break
				}
			}
			hasPriceBar := seenPrice[row.SecurityID]
			if !hasPriceBar && (row.Eligibility.Status != "rejected" || !hasMissingBarReason) {
				return r, fmt.Errorf("ranking %s has no price bar but is not rejected for a missing session bar", row.SecurityID)
			}
			if hasPriceBar && hasMissingBarReason {
				return r, fmt.Errorf("ranking %s reports a missing session bar that is present", row.SecurityID)
			}
		}
		sort.Slice(session.PriceBars, func(a, b int) bool { return session.PriceBars[a].SecurityID < session.PriceBars[b].SecurityID })
	}
	return r, nil
}

// ValidateDiscoveryBackfillRegistration applies the catalog contract checks and
// returns a normalized copy suitable for path validation and registration.
func ValidateDiscoveryBackfillRegistration(registration DiscoveryBackfillRegistration) (DiscoveryBackfillRegistration, error) {
	return registration.normalized()
}

func validBackfillNumber(value string) bool {
	value = strings.TrimSpace(value)
	if !discoveryBackfillNumberPattern.MatchString(value) {
		return false
	}
	parsed, err := strconv.ParseFloat(value, 64)
	return err == nil && !math.IsInf(parsed, 0) && !math.IsNaN(parsed)
}

func backfillFeaturesPresent(features DiscoveryBackfillFeatures) bool {
	for _, value := range []*DiscoveryBackfillNumber{
		features.Return1M, features.Return3M, features.Return6M, features.Return12M,
		features.RealizedVolatility1M, features.MaxDrawdown1M,
	} {
		if value == nil || !value.valid {
			return false
		}
	}
	return true
}

func validNonnegativeBackfillNumber(value string) bool {
	parsed, err := strconv.ParseFloat(value, 64)
	return err == nil && !math.IsInf(parsed, 0) && !math.IsNaN(parsed) && parsed >= 0
}

func requireBackfillHash(value, label string) error {
	if !discoveryBackfillHashPattern.MatchString(value) {
		return fmt.Errorf("%s must be a lower-case SHA-256", label)
	}
	return nil
}

func requireBackfillPath(value, label string) error {
	if err := requireFeatureArtifactPath(value, label); err != nil {
		return err
	}
	if strings.Contains(value, "..") {
		return fmt.Errorf("%s contains an unsafe path component", label)
	}
	return nil
}

func normalizeBackfillUUID(value, label string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.String() != value {
		return "", fmt.Errorf("%s must be a lower-case canonical UUID", label)
	}
	return value, nil
}

func normalizeBackfillDate(value, label string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil || parsed.Format("2006-01-02") != value {
		return "", fmt.Errorf("%s must be an ISO date", label)
	}
	return value, nil
}

func normalizeBackfillTimestamp(value, label string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", fmt.Errorf("%s must be an ISO timestamp with an offset: %w", label, err)
	}
	return parsed.UTC().Format(time.RFC3339Nano), nil
}

func discoveryBackfillRegistrationSHA256(r DiscoveryBackfillRegistration) (string, error) {
	encoded, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("encode retrospective backfill registration: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func discoveryBackfillSessionSHA256(session DiscoveryBackfillSession) (string, error) {
	encoded, err := json.Marshal(session)
	if err != nil {
		return "", fmt.Errorf("encode retrospective backfill session: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (r *Repository) RegisterRetrospectiveDiscoveryBackfill(ctx context.Context, registration DiscoveryBackfillRegistration) (DiscoveryBackfillRegistrationResult, error) {
	if r == nil || r.pool == nil {
		return DiscoveryBackfillRegistrationResult{}, errors.New("database repository is required")
	}
	normalized, err := registration.normalized()
	if err != nil {
		return DiscoveryBackfillRegistrationResult{}, err
	}
	digest, err := discoveryBackfillRegistrationSHA256(normalized)
	if err != nil {
		return DiscoveryBackfillRegistrationResult{}, err
	}
	m := normalized.Manifest
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return DiscoveryBackfillRegistrationResult{}, err
	}
	defer tx.Rollback(ctx)
	var stored string
	err = tx.QueryRow(ctx, `
INSERT INTO discovery_backfill_runs (
  reconstruction_run_id, model_name, model_version, generator_version, git_commit, universe_id, universe_name,
  universe_fingerprint, membership_as_of, profile_sha256, profile_snapshot_path,
  observation_mode, universe_basis, period_start, period_end, reconstructed_at,
  calendar_data_source_id, calendar_mic, calendar_version, calendar_session_count, calendar_session_fingerprint,
  calendar_available_at, calendar_snapshot_path, calendar_snapshot_sha256,
  input_fitness, availability_policy, input_fingerprint, session_count,
  manifest_path, manifest_sha256, registration_sha256
) VALUES (
  $1::uuid,$2,$3,$4,$5,$6::uuid,$7,$8,$9::date,$10,$11,$12,$13,$14::date,$15::date,$16::timestamptz,
  $17::uuid,$18,$19,$20,$21,$22::timestamptz,$23,$24,$25,$26,$27,$28,$29,$30,$31
) ON CONFLICT DO NOTHING RETURNING registration_sha256`,
		m.ReconstructionRunID, m.Model.Name, m.Model.Version, m.GeneratorVersion, m.Model.GitCommit,
		m.Profile.UniverseID, m.Profile.Name, m.Profile.Fingerprint, m.Profile.MembershipAsOf,
		m.Profile.ProfileSHA256, m.Profile.SnapshotPath, m.ObservationMode, m.UniverseBasis,
		m.Range.StartDate, m.Range.EndDate, m.ReconstructedAt, m.Calendar.DataSourceID,
		m.Calendar.MIC, m.Calendar.CalendarVersion, m.Calendar.SessionCount,
		m.Calendar.SessionFingerprint, m.Calendar.AvailableAt, m.Calendar.SnapshotPath, m.Calendar.SnapshotSHA256,
		m.PriceInputs.HistoricalFitness, m.PriceInputs.AvailabilityPolicy, m.InputFingerprint,
		len(m.Sessions), normalized.ManifestPath, normalized.ManifestSHA256, digest,
	).Scan(&stored)
	if errors.Is(err, pgx.ErrNoRows) {
		var storedID string
		err = tx.QueryRow(ctx, `SELECT reconstruction_run_id::text, registration_sha256
FROM discovery_backfill_runs WHERE reconstruction_run_id=$1::uuid OR input_fingerprint=$2`,
			m.ReconstructionRunID, m.InputFingerprint).Scan(&storedID, &stored)
		if err != nil {
			return DiscoveryBackfillRegistrationResult{}, err
		}
		if storedID != m.ReconstructionRunID || stored != digest {
			return DiscoveryBackfillRegistrationResult{}, errors.New("retrospective backfill conflicts with an immutable registration")
		}
		if err := backfillExistingChildrenMatch(ctx, tx, normalized); err != nil {
			return DiscoveryBackfillRegistrationResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return DiscoveryBackfillRegistrationResult{}, err
		}
		return DiscoveryBackfillRegistrationResult{ReconstructionRunID: m.ReconstructionRunID, RegistrationSHA256: digest, AlreadyPresent: true}, nil
	}
	if err != nil {
		return DiscoveryBackfillRegistrationResult{}, err
	}
	if stored != digest {
		return DiscoveryBackfillRegistrationResult{}, errors.New("database returned an unexpected retrospective backfill registration hash")
	}
	for _, session := range m.Sessions {
		marketSession, _ := time.Parse("2006-01-02", session.MarketSession)
		decisionAt, _ := time.Parse(time.RFC3339Nano, session.DecisionAt)
		sessionDigest, err := discoveryBackfillSessionSHA256(session)
		if err != nil {
			return DiscoveryBackfillRegistrationResult{}, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO discovery_backfill_indexes (
  index_id, reconstruction_run_id, market_session, decision_at, candidate_count,
  eligible_count, rejected_count, price_input_fingerprint, session_manifest_sha256
) VALUES ($1::uuid,$2::uuid,$3::date,$4::timestamptz,$5,$6,$7,$8,$9)`,
			session.IndexID, m.ReconstructionRunID, marketSession, decisionAt,
			session.CandidateCount, session.EligibleCount, session.RejectedCount,
			session.PriceInputFingerprint, sessionDigest)
		if err != nil {
			return DiscoveryBackfillRegistrationResult{}, fmt.Errorf("insert backfill session %s: %w", session.MarketSession, err)
		}
		for _, row := range session.Rankings {
			_, err = tx.Exec(ctx, `INSERT INTO discovery_backfill_rankings (
  reconstruction_run_id, market_session, security_id, rank, ticker, sector,
  candidate, score, return_1m, return_3m, return_6m, return_12m,
  realized_volatility_1m, max_drawdown_1m, eligibility_status, rejection_reasons
) VALUES ($1::uuid,$2::date,$3::uuid,$4,$5,$6,$7,$8::numeric,$9::numeric,$10::numeric,$11::numeric,$12::numeric,$13::numeric,$14::numeric,$15,$16)`,
				m.ReconstructionRunID, marketSession, row.SecurityID, row.Rank, row.Ticker, row.Sector,
				row.Candidate, nullableBackfillNumberSQLText(row.Score), nullableBackfillNumberSQLText(row.Features.Return1M), nullableBackfillNumberSQLText(row.Features.Return3M),
				nullableBackfillNumberSQLText(row.Features.Return6M), nullableBackfillNumberSQLText(row.Features.Return12M),
				nullableBackfillNumberSQLText(row.Features.RealizedVolatility1M), nullableBackfillNumberSQLText(row.Features.MaxDrawdown1M),
				row.Eligibility.Status, row.Eligibility.Reasons)
			if err != nil {
				return DiscoveryBackfillRegistrationResult{}, fmt.Errorf("insert backfill ranking %s/%s: %w", session.MarketSession, row.SecurityID, err)
			}
		}
		for _, bar := range session.PriceBars {
			observedAt, _ := time.Parse(time.RFC3339Nano, bar.ObservedAt)
			availableAt, _ := time.Parse(time.RFC3339Nano, bar.AvailableAt)
			ingestedAt, _ := time.Parse(time.RFC3339Nano, bar.IngestedAt)
			_, err = tx.Exec(ctx, `INSERT INTO discovery_backfill_prices (
  reconstruction_run_id, market_session, security_id, ticker, observed_at, available_at,
  ingested_at, price_basis, currency, open_value, high_value, low_value, close_value,
  volume_value, raw_payload_hash, data_source_id, ingestion_run_id, raw_record_locator,
  manifest_path, source_manifest_path, manifest_sha256, part_path, source_part_path, part_sha256
) VALUES ($1::uuid,$2::date,$3::uuid,$4,$5::timestamptz,$6::timestamptz,$7::timestamptz,
  $8,$9,$10::numeric,$11::numeric,$12::numeric,$13::numeric,$14::numeric,$15,$16::uuid,
  $17::uuid,$18,$19,$20,$21,$22,$23,$24)`,
				m.ReconstructionRunID, marketSession, bar.SecurityID, bar.Ticker,
				observedAt, availableAt, ingestedAt, bar.PriceBasis, bar.Currency,
				bar.Open.text, bar.High.text, bar.Low.text, bar.Close.text, nullableBackfillNumberSQLText(bar.Volume),
				bar.RawPayloadHash, bar.DataSourceID, bar.IngestionRunID, bar.RawRecordLocator,
				bar.ManifestPath, bar.SourceManifestPath, bar.ManifestSHA256, bar.PartPath, bar.SourcePartPath, bar.PartSHA256)
			if err != nil {
				return DiscoveryBackfillRegistrationResult{}, fmt.Errorf("insert backfill price %s/%s: %w", session.MarketSession, bar.SecurityID, err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return DiscoveryBackfillRegistrationResult{}, err
	}
	return DiscoveryBackfillRegistrationResult{ReconstructionRunID: m.ReconstructionRunID, RegistrationSHA256: digest}, nil
}

func backfillExistingChildrenMatch(ctx context.Context, tx pgx.Tx, registration DiscoveryBackfillRegistration) error {
	var indexCount, rankingCount, priceCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM discovery_backfill_indexes WHERE reconstruction_run_id=$1::uuid`, registration.Manifest.ReconstructionRunID).Scan(&indexCount); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM discovery_backfill_rankings WHERE reconstruction_run_id=$1::uuid`, registration.Manifest.ReconstructionRunID).Scan(&rankingCount); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM discovery_backfill_prices WHERE reconstruction_run_id=$1::uuid`, registration.Manifest.ReconstructionRunID).Scan(&priceCount); err != nil {
		return err
	}
	wantRankings, wantPrices := int64(0), int64(0)
	for _, session := range registration.Manifest.Sessions {
		wantRankings += int64(len(session.Rankings))
		wantPrices += int64(len(session.PriceBars))
	}
	if indexCount != int64(len(registration.Manifest.Sessions)) || rankingCount != wantRankings || priceCount != wantPrices {
		return errors.New("existing retrospective backfill child rows differ from immutable registration")
	}
	return nil
}
