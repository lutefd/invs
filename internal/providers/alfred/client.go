package alfred

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/luisdourado/invs/internal/model"
	"github.com/luisdourado/invs/internal/providers"
)

const (
	defaultBaseURL        = "https://api.stlouisfed.org/fred/series/observations"
	EarliestRealtimeStart = "1776-07-04"
	PageLimit             = 100000
	OutputType            = 1
	source                = "alfred"
)

type Getter interface {
	Get(context.Context, string) ([]byte, error)
}

type Series struct {
	ID                 string
	Geography          string
	Unit               string
	Frequency          string
	SeasonalAdjustment string
	RealtimeEnd        string
	ObservationStart   string
	ObservationEnd     string
}

type RawPage struct {
	Offset    int
	Count     int
	Limit     int
	Bytes     []byte
	SHA256    string
	FetchedAt time.Time
}

type Result struct {
	providers.ResourceResult

	Observations []model.EconomicObservation
	Pages        []RawPage

	RecordsReceived, RecordsRejected, RecordsMissing int
}

type Client struct {
	http    Getter
	apiKey  string
	baseURL string
	now     func() time.Time
}

func NewClient(http Getter, apiKey string) *Client {
	return &Client{http: http, apiKey: strings.TrimSpace(apiKey), baseURL: defaultBaseURL, now: time.Now}
}

// Collect downloads observations by real-time period. Each ALFRED realtime_start
// is a date-only release/vintage boundary. PublishedAt preserves that source date
// at UTC midnight and AvailableAt advances 36 hours. This deliberately conservative
// policy is after the source civil date has ended in every inhabited time zone, so
// an unknown intraday release time cannot leak into same-day research.
func (c *Client) Collect(ctx context.Context, series Series) (Result, error) {
	normalized, err := normalizeSeries(series)
	if err != nil {
		return Result{}, fmt.Errorf("ALFRED series: %w", err)
	}
	if err := validateAPIKey(c.apiKey); err != nil {
		return Result{}, err
	}

	result := Result{}
	unique := make(map[string]model.EconomicObservation)
	total := -1
	for offset := 0; ; {
		requestURL, err := buildURL(c.baseURL, c.apiKey, normalized, offset)
		if err != nil {
			return result, fmt.Errorf("ALFRED series %s: %w", normalized.ID, err)
		}
		body, err := c.http.Get(ctx, requestURL)
		if err != nil {
			return result, fmt.Errorf("ALFRED series %s offset %d: %w", normalized.ID, offset, redactAPIKey(err, c.apiKey))
		}
		fetchedAt := c.now().UTC().Truncate(time.Microsecond)
		resource := providers.NewRawResource("series_page", fmt.Sprintf("%s/offset=%d", normalized.ID, offset), body, fetchedAt, "application/json")
		result.Resources = append(result.Resources, resource)
		page := RawPage{
			Offset: offset, Bytes: resource.Bytes, SHA256: resource.SHA256,
			FetchedAt: fetchedAt,
		}
		result.Pages = append(result.Pages, page)

		parsed, err := parsePage(body, normalized, offset, fetchedAt)
		result.Pages[len(result.Pages)-1].Count = parsed.Count
		result.Pages[len(result.Pages)-1].Limit = parsed.Limit
		result.RecordsReceived += parsed.Received
		result.RecordsRejected += parsed.Rejected
		result.RecordsMissing += parsed.Missing
		if err != nil {
			return result, fmt.Errorf("ALFRED series %s offset %d: %w", normalized.ID, offset, err)
		}
		if total == -1 {
			total = parsed.Count
		} else if parsed.Count != total {
			return result, fmt.Errorf("ALFRED series %s count changed during pagination: %d to %d", normalized.ID, total, parsed.Count)
		}
		for _, observation := range parsed.Observations {
			key := observationKey(observation)
			if prior, exists := unique[key]; exists {
				if prior.Value != observation.Value {
					return result, fmt.Errorf("ALFRED series %s has conflicting duplicate %s", normalized.ID, key)
				}
				continue
			}
			unique[key] = observation
		}

		pageRows := parsed.PageRows
		if offset+pageRows >= total {
			break
		}
		if pageRows == 0 {
			return result, fmt.Errorf("ALFRED series %s pagination stopped before count %d", normalized.ID, total)
		}
		offset += pageRows
	}

	result.Observations = make([]model.EconomicObservation, 0, len(unique))
	for _, observation := range unique {
		result.Observations = append(result.Observations, observation)
	}
	sort.Slice(result.Observations, func(i, j int) bool {
		a, b := result.Observations[i], result.Observations[j]
		if !a.Temporal.ObservedAt.Equal(b.Temporal.ObservedAt) {
			return a.Temporal.ObservedAt.Before(b.Temporal.ObservedAt)
		}
		if !a.Temporal.PublishedAt.Equal(b.Temporal.PublishedAt) {
			return a.Temporal.PublishedAt.Before(b.Temporal.PublishedAt)
		}
		return a.Provenance.RawRecordLocator < b.Provenance.RawRecordLocator
	})
	var observedAt time.Time
	revision := 0
	for i := range result.Observations {
		if i == 0 || !result.Observations[i].Temporal.ObservedAt.Equal(observedAt) {
			observedAt = result.Observations[i].Temporal.ObservedAt
			revision = 0
		}
		result.Observations[i].Revision = revision
		revision++
	}
	return result, nil
}

