package b3

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/luisdourado/invs/internal/providers"
)

const (
	ListingLifecycleParserVersion = "b3-listing-lifecycle-notice-v1"
	ListingLifecycleResourceKind  = "listing_lifecycle_notice"
)

var (
	b3LifecycleTitlePattern     = regexp.MustCompile(`(?i)\b([A-Z0-9]+)\s+\(([A-Z0-9]+)-NM\)\s+-\s+Fato Relevante\s+-\s+(\d{2})/(\d{2})/(\d{2})`)
	b3LifecycleEffectivePattern = regexp.MustCompile(`(?i)A partir de\s+(\d{2})/(\d{2})/(\d{4}),\s+as acoes da companhia deixam de ser negociadas`)
)

type ListingLifecycleRequest struct {
	URL string
}

type ListingLifecycleEvent struct {
	TradingName      string
	EventKind        string
	Reason           string
	EffectiveAt      time.Time
	AnnouncedAt      time.Time
	AvailableAt      time.Time
	SourceNoticeID   string
	RawRecordLocator string
}

type ListingLifecycleResult struct {
	providers.ResourceResult
	Events []ListingLifecycleEvent
}

func (c *Client) CollectListingLifecycle(ctx context.Context, request ListingLifecycleRequest) (ListingLifecycleResult, error) {
	if c == nil || c.http == nil {
		return ListingLifecycleResult{}, errors.New("B3 listing-lifecycle client HTTP getter is required")
	}
	requestURL, announcedAt, noticeID, err := normalizeListingLifecycleURL(request.URL)
	if err != nil {
		return ListingLifecycleResult{}, err
	}
	body, err := c.http.Get(ctx, requestURL)
	if err != nil {
		return ListingLifecycleResult{}, fmt.Errorf("B3 listing-lifecycle request: %w", err)
	}
	fetchedAt := canonicalTime(c.now())
	resource := providers.NewRawResource(ListingLifecycleResourceKind, "plantao-"+noticeID+".html", body, fetchedAt, "text/html; charset=utf-8")
	resource.URL = requestURL
	resource.ParserVersion = ListingLifecycleParserVersion
	resource.ParserMetadata = map[string]string{
		"source_owner":     "B3 S.A. - Brasil, Bolsa, Balcao",
		"source_transport": "B3 Plantao de Noticias",
		"notice_id":        noticeID,
		"request_url":      requestURL,
	}
	result := ListingLifecycleResult{ResourceResult: providers.ResourceResult{Resources: []providers.RawResource{resource}}}
	event, err := parseListingLifecycle(body, announcedAt, noticeID)
	if err != nil {
		return result, fmt.Errorf("B3 listing-lifecycle notice: %w", err)
	}
	result.Events = []ListingLifecycleEvent{event}
	resource.ParserMetadata["event_kind"] = event.EventKind
	resource.ParserMetadata["trading_name"] = event.TradingName
	resource.ParserMetadata["effective_at"] = event.EffectiveAt.Format(time.RFC3339)
	resource.ParserMetadata["available_at"] = event.AvailableAt.Format(time.RFC3339)
	return result, nil
}

func normalizeListingLifecycleURL(value string) (string, time.Time, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host != "sistemasweb.b3.com.br" {
		return "", time.Time{}, "", fmt.Errorf("B3 listing-lifecycle URL %q must use the official HTTPS host", value)
	}
	if parsed.Path != "/PlantaoNoticias/Noticias/Detail" {
		return "", time.Time{}, "", fmt.Errorf("B3 listing-lifecycle URL %q must use the official Plantao detail path", value)
	}
	query := parsed.Query()
	if query.Get("agencia") == "" || query.Get("idNoticia") == "" || query.Get("dataNoticia") == "" {
		return "", time.Time{}, "", errors.New("B3 listing-lifecycle URL requires agencia, idNoticia, and dataNoticia")
	}
	if len(query) != 3 {
		return "", time.Time{}, "", errors.New("B3 listing-lifecycle URL contains unsupported query fields")
	}
	for _, field := range []string{"agencia", "idNoticia", "dataNoticia"} {
		if len(query[field]) != 1 {
			return "", time.Time{}, "", fmt.Errorf("B3 listing-lifecycle URL requires exactly one %s", field)
		}
	}
	location, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return "", time.Time{}, "", err
	}
	announcedAt, err := time.ParseInLocation("2006-01-02 15:04:05", query.Get("dataNoticia"), location)
	if err != nil {
		return "", time.Time{}, "", fmt.Errorf("B3 listing-lifecycle dataNoticia: %w", err)
	}
	parsed.RawQuery = query.Encode()
	parsed.Fragment = ""
	return parsed.String(), announcedAt.UTC(), query.Get("idNoticia"), nil
}

func parseListingLifecycle(body []byte, announcedAt time.Time, noticeID string) (ListingLifecycleEvent, error) {
	text := cleanMarketCalendarHTML(string(body))
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "fato relevante") || !strings.Contains(lower, "incorporacao") {
		return ListingLifecycleEvent{}, errors.New("notice is not a B3 incorporation lifecycle notice")
	}
	title := b3LifecycleTitlePattern.FindStringSubmatch(text)
	if len(title) != 6 || !strings.EqualFold(title[1], title[2]) {
		return ListingLifecycleEvent{}, errors.New("notice has no canonical B3 company title")
	}
	year := 2000 + parseTwoDigits(title[5])
	if announcedAt.Year() != year || int(announcedAt.Month()) != parseTwoDigits(title[4]) || announcedAt.Day() != parseTwoDigits(title[3]) {
		return ListingLifecycleEvent{}, errors.New("notice title date disagrees with dataNoticia")
	}
	effective := b3LifecycleEffectivePattern.FindStringSubmatch(text)
	if len(effective) != 4 {
		return ListingLifecycleEvent{}, errors.New("notice has no trading-cessation effective date")
	}
	location, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return ListingLifecycleEvent{}, err
	}
	effectiveAt := time.Date(parseFourDigits(effective[3]), time.Month(parseTwoDigits(effective[2])), parseTwoDigits(effective[1]), 0, 0, 0, 0, location)
	if effectiveAt.Format("02/01/2006") != strings.Join([]string{effective[1], effective[2], effective[3]}, "/") {
		return ListingLifecycleEvent{}, errors.New("notice trading-cessation date is invalid")
	}
	return ListingLifecycleEvent{
		TradingName: title[1], EventKind: "trading_cessation", Reason: "incorporation",
		EffectiveAt: effectiveAt.UTC(), AnnouncedAt: announcedAt.UTC(), AvailableAt: announcedAt.UTC(),
		SourceNoticeID:   noticeID,
		RawRecordLocator: "plantao/id=" + noticeID + "/trading-name=" + title[1] + "/effective=" + effectiveAt.Format(time.DateOnly) + "/trading-cessation",
	}, nil
}

func parseTwoDigits(value string) int {
	return int(value[0]-'0')*10 + int(value[1]-'0')
}

func parseFourDigits(value string) int {
	return parseTwoDigits(value[:2])*100 + parseTwoDigits(value[2:])
}
