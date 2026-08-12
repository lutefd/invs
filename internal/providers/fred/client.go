package fred

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/luisdourado/invs/internal/model"
)

const defaultBaseURL = "https://fred.stlouisfed.org/graph/fredgraph.csv"

type Getter interface {
	Get(context.Context, string) ([]byte, error)
}
type Client struct {
	http    Getter
	baseURL string
	now     func() time.Time
}

func NewClient(http Getter) *Client {
	return &Client{http: http, baseURL: defaultBaseURL, now: time.Now}
}

type Result struct {
	Observations                     []model.EconomicObservation
	Raw                              []byte
	SHA256                           string
	RecordsReceived, RecordsRejected int
}

// Collect downloads FRED's current-vintage public CSV. Because it contains neither
// release timestamps nor vintages, PublishedAt and AvailableAt are conservatively set
// to IngestedAt. Historical point-in-time research must use an ALFRED adapter later.
func (c *Client) Collect(ctx context.Context, seriesID string) (Result, error) {
	seriesID = strings.TrimSpace(seriesID)
	if seriesID == "" {
		return Result{}, fmt.Errorf("FRED series ID is required")
	}
	u, _ := url.Parse(c.baseURL)
	q := u.Query()
	q.Set("id", seriesID)
	u.RawQuery = q.Encode()
	b, err := c.http.Get(ctx, u.String())
	if err != nil {
		return Result{}, fmt.Errorf("FRED series %s: %w", seriesID, err)
	}
	obs, received, rejected, err := parseCSV(b, seriesID, c.now().UTC())
	if err != nil {
		return Result{}, err
	}
	return Result{Observations: obs, Raw: b, SHA256: digest(b), RecordsReceived: received, RecordsRejected: rejected}, nil
}

func parseCSV(b []byte, seriesID string, ingested time.Time) ([]model.EconomicObservation, int, int, error) {
	r := csv.NewReader(strings.NewReader(string(b)))
	rows, err := r.ReadAll()
	if err != nil {
		return nil, 0, 0, fmt.Errorf("decode FRED CSV: %w", err)
	}
	if len(rows) == 0 || len(rows[0]) != 2 || rows[0][0] != "observation_date" || !strings.EqualFold(rows[0][1], seriesID) {
		return nil, 0, 0, fmt.Errorf("unexpected FRED columns")
	}
	received, rejected := len(rows)-1, 0
	hash := digest(b)
	unique := map[string]model.EconomicObservation{}
	for _, row := range rows[1:] {
		if len(row) != 2 || row[1] == "." || row[1] == "" {
			rejected++
			continue
		}
		day, e1 := time.Parse("2006-01-02", row[0])
		value, e2 := strconv.ParseFloat(row[1], 64)
		if e1 != nil || e2 != nil {
			rejected++
			continue
		}
		day = day.UTC()
		unique[row[0]] = model.EconomicObservation{Source: "fred", SeriesID: seriesID, Unit: "unknown", Value: value, RawPayloadHash: hash, Temporal: model.Temporal{ObservedAt: day, PublishedAt: ingested, PublishedPrecision: model.PrecisionSecond, AvailableAt: ingested, IngestedAt: ingested}}
	}
	result := make([]model.EconomicObservation, 0, len(unique))
	for _, o := range unique {
		result = append(result, o)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Temporal.ObservedAt.Before(result[j].Temporal.ObservedAt) })
	return result, received, rejected, nil
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
