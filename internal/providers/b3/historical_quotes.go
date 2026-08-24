package b3

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/luisdourado/invs/internal/model"
	"github.com/luisdourado/invs/internal/providers"
)

const (
	DefaultHistoricalQuotesBaseURL = "https://bvmf.bmfbovespa.com.br/InstDados/SerHist/"
	HistoricalQuotesParserVersion  = "b3-cotahist-v1"
	HistoricalQuotesResourceKind   = "historical_quotes"
)

// HistoricalQuoteSecurity is an explicit cross-source identity assertion. COTAHIST
// is never allowed to discover securities or map a ticker without its configured
// B3 ISIN.
type HistoricalQuoteSecurity struct {
	SecurityID string
	Ticker     string
	ISIN       string
	Currency   string
}

// HistoricalQuotesRequest deliberately accepts closed annual archives only. The
// file has no defensible historical publication timestamp, so canonical rows use
// local durable receipt as availability and remain installation-replay inputs.
type HistoricalQuotesRequest struct {
	Year       int
	Start      time.Time
	End        time.Time
	Securities []HistoricalQuoteSecurity
}

type HistoricalQuotesResult struct {
	providers.ResourceResult
	Bars            map[string][]model.PriceBar
	RecordsReceived int
	RecordsRejected int
	Duplicates      int
}

func (c *Client) CollectHistoricalQuotes(ctx context.Context, request HistoricalQuotesRequest) (HistoricalQuotesResult, error) {
	if c == nil || c.http == nil {
		return HistoricalQuotesResult{}, errors.New("B3 client HTTP getter is required")
	}
	normalized, requested, err := normalizeHistoricalQuotesRequest(request, c.now())
	if err != nil {
		return HistoricalQuotesResult{}, fmt.Errorf("B3 historical quotes request: %w", err)
	}
	fileName := fmt.Sprintf("COTAHIST_A%d.ZIP", normalized.Year)
	base, err := url.Parse(c.historicalQuotesBaseURL)
	if err != nil {
		return HistoricalQuotesResult{}, fmt.Errorf("parse B3 historical quotes base URL: %w", err)
	}
	downloadURL := base.ResolveReference(&url.URL{Path: fileName}).String()
	body, err := c.http.Get(ctx, downloadURL)
	if err != nil {
		return HistoricalQuotesResult{}, fmt.Errorf("B3 historical quotes download: %w", err)
	}
	fetchedAt := canonicalTime(c.now())
	resource := providers.NewRawResource(HistoricalQuotesResourceKind, fileName, body, fetchedAt, "application/zip")
	resource.URL = downloadURL
	resource.Year = normalized.Year
	resource.ParserVersion = HistoricalQuotesParserVersion
	resource.ParserMetadata = map[string]string{
		"year":                strconv.Itoa(normalized.Year),
		"start":               normalized.Start.Format(time.DateOnly),
		"end":                 normalized.End.Format(time.DateOnly),
		"member":              fmt.Sprintf("COTAHIST_A%d.TXT", normalized.Year),
		"record_size_bytes":   "245",
		"charset":             "windows-1252",
		"filter":              "CODBDI=02,TPMERC=010,exact-ticker-and-isin",
		"availability_policy": "installation_receipt",
		"price_basis":         "raw",
	}
	result := HistoricalQuotesResult{ResourceResult: providers.ResourceResult{Resources: []providers.RawResource{resource}}}
	result.Bars, result.RecordsReceived, result.RecordsRejected, result.Duplicates, err = parseHistoricalQuotes(resource.Bytes, normalized.Year, normalized.Start, normalized.End, requested, fetchedAt)
	if err != nil {
		return result, fmt.Errorf("B3 historical quotes: %w", err)
	}
	return result, nil
}

