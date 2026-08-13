package bcb

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/luisdourado/invs/internal/model"
	"github.com/luisdourado/invs/internal/providers"
)

const defaultBaseURL = "https://api.bcb.gov.br/dados/serie/"

const source = "bcb"

// Getter is the small HTTP boundary shared by the repository's providers. The
// production implementation is internal/httpx.Client; tests can provide a
// deterministic fake without changing the adapter.
type Getter interface {
	Get(context.Context, string) ([]byte, error)
}

// Series contains the source metadata that is not present in the SGS CSV
// response but is required to construct a model.EconomicObservation. Start and
// End use the repository's ISO date configuration convention. They are
// translated to SGS's dd/mm/yyyy query parameters when a request is built.
type Series struct {
	Code               string
	Geography          string
	Unit               string
	Frequency          string
	SeasonalAdjustment string
	Start              string
	End                string
}

// SeriesConfig is an explicit name for callers that keep provider settings
// separate from the observation itself.
type SeriesConfig = Series

type Client struct {
	http    Getter
	baseURL string
	now     func() time.Time
}

func NewClient(http Getter) *Client {
	return &Client{http: http, baseURL: defaultBaseURL, now: time.Now}
}

type Result struct {
	providers.ResourceResult

	// Raw and SHA256 are retained as compatibility aliases for this provider's
	// single response. Resources is the canonical downloaded-resource contract.
	Observations []model.EconomicObservation
	Raw          []byte
	SHA256       string

	RecordsReceived, RecordsRejected, RecordsMissing int
}

// Collect downloads one current SGS CSV series. SGS does not include release
// timestamps or vintages in this endpoint, so PublishedAt and AvailableAt are
// conservatively the local receipt time. VintageAt is the same current-download
// marker used by the FRED adapter; it is not claimed to be a historical BCB
// release timestamp. Historical point-in-time research requires a source that
// exposes release/vintage history.
func (c *Client) Collect(ctx context.Context, series Series) (Result, error) {
	normalized, err := normalizeSeries(series)
	if err != nil {
		return Result{}, fmt.Errorf("BCB SGS series: %w", err)
	}
	requestURL, err := buildURL(c.baseURL, normalized)
	if err != nil {
		return Result{}, fmt.Errorf("BCB SGS series %s: %w", normalized.Code, err)
	}
	b, err := c.http.Get(ctx, requestURL)
	if err != nil {
		return Result{}, fmt.Errorf("BCB SGS series %s: %w", normalized.Code, err)
	}

	fetchedAt := c.now().UTC().Truncate(time.Microsecond)
	resource := providers.NewRawResource("series", normalized.Code, b, fetchedAt, "text/csv")
	result := Result{
		ResourceResult: providers.ResourceResult{Resources: []providers.RawResource{resource}},
		Raw:            resource.Bytes,
		SHA256:         resource.SHA256,
	}
	observations, received, rejected, missing, err := parseCSV(resource.Bytes, normalized, fetchedAt)
	result.Observations = observations
	result.RecordsReceived = received
	result.RecordsRejected = rejected
	result.RecordsMissing = missing
	if err != nil {
		return result, err
	}
	return result, nil
}

func buildURL(baseURL string, series Series) (string, error) {
	normalized, err := normalizeSeries(series)
	if err != nil {
		return "", err
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse BCB base URL: %w", err)
	}
	if base.Scheme == "" || base.Host == "" {
		return "", fmt.Errorf("BCB base URL must include scheme and host")
	}

	base.Path = strings.TrimRight(base.Path, "/") + "/bcdata.sgs." + url.PathEscape(normalized.Code) + "/dados"
	base.RawPath = ""
	base.Fragment = ""
	query := base.Query()
	query.Set("formato", "csv")
	if normalized.Start != "" {
		start, _ := parseISODate(normalized.Start)
		query.Set("dataInicial", start.Format("02/01/2006"))
	}
	if normalized.End != "" {
		end, _ := parseISODate(normalized.End)
		query.Set("dataFinal", end.Format("02/01/2006"))
	}
	base.RawQuery = query.Encode()
	return base.String(), nil
}

func parseCSV(b []byte, series Series, ingested time.Time) ([]model.EconomicObservation, int, int, int, error) {
	normalized, err := normalizeSeries(series)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	ingested = ingested.UTC().Truncate(time.Microsecond)
	if ingested.IsZero() {
		return nil, 0, 0, 0, fmt.Errorf("BCB SGS ingestion time is required")
	}

	reader := csv.NewReader(bytes.NewReader(b))
	reader.Comma = ';'
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = false
	header, err := reader.Read()
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("decode BCB SGS CSV: %w", err)
	}
	if len(header) != 2 || header[0] != "data" || header[1] != "valor" {
		return nil, 0, 0, 0, fmt.Errorf("unexpected BCB SGS columns")
	}

	received, rejected, missing := 0, 0, 0
	unique := make(map[string]model.EconomicObservation)
	hash := digest(b)
	for {
		row, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, received, rejected, missing, fmt.Errorf("decode BCB SGS CSV: %w", readErr)
		}
		received++
		if len(row) != 2 {
			rejected++
			continue
		}

		day, dateErr := parseSourceDate(row[0])
		if dateErr != nil || day.After(ingested) {
			rejected++
			continue
		}
		if isMissingValue(row[1]) {
			missing++
			continue
		}
		value, valueErr := canonicalValue(row[1])
		if valueErr != nil {
			rejected++
			continue
		}

		dateKey := day.Format("2006-01-02")
		vintage := ingested
		unique[dateKey] = model.EconomicObservation{
			Source: source, SeriesID: normalized.Code, Geography: normalized.Geography,
			Unit: normalized.Unit, Frequency: normalized.Frequency,
			SeasonalAdjustment: normalized.SeasonalAdjustment, Value: value, Revision: 0,
			VintageAt: &vintage, RawPayloadHash: hash,
			Provenance: model.Provenance{
				RawPayloadHash: hash, RawRecordLocator: "csv/date=" + dateKey,
				IngestedAt: ingested, NormalizerVersion: model.NormalizerVersion,
			},
			Temporal: model.Temporal{
				ObservedAt: day, ObservedPrecision: model.PrecisionDate,
				PublishedAt: ingested, PublishedPrecision: model.PrecisionUnknown,
				AvailableAt: ingested, IngestedAt: ingested,
			},
		}
	}

	observations := make([]model.EconomicObservation, 0, len(unique))
	for _, observation := range unique {
		observations = append(observations, observation)
	}
	sort.Slice(observations, func(i, j int) bool {
		if !observations[i].Temporal.ObservedAt.Equal(observations[j].Temporal.ObservedAt) {
			return observations[i].Temporal.ObservedAt.Before(observations[j].Temporal.ObservedAt)
		}
		return observations[i].Provenance.RawRecordLocator < observations[j].Provenance.RawRecordLocator
	})
	return observations, received, rejected, missing, nil
}

