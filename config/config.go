package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DataDir   string     `yaml:"data_dir"`
	UserAgent string     `yaml:"user_agent"`
	HTTP      HTTP       `yaml:"http"`
	Providers Providers  `yaml:"providers"`
	Universe  []Security `yaml:"universe"`
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
type Security struct {
	IssuerID    string `yaml:"issuer_id"`
	SecurityID  string `yaml:"security_id"`
	LegalName   string `yaml:"legal_name"`
	CIK         int64  `yaml:"cik"`
	Ticker      string `yaml:"ticker"`
	YahooSymbol string `yaml:"yahoo_symbol"`
	Exchange    string `yaml:"exchange"`
	MIC         string `yaml:"mic"`
	Currency    string `yaml:"currency"`
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
	if strings.TrimSpace(c.UserAgent) == "" {
		errs = append(errs, errors.New("user_agent is required (SEC requires contact information)"))
	}
	if c.HTTP.Timeout <= 0 {
		errs = append(errs, errors.New("http.timeout must be positive"))
	}
	if c.HTTP.RequestsPerSecond <= 0 {
		errs = append(errs, errors.New("http.requests_per_second must be positive"))
	}
	seenIssuer, seenSecurity := map[string]bool{}, map[string]bool{}
	for i, s := range c.Universe {
		pfx := fmt.Sprintf("universe[%d]", i)
		if s.IssuerID == "" || s.SecurityID == "" {
			errs = append(errs, fmt.Errorf("%s: issuer_id and security_id are required", pfx))
		}
		if !safeID(s.IssuerID) || !safeID(s.SecurityID) {
			errs = append(errs, fmt.Errorf("%s: IDs may contain only letters, digits, dot, underscore and hyphen", pfx))
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
		}
		if s.Currency == "" {
			errs = append(errs, fmt.Errorf("%s: currency is required", pfx))
		}
	}
	return errors.Join(errs...)
}

func safeID(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}