type apiResponse struct {
	RealtimeStart    string           `json:"realtime_start"`
	RealtimeEnd      string           `json:"realtime_end"`
	ObservationStart string           `json:"observation_start"`
	ObservationEnd   string           `json:"observation_end"`
	OutputType       int              `json:"output_type"`
	Count            int              `json:"count"`
	Offset           int              `json:"offset"`
	Limit            int              `json:"limit"`
	Observations     []apiObservation `json:"observations"`
	ErrorCode        int              `json:"error_code"`
	ErrorMessage     string           `json:"error_message"`
}

type apiObservation struct {
	RealtimeStart string `json:"realtime_start"`
	RealtimeEnd   string `json:"realtime_end"`
	Date          string `json:"date"`
	Value         string `json:"value"`
}

type parsedPage struct {
	Observations                []model.EconomicObservation
	Count, Limit, PageRows      int
	Received, Rejected, Missing int
}

func parsePage(body []byte, series Series, expectedOffset int, fetchedAt time.Time) (parsedPage, error) {
	var response apiResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&response); err != nil {
		return parsedPage{}, fmt.Errorf("decode JSON: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return parsedPage{}, err
	}
	page := parsedPage{Count: response.Count, Limit: response.Limit, PageRows: len(response.Observations)}
	if response.ErrorCode != 0 || response.ErrorMessage != "" {
		return page, fmt.Errorf("API error %d: %s", response.ErrorCode, response.ErrorMessage)
	}
	if response.OutputType != OutputType {
		return page, fmt.Errorf("unexpected output_type %d", response.OutputType)
	}
	if response.Offset != expectedOffset || response.Count < 0 || response.Limit <= 0 || response.Limit > PageLimit {
		return page, fmt.Errorf("invalid pagination metadata count=%d offset=%d limit=%d", response.Count, response.Offset, response.Limit)
	}
	if len(response.Observations) > response.Limit || response.Offset+len(response.Observations) > response.Count {
		return page, fmt.Errorf("observation page exceeds declared pagination bounds")
	}

	fetchedAt = fetchedAt.UTC().Truncate(time.Microsecond)
	hash := digest(body)
	page.Observations = make([]model.EconomicObservation, 0, len(response.Observations))
	for index, row := range response.Observations {
		page.Received++
		observed, observedErr := time.Parse("2006-01-02", row.Date)
		published, publishedErr := time.Parse("2006-01-02", row.RealtimeStart)
		realtimeEnd, realtimeEndErr := time.Parse("2006-01-02", row.RealtimeEnd)
		if observedErr != nil || publishedErr != nil || realtimeEndErr != nil || realtimeEnd.Before(published) {
			page.Rejected++
			continue
		}
		observed, published = observed.UTC(), published.UTC()
		if observed.After(published) {
			page.Rejected++
			continue
		}
		value := ""
		var valueErr error
		if row.Value == "." || strings.TrimSpace(row.Value) == "" {
			page.Missing++
		} else {
			value, valueErr = model.CanonicalDecimal(row.Value, false)
		}
		available := published.Add(36 * time.Hour)
		if valueErr != nil || available.After(fetchedAt) {
			page.Rejected++
			continue
		}
		vintage := published
		page.Observations = append(page.Observations, model.EconomicObservation{
			Source: source, SeriesID: series.ID, Geography: series.Geography,
			Unit: series.Unit, Frequency: series.Frequency,
			SeasonalAdjustment: series.SeasonalAdjustment,
			Value:              value, Revision: 0, VintageAt: &vintage, RawPayloadHash: hash,
			Temporal: model.Temporal{
				ObservedAt: observed, ObservedPrecision: model.PrecisionDate,
				PublishedAt: published, PublishedPrecision: model.PrecisionDate,
				AvailableAt: available, IngestedAt: fetchedAt,
			},
			Provenance: model.Provenance{
				RawPayloadHash: hash,
				RawRecordLocator: fmt.Sprintf(
					"json/offset=%d/observations/%d/date=%s/realtime_start=%s/realtime_end=%s",
					expectedOffset, index, row.Date, row.RealtimeStart, row.RealtimeEnd,
				),
				IngestedAt: fetchedAt, NormalizerVersion: model.NormalizerVersion,
			},
		})
	}
	return page, nil
}

