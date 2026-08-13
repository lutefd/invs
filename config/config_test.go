package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func validConfig() Config {
	return Config{UserAgent: "invs/1 user@real.test", HTTP: HTTP{Timeout: 1, RequestsPerSecond: 1}, Providers: Providers{Prices: PriceProvider{Enabled: true, Start: "2020-01-01"}, FRED: FREDProvider{Enabled: true, Series: []string{"DGS10"}}}, Universe: []Security{{IssuerID: "1b3d88f5-55b8-4dc5-a6be-2f77e9e99201", SecurityID: "469fc20f-7d4b-45bb-b827-05f8410e71aa", LegalName: "Apple", CountryCode: "US", SecurityType: "common_stock", CIK: 320193, Ticker: "AAPL", IdentifierValidFrom: "1980-12-12", YahooSymbol: "AAPL", MIC: "XNAS", Currency: "USD"}}}
}
func TestValidateStrictCanonicalIdentity(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatal(err)
	}
	c := validConfig()
	c.Universe[0].SecurityID = "ticker"
	if err := c.Validate(); err == nil {
		t.Fatal("non UUID accepted")
	}
}
func TestValidateProviderRequirements(t *testing.T) {
	c := validConfig()
	c.Providers.SEC.Enabled = true
	c.UserAgent = "invs contact@example.com"
	if err := c.Validate(); err == nil {
		t.Fatal("placeholder SEC contact accepted")
	}
	c = validConfig()
	c.Providers.FRED.Series = nil
	if err := c.Validate(); err == nil {
		t.Fatal("empty FRED accepted")
	}
	c = validConfig()
	c.Universe[0].YahooSymbol = ""
	if err := c.Validate(); err == nil {
		t.Fatal("missing symbol accepted")
	}
}

func TestValidateBCBProviderRequirements(t *testing.T) {
	c := validConfig()
	c.Providers.BCB = BCBProvider{Enabled: true, Series: []BCBSeries{{
		Code: "432", Geography: "BR", Unit: "percent", Frequency: "daily",
		SeasonalAdjustment: "not_adjusted", Start: "2024-01-01", End: "2024-01-31",
	}}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*BCBSeries){
		"missing code":      func(s *BCBSeries) { s.Code = "" },
		"noncanonical code": func(s *BCBSeries) { s.Code = "0432" },
		"missing geography": func(s *BCBSeries) { s.Geography = "" },
		"missing unit":      func(s *BCBSeries) { s.Unit = "" },
		"invalid frequency": func(s *BCBSeries) { s.Frequency = "business_daily" },
		"invalid start":     func(s *BCBSeries) { s.Start = "01/01/2024" },
		"reversed dates":    func(s *BCBSeries) { s.Start, s.End = "2024-02-01", "2024-01-01" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := c
			candidate.Providers.BCB.Series = []BCBSeries{c.Providers.BCB.Series[0]}
			mutate(&candidate.Providers.BCB.Series[0])
			if err := candidate.Validate(); err == nil {
				t.Fatal("invalid BCB configuration accepted")
			}
		})
	}

	duplicate := c
	duplicate.Providers.BCB.Series = append(duplicate.Providers.BCB.Series, duplicate.Providers.BCB.Series[0])
	if err := duplicate.Validate(); err == nil {
		t.Fatal("duplicate BCB series accepted")
	}
}

func TestValidateALFREDProviderRequirements(t *testing.T) {
	c := validConfig()
	c.FREDAPIKey = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	c.Providers.ALFRED = ALFREDProvider{Enabled: true, Series: []ALFREDSeries{{
		ID: "CPIAUCSL", Geography: "US", Unit: "index", Frequency: "monthly",
		SeasonalAdjustment: "seasonally_adjusted",
		RealtimeEnd:        "2026-08-11",
		ObservationStart:   "2018-01-01", ObservationEnd: "2026-07-01",
	}}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*ALFREDSeries){
		"missing id":                  func(s *ALFREDSeries) { s.ID = "" },
		"unsafe id":                   func(s *ALFREDSeries) { s.ID = "CPI/AUCSL" },
		"missing geography":           func(s *ALFREDSeries) { s.Geography = "" },
		"missing unit":                func(s *ALFREDSeries) { s.Unit = "" },
		"invalid frequency":           func(s *ALFREDSeries) { s.Frequency = "business_daily" },
		"missing realtime end":        func(s *ALFREDSeries) { s.RealtimeEnd = "" },
		"invalid realtime end":        func(s *ALFREDSeries) { s.RealtimeEnd = "12/08/2026" },
		"realtime end before minimum": func(s *ALFREDSeries) { s.RealtimeEnd = "1776-07-03" },
		"invalid observation start":   func(s *ALFREDSeries) { s.ObservationStart = "2018" },
		"reversed observation range":  func(s *ALFREDSeries) { s.ObservationStart, s.ObservationEnd = "2026-07-01", "2018-01-01" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := c
			candidate.Providers.ALFRED.Series = []ALFREDSeries{c.Providers.ALFRED.Series[0]}
			mutate(&candidate.Providers.ALFRED.Series[0])
			if err := candidate.Validate(); err == nil {
				t.Fatal("invalid ALFRED configuration accepted")
			}
		})
	}

	duplicate := c
	duplicate.Providers.ALFRED.Series = append(duplicate.Providers.ALFRED.Series, duplicate.Providers.ALFRED.Series[0])
	if err := duplicate.Validate(); err == nil {
		t.Fatal("duplicate ALFRED series accepted")
	}
}

