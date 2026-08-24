package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

type Config struct {
	DataDir     string     `yaml:"data_dir"`
	DatabaseURL string     `yaml:"-"`
	FREDAPIKey  string     `yaml:"-"`
	UserAgent   string     `yaml:"user_agent"`
	HTTP        HTTP       `yaml:"http"`
	Providers   Providers  `yaml:"providers"`
	Universe    []Security `yaml:"universe"`
}

type HTTP struct {
	Timeout           time.Duration `yaml:"timeout"`
	RequestsPerSecond float64       `yaml:"requests_per_second"`
	Burst             int           `yaml:"burst"`
	MaxAttempts       int           `yaml:"max_attempts"`
	InitialBackoff    time.Duration `yaml:"initial_backoff"`
}

func (h *HTTP) UnmarshalYAML(node *yaml.Node) error {
	type wire struct {
		Timeout           string  `yaml:"timeout"`
		RequestsPerSecond float64 `yaml:"requests_per_second"`
		Burst             int     `yaml:"burst"`
		MaxAttempts       int     `yaml:"max_attempts"`
		InitialBackoff    string  `yaml:"initial_backoff"`
	}
	var w wire
	if err := node.Decode(&w); err != nil {
		return err
	}
	var err error
	if w.Timeout != "" {
		h.Timeout, err = time.ParseDuration(w.Timeout)
		if err != nil {
			return fmt.Errorf("http.timeout: %w", err)
		}
	}
	if w.InitialBackoff != "" {
		h.InitialBackoff, err = time.ParseDuration(w.InitialBackoff)
		if err != nil {
			return fmt.Errorf("http.initial_backoff: %w", err)
		}
	}
	h.RequestsPerSecond, h.Burst, h.MaxAttempts = w.RequestsPerSecond, w.Burst, w.MaxAttempts
	return nil
}

type Providers struct {
	SEC                   EnabledProvider            `yaml:"sec"`
	Prices                PriceProvider              `yaml:"prices"`
	FRED                  FREDProvider               `yaml:"fred"`
	ALFRED                ALFREDProvider             `yaml:"alfred"`
	BCB                   BCBProvider                `yaml:"bcb"`
	PTAX                  PTAXProvider               `yaml:"ptax"`
	B3                    B3Provider                 `yaml:"b3"`
	B3HistoricalPrices    B3HistoricalPriceProvider  `yaml:"b3_historical_prices"`
	B3Membership          IndexMembershipProvider    `yaml:"b3_membership"`
	B3ListingHistory      ListingHistoryProvider     `yaml:"b3_listing_history"`
	NasdaqMembership      IndexMembershipProvider    `yaml:"nasdaq_membership"`
	NasdaqCalendarHistory HistoricalCalendarProvider `yaml:"nasdaq_calendar_history"`
	B3CalendarHistory     HistoricalCalendarProvider `yaml:"b3_calendar_history"`
	SECActionHistory      CorporateActionProvider    `yaml:"sec_action_history"`
	B3ActionReplay        CorporateActionProvider    `yaml:"b3_action_replay"`
	NYSE                  CalendarProvider           `yaml:"nyse"`
	CVM                   CVMProvider                `yaml:"cvm"`
}

type EnabledProvider struct {
	Enabled bool `yaml:"enabled"`
}
type PriceProvider struct {
	Enabled bool   `yaml:"enabled"`
	Start   string `yaml:"start"`
	End     string `yaml:"end"`
}
type FREDProvider struct {
	Enabled bool     `yaml:"enabled"`
	Series  []string `yaml:"series"`
}
type ALFREDProvider struct {
	Enabled bool           `yaml:"enabled"`
	Series  []ALFREDSeries `yaml:"series"`
}
type ALFREDSeries struct {
	ID                 string `yaml:"id"`
	Geography          string `yaml:"geography"`
	Unit               string `yaml:"unit"`
	Frequency          string `yaml:"frequency"`
	SeasonalAdjustment string `yaml:"seasonal_adjustment"`
	RealtimeEnd        string `yaml:"realtime_end"`
	ObservationStart   string `yaml:"observation_start"`
	ObservationEnd     string `yaml:"observation_end"`
}
type BCBProvider struct {
	Enabled bool        `yaml:"enabled"`
	Series  []BCBSeries `yaml:"series"`
}
type BCBSeries struct {
	Code               string `yaml:"code"`
	Geography          string `yaml:"geography"`
	Unit               string `yaml:"unit"`
	Frequency          string `yaml:"frequency"`
	SeasonalAdjustment string `yaml:"seasonal_adjustment"`
	Start              string `yaml:"start"`
	End                string `yaml:"end"`
}
type PTAXProvider struct {
	Enabled bool   `yaml:"enabled"`
	Start   string `yaml:"start"`
	End     string `yaml:"end"`
}
type B3Provider struct {
	Enabled    bool             `yaml:"enabled"`
	ReportDate string           `yaml:"report_date"`
	Tickers    []string         `yaml:"tickers"`
	Calendar   CalendarProvider `yaml:"calendar"`
}
type B3HistoricalPriceProvider struct {
	Enabled bool     `yaml:"enabled"`
	Year    int      `yaml:"year"`
	Start   string   `yaml:"start"`
	End     string   `yaml:"end"`
	Tickers []string `yaml:"tickers"`
}
type CalendarProvider struct {
	Enabled       bool   `yaml:"enabled"`
	Year          int    `yaml:"year"`
	CoverageStart string `yaml:"coverage_start"`
	CoverageEnd   string `yaml:"coverage_end"`
}
type HistoricalCalendarProvider struct {
	Enabled           bool                        `yaml:"enabled"`
	Year              int                         `yaml:"year"`
	CoverageStart     string                      `yaml:"coverage_start"`
	CoverageEnd       string                      `yaml:"coverage_end"`
	RegularOpenLocal  string                      `yaml:"regular_open_local"`
	RegularCloseLocal string                      `yaml:"regular_close_local"`
	Versions          []HistoricalCalendarVersion `yaml:"versions"`
}
type HistoricalCalendarVersion struct {
	AvailableAt string                       `yaml:"available_at"`
	Revision    int                          `yaml:"revision"`
	Resources   []HistoricalCalendarResource `yaml:"resources"`
	Events      []HistoricalCalendarEvent    `yaml:"events"`
}
type HistoricalCalendarResource struct {
	Kind        string `yaml:"kind"`
	URL         string `yaml:"url"`
	SHA256      string `yaml:"sha256"`
	ContentType string `yaml:"content_type"`
}
type HistoricalCalendarEvent struct {
	Date          string `yaml:"date"`
	Status        string `yaml:"status"`
	OpenLocal     string `yaml:"open_local"`
	CloseLocal    string `yaml:"close_local"`
	ResourceKind  string `yaml:"resource_kind"`
	SourceLocator string `yaml:"source_locator"`
}
type CorporateActionProvider struct {
	Enabled            bool                      `yaml:"enabled"`
	AvailabilityPolicy string                    `yaml:"availability_policy"`
	Resources          []CorporateActionResource `yaml:"resources"`
	Actions            []CorporateActionVersion  `yaml:"actions"`
}
type CorporateActionResource struct {
	Kind        string `yaml:"kind"`
	URL         string `yaml:"url"`
	SHA256      string `yaml:"sha256"`
	ContentType string `yaml:"content_type"`
}
type CorporateActionVersion struct {
	SecurityID         string `yaml:"security_id"`
	SourceEventID      string `yaml:"source_event_id"`
	Revision           int    `yaml:"revision"`
	ActionStatus       string `yaml:"action_status"`
	ActionType         string `yaml:"action_type"`
	ObservedAt         string `yaml:"observed_at"`
	ObservedPrecision  string `yaml:"observed_precision"`
	PublishedAt        string `yaml:"published_at"`
	PublishedPrecision string `yaml:"published_precision"`
	AvailableAt        string `yaml:"available_at"`
	EffectiveAt        string `yaml:"effective_at"`
	EffectivePrecision string `yaml:"effective_precision"`
	RecordDate         string `yaml:"record_date"`
	PaymentDate        string `yaml:"payment_date"`
	RatioNumerator     string `yaml:"ratio_numerator"`
	RatioDenominator   string `yaml:"ratio_denominator"`
	CashAmount         string `yaml:"cash_amount"`
	Currency           string `yaml:"currency"`
	TargetSecurityID   string `yaml:"target_security_id"`
	ResourceKind       string `yaml:"resource_kind"`
	SourceLocator      string `yaml:"source_locator"`
}
type IndexMembershipProvider struct {
	Enabled    bool     `yaml:"enabled"`
	UniverseID string   `yaml:"universe_id"`
	Tickers    []string `yaml:"tickers"`
	Notices    []string `yaml:"notices"`
}
type ListingHistoryProvider struct {
	Enabled bool                   `yaml:"enabled"`
	Notices []ListingHistoryNotice `yaml:"notices"`
}
type ListingHistoryNotice struct {
	URL         string `yaml:"url"`
	TradingName string `yaml:"trading_name"`
	Ticker      string `yaml:"ticker"`
	ValidFrom   string `yaml:"valid_from"`
}
type CVMProvider struct {
	Enabled bool         `yaml:"enabled"`
	CAD     bool         `yaml:"cad"`
	IPE     CVMIPEConfig `yaml:"ipe"`
}
type CVMIPEConfig struct {
	Years []int `yaml:"years"`
}
type Security struct {
	IssuerID            string `yaml:"issuer_id"`
	SecurityID          string `yaml:"security_id"`
	LegalName           string `yaml:"legal_name"`
	CountryCode         string `yaml:"country_code"`
	SecurityType        string `yaml:"security_type"`
	PrimaryListing      bool   `yaml:"primary_listing"`
	CIK                 int64  `yaml:"cik"`
	CVMCode             string `yaml:"cvm_code"`
	Ticker              string `yaml:"ticker"`
	ISIN                string `yaml:"isin"`
	IdentifierValidFrom string `yaml:"identifier_valid_from"`
	YahooSymbol         string `yaml:"yahoo_symbol"`
	Exchange            string `yaml:"exchange"`
	MIC                 string `yaml:"mic"`
	Currency            string `yaml:"currency"`
}

