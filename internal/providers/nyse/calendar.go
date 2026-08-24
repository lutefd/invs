// Package nyse provides a raw-first adapter for NYSE's public holiday and
// core-trading-hours page.
package nyse

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
	DefaultCalendarURL    = "https://www.nyse.com/trade/hours-calendars"
	CalendarParserVersion = "nyse-holidays-hours-v1"
	CalendarResourceKind  = "market_calendar"
)

var (
	tablePattern         = regexp.MustCompile(`(?is)<table\b[^>]*>(.*?)</table>`)
	rowPattern           = regexp.MustCompile(`(?is)<tr\b[^>]*>(.*?)</tr>`)
	cellPattern          = regexp.MustCompile(`(?is)<t[dh]\b[^>]*>(.*?)</t[dh]>`)
	paragraphPattern     = regexp.MustCompile(`(?is)<p\b[^>]*>(.*?)</p>`)
	tagPattern           = regexp.MustCompile(`(?is)<[^>]*>`)
	monthDayPattern      = regexp.MustCompile(`(?i)\b(January|February|March|April|May|June|July|August|September|October|November|December)\s+(\d{1,2})\b`)
	fullDatePattern      = regexp.MustCompile(`(?i)\b(?:Monday|Tuesday|Wednesday|Thursday|Friday|Saturday|Sunday),\s+(January|February|March|April|May|June|July|August|September|October|November|December)\s+(\d{1,2}),\s+(\d{4})\b`)
	coreHoursPattern     = regexp.MustCompile(`(?i)Core Trading Session:\s*(\d{1,2}):(\d{2})\s*([ap])\.?m\.?\s+to\s+(\d{1,2}):(\d{2})\s*([ap])\.?m\.?\s+ET`)
	calendarMonthNumbers = map[string]time.Month{
		"january": time.January, "february": time.February, "march": time.March,
		"april": time.April, "may": time.May, "june": time.June,
		"july": time.July, "august": time.August, "september": time.September,
		"october": time.October, "november": time.November, "december": time.December,
	}
)

type Getter interface {
	Get(context.Context, string) ([]byte, error)
}

type Client struct {
	http        Getter
	calendarURL string
	now         func() time.Time
}

func NewClient(getter Getter) *Client {
	return &Client{http: getter, calendarURL: DefaultCalendarURL, now: time.Now}
}

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
		return CalendarResult{}, errors.New("NYSE client HTTP getter is required")
	}
	if request.Year < 2000 || request.Year > 2100 {
		return CalendarResult{}, fmt.Errorf("NYSE calendar year %d must be between 2000 and 2100", request.Year)
	}
	requestURL, err := normalizeCalendarURL(c.calendarURL)
	if err != nil {
		return CalendarResult{}, err
	}
	body, err := c.http.Get(ctx, requestURL)
	if err != nil {
		return CalendarResult{}, fmt.Errorf("NYSE calendar request: %w", err)
	}
	fetchedAt := c.now().UTC().Truncate(time.Microsecond)
	resource := providers.NewRawResource(CalendarResourceKind, fmt.Sprintf("nyse-calendar-%d.html", request.Year), body, fetchedAt, "text/html; charset=utf-8")
	resource.URL = requestURL
	resource.ParserVersion = CalendarParserVersion
	resource.ParserMetadata = map[string]string{
		"year":             strconv.Itoa(request.Year),
		"mic":              "XNYS",
		"timezone":         "America/New_York",
		"session_scope":    "NYSE core trading session",
		"source_semantics": "current published holiday/hours page; receipt-time availability and no historical revision chain",
		"request_url":      requestURL,
	}
	result := CalendarResult{ResourceResult: providers.ResourceResult{Resources: []providers.RawResource{resource}}, Year: request.Year}
	result.CoreHours, result.Events, err = parseCalendar(body, request.Year)
	if err != nil {
		return result, fmt.Errorf("NYSE calendar: %w", err)
	}
	resource.ParserMetadata["open_local"] = result.CoreHours.OpenLocal
	resource.ParserMetadata["close_local"] = result.CoreHours.CloseLocal
	resource.ParserMetadata["closed_dates"] = strconv.Itoa(countStatus(result.Events, "closed"))
	resource.ParserMetadata["early_close_dates"] = strconv.Itoa(countStatus(result.Events, "early_close"))
	return result, nil
}

func normalizeCalendarURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid NYSE calendar URL %q", value)
	}
	return parsed.String(), nil
}

