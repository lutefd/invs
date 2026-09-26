package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luisdourado/invs/config"
	"github.com/luisdourado/invs/internal/providers/nasdaq"
	"gopkg.in/yaml.v3"
)

type nasdaqFixtureDoer struct{ body []byte }

func (f nasdaqFixtureDoer) Do(request *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK, Body: io.NopCloser(bytesReader(f.body)), Request: request,
	}, nil
}

type staticYahooGetter struct{ body []byte }

func (f staticYahooGetter) Get(context.Context, string) ([]byte, error) {
	return append([]byte(nil), f.body...), nil
}

func TestGIWSourceSelectionFailsClosedWithoutCredentials(t *testing.T) {
	t.Setenv("INVS_NASDAQ_SOURCE", "giw")
	t.Setenv("INVS_NASDAQ_GIW_USERNAME", "")
	t.Setenv("INVS_NASDAQ_GIW_PASSWORD", "")
	_, _, err := fetchNasdaqCurrentUniverse(context.Background(), nasdaqFixtureDoer{}, "invs-test", time.Now())
	if err == nil || !strings.Contains(err.Error(), "username and password are required") {
		t.Fatalf("missing GIW credentials error=%v", err)
	}
}

func TestNasdaqHTTPClientDisablesHTTP2(t *testing.T) {
	client := newNasdaqHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Nasdaq HTTP transport type = %T", client.Transport)
	}
	if transport.ForceAttemptHTTP2 || transport.TLSNextProto == nil || len(transport.TLSNextProto) != 0 ||
		transport.TLSClientConfig == nil || len(transport.TLSClientConfig.NextProtos) != 1 || transport.TLSClientConfig.NextProtos[0] != "http/1.1" {
		t.Fatalf("Nasdaq HTTP/2 remains enabled: force=%t protocols=%v", transport.ForceAttemptHTTP2, transport.TLSNextProto)
	}
}

func TestReadNasdaqSyncStateAcceptsGIWAndMigratesWebsiteSourceLabel(t *testing.T) {
	for _, test := range []struct {
		name, source, sourceURL, wantSource string
	}{
		{name: "licensed GIW", source: nasdaq.CurrentUniverseSourceGIW, sourceURL: nasdaq.GIWWeightingURL, wantSource: nasdaq.CurrentUniverseSourceGIW},
		{name: "legacy website state", sourceURL: nasdaq.CurrentUniverseURL, wantSource: nasdaq.CurrentUniverseSourceWebsite},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			body, err := json.Marshal(nasdaqSyncState{Version: nasdaqSyncStateVersion, Source: test.source, SourceURL: test.sourceURL})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatal(err)
			}
			state, exists, err := readNasdaqSyncState(path)
			if err != nil || !exists || state.Source != test.wantSource {
				t.Fatalf("state/existence/error=%+v/%t/%v", state, exists, err)
			}
		})
	}
}