func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	applyEnv(&c)
	applyDefaults(&c)
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func applyEnv(c *Config) {
	c.DatabaseURL = os.Getenv("DATABASE_URL")
	c.FREDAPIKey = strings.TrimSpace(os.Getenv("FRED_API_KEY"))
	if v := os.Getenv("INVS_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv("INVS_USER_AGENT"); v != "" {
		c.UserAgent = v
	} else if v := os.Getenv("SEC_USER_AGENT"); v != "" {
		c.UserAgent = v
	}
	if v := os.Getenv("INVS_HTTP_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.HTTP.Timeout = d
		}
	}
	if v := os.Getenv("INVS_HTTP_REQUESTS_PER_SECOND"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			c.HTTP.RequestsPerSecond = n
		}
	}
}

func applyDefaults(c *Config) {
	if c.DataDir == "" {
		c.DataDir = "./data"
	}
	if c.HTTP.Timeout == 0 {
		c.HTTP.Timeout = 30 * time.Second
	}
	if c.HTTP.RequestsPerSecond == 0 {
		c.HTTP.RequestsPerSecond = 5
	}
	if c.HTTP.Burst == 0 {
		c.HTTP.Burst = 1
	}
	if c.HTTP.MaxAttempts == 0 {
		c.HTTP.MaxAttempts = 4
	}
	if c.HTTP.InitialBackoff == 0 {
		c.HTTP.InitialBackoff = 500 * time.Millisecond
	}
}

