package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/luisdourado/invs/config"
	"github.com/luisdourado/invs/internal/httpx"
	"github.com/luisdourado/invs/internal/providers/nasdaq"
	"github.com/luisdourado/invs/internal/providers/yahoo"
	"gopkg.in/yaml.v3"
)

const (
	nasdaqSyncStateVersion        = 1
	nasdaqSourceLabel             = "nasdaq_100"
	nasdaqSectorLookupSourceLabel = "nasdaq_sector_lookup"
)

type nasdaqSyncState struct {
	Version       int      `json:"version"`
	Source        string   `json:"source,omitempty"`
	SourceURL     string   `json:"source_url"`
	SourceAsOf    string   `json:"source_as_of"`
	RetrievedAt   string   `json:"retrieved_at"`
	SHA256        string   `json:"sha256"`
	RawPath       string   `json:"raw_path"`
	SourceTickers []string `json:"source_tickers"`
	ManualTickers []string `json:"manual_tickers"`
}

type nasdaqStatusView struct {
	Status      string `json:"status"`
	Source      string `json:"source,omitempty"`
	SourceAsOf  string `json:"source_as_of,omitempty"`
	RetrievedAt string `json:"retrieved_at,omitempty"`
	Count       int    `json:"count"`
	SHA256      string `json:"sha256,omitempty"`
}

type nasdaqSyncResult struct {
	Status             string   `json:"status"`
	Source             string   `json:"source"`
	SourceAsOf         string   `json:"source_as_of"`
	RetrievedAt        string   `json:"retrieved_at"`
	Constituents       int      `json:"constituents"`
	Tracked            int      `json:"tracked"`
	Added              []string `json:"added"`
	Removed            []string `json:"removed"`
	HistoryUnavailable []string `json:"history_unavailable"`
	SHA256             string   `json:"sha256"`
}

func (m *manager) nasdaqStatePath() string {
	return filepath.Join(filepath.Dir(m.backupDir), "nasdaq-100-state.json")
}

func (m *manager) nasdaqSnapshotDir() string {
	return filepath.Join(filepath.Dir(m.backupDir), "nasdaq-100-snapshots")
}

func (m *manager) nasdaqStatus() nasdaqStatusView {
	state, exists, err := readNasdaqSyncState(m.nasdaqStatePath())
	if err != nil {
		return nasdaqStatusView{Status: "unavailable"}
	}
	if !exists {
		return nasdaqStatusView{Status: "ready_to_sync"}
	}
	return nasdaqStatusView{
		Status: "current_snapshot", Source: currentUniverseSourceName(state.Source, state.SourceURL), SourceAsOf: state.SourceAsOf,
		RetrievedAt: state.RetrievedAt, Count: len(state.SourceTickers), SHA256: state.SHA256,
	}
}

