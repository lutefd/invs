package nasdaq

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const CurrentUniverseURL = "https://api.nasdaq.com/api/quote/list-type/nasdaq100?limit=200&offset=0"

var currentTickerPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9.-]{0,15}$`)

type CurrentConstituent struct {
	Ticker  string `json:"ticker"`
	Company string `json:"company_name"`
	Sector  string `json:"sector,omitempty"`
}

type CurrentUniverse struct {
	AsOf      time.Time            `json:"as_of"`
	Retrieved time.Time            `json:"retrieved_at"`
	Count     int                  `json:"count"`
	SHA256    string               `json:"sha256"`
	Members   []CurrentConstituent `json:"members"`
}

type currentUniverseWire struct {
	Status struct {
		Code int `json:"rCode"`
	} `json:"status"`
	Data struct {
		Date         string `json:"date"`
		TotalRecords int    `json:"totalrecords"`
		Limit        int    `json:"limit"`
		Offset       int    `json:"offset"`
		Data         struct {
			Rows []struct {
				Ticker  string `json:"symbol"`
				Company string `json:"companyName"`
				Sector  string `json:"sector"`
			} `json:"rows"`
		} `json:"data"`
	} `json:"data"`
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// FetchCurrentUniverse reads Nasdaq's current website constituent snapshot. The
// endpoint is an undocumented UI feed, so callers must retain the response and
// treat it as current research context rather than historical membership truth.
func FetchCurrentUniverse(ctx context.Context, client HTTPDoer, userAgent string, retrievedAt time.Time) (CurrentUniverse, []byte, error) {
	if client == nil {
		return CurrentUniverse{}, nil, errors.New("Nasdaq current-universe HTTP client is required")
	}
	userAgent = strings.TrimSpace(userAgent)
	if userAgent == "" {
		return CurrentUniverse{}, nil, errors.New("Nasdaq current-universe user agent is required")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, CurrentUniverseURL, nil)
	if err != nil {
		return CurrentUniverse{}, nil, err
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Origin", "https://www.nasdaq.com")
	request.Header.Set("Referer", "https://www.nasdaq.com/")
	response, err := client.Do(request)
	if err != nil {
		return CurrentUniverse{}, nil, fmt.Errorf("fetch Nasdaq current constituents: %w", err)
	}
	defer response.Body.Close()
	if response.Request != nil && response.Request.URL.Host != "api.nasdaq.com" {
		return CurrentUniverse{}, nil, errors.New("Nasdaq current-universe request redirected away from api.nasdaq.com")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil {
		return CurrentUniverse{}, nil, fmt.Errorf("read Nasdaq current constituents: %w", err)
	}
	if len(body) > 4<<20 {
		return CurrentUniverse{}, body, errors.New("Nasdaq current-universe response exceeded 4 MiB")
	}
	if response.StatusCode != http.StatusOK {
		return CurrentUniverse{}, body, fmt.Errorf("Nasdaq current-universe request returned HTTP %d", response.StatusCode)
	}
	snapshot, err := ParseCurrentUniverse(body, retrievedAt)
	return snapshot, body, err
}

func ParseCurrentUniverse(body []byte, retrievedAt time.Time) (CurrentUniverse, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	var wire currentUniverseWire
	if err := decoder.Decode(&wire); err != nil {
		return CurrentUniverse{}, fmt.Errorf("decode Nasdaq current-universe response: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return CurrentUniverse{}, errors.New("Nasdaq current-universe response contains trailing data")
	}
	rows := wire.Data.Data.Rows
	if wire.Status.Code != http.StatusOK {
		return CurrentUniverse{}, fmt.Errorf("Nasdaq current-universe status code is %d", wire.Status.Code)
	}
	if wire.Data.Offset != 0 || wire.Data.Limit < len(rows) || wire.Data.TotalRecords != len(rows) || len(rows) < 90 || len(rows) > 110 {
		return CurrentUniverse{}, fmt.Errorf("Nasdaq current-universe row count is incomplete or outside the expected 90–110 securities (total=%d limit=%d offset=%d rows=%d)", wire.Data.TotalRecords, wire.Data.Limit, wire.Data.Offset, len(rows))
	}
	eastern, err := time.LoadLocation("America/New_York")
	if err != nil {
		return CurrentUniverse{}, fmt.Errorf("load Nasdaq source timezone: %w", err)
	}
	asOf, err := time.ParseInLocation("Jan 2, 2006 3:04 PM", strings.TrimSpace(wire.Data.Date), eastern)
	if err != nil {
		return CurrentUniverse{}, fmt.Errorf("parse Nasdaq source timestamp: %w", err)
	}
	if retrievedAt.IsZero() {
		return CurrentUniverse{}, errors.New("Nasdaq snapshot retrieval time is required")
	}
	retrievedAt = retrievedAt.UTC().Truncate(time.Microsecond)
	digest := sha256.Sum256(body)
	snapshot := CurrentUniverse{
		AsOf: asOf.UTC().Truncate(time.Microsecond), Retrieved: retrievedAt,
		Count: len(rows), SHA256: hex.EncodeToString(digest[:]),
		Members: make([]CurrentConstituent, 0, len(rows)),
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		ticker := strings.ToUpper(strings.TrimSpace(row.Ticker))
		company := strings.TrimSpace(row.Company)
		if !currentTickerPattern.MatchString(ticker) || seen[ticker] {
			return CurrentUniverse{}, fmt.Errorf("Nasdaq current-universe contains invalid or duplicate ticker %q", row.Ticker)
		}
		if len([]rune(company)) < 2 || len([]rune(company)) > 160 {
			return CurrentUniverse{}, fmt.Errorf("Nasdaq current-universe has no usable company name for %s", ticker)
		}
		seen[ticker] = true
		snapshot.Members = append(snapshot.Members, CurrentConstituent{
			Ticker: ticker, Company: company, Sector: strings.TrimSpace(row.Sector),
		})
	}
	return snapshot, nil
}