func (c Config) Validate() error {
	var errs []error
	if len(c.Universe) == 0 {
		errs = append(errs, errors.New("universe must contain at least one security"))
	}
	if strings.TrimSpace(c.UserAgent) == "" {
		errs = append(errs, errors.New("user_agent is required (SEC requires contact information)"))
	}
	if c.HTTP.Timeout <= 0 {
		errs = append(errs, errors.New("http.timeout must be positive"))
	}
	if c.HTTP.RequestsPerSecond <= 0 {
		errs = append(errs, errors.New("http.requests_per_second must be positive"))
	}
	if c.Providers.SEC.Enabled && (strings.Contains(strings.ToLower(c.UserAgent), "example.com") || !strings.Contains(c.UserAgent, "@")) {
		errs = append(errs, errors.New("enabled SEC provider requires a real contact email in user_agent"))
	}
	if c.Providers.SEC.Enabled {
		hasCIK := false
		for _, security := range c.Universe {
			if security.CIK > 0 {
				hasCIK = true
				break
			}
		}
		if !hasCIK {
			errs = append(errs, errors.New("enabled SEC provider requires at least one universe issuer with a CIK"))
		}
	}
	if c.Providers.Prices.Enabled {
		if _, err := time.Parse("2006-01-02", c.Providers.Prices.Start); err != nil {
			errs = append(errs, errors.New("enabled prices provider requires a valid start date"))
		}
	}
	if c.Providers.FRED.Enabled && len(c.Providers.FRED.Series) == 0 {
		errs = append(errs, errors.New("enabled FRED provider requires at least one series"))
	}
	if c.Providers.ALFRED.Enabled {
		if !validFREDAPIKey(c.FREDAPIKey) {
			errs = append(errs, errors.New("enabled ALFRED provider requires FRED_API_KEY as 32 lowercase alphanumeric characters"))
		}
		if len(c.Providers.ALFRED.Series) == 0 {
			errs = append(errs, errors.New("enabled ALFRED provider requires at least one series"))
		}
		seenSeries := make(map[string]bool, len(c.Providers.ALFRED.Series))
		for i, series := range c.Providers.ALFRED.Series {
			pfx := fmt.Sprintf("providers.alfred.series[%d]", i)
			if !validSourceIdentifier(series.ID) {
				errs = append(errs, fmt.Errorf("%s.id must contain only letters, digits, '.', '_' or '-'", pfx))
			}
			if strings.TrimSpace(series.Geography) == "" {
				errs = append(errs, fmt.Errorf("%s.geography is required", pfx))
			}
			if strings.TrimSpace(series.Unit) == "" {
				errs = append(errs, fmt.Errorf("%s.unit is required", pfx))
			}
			if !validEconomicFrequency(series.Frequency) {
				errs = append(errs, fmt.Errorf("%s.frequency is unsupported", pfx))
			}
			if series.SeasonalAdjustment != "" && strings.TrimSpace(series.SeasonalAdjustment) == "" {
				errs = append(errs, fmt.Errorf("%s.seasonal_adjustment must not be whitespace", pfx))
			}
			realtimeEnd, realtimeEndErr := requiredISODate(series.RealtimeEnd)
			if realtimeEndErr != nil {
				errs = append(errs, fmt.Errorf("%s.realtime_end must be an ISO date", pfx))
			}
			if realtimeEndErr == nil && realtimeEnd.Before(time.Date(1776, 7, 4, 0, 0, 0, 0, time.UTC)) {
				errs = append(errs, fmt.Errorf("%s.realtime_end precedes ALFRED's earliest supported realtime date", pfx))
			}
			observationStart, observationStartErr := optionalISODate(series.ObservationStart)
			observationEnd, observationEndErr := optionalISODate(series.ObservationEnd)
			if observationStartErr != nil {
				errs = append(errs, fmt.Errorf("%s.observation_start must be an ISO date", pfx))
			}
			if observationEndErr != nil {
				errs = append(errs, fmt.Errorf("%s.observation_end must be an ISO date", pfx))
			}
			if observationStartErr == nil && observationEndErr == nil && !observationStart.IsZero() && !observationEnd.IsZero() && observationEnd.Before(observationStart) {
				errs = append(errs, fmt.Errorf("%s.observation_end must not precede observation_start", pfx))
			}
			if validSourceIdentifier(series.ID) {
				if seenSeries[series.ID] {
					errs = append(errs, fmt.Errorf("%s.id duplicates %q", pfx, series.ID))
				}
				seenSeries[series.ID] = true
			}
		}
	}
	if c.Providers.BCB.Enabled {
		if len(c.Providers.BCB.Series) == 0 {
			errs = append(errs, errors.New("enabled BCB provider requires at least one series"))
		}
		seenCodes := make(map[string]bool, len(c.Providers.BCB.Series))
		for i, series := range c.Providers.BCB.Series {
			pfx := fmt.Sprintf("providers.bcb.series[%d]", i)
			if !validBCBCode(series.Code) {
				errs = append(errs, fmt.Errorf("%s.code must be a positive canonical decimal code", pfx))
			}
			if strings.TrimSpace(series.Geography) == "" {
				errs = append(errs, fmt.Errorf("%s.geography is required", pfx))
			}
			if strings.TrimSpace(series.Unit) == "" {
				errs = append(errs, fmt.Errorf("%s.unit is required", pfx))
			}
			if !validEconomicFrequency(series.Frequency) {
				errs = append(errs, fmt.Errorf("%s.frequency is unsupported", pfx))
			}
			if series.SeasonalAdjustment != "" && strings.TrimSpace(series.SeasonalAdjustment) == "" {
				errs = append(errs, fmt.Errorf("%s.seasonal_adjustment must not be whitespace", pfx))
			}
			start, startErr := optionalISODate(series.Start)
			if startErr != nil {
				errs = append(errs, fmt.Errorf("%s.start must be an ISO date", pfx))
			}
			end, endErr := optionalISODate(series.End)
			if endErr != nil {
				errs = append(errs, fmt.Errorf("%s.end must be an ISO date", pfx))
			}
			if startErr == nil && endErr == nil && !start.IsZero() && !end.IsZero() && end.Before(start) {
				errs = append(errs, fmt.Errorf("%s.end must not precede start", pfx))
			}
			if validBCBCode(series.Code) {
				code := strings.TrimSpace(series.Code)
				if seenCodes[code] {
					errs = append(errs, fmt.Errorf("%s.code duplicates %q", pfx, code))
				}
				seenCodes[code] = true
			}
		}
	}
	if c.Providers.PTAX.Enabled {
		start, startErr := requiredISODate(c.Providers.PTAX.Start)
		end, endErr := requiredISODate(c.Providers.PTAX.End)
		if startErr != nil {
			errs = append(errs, errors.New("enabled PTAX provider requires a valid start date"))
		}
		if endErr != nil {
			errs = append(errs, errors.New("enabled PTAX provider requires a valid end date"))
		}
		if startErr == nil && start.Before(time.Date(1984, 11, 28, 0, 0, 0, 0, time.UTC)) {
			errs = append(errs, errors.New("providers.ptax.start precedes available USD/BRL history"))
		}
		if startErr == nil && endErr == nil {
			if end.Before(start) {
				errs = append(errs, errors.New("providers.ptax.end must not precede start"))
			} else if int(end.Sub(start).Hours()/24)+1 > 366 {
				errs = append(errs, errors.New("providers.ptax range must not exceed 366 inclusive days"))
			}
		}
	}
	if c.Providers.B3.Enabled {
		reportDate, reportDateErr := requiredISODate(c.Providers.B3.ReportDate)
		if reportDateErr != nil {
			errs = append(errs, errors.New("enabled B3 provider requires a valid report_date"))
		}
		if len(c.Providers.B3.Tickers) == 0 {
			errs = append(errs, errors.New("enabled B3 provider requires at least one ticker"))
		}
		seenTickers := make(map[string]bool, len(c.Providers.B3.Tickers))
		universeByTicker := make(map[string][]Security)
		for _, security := range c.Universe {
			ticker := strings.ToUpper(strings.TrimSpace(security.Ticker))
			universeByTicker[ticker] = append(universeByTicker[ticker], security)
		}
		for i, value := range c.Providers.B3.Tickers {
			pfx := fmt.Sprintf("providers.b3.tickers[%d]", i)
			ticker := strings.TrimSpace(value)
			if ticker != value || ticker == "" || !validB3Ticker(ticker) {
				errs = append(errs, fmt.Errorf("%s must be an uppercase canonical B3 ticker", pfx))
			}
			if seenTickers[ticker] {
				errs = append(errs, fmt.Errorf("%s duplicates %q", pfx, ticker))
			}
			seenTickers[ticker] = true
			matches := universeByTicker[ticker]
			if len(matches) != 1 {
				errs = append(errs, fmt.Errorf("%s must match exactly one universe ticker", pfx))
				continue
			}
			security := matches[0]
			if security.CountryCode != "BR" || security.Exchange != "B3" || security.MIC != "BVMF" || security.Currency != "BRL" {
				errs = append(errs, fmt.Errorf("%s universe mapping must declare BR/B3/BVMF/BRL", pfx))
			}
			if !validISIN(security.ISIN) {
				errs = append(errs, fmt.Errorf("%s universe mapping requires a valid uppercase ISIN", pfx))
			}
		}
		if reportDateErr == nil && reportDate.After(time.Now().UTC().Truncate(24*time.Hour)) {
			errs = append(errs, errors.New("enabled B3 provider report_date must not be in the future"))
		}
		if c.Providers.B3.Calendar.Enabled {
			errs = append(errs, validateCalendarProvider("providers.b3.calendar", c.Providers.B3.Calendar)...)
		}
	} else if c.Providers.B3.Calendar.Enabled {
		errs = append(errs, errors.New("providers.b3.calendar requires providers.b3.enabled"))
	}
	if c.Providers.B3HistoricalPrices.Enabled {
		provider := c.Providers.B3HistoricalPrices
		currentYear := time.Now().UTC().Year()
		if provider.Year < 1986 || provider.Year >= currentYear {
			errs = append(errs, fmt.Errorf("providers.b3_historical_prices.year must be between 1986 and %d", currentYear-1))
		}
		start, startErr := requiredISODate(provider.Start)
		end, endErr := requiredISODate(provider.End)
		if startErr != nil {
			errs = append(errs, errors.New("providers.b3_historical_prices.start must be an ISO date"))
		}
		if endErr != nil {
			errs = append(errs, errors.New("providers.b3_historical_prices.end must be an ISO date"))
		}
		if startErr == nil && endErr == nil && (start.Year() != provider.Year || end.Year() != provider.Year || end.Before(start)) {
			errs = append(errs, errors.New("providers.b3_historical_prices start/end must be an ordered range within year"))
		}
		if len(provider.Tickers) == 0 {
			errs = append(errs, errors.New("enabled B3 historical prices provider requires at least one ticker"))
		}
		universeByTicker := make(map[string][]Security)
		for _, security := range c.Universe {
			ticker := strings.ToUpper(strings.TrimSpace(security.Ticker))
			universeByTicker[ticker] = append(universeByTicker[ticker], security)
		}
		seenTickers := make(map[string]bool, len(provider.Tickers))
		for index, value := range provider.Tickers {
			prefix := fmt.Sprintf("providers.b3_historical_prices.tickers[%d]", index)
			ticker := strings.TrimSpace(value)
			if ticker != value || !validB3Ticker(ticker) {
				errs = append(errs, fmt.Errorf("%s must be an uppercase canonical B3 ticker", prefix))
			}
			if seenTickers[ticker] {
				errs = append(errs, fmt.Errorf("%s duplicates %q", prefix, ticker))
			}
			seenTickers[ticker] = true
			matches := universeByTicker[ticker]
			if len(matches) != 1 {
				errs = append(errs, fmt.Errorf("%s must match exactly one universe ticker", prefix))
				continue
			}
			security := matches[0]
			if security.CountryCode != "BR" || security.Exchange != "B3" || security.MIC != "BVMF" || security.Currency != "BRL" {
				errs = append(errs, fmt.Errorf("%s universe mapping must declare BR/B3/BVMF/BRL", prefix))
			}
			if !validISIN(security.ISIN) {
				errs = append(errs, fmt.Errorf("%s universe mapping requires a valid uppercase ISIN", prefix))
			}
		}
	}
	if c.Providers.NYSE.Enabled {
		errs = append(errs, validateCalendarProvider("providers.nyse", c.Providers.NYSE)...)
	}
	if c.Providers.NasdaqMembership.Enabled {
		errs = append(errs, validateIndexMembershipProvider("providers.nasdaq_membership", c.Providers.NasdaqMembership, c.Universe, "US", "NASDAQ", "XNAS", "USD", "www.globenewswire.com", "/news-release/")...)
	}
	if c.Providers.NasdaqCalendarHistory.Enabled {
		errs = append(errs, validateHistoricalCalendarProvider(
			"providers.nasdaq_calendar_history", c.Providers.NasdaqCalendarHistory,
			[]string{"www.nasdaqtrader.com", "nasdaqtrader.com"},
			[]string{"/content/technicalsupport/", "/content/productsservices/trading/", "/TraderNews.aspx"},
		)...)
	}
	if c.Providers.B3CalendarHistory.Enabled {
		errs = append(errs, validateHistoricalCalendarProvider(
			"providers.b3_calendar_history", c.Providers.B3CalendarHistory,
			[]string{"www.b3.com.br", "b3.com.br"}, []string{"/data/files/"},
		)...)
	}
	if c.Providers.SECActionHistory.Enabled {
		errs = append(errs, validateCorporateActionProvider(
			"providers.sec_action_history", c.Providers.SECActionHistory, c.Universe,
			[]string{"www.sec.gov", "sec.gov"}, []string{"/Archives/edgar/data/"},
		)...)
	}
	if c.Providers.B3ActionReplay.Enabled {
		errs = append(errs, validateCorporateActionProvider(
			"providers.b3_action_replay", c.Providers.B3ActionReplay, c.Universe,
			[]string{"www.b3.com.br", "b3.com.br"}, []string{"/data/files/"},
		)...)
	}
	if c.Providers.B3Membership.Enabled {
		errs = append(errs, validateIndexMembershipProvider("providers.b3_membership", c.Providers.B3Membership, c.Universe, "BR", "B3", "BVMF", "BRL", "www.b3.com.br", "/pt_br/noticias/")...)
	}
	if c.Providers.B3ListingHistory.Enabled {
		errs = append(errs, validateB3ListingHistory(c.Providers.B3ListingHistory, c.Providers.B3Membership, c.Universe)...)
	}
	if c.Providers.CVM.Enabled {
		if !c.Providers.CVM.CAD && len(c.Providers.CVM.IPE.Years) == 0 {
			errs = append(errs, errors.New("enabled CVM provider requires cad or at least one IPE year"))
		}
		seenYears := make(map[int]bool, len(c.Providers.CVM.IPE.Years))
		currentYear := time.Now().UTC().Year()
		for i, year := range c.Providers.CVM.IPE.Years {
			if year < 2003 || year > currentYear {
				errs = append(errs, fmt.Errorf("providers.cvm.ipe.years[%d] must be between 2003 and %d", i, currentYear))
			}
			if seenYears[year] {
				errs = append(errs, fmt.Errorf("providers.cvm.ipe.years[%d] duplicates %d", i, year))
			}
			seenYears[year] = true
		}
	}
	if !c.Providers.SEC.Enabled && !c.Providers.Prices.Enabled && !c.Providers.FRED.Enabled && !c.Providers.ALFRED.Enabled && !c.Providers.BCB.Enabled && !c.Providers.PTAX.Enabled && !c.Providers.B3.Enabled && !c.Providers.B3HistoricalPrices.Enabled && !c.Providers.B3Membership.Enabled && !c.Providers.B3ListingHistory.Enabled && !c.Providers.NasdaqMembership.Enabled && !c.Providers.NasdaqCalendarHistory.Enabled && !c.Providers.B3CalendarHistory.Enabled && !c.Providers.SECActionHistory.Enabled && !c.Providers.B3ActionReplay.Enabled && !c.Providers.NYSE.Enabled && !c.Providers.CVM.Enabled {
		errs = append(errs, errors.New("at least one provider must be enabled"))
	}
	seenIssuer, seenSecurity := map[string]bool{}, map[string]bool{}
	for i, s := range c.Universe {
		pfx := fmt.Sprintf("universe[%d]", i)
		if s.IssuerID == "" || s.SecurityID == "" {
			errs = append(errs, fmt.Errorf("%s: issuer_id and security_id are required", pfx))
		}
		if _, err := uuid.Parse(s.IssuerID); err != nil {
			errs = append(errs, fmt.Errorf("%s: issuer_id must be a UUID", pfx))
		}
		if _, err := uuid.Parse(s.SecurityID); err != nil {
			errs = append(errs, fmt.Errorf("%s: security_id must be a UUID", pfx))
		}
		if !upperAlpha(s.CountryCode, 2) {
			errs = append(errs, fmt.Errorf("%s: country_code must be ISO alpha-2 uppercase", pfx))
		}
		if !validType(s.SecurityType) {
			errs = append(errs, fmt.Errorf("%s: security_type must be lowercase snake_case", pfx))
		}
		if !upperAlnum(s.MIC, 4) {
			errs = append(errs, fmt.Errorf("%s: mic must be four uppercase characters", pfx))
		}
		if seenIssuer[s.IssuerID] {
			errs = append(errs, fmt.Errorf("%s: duplicate issuer_id %q", pfx, s.IssuerID))
		}
		if seenSecurity[s.SecurityID] {
			errs = append(errs, fmt.Errorf("%s: duplicate security_id %q", pfx, s.SecurityID))
		}
		seenIssuer[s.IssuerID], seenSecurity[s.SecurityID] = true, true
		if s.CIK < 0 {
			errs = append(errs, fmt.Errorf("%s: cik must not be negative", pfx))
		} else if s.CIK > 9999999999 {
			errs = append(errs, fmt.Errorf("%s: cik must contain at most 10 digits", pfx))
		}
		if s.CVMCode != "" && !validCVMCode(s.CVMCode) {
			errs = append(errs, fmt.Errorf("%s: cvm_code must contain only letters, digits, '.', '_' or '-'", pfx))
		}
		if strings.TrimSpace(s.LegalName) == "" {
			errs = append(errs, fmt.Errorf("%s: legal_name is required", pfx))
		}
		if strings.TrimSpace(s.Ticker) == "" {
			errs = append(errs, fmt.Errorf("%s: ticker is required", pfx))
		}
		if !upperAlpha(s.Currency, 3) {
			errs = append(errs, fmt.Errorf("%s: currency must be three uppercase letters", pfx))
		}
		if c.Providers.Prices.Enabled && strings.TrimSpace(s.YahooSymbol) == "" {
			errs = append(errs, fmt.Errorf("%s: yahoo_symbol required when prices enabled", pfx))
		}
		{
			validFrom, err := time.Parse("2006-01-02", s.IdentifierValidFrom)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: identifier_valid_from must be ISO date", pfx))
			} else if start, parseErr := time.Parse("2006-01-02", c.Providers.Prices.Start); c.Providers.Prices.Enabled && parseErr == nil && validFrom.After(start) {
				errs = append(errs, fmt.Errorf("%s: identifier_valid_from must not follow prices.start", pfx))
			}
		}
	}
	return errors.Join(errs...)
}