func (m *manager) nasdaqSyncHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	result, err := m.syncNasdaq100(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "Nasdaq refresh failed; the last complete tracked universe remains active: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (m *manager) syncNasdaq100(ctx context.Context) (nasdaqSyncResult, error) {
	cfg, profile, configNode, profileNode, err := m.load()
	if err != nil {
		return nasdaqSyncResult{}, fmt.Errorf("load current tracker configuration: %w", err)
	}

	userAgent := strings.TrimSpace(os.Getenv("INVS_USER_AGENT"))
	if userAgent == "" {
		userAgent = strings.TrimSpace(os.Getenv("SEC_USER_AGENT"))
	}
	if userAgent == "" {
		userAgent = "personal-quant-research/0.1 contact@example.com"
	}
	retrievedAt := m.now().UTC().Truncate(time.Microsecond)
	fetchContext, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	nasdaqHTTP := m.nasdaqHTTP
	if nasdaqHTTP == nil {
		nasdaqHTTP = newNasdaqHTTPClient()
	}
	snapshot, raw, fetchErr := fetchNasdaqCurrentUniverse(fetchContext, nasdaqHTTP, userAgent, retrievedAt)
	extension := ".json"
	if configuredNasdaqSource() == "giw" || configuredNasdaqSource() == nasdaq.CurrentUniverseSourceGIW || snapshot.Source == nasdaq.CurrentUniverseSourceGIW {
		extension = ".txt"
	}
	if len(raw) != 0 {
		if err := m.archiveNasdaqResponse(raw, retrievedAt, extension); err != nil {
			return nasdaqSyncResult{}, fmt.Errorf("archive raw Nasdaq response: %w", err)
		}
	}
	if fetchErr != nil {
		return nasdaqSyncResult{}, fetchErr
	}
	if err := fetchContext.Err(); err != nil {
		return nasdaqSyncResult{}, err
	}
	eastern, err := time.LoadLocation("America/New_York")
	if err != nil {
		return nasdaqSyncResult{}, err
	}
	sourceDate := snapshot.AsOf.In(eastern).Format(time.DateOnly)
	if sourceDate == "" || snapshot.Count != len(snapshot.Members) {
		return nasdaqSyncResult{}, errors.New("Nasdaq snapshot has an invalid as-of date or member count")
	}

	statePath := m.nasdaqStatePath()
	previousState, stateExists, err := readNasdaqSyncState(statePath)
	if err != nil {
		return nasdaqSyncResult{}, fmt.Errorf("read prior Nasdaq snapshot state: %w", err)
	}
	previousSource := tickerSet(previousState.SourceTickers)
	manual := tickerSet(previousState.ManualTickers)
	if !stateExists {
		for _, member := range profile.Members {
			manual[strings.ToUpper(strings.TrimSpace(member.Ticker))] = true
		}
	} else {
		// Preserve entries added directly in the tracker since the last source sync.
		for _, member := range profile.Members {
			ticker := strings.ToUpper(strings.TrimSpace(member.Ticker))
			if !previousSource[ticker] || containsSource(member.Sources, "manual") {
				manual[ticker] = true
			}
		}
	}

	configByTicker := make(map[string]config.Security, len(cfg.Universe)+snapshot.Count)
	for _, security := range cfg.Universe {
		ticker := strings.ToUpper(strings.TrimSpace(security.Ticker))
		if _, exists := configByTicker[ticker]; exists {
			return nasdaqSyncResult{}, fmt.Errorf("collector configuration contains duplicate ticker %s", ticker)
		}
		configByTicker[ticker] = security
	}
	profileByTicker := make(map[string]profileMember, len(profile.Members)+snapshot.Count)
	for _, member := range profile.Members {
		ticker := strings.ToUpper(strings.TrimSpace(member.Ticker))
		if _, exists := profileByTicker[ticker]; exists {
			return nasdaqSyncResult{}, fmt.Errorf("discovery profile contains duplicate ticker %s", ticker)
		}
		member.Ticker = ticker
		profileByTicker[ticker] = member
	}

	additions := make([]string, 0)
	removed := make([]string, 0)
	historyUnavailable := make([]string, 0)
	newSecurities := make([]config.Security, 0)
	newMembers := make([]profileMember, 0, snapshot.Count)
	yahooClient, yahooErr := newYahooHistoryClient(userAgent, cfg, m.yahooHTTP)
	for _, constituent := range snapshot.Members {
		ticker := constituent.Ticker
		security, exists := configByTicker[ticker]
		if exists && !isSupportedNasdaqSecurity(security) {
			return nasdaqSyncResult{}, fmt.Errorf("Nasdaq constituent %s conflicts with an unsupported collector mapping", ticker)
		}
		member, memberExists := profileByTicker[ticker]
		if exists && memberExists && (member.SecurityID != security.SecurityID || member.IssuerID != security.IssuerID) {
			return nasdaqSyncResult{}, fmt.Errorf("Nasdaq constituent %s has conflicting profile and collector IDs", ticker)
		}
		if !exists {
			validFrom := time.Date(snapshot.AsOf.In(eastern).Year(), snapshot.AsOf.In(eastern).Month(), snapshot.AsOf.In(eastern).Day(), 0, 0, 0, 0, time.UTC)
			if yahooErr == nil {
				lookupContext, lookupCancel := context.WithTimeout(fetchContext, 12*time.Second)
				firstDate, lookupErr := yahooClient.FirstTradeDate(lookupContext, ticker)
				lookupCancel()
				if lookupErr == nil && !firstDate.After(validFrom) {
					validFrom = firstDate
				} else {
					historyUnavailable = append(historyUnavailable, ticker)
				}
			} else {
				historyUnavailable = append(historyUnavailable, ticker)
			}
			security = newNasdaqSecurity(constituent, validFrom)
			if _, duplicate := configByTicker[ticker]; duplicate {
				return nasdaqSyncResult{}, fmt.Errorf("Nasdaq constituent %s collides with an existing collector mapping", ticker)
			}
			configByTicker[ticker] = security
			newSecurities = append(newSecurities, security)
			additions = append(additions, ticker)
		}
		if !memberExists {
			sector, inferred, err := resolveNasdaqSector(constituent)
			if err != nil {
				return nasdaqSyncResult{}, err
			}
			member = profileMember{
				Ticker: ticker, SecurityID: security.SecurityID, IssuerID: security.IssuerID,
				Sector: sector, Sources: []string{nasdaqSourceLabel},
			}
			if inferred {
				member.Sources = addSource(member.Sources, nasdaqSectorLookupSourceLabel)
			}
		} else {
			if member.SecurityID != security.SecurityID || member.IssuerID != security.IssuerID {
				return nasdaqSyncResult{}, fmt.Errorf("Nasdaq constituent %s has conflicting profile and collector IDs", ticker)
			}
			member.Sources = addSource(member.Sources, nasdaqSourceLabel)
			if member.Sector == "" || member.Sector == "unclassified" {
				if !containsSource(member.Sources, "manual") {
					sector, inferred, err := resolveNasdaqSector(constituent)
					if err != nil {
						return nasdaqSyncResult{}, err
					}
					if sector != "unclassified" {
						member.Sector = sector
						if inferred {
							member.Sources = addSource(member.Sources, nasdaqSectorLookupSourceLabel)
						}
					}
				}
			}
		}
		profileByTicker[ticker] = member
		newMembers = append(newMembers, member)
	}

	currentSource := tickerSet(snapshotTickers(snapshot.Members))
	for ticker := range previousSource {
		if !currentSource[ticker] && !manual[ticker] {
			removed = append(removed, ticker)
		}
	}
	manualMembers := make(map[string]profileMember, len(manual))
	for ticker := range manual {
		member, exists := profileByTicker[ticker]
		if !exists {
			return nasdaqSyncResult{}, fmt.Errorf("manually tracked ticker %s is missing from the current profile", ticker)
		}
		security, exists := configByTicker[ticker]
		if !exists || security.SecurityID != member.SecurityID || security.IssuerID != member.IssuerID || !isSupportedNasdaqSecurity(security) {
			return nasdaqSyncResult{}, fmt.Errorf("manually tracked ticker %s has no matching supported collector mapping", ticker)
		}
		member.Sources = addSource(member.Sources, "manual")
		manualMembers[ticker] = member
	}
	for _, member := range newMembers {
		if manual[strings.ToUpper(member.Ticker)] {
			member.Sources = addSource(member.Sources, "manual")
		}
		profileByTicker[member.Ticker] = member
	}
	merged := make(map[string]profileMember, len(currentSource)+len(manualMembers))
	for _, member := range newMembers {
		merged[member.Ticker] = profileByTicker[member.Ticker]
	}
	for ticker, member := range manualMembers {
		member.Sources = addSource(member.Sources, "manual")
		if currentSource[ticker] {
			member.Sources = addSource(member.Sources, nasdaqSourceLabel)
		}
		merged[ticker] = member
	}
	profile.Members = profile.Members[:0]
	for _, member := range merged {
		profile.Members = append(profile.Members, member)
	}
	sort.Slice(profile.Members, func(i, j int) bool { return profile.Members[i].Ticker < profile.Members[j].Ticker })
	profile.MembershipAsOf = sourceDate
	profile.SourceURL = snapshot.SourceURL
	if err := setMappingValue(profileNode, "membership_as_of", stringNode(sourceDate)); err != nil {
		return nasdaqSyncResult{}, err
	}
	if err := setOrAppendMappingValue(profileNode, "source_url", stringNode(snapshot.SourceURL)); err != nil {
		return nasdaqSyncResult{}, err
	}
	if err := setProfileMembers(profileNode, profile.Members); err != nil {
		return nasdaqSyncResult{}, err
	}
	for _, security := range newSecurities {
		if _, err := appendSecurityNode(configNode, security); err != nil {
			return nasdaqSyncResult{}, err
		}
	}
	cfg.Universe = cfg.Universe[:0]
	for _, security := range configByTicker {
		cfg.Universe = append(cfg.Universe, security)
	}
	if err := validateCandidateConfig(cfg); err != nil {
		return nasdaqSyncResult{}, fmt.Errorf("Nasdaq snapshot would make collector config invalid: %w", err)
	}
	if err := validateProfile(profile); err != nil {
		return nasdaqSyncResult{}, fmt.Errorf("Nasdaq snapshot would make discovery profile invalid: %w", err)
	}
	configBytes, err := encodeYAML(configNode)
	if err != nil {
		return nasdaqSyncResult{}, fmt.Errorf("encode updated collector configuration: %w", err)
	}
	profileBytes, err := encodeYAML(profileNode)
	if err != nil {
		return nasdaqSyncResult{}, fmt.Errorf("encode updated discovery profile: %w", err)
	}

	configOriginal, err := os.ReadFile(m.configPath)
	if err != nil {
		return nasdaqSyncResult{}, fmt.Errorf("read collector configuration before update: %w", err)
	}
	profileOriginal, err := os.ReadFile(m.profilePath)
	if err != nil {
		return nasdaqSyncResult{}, fmt.Errorf("read discovery profile before update: %w", err)
	}
	var stateOriginal []byte
	if stateExists {
		stateOriginal, err = os.ReadFile(statePath)
		if err != nil {
			return nasdaqSyncResult{}, fmt.Errorf("read Nasdaq state before update: %w", err)
		}
	}
	stamp := retrievedAt.Format("20060102T150405.000000000Z")
	if len(newSecurities) > 0 {
		if err := m.backup("collector-config.yaml", stamp, configOriginal); err != nil {
			return nasdaqSyncResult{}, fmt.Errorf("backup collector configuration: %w", err)
		}
	}
	if err := m.backup("nasdaq-100-starter.yaml", stamp, profileOriginal); err != nil {
		return nasdaqSyncResult{}, fmt.Errorf("backup discovery profile: %w", err)
	}
	if stateExists {
		if err := m.backup("nasdaq-100-state.json", stamp, stateOriginal); err != nil {
			return nasdaqSyncResult{}, fmt.Errorf("backup previous Nasdaq state: %w", err)
		}
	}

	if len(newSecurities) > 0 {
		if err := writeMountedFile(m.configPath, configBytes); err != nil {
			return nasdaqSyncResult{}, fmt.Errorf("write updated collector configuration: %w", err)
		}
	}
	rollback := func() {
		if len(newSecurities) > 0 {
			if err := writeMountedFile(m.configPath, configOriginal); err != nil {
				slog.Error("Nasdaq sync collector-config rollback failed", "error", err)
			}
		}
		if err := writeAtomic(m.profilePath, profileOriginal); err != nil {
			slog.Error("Nasdaq sync profile rollback failed", "error", err)
		}
		if stateExists {
			if err := writeAtomic(statePath, stateOriginal); err != nil {
				slog.Error("Nasdaq sync state rollback failed", "error", err)
			}
		} else if err := os.Remove(statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Error("Nasdaq sync state cleanup failed", "error", err)
		}
	}
	if err := writeAtomic(m.profilePath, profileBytes); err != nil {
		rollback()
		return nasdaqSyncResult{}, fmt.Errorf("write updated discovery profile: %w", err)
	}
	newState := nasdaqSyncState{
		Version: nasdaqSyncStateVersion, Source: snapshot.Source, SourceURL: snapshot.SourceURL,
		SourceAsOf: sourceDate, RetrievedAt: retrievedAt.Format(time.RFC3339Nano),
		SHA256: snapshot.SHA256, RawPath: filepath.Join(m.nasdaqSnapshotDir(), retrievedAt.Format("20060102T150405Z")+"-"+snapshot.SHA256+extension),
		SourceTickers: snapshotTickers(snapshot.Members), ManualTickers: sortedKeys(manual),
	}
	stateBytes, err := json.MarshalIndent(newState, "", "  ")
	if err != nil {
		rollback()
		return nasdaqSyncResult{}, fmt.Errorf("encode Nasdaq snapshot state: %w", err)
	}
	stateBytes = append(stateBytes, '\n')
	if err := writeAtomicCreate(statePath, stateBytes, stateExists); err != nil {
		rollback()
		return nasdaqSyncResult{}, fmt.Errorf("write Nasdaq snapshot state: %w", err)
	}
	return nasdaqSyncResult{
		Status: "ok", Source: currentUniverseSourceName(snapshot.Source, snapshot.SourceURL),
		SourceAsOf: sourceDate, RetrievedAt: retrievedAt.Format(time.RFC3339Nano),
		Constituents: snapshot.Count, Tracked: len(profile.Members),
		Added: sortedKeys(tickerSet(additions)), Removed: sortedKeys(tickerSet(removed)),
		HistoryUnavailable: sortedKeys(tickerSet(historyUnavailable)), SHA256: snapshot.SHA256,
	}, nil
}

func newNasdaqHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// The public Nasdaq endpoint resets HTTP/2 streams on this deployment.
	// Keep the feed's request on HTTP/1.1 and IPv4: the container's IPv6 route
	// stalls while the public IPv4 endpoint responds normally.
	transport.ForceAttemptHTTP2 = false
	tlsConfig := &tls.Config{NextProtos: []string{"http/1.1"}}
	if transport.TLSClientConfig != nil {
		tlsConfig = transport.TLSClientConfig.Clone()
		tlsConfig.NextProtos = []string{"http/1.1"}
	}
	transport.TLSClientConfig = tlsConfig
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, _ string, addr string) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp4", addr)
	}
	return &http.Client{
		Transport: transport,
		Timeout:   40 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func configuredNasdaqSource() string {
	source := strings.ToLower(strings.TrimSpace(os.Getenv("INVS_NASDAQ_SOURCE")))
	if source != "" {
		return source
	}
	if strings.TrimSpace(os.Getenv("INVS_NASDAQ_GIW_USERNAME")) != "" || strings.TrimSpace(os.Getenv("INVS_NASDAQ_GIW_PASSWORD")) != "" {
		return "giw"
	}
	return "website"
}

func fetchNasdaqCurrentUniverse(ctx context.Context, client nasdaq.HTTPDoer, userAgent string, retrievedAt time.Time) (nasdaq.CurrentUniverse, []byte, error) {
	source := configuredNasdaqSource()
	username := strings.TrimSpace(os.Getenv("INVS_NASDAQ_GIW_USERNAME"))
	password := strings.TrimSpace(os.Getenv("INVS_NASDAQ_GIW_PASSWORD"))
	switch source {
	case "giw", nasdaq.CurrentUniverseSourceGIW:
		return nasdaq.FetchGIWCurrentUniverse(ctx, client, username, password, userAgent, retrievedAt)
	case "website", "nasdaq.com", nasdaq.CurrentUniverseSourceWebsite:
		return nasdaq.FetchCurrentUniverse(ctx, client, userAgent, retrievedAt)
	default:
		return nasdaq.CurrentUniverse{}, nil, fmt.Errorf("unsupported INVS_NASDAQ_SOURCE %q; use giw or website", source)
	}
}

func currentUniverseSourceName(source, sourceURL string) string {
	switch source {
	case nasdaq.CurrentUniverseSourceGIW:
		return "Nasdaq GIW Web Services (licensed EOD constituent file)"
	case nasdaq.CurrentUniverseSourceWebsite:
		return "Nasdaq public website feed (temporary, undocumented)"
	default:
		if sourceURL == nasdaq.GIWWeightingURL {
			return "Nasdaq GIW Web Services (licensed EOD constituent file)"
		}
		return "Nasdaq public website feed (temporary, undocumented)"
	}
}

func newYahooHistoryClient(userAgent string, cfg config.Config, injected yahoo.Getter) (*yahoo.Client, error) {
	getter := injected
	if getter == nil {
		var err error
		getter, err = httpx.New(httpx.Config{
			UserAgent: userAgent, Timeout: cfg.HTTP.Timeout,
			RequestsPerSecond: cfg.HTTP.RequestsPerSecond, Burst: cfg.HTTP.Burst,
			MaxAttempts: cfg.HTTP.MaxAttempts, InitialBackoff: cfg.HTTP.InitialBackoff,
		})
		if err != nil {
			return nil, err
		}
	}
	return yahoo.NewClient(getter), nil
}

func newNasdaqSecurity(member nasdaq.CurrentConstituent, historyStart time.Time) config.Security {
	ticker := member.Ticker
	issuerID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://invs.invalid/nasdaq-100/issuer/"+ticker)).String()
	securityID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://invs.invalid/nasdaq-100/security/"+ticker)).String()
	return config.Security{
		IssuerID: issuerID, SecurityID: securityID, LegalName: member.Company,
		CountryCode: "US", SecurityType: "common_stock", PrimaryListing: true,
		Ticker: ticker, IdentifierValidFrom: historyStart.Format(time.DateOnly),
		YahooSymbol: ticker, Exchange: "NASDAQ", MIC: "XNAS", Currency: "USD",
	}
}

func currentSector(value string) string {
	key := strings.ToLower(strings.TrimSpace(value))
	key = strings.ReplaceAll(key, "&", "and")
	key = strings.Join(strings.Fields(key), " ")
	switch key {
	case "communication services", "communications", "telecommunications":
		return "communication_services"
	case "consumer discretionary", "consumer cyclical":
		return "consumer_discretionary"
	case "consumer staples", "consumer defensive":
		return "consumer_staples"
	case "health care", "healthcare":
		return "health_care"
	case "technology", "information technology":
		return "technology"
	case "financials", "financial services":
		return "financials"
	case "energy":
		return "energy"
	case "industrials":
		return "industrials"
	case "materials", "basic materials":
		return "materials"
	case "real estate":
		return "real_estate"
	case "utilities":
		return "utilities"
	default:
		return "unclassified"
	}
}

func resolveNasdaqSector(member nasdaq.CurrentConstituent) (sector string, inferred bool, err error) {
	if sector = currentSector(member.Sector); sector != "unclassified" {
		return sector, false, nil
	}
	entry, matched, err := nasdaq.LookupCurrentSector(member.Ticker, member.Company)
	if err != nil {
		return "unclassified", false, fmt.Errorf("load Nasdaq sector lookup: %w", err)
	}
	if matched {
		return entry.Sector, true, nil
	}
	return "unclassified", false, nil
}

func (m *manager) archiveNasdaqResponse(body []byte, retrieved time.Time, extension string) error {
	if err := os.MkdirAll(m.nasdaqSnapshotDir(), 0o700); err != nil {
		return err
	}
	if extension != ".json" && extension != ".txt" {
		return fmt.Errorf("unsupported Nasdaq archive extension %q", extension)
	}
	digest := sha256.Sum256(body)
	path := filepath.Join(m.nasdaqSnapshotDir(), retrieved.Format("20060102T150405Z")+"-"+hex.EncodeToString(digest[:])+extension)
	return writeImmutable(path, body)
}

func readNasdaqSyncState(path string) (nasdaqSyncState, bool, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nasdaqSyncState{}, false, nil
	}
	if err != nil {
		return nasdaqSyncState{}, false, err
	}
	var state nasdaqSyncState
	if err := json.Unmarshal(body, &state); err != nil {
		return nasdaqSyncState{}, true, err
	}
	if state.Version != nasdaqSyncStateVersion || !isValidNasdaqStateSource(state.Source, state.SourceURL) || len(tickerSet(state.SourceTickers)) != len(state.SourceTickers) || len(tickerSet(state.ManualTickers)) != len(state.ManualTickers) {
		return nasdaqSyncState{}, true, errors.New("stored Nasdaq sync state is invalid")
	}
	if state.Source == "" {
		if state.SourceURL == nasdaq.GIWWeightingURL {
			state.Source = nasdaq.CurrentUniverseSourceGIW
		} else {
			state.Source = nasdaq.CurrentUniverseSourceWebsite
		}
	}
	return state, true, nil
}