func TestSyncNasdaq100MergesCurrentMembersAndRemovesOnlyFormerSourceMembers(t *testing.T) {
	m := fixtureManager(t)
	now := time.Date(2026, 9, 23, 22, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	firstTrade := time.Date(2010, 2, 4, 9, 30, 0, 0, mustNewYork(t)).Unix()
	m.yahooHTTP = staticYahooGetter{body: []byte(fmt.Sprintf(`{"chart":{"result":[{"meta":{"exchangeTimezoneName":"America/New_York","firstTradeDate":%d},"timestamp":[],"indicators":{}}],"error":null}}`, firstTrade))}
	m.nasdaqHTTP = nasdaqFixtureDoer{body: nasdaqFixtureBody(testNasdaqTickers(1, 99))}

	first, err := m.syncNasdaq100(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Constituents != 100 || first.Tracked != 100 || len(first.Added) != 99 || len(first.HistoryUnavailable) != 0 {
		t.Fatalf("first sync result = %+v", first)
	}
	var profile universeProfile
	readYAMLTest(t, m.profilePath, &profile)
	if len(profile.Members) != 100 || profile.MembershipAsOf != "2026-09-23" {
		t.Fatalf("profile members/as-of = %d/%s", len(profile.Members), profile.MembershipAsOf)
	}
	var aapl, generated profileMember
	for _, member := range profile.Members {
		switch member.Ticker {
		case "AAPL":
			aapl = member
		case "S002":
			generated = member
		}
	}
	if !containsSource(aapl.Sources, "manual") || !containsSource(aapl.Sources, nasdaqSourceLabel) {
		t.Fatalf("pre-existing manual member lost its source tags: %+v", aapl)
	}
	if !containsSource(generated.Sources, nasdaqSourceLabel) || generated.Sector != "unclassified" {
		t.Fatalf("new source member not labeled safely: %+v", generated)
	}
	var cfg config.Config
	readYAMLTest(t, m.configPath, &cfg)
	if len(cfg.Universe) != 100 {
		t.Fatalf("collector universe contains %d securities, want 100", len(cfg.Universe))
	}
	var stableID string
	for _, member := range profile.Members {
		if member.Ticker == "S002" {
			stableID = member.SecurityID
		}
	}

	now = now.Add(time.Second)
	m.nasdaqHTTP = nasdaqFixtureDoer{body: nasdaqFixtureBody(testNasdaqTickers(2, 100))}
	second, err := m.syncNasdaq100(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Constituents != 100 || second.Tracked != 100 || len(second.Added) != 1 || second.Added[0] != "S100" || len(second.Removed) != 1 || second.Removed[0] != "S001" {
		t.Fatalf("second sync result = %+v", second)
	}
	readYAMLTest(t, m.profilePath, &profile)
	var foundStable, foundRemoved bool
	for _, member := range profile.Members {
		if member.Ticker == "S002" {
			foundStable = member.SecurityID == stableID
		}
		if member.Ticker == "S001" {
			foundRemoved = true
		}
	}
	if !foundStable || foundRemoved {
		t.Fatalf("stable IDs/source removals were not applied: foundStable=%t foundRemoved=%t", foundStable, foundRemoved)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(m.backupDir), "nasdaq-100-state.json")); err != nil {
		t.Fatalf("snapshot state was not written: %v", err)
	}
	if paths, err := filepath.Glob(filepath.Join(m.nasdaqSnapshotDir(), "*.json")); err != nil || len(paths) != 2 {
		t.Fatalf("raw snapshot archives=%d err=%v", len(paths), err)
	}
}

func TestSyncNasdaq100LeavesLastCompleteProfileOnInvalidSnapshot(t *testing.T) {
	m := fixtureManager(t)
	profileBefore, err := os.ReadFile(m.profilePath)
	if err != nil {
		t.Fatal(err)
	}
	configBefore, err := os.ReadFile(m.configPath)
	if err != nil {
		t.Fatal(err)
	}
	m.nasdaqHTTP = nasdaqFixtureDoer{body: []byte(`{"status":{"rCode":200},"data":{"date":"Sep 23, 2026 3:10 PM","totalrecords":1,"limit":1,"offset":0,"data":{"rows":[]}}}`)}
	if _, err := m.syncNasdaq100(context.Background()); err == nil {
		t.Fatal("incomplete source snapshot was accepted")
	}
	profileAfter, err := os.ReadFile(m.profilePath)
	if err != nil {
		t.Fatal(err)
	}
	configAfter, err := os.ReadFile(m.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(profileAfter) != string(profileBefore) || string(configAfter) != string(configBefore) {
		t.Fatal("invalid source snapshot changed the active profile or collector config")
	}
}

func TestResolveNasdaqSectorFallsBackToTickerAndCompanyLookup(t *testing.T) {
	sector, inferred, err := resolveNasdaqSector(nasdaq.CurrentConstituent{
		Ticker: "ABNB", Company: "Airbnb, Inc. Class A Common Stock",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sector != "consumer_discretionary" || !inferred {
		t.Fatalf("lookup result sector=%q inferred=%t, want consumer_discretionary inferred", sector, inferred)
	}
	sector, inferred, err = resolveNasdaqSector(nasdaq.CurrentConstituent{
		Ticker: "ABNB", Company: "Different Company Inc.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sector != "unclassified" || inferred {
		t.Fatalf("mismatched company result sector=%q inferred=%t, want unclassified without inference", sector, inferred)
	}
}

func nasdaqFixtureBody(tickers []string) []byte {
	rows := make([]map[string]string, 0, len(tickers))
	for _, ticker := range tickers {
		rows = append(rows, map[string]string{
			"symbol": ticker, "companyName": "Sample " + ticker + " Inc. Common Stock", "sector": "",
		})
	}
	data := map[string]any{
		"status": map[string]any{"rCode": 200},
		"data": map[string]any{
			"date": "Sep 23, 2026 3:10 PM", "totalrecords": len(rows), "limit": 200, "offset": 0,
			"data": map[string]any{"rows": rows},
		},
	}
	body, _ := json.Marshal(data)
	return body
}

func testNasdaqTickers(first, last int) []string {
	values := []string{"AAPL"}
	for i := first; i <= last; i++ {
		values = append(values, fmt.Sprintf("S%03d", i))
	}
	return values
}

func mustNewYork(t *testing.T) *time.Location {
	t.Helper()
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	return location
}

func readYAMLTest(t *testing.T, path string, target any) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(body, target); err != nil {
		t.Fatal(err)
	}
}

func bytesReader(body []byte) *bytes.Reader {
	return bytes.NewReader(body)
}
