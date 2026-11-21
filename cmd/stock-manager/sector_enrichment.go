package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/luisdourado/invs/config"
	"github.com/luisdourado/invs/internal/providers/nasdaq"
)

type sectorEnrichmentResult struct {
	Source                string         `json:"source"`
	Classified            int            `json:"classified"`
	RemainingUnclassified int            `json:"remaining_unclassified"`
	Unmatched             []string       `json:"unmatched"`
	Sectors               map[string]int `json:"sectors"`
}

func (m *manager) nasdaqSectorEnrichmentHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	result, err := m.enrichNasdaqSectors()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not classify missing Nasdaq sectors: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (m *manager) enrichNasdaqSectors() (sectorEnrichmentResult, error) {
	cfg, profile, _, profileNode, err := m.load()
	if err != nil {
		return sectorEnrichmentResult{}, fmt.Errorf("load tracker configuration: %w", err)
	}
	configByTicker := make(map[string]config.Security, len(cfg.Universe))
	for _, security := range cfg.Universe {
		ticker := strings.ToUpper(strings.TrimSpace(security.Ticker))
		if _, exists := configByTicker[ticker]; exists {
			return sectorEnrichmentResult{}, fmt.Errorf("collector configuration contains duplicate ticker %s", ticker)
		}
		configByTicker[ticker] = security
	}

	result := sectorEnrichmentResult{
		Source:    "Nasdaq-100 ICB Industry company lookup; inferred classifications",
		Unmatched: []string{},
		Sectors:   map[string]int{},
	}
	for i := range profile.Members {
		member := &profile.Members[i]
		member.Ticker = strings.ToUpper(strings.TrimSpace(member.Ticker))
		if !containsSource(member.Sources, nasdaqSourceLabel) {
			continue
		}
		sector := strings.TrimSpace(member.Sector)
		if (sector == "" || sector == "unclassified") && !containsSource(member.Sources, "manual") {
			security, exists := configByTicker[member.Ticker]
			if !exists {
				result.Unmatched = append(result.Unmatched, member.Ticker+": collector mapping missing")
			} else {
				entry, matched, err := nasdaq.LookupCurrentSector(member.Ticker, security.LegalName)
				if err != nil {
					return sectorEnrichmentResult{}, fmt.Errorf("load sector lookup: %w", err)
				}
				if matched {
					member.Sector = entry.Sector
					member.Sources = addSource(member.Sources, nasdaqSectorLookupSourceLabel)
					result.Classified++
				} else {
					result.Unmatched = append(result.Unmatched, member.Ticker+": ticker/company mismatch or no lookup row")
				}
			}
		}
		if member.Sector == "" || member.Sector == "unclassified" {
			result.RemainingUnclassified++
		} else {
			result.Sectors[member.Sector]++
		}
	}
	sort.Strings(result.Unmatched)
	if result.Classified == 0 {
		return result, nil
	}
	if err := validateProfile(profile); err != nil {
		return sectorEnrichmentResult{}, fmt.Errorf("classified profile is invalid: %w", err)
	}
	if err := setProfileMembers(profileNode, profile.Members); err != nil {
		return sectorEnrichmentResult{}, fmt.Errorf("encode classified profile members: %w", err)
	}
	updated, err := encodeYAML(profileNode)
	if err != nil {
		return sectorEnrichmentResult{}, fmt.Errorf("encode classified profile: %w", err)
	}
	original, err := os.ReadFile(m.profilePath)
	if err != nil {
		return sectorEnrichmentResult{}, fmt.Errorf("read profile before classification: %w", err)
	}
	stampTime := time.Now().UTC()
	if m.now != nil {
		stampTime = m.now().UTC()
	}
	stamp := stampTime.Format("20060102T150405.000000000Z")
	if err := m.backup(filepath.Base(m.profilePath), stamp, original); err != nil {
		return sectorEnrichmentResult{}, fmt.Errorf("backup profile before classification: %w", err)
	}
	if err := writeAtomic(m.profilePath, updated); err != nil {
		return sectorEnrichmentResult{}, fmt.Errorf("write classified profile: %w", err)
	}
	return result, nil
}
