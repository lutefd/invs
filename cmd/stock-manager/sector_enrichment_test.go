package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luisdourado/invs/internal/providers/nasdaq"
)

func TestNasdaqSectorEnrichmentClassifiesMatchesAndPreservesManualRows(t *testing.T) {
	m := fixtureManager(t)
	_, profile, configNode, profileNode, err := m.load()
	if err != nil {
		t.Fatal(err)
	}
	security := newNasdaqSecurity(nasdaq.CurrentConstituent{Ticker: "ABNB", Company: "Airbnb Inc."}, time.Date(2020, 12, 10, 0, 0, 0, 0, time.UTC))
	if _, err := appendSecurityNode(configNode, security); err != nil {
		t.Fatal(err)
	}
	configBytes, err := encodeYAML(configNode)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.configPath, configBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	profile.Members[0].Sector = "unclassified"
	profile.Members[0].Sources = []string{"manual", nasdaqSourceLabel}
	profile.Members = append(profile.Members, profileMember{
		Ticker: "ABNB", SecurityID: security.SecurityID, IssuerID: security.IssuerID,
		Sector: "unclassified", Sources: []string{nasdaqSourceLabel},
	})
	if err := setProfileMembers(profileNode, profile.Members); err != nil {
		t.Fatal(err)
	}
	profileBytes, err := encodeYAML(profileNode)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.profilePath, profileBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := m.enrichNasdaqSectors()
	if err != nil {
		t.Fatal(err)
	}
	if result.Classified != 1 || result.RemainingUnclassified != 1 || result.Sectors["consumer_discretionary"] != 1 {
		t.Fatalf("unexpected sector enrichment result: %+v", result)
	}
	readYAMLTest(t, m.profilePath, &profile)
	var aapl, abnb profileMember
	for _, member := range profile.Members {
		switch member.Ticker {
		case "AAPL":
			aapl = member
		case "ABNB":
			abnb = member
		}
	}
	if aapl.Sector != "unclassified" || !containsSource(aapl.Sources, "manual") {
		t.Fatalf("manual classification was changed: %+v", aapl)
	}
	if abnb.Sector != "consumer_discretionary" || !containsSource(abnb.Sources, nasdaqSectorLookupSourceLabel) {
		t.Fatalf("lookup classification/provenance was not stored: %+v", abnb)
	}
	backups, err := filepath.Glob(filepath.Join(m.backupDir, "profile.yaml.*.bak"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("profile backups=%d err=%v", len(backups), err)
	}

	second, err := m.enrichNasdaqSectors()
	if err != nil || second.Classified != 0 {
		t.Fatalf("repeat classification result=%+v err=%v", second, err)
	}
}
