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

func TestValidatePTAXProviderRequirements(t *testing.T) {
	c := validConfig()
	c.Providers.PTAX = PTAXProvider{Enabled: true, Start: "2026-01-01", End: "2026-12-31"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*PTAXProvider){
		"missing start":  func(provider *PTAXProvider) { provider.Start = "" },
		"invalid end":    func(provider *PTAXProvider) { provider.End = "31/12/2026" },
		"before history": func(provider *PTAXProvider) { provider.Start = "1984-11-27" },
		"reversed":       func(provider *PTAXProvider) { provider.Start, provider.End = "2026-02-01", "2026-01-01" },
		"too broad":      func(provider *PTAXProvider) { provider.Start, provider.End = "2025-01-01", "2026-01-02" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := c
			mutate(&candidate.Providers.PTAX)
			if err := candidate.Validate(); err == nil {
				t.Fatal("invalid PTAX configuration accepted")
			}
		})
	}
}

func TestValidateB3ProviderRequiresExplicitUniverseMapping(t *testing.T) {
	c := validConfig()
	c.Universe[0].CountryCode = "BR"
	c.Universe[0].Ticker = "PETR4"
	c.Universe[0].ISIN = "BRPETRACNPR6"
	c.Universe[0].Exchange = "B3"
	c.Universe[0].MIC = "BVMF"
	c.Universe[0].Currency = "BRL"
	c.Providers.B3 = B3Provider{
		Enabled:    true,
		ReportDate: time.Now().UTC().Add(-24 * time.Hour).Format("2006-01-02"),
		Tickers:    []string{"PETR4"},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*Config){
		"missing report date": func(candidate *Config) { candidate.Providers.B3.ReportDate = "" },
		"missing ticker":      func(candidate *Config) { candidate.Providers.B3.Tickers = nil },
		"unknown ticker":      func(candidate *Config) { candidate.Providers.B3.Tickers = []string{"VALE3"} },
		"wrong market mapping": func(candidate *Config) {
			candidate.Universe[0].MIC = "XBSP"
		},
		"missing isin": func(candidate *Config) {
			candidate.Universe[0].ISIN = ""
		},
		"duplicate ticker": func(candidate *Config) {
			candidate.Providers.B3.Tickers = []string{"PETR4", "PETR4"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := c
			candidate.Providers.B3.Tickers = append([]string(nil), c.Providers.B3.Tickers...)
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("invalid B3 configuration accepted")
			}
		})
	}
}

func TestValidateIndexMembershipProvidersRequireAdmittedNoticesAndScopedMappings(t *testing.T) {
	nasdaq := validConfig()
	nasdaq.Providers.Prices.Enabled = false
	nasdaq.Providers.FRED.Enabled = false
	nasdaq.Universe[0].Exchange = "NASDAQ"
	nasdaq.Universe[0].PrimaryListing = true
	nasdaq.Providers.NasdaqMembership = IndexMembershipProvider{
		Enabled: true, UniverseID: "nasdaq_100", Tickers: []string{"AAPL"},
		Notices: []string{"https://www.globenewswire.com/news-release/2025/12/13/example.html"},
	}
	if err := nasdaq.Validate(); err != nil {
		t.Fatalf("valid Nasdaq membership provider: %v", err)
	}

	b3 := validConfig()
	b3.Providers.Prices.Enabled = false
	b3.Providers.FRED.Enabled = false
	b3.Universe[0].CountryCode = "BR"
	b3.Universe[0].Ticker = "PETZ3"
	b3.Universe[0].Exchange = "B3"
	b3.Universe[0].MIC = "BVMF"
	b3.Universe[0].Currency = "BRL"
	b3.Universe[0].PrimaryListing = true
	b3.Providers.B3Membership = IndexMembershipProvider{
		Enabled: true, UniverseID: "ibovespa", Tickers: []string{"PETZ3"},
		Notices: []string{"https://www.b3.com.br/pt_br/noticias/example.htm"},
	}
	if err := b3.Validate(); err != nil {
		t.Fatalf("valid B3 membership provider: %v", err)
	}

	cases := map[string]func(*Config){
		"untrusted notice": func(candidate *Config) {
			candidate.Providers.NasdaqMembership.Notices[0] = "https://example.com/release"
		},
		"unknown ticker":   func(candidate *Config) { candidate.Providers.NasdaqMembership.Tickers = []string{"INSM"} },
		"invalid universe": func(candidate *Config) { candidate.Providers.NasdaqMembership.UniverseID = "Nasdaq 100" },
		"duplicate notice": func(candidate *Config) {
			candidate.Providers.NasdaqMembership.Notices = append(candidate.Providers.NasdaqMembership.Notices, candidate.Providers.NasdaqMembership.Notices[0])
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			candidate := nasdaq
			candidate.Providers.NasdaqMembership.Tickers = append([]string(nil), nasdaq.Providers.NasdaqMembership.Tickers...)
			candidate.Providers.NasdaqMembership.Notices = append([]string(nil), nasdaq.Providers.NasdaqMembership.Notices...)
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("invalid membership provider accepted")
			}
		})
	}
}

