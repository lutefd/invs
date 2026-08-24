package b3

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/luisdourado/invs/internal/providers"
)

const (
	// DefaultMarketCalendarURL is B3's public multi-year market-calendar page.
	// It is separate from the trading-hours page because the calendar page
	// carries date-specific closures and special-hours notices.
	DefaultMarketCalendarURL    = "https://b3.com.br/en_us/solutions/platforms/puma-trading-system/for-members-and-traders/trading-calendar/holidays/"
	MarketCalendarParserVersion = "b3-market-calendar-v1"
	MarketCalendarResourceKind  = "market_calendar"
)

var (
	marketCalendarHeaderPattern = regexp.MustCompile(`(?is)<h2\b[^>]*>\s*Market Calendar\s+(\d{4})\s*</h2>`)
	marketCalendarMonthPattern  = regexp.MustCompile(`(?is)<li\b[^>]*class="[^"]*\baccordion-navigation\b[^"]*"[^>]*>\s*<a\b[^>]*>\s*([^<]+?)\s*</a>`)
	marketCalendarRowPattern    = regexp.MustCompile(`(?is)<tr\b[^>]*>(.*?)</tr>`)
	marketCalendarCellPattern   = regexp.MustCompile(`(?is)<td\b[^>]*>(.*?)</td>`)
	marketCalendarTagPattern    = regexp.MustCompile(`(?is)<[^>]*>`)
	marketCalendarStartPattern  = regexp.MustCompile(`(?i)trading and registration will start at\s+(\d{1,2}):(\d{2})\s*([ap])\.?m\.?`)
)

var marketCalendarMonths = map[string]time.Month{
	"january":   time.January,
	"february":  time.February,
	"march":     time.March,
	"april":     time.April,
	"may":       time.May,
	"june":      time.June,
	"july":      time.July,
	"august":    time.August,
	"september": time.September,
	"october":   time.October,
	"november":  time.November,
	"december":  time.December,
}

// MarketCalendarRequest requires an explicit year because the B3 page is a
// multi-year presentation and the adapter must not silently select "latest".
type MarketCalendarRequest struct {
	Year int
}

// MarketCalendarResult is source-native B3 calendar evidence. It records
// listed-market closure and special-hours notices, but does not synthesize
// canonical TradingSession times from a page that mixes market segments.
type MarketCalendarResult struct {
	providers.ResourceResult
	Year   int
	Events []MarketCalendarEvent
	Stats  MarketCalendarParseStats
}

type MarketCalendarParseStats struct {
	RowsReceived int
	RowsRejected int
	Duplicates   int
	ListedRows   int
}

type MarketCalendarEvent struct {
	Date              time.Time
	Event             string
	Status            string
	IsClosed          bool
	IsSpecialHours    bool
	SpecialOpenLocal  string
	ListedDescription string
	RawRecordLocator  string
}

func (c *Client) CollectMarketCalendar(ctx context.Context, request MarketCalendarRequest) (MarketCalendarResult, error) {
	if c == nil || c.http == nil {
		return MarketCalendarResult{}, errors.New("B3 client HTTP getter is required")
	}
	normalized, err := normalizeMarketCalendarRequest(request)
	if err != nil {
		return MarketCalendarResult{}, fmt.Errorf("B3 market-calendar request: %w", err)
	}
	requestURL, err := c.marketCalendarURL()
	if err != nil {
		return MarketCalendarResult{}, err
	}
	body, err := c.http.Get(ctx, requestURL)
	if err != nil {
		return MarketCalendarResult{}, fmt.Errorf("B3 market-calendar request: %w", err)
	}
	fetchedAt := canonicalTime(c.now())
	resource := providers.NewRawResource(
		MarketCalendarResourceKind,
		fmt.Sprintf("market-calendar-%04d.html", normalized.Year),
		body,
		fetchedAt,
		"text/html; charset=utf-8",
	)
	resource.URL = requestURL
	resource.ParserVersion = MarketCalendarParserVersion
	resource.ParserMetadata = map[string]string{
		"year":                strconv.Itoa(normalized.Year),
		"source_language":     "en-US",
		"listed_market_scope": "B3 Listed",
		"source_semantics":    "B3 public market-calendar page; date-specific notices only, no synthesized session hours",
		"request_url":         requestURL,
	}
	result := MarketCalendarResult{
		ResourceResult: providers.ResourceResult{Resources: []providers.RawResource{resource}},
		Year:           normalized.Year,
	}
	result.Events, result.Stats, err = parseMarketCalendar(body, normalized.Year)
	if err != nil {
		return result, fmt.Errorf("B3 market-calendar: %w", err)
	}
	resource.ParserMetadata["listed_rows"] = strconv.Itoa(result.Stats.ListedRows)
	resource.ParserMetadata["closed_rows"] = strconv.Itoa(countMarketCalendarStatus(result.Events, "closed"))
	resource.ParserMetadata["special_hours_rows"] = strconv.Itoa(countMarketCalendarStatus(result.Events, "special_hours"))
	return result, nil
}

func (c *Client) marketCalendarURL() (string, error) {
	base := strings.TrimSpace(c.marketCalendarBaseURL)
	if base == "" {
		base = DefaultMarketCalendarURL
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid B3 market-calendar URL %q", base)
	}
	return parsed.String(), nil
}

