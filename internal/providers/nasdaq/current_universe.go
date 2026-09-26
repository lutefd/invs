package nasdaq

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const CurrentUniverseURL = "https://api.nasdaq.com/api/quote/list-type/nasdaq100?limit=200&offset=0"
const GIWWeightingURL = "https://indexes.nasdaqomx.com/reports2/UFFWeighting.ashx"

const (
	CurrentUniverseSourceWebsite = "nasdaq_website"
	CurrentUniverseSourceGIW     = "nasdaq_giw"
)

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
	Source    string               `json:"source"`
	SourceURL string               `json:"source_url"`
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
	snapshot.Source = CurrentUniverseSourceWebsite
	snapshot.SourceURL = CurrentUniverseURL
	return snapshot, body, err
}

// FetchGIWCurrentUniverse requests Nasdaq's authenticated daily constituent
// weighting file. GIW is the supported source; credentials are supplied by the
// operator and are sent only in the HTTPS form body.
func FetchGIWCurrentUniverse(ctx context.Context, client HTTPDoer, username, password, userAgent string, retrievedAt time.Time) (CurrentUniverse, []byte, error) {
	if client == nil {
		return CurrentUniverse{}, nil, errors.New("Nasdaq GIW HTTP client is required")
	}
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	userAgent = strings.TrimSpace(userAgent)
	if username == "" || password == "" {
		return CurrentUniverse{}, nil, errors.New("Nasdaq GIW username and password are required")
	}
	if userAgent == "" {
		return CurrentUniverse{}, nil, errors.New("Nasdaq GIW user agent is required")
	}
	if retrievedAt.IsZero() {
		return CurrentUniverse{}, nil, errors.New("Nasdaq GIW snapshot retrieval time is required")
	}
	eastern, err := time.LoadLocation("America/New_York")
	if err != nil {
		return CurrentUniverse{}, nil, fmt.Errorf("load Nasdaq GIW source timezone: %w", err)
	}
	localNow := retrievedAt.In(eastern)
	firstDate := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, eastern)
	var lastRaw []byte
	var lastErr error
	for daysBack := 0; daysBack < 8; daysBack++ {
		tradeDate := firstDate.AddDate(0, 0, -daysBack)
		if tradeDate.Weekday() == time.Saturday || tradeDate.Weekday() == time.Sunday {
			continue
		}
		body, status, err := requestGIWWeightings(ctx, client, username, password, userAgent, tradeDate)
		lastRaw = body
		if err != nil {
			return CurrentUniverse{}, body, fmt.Errorf("fetch Nasdaq GIW constituents for %s: %w", tradeDate.Format(time.DateOnly), err)
		}
		if looksLikeGIWAuthenticationPage(body) {
			return CurrentUniverse{}, body, errors.New("Nasdaq GIW returned an authentication page; check GIW credentials and index entitlement")
		}
		if status >= 300 && status < 400 {
			return CurrentUniverse{}, body, errors.New("Nasdaq GIW redirected the request; check GIW credentials and index entitlement")
		}
		switch status {
		case http.StatusOK:
		case http.StatusNotFound:
			lastErr = fmt.Errorf("GIW has no end-of-day file for %s", tradeDate.Format(time.DateOnly))
			continue
		case http.StatusUnauthorized, http.StatusForbidden:
			return CurrentUniverse{}, body, fmt.Errorf("Nasdaq GIW rejected the configured account (HTTP %d); check GIW credentials and index entitlement", status)
		default:
			return CurrentUniverse{}, body, fmt.Errorf("Nasdaq GIW request returned HTTP %d", status)
		}
		snapshot, parseErr := ParseGIWCurrentUniverse(body, tradeDate, retrievedAt)
		if parseErr == nil {
			return snapshot, body, nil
		}
		if isGIWNoDataResponse(body) {
			lastErr = fmt.Errorf("GIW has no end-of-day file for %s", tradeDate.Format(time.DateOnly))
			continue
		}
		return CurrentUniverse{}, body, parseErr
	}
	if lastErr == nil {
		lastErr = errors.New("no weekday in the GIW lookup window")
	}
	return CurrentUniverse{}, lastRaw, fmt.Errorf("no complete Nasdaq GIW end-of-day snapshot found in the last 8 calendar days: %w", lastErr)
}

func requestGIWWeightings(ctx context.Context, client HTTPDoer, username, password, userAgent string, tradeDate time.Time) ([]byte, int, error) {
	const maxAttempts = 3
	var lastBody []byte
	var lastStatus int
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		lastBody, lastStatus, lastErr = requestGIWWeightingsOnce(ctx, client, username, password, userAgent, tradeDate)
		if ctx.Err() != nil {
			return lastBody, lastStatus, ctx.Err()
		}
		if lastErr == nil && !retryableGIWStatus(lastStatus) {
			return lastBody, lastStatus, nil
		}
		if attempt == maxAttempts-1 {
			if lastErr != nil {
				return lastBody, lastStatus, fmt.Errorf("GIW request failed after %d attempts: %w", maxAttempts, lastErr)
			}
			return lastBody, lastStatus, fmt.Errorf("GIW request returned HTTP %d after %d attempts", lastStatus, maxAttempts)
		}
		delay := 500 * time.Millisecond * time.Duration(1<<attempt)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return lastBody, lastStatus, ctx.Err()
		case <-timer.C:
		}
	}
	return lastBody, lastStatus, lastErr
}

func retryableGIWStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

func requestGIWWeightingsOnce(ctx context.Context, client HTTPDoer, username, password, userAgent string, tradeDate time.Time) ([]byte, int, error) {
	query := url.Values{}
	query.Set("IndexSymbol", "NDX")
	query.Set("Date", tradeDate.Format(time.DateOnly))
	query.Set("Type", "PIPE")
	query.Set("FileType", "EOD")
	endpoint := GIWWeightingURL + "?" + query.Encode()
	form := url.Values{}
	form.Set("username", username)
	form.Set("password", password)
	requestContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "text/plain, text/csv, */*")
	request.Header.Set("User-Agent", userAgent)
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("request GIW web service: %w", err)
	}
	defer response.Body.Close()
	if response.Request != nil && response.Request.URL.Host != "indexes.nasdaqomx.com" {
		return nil, 0, errors.New("Nasdaq GIW request redirected away from indexes.nasdaqomx.com")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil {
		return nil, response.StatusCode, fmt.Errorf("read GIW response: %w", err)
	}
	if len(body) > 4<<20 {
		return body, response.StatusCode, errors.New("Nasdaq GIW response exceeded 4 MiB")
	}
	return body, response.StatusCode, nil
}

// ParseGIWCurrentUniverse reads the documented UFF PIPE response. The first
// six weighting fields are Symbol, Closing Price, Market Value, Index Shares,
// Index Weight, and Company Name.
func ParseGIWCurrentUniverse(body []byte, tradeDate, retrievedAt time.Time) (CurrentUniverse, error) {
	if tradeDate.IsZero() || retrievedAt.IsZero() {
		return CurrentUniverse{}, errors.New("Nasdaq GIW trade date and retrieval time are required")
	}
	eastern, err := time.LoadLocation("America/New_York")
	if err != nil {
		return CurrentUniverse{}, fmt.Errorf("load Nasdaq GIW source timezone: %w", err)
	}
	tradeDate = tradeDate.In(eastern)
	dateString := tradeDate.Format(time.DateOnly)
	if !bytes.Contains(body, []byte(dateString)) {
		return CurrentUniverse{}, fmt.Errorf("Nasdaq GIW response does not identify the requested trade date %s", dateString)
	}
	reader := csv.NewReader(bytes.NewReader(body))
	reader.Comma = '|'
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	rows := make([]CurrentConstituent, 0, 101)
	seen := make(map[string]bool, 101)
	for {
		fields, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return CurrentUniverse{}, fmt.Errorf("decode Nasdaq GIW pipe data: %w", err)
		}
		if len(fields) < 6 {
			continue
		}
		ticker := strings.ToUpper(strings.TrimSpace(fields[0]))
		if !currentTickerPattern.MatchString(ticker) {
			continue
		}
		if _, err := strconv.ParseFloat(strings.TrimSpace(fields[4]), 64); err != nil {
			continue
		}
		company := strings.TrimSpace(fields[5])
		if len([]rune(company)) < 2 || len([]rune(company)) > 160 {
			return CurrentUniverse{}, fmt.Errorf("Nasdaq GIW has no usable company name for %s", ticker)
		}
		if seen[ticker] {
			return CurrentUniverse{}, fmt.Errorf("Nasdaq GIW contains duplicate ticker %q", ticker)
		}
		seen[ticker] = true
		rows = append(rows, CurrentConstituent{Ticker: ticker, Company: company})
	}
	if len(rows) < 90 || len(rows) > 110 {
		return CurrentUniverse{}, fmt.Errorf("Nasdaq GIW row count is incomplete or outside the expected 90–110 securities (rows=%d)", len(rows))
	}
	date := time.Date(tradeDate.Year(), tradeDate.Month(), tradeDate.Day(), 0, 0, 0, 0, eastern).UTC().Truncate(time.Microsecond)
	retrievedAt = retrievedAt.UTC().Truncate(time.Microsecond)
	digest := sha256.Sum256(body)
	return CurrentUniverse{
		AsOf: date, Retrieved: retrievedAt, Count: len(rows), SHA256: hex.EncodeToString(digest[:]),
		Source: CurrentUniverseSourceGIW, SourceURL: GIWWeightingURL, Members: rows,
	}, nil
}
func looksLikeGIWAuthenticationPage(body []byte) bool {
	content := strings.ToLower(string(body))
	return strings.Contains(content, "log in") || strings.Contains(content, "login") ||
		strings.Contains(content, "unauthorized") || strings.Contains(content, "authentication") ||
		strings.Contains(content, "access denied") || strings.Contains(content, "entitlement")
}

func isGIWNoDataResponse(body []byte) bool {
	content := strings.ToLower(strings.TrimSpace(string(body)))
	return strings.Contains(content, "no data") || strings.Contains(content, "no file") ||
		strings.Contains(content, "not available") || strings.Contains(content, "file not found")
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
		asOf, err = time.ParseInLocation("Jan 2, 2006", strings.TrimSpace(wire.Data.Date), eastern)
		if err != nil {
			return CurrentUniverse{}, fmt.Errorf("parse Nasdaq source date: %w", err)
		}
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
