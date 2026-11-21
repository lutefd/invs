package nasdaq

import (
	"embed"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode"
)

//go:embed nasdaq100_sector_lookup.csv
var sectorLookupFiles embed.FS

// SectorClassification is a current Nasdaq-100 ICB classification mapped to
// the sector labels used by the discovery profile.
type SectorClassification struct {
	Ticker      string
	CompanyName string
	ICBIndustry string
	Sector      string
}

var (
	sectorLookupOnce sync.Once
	sectorLookup     map[string]SectorClassification
	sectorLookupErr  error
)

// LookupCurrentSector returns a classification only when both the ticker and
// company name match the checked-in current-universe lookup. Company-name
// matching ignores punctuation and common listing/legal suffixes.
func LookupCurrentSector(ticker, companyName string) (SectorClassification, bool, error) {
	sectorLookupOnce.Do(loadSectorLookup)
	if sectorLookupErr != nil {
		return SectorClassification{}, false, sectorLookupErr
	}
	entry, ok := sectorLookup[strings.ToUpper(strings.TrimSpace(ticker))]
	if !ok || normalizeLookupCompany(entry.CompanyName) != normalizeLookupCompany(companyName) {
		return SectorClassification{}, false, nil
	}
	return entry, true, nil
}

func loadSectorLookup() {
	body, err := sectorLookupFiles.ReadFile("nasdaq100_sector_lookup.csv")
	if err != nil {
		sectorLookupErr = fmt.Errorf("read embedded Nasdaq sector lookup: %w", err)
		return
	}
	reader := csv.NewReader(strings.NewReader(string(body)))
	reader.FieldsPerRecord = 4
	header, err := reader.Read()
	if err != nil {
		sectorLookupErr = fmt.Errorf("read Nasdaq sector lookup header: %w", err)
		return
	}
	if strings.Join(header, ",") != "ticker,company_name,icb_industry,app_sector" {
		sectorLookupErr = errors.New("Nasdaq sector lookup has an unexpected header")
		return
	}
	loaded := make(map[string]SectorClassification)
	for line := 2; ; line++ {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			sectorLookupErr = fmt.Errorf("read Nasdaq sector lookup row %d: %w", line, err)
			return
		}
		ticker := strings.ToUpper(strings.TrimSpace(record[0]))
		company := strings.TrimSpace(record[1])
		icbIndustry := strings.TrimSpace(record[2])
		appSector := strings.TrimSpace(record[3])
		if ticker == "" || company == "" || icbIndustry == "" || !validLookupSector(appSector) {
			sectorLookupErr = fmt.Errorf("Nasdaq sector lookup row %d is incomplete or has an unsupported sector", line)
			return
		}
		if _, duplicate := loaded[ticker]; duplicate {
			sectorLookupErr = fmt.Errorf("Nasdaq sector lookup contains duplicate ticker %s", ticker)
			return
		}
		loaded[ticker] = SectorClassification{
			Ticker: ticker, CompanyName: company, ICBIndustry: icbIndustry, Sector: appSector,
		}
	}
	if len(loaded) == 0 {
		sectorLookupErr = errors.New("Nasdaq sector lookup is empty")
		return
	}
	sectorLookup = loaded
}

func validLookupSector(value string) bool {
	switch value {
	case "communication_services", "consumer_discretionary", "consumer_staples",
		"energy", "financials", "health_care", "industrials", "materials",
		"real_estate", "technology", "utilities":
		return true
	default:
		return false
	}
}

func normalizeLookupCompany(value string) string {
	var tokens []string
	var current strings.Builder
	dropNext := false
	flush := func() {
		if current.Len() == 0 {
			return
		}
		token := current.String()
		current.Reset()
		if token == "class" {
			dropNext = true
			return
		}
		if dropNext {
			dropNext = false
			return
		}
		tokens = append(tokens, token)
	}
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	filtered := tokens[:0]
	for _, token := range tokens {
		switch token {
		case "inc", "incorporated", "corporation", "corp", "company", "co", "ltd", "limited", "plc", "holdings", "holding", "common", "stock", "shares", "share", "ordinary", "the":
			continue
		default:
			filtered = append(filtered, token)
		}
	}
	return strings.Join(filtered, " ")
}