func validateIndexMembershipProvider(prefix string, provider IndexMembershipProvider, universe []Security, country, exchange, mic, currency, host, pathPrefix string) []error {
	var errs []error
	if !validType(provider.UniverseID) {
		errs = append(errs, fmt.Errorf("%s.universe_id must be lowercase snake_case", prefix))
	}
	if len(provider.Tickers) == 0 {
		errs = append(errs, fmt.Errorf("%s requires at least one ticker", prefix))
	}
	seenTickers := make(map[string]struct{}, len(provider.Tickers))
	for index, value := range provider.Tickers {
		itemPrefix := fmt.Sprintf("%s.tickers[%d]", prefix, index)
		if value != strings.TrimSpace(value) || value != strings.ToUpper(value) || !validB3Ticker(value) {
			errs = append(errs, fmt.Errorf("%s must be an uppercase canonical ticker", itemPrefix))
			continue
		}
		if _, duplicate := seenTickers[value]; duplicate {
			errs = append(errs, fmt.Errorf("%s duplicates %q", itemPrefix, value))
			continue
		}
		seenTickers[value] = struct{}{}
		matches := 0
		for _, security := range universe {
			if security.Ticker == value && security.CountryCode == country && security.Exchange == exchange && security.MIC == mic && security.Currency == currency && security.PrimaryListing {
				matches++
			}
		}
		if matches != 1 {
			errs = append(errs, fmt.Errorf("%s must match exactly one primary %s/%s/%s/%s universe security", itemPrefix, country, exchange, mic, currency))
		}
	}
	if len(provider.Notices) == 0 {
		errs = append(errs, fmt.Errorf("%s requires at least one notice URL", prefix))
	}
	seenNotices := make(map[string]struct{}, len(provider.Notices))
	for index, value := range provider.Notices {
		itemPrefix := fmt.Sprintf("%s.notices[%d]", prefix, index)
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme != "https" || parsed.Host != host || !strings.HasPrefix(parsed.Path, pathPrefix) || parsed.RawQuery != "" || parsed.Fragment != "" {
			errs = append(errs, fmt.Errorf("%s must use the admitted HTTPS %s%s source boundary", itemPrefix, host, pathPrefix))
			continue
		}
		if _, duplicate := seenNotices[value]; duplicate {
			errs = append(errs, fmt.Errorf("%s duplicates %q", itemPrefix, value))
		}
		seenNotices[value] = struct{}{}
	}
	return errs
}

