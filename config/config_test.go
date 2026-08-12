package config

import "testing"

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