func parseCalendar(body []byte, year int) (CoreTradingHours, []CalendarEvent, error) {
	source := string(body)
	hours, err := parseCoreHours(cleanHTML(source))
	if err != nil {
		return CoreTradingHours{}, nil, err
	}
	closed, err := parseHolidayTable(source, year)
	if err != nil {
		return CoreTradingHours{}, nil, err
	}
	events := append([]CalendarEvent(nil), closed...)
	seen := make(map[string]struct{}, len(events))
	for _, event := range events {
		seen[event.Date.Format(time.DateOnly)+"|"+event.Status] = struct{}{}
	}
	for _, paragraph := range paragraphPattern.FindAllStringSubmatch(source, -1) {
		text := cleanHTML(paragraph[1])
		if !strings.Contains(strings.ToLower(text), "close early at 1:00 p.m.") {
			continue
		}
		for _, match := range fullDatePattern.FindAllStringSubmatch(text, -1) {
			matchedYear, _ := strconv.Atoi(match[3])
			if matchedYear != year {
				continue
			}
			date, dateErr := calendarDate(year, match[1], match[2])
			if dateErr != nil {
				return CoreTradingHours{}, nil, dateErr
			}
			key := date.Format(time.DateOnly) + "|early_close"
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			events = append(events, CalendarEvent{
				Date:              date,
				Name:              "NYSE published early close",
				Status:            "early_close",
				SpecialCloseLocal: "13:00",
				RawRecordLocator:  "hours-calendars/year=" + strconv.Itoa(year) + "/early-close/date=" + date.Format(time.DateOnly),
			})
		}
	}
	sort.Slice(events, func(i, j int) bool {
		if !events[i].Date.Equal(events[j].Date) {
			return events[i].Date.Before(events[j].Date)
		}
		return events[i].Status < events[j].Status
	})
	return hours, events, nil
}

func parseCoreHours(text string) (CoreTradingHours, error) {
	match := coreHoursPattern.FindStringSubmatch(text)
	if len(match) != 7 {
		return CoreTradingHours{}, errors.New("NYSE core trading hours were not found")
	}
	openLocal, err := meridiemTime(match[1], match[2], match[3])
	if err != nil {
		return CoreTradingHours{}, err
	}
	closeLocal, err := meridiemTime(match[4], match[5], match[6])
	if err != nil {
		return CoreTradingHours{}, err
	}
	if openLocal >= closeLocal {
		return CoreTradingHours{}, fmt.Errorf("NYSE core trading hours are not increasing: %s-%s", openLocal, closeLocal)
	}
	return CoreTradingHours{OpenLocal: openLocal, CloseLocal: closeLocal}, nil
}

func parseHolidayTable(source string, year int) ([]CalendarEvent, error) {
	yearText := strconv.Itoa(year)
	for _, table := range tablePattern.FindAllStringSubmatch(source, -1) {
		rows := rowPattern.FindAllStringSubmatch(table[1], -1)
		if len(rows) < 2 {
			continue
		}
		headings := cellTexts(rows[0][1])
		if len(headings) < 2 || headings[0] != "Holiday" {
			continue
		}
		yearColumn := -1
		for index, heading := range headings {
			if heading == yearText {
				yearColumn = index
				break
			}
		}
		if yearColumn < 1 {
			continue
		}
		events := make([]CalendarEvent, 0, len(rows)-1)
		for rowIndex, row := range rows[1:] {
			cells := cellTexts(row[1])
			if len(cells) <= yearColumn {
				return nil, fmt.Errorf("NYSE holiday row %d has %d cells, need year column %d", rowIndex+1, len(cells), yearColumn)
			}
			match := monthDayPattern.FindStringSubmatch(cells[yearColumn])
			if len(match) != 3 {
				return nil, fmt.Errorf("NYSE holiday %q has no date for %d", cells[0], year)
			}
			date, err := calendarDate(year, match[1], match[2])
			if err != nil {
				return nil, fmt.Errorf("NYSE holiday %q: %w", cells[0], err)
			}
			events = append(events, CalendarEvent{
				Date:             date,
				Name:             cells[0],
				Status:           "closed",
				RawRecordLocator: fmt.Sprintf("hours-calendars/year=%d/holiday-row=%d/date=%s", year, rowIndex+1, date.Format(time.DateOnly)),
			})
		}
		if len(events) == 0 {
			return nil, fmt.Errorf("NYSE holiday table has no rows for %d", year)
		}
		return events, nil
	}
	return nil, fmt.Errorf("NYSE holiday table for %d was not found", year)
}

func cellTexts(row string) []string {
	matches := cellPattern.FindAllStringSubmatch(row, -1)
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		result = append(result, cleanHTML(match[1]))
	}
	return result
}

func cleanHTML(value string) string {
	value = tagPattern.ReplaceAllString(value, " ")
	value = html.UnescapeString(value)
	return strings.Join(strings.Fields(value), " ")
}

func calendarDate(year int, monthText, dayText string) (time.Time, error) {
	month, ok := calendarMonthNumbers[strings.ToLower(monthText)]
	day, err := strconv.Atoi(dayText)
	if !ok || err != nil || day < 1 || day > 31 {
		return time.Time{}, fmt.Errorf("invalid calendar date %s %s", monthText, dayText)
	}
	date := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	if date.Year() != year || date.Month() != month || date.Day() != day {
		return time.Time{}, fmt.Errorf("invalid calendar date %s %s", monthText, dayText)
	}
	return date, nil
}

func meridiemTime(hourText, minute, marker string) (string, error) {
	hour, err := strconv.Atoi(hourText)
	if err != nil || hour < 1 || hour > 12 {
		return "", fmt.Errorf("invalid hour %q", hourText)
	}
	if strings.EqualFold(marker, "p") && hour != 12 {
		hour += 12
	}
	if strings.EqualFold(marker, "a") && hour == 12 {
		hour = 0
	}
	return fmt.Sprintf("%02d:%s", hour, minute), nil
}

func countStatus(events []CalendarEvent, status string) int {
	count := 0
	for _, event := range events {
		if event.Status == status {
			count++
		}
	}
	return count
}
