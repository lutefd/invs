package b3

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/luisdourado/invs/internal/providers"
)

const (
	IndexMembershipParserVersion = "b3-index-membership-notice-v1"
	IndexMembershipResourceKind  = "index_membership_notice"
)

var (
	b3NoticeNumericDatePattern = regexp.MustCompile(`\b(\d{1,2})/(\d{1,2})/(\d{4})\b`)
	b3NoticeLongDatePattern    = regexp.MustCompile(`(?i)\b(\d{1,2})\s*(?:º)?\s+de\s+(janeiro|fevereiro|março|marco|abril|maio|junho|julho|agosto|setembro|outubro|novembro|dezembro)\s+de\s+(\d{4})\b`)
	b3NoticeTickerPattern      = regexp.MustCompile(`\(([A-Z][A-Z0-9._-]{1,15})\)`)
)

var b3NoticeMonths = map[string]time.Month{
	"janeiro": time.January, "fevereiro": time.February, "março": time.March,
	"marco": time.March, "abril": time.April, "maio": time.May,
	"junho": time.June, "julho": time.July, "agosto": time.August,
	"setembro": time.September, "outubro": time.October,
	"novembro": time.November, "dezembro": time.December,
}

type IndexMembershipRequest struct {
	URL string
}

type IndexMembershipEvent struct {
	Ticker           string
	Member           bool
	EffectiveAt      time.Time
	AnnouncedAt      time.Time
	AvailableAt      time.Time
	CoverageEnd      time.Time
	RawRecordLocator string
}

type IndexMembershipResult struct {
	providers.ResourceResult
	Events []IndexMembershipEvent
}

func (c *Client) CollectIndexMembership(ctx context.Context, request IndexMembershipRequest) (IndexMembershipResult, error) {
	if c == nil || c.http == nil {
		return IndexMembershipResult{}, errors.New("B3 index-membership client HTTP getter is required")
	}
	requestURL, err := normalizeIndexMembershipURL(request.URL)
	if err != nil {
		return IndexMembershipResult{}, err
	}
	body, err := c.http.Get(ctx, requestURL)
	if err != nil {
		return IndexMembershipResult{}, fmt.Errorf("B3 index-membership request: %w", err)
	}
	fetchedAt := canonicalTime(c.now())
	resource := providers.NewRawResource(IndexMembershipResourceKind, indexMembershipResourceKey(requestURL), body, fetchedAt, "text/html; charset=utf-8")
	resource.URL = requestURL
	resource.ParserVersion = IndexMembershipParserVersion
	resource.ParserMetadata = map[string]string{
		"source_owner":     "B3 S.A. - Brasil, Bolsa, Balcao",
		"source_transport": "B3 official index portfolio notice",
		"universe":         "Ibovespa B3",
		"request_url":      requestURL,
	}
	result := IndexMembershipResult{ResourceResult: providers.ResourceResult{Resources: []providers.RawResource{resource}}}
	result.Events, err = parseIndexMembership(body)
	if err != nil {
		return result, fmt.Errorf("B3 index-membership notice: %w", err)
	}
	resource.ParserMetadata["events"] = strconv.Itoa(len(result.Events))
	resource.ParserMetadata["effective_at"] = result.Events[0].EffectiveAt.Format(time.RFC3339)
	resource.ParserMetadata["available_at"] = result.Events[0].AvailableAt.Format(time.RFC3339)
	resource.ParserMetadata["coverage_end"] = result.Events[0].CoverageEnd.Format(time.DateOnly)
	return result, nil
}

func normalizeIndexMembershipURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host != "www.b3.com.br" {
		return "", fmt.Errorf("B3 index-membership URL %q must use the official HTTPS host", value)
	}
	if !strings.HasPrefix(parsed.Path, "/pt_br/noticias/") {
		return "", fmt.Errorf("B3 index-membership URL %q must use the official news path", value)
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func indexMembershipResourceKey(requestURL string) string {
	parsed, _ := url.Parse(requestURL)
	name := strings.TrimSuffix(strings.TrimPrefix(parsed.Path, "/pt_br/noticias/"), ".htm")
	if name == "" {
		return "b3-index-membership.html"
	}
	return name + ".html"
}

func parseIndexMembership(body []byte) ([]IndexMembershipEvent, error) {
	text := cleanMarketCalendarHTML(string(body))
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "b3") || !strings.Contains(lower, "ibovespa") {
		return nil, errors.New("notice is not an official B3 Ibovespa portfolio notice")
	}
	location, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return nil, err
	}
	announcedDate, err := firstB3NoticeDate(text, location)
	if err != nil {
		return nil, fmt.Errorf("announcement date: %w", err)
	}
	effective, coverageEnd, err := b3NoticeCoverage(text, location)
	if err != nil {
		return nil, err
	}
	added, removed, err := b3NoticeChanges(text)
	if err != nil {
		return nil, err
	}
	// B3 exposes only a publication date on these pages. Eligibility begins at
	// the next local midnight so an unknown intraday release time is never guessed.
	availableAt := announcedDate.AddDate(0, 0, 1).UTC()
	effectiveAt := effective.UTC()
	announcedAt := announcedDate.UTC()
	events := make([]IndexMembershipEvent, 0, len(added)+len(removed))
	seen := make(map[string]bool, len(added)+len(removed))
	appendEvents := func(tickers []string, member bool, kind string) error {
		for _, ticker := range tickers {
			if previous, exists := seen[ticker]; exists && previous != member {
				return fmt.Errorf("ticker %s is both added and removed", ticker)
			}
			if _, exists := seen[ticker]; exists {
				continue
			}
			seen[ticker] = member
			events = append(events, IndexMembershipEvent{
				Ticker: ticker, Member: member, EffectiveAt: effectiveAt,
				AnnouncedAt: announcedAt, AvailableAt: availableAt,
				CoverageEnd:      coverageEnd.UTC(),
				RawRecordLocator: "ibovespa/effective=" + effective.Format(time.DateOnly) + "/" + kind + "/ticker=" + ticker,
			})
		}
		return nil
	}
	if err := appendEvents(added, true, "addition"); err != nil {
		return nil, err
	}
	if err := appendEvents(removed, false, "removal"); err != nil {
		return nil, err
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].Ticker != events[j].Ticker {
			return events[i].Ticker < events[j].Ticker
		}
		return events[i].Member && !events[j].Member
	})
	return events, nil
}

func firstB3NoticeDate(text string, location *time.Location) (time.Time, error) {
	if match := b3NoticeNumericDatePattern.FindStringSubmatch(text); len(match) == 4 {
		return b3NumericDate(match, location)
	}
	if match := b3NoticeLongDatePattern.FindStringSubmatch(text); len(match) == 4 {
		return b3LongDate(match, location)
	}
	return time.Time{}, errors.New("no publication date")
}

func b3NoticeCoverage(text string, location *time.Location) (time.Time, time.Time, error) {
	lower := strings.ToLower(text)
	start := strings.Index(lower, "vai vigorar de ")
	if start < 0 {
		return time.Time{}, time.Time{}, errors.New("notice has no portfolio coverage interval")
	}
	coverage := text[start+len("vai vigorar de "):]
	end := strings.Index(strings.ToLower(coverage), ", com base")
	if end < 0 {
		return time.Time{}, time.Time{}, errors.New("notice coverage interval has no source boundary")
	}
	coverage = strings.TrimSpace(coverage[:end])
	parts := regexp.MustCompile(`(?i)\s+a\s+`).Split(coverage, 2)
	if len(parts) != 2 {
		return time.Time{}, time.Time{}, errors.New("notice coverage interval is malformed")
	}
	from, err := parseB3NoticeDate(parts[0], location)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("coverage start: %w", err)
	}
	through, err := parseB3NoticeDate(parts[1], location)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("coverage end: %w", err)
	}
	if through.Before(from) {
		return time.Time{}, time.Time{}, errors.New("notice coverage ends before it starts")
	}
	return from, through.AddDate(0, 0, 1), nil
}