func normalizeSeries(series Series) (Series, error) {
	series.Code = strings.TrimSpace(series.Code)
	if series.Code == "" {
		return Series{}, fmt.Errorf("series code is required")
	}
	if strings.TrimLeft(series.Code, "0123456789") != "" {
		return Series{}, fmt.Errorf("series code %q must contain only decimal digits", series.Code)
	}
	if series.Code == "0" || (len(series.Code) > 1 && series.Code[0] == '0') {
		return Series{}, fmt.Errorf("series code %q is not canonical", series.Code)
	}
	for name, value := range map[string]string{
		"geography": series.Geography,
		"unit":      series.Unit,
		"frequency": series.Frequency,
	} {
		if strings.TrimSpace(value) == "" {
			return Series{}, fmt.Errorf("series %s is required", name)
		}
	}
	series.Geography = strings.TrimSpace(series.Geography)
	series.Unit = strings.TrimSpace(series.Unit)
	series.Frequency = strings.TrimSpace(series.Frequency)
	series.SeasonalAdjustment = strings.TrimSpace(series.SeasonalAdjustment)
	if !validFrequency(series.Frequency) {
		return Series{}, fmt.Errorf("series frequency %q is unsupported", series.Frequency)
	}

	start, startSet, err := parseOptionalISODate(series.Start, "start")
	if err != nil {
		return Series{}, err
	}
	end, endSet, err := parseOptionalISODate(series.End, "end")
	if err != nil {
		return Series{}, err
	}
	if startSet && endSet && end.Before(start) {
		return Series{}, fmt.Errorf("series end date must not precede start date")
	}
	series.Start = strings.TrimSpace(series.Start)
	series.End = strings.TrimSpace(series.End)
	return series, nil
}

func parseOptionalISODate(value, field string) (time.Time, bool, error) {
	if value == "" {
		return time.Time{}, false, nil
	}
	parsed, err := parseISODate(value)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("series %s must be an ISO date: %w", field, err)
	}
	return parsed, true, nil
}

func parseISODate(value string) (time.Time, error) {
	if strings.TrimSpace(value) != value {
		return time.Time{}, fmt.Errorf("date must not contain surrounding whitespace")
	}
	return time.ParseInLocation("2006-01-02", value, time.UTC)
}

func parseSourceDate(value string) (time.Time, error) {
	return time.ParseInLocation("02/01/2006", value, time.UTC)
}

func canonicalValue(raw string) (string, error) {
	// SGS CSV uses a comma as its decimal separator. A dot is rejected here
	// rather than guessed as a thousands separator; raw bytes retain the
	// provider lexeme for diagnosis and replay.
	if !validSGSDecimalLexeme(raw) {
		return "", fmt.Errorf("invalid SGS decimal %q", raw)
	}
	normalized := strings.Replace(raw, ",", ".", 1)
	value, err := model.CanonicalDecimal(normalized, false)
	if err != nil {
		return "", fmt.Errorf("invalid SGS decimal %q: %w", raw, err)
	}
	return value, nil
}

func validSGSDecimalLexeme(value string) bool {
	if value == "" {
		return false
	}
	start := 0
	if value[0] == '-' {
		start++
	}
	if start == len(value) {
		return false
	}
	separator := strings.IndexByte(value[start:], ',')
	if separator < 0 {
		separator = len(value)
	} else {
		separator += start
		if separator+1 == len(value) {
			return false
		}
		if strings.IndexByte(value[separator+1:], ',') >= 0 {
			return false
		}
	}
	if separator == start {
		return false
	}
	for _, character := range value[start:separator] {
		if character < '0' || character > '9' {
			return false
		}
	}
	if separator < len(value) {
		for _, character := range value[separator+1:] {
			if character < '0' || character > '9' {
				return false
			}
		}
	}
	return true
}

func isMissingValue(value string) bool {
	// The documented SGS CSV missing marker is a hyphen. Empty fields are also
	// treated as missing, while other non-finite-looking tokens are rejected.
	return value == "" || value == "-"
}

func validFrequency(value string) bool {
	switch value {
	case "daily", "weekly", "monthly", "quarterly", "semiannual", "annual", "irregular":
		return true
	default:
		return false
	}
}

func digest(b []byte) string { return providers.SHA256(b) }
