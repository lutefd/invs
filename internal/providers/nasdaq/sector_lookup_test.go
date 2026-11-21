package nasdaq

import "testing"

func TestLookupCurrentSectorRequiresTickerAndCompanyMatch(t *testing.T) {
	tests := []struct {
		name       string
		ticker     string
		company    string
		wantSector string
		wantMatch  bool
	}{
		{name: "class suffix and punctuation normalize", ticker: "ABNB", company: "Airbnb Inc Class A", wantSector: "consumer_discretionary", wantMatch: true},
		{name: "ordinary legal suffix normalizes", ticker: "AAPL", company: "Apple, Inc.", wantSector: "technology", wantMatch: true},
		{name: "telecom mapping", ticker: "TMUS", company: "T-Mobile US, Inc.", wantSector: "communication_services", wantMatch: true},
		{name: "ticker must exist", ticker: "NOPE", company: "Apple Inc", wantMatch: false},
		{name: "company must match ticker", ticker: "AAPL", company: "Acme Holdings", wantMatch: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry, matched, err := LookupCurrentSector(tt.ticker, tt.company)
			if err != nil {
				t.Fatal(err)
			}
			if matched != tt.wantMatch {
				t.Fatalf("matched=%t, want %t", matched, tt.wantMatch)
			}
			if matched && entry.Sector != tt.wantSector {
				t.Fatalf("sector=%q, want %q", entry.Sector, tt.wantSector)
			}
		})
	}
}

func TestNasdaq100SectorLookupContainsAllCurrentRows(t *testing.T) {
	sectorLookupOnce.Do(loadSectorLookup)
	if sectorLookupErr != nil {
		t.Fatal(sectorLookupErr)
	}
	if len(sectorLookup) != 101 {
		t.Fatalf("lookup contains %d securities, want 101", len(sectorLookup))
	}
	for ticker, entry := range sectorLookup {
		if ticker != entry.Ticker || entry.CompanyName == "" || entry.ICBIndustry == "" || !validLookupSector(entry.Sector) {
			t.Errorf("invalid lookup entry for %s: %+v", ticker, entry)
		}
	}
}