func TestALFREDProviderCountsAsEnabledProvider(t *testing.T) {
	c := validConfig()
	c.FREDAPIKey = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	c.Providers.Prices.Enabled = false
	c.Providers.FRED.Enabled = false
	c.Providers.ALFRED = ALFREDProvider{Enabled: true, Series: []ALFREDSeries{{
		ID: "CPIAUCSL", Geography: "US", Unit: "index", Frequency: "monthly",
		RealtimeEnd: "2026-08-11",
	}}}
	if err := c.Validate(); err != nil {
		t.Fatalf("ALFRED-only provider configuration rejected: %v", err)
	}
}

func TestValidateALFREDRequiresEnvironmentOnlyAPIKey(t *testing.T) {
	c := validConfig()
	c.Providers.ALFRED = ALFREDProvider{Enabled: true, Series: []ALFREDSeries{{
		ID: "CPIAUCSL", Geography: "US", Unit: "index", Frequency: "monthly", RealtimeEnd: "2026-08-11",
	}}}
	for _, key := range []string{"", "short", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa!"} {
		c.FREDAPIKey = key
		if err := c.Validate(); err == nil {
			t.Fatalf("invalid key %q accepted", key)
		}
	}
}

func TestValidateCVMProviderRequirements(t *testing.T) {
	c := validConfig()
	c.Providers.CVM = CVMProvider{
		Enabled: true,
		CAD:     true,
		IPE:     CVMIPEConfig{Years: []int{2025, time.Now().UTC().Year()}},
	}
	c.Universe[0].CVMCode = "1023"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*Config){
		"no requested resource": func(candidate *Config) {
			candidate.Providers.CVM.CAD = false
			candidate.Providers.CVM.IPE.Years = nil
		},
		"year before source history": func(candidate *Config) {
			candidate.Providers.CVM.IPE.Years = []int{2002}
		},
		"future year": func(candidate *Config) {
			candidate.Providers.CVM.IPE.Years = []int{time.Now().UTC().Year() + 1}
		},
		"duplicate year": func(candidate *Config) {
			candidate.Providers.CVM.IPE.Years = []int{2025, 2025}
		},
		"invalid code": func(candidate *Config) {
			candidate.Universe[0].CVMCode = "10/23"
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := c
			candidate.Providers.CVM.IPE.Years = append([]int(nil), c.Providers.CVM.IPE.Years...)
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("invalid CVM configuration accepted")
			}
		})
	}
}

func TestCVMProviderCountsAsEnabledProvider(t *testing.T) {
	c := validConfig()
	c.Providers.Prices.Enabled = false
	c.Providers.FRED.Enabled = false
	c.Providers.CVM = CVMProvider{Enabled: true, CAD: true}
	if err := c.Validate(); err != nil {
		t.Fatalf("CVM-only provider configuration rejected: %v", err)
	}
}

func TestLoadRejectsCVMDocumentURLConfiguration(t *testing.T) {
	config := `
user_agent: "invs/1 user@real.test"
http:
  timeout: 1s
  requests_per_second: 1
providers:
  prices:
    enabled: true
    start: 2020-01-01
  fred:
    enabled: true
    series: [DGS10]
  cvm:
    enabled: false
    document_urls: [https://example.test/document.zip]
universe:
  - issuer_id: 1b3d88f5-55b8-4dc5-a6be-2f77e9e99201
    security_id: 469fc20f-7d4b-45bb-b827-05f8410e71aa
    legal_name: Apple
    country_code: US
    security_type: common_stock
    primary_listing: true
    cik: 320193
    ticker: AAPL
    identifier_valid_from: 1980-12-12
    yahoo_symbol: AAPL
    exchange: NASDAQ
    mic: XNAS
    currency: USD
`
	path := t.TempDir() + "/config.yaml"
	if err := os.WriteFile(path, []byte(strings.TrimSpace(config)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "document_urls") {
		t.Fatalf("Load error = %v, want unknown document_urls field", err)
	}
}

func TestIdentifierValidityRequiredWithPricesDisabled(t *testing.T) {
	c := validConfig()
	c.Providers.Prices.Enabled = false
	c.Universe[0].IdentifierValidFrom = ""
	if err := c.Validate(); err == nil {
		t.Fatal("missing identifier_valid_from accepted")
	}
}
func TestAtLeastOneProviderRequired(t *testing.T) {
	c := validConfig()
	c.Providers.Prices.Enabled = false
	c.Providers.FRED.Enabled = false
	if err := c.Validate(); err == nil {
		t.Fatal("providerless config accepted")
	}
}

func TestDatabaseConstraintParity(t *testing.T) {
	c := validConfig()
	c.Universe[0].SecurityType = "x"
	if err := c.Validate(); err == nil {
		t.Fatal("one-character security_type accepted")
	}
	c = validConfig()
	c.Universe[0].SecurityType = "a" + string(make([]byte, 64))
	if err := c.Validate(); err == nil {
		t.Fatal("overlong security_type accepted")
	}
	c = validConfig()
	c.Universe[0].CIK = 10000000000
	if err := c.Validate(); err == nil {
		t.Fatal("11-digit CIK accepted")
	}
	c = validConfig()
	c.Universe[0].CIK = 9999999999
	if err := c.Validate(); err != nil {
		t.Fatalf("10-digit CIK rejected: %v", err)
	}
}
