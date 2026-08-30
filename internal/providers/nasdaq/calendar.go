package nasdaq

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
	DefaultCalendarURL    = "https://www.nasdaqtrader.com/Trader.aspx?id=Calendar"
	CalendarParserVersion = "nasdaq-holidays-v1"
	CalendarResourceKind  = "market_calendar"
	nasdaqRegularOpen     = "09:30"
	nasdaqRegularClose    = "16:00"
)

var (
	nasdaqCalendarTablePattern = regexp.MustCompile(`(?is)<table\b[^>]*>(.*?)</table>`)
	nasdaqCalendarRowPattern   = regexp.MustCompile(`(?is)<tr\b[^>]*>(.*?)</tr>`)
	nasdaqCalendarCellPattern  = regexp.MustCompile(`(?is)<t[dh]\b[^>]*>(.*?)</t[dh]>`)
	nasdaqEarlyClosePattern    = regexp.MustCompile(`(?i)^1:00\s*p\.?m\.?$`)
)

type CalendarRequest struct {
	Year int
}

type CalendarEvent struct {
	Date              time.Time
	Name              string
	Status            string
	SpecialCloseLocal string
	RawRecordLocator  string
}

type CoreTradingHours struct {
	OpenLocal  string
	CloseLocal string
}

type CalendarResult struct {
	providers.ResourceResult
	Year      int
	CoreHours CoreTradingHours
	Events    []CalendarEvent
}

func (c *Client) CollectCalendar(ctx context.Context, request CalendarRequest) (CalendarResult, error) {
	if c == nil || c.http == nil {
		return CalendarResult{}, errors.New("Nasdaq calendar client HTTP getter is required")
	}
	if request.Year < 2000 || request.Year > 2100 {
		return CalendarResult{}, fmt.Errorf("Nasdaq calendar year %d must be between 2000 and 2100", request.Year)
	}
	requestURL, err := normalizeCalendarURL(c.calendarURL)
	if err != nil {
		return CalendarResult{}, err
	}
	body, err := c.http.Get(ctx, requestURL)
	if err != nil {
		return CalendarResult{}, fmt.Errorf("Nasdaq calendar request: %w", err)
	}
	fetchedAt := c.now().UTC().Truncate(time.Microsecond)
	resource := providers.NewRawResource(CalendarResourceKind, fmt.Sprintf("nasdaq-calendar-%d.html", request.Year), body, fetchedAt, "text/html; charset=utf-8")
	resource.Year = request.Year
	resource.URL = requestURL
	resource.ParserVersion = CalendarParserVersion
	resource.ParserMetadata = map[string]string{
		"source_owner":     "Nasdaq, Inc.",
		"source_transport": "Nasdaq Trader current holiday schedule",
		"mic":              "XNAS",
		"timezone":         "America/New_York",
		"session_scope":    "Nasdaq Stock Market regular equity session",
		"source_semantics": "current published holiday schedule; receipt-time availability and no historical revision chain",
		"request_url":      requestURL,
	}
	result := CalendarResult{ResourceResult: providers.ResourceResult{Resources: []providers.RawResource{resource}}, Year: request.Year}
	result.CoreHours, result.Events, err = parseCalendar(body, request.Year)
	if err != nil {
		return result, fmt.Errorf("Nasdaq calendar: %w", err)
	}
	resource.ParserMetadata["open_local"] = result.CoreHours.OpenLocal
	resource.ParserMetadata["close_local"] = result.CoreHours.CloseLocal
	resource.ParserMetadata["closed_dates"] = strconv.Itoa(countCalendarStatus(result.Events, "closed"))
	resource.ParserMetadata["early_close_dates"] = strconv.Itoa(countCalendarStatus(result.Events, "early_close"))
	return result, nil
}

func normalizeCalendarURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Port() != "" {
		return "", fmt.Errorf("Nasdaq calendar URL %q must be an HTTPS Nasdaq Trader calendar page", value)
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "www.nasdaqtrader.com" && host != "nasdaqtrader.com" {
		return "", fmt.Errorf("Nasdaq calendar URL %q must use the Nasdaq Trader host", value)
	}
	if parsed.Path != "/Trader.aspx" || parsed.RawQuery != "id=Calendar" || parsed.Fragment != "" {
		return "", fmt.Errorf("Nasdaq calendar URL %q must be the canonical Trader.aspx?id=Calendar page", value)
	}
	return parsed.String(), nil
}

