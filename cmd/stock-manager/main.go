package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/luisdourado/invs/config"
	"github.com/luisdourado/invs/internal/providers/nasdaq"
	"github.com/luisdourado/invs/internal/providers/yahoo"
	"gopkg.in/yaml.v3"
)

//go:embed web/index.html
var webFiles embed.FS

var (
	tickerPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9.-]{0,15}$`)
	slugPattern   = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)
	sectors       = []string{
		"communication_services", "consumer_discretionary", "consumer_staples",
		"energy", "financials", "health_care", "industrials", "materials",
		"real_estate", "technology", "utilities",
	}
)

type manager struct {
	configPath  string
	profilePath string
	backupDir   string
	grafanaPort string
	timezone    string
	nasdaqHTTP  nasdaq.HTTPDoer
	yahooHTTP   yahoo.Getter
	mu          sync.Mutex
	now         func() time.Time
}

type universeProfile struct {
	SchemaVersion  string          `yaml:"schema_version"`
	UniverseID     string          `yaml:"universe_id"`
	Name           string          `yaml:"name"`
	MembershipAsOf string          `yaml:"membership_as_of"`
	SourceURL      string          `yaml:"source_url"`
	Members        []profileMember `yaml:"members"`
}

type profileMember struct {
	Ticker     string   `yaml:"ticker" json:"ticker"`
	SecurityID string   `yaml:"security_id" json:"security_id"`
	IssuerID   string   `yaml:"issuer_id" json:"issuer_id"`
	Sector     string   `yaml:"sector" json:"sector"`
	Themes     []string `yaml:"themes" json:"themes"`
	Sources    []string `yaml:"sources,omitempty" json:"sources,omitempty"`
}

type stockView struct {
	Ticker      string   `json:"ticker"`
	LegalName   string   `json:"legal_name"`
	YahooSymbol string   `json:"yahoo_symbol"`
	SecurityID  string   `json:"security_id"`
	Sector      string   `json:"sector"`
	Themes      []string `json:"themes"`
	Sources     []string `json:"sources,omitempty"`
}

type stockList struct {
	MembershipAsOf string           `json:"membership_as_of"`
	PricesEnabled  bool             `json:"prices_enabled"`
	PricesStart    string           `json:"prices_start"`
	GrafanaPort    string           `json:"grafana_port"`
	Nasdaq100      nasdaqStatusView `json:"nasdaq_100"`
	Tracked        []stockView      `json:"tracked"`
	Available      []stockView      `json:"available"`
}

type addStockRequest struct {
	Ticker              string   `json:"ticker"`
	LegalName           string   `json:"legal_name"`
	YahooSymbol         string   `json:"yahoo_symbol"`
	IdentifierValidFrom string   `json:"identifier_valid_from"`
	CIK                 string   `json:"cik"`
	Sector              string   `json:"sector"`
	Themes              []string `json:"themes"`
	MembershipAsOf      string   `json:"membership_as_of"`
}

type apiError struct {
	Error string `json:"error"`
}

func main() {
	m := &manager{
		configPath:  envOr("STOCK_MANAGER_CONFIG", "/config/collector-config.yaml"),
		profilePath: envOr("STOCK_MANAGER_PROFILE", "/config/universes/nasdaq-100-starter.yaml"),
		backupDir:   envOr("STOCK_MANAGER_BACKUP_DIR", "/data/research/stock-manager/backups"),
		grafanaPort: envOr("GRAFANA_HOST_PORT", "3000"),
		timezone:    envOr("INVS_TIMEZONE", "America/Sao_Paulo"),
		now:         time.Now,
	}
	server := &http.Server{
		Addr:              envOr("STOCK_MANAGER_LISTEN", ":8080"),
		Handler:           m.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	slog.Info("stock manager listening", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("stock manager stopped", "error", err)
		os.Exit(1)
	}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func (m *manager) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		m.mu.Lock()
		_, _, _, _, err := m.load()
		m.mu.Unlock()
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "stock profile or collector config is unavailable")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/api/stocks", m.stocksHandler)
	mux.HandleFunc("/api/nasdaq-100/sync", m.nasdaqSyncHandler)
	mux.HandleFunc("/api/nasdaq-100/classify-sectors", m.nasdaqSectorEnrichmentHandler)
	mux.HandleFunc("/", m.indexHandler)
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (m *manager) indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	data, err := webFiles.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "stock manager UI unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (m *manager) stocksHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		m.listStocks(w)
	case http.MethodPost:
		m.addStock(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (m *manager) listStocks(w http.ResponseWriter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, profile, _, _, err := m.load()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "could not read the stock profile and collector configuration; inspect the stock-manager logs")
		return
	}
	response := stockList{
		MembershipAsOf: profile.MembershipAsOf,
		PricesEnabled:  cfg.Providers.Prices.Enabled,
		PricesStart:    cfg.Providers.Prices.Start,
		GrafanaPort:    m.grafanaPort,
		Nasdaq100:      m.nasdaqStatus(),
		Tracked:        []stockView{},
		Available:      []stockView{},
	}
	configByTicker := make(map[string][]config.Security, len(cfg.Universe))
	for _, security := range cfg.Universe {
		configByTicker[strings.ToUpper(strings.TrimSpace(security.Ticker))] = append(configByTicker[strings.ToUpper(strings.TrimSpace(security.Ticker))], security)
	}
	tracked := make(map[string]bool, len(profile.Members))
	for _, member := range profile.Members {
		ticker := strings.ToUpper(strings.TrimSpace(member.Ticker))
		tracked[ticker] = true
		view := stockView{Ticker: ticker, SecurityID: member.SecurityID, Sector: member.Sector, Themes: member.Themes, Sources: member.Sources}
		if securities := configByTicker[ticker]; len(securities) == 1 && securities[0].SecurityID == member.SecurityID {
			view.LegalName = securities[0].LegalName
			view.YahooSymbol = securities[0].YahooSymbol
		}
		response.Tracked = append(response.Tracked, view)
	}
	for _, securities := range configByTicker {
		if len(securities) != 1 {
			continue
		}
		security := securities[0]
		ticker := strings.ToUpper(strings.TrimSpace(security.Ticker))
		if tracked[ticker] || !isSupportedNasdaqSecurity(security) {
			continue
		}
		response.Available = append(response.Available, stockView{
			Ticker: ticker, LegalName: security.LegalName, YahooSymbol: security.YahooSymbol,
			SecurityID: security.SecurityID,
			Sources:    []string{"collector_config"},
		})
	}
	sort.Slice(response.Tracked, func(i, j int) bool { return response.Tracked[i].Ticker < response.Tracked[j].Ticker })
	sort.Slice(response.Available, func(i, j int) bool { return response.Available[i].Ticker < response.Available[j].Ticker })
	writeJSON(w, http.StatusOK, response)
}

func (m *manager) addStock(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request addStockRequest
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "request body is not valid stock data")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	result, status, err := m.add(request)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (m *manager) add(request addStockRequest) (map[string]any, int, error) {
	cfg, profile, configNode, profileNode, err := m.load()
	if err != nil {
		return nil, http.StatusServiceUnavailable, errors.New("could not read the stock profile and collector configuration; inspect the stock-manager logs")
	}
	ticker := strings.ToUpper(strings.TrimSpace(request.Ticker))
	if !tickerPattern.MatchString(ticker) {
		return nil, http.StatusBadRequest, errors.New("ticker must use 1–16 letters, digits, dots, or hyphens")
	}
	sector := strings.ToLower(strings.TrimSpace(request.Sector))
	if !contains(sectors, sector) {
		return nil, http.StatusBadRequest, errors.New("choose a supported sector")
	}
	themes, err := normalizeThemes(request.Themes)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	for _, member := range profile.Members {
		if strings.EqualFold(member.Ticker, ticker) {
			return nil, http.StatusConflict, fmt.Errorf("%s is already tracked", ticker)
		}
	}
	memberDate, err := time.Parse("2006-01-02", strings.TrimSpace(request.MembershipAsOf))
	if err != nil {
		return nil, http.StatusBadRequest, errors.New("membership effective date must be YYYY-MM-DD")
	}
	profileDate, err := time.Parse("2006-01-02", profile.MembershipAsOf)
	if err != nil {
		return nil, http.StatusServiceUnavailable, errors.New("profile membership date is invalid; inspect the stock-manager logs")
	}
	today := m.currentDate()
	if memberDate.Before(profileDate) || memberDate.After(today) {
		return nil, http.StatusBadRequest, fmt.Errorf("membership effective date must be between %s and %s", profile.MembershipAsOf, today.Format("2006-01-02"))
	}
	if !cfg.Providers.Prices.Enabled {
		return nil, http.StatusConflict, errors.New("Yahoo price collection is disabled in the collector configuration")
	}
	security, reused, err := findSecurity(cfg.Universe, ticker)
	if err != nil {
		return nil, http.StatusConflict, err
	}
	configChanged := false
	if reused {
		if !isSupportedNasdaqSecurity(security) {
			return nil, http.StatusConflict, fmt.Errorf("%s exists in collector config but is not a primary US/NASDAQ/USD listing", ticker)
		}
		validFrom, parseErr := time.Parse("2006-01-02", security.IdentifierValidFrom)
		if parseErr != nil || validFrom.After(memberDate) || validFrom.After(today) {
			return nil, http.StatusConflict, fmt.Errorf("%s has an invalid identifier_valid_from date for this membership", ticker)
		}
	} else {
		security, err = newSecurity(request, ticker, memberDate, today)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		configNode, err = appendSecurityNode(configNode, security)
		if err != nil {
			return nil, http.StatusServiceUnavailable, errors.New("collector config does not contain a writable universe list")
		}
		cfg.Universe = append(cfg.Universe, security)
		configChanged = true
	}
	if err := validateCandidateConfig(cfg); err != nil {
		return nil, http.StatusConflict, fmt.Errorf("the collector config would not validate: %s", err)
	}
	newMember := profileMember{
		Ticker: ticker, SecurityID: security.SecurityID, IssuerID: security.IssuerID,
		Sector: sector, Themes: themes, Sources: []string{"manual"},
	}
	profileNode, err = appendProfileMember(profileNode, memberDate.Format("2006-01-02"), newMember)
	if err != nil {
		return nil, http.StatusServiceUnavailable, errors.New("discovery profile does not contain a writable members list")
	}
	profile.MembershipAsOf = memberDate.Format("2006-01-02")
	profile.Members = append(profile.Members, newMember)
	if err := validateProfile(profile); err != nil {
		return nil, http.StatusConflict, fmt.Errorf("the discovery profile would not validate: %s", err)
	}
	configBytes, err := encodeYAML(configNode)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.New("could not encode updated collector config")
	}
	profileBytes, err := encodeYAML(profileNode)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.New("could not encode updated discovery profile")
	}
	profileOriginal, err := os.ReadFile(m.profilePath)
	if err != nil {
		return nil, http.StatusServiceUnavailable, errors.New("could not read discovery profile before update")
	}
	var configOriginal []byte
	if configChanged {
		configOriginal, err = os.ReadFile(m.configPath)
		if err != nil {
			return nil, http.StatusServiceUnavailable, errors.New("could not read collector config before update")
		}
	}
	stamp := m.now().UTC().Format("20060102T150405.000000000Z")
	if err := m.backup("nasdaq-100-starter.yaml", stamp, profileOriginal); err != nil {
		return nil, http.StatusInternalServerError, errors.New("could not create the profile backup")
	}
	if configChanged {
		if err := m.backup("collector-config.yaml", stamp, configOriginal); err != nil {
			return nil, http.StatusInternalServerError, errors.New("could not create the collector config backup")
		}
	}
	if configChanged {
		if err := writeMountedFile(m.configPath, configBytes); err != nil {
			if rollbackErr := writeMountedFile(m.configPath, configOriginal); rollbackErr != nil {
				slog.Error("collector config rollback failed", "error", rollbackErr)
			}
			return nil, http.StatusInternalServerError, errors.New("could not write updated collector config")
		}
	}
	if err := writeAtomic(m.profilePath, profileBytes); err != nil {
		if rollbackErr := writeAtomic(m.profilePath, profileOriginal); rollbackErr != nil {
			slog.Error("discovery profile rollback failed", "error", rollbackErr)
		}
		if configChanged {
			if rollbackErr := writeMountedFile(m.configPath, configOriginal); rollbackErr != nil {
				slog.Error("collector config rollback failed", "error", rollbackErr)
			}
		}
		return nil, http.StatusInternalServerError, errors.New("could not write updated discovery profile")
	}
	slog.Info("tracked stock added", "ticker", ticker, "config_updated", configChanged, "membership_as_of", profile.MembershipAsOf)
	return map[string]any{
		"status": "tracked", "ticker": ticker, "security_id": security.SecurityID,
		"membership_as_of": profile.MembershipAsOf, "collector_config_updated": configChanged,
		"next_step": "This ticker will be ranked on the next eligible market session; published sessions remain unchanged.",
	}, http.StatusCreated, nil
}

func (m *manager) load() (config.Config, universeProfile, *yaml.Node, *yaml.Node, error) {
	configBytes, err := os.ReadFile(m.configPath)
	if err != nil {
		return config.Config{}, universeProfile{}, nil, nil, fmt.Errorf("read collector config: %w", err)
	}
	profileBytes, err := os.ReadFile(m.profilePath)
	if err != nil {
		return config.Config{}, universeProfile{}, nil, nil, fmt.Errorf("read discovery profile: %w", err)
	}
	var cfg config.Config
	configDecoder := yaml.NewDecoder(bytes.NewReader(configBytes))
	configDecoder.KnownFields(true)
	if err := configDecoder.Decode(&cfg); err != nil {
		return config.Config{}, universeProfile{}, nil, nil, fmt.Errorf("decode collector config: %w", err)
	}
	var profile universeProfile
	if err := yaml.Unmarshal(profileBytes, &profile); err != nil {
		return config.Config{}, universeProfile{}, nil, nil, fmt.Errorf("decode discovery profile: %w", err)
	}
	if err := validateProfile(profile); err != nil {
		return config.Config{}, universeProfile{}, nil, nil, fmt.Errorf("validate discovery profile: %w", err)
	}
	var configNode, profileNode yaml.Node
	if err := yaml.Unmarshal(configBytes, &configNode); err != nil {
		return config.Config{}, universeProfile{}, nil, nil, fmt.Errorf("decode collector config nodes: %w", err)
	}
	if err := yaml.Unmarshal(profileBytes, &profileNode); err != nil {
		return config.Config{}, universeProfile{}, nil, nil, fmt.Errorf("decode discovery profile nodes: %w", err)
	}
	return cfg, profile, &configNode, &profileNode, nil
}

func validateProfile(profile universeProfile) error {
	if profile.SchemaVersion != "1.0.0" || profile.Name == "" || len(profile.Members) == 0 {
		return errors.New("profile has missing or unsupported fields")
	}
	if _, err := uuid.Parse(profile.UniverseID); err != nil {
		return errors.New("profile universe_id must be a UUID")
	}
	if _, err := time.Parse("2006-01-02", profile.MembershipAsOf); err != nil {
		return errors.New("profile membership_as_of must be an ISO date")
	}
	seenTickers := make(map[string]bool, len(profile.Members))
	seenIDs := make(map[string]bool, len(profile.Members))
	for _, member := range profile.Members {
		ticker := strings.ToUpper(strings.TrimSpace(member.Ticker))
		if !tickerPattern.MatchString(ticker) || seenTickers[ticker] {
			return fmt.Errorf("profile contains invalid or duplicate ticker %q", member.Ticker)
		}
		if _, err := uuid.Parse(member.SecurityID); err != nil || seenIDs[member.SecurityID] {
			return fmt.Errorf("profile contains invalid or duplicate security_id for %s", ticker)
		}
		if _, err := uuid.Parse(member.IssuerID); err != nil {
			return fmt.Errorf("profile contains invalid issuer_id for %s", ticker)
		}
		if !slugPattern.MatchString(member.Sector) {
			return fmt.Errorf("profile has invalid sector for %s", ticker)
		}
		seenSources := make(map[string]bool, len(member.Sources))
		for _, source := range member.Sources {
			if (source != "manual" && source != nasdaqSourceLabel && source != nasdaqSectorLookupSourceLabel) || seenSources[source] {
				return fmt.Errorf("profile has invalid source metadata for %s", ticker)
			}
			seenSources[source] = true
		}
		seenTickers[ticker], seenIDs[member.SecurityID] = true, true
	}
	return nil
}

func normalizeThemes(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool)
	for _, value := range values {
		theme := strings.ToLower(strings.TrimSpace(value))
		if theme == "" {
			continue
		}
		if !slugPattern.MatchString(theme) {
			return nil, fmt.Errorf("theme %q must be a lowercase word using letters, digits, and underscores", value)
		}
		if !seen[theme] {
			seen[theme] = true
			result = append(result, theme)
		}
	}
	if len(result) > 8 {
		return nil, errors.New("use no more than eight themes")
	}
	return result, nil
}

func newSecurity(request addStockRequest, ticker string, membershipDate, today time.Time) (config.Security, error) {
	legalName := strings.TrimSpace(request.LegalName)
	if len([]rune(legalName)) < 2 || len([]rune(legalName)) > 160 {
		return config.Security{}, errors.New("company name must be between 2 and 160 characters")
	}
	yahooSymbol := strings.ToUpper(strings.TrimSpace(request.YahooSymbol))
	if yahooSymbol == "" {
		yahooSymbol = ticker
	}
	if !tickerPattern.MatchString(yahooSymbol) {
		return config.Security{}, errors.New("Yahoo symbol must use 1–16 letters, digits, dots, or hyphens")
	}
	validFrom, err := time.Parse("2006-01-02", strings.TrimSpace(request.IdentifierValidFrom))
	if err != nil {
		return config.Security{}, errors.New("ticker valid-from date must be YYYY-MM-DD")
	}
	if validFrom.After(today) {
		return config.Security{}, errors.New("ticker valid-from date cannot be in the future")
	}
	if validFrom.After(membershipDate) {
		return config.Security{}, errors.New("ticker valid-from date cannot follow the universe membership date")
	}
	var cik int64
	if strings.TrimSpace(request.CIK) != "" {
		cik, err = strconv.ParseInt(strings.TrimSpace(request.CIK), 10, 64)
		if err != nil || cik <= 0 || cik > 9999999999 {
			return config.Security{}, errors.New("CIK must contain 1 to 10 digits")
		}
	}
	return config.Security{
		IssuerID: uuid.NewString(), SecurityID: uuid.NewString(), LegalName: legalName,
		CountryCode: "US", SecurityType: "common_stock", PrimaryListing: true,
		CIK: cik, Ticker: ticker, IdentifierValidFrom: validFrom.Format("2006-01-02"),
		YahooSymbol: yahooSymbol, Exchange: "NASDAQ", MIC: "XNAS", Currency: "USD",
	}, nil
}

func findSecurity(universe []config.Security, ticker string) (config.Security, bool, error) {
	var match config.Security
	count := 0
	for _, security := range universe {
		if strings.EqualFold(strings.TrimSpace(security.Ticker), ticker) {
			match = security
			count++
		}
	}
	if count > 1 {
		return config.Security{}, false, fmt.Errorf("%s matches multiple collector securities; resolve the mapping first", ticker)
	}
	return match, count == 1, nil
}

func isSupportedNasdaqSecurity(security config.Security) bool {
	return security.CountryCode == "US" && security.Exchange == "NASDAQ" && security.MIC == "XNAS" && security.Currency == "USD" && security.PrimaryListing && strings.TrimSpace(security.YahooSymbol) != ""
}

func validateCandidateConfig(cfg config.Config) error {
	// The stock manager never receives the ALFRED key. ALFRED settings are
	// untouched by stock additions, so validate the remaining collector config.
	cfg.Providers.ALFRED.Enabled = false
	if userAgent := strings.TrimSpace(os.Getenv("INVS_USER_AGENT")); userAgent != "" {
		cfg.UserAgent = userAgent
	} else if userAgent := strings.TrimSpace(os.Getenv("SEC_USER_AGENT")); userAgent != "" {
		cfg.UserAgent = userAgent
	}
	return cfg.Validate()
}

func appendSecurityNode(root *yaml.Node, security config.Security) (*yaml.Node, error) {
	sequence, err := mappingSequence(root, "universe")
	if err != nil {
		return nil, err
	}
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	add := func(key string, value *yaml.Node) {
		node.Content = append(node.Content, stringNode(key), value)
	}
	add("issuer_id", stringNode(security.IssuerID))
	add("security_id", stringNode(security.SecurityID))
	add("legal_name", stringNode(security.LegalName))
	add("country_code", stringNode(security.CountryCode))
	add("security_type", stringNode(security.SecurityType))
	add("primary_listing", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
	if security.CIK > 0 {
		add("cik", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(security.CIK, 10)})
	}
	add("ticker", stringNode(security.Ticker))
	add("identifier_valid_from", stringNode(security.IdentifierValidFrom))
	add("yahoo_symbol", stringNode(security.YahooSymbol))
	add("exchange", stringNode(security.Exchange))
	add("mic", stringNode(security.MIC))
	add("currency", stringNode(security.Currency))
	sequence.Content = append(sequence.Content, node)
	return root, nil
}

func appendProfileMember(root *yaml.Node, membershipAsOf string, member profileMember) (*yaml.Node, error) {
	sequence, err := mappingSequence(root, "members")
	if err != nil {
		return nil, err
	}
	if err := setMappingValue(root, "membership_as_of", stringNode(membershipAsOf)); err != nil {
		return nil, err
	}
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
	return root, nil
}

func mappingSequence(root *yaml.Node, key string) (*yaml.Node, error) {
	value, err := mappingValue(root, key)
	if err != nil || value.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%s must be a YAML sequence", key)
	}
	return value, nil
}

func mappingValue(root *yaml.Node, key string) (*yaml.Node, error) {
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("YAML root must be a mapping")
	}
	for index := 0; index+1 < len(root.Content); index += 2 {
		if root.Content[index].Value == key {
			return root.Content[index+1], nil
		}
	}
	return nil, fmt.Errorf("YAML mapping is missing %s", key)
}

func setMappingValue(root *yaml.Node, key string, value *yaml.Node) error {
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return errors.New("YAML root must be a mapping")
	}
	for index := 0; index+1 < len(root.Content); index += 2 {
		if root.Content[index].Value == key {
			root.Content[index+1] = value
			return nil
		}
	}
	return fmt.Errorf("YAML mapping is missing %s", key)
}

func stringNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func stringSequence(values []string) *yaml.Node {
	node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, value := range values {
		node.Content = append(node.Content, stringNode(value))
	}
	return node
}

func encodeYAML(node *yaml.Node) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(node); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func (m *manager) currentDate() time.Time {
	location, err := time.LoadLocation(m.timezone)
	if err != nil {
		location = time.UTC
	}
	now := m.now().In(location)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

func (m *manager) backup(name, stamp string, data []byte) error {
	if err := os.MkdirAll(m.backupDir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(m.backupDir, name+"."+stamp+".bak")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func writeAtomic(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	mode := info.Mode().Perm() | 0o200
	temporary, err := os.CreateTemp(filepath.Dir(path), ".stock-manager-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(mode); err != nil {
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

func writeMountedFile(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, info.Mode().Perm()|0o200)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, apiError{Error: message})
}