func validateB3ListingHistory(provider ListingHistoryProvider, membership IndexMembershipProvider, universe []Security) []error {
	const prefix = "providers.b3_listing_history"
	var errs []error
	if !membership.Enabled {
		errs = append(errs, errors.New("providers.b3_listing_history requires providers.b3_membership"))
	}
	if len(provider.Notices) == 0 {
		errs = append(errs, errors.New("providers.b3_listing_history requires at least one notice"))
	}
	membershipTickers := make(map[string]struct{}, len(membership.Tickers))
	for _, ticker := range membership.Tickers {
		membershipTickers[ticker] = struct{}{}
	}
	seenTickers := make(map[string]struct{}, len(provider.Notices))
	seenURLs := make(map[string]struct{}, len(provider.Notices))
	for index, notice := range provider.Notices {
		itemPrefix := fmt.Sprintf("%s.notices[%d]", prefix, index)
		if notice.TradingName != strings.TrimSpace(notice.TradingName) || notice.TradingName != strings.ToUpper(notice.TradingName) || !validB3Ticker(notice.TradingName) {
			errs = append(errs, fmt.Errorf("%s.trading_name must be an uppercase canonical B3 trading name", itemPrefix))
		}
		if notice.Ticker != strings.TrimSpace(notice.Ticker) || notice.Ticker != strings.ToUpper(notice.Ticker) || !validB3Ticker(notice.Ticker) {
			errs = append(errs, fmt.Errorf("%s.ticker must be an uppercase canonical B3 ticker", itemPrefix))
		}
		if _, exists := membershipTickers[notice.Ticker]; !exists {
			errs = append(errs, fmt.Errorf("%s.ticker must be configured in providers.b3_membership", itemPrefix))
		}
		if _, duplicate := seenTickers[notice.Ticker]; duplicate {
			errs = append(errs, fmt.Errorf("%s.ticker duplicates %q", itemPrefix, notice.Ticker))
		}
		seenTickers[notice.Ticker] = struct{}{}
		validFrom, validFromErr := time.Parse(time.RFC3339, notice.ValidFrom)
		if validFromErr != nil || notice.ValidFrom != validFrom.UTC().Format(time.RFC3339) {
			errs = append(errs, fmt.Errorf("%s.valid_from must be a canonical UTC RFC 3339 timestamp", itemPrefix))
		}
		parsed, parseErr := url.Parse(notice.URL)
		if parseErr != nil || parsed.Scheme != "https" || parsed.Host != "sistemasweb.b3.com.br" || parsed.Path != "/PlantaoNoticias/Noticias/Detail" || parsed.Fragment != "" {
			errs = append(errs, fmt.Errorf("%s.url must use the admitted B3 Plantao detail source", itemPrefix))
		} else {
			query := parsed.Query()
			if len(query) != 3 || len(query["agencia"]) != 1 || len(query["dataNoticia"]) != 1 || len(query["idNoticia"]) != 1 {
				errs = append(errs, fmt.Errorf("%s.url requires exactly agencia, dataNoticia, and idNoticia", itemPrefix))
			}
		}
		if _, duplicate := seenURLs[notice.URL]; duplicate {
			errs = append(errs, fmt.Errorf("%s.url duplicates %q", itemPrefix, notice.URL))
		}
		seenURLs[notice.URL] = struct{}{}
		matches := 0
		for _, security := range universe {
			if security.Ticker == notice.Ticker && security.CountryCode == "BR" && security.Exchange == "B3" && security.MIC == "BVMF" && security.Currency == "BRL" && security.PrimaryListing {
				matches++
			}
		}
		if matches != 1 {
			errs = append(errs, fmt.Errorf("%s.ticker must match exactly one primary BR/B3/BVMF/BRL universe security", itemPrefix))
		}
	}
	return errs
}

