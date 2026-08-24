// Package b3 provides a raw-first adapter for B3's public
// InstrumentsConsolidatedFile download.
package b3

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/luisdourado/invs/internal/providers"
	"golang.org/x/text/encoding/charmap"
)

const (
	DefaultAPIBaseURL = "https://arquivos.b3.com.br/api/"
	ParserVersion     = "b3-v1"
	FileName          = "InstrumentsConsolidated"
	ResourceKind      = "instruments"
)

var (
	isinPattern   = regexp.MustCompile(`^[A-Z]{2}[A-Z0-9]{9}[0-9]$`)
	tickerPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]*$`)
)

// Getter is the HTTP boundary shared by all provider adapters. Production
// callers should inject the repository's bounded HTTP client.
type Getter interface {
	Get(context.Context, string) ([]byte, error)
}

type Client struct {
	http                   Getter
	apiBaseURL             string
	listedCompaniesBaseURL string
	marketCalendarBaseURL  string
	now                    func() time.Time
}

func NewClient(getter Getter) *Client {
	return &Client{http: getter, apiBaseURL: DefaultAPIBaseURL, now: time.Now}
}

// Request deliberately requires an explicit report date and exact ticker
// allowlist. The public daily file is a current/reference snapshot; the
// adapter does not discover a universe or infer symbols from names.
type Request struct {
	ReportDate time.Time
	Tickers    []string
}

type ParseStats struct {
	RecordsReceived int
	RecordsRejected int
	Duplicates      int
}

type Result struct {
	providers.ResourceResult
	Instruments []Instrument
	Stats       ParseStats

	RecordsReceived int
	RecordsRejected int
	Duplicates      int
}

// Instrument is the source-native identity/listing subset needed by the
// historical metadata boundary. TradingStartDate/TradingEndDate retain B3's
// instrument trading interval semantics; they are not legal incorporation or
// original listing dates.
type Instrument struct {
	ReportDate           time.Time
	Ticker               string
	Asset                string
	AssetDescription     string
	Segment              string
	Market               string
	SecurityCategory     string
	TradingStartDate     time.Time
	TradingEndDate       *time.Time
	ISIN                 string
	TradingCurrency      string
	Specification        string
	CompanyName          string
	CorporateActionStart *time.Time
	MarketCapitalization string
	CorporateGovernance  string
	DistributionID       string
	RawRecordLocator     string
}

type requestNameResponse struct {
	RedirectURL string `json:"redirectUrl"`
	Token       string `json:"token"`
	File        struct {
		Name      string `json:"name"`
		Extension string `json:"extension"`
	} `json:"file"`
}

type instrumentColumns struct {
	reportDate           int
	ticker               int
	asset                int
	assetDescription     int
	segment              int
	market               int
	securityCategory     int
	tradingStartDate     int
	tradingEndDate       int
	isin                 int
	tradingCurrency      int
	specification        int
	companyName          int
	corporateActionStart int
	marketCapitalization int
	corporateGovernance  int
	distributionID       int
}

func (c *Client) Collect(ctx context.Context, request Request) (Result, error) {
	if c == nil || c.http == nil {
		return Result{}, errors.New("B3 client HTTP getter is required")
	}
	normalized, requested, err := normalizeRequest(request)
	if err != nil {
		return Result{}, fmt.Errorf("B3 request: %w", err)
	}
	requestURL, err := endpointURL(c.apiBaseURL, "download/requestname", url.Values{
		"fileName": []string{FileName},
		"date":     []string{normalized.ReportDate.Format(time.DateOnly)},
	})
	if err != nil {
		return Result{}, err
	}
	requestBody, err := c.http.Get(ctx, requestURL)
	if err != nil {
		return Result{}, fmt.Errorf("B3 request file: %w", err)
	}
	var download requestNameResponse
	if err := json.Unmarshal(requestBody, &download); err != nil {
		return Result{}, fmt.Errorf("B3 request file response: %w", err)
	}
	if strings.TrimSpace(download.Token) == "" {
		return Result{}, errors.New("B3 request file response has no download token")
	}
	fileName := strings.TrimSpace(download.File.Name)
	if fileName == "" {
		fileName = fmt.Sprintf("%sFile_%s_1", FileName, normalized.ReportDate.Format("20060102"))
	}
	extension := strings.TrimSpace(download.File.Extension)
	if extension == "" {
		extension = ".csv"
	}
	if !strings.HasPrefix(extension, ".") || strings.ContainsAny(extension, "/\\") {
		return Result{}, fmt.Errorf("B3 request file response has invalid extension %q", extension)
	}
	downloadURL, err := endpointURL(c.apiBaseURL, "download/", url.Values{"token": []string{download.Token}})
	if err != nil {
		return Result{}, err
	}
	body, err := c.http.Get(ctx, downloadURL)
	if err != nil {
		return Result{}, fmt.Errorf("B3 instruments download: %w", err)
	}
	fetchedAt := canonicalTime(c.now())
	resource := providers.NewRawResource(ResourceKind, fileName+extension, body, fetchedAt, "text/csv; charset=windows-1252")
	resource.URL = downloadURL
	resource.ParserVersion = ParserVersion
	resource.ParserMetadata = map[string]string{
		"report_date": normalized.ReportDate.Format(time.DateOnly),
		"file_name":   fileName + extension,
		"request_url": requestURL,
		"charset":     "windows-1252",
		"filter":      "SgmtNm=CASH,MktNm=EQUITY-CASH,SctyCtgyNm=SHARES",
	}
	result := Result{ResourceResult: providers.ResourceResult{Resources: []providers.RawResource{resource}}}
	result.Instruments, result.Stats, err = parseInstruments(body, normalized.ReportDate, requested)
	result.RecordsReceived = result.Stats.RecordsReceived
	result.RecordsRejected = result.Stats.RecordsRejected
	result.Duplicates = result.Stats.Duplicates
	if err != nil {
		return result, fmt.Errorf("B3 instruments: %w", err)
	}
	return result, nil
}

func normalizeRequest(request Request) (Request, map[string]struct{}, error) {
	if request.ReportDate.IsZero() {
		return Request{}, nil, errors.New("report date is required")
	}
	request.ReportDate = time.Date(request.ReportDate.UTC().Year(), request.ReportDate.UTC().Month(), request.ReportDate.UTC().Day(), 0, 0, 0, 0, time.UTC)
	if len(request.Tickers) == 0 {
		return Request{}, nil, errors.New("at least one ticker is required")
	}
	requested := make(map[string]struct{}, len(request.Tickers))
	for _, value := range request.Tickers {
		ticker := strings.ToUpper(strings.TrimSpace(value))
		if !tickerPattern.MatchString(ticker) {
			return Request{}, nil, fmt.Errorf("ticker %q is not a canonical B3 ticker", value)
		}
		if _, exists := requested[ticker]; exists {
			return Request{}, nil, fmt.Errorf("ticker %q is duplicated", ticker)
		}
		requested[ticker] = struct{}{}
	}
	return request, requested, nil
}

func parseInstruments(body []byte, reportDate time.Time, requested map[string]struct{}) ([]Instrument, ParseStats, error) {
	decoded, err := charmap.Windows1252.NewDecoder().Bytes(body)
	if err != nil {
		return nil, ParseStats{}, fmt.Errorf("decode CSV as Windows-1252: %w", err)
	}
	reader := csv.NewReader(bytes.NewReader(decoded))
	reader.Comma = ';'
	reader.FieldsPerRecord = -1
	reader.ReuseRecord = false
	status, err := reader.Read()
	if err != nil {
		return nil, ParseStats{}, fmt.Errorf("read file status: %w", err)
	}
	if len(status) != 1 || strings.TrimSpace(status[0]) != "Status do Arquivo: Final" {
		return nil, ParseStats{}, fmt.Errorf("file status %q is not final", strings.Join(status, ";"))
	}
	header, err := reader.Read()
	if err != nil {
		return nil, ParseStats{}, fmt.Errorf("read CSV header: %w", err)
	}
	columns, err := requiredColumns(header)
	if err != nil {
		return nil, ParseStats{}, err
	}

	stats := ParseStats{}
	byTicker := make(map[string]Instrument, len(requested))
	rowNumber := 2
	for {
		row, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		rowNumber++
		stats.RecordsReceived++
		if readErr != nil {
			stats.RecordsRejected++
			return nil, stats, fmt.Errorf("read CSV row %d: %w", rowNumber, readErr)
		}
		if len(row) <= maxColumn(columns) {
			stats.RecordsRejected++
			return nil, stats, fmt.Errorf("CSV row %d has %d fields, required index %d", rowNumber, len(row), maxColumn(columns))
		}
		ticker := strings.ToUpper(strings.TrimSpace(row[columns.ticker]))
		if _, wanted := requested[ticker]; !wanted {
			continue
		}
		if strings.TrimSpace(row[columns.segment]) != "CASH" || strings.TrimSpace(row[columns.market]) != "EQUITY-CASH" || strings.TrimSpace(row[columns.securityCategory]) != "SHARES" {
			continue
		}
		if _, exists := byTicker[ticker]; exists {
			stats.Duplicates++
			stats.RecordsRejected++
			return nil, stats, fmt.Errorf("ticker %s has multiple CASH/EQUITY-CASH/SHARES rows", ticker)
		}
		instrument, err := parseInstrumentRow(row, columns, reportDate, rowNumber, ticker)
		if err != nil {
			stats.RecordsRejected++
			return nil, stats, err
		}
		byTicker[ticker] = instrument
	}
	if len(byTicker) != len(requested) {
		missing := make([]string, 0, len(requested)-len(byTicker))
		for ticker := range requested {
			if _, exists := byTicker[ticker]; !exists {
				missing = append(missing, ticker)
			}
		}
		return nil, stats, fmt.Errorf("requested B3 ticker(s) missing from equity share rows: %s", strings.Join(sortedStrings(missing), ", "))
	}
	result := make([]Instrument, 0, len(byTicker))
	for ticker := range requested {
		result = append(result, byTicker[ticker])
	}
	sortInstruments(result)
	return result, stats, nil
}

func requiredColumns(header []string) (instrumentColumns, error) {
	positions := make(map[string]int, len(header))
	for index, value := range header {
		name := strings.TrimSpace(value)
		if _, exists := positions[name]; exists {
			return instrumentColumns{}, fmt.Errorf("CSV header has duplicate column %q", name)
		}
		positions[name] = index
	}
	index := func(name string) (int, error) {
		value, exists := positions[name]
		if !exists {
			return 0, fmt.Errorf("CSV header is missing required column %q", name)
		}
		return value, nil
	}
	var columns instrumentColumns
	var err error
	if columns.reportDate, err = index("RptDt"); err != nil {
		return columns, err
	}
	if columns.ticker, err = index("TckrSymb"); err != nil {
		return columns, err
	}
	if columns.asset, err = index("Asst"); err != nil {
		return columns, err
	}
	if columns.assetDescription, err = index("AsstDesc"); err != nil {
		return columns, err
	}
	if columns.segment, err = index("SgmtNm"); err != nil {
		return columns, err
	}
	if columns.market, err = index("MktNm"); err != nil {
		return columns, err
	}
	if columns.securityCategory, err = index("SctyCtgyNm"); err != nil {
		return columns, err
	}
	if columns.tradingStartDate, err = index("TradgStartDt"); err != nil {
		return columns, err
	}
	if columns.tradingEndDate, err = index("TradgEndDt"); err != nil {
		return columns, err
	}
	if columns.isin, err = index("ISIN"); err != nil {
		return columns, err
	}
	if columns.tradingCurrency, err = index("TradgCcy"); err != nil {
		return columns, err
	}
	if columns.specification, err = index("SpcfctnCd"); err != nil {
		return columns, err
	}
	if columns.companyName, err = index("CrpnNm"); err != nil {
		return columns, err
	}
	if columns.corporateActionStart, err = index("CorpActnStartDt"); err != nil {
		return columns, err
	}
	if columns.marketCapitalization, err = index("MktCptlstn"); err != nil {
		return columns, err
	}
	if columns.corporateGovernance, err = index("CorpGovnLvlNm"); err != nil {
		return columns, err
	}
	if columns.distributionID, err = index("DstrbtnId"); err != nil {
		return columns, err
	}
	return columns, nil
}

func parseInstrumentRow(row []string, columns instrumentColumns, reportDate time.Time, rowNumber int, ticker string) (Instrument, error) {
	rowDate, err := parseDate(row[columns.reportDate], "RptDt")
	if err != nil {
		return Instrument{}, fmt.Errorf("CSV row %d ticker %s: %w", rowNumber, ticker, err)
	}
	if !rowDate.Equal(reportDate) {
		return Instrument{}, fmt.Errorf("CSV row %d ticker %s report date %s differs from requested %s", rowNumber, ticker, rowDate.Format(time.DateOnly), reportDate.Format(time.DateOnly))
	}
	tradingStart, err := parseDate(row[columns.tradingStartDate], "TradgStartDt")
	if err != nil {
		return Instrument{}, fmt.Errorf("CSV row %d ticker %s: %w", rowNumber, ticker, err)
	}
	tradingEnd, err := parseOptionalDate(row[columns.tradingEndDate], "TradgEndDt")
	if err != nil {
		return Instrument{}, fmt.Errorf("CSV row %d ticker %s: %w", rowNumber, ticker, err)
	}
	if tradingEnd != nil && !tradingEnd.After(tradingStart) {
		return Instrument{}, fmt.Errorf("CSV row %d ticker %s has non-increasing trading interval", rowNumber, ticker)
	}
	isin := strings.ToUpper(strings.TrimSpace(row[columns.isin]))
	if !isinPattern.MatchString(isin) {
		return Instrument{}, fmt.Errorf("CSV row %d ticker %s has invalid ISIN %q", rowNumber, ticker, isin)
	}
	currency := strings.ToUpper(strings.TrimSpace(row[columns.tradingCurrency]))
	if len(currency) != 3 || strings.Trim(currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return Instrument{}, fmt.Errorf("CSV row %d ticker %s has invalid trading currency %q", rowNumber, ticker, currency)
	}
	companyName := strings.TrimSpace(row[columns.companyName])
	if companyName == "" {
		return Instrument{}, fmt.Errorf("CSV row %d ticker %s has empty company name", rowNumber, ticker)
	}
	corporateActionStart, err := parseOptionalDate(row[columns.corporateActionStart], "CorpActnStartDt")
	if err != nil {
		return Instrument{}, fmt.Errorf("CSV row %d ticker %s: %w", rowNumber, ticker, err)
	}
	return Instrument{
		ReportDate:           rowDate,
		Ticker:               ticker,
		Asset:                strings.TrimSpace(row[columns.asset]),
		AssetDescription:     strings.TrimSpace(row[columns.assetDescription]),
		Segment:              strings.TrimSpace(row[columns.segment]),
		Market:               strings.TrimSpace(row[columns.market]),
		SecurityCategory:     strings.TrimSpace(row[columns.securityCategory]),
		TradingStartDate:     tradingStart,
		TradingEndDate:       tradingEnd,
		ISIN:                 isin,
		TradingCurrency:      currency,
		Specification:        strings.TrimSpace(row[columns.specification]),
		CompanyName:          companyName,
		CorporateActionStart: corporateActionStart,
		MarketCapitalization: strings.TrimSpace(row[columns.marketCapitalization]),
		CorporateGovernance:  strings.TrimSpace(row[columns.corporateGovernance]),
		DistributionID:       strings.TrimSpace(row[columns.distributionID]),
		RawRecordLocator:     fmt.Sprintf("instruments/report_date=%s/row=%d/ticker=%s/isin=%s", rowDate.Format(time.DateOnly), rowNumber, ticker, isin),
	}, nil
}

func parseDate(value, field string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("%s is required", field)
	}
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be YYYY-MM-DD: %w", field, err)
	}
	return parsed.UTC(), nil
}

func parseOptionalDate(value, field string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "9999-12-31" {
		return nil, nil
	}
	parsed, err := parseDate(value, field)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func endpointURL(base, path string, query url.Values) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid B3 API base URL %q", base)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + strings.TrimLeft(path, "/")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func maxColumn(columns instrumentColumns) int {
	values := []int{columns.reportDate, columns.ticker, columns.asset, columns.assetDescription, columns.segment, columns.market, columns.securityCategory, columns.tradingStartDate, columns.tradingEndDate, columns.isin, columns.tradingCurrency, columns.specification, columns.companyName, columns.corporateActionStart, columns.marketCapitalization, columns.corporateGovernance, columns.distributionID}
	max := 0
	for _, value := range values {
		if value > max {
			max = value
		}
	}
	return max
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	for i := 1; i < len(result); i++ {
		for j := i; j > 0 && result[j] < result[j-1]; j-- {
			result[j], result[j-1] = result[j-1], result[j]
		}
	}
	return result
}

func sortInstruments(values []Instrument) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j].Ticker < values[j-1].Ticker; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func canonicalTime(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}