func TestValidateB3ListingHistoryRequiresMembershipBackedExactMapping(t *testing.T) {
	c := validConfig()
	c.Providers.Prices.Enabled = false
	c.Providers.FRED.Enabled = false
	c.Universe[0].CountryCode = "BR"
	c.Universe[0].Ticker = "PETZ3"
	c.Universe[0].Exchange = "B3"
	c.Universe[0].MIC = "BVMF"
	c.Universe[0].Currency = "BRL"
	c.Universe[0].PrimaryListing = true
	c.Providers.B3Membership = IndexMembershipProvider{
		Enabled: true, UniverseID: "ibovespa", Tickers: []string{"PETZ3"},
		Notices: []string{"https://www.b3.com.br/pt_br/noticias/example.htm"},
	}
	c.Providers.B3ListingHistory = ListingHistoryProvider{
		Enabled: true,
		Notices: []ListingHistoryNotice{{
			URL:         "https://sistemasweb.b3.com.br/PlantaoNoticias/Noticias/Detail?agencia=18&dataNoticia=2026-01-02+19%3A43%3A10&idNoticia=3192104",
			TradingName: "PETZ", Ticker: "PETZ3", ValidFrom: "2021-09-06T03:00:00Z",
		}},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("valid B3 listing history: %v", err)
	}

	for name, mutate := range map[string]func(*Config){
		"membership disabled": func(candidate *Config) { candidate.Providers.B3Membership.Enabled = false },
		"untrusted URL": func(candidate *Config) {
			candidate.Providers.B3ListingHistory.Notices[0].URL = "https://example.com/notice"
		},
		"unbacked ticker": func(candidate *Config) {
			candidate.Providers.B3ListingHistory.Notices[0].Ticker = "VALE3"
		},
		"noncanonical valid from": func(candidate *Config) {
			candidate.Providers.B3ListingHistory.Notices[0].ValidFrom = "2021-09-06"
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := c
			candidate.Providers.B3Membership.Tickers = append([]string(nil), c.Providers.B3Membership.Tickers...)
			candidate.Providers.B3ListingHistory.Notices = append([]ListingHistoryNotice(nil), c.Providers.B3ListingHistory.Notices...)
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("invalid B3 listing-history configuration accepted")
			}
		})
	}
}

func TestValidateExchangeCalendarProviders(t *testing.T) {
	c := validConfig()
	c.Providers.NYSE = CalendarProvider{Enabled: true, Year: 2026, CoverageStart: "2026-01-01", CoverageEnd: "2026-12-31"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*CalendarProvider){
		"missing year":  func(provider *CalendarProvider) { provider.Year = 0 },
		"invalid start": func(provider *CalendarProvider) { provider.CoverageStart = "01/01/2026" },
		"reversed coverage": func(provider *CalendarProvider) {
			provider.CoverageStart, provider.CoverageEnd = provider.CoverageEnd, provider.CoverageStart
		},
		"year mismatch": func(provider *CalendarProvider) { provider.CoverageEnd = "2027-01-01" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := c
			mutate(&candidate.Providers.NYSE)
			if err := candidate.Validate(); err == nil {
				t.Fatal("invalid NYSE calendar configuration accepted")
			}
		})
	}

	b3 := validConfig()
	b3.Universe[0].CountryCode = "BR"
	b3.Universe[0].Ticker = "PETR4"
	b3.Universe[0].ISIN = "BRPETRACNPR6"
	b3.Universe[0].Exchange = "B3"
	b3.Universe[0].MIC = "BVMF"
	b3.Universe[0].Currency = "BRL"
	b3.Providers.B3 = B3Provider{
		Enabled: true, ReportDate: time.Now().UTC().Add(-24 * time.Hour).Format(time.DateOnly), Tickers: []string{"PETR4"},
		Calendar: CalendarProvider{Enabled: true, Year: 2026, CoverageStart: "2026-08-24", CoverageEnd: "2026-08-28"},
	}
	if err := b3.Validate(); err != nil {
		t.Fatal(err)
	}
}