func validateCalendarProvider(prefix string, provider CalendarProvider) []error {
	var errs []error
	currentYear := time.Now().UTC().Year()
	if provider.Year < 2000 || provider.Year > currentYear+2 {
		errs = append(errs, fmt.Errorf("%s.year must be between 2000 and %d", prefix, currentYear+2))
	}
	start, startErr := requiredISODate(provider.CoverageStart)
	end, endErr := requiredISODate(provider.CoverageEnd)
	if startErr != nil {
		errs = append(errs, fmt.Errorf("%s.coverage_start must be an ISO date", prefix))
	}
	if endErr != nil {
		errs = append(errs, fmt.Errorf("%s.coverage_end must be an ISO date", prefix))
	}
	if startErr == nil && endErr == nil {
		if end.Before(start) {
			errs = append(errs, fmt.Errorf("%s.coverage_end must not precede coverage_start", prefix))
		}
		if start.Year() != provider.Year || end.Year() != provider.Year {
			errs = append(errs, fmt.Errorf("%s coverage dates must be within year %d", prefix, provider.Year))
		}
		if end.Sub(start) > 369*24*time.Hour {
			errs = append(errs, fmt.Errorf("%s coverage cannot exceed 370 days", prefix))
		}
	}
	return errs
}

func validateHistoricalCalendarProvider(prefix string, provider HistoricalCalendarProvider, allowedHosts, pathPrefixes []string) []error {
	var errs []error
	base := CalendarProvider{
		Enabled: provider.Enabled, Year: provider.Year,
		CoverageStart: provider.CoverageStart, CoverageEnd: provider.CoverageEnd,
	}
	errs = append(errs, validateCalendarProvider(prefix, base)...)
	if !validLocalClock(provider.RegularOpenLocal) || !validLocalClock(provider.RegularCloseLocal) || provider.RegularOpenLocal >= provider.RegularCloseLocal {
		errs = append(errs, fmt.Errorf("%s regular local hours must be canonical increasing HH:MM values", prefix))
	}
	if len(provider.Versions) == 0 {
		errs = append(errs, fmt.Errorf("%s requires at least one version", prefix))
		return errs
	}
	coverageStart, startErr := requiredISODate(provider.CoverageStart)
	coverageEnd, endErr := requiredISODate(provider.CoverageEnd)
	var previousAvailability time.Time
	for versionIndex, version := range provider.Versions {
		versionPrefix := fmt.Sprintf("%s.versions[%d]", prefix, versionIndex)
		if version.Revision != versionIndex {
			errs = append(errs, fmt.Errorf("%s.revision must equal its zero-based version index", versionPrefix))
		}
		availableAt, availableErr := canonicalUTCTimestamp(version.AvailableAt)
		if availableErr != nil {
			errs = append(errs, fmt.Errorf("%s.available_at must be a canonical UTC RFC 3339 timestamp", versionPrefix))
		} else if availableAt.After(time.Now().UTC()) {
			errs = append(errs, fmt.Errorf("%s.available_at must not be in the future", versionPrefix))
		} else if !previousAvailability.IsZero() && !availableAt.After(previousAvailability) {
			errs = append(errs, fmt.Errorf("%s.available_at must be later than the prior version", versionPrefix))
		}
		if availableErr == nil {
			previousAvailability = availableAt
		}
		if len(version.Resources) == 0 {
			errs = append(errs, fmt.Errorf("%s requires at least one resource", versionPrefix))
		}
		resourceKinds := make(map[string]struct{}, len(version.Resources))
		resourceURLs := make(map[string]struct{}, len(version.Resources))
		for resourceIndex, resource := range version.Resources {
			resourcePrefix := fmt.Sprintf("%s.resources[%d]", versionPrefix, resourceIndex)
			if !validSourceIdentifier(resource.Kind) {
				errs = append(errs, fmt.Errorf("%s.kind must be a canonical source identifier", resourcePrefix))
			}
			if _, duplicate := resourceKinds[resource.Kind]; duplicate {
				errs = append(errs, fmt.Errorf("%s.kind duplicates %q", resourcePrefix, resource.Kind))
			}
			resourceKinds[resource.Kind] = struct{}{}
			parsed, parseErr := url.Parse(resource.URL)
			if parseErr != nil || !admittedHistoricalCalendarURL(parsed, allowedHosts, pathPrefixes) {
				errs = append(errs, fmt.Errorf("%s.url must be an admitted official HTTPS artifact", resourcePrefix))
			}
			if _, duplicate := resourceURLs[resource.URL]; duplicate {
				errs = append(errs, fmt.Errorf("%s.url duplicates %q", resourcePrefix, resource.URL))
			}
			resourceURLs[resource.URL] = struct{}{}
			if !validSHA256(resource.SHA256) {
				errs = append(errs, fmt.Errorf("%s.sha256 must be a lowercase SHA-256", resourcePrefix))
			}
			if resource.ContentType != "application/pdf" && resource.ContentType != "text/html; charset=utf-8" {
				errs = append(errs, fmt.Errorf("%s.content_type must be application/pdf or text/html; charset=utf-8", resourcePrefix))
			}
		}
		seenDates := make(map[string]struct{}, len(version.Events))
		for eventIndex, event := range version.Events {
			eventPrefix := fmt.Sprintf("%s.events[%d]", versionPrefix, eventIndex)
			date, dateErr := requiredISODate(event.Date)
			if dateErr != nil {
				errs = append(errs, fmt.Errorf("%s.date must be an ISO date", eventPrefix))
			} else if startErr == nil && endErr == nil && (date.Before(coverageStart) || date.After(coverageEnd)) {
				errs = append(errs, fmt.Errorf("%s.date is outside configured coverage", eventPrefix))
			}
			if _, duplicate := seenDates[event.Date]; duplicate {
				errs = append(errs, fmt.Errorf("%s.date duplicates %q", eventPrefix, event.Date))
			}
			seenDates[event.Date] = struct{}{}
			if event.Status != "closed" && event.Status != "open" {
				errs = append(errs, fmt.Errorf("%s.status must be closed or open", eventPrefix))
			}
			if event.Status == "closed" && (event.OpenLocal != "" || event.CloseLocal != "") {
				errs = append(errs, fmt.Errorf("%s closed event cannot declare hours", eventPrefix))
			}
			if event.Status == "open" {
				if event.OpenLocal == "" && event.CloseLocal == "" {
					errs = append(errs, fmt.Errorf("%s open event must override at least one session boundary", eventPrefix))
				}
				if event.OpenLocal != "" && !validLocalClock(event.OpenLocal) {
					errs = append(errs, fmt.Errorf("%s.open_local must be canonical HH:MM", eventPrefix))
				}
				if event.CloseLocal != "" && !validLocalClock(event.CloseLocal) {
					errs = append(errs, fmt.Errorf("%s.close_local must be canonical HH:MM", eventPrefix))
				}
			}
			if _, exists := resourceKinds[event.ResourceKind]; !exists {
				errs = append(errs, fmt.Errorf("%s.resource_kind must name a version resource", eventPrefix))
			}
			if strings.TrimSpace(event.SourceLocator) == "" || event.SourceLocator != strings.TrimSpace(event.SourceLocator) {
				errs = append(errs, fmt.Errorf("%s.source_locator is required and canonical", eventPrefix))
			}
		}
	}
	return errs
}