func parseB3NoticeDate(value string, location *time.Location) (time.Time, error) {
	value = strings.TrimSpace(value)
	if match := b3NoticeNumericDatePattern.FindStringSubmatch(value); len(match) == 4 {
		return b3NumericDate(match, location)
	}
	if match := b3NoticeLongDatePattern.FindStringSubmatch(value); len(match) == 4 {
		return b3LongDate(match, location)
	}
	return time.Time{}, fmt.Errorf("unsupported date %q", value)
}

func b3NumericDate(match []string, location *time.Location) (time.Time, error) {
	day, _ := strconv.Atoi(match[1])
	month, _ := strconv.Atoi(match[2])
	year, _ := strconv.Atoi(match[3])
	return validatedB3Date(year, time.Month(month), day, location)
}

func b3LongDate(match []string, location *time.Location) (time.Time, error) {
	day, _ := strconv.Atoi(match[1])
	year, _ := strconv.Atoi(match[3])
	month, exists := b3NoticeMonths[strings.ToLower(match[2])]
	if !exists {
		return time.Time{}, fmt.Errorf("unsupported month %q", match[2])
	}
	return validatedB3Date(year, month, day, location)
}

func validatedB3Date(year int, month time.Month, day int, location *time.Location) (time.Time, error) {
	value := time.Date(year, month, day, 0, 0, 0, 0, location)
	if value.Year() != year || value.Month() != month || value.Day() != day {
		return time.Time{}, fmt.Errorf("invalid date %04d-%02d-%02d", year, month, day)
	}
	return value, nil
}

func b3NoticeChanges(text string) ([]string, []string, error) {
	lower := strings.ToLower(text)
	marker := "registra a entrada"
	start := strings.Index(lower, marker)
	if start < 0 {
		return nil, nil, errors.New("notice has no definitive entry section")
	}
	changeText := text[start+len(marker):]
	changeText = strings.TrimSpace(changeText)
	for _, prefix := range []string{"das empresas ", "da ", "de "} {
		if strings.HasPrefix(strings.ToLower(changeText), prefix) {
			changeText = changeText[len(prefix):]
			break
		}
	}
	lowerChange := strings.ToLower(changeText)
	exitAt := strings.Index(lowerChange, "e a saída")
	entryEnd := strings.Index(lowerChange, ", totalizando")
	if entryEnd < 0 {
		entryEnd = strings.Index(lowerChange, ".")
	}
	var addedText, removedText string
	if exitAt >= 0 && (entryEnd < 0 || exitAt < entryEnd) {
		addedText = changeText[:exitAt]
		removedText = strings.TrimSpace(changeText[exitAt+len("e a saída"):])
		for _, prefix := range []string{"das empresas ", "da ", "de "} {
			if strings.HasPrefix(strings.ToLower(removedText), prefix) {
				removedText = removedText[len(prefix):]
				break
			}
		}
		if stop := strings.Index(removedText, "."); stop >= 0 {
			removedText = removedText[:stop]
		}
	} else {
		if entryEnd < 0 {
			return nil, nil, errors.New("notice entry section has no source boundary")
		}
		addedText = changeText[:entryEnd]
	}
	added := b3SectionTickers(addedText)
	removed := b3SectionTickers(removedText)
	if len(added) == 0 {
		return nil, nil, errors.New("notice has no entry tickers")
	}
	return added, removed, nil
}

func b3SectionTickers(value string) []string {
	matches := b3NoticeTickerPattern.FindAllStringSubmatch(value, -1)
	result := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if _, exists := seen[match[1]]; exists {
			continue
		}
		seen[match[1]] = struct{}{}
		result = append(result, match[1])
	}
	return result
}
