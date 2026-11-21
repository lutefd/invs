package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luisdourado/invs/config"
	"gopkg.in/yaml.v3"
)

const testCollectorConfig = `user_agent: "invs/1 test@example.com"
http:
  timeout: 30s
  requests_per_second: 5
  burst: 1
  max_attempts: 4
  initial_backoff: 500ms
providers:
  sec:
    enabled: false
  prices:
    enabled: true
    start: 2020-01-01
  fred:
    enabled: false
universe:
  - issuer_id: 1b3d88f5-7d4b-45bb-b827-05f8410e71aa
    security_id: 469fc20f-7d4b-45bb-b827-05f8410e71aa
    legal_name: Apple Inc.
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

const testDiscoveryProfile = `schema_version: 1.0.0
universe_id: 51efed8d-2106-5ac7-b12a-a66cfe0eb097
name: nasdaq-100-starter
membership_as_of: 2026-09-06
members:
  - ticker: AAPL
    security_id: 469fc20f-7d4b-45bb-b827-05f8410e71aa
    issuer_id: 1b3d88f5-7d4b-45bb-b827-05f8410e71aa
    sector: technology
    themes: [consumer_technology]
`

func fixtureManager(t *testing.T) *manager {
	t.Helper()
	root := t.TempDir()
	configPath := filepath.Join(root, "config.local.yaml")
	profilePath := filepath.Join(root, "profile.yaml")
	if err := os.WriteFile(configPath, []byte(testCollectorConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profilePath, []byte(testDiscoveryProfile), 0o600); err != nil {
		t.Fatal(err)
	}
	return &manager{
		configPath:  configPath,
		profilePath: profilePath,
		backupDir:   filepath.Join(root, "backups"),
		grafanaPort: "3000",
		timezone:    "America/Sao_Paulo",
		now:         func() time.Time { return time.Date(2026, 9, 23, 15, 0, 0, 0, time.UTC) },
	}
}

func TestAddNewStockUpdatesBothConfigLayersAndBacksThemUp(t *testing.T) {
	m := fixtureManager(t)
	result, status, err := m.add(addStockRequest{
		Ticker: "msft", LegalName: "Microsoft Corporation", YahooSymbol: "msft",
		IdentifierValidFrom: "1986-03-13", CIK: "789019", Sector: "technology",
		Themes: []string{"cloud", "AI", "cloud"}, MembershipAsOf: "2026-09-23",
	})
	if err != nil || status != 201 {
		t.Fatalf("add stock = result %v status %d error %v", result, status, err)
	}
	if result["ticker"] != "MSFT" || result["collector_config_updated"] != true {
		t.Fatalf("unexpected add result: %#v", result)
	}

	configBytes, err := os.ReadFile(m.configPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := yaml.Unmarshal(configBytes, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Universe) != 2 || cfg.Universe[1].Ticker != "MSFT" || cfg.Universe[1].IdentifierValidFrom != "1986-03-13" || cfg.Universe[1].YahooSymbol != "MSFT" {
		t.Fatalf("collector config security = %+v", cfg.Universe)
	}
	profileBytes, err := os.ReadFile(m.profilePath)
	if err != nil {
		t.Fatal(err)
	}
	var profile universeProfile
	if err := yaml.Unmarshal(profileBytes, &profile); err != nil {
		t.Fatal(err)
	}
	if len(profile.Members) != 2 || profile.MembershipAsOf != "2026-09-23" {
		t.Fatalf("profile = %+v", profile)
	}
	member := profile.Members[1]
	if member.Ticker != "MSFT" || member.SecurityID != cfg.Universe[1].SecurityID || member.IssuerID != cfg.Universe[1].IssuerID {
		t.Fatalf("profile member does not match collector security: member=%+v security=%+v", member, cfg.Universe[1])
	}
	if len(member.Themes) != 2 || member.Themes[0] != "cloud" || member.Themes[1] != "ai" {
		t.Fatalf("themes were not normalized: %v", member.Themes)
	}
	backups, err := filepath.Glob(filepath.Join(m.backupDir, "*.bak"))
	if err != nil || len(backups) != 2 {
		t.Fatalf("backups=%v err=%v", backups, err)
	}
}

func TestAddExistingCollectorMappingReusesStableIDs(t *testing.T) {
	m := fixtureManager(t)
	configBytes, err := os.ReadFile(m.configPath)
	if err != nil {
		t.Fatal(err)
	}
	configBytes = append(configBytes, []byte(`  - issuer_id: 5f7f9bec-890c-5e0d-8760-98163e63f4e5
    security_id: b40636a9-cc8e-5e0d-8760-98163e63f4e5
    legal_name: Microsoft Corporation
    country_code: US
    security_type: common_stock
    primary_listing: true
    cik: 789019
    ticker: MSFT
    identifier_valid_from: 1986-03-13
    yahoo_symbol: MSFT
    exchange: NASDAQ
    mic: XNAS
    currency: USD