func validateCorporateActionProvider(prefix string, provider CorporateActionProvider, universe []Security, allowedHosts, pathPrefixes []string) []error {
	var errs []error
	if provider.AvailabilityPolicy != "source_publication" && provider.AvailabilityPolicy != "installation_receipt" {
		errs = append(errs, fmt.Errorf("%s.availability_policy must be source_publication or installation_receipt", prefix))
	}
	if len(provider.Resources) == 0 || len(provider.Actions) == 0 {
		errs = append(errs, fmt.Errorf("%s requires resources and actions", prefix))
	}
	securityIDs := make(map[string]struct{}, len(universe))
	for _, security := range universe {
		securityIDs[security.SecurityID] = struct{}{}
	}
	resourceKinds := make(map[string]struct{}, len(provider.Resources))
	for index, resource := range provider.Resources {
		resourcePrefix := fmt.Sprintf("%s.resources[%d]", prefix, index)
		if !validSourceIdentifier(resource.Kind) {
			errs = append(errs, fmt.Errorf("%s.kind must be a source identifier", resourcePrefix))
		}
		if _, duplicate := resourceKinds[resource.Kind]; duplicate {
			errs = append(errs, fmt.Errorf("%s.kind duplicates %q", resourcePrefix, resource.Kind))
		}
		resourceKinds[resource.Kind] = struct{}{}
		parsed, err := url.Parse(resource.URL)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" || parsed.Port() != "" || parsed.RawQuery != "" || !containsString(allowedHosts, parsed.Host) || !hasPathPrefix(parsed.EscapedPath(), pathPrefixes) {
			errs = append(errs, fmt.Errorf("%s.url must be an admitted official HTTPS artifact", resourcePrefix))
		}
		if !validSHA256(resource.SHA256) {
			errs = append(errs, fmt.Errorf("%s.sha256 must be a lowercase SHA-256", resourcePrefix))
		}
		if resource.ContentType != "application/zip" && resource.ContentType != "text/html; charset=utf-8" {
			errs = append(errs, fmt.Errorf("%s.content_type must be application/zip or text/html; charset=utf-8", resourcePrefix))
		}
	}
	nextRevision := make(map[string]int)
	for index, action := range provider.Actions {
		actionPrefix := fmt.Sprintf("%s.actions[%d]", prefix, index)
		if _, exists := securityIDs[action.SecurityID]; !exists {
			errs = append(errs, fmt.Errorf("%s.security_id must name a configured security", actionPrefix))
		}
		if strings.TrimSpace(action.SourceEventID) == "" || strings.TrimSpace(action.SourceEventID) != action.SourceEventID {
			errs = append(errs, fmt.Errorf("%s.source_event_id is required and canonical", actionPrefix))
		}
		if action.Revision != nextRevision[action.SourceEventID] {
			errs = append(errs, fmt.Errorf("%s.revision must be the next zero-based event revision", actionPrefix))
		}
		nextRevision[action.SourceEventID]++
		if action.ActionStatus != "active" && action.ActionStatus != "cancelled" && action.ActionStatus != "unsupported" {
			errs = append(errs, fmt.Errorf("%s.action_status is unsupported", actionPrefix))
		}
		if !containsString([]string{"split", "reverse_split", "cash_dividend", "stock_dividend", "spinoff", "merger", "acquisition", "delisting", "ticker_change", "exchange_change", "rights_issue", "other"}, action.ActionType) {
			errs = append(errs, fmt.Errorf("%s.action_type is unsupported", actionPrefix))
		}
		observed, observedErr := validateConfiguredActionTime(action.ObservedAt, action.ObservedPrecision)
		published, publishedErr := validateConfiguredActionTime(action.PublishedAt, action.PublishedPrecision)
		effective, effectiveErr := validateConfiguredActionTime(action.EffectiveAt, action.EffectivePrecision)
		if observedErr != nil {
			errs = append(errs, fmt.Errorf("%s observed time: %w", actionPrefix, observedErr))
		}
		if publishedErr != nil {
			errs = append(errs, fmt.Errorf("%s published time: %w", actionPrefix, publishedErr))
		}
		if effectiveErr != nil {
			errs = append(errs, fmt.Errorf("%s effective time: %w", actionPrefix, effectiveErr))
		}
		_ = observed
		_ = effective
		if provider.AvailabilityPolicy == "source_publication" {
			available, availableErr := canonicalUTCTimestamp(action.AvailableAt)
			if availableErr != nil {
				errs = append(errs, fmt.Errorf("%s.available_at must be canonical UTC", actionPrefix))
			} else if publishedErr == nil && available.Before(published) {
				errs = append(errs, fmt.Errorf("%s.available_at must not precede published_at", actionPrefix))
			}
		} else if action.AvailableAt != "" {
			errs = append(errs, fmt.Errorf("%s.available_at must be empty for installation receipt", actionPrefix))
		}
		for field, value := range map[string]string{"record_date": action.RecordDate, "payment_date": action.PaymentDate} {
			if value != "" {
				if _, err := optionalISODate(value); err != nil {
					errs = append(errs, fmt.Errorf("%s.%s must be an ISO date", actionPrefix, field))
				}
			}
		}
		for field, value := range map[string]string{"ratio_numerator": action.RatioNumerator, "ratio_denominator": action.RatioDenominator, "cash_amount": action.CashAmount} {
			if value != "" && !validNonNegativeDecimal(value) {
				errs = append(errs, fmt.Errorf("%s.%s must be a canonical nonnegative decimal", actionPrefix, field))
			}
		}
		if action.Currency != "" && (len(action.Currency) != 3 || action.Currency != strings.ToUpper(action.Currency)) {
			errs = append(errs, fmt.Errorf("%s.currency must be uppercase ISO-like", actionPrefix))
		}
		if action.TargetSecurityID != "" {
			if _, err := uuid.Parse(action.TargetSecurityID); err != nil {
				errs = append(errs, fmt.Errorf("%s.target_security_id must be a UUID", actionPrefix))
			}
		}
		if _, exists := resourceKinds[action.ResourceKind]; !exists {
			errs = append(errs, fmt.Errorf("%s.resource_kind must name a configured resource", actionPrefix))
		}
		if strings.TrimSpace(action.SourceLocator) == "" || strings.TrimSpace(action.SourceLocator) != action.SourceLocator {
			errs = append(errs, fmt.Errorf("%s.source_locator is required and canonical", actionPrefix))
		}
		if action.ActionStatus == "active" && (action.ActionType == "split" || action.ActionType == "reverse_split") && (!validPositiveDecimal(action.RatioNumerator) || !validPositiveDecimal(action.RatioDenominator) || action.CashAmount != "" || action.Currency != "") {
			errs = append(errs, fmt.Errorf("%s active split requires positive ratios and no cash fields", actionPrefix))
		}
		if action.ActionStatus == "active" && action.ActionType == "cash_dividend" && (action.CashAmount == "" || action.Currency == "" || action.RatioNumerator != "" || action.RatioDenominator != "") {
			errs = append(errs, fmt.Errorf("%s active cash dividend requires cash fields and no ratio", actionPrefix))
		}
	}
	return errs
}