func normalizeHistoricalQuotesRequest(request HistoricalQuotesRequest, now time.Time) (HistoricalQuotesRequest, map[string]HistoricalQuoteSecurity, error) {
	if request.Year < 1986 || request.Year >= now.UTC().Year() {
		return HistoricalQuotesRequest{}, nil, fmt.Errorf("year must be between 1986 and %d", now.UTC().Year()-1)
	}
	if request.Start.IsZero() || request.End.IsZero() {
		return HistoricalQuotesRequest{}, nil, errors.New("start and end dates are required")
	}
	request.Start = time.Date(request.Start.UTC().Year(), request.Start.UTC().Month(), request.Start.UTC().Day(), 0, 0, 0, 0, time.UTC)
	request.End = time.Date(request.End.UTC().Year(), request.End.UTC().Month(), request.End.UTC().Day(), 0, 0, 0, 0, time.UTC)
	if request.Start.Year() != request.Year || request.End.Year() != request.Year || request.End.Before(request.Start) {
		return HistoricalQuotesRequest{}, nil, errors.New("start and end must be an ordered range within the requested year")
	}
	if len(request.Securities) == 0 {
		return HistoricalQuotesRequest{}, nil, errors.New("at least one security is required")
	}
	requested := make(map[string]HistoricalQuoteSecurity, len(request.Securities))
	for _, item := range request.Securities {
		item.SecurityID = strings.TrimSpace(item.SecurityID)
		item.Ticker = strings.ToUpper(strings.TrimSpace(item.Ticker))
		item.ISIN = strings.ToUpper(strings.TrimSpace(item.ISIN))
		item.Currency = strings.ToUpper(strings.TrimSpace(item.Currency))
		if item.SecurityID == "" || !tickerPattern.MatchString(item.Ticker) || !isinPattern.MatchString(item.ISIN) {
			return HistoricalQuotesRequest{}, nil, fmt.Errorf("security %q requires an ID, canonical ticker, and ISIN", item.Ticker)
		}
		if item.Currency != "BRL" {
			return HistoricalQuotesRequest{}, nil, fmt.Errorf("security %q currency must be BRL", item.Ticker)
		}
		if _, exists := requested[item.Ticker]; exists {
			return HistoricalQuotesRequest{}, nil, fmt.Errorf("ticker %q is duplicated", item.Ticker)
		}
		requested[item.Ticker] = item
	}
	request.Securities = nil
	for _, item := range requested {
		request.Securities = append(request.Securities, item)
	}
	sort.Slice(request.Securities, func(i, j int) bool { return request.Securities[i].Ticker < request.Securities[j].Ticker })
	return request, requested, nil
}