func isValidNasdaqStateSource(source, sourceURL string) bool {
	switch source {
	case "":
		return sourceURL == nasdaq.CurrentUniverseURL || sourceURL == nasdaq.GIWWeightingURL
	case nasdaq.CurrentUniverseSourceWebsite:
		return sourceURL == nasdaq.CurrentUniverseURL
	case nasdaq.CurrentUniverseSourceGIW:
		return sourceURL == nasdaq.GIWWeightingURL
	default:
		return false
	}
}

func tickerSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value != "" {
			result[value] = true
		}
	}
	return result
}

func snapshotTickers(members []nasdaq.CurrentConstituent) []string {
	values := make([]string, 0, len(members))
	for _, member := range members {
		values = append(values, member.Ticker)
	}
	sort.Strings(values)
	return values
}

func sortedKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func containsSource(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func addSource(values []string, target string) []string {
	if containsSource(values, target) {
		return values
	}
	values = append(append([]string(nil), values...), target)
	sort.Strings(values)
	return values
}

func setProfileMembers(root *yaml.Node, members []profileMember) error {
	sequence, err := mappingSequence(root, "members")
	if err != nil {
		return err
	}
	sequence.Content = sequence.Content[:0]
	for _, member := range members {
		item := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		item.Content = append(item.Content,
			stringNode("ticker"), stringNode(member.Ticker),
			stringNode("security_id"), stringNode(member.SecurityID),
			stringNode("issuer_id"), stringNode(member.IssuerID),
			stringNode("sector"), stringNode(member.Sector),
			stringNode("themes"), stringSequence(member.Themes),
		)
		if len(member.Sources) > 0 {
			values := append([]string(nil), member.Sources...)
			sort.Strings(values)
			item.Content = append(item.Content, stringNode("sources"), stringSequence(values))
		}
		sequence.Content = append(sequence.Content, item)
	}
	return nil
}

func setOrAppendMappingValue(root *yaml.Node, key string, value *yaml.Node) error {
	if err := setMappingValue(root, key, value); err == nil {
		return nil
	}
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return errors.New("YAML root must be a mapping")
	}
	root.Content = append(root.Content, stringNode(key), value)
	return nil
}

func writeImmutable(path string, data []byte) error {
	if existing, err := os.ReadFile(path); err == nil {
		if !bytes.Equal(existing, data) {
			return fmt.Errorf("immutable snapshot path already contains different bytes")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func writeAtomicCreate(path string, data []byte, exists bool) error {
	if exists {
		return writeAtomic(path, data)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".nasdaq-state-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