func validHistoricalCalendarProvider() HistoricalCalendarProvider {
	return HistoricalCalendarProvider{
		Enabled: true, Year: 2025, CoverageStart: "2025-12-24", CoverageEnd: "2025-12-25",
		RegularOpenLocal: "09:30", RegularCloseLocal: "16:00",
		Versions: []HistoricalCalendarVersion{{
			AvailableAt: "2024-12-13T05:00:00Z", Revision: 0,
			Resources: []HistoricalCalendarResource{{
				Kind: "annual_calendar", URL: "https://www.nasdaqtrader.com/content/technicalsupport/2025tradingcalendar.pdf",
				SHA256: strings.Repeat("a", 64), ContentType: "application/pdf",
			}},
			Events: []HistoricalCalendarEvent{
				{Date: "2025-12-24", Status: "open", CloseLocal: "13:00", ResourceKind: "annual_calendar", SourceLocator: "calendar/date=2025-12-24"},
				{Date: "2025-12-25", Status: "closed", ResourceKind: "annual_calendar", SourceLocator: "calendar/date=2025-12-25"},
			},
		}},
	}
}

func TestValidateHistoricalCalendarArtifacts(t *testing.T) {
	c := validConfig()
	c.Providers.NasdaqCalendarHistory = validHistoricalCalendarProvider()
	if err := c.Validate(); err != nil {
		t.Fatalf("valid historical calendar: %v", err)
	}

	b3 := validConfig()
	b3Provider := validHistoricalCalendarProvider()
	b3Provider.Year = 2026
	b3Provider.CoverageStart, b3Provider.CoverageEnd = "2026-02-16", "2026-02-18"
	b3Provider.Versions[0].Resources[0].URL = "https://www.b3.com.br/data/files/AA/calendar.pdf"
	b3Provider.Versions[0].Events = []HistoricalCalendarEvent{{
		Date: "2026-02-18", Status: "open", OpenLocal: "13:00", CloseLocal: "18:00",
		ResourceKind: "annual_calendar", SourceLocator: "circular/ash-wednesday",
	}}
	b3.Providers.B3CalendarHistory = b3Provider
	if err := b3.Validate(); err != nil {
		t.Fatalf("valid B3 historical calendar: %v", err)
	}

	for name, mutate := range map[string]func(*HistoricalCalendarProvider){
		"untrusted URL": func(provider *HistoricalCalendarProvider) {
			provider.Versions[0].Resources[0].URL = "https://example.com/calendar.pdf"
		},
		"official URL with port": func(provider *HistoricalCalendarProvider) {
			provider.Versions[0].Resources[0].URL = "https://www.nasdaqtrader.com:8443/content/technicalsupport/2025tradingcalendar.pdf"
		},
		"official URL with fragment": func(provider *HistoricalCalendarProvider) {
			provider.Versions[0].Resources[0].URL = "https://www.nasdaqtrader.com/content/technicalsupport/2025tradingcalendar.pdf#page=1"
		},
		"artifact URL with query": func(provider *HistoricalCalendarProvider) {
			provider.Versions[0].Resources[0].URL = "https://www.nasdaqtrader.com/content/technicalsupport/2025tradingcalendar.pdf?draft=true"
		},
		"wrong hash": func(provider *HistoricalCalendarProvider) {
			provider.Versions[0].Resources[0].SHA256 = "ABC"
		},
		"wrong content type": func(provider *HistoricalCalendarProvider) {
			provider.Versions[0].Resources[0].ContentType = "application/octet-stream"
		},
		"noncanonical availability": func(provider *HistoricalCalendarProvider) {
			provider.Versions[0].AvailableAt = "2024-12-13T00:00:00-05:00"
		},
		"future availability": func(provider *HistoricalCalendarProvider) {
			provider.Versions[0].AvailableAt = time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second).Format(time.RFC3339)
		},
		"revision gap": func(provider *HistoricalCalendarProvider) {
			provider.Versions[0].Revision = 1
		},
		"event outside coverage": func(provider *HistoricalCalendarProvider) {
			provider.Versions[0].Events[0].Date = "2025-12-23"
		},
		"unknown event resource": func(provider *HistoricalCalendarProvider) {
			provider.Versions[0].Events[0].ResourceKind = "notice"
		},
		"closed event with hours": func(provider *HistoricalCalendarProvider) {
			provider.Versions[0].Events[1].OpenLocal = "09:30"
		},
		"open event without override": func(provider *HistoricalCalendarProvider) {
			provider.Versions[0].Events[0].CloseLocal = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := validConfig()
			provider := validHistoricalCalendarProvider()
			mutate(&provider)
			candidate.Providers.NasdaqCalendarHistory = provider
			if err := candidate.Validate(); err == nil {
				t.Fatal("invalid historical calendar configuration accepted")
			}
		})
	}
}