func validateConfiguredActionTime(value, precision string) (time.Time, error) {
	parsed, err := canonicalUTCTimestamp(value)
	if err != nil {
		return time.Time{}, err
	}
	if precision != "date" && precision != "second" && precision != "unknown" {
		return time.Time{}, errors.New("precision must be date, second, or unknown")
	}
	if precision == "date" && (parsed.Hour() != 0 || parsed.Minute() != 0 || parsed.Second() != 0) {
		return time.Time{}, errors.New("date precision requires UTC midnight")
	}
	return parsed, nil
}

func validNonNegativeDecimal(value string) bool {
	if value == "" {
		return false
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" || (len(parts[0]) > 1 && parts[0][0] == '0') || (len(parts) == 2 && parts[1] == "") {
		return false
	}
	for _, part := range parts {
		for _, char := range part {
			if char < '0' || char > '9' {
				return false
			}
		}
	}
	return true
}

func validPositiveDecimal(value string) bool {
	return validNonNegativeDecimal(value) && strings.Trim(value, "0.") != ""
}

func canonicalUTCTimestamp(value string) (time.Time, error) {
	if !strings.HasSuffix(value, "Z") {
		return time.Time{}, errors.New("timestamp must end in Z")
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil || parsed.UTC().Format(time.RFC3339) != value {
		return time.Time{}, errors.New("timestamp must be canonical UTC RFC 3339")
	}
	return parsed.UTC(), nil
}

func validLocalClock(value string) bool {
	if len(value) != 5 || value[2] != ':' {
		return false
	}
	_, err := time.Parse("15:04", value)
	return err == nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func admittedHistoricalCalendarURL(parsed *url.URL, allowedHosts, pathPrefixes []string) bool {
	if parsed == nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" || parsed.Port() != "" || !containsString(allowedHosts, parsed.Host) || !hasPathPrefix(parsed.EscapedPath(), pathPrefixes) {
		return false
	}
	if parsed.Path != "/TraderNews.aspx" {
		return parsed.RawQuery == ""
	}
	query := parsed.Query()
	return len(query) == 1 && len(query["id"]) == 1 && strings.TrimSpace(query.Get("id")) != "" && parsed.RawQuery == query.Encode()
}

func hasPathPrefix(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func validBCBCode(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || value == "0" || (len(value) > 1 && value[0] == '0') {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validB3Ticker(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func validISIN(value string) bool {
	if len(value) != 12 || value != strings.ToUpper(value) {
		return false
	}
	for i, r := range value {
		if i < 2 && (r < 'A' || r > 'Z') {
			return false
		}
		if i >= 2 && i < 11 && !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
		if i == 11 && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func validCVMCode(value string) bool {
	return validSourceIdentifier(value)
}

func validSourceIdentifier(value string) bool {
	if strings.TrimSpace(value) != value || value == "" {
		return false
	}
	for _, r := range value {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func validFREDAPIKey(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func requiredISODate(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, errors.New("date is required")
	}
	return optionalISODate(value)
}

func validEconomicFrequency(value string) bool {
	switch value {
	case "daily", "weekly", "monthly", "quarterly", "semiannual", "annual", "irregular":
		return true
	default:
		return false
	}
}

func optionalISODate(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if strings.TrimSpace(value) != value {
		return time.Time{}, errors.New("date has surrounding whitespace")
	}
	return time.Parse("2006-01-02", value)
}

func upperAlpha(v string, n int) bool {
	if len(v) != n {
		return false
	}
	for _, r := range v {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}
func upperAlnum(v string, n int) bool {
	if len(v) != n {
		return false
	}
	for _, r := range v {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func validType(v string) bool {
	if len(v) < 2 || len(v) > 64 {
		return false
	}
	for i, r := range v {
		if !(r >= 'a' && r <= 'z' || i > 0 && (r >= '0' && r <= '9' || r == '_')) {
			return false
		}
	}
	return true
}