func normalizeMarketCalendarRequest(request MarketCalendarRequest) (MarketCalendarRequest, error) {
	if request.Year < 2000 || request.Year > 2100 {
		return MarketCalendarRequest{}, fmt.Errorf("year %d must be between 2000 and 2100", request.Year)
	}
	return request, nil
}

func parseMarketCalendar(body []byte, year int) ([]MarketCalendarEvent, MarketCalendarParseStats, error) {
	source := string(body)
	headers := marketCalendarHeaderPattern.FindAllStringSubmatchIndex(source, -1)
	var section string
	for index, header := range headers {
		if source[header[2]:header[3]] == strconv.Itoa(year) {
			sectionStart := header[1]
			sectionEnd := len(source)
			if index+1 < len(headers) {
				sectionEnd = headers[index+1][0]
			}
			section = source[sectionStart:sectionEnd]
			break
		}
	}
	if section == "" {
		return nil, MarketCalendarParseStats{}, fmt.Errorf("market-calendar year %d was not found", year)
	}

	monthMatches := marketCalendarMonthPattern.FindAllStringSubmatchIndex(section, -1)
	if len(monthMatches) == 0 {
		return nil, MarketCalendarParseStats{}, errors.New("market-calendar has no month sections")
	}
	events := make([]MarketCalendarEvent, 0)
	stats := MarketCalendarParseStats{}
	seen := make(map[string]struct{})
	rowNumber := 0
	for index, match := range monthMatches {
		monthName := strings.ToLower(strings.TrimSpace(section[match[2]:match[3]]))
		month, exists := marketCalendarMonths[monthName]
		if !exists {
			continue
		}
		blockStart := match[1]
		blockEnd := len(section)
		if index+1 < len(monthMatches) {
			blockEnd = monthMatches[index+1][0]
		}
		rows := marketCalendarRowPattern.FindAllStringSubmatch(section[blockStart:blockEnd], -1)
		for _, rowMatch := range rows {
			cells := marketCalendarCellPattern.FindAllStringSubmatch(rowMatch[1], -1)
			if len(cells) == 0 {
				continue
			}
			rowNumber++
			stats.RowsReceived++
			if len(cells) < 2 {
				stats.RowsRejected++
				continue
			}
			dayText := cleanMarketCalendarHTML(cells[0][1])
			day, parseErr := strconv.Atoi(dayText)
			if parseErr != nil || day < 1 || day > 31 {
				stats.RowsRejected++
				continue
			}
			date := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
			if date.Year() != year || date.Month() != month || date.Day() != day {
				stats.RowsRejected++
				continue
			}
			rowText := cleanMarketCalendarHTML(rowMatch[1])
			lowerRowText := strings.ToLower(rowText)
			if !strings.Contains(lowerRowText, "listed b3") && !strings.Contains(lowerRowText, "b3 listed") {
				continue
			}
			stats.ListedRows++
			eventName := cleanMarketCalendarHTML(cells[1][1])
			closed := strings.Contains(lowerRowText, "there will be no trading on the equity")
			early := strings.Contains(lowerRowText, "special trading hours") || strings.Contains(lowerRowText, "trading and registration will start at")
			status := "listed_notice"
			if closed {
				status = "closed"
			} else if early {
				status = "special_hours"
			}
			identity := fmt.Sprintf("%s|%s|%s", date.Format(time.DateOnly), status, eventName)
			if _, duplicate := seen[identity]; duplicate {
				stats.Duplicates++
				continue
			}
			seen[identity] = struct{}{}
			description := rowText
			if len(cells) >= 4 {
				description = cleanMarketCalendarHTML(cells[3][1])
			}
			specialOpen := ""
			if early {
				specialOpen = parseMarketCalendarStart(description)
			}
			events = append(events, MarketCalendarEvent{
				Date:              date,
				Event:             eventName,
				Status:            status,
				IsClosed:          closed,
				IsSpecialHours:    early,
				SpecialOpenLocal:  specialOpen,
				ListedDescription: description,
				RawRecordLocator:  fmt.Sprintf("market_calendar/year=%d/month=%s/day=%02d/row=%d", year, monthName, day, rowNumber),
			})
		}
	}
	if len(events) == 0 {
		return nil, stats, fmt.Errorf("market-calendar year %d has no listed-market events", year)
	}
	sort.Slice(events, func(i, j int) bool {
		if !events[i].Date.Equal(events[j].Date) {
			return events[i].Date.Before(events[j].Date)
		}
		return events[i].Event < events[j].Event
	})
	return events, stats, nil
}

func parseMarketCalendarStart(description string) string {
	match := marketCalendarStartPattern.FindStringSubmatch(description)
	if len(match) != 4 {
		return ""
	}
	hour, err := strconv.Atoi(match[1])
	if err != nil || hour < 1 || hour > 12 {
		return ""
	}
	if strings.EqualFold(match[3], "p") && hour != 12 {
		hour += 12
	}
	if strings.EqualFold(match[3], "a") && hour == 12 {
		hour = 0
	}
	return fmt.Sprintf("%02d:%s", hour, match[2])
}

func cleanMarketCalendarHTML(value string) string {
	value = marketCalendarTagPattern.ReplaceAllString(value, " ")
	value = html.UnescapeString(value)
	return strings.Join(strings.Fields(value), " ")
}

func countMarketCalendarStatus(events []MarketCalendarEvent, status string) int {
	count := 0
	for _, event := range events {
		if event.Status == status {
			count++
		}
	}
	return count
}