func validCorporateActionProvider() CorporateActionProvider {
	return CorporateActionProvider{
		Enabled: true, AvailabilityPolicy: "source_publication",
		Resources: []CorporateActionResource{{
			Kind: "filing", URL: "https://www.sec.gov/Archives/edgar/data/320193/action.html",
			SHA256: strings.Repeat("a", 64), ContentType: "text/html; charset=utf-8",
		}},
		Actions: []CorporateActionVersion{{
			SecurityID:    "469fc20f-7d4b-45bb-b827-05f8410e71aa",
			SourceEventID: "0000320193-20-000060/exhibit-99.1/four-for-one-split",
			Revision:      0, ActionStatus: "active", ActionType: "split",
			ObservedAt: "2020-08-31T00:00:00Z", ObservedPrecision: "date",
			PublishedAt: "2020-07-30T22:55:04Z", PublishedPrecision: "second",
			AvailableAt: "2020-07-30T22:55:04Z",
			EffectiveAt: "2020-08-28T00:00:00Z", EffectivePrecision: "date",
			RatioNumerator: "4", RatioDenominator: "1",
			ResourceKind: "filing", SourceLocator: "exhibit-99.1/four-for-one-split",
		}},
	}
}

func TestValidateCorporateActionArtifacts(t *testing.T) {
	c := validConfig()
	c.Providers.SECActionHistory = validCorporateActionProvider()
	if err := c.Validate(); err != nil {
		t.Fatalf("valid SEC action artifacts: %v", err)
	}

	replay := validConfig()
	provider := validCorporateActionProvider()
	provider.AvailabilityPolicy = "installation_receipt"
	provider.Actions[0].AvailableAt = ""
	provider.Resources[0].URL = "https://www.b3.com.br/data/files/AA/action.zip"
	provider.Resources[0].ContentType = "application/zip"
	replay.Providers.B3ActionReplay = provider
	if err := replay.Validate(); err != nil {
		t.Fatalf("valid B3 action replay: %v", err)
	}

	for name, mutate := range map[string]func(*CorporateActionProvider){
		"untrusted URL": func(provider *CorporateActionProvider) {
			provider.Resources[0].URL = "https://example.test/action.html"
		},
		"bad revision": func(provider *CorporateActionProvider) {
			provider.Actions[0].Revision = 1
		},
		"unknown resource": func(provider *CorporateActionProvider) {
			provider.Actions[0].ResourceKind = "other"
		},
		"missing ratio": func(provider *CorporateActionProvider) {
			provider.Actions[0].RatioNumerator = ""
		},
		"noncanonical availability": func(provider *CorporateActionProvider) {
			provider.Actions[0].AvailableAt = "2020-07-30T18:55:04-04:00"
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := validConfig()
			provider := validCorporateActionProvider()
			mutate(&provider)
			candidate.Providers.SECActionHistory = provider
			if err := candidate.Validate(); err == nil {
				t.Fatal("invalid corporate-action artifact config accepted")
			}
		})
	}
}

func TestValidateHistoricalCalendarAdmitsCanonicalNasdaqNoticeURL(t *testing.T) {
	c := validConfig()
	provider := validHistoricalCalendarProvider()
	provider.Versions[0].Resources = append(provider.Versions[0].Resources, HistoricalCalendarResource{
		Kind: "publication_notice", URL: "https://nasdaqtrader.com/TraderNews.aspx?id=ETA2024-84",
		SHA256: strings.Repeat("b", 64), ContentType: "text/html; charset=utf-8",
	})
	c.Providers.NasdaqCalendarHistory = provider
	if err := c.Validate(); err != nil {
		t.Fatalf("canonical Nasdaq notice URL: %v", err)
	}
}

func TestValidateHistoricalCalendarAdmitsCanonicalNasdaqHoursGuideURL(t *testing.T) {
	c := validConfig()
	provider := validHistoricalCalendarProvider()
	provider.Versions[0].Resources = append(provider.Versions[0].Resources, HistoricalCalendarResource{
		Kind: "session_hours", URL: "https://www.nasdaqtrader.com/content/productsservices/trading/oe_refguide.pdf",
		SHA256: strings.Repeat("b", 64), ContentType: "application/pdf",
	})
	c.Providers.NasdaqCalendarHistory = provider
	if err := c.Validate(); err != nil {
		t.Fatalf("canonical Nasdaq hours guide URL: %v", err)
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
	c.Universe[0].CIK = -1
	if err := c.Validate(); err == nil {
		t.Fatal("negative CIK accepted")
	}
	c = validConfig()
	c.Universe[0].CIK = 0
	if err := c.Validate(); err != nil {
		t.Fatalf("issuer without optional CIK rejected while SEC disabled: %v", err)
	}
	c = validConfig()
	c.Providers.SEC.Enabled = true
	c.Universe[0].CIK = 0
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "at least one universe issuer with a CIK") {
		t.Fatalf("SEC config without an eligible CIK error = %v", err)
	}
	c = validConfig()
	c.Universe[0].CIK = 9999999999
	if err := c.Validate(); err != nil {
		t.Fatalf("10-digit CIK rejected: %v", err)
	}
}
