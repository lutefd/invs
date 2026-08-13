package config

import (
	"errors"
	"fmt"
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
	SEC    EnabledProvider `yaml:"sec"`
	Prices PriceProvider   `yaml:"prices"`
	FRED   FREDProvider    `yaml:"fred"`
	ALFRED ALFREDProvider  `yaml:"alfred"`
	BCB    BCBProvider     `yaml:"bcb"`
	CVM    CVMProvider     `yaml:"cvm"`
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
	if !c.Providers.SEC.Enabled && !c.Providers.Prices.Enabled && !c.Providers.FRED.Enabled && !c.Providers.ALFRED.Enabled && !c.Providers.BCB.Enabled && !c.Providers.CVM.Enabled {
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
		if s.CIK <= 0 {
			errs = append(errs, fmt.Errorf("%s: cik must be positive", pfx))
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
			} else if start, parseErr := time.Parse("2006-01-02", c.Providers.Prices.Start); parseErr == nil && validFrom.After(start) {
				errs = append(errs, fmt.Errorf("%s: identifier_valid_from must not follow prices.start", pfx))
			}
		}
	}
	return errors.Join(errs...)
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
