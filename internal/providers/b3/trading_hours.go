package b3

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/luisdourado/invs/internal/providers"
)

const (
	DefaultTradingHoursURL    = "https://www.b3.com.br/en_us/solutions/platforms/puma-trading-system/for-members-and-traders/trading-hours/equities/"
	TradingHoursParserVersion = "b3-cash-equity-hours-v1"
	TradingHoursResourceKind  = "trading_hours"
)

var (
	tradingHoursRowPattern  = regexp.MustCompile(`(?is)<tr\b[^>]*>(.*?)</tr>`)
	tradingHoursCellPattern = regexp.MustCompile(`(?is)<t[dh]\b[^>]*>(.*?)</t[dh]>`)
	localTimePattern        = regexp.MustCompile(`^(?:[01]?\d|2[0-3]):[0-5]\d$`)
)

// CashEquityHours is the source-native B3 cash-market core session. CloseLocal
// includes the closing call because the supported decision clock is based on
// the official close, not the end of continuous trading.
type CashEquityHours struct {
	OpenLocal            string
	ContinuousTradingEnd string
	CloseLocal           string
	SourceRow            string
}

type TradingHoursResult struct {
	providers.ResourceResult
	CashEquity CashEquityHours
}

func (c *Client) CollectTradingHours(ctx context.Context) (TradingHoursResult, error) {
	if c == nil || c.http == nil {
		return TradingHoursResult{}, errors.New("B3 client HTTP getter is required")
	}
	requestURL, err := c.tradingHoursURL()
	if err != nil {
		return TradingHoursResult{}, err
	}
	body, err := c.http.Get(ctx, requestURL)
	if err != nil {
		return TradingHoursResult{}, fmt.Errorf("B3 trading-hours request: %w", err)
	}
	resource := providers.NewRawResource(
		TradingHoursResourceKind,
		"cash-equity-trading-hours.html",
		body,
		canonicalTime(c.now()),
		"text/html; charset=utf-8",
	)
	resource.URL = requestURL
	resource.ParserVersion = TradingHoursParserVersion
	resource.ParserMetadata = map[string]string{
		"market":           "Cash Market, Odd lots market, Investment Funds and OTC Market",
		"mic":              "BVMF",
		"timezone":         "America/Sao_Paulo",
		"source_semantics": "current published cash-equity hours; no historical effective date or revision chain",
		"request_url":      requestURL,
	}
	result := TradingHoursResult{ResourceResult: providers.ResourceResult{Resources: []providers.RawResource{resource}}}
	result.CashEquity, err = parseCashEquityHours(body)
	if err != nil {
		return result, fmt.Errorf("B3 trading hours: %w", err)
	}
	resource.ParserMetadata["open_local"] = result.CashEquity.OpenLocal
	resource.ParserMetadata["continuous_trading_end"] = result.CashEquity.ContinuousTradingEnd
	resource.ParserMetadata["close_local"] = result.CashEquity.CloseLocal
	return result, nil
}

func (c *Client) tradingHoursURL() (string, error) {
	base := strings.TrimSpace(c.tradingHoursBaseURL)
	if base == "" {
		base = DefaultTradingHoursURL
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid B3 trading-hours URL %q", base)
	}
	return parsed.String(), nil
}

func parseCashEquityHours(body []byte) (CashEquityHours, error) {
	for _, row := range tradingHoursRowPattern.FindAllStringSubmatch(string(body), -1) {
		cells := tradingHoursCellPattern.FindAllStringSubmatch(row[1], -1)
		if len(cells) < 9 {
			continue
		}
		name := cleanMarketCalendarHTML(cells[0][1])
		if name != "Cash Market, Odd lots market, Investment Funds and OTC Market" {
			continue
		}
		result := CashEquityHours{
			OpenLocal:            cleanMarketCalendarHTML(cells[5][1]),
			ContinuousTradingEnd: cleanMarketCalendarHTML(cells[6][1]),
			CloseLocal:           cleanMarketCalendarHTML(cells[8][1]),
			SourceRow:            name,
		}
		if !localTimePattern.MatchString(result.OpenLocal) || !localTimePattern.MatchString(result.ContinuousTradingEnd) || !localTimePattern.MatchString(result.CloseLocal) {
			return CashEquityHours{}, fmt.Errorf("cash-equity row has invalid time fields %+v", result)
		}
		if !(result.OpenLocal < result.ContinuousTradingEnd && result.ContinuousTradingEnd < result.CloseLocal) {
			return CashEquityHours{}, fmt.Errorf("cash-equity row has non-increasing time fields %+v", result)
		}
		return result, nil
	}
	return CashEquityHours{}, errors.New("cash-equity trading-hours row was not found")
}
