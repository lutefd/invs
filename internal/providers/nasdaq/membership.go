// Package nasdaq provides raw-first adapters for Nasdaq-authored index notices.
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
	MembershipParserVersion = "nasdaq-membership-notice-v1"
	MembershipResourceKind  = "index_membership_notice"
)

var (
	nasdaqTagPattern       = regexp.MustCompile(`(?is)<[^>]*>`)
	nasdaqPublishedPattern = regexp.MustCompile(`(?i)\b(January|February|March|April|May|June|July|August|September|October|November|December)\s+(\d{1,2}),\s+(\d{4})\s+(\d{1,2}):(\d{2})\s+ET\s*\|\s*Source:\s*Nasdaq,\s*Inc\.`)
	nasdaqDatelinePattern  = regexp.MustCompile(`(?i)\bNEW YORK,\s+(?:Jan\.|Feb\.|Mar\.|Apr\.|Jun\.|Jul\.|Aug\.|Sep\.|Sept\.|Oct\.|Nov\.|Dec\.|January|February|March|April|May|June|July|August|September|October|November|December)\s+\d{1,2},\s+\d{4}\s+\(GLOBE NEWSWIRE\)`)
	nasdaqEffectivePattern = regexp.MustCompile(`(?i)prior to market open on (?:Monday|Tuesday|Wednesday|Thursday|Friday),\s+(January|February|March|April|May|June|July|August|September|October|November|December)\s+(\d{1,2}),\s+(\d{4})`)
	nasdaqTickerPattern    = regexp.MustCompile(`(?i)\(Nasdaq:\s*([A-Z0-9][A-Z0-9.-]*)\)`)
)

type Getter interface {
	Get(context.Context, string) ([]byte, error)
}

type Client struct {
	http Getter
	now  func() time.Time
}

func NewClient(getter Getter) *Client {
	return &Client{http: getter, now: time.Now}
}

type MembershipRequest struct {
	URL string
}

type MembershipEvent struct {
	Ticker           string
	Member           bool
	EffectiveAt      time.Time
	AnnouncedAt      time.Time
	AvailableAt      time.Time
	RawRecordLocator string
}

type MembershipResult struct {
	providers.ResourceResult
	Events []MembershipEvent
}

func (c *Client) CollectMembership(ctx context.Context, request MembershipRequest) (MembershipResult, error) {
	if c == nil || c.http == nil {
		return MembershipResult{}, errors.New("Nasdaq membership client HTTP getter is required")
	}
	requestURL, err := normalizeMembershipURL(request.URL)
	if err != nil {
		return MembershipResult{}, err
	}
	body, err := c.http.Get(ctx, requestURL)
	if err != nil {
		return MembershipResult{}, fmt.Errorf("Nasdaq membership request: %w", err)
	}
	fetchedAt := c.now().UTC().Truncate(time.Microsecond)
	resource := providers.NewRawResource(MembershipResourceKind, membershipResourceKey(requestURL), body, fetchedAt, "text/html; charset=utf-8")
	resource.URL = requestURL
	resource.ParserVersion = MembershipParserVersion
	resource.ParserMetadata = map[string]string{
		"source_owner":     "Nasdaq, Inc.",
		"source_transport": "Nasdaq-authored GlobeNewswire release",
		"universe":         "Nasdaq-100",
		"request_url":      requestURL,
	}
	result := MembershipResult{ResourceResult: providers.ResourceResult{Resources: []providers.RawResource{resource}}}
	result.Events, err = parseMembership(body)
	if err != nil {
		return result, fmt.Errorf("Nasdaq membership notice: %w", err)
	}
	resource.ParserMetadata["events"] = strconv.Itoa(len(result.Events))
	resource.ParserMetadata["effective_at"] = result.Events[0].EffectiveAt.Format(time.RFC3339)
	resource.ParserMetadata["available_at"] = result.Events[0].AvailableAt.Format(time.RFC3339)
	return result, nil
}

func normalizeMembershipURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host != "www.globenewswire.com" {
		return "", fmt.Errorf("Nasdaq membership URL %q must be an HTTPS GlobeNewswire release", value)
	}
	if !strings.HasPrefix(parsed.Path, "/news-release/") {
		return "", fmt.Errorf("Nasdaq membership URL %q must use the GlobeNewswire news-release path", value)
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func membershipResourceKey(requestURL string) string {
	parsed, _ := url.Parse(requestURL)
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) >= 4 {
		return "nasdaq-" + parts[2] + ".html"
	}
	return "nasdaq-membership.html"
}

func parseMembership(body []byte) ([]MembershipEvent, error) {
	text := cleanMembershipHTML(string(body))
	if !nasdaqDatelinePattern.MatchString(text) || !strings.Contains(text, "Source: Nasdaq, Inc.") {
		return nil, errors.New("notice is not a Nasdaq-authored GlobeNewswire release")
	}
	published, err := parsePublishedAt(text)
	if err != nil {
		return nil, err
	}
	effective, err := parseEffectiveAt(text)
	if err != nil {
		return nil, err
	}
	addedSection, err := noticeSection(text, "companies will be added to the Index:", []string{"The Nasdaq-100 Index", "As a result of the reconstitution", "companies will be removed from the Index:"})
	if err != nil {
		return nil, err
	}
	removedSection, err := noticeSection(text, "companies will be removed from the Index:", []string{"For additional information", "For information about", "About Nasdaq Global Indexes", "About Nasdaq"})
	if err != nil {
		return nil, err
	}
	added := sectionTickers(addedSection)
	removed := sectionTickers(removedSection)
	if len(added) == 0 || len(removed) == 0 {
		return nil, errors.New("notice must contain at least one addition and removal")
	}
	events := make([]MembershipEvent, 0, len(added)+len(removed))
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
			events = append(events, MembershipEvent{
				Ticker: ticker, Member: member, EffectiveAt: effective,
				AnnouncedAt: published, AvailableAt: published,
				RawRecordLocator: "nasdaq-100/effective=" + effective.Format(time.DateOnly) + "/" + kind + "/ticker=" + ticker,
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

func parsePublishedAt(text string) (time.Time, error) {
	match := nasdaqPublishedPattern.FindStringSubmatch(text)
	if len(match) != 6 {
		return time.Time{}, errors.New("notice has no exact Nasdaq publication timestamp")
	}
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.Time{}, err
	}
	value := fmt.Sprintf("%s %s %s %s:%s", match[1], match[2], match[3], match[4], match[5])
	parsed, err := time.ParseInLocation("January 2 2006 15:04", value, location)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse Nasdaq publication timestamp: %w", err)
	}
	return parsed.UTC(), nil
}

func parseEffectiveAt(text string) (time.Time, error) {
	match := nasdaqEffectivePattern.FindStringSubmatch(text)
	if len(match) != 4 {
		return time.Time{}, errors.New("notice has no prior-to-market-open effective date")
	}
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.Time{}, err
	}
	parsed, err := time.ParseInLocation("January 2 2006 15:04", fmt.Sprintf("%s %s %s 09:30", match[1], match[2], match[3]), location)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse Nasdaq effective date: %w", err)
	}
	return parsed.UTC(), nil
}

func noticeSection(text, marker string, endings []string) (string, error) {
	lower := strings.ToLower(text)
	start := strings.Index(lower, strings.ToLower(marker))
	if start < 0 {
		return "", fmt.Errorf("notice section %q was not found", marker)
	}
	start += len(marker)
	end := len(text)
	for _, ending := range endings {
		if offset := strings.Index(lower[start:], strings.ToLower(ending)); offset >= 0 && start+offset < end {
			end = start + offset
		}
	}
	return strings.TrimSpace(text[start:end]), nil
}

func sectionTickers(section string) []string {
	matches := nasdaqTickerPattern.FindAllStringSubmatch(section, -1)
	result := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		ticker := strings.ToUpper(match[1])
		if _, exists := seen[ticker]; exists {
			continue
		}
		seen[ticker] = struct{}{}
		result = append(result, ticker)
	}
	return result
}

func cleanMembershipHTML(value string) string {
	value = nasdaqTagPattern.ReplaceAllString(value, " ")
	value = html.UnescapeString(value)
	return strings.Join(strings.Fields(value), " ")
}