func parseCalendar(body []byte, year int) (CoreTradingHours, []CalendarEvent, error) {
	text := cleanNasdaqCalendarHTML(string(body))
	if !strings.Contains(text, fmt.Sprintf("U.S. Equity and Options Markets Holiday Schedule %d", year)) {
		return CoreTradingHours{}, nil, fmt.Errorf("holiday schedule heading for %d was not found", year)
	}
	events, err := parseHolidayTable(body, year)
	if err != nil {
		return CoreTradingHours{}, nil, err
	}
	// Nasdaq Trader's holiday page publishes exception rows, not the regular
	// equity-session hours. The stable XNAS core session is retained explicitly
	// in the adapter contract and the exact source page remains raw evidence.
	return CoreTradingHours{OpenLocal: nasdaqRegularOpen, CloseLocal: nasdaqRegularClose}, events, nil
}

func parseHolidayTable(body []byte, year int) ([]CalendarEvent, error) {
	yearText := strconv.Itoa(year)
	for _, table := range nasdaqCalendarTablePattern.FindAllStringSubmatch(string(body), -1) {
		rows := nasdaqCalendarRowPattern.FindAllStringSubmatch(table[1], -1)
		if len(rows) < 2 {
			continue
		}
		headings := nasdaqCalendarCellTexts(rows[0][1])
		yearColumn, holidayColumn, statusColumn := -1, -1, -1
		for index, heading := range headings {
			switch strings.ToLower(strings.TrimSpace(heading)) {
			case yearText:
				yearColumn = index
			case "holiday":
				holidayColumn = index
			case "status":
				statusColumn = index
			}
		}
		if yearColumn < 0 || holidayColumn < 0 || statusColumn < 0 {
			continue
		}
		events := make([]CalendarEvent, 0, len(rows)-1)
		seenDates := make(map[string]struct{}, len(rows)-1)
		maxColumn := yearColumn
		if holidayColumn > maxColumn {
			maxColumn = holidayColumn
		}
		if statusColumn > maxColumn {
			maxColumn = statusColumn
		}
		for rowIndex, row := range rows[1:] {
			cells := nasdaqCalendarCellTexts(row[1])
			if len(cells) <= maxColumn {
				return nil, fmt.Errorf("Nasdaq holiday row %d has %d cells, need column %d", rowIndex+1, len(cells), maxColumn)
			}
			date, err := time.Parse("January 2, 2006", cells[yearColumn])
			if err != nil || date.Year() != year {
				return nil, fmt.Errorf("Nasdaq holiday %q has no valid date for %d", cells[holidayColumn], year)
			}
			name := strings.TrimSpace(cells[holidayColumn])
			if name == "" {
				return nil, fmt.Errorf("Nasdaq holiday row %d has an empty name", rowIndex+1)
			}
			dateKey := date.Format(time.DateOnly)
			if _, duplicate := seenDates[dateKey]; duplicate {
				return nil, fmt.Errorf("Nasdaq holiday table repeats %s", dateKey)
			}
			seenDates[dateKey] = struct{}{}
			status, specialClose, err := parseHolidayStatus(cells[statusColumn])
			if err != nil {
				return nil, fmt.Errorf("Nasdaq holiday %q: %w", name, err)
			}
			events = append(events, CalendarEvent{
				Date: date, Name: name, Status: status, SpecialCloseLocal: specialClose,
				RawRecordLocator: fmt.Sprintf("Trader.aspx?id=Calendar/year=%d/holiday-row=%d/date=%s", year, rowIndex+1, dateKey),
			})
		}
		if len(events) == 0 {
			return nil, fmt.Errorf("Nasdaq holiday table has no rows for %d", year)
		}
		sort.Slice(events, func(i, j int) bool { return events[i].Date.Before(events[j].Date) })
		return events, nil
	}
	return nil, fmt.Errorf("Nasdaq holiday table for %d was not found", year)
}

func parseHolidayStatus(value string) (string, string, error) {
	status := strings.TrimSpace(value)
	switch strings.ToLower(status) {
	case "closed":
		return "closed", "", nil
	case "1:00 p.m.", "1:00 p.m", "1:00 pm", "1:00pm":
		return "early_close", "13:00", nil
	default:
		if nasdaqEarlyClosePattern.MatchString(status) {
			return "early_close", "13:00", nil
		}
		return "", "", fmt.Errorf("unsupported status %q", status)
	}
}

func nasdaqCalendarCellTexts(row string) []string {
	matches := nasdaqCalendarCellPattern.FindAllStringSubmatch(row, -1)
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		result = append(result, cleanNasdaqCalendarText(match[1]))
	}
	return result
}

func cleanNasdaqCalendarHTML(value string) string {
	return cleanNasdaqCalendarText(value)
}

func cleanNasdaqCalendarText(value string) string {
	value = nasdaqTagPattern.ReplaceAllString(value, " ")
	value = html.UnescapeString(value)
	return strings.Join(strings.Fields(value), " ")
}

func countCalendarStatus(events []CalendarEvent, status string) int {
	count := 0
	for _, event := range events {
		if event.Status == status {
			count++
		}
	}
	return count
}