`)...)
	if err := os.WriteFile(m.configPath, configBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	result, status, err := m.add(addStockRequest{
		Ticker: "MSFT", Sector: "technology", MembershipAsOf: "2026-09-23",
	})
	if err != nil || status != 201 {
		t.Fatalf("add existing mapping = result %v status %d error %v", result, status, err)
	}
	if result["collector_config_updated"] != false || result["security_id"] != "b40636a9-cc8e-5e0d-8760-98163e63f4e5" {
		t.Fatalf("existing security mapping was not reused: %#v", result)
	}
	profileBytes, err := os.ReadFile(m.profilePath)
	if err != nil {
		t.Fatal(err)
	}
	var profile universeProfile
	if err := yaml.Unmarshal(profileBytes, &profile); err != nil {
		t.Fatal(err)
	}
	if len(profile.Members) != 2 || profile.Members[1].SecurityID != "b40636a9-cc8e-5e0d-8760-98163e63f4e5" {
		t.Fatalf("unexpected profile mapping: %+v", profile.Members)
	}
}

func TestAddRejectsBackdatedOrFutureMembershipDate(t *testing.T) {
	for _, date := range []string{"2026-09-05", "2026-09-24", "not-a-date"} {
		t.Run(date, func(t *testing.T) {
			m := fixtureManager(t)
			_, status, err := m.add(addStockRequest{
				Ticker: "MSFT", LegalName: "Microsoft Corporation", YahooSymbol: "MSFT",
				IdentifierValidFrom: "1986-03-13", Sector: "technology", MembershipAsOf: date,
			})
			if err == nil || status != 400 {
				t.Fatalf("membership date %q accepted: status=%d err=%v", date, status, err)
			}
		})
	}
}

func TestAddRejectsExistingSecurityWithFutureIdentifierDate(t *testing.T) {
	m := fixtureManager(t)
	configBytes := testCollectorConfig + `  - issuer_id: 5f7f9bec-890c-5e0d-8760-98163e63f4e5
    security_id: b40636a9-cc8e-5e0d-8760-98163e63f4e5
    legal_name: Microsoft Corporation
    country_code: US
    security_type: common_stock
    primary_listing: true
    cik: 789019
    ticker: MSFT
    identifier_valid_from: 2030-01-01
    yahoo_symbol: MSFT
    exchange: NASDAQ
    mic: XNAS
    currency: USD
`
	if err := os.WriteFile(m.configPath, []byte(configBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	_, status, err := m.add(addStockRequest{
		Ticker: "MSFT", Sector: "technology", MembershipAsOf: "2026-09-23",
	})
	if err == nil || status != 409 {
		t.Fatalf("future identifier date accepted: status=%d err=%v", status, err)
	}
}

func TestLoadRejectsUnknownCollectorConfigFields(t *testing.T) {
	m := fixtureManager(t)
	configBytes, err := os.ReadFile(m.configPath)
	if err != nil {
		t.Fatal(err)
	}
	configBytes = append(configBytes, []byte("\nunknown_stock_manager_test_field: true\n")...)
	if err := os.WriteFile(m.configPath, configBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := m.load(); err == nil {
		t.Fatal("collector config with an unknown field was accepted")
	}
}