func parseHistoricalQuotes(body []byte, year int, start, end time.Time, requested map[string]HistoricalQuoteSecurity, fetchedAt time.Time) (map[string][]model.PriceBar, int, int, int, error) {
	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("open ZIP: %w", err)
	}
	wantMember := fmt.Sprintf("COTAHIST_A%d.TXT", year)
	var member *zip.File
	for _, candidate := range archive.File {
		if strings.EqualFold(path.Base(candidate.Name), wantMember) {
			if member != nil {
				return nil, 0, 0, 0, fmt.Errorf("ZIP contains duplicate %s members", wantMember)
			}
			member = candidate
		}
	}
	if member == nil {
		return nil, 0, 0, 0, fmt.Errorf("ZIP member %s is missing", wantMember)
	}
	reader, err := member.Open()
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("open ZIP member: %w", err)
	}
	defer reader.Close()

	bars := make(map[string][]model.PriceBar, len(requested))
	seen := make(map[string]struct{})
	found := make(map[string]bool, len(requested))
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	lineNumber, dataRecords, rejected, duplicates := 0, 0, 0, 0
	trailerSeen := false
	for scanner.Scan() {
		lineNumber++
		line := scanner.Bytes()
		if len(line) != 245 {
			return nil, dataRecords, rejected, duplicates, fmt.Errorf("member line %d has %d bytes, want 245", lineNumber, len(line))
		}
		switch string(line[0:2]) {
		case "00":
			if lineNumber != 1 || strings.TrimSpace(string(line[2:15])) != fmt.Sprintf("COTAHIST.%d", year) || strings.TrimSpace(string(line[15:23])) != "BOVESPA" {
				return nil, dataRecords, rejected, duplicates, errors.New("invalid COTAHIST header identity")
			}
		case "01":
			if trailerSeen {
				return nil, dataRecords, rejected, duplicates, errors.New("data record follows trailer")
			}
			dataRecords++
			ticker := strings.TrimSpace(string(line[12:24]))
			item, wanted := requested[ticker]
			if !wanted || string(line[10:12]) != "02" || string(line[24:27]) != "010" {
				continue
			}
			bar, parseErr := parseHistoricalQuoteRecord(line, lineNumber, year, item, fetchedAt, providers.SHA256(body), wantMember)
			if parseErr != nil {
				rejected++
				continue
			}
			if bar.Temporal.ObservedAt.Before(start) || bar.Temporal.ObservedAt.After(end) {
				continue
			}
			key := item.SecurityID + "\x00" + bar.Temporal.ObservedAt.Format(time.DateOnly)
			if _, exists := seen[key]; exists {
				duplicates++
				continue
			}
			seen[key] = struct{}{}
			found[ticker] = true
			bars[item.SecurityID] = append(bars[item.SecurityID], bar)
		case "99":
			if trailerSeen {
				return nil, dataRecords, rejected, duplicates, errors.New("duplicate COTAHIST trailer")
			}
			trailerSeen = true
			wantTotal, parseErr := strconv.Atoi(strings.TrimSpace(string(line[31:42])))
			if parseErr != nil || wantTotal != lineNumber {
				return nil, dataRecords, rejected, duplicates, fmt.Errorf("trailer record count %q does not equal %d", strings.TrimSpace(string(line[31:42])), lineNumber)
			}
		default:
			return nil, dataRecords, rejected, duplicates, fmt.Errorf("unsupported record type on line %d", lineNumber)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, dataRecords, rejected, duplicates, fmt.Errorf("scan member: %w", err)
	}
	if !trailerSeen {
		return nil, dataRecords, rejected, duplicates, errors.New("COTAHIST trailer is missing")
	}
	var missing []string
	for ticker := range requested {
		if !found[ticker] {
			missing = append(missing, ticker)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return bars, dataRecords, rejected, duplicates, fmt.Errorf("no exact round-lot cash rows for configured tickers: %s", strings.Join(missing, ", "))
	}
	for securityID := range bars {
		sort.Slice(bars[securityID], func(i, j int) bool {
			return bars[securityID][i].Temporal.ObservedAt.Before(bars[securityID][j].Temporal.ObservedAt)
		})
	}
	return bars, dataRecords, rejected, duplicates, nil
}

func parseHistoricalQuoteRecord(line []byte, lineNumber, year int, item HistoricalQuoteSecurity, fetchedAt time.Time, rawHash, member string) (model.PriceBar, error) {
	date, err := time.Parse("20060102", string(line[2:10]))
	if err != nil || date.Year() != year {
		return model.PriceBar{}, errors.New("invalid trading date")
	}
	isin := strings.TrimSpace(string(line[230:242]))
	if isin != item.ISIN {
		return model.PriceBar{}, fmt.Errorf("ISIN %q does not match configured %q", isin, item.ISIN)
	}
	if strings.TrimSpace(string(line[52:56])) != "R$" {
		return model.PriceBar{}, fmt.Errorf("reference currency %q is not BRL", strings.TrimSpace(string(line[52:56])))
	}
	factor, err := strconv.Atoi(strings.TrimSpace(string(line[210:217])))
	if err != nil || (factor != 1 && factor != 1000) {
		return model.PriceBar{}, fmt.Errorf("unsupported quotation factor %q", strings.TrimSpace(string(line[210:217])))
	}
	open, err0 := cotahistPrice(line[56:69], factor)
	high, err1 := cotahistPrice(line[69:82], factor)
	low, err2 := cotahistPrice(line[82:95], factor)
	closeValue, err3 := cotahistPrice(line[108:121], factor)
	volume, err4 := cotahistInteger(line[152:170])
	if err0 != nil || err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return model.PriceBar{}, errors.New("invalid numeric field")
	}
	if comparePriceDecimal(low, high) > 0 || comparePriceDecimal(open, low) < 0 || comparePriceDecimal(open, high) > 0 || comparePriceDecimal(closeValue, low) < 0 || comparePriceDecimal(closeValue, high) > 0 {
		return model.PriceBar{}, errors.New("invalid OHLC invariant")
	}
	observedAt := date.UTC()
	return model.PriceBar{
		Source: "b3_cotahist", SecurityID: item.SecurityID, Currency: item.Currency,
		Interval: "1d", PriceBasis: "raw", Open: open, High: high, Low: low,
		Close: closeValue, Volume: volume, RawPayloadHash: rawHash,
		Temporal: model.Temporal{
			ObservedAt: observedAt, ObservedPrecision: model.PrecisionDate,
			PublishedPrecision: model.PrecisionUnknown, AvailableAt: fetchedAt, IngestedAt: fetchedAt,
		},
		Provenance: model.Provenance{
			RawPayloadHash:   rawHash,
			RawRecordLocator: fmt.Sprintf("zip=%s/member=%s/line=%d/date=%s/ticker=%s/isin=%s", fmt.Sprintf("COTAHIST_A%d.ZIP", year), member, lineNumber, date.Format(time.DateOnly), item.Ticker, item.ISIN),
			IngestedAt:       fetchedAt, NormalizerVersion: HistoricalQuotesParserVersion,
		},
	}, nil
}

func cotahistPrice(raw []byte, factor int) (string, error) {
	digits := strings.TrimSpace(string(raw))
	if len(digits) == 0 {
		return "", errors.New("empty price")
	}
	for _, value := range digits {
		if value < '0' || value > '9' {
			return "", errors.New("price is not numeric")
		}
	}
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		digits = "0"
	}
	scale := 2
	if factor == 1000 {
		scale = 5
	}
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	value := digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
	return model.CanonicalDecimal(value, true)
}

func cotahistInteger(raw []byte) (string, error) {
	digits := strings.TrimSpace(string(raw))
	if digits == "" {
		return "", errors.New("empty integer")
	}
	for _, value := range digits {
		if value < '0' || value > '9' {
			return "", errors.New("integer is not numeric")
		}
	}
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		digits = "0"
	}
	return digits, nil
}

func comparePriceDecimal(a, b string) int {
	left, _ := new(big.Rat).SetString(a)
	right, _ := new(big.Rat).SetString(b)
	return left.Cmp(right)
}