func buildURL(baseURL, apiKey string, series Series, offset int) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base URL: %w", err)
	}
	if base.Scheme != "https" || base.Host == "" {
		return "", errors.New("base URL must be absolute HTTPS")
	}
	query := base.Query()
	query.Set("api_key", apiKey)
	query.Set("file_type", "json")
	query.Set("series_id", series.ID)
	query.Set("units", "lin")
	query.Set("output_type", strconv.Itoa(OutputType))
	query.Set("realtime_start", EarliestRealtimeStart)
	query.Set("realtime_end", series.RealtimeEnd)
	query.Set("sort_order", "asc")
	query.Set("limit", strconv.Itoa(PageLimit))
	query.Set("offset", strconv.Itoa(offset))
	if series.ObservationStart != "" {
		query.Set("observation_start", series.ObservationStart)
	}
	if series.ObservationEnd != "" {
		query.Set("observation_end", series.ObservationEnd)
	}
	base.RawQuery = query.Encode()
	return base.String(), nil
}

func normalizeSeries(series Series) (Series, error) {
	series.ID = strings.TrimSpace(series.ID)
	if !safeIdentifier(series.ID) {
		return Series{}, errors.New("series ID is required and must use safe identifier characters")
	}
	for name, value := range map[string]string{
		"geography": series.Geography, "unit": series.Unit, "frequency": series.Frequency,
	} {
		if strings.TrimSpace(value) == "" {
			return Series{}, fmt.Errorf("%s is required", name)
		}
	}
	if !validFrequency(series.Frequency) {
		return Series{}, fmt.Errorf("unsupported frequency %q", series.Frequency)
	}
	realtimeEnd, err := requiredDate(series.RealtimeEnd)
	if err != nil {
		return Series{}, fmt.Errorf("realtime_end: %w", err)
	}
	earliest, _ := time.Parse("2006-01-02", EarliestRealtimeStart)
	if realtimeEnd.Before(earliest) {
		return Series{}, errors.New("realtime_end precedes ALFRED's earliest supported realtime date")
	}
	observationStart, err := optionalDate(series.ObservationStart)
	if err != nil {
		return Series{}, fmt.Errorf("observation_start: %w", err)
	}
	observationEnd, err := optionalDate(series.ObservationEnd)
	if err != nil {
		return Series{}, fmt.Errorf("observation_end: %w", err)
	}
	if !observationStart.IsZero() && !observationEnd.IsZero() && observationEnd.Before(observationStart) {
		return Series{}, errors.New("observation_end precedes observation_start")
	}
	return series, nil
}

func validateAPIKey(value string) error {
	if len(value) != 32 {
		return errors.New("ALFRED requires FRED_API_KEY as 32 lowercase alphanumeric characters")
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return errors.New("ALFRED requires FRED_API_KEY as 32 lowercase alphanumeric characters")
		}
	}
	return nil
}

func redactAPIKey(err error, apiKey string) error {
	if err == nil {
		return nil
	}
	return errors.New(strings.ReplaceAll(err.Error(), apiKey, "[REDACTED]"))
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("decode JSON: multiple top-level values")
		}
		return fmt.Errorf("decode JSON: %w", err)
	}
	return nil
}

func observationKey(observation model.EconomicObservation) string {
	return strings.Join([]string{
		observation.SeriesID,
		observation.Temporal.ObservedAt.Format("2006-01-02"),
		observation.Temporal.PublishedAt.Format("2006-01-02"),
	}, "\x1f")
}

func safeIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func validFrequency(value string) bool {
	switch value {
	case "daily", "weekly", "monthly", "quarterly", "semiannual", "annual", "irregular":
		return true
	default:
		return false
	}
}

func requiredDate(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, errors.New("date is required")
	}
	return optionalDate(value)
}

func optionalDate(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if strings.TrimSpace(value) != value {
		return time.Time{}, errors.New("date has surrounding whitespace")
	}
	return time.Parse("2006-01-02", value)
}

func digest(body []byte) string { return providers.SHA256(body) }
