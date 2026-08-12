package yahoo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/luisdourado/invs/internal/model"
)

const defaultBaseURL = "https://query1.finance.yahoo.com/v8/finance/chart/"

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
	Bars                             []model.PriceBar
	Raw                              []byte
	SHA256                           string
	RecordsReceived, RecordsRejected int
}

func (c *Client) HistoricalPrices(ctx context.Context, req model.HistoricalPriceRequest) ([]model.PriceBar, []byte, error) {
	r, err := c.Collect(ctx, req)
	return r.Bars, r.Raw, err
}
func (c *Client) Collect(ctx context.Context, req model.HistoricalPriceRequest) (Result, error) {
	if req.SecurityID == "" || req.VendorSymbol == "" || req.Currency == "" {
		return Result{}, fmt.Errorf("security ID, vendor symbol and currency are required")
	}
	if req.Start.IsZero() || req.End.IsZero() || req.End.Before(req.Start) {
		return Result{}, fmt.Errorf("valid start and end dates are required")
	}
	u, err := chartURL(c.baseURL, req.VendorSymbol)
	if err != nil {
		return Result{}, err
	}
	q := u.Query()
	q.Set("period1", strconv.FormatInt(req.Start.UTC().Unix(), 10))
	q.Set("period2", strconv.FormatInt(req.End.UTC().AddDate(0, 0, 1).Unix(), 10))
	q.Set("interval", "1d")
	q.Set("events", "history")
	u.RawQuery = q.Encode()
	b, err := c.http.Get(ctx, u.String())
	if err != nil {
		return Result{}, fmt.Errorf("Yahoo prices %s: %w", req.VendorSymbol, err)
	}
	bars, received, rejected, err := parse(b, req.SecurityID, req.Currency, c.now().UTC())
	if err != nil {
		return Result{}, err
	}
	return Result{Bars: bars, Raw: b, SHA256: digest(b), RecordsReceived: received, RecordsRejected: rejected}, nil
}

func chartURL(baseURL, symbol string) (*url.URL, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse Yahoo base URL: %w", err)
	}
	escaped := url.PathEscape(symbol)
	return base.ResolveReference(&url.URL{Path: symbol, RawPath: escaped}), nil
}

type response struct {
	Chart struct {
		Result []chartResult `json:"result"`
		Error  *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}
type chartResult struct {
	Meta struct {
		Currency             string `json:"currency"`
		ExchangeTimezoneName string `json:"exchangeTimezoneName"`
	} `json:"meta"`
	Timestamp  []int64 `json:"timestamp"`
	Indicators struct {
		Quote []quote `json:"quote"`
	} `json:"indicators"`
}
type quote struct {
	Open   []*float64 `json:"open"`
	High   []*float64 `json:"high"`
	Low    []*float64 `json:"low"`
	Close  []*float64 `json:"close"`
	Volume []*int64   `json:"volume"`
}

func parse(b []byte, securityID, currency string, ingested time.Time) ([]model.PriceBar, int, int, error) {
	var raw response
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, 0, 0, fmt.Errorf("decode Yahoo response: %w", err)
	}
	if raw.Chart.Error != nil {
		return nil, 0, 0, fmt.Errorf("Yahoo API %s: %s", raw.Chart.Error.Code, raw.Chart.Error.Description)
	}
	if len(raw.Chart.Result) != 1 || len(raw.Chart.Result[0].Indicators.Quote) != 1 {
		return nil, 0, 0, fmt.Errorf("unexpected Yahoo chart result")
	}
	r := raw.Chart.Result[0]
	if r.Meta.Currency != "" && r.Meta.Currency != currency {
		return nil, 0, 0, fmt.Errorf("Yahoo currency %q does not match configured %q", r.Meta.Currency, currency)
	}
	loc, err := time.LoadLocation(r.Meta.ExchangeTimezoneName)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("Yahoo exchange timezone %q: %w", r.Meta.ExchangeTimezoneName, err)
	}
	q := r.Indicators.Quote[0]
	received, rejected := len(r.Timestamp), 0
	unique := map[string]model.PriceBar{}
	hash := digest(b)
	for i, ts := range r.Timestamp {
		if i >= len(q.Open) || i >= len(q.High) || i >= len(q.Low) || i >= len(q.Close) || i >= len(q.Volume) || q.Open[i] == nil || q.High[i] == nil || q.Low[i] == nil || q.Close[i] == nil || q.Volume[i] == nil {
			rejected++
			continue
		}
		open, high, low, closeValue, volume := *q.Open[i], *q.High[i], *q.Low[i], *q.Close[i], *q.Volume[i]
		if volume < 0 || open < 0 || high < 0 || low < 0 || closeValue < 0 || math.IsNaN(open) || math.IsNaN(high) || math.IsNaN(low) || math.IsNaN(closeValue) || math.IsInf(open, 0) || math.IsInf(high, 0) || math.IsInf(low, 0) || math.IsInf(closeValue, 0) || low > high || open < low || open > high || closeValue < low || closeValue > high {
			rejected++
			continue
		}
		day := time.Unix(ts, 0).In(loc)
		closeAt := time.Date(day.Year(), day.Month(), day.Day(), 16, 0, 0, 0, loc).UTC()
		if closeAt.After(ingested) {
			continue
		}
		key := closeAt.Format("2006-01-02")
		if _, ok := unique[key]; ok {
			continue
		}
		unique[key] = model.PriceBar{Source: "yahoo", SecurityID: securityID, Currency: currency, Open: open, High: high, Low: low, Close: closeValue, Volume: volume, RawPayloadHash: hash, Temporal: model.Temporal{ObservedAt: closeAt, PublishedAt: ingested, PublishedPrecision: model.PrecisionSecond, AvailableAt: ingested, IngestedAt: ingested}}
	}
	bars := make([]model.PriceBar, 0, len(unique))
	for _, v := range unique {
		bars = append(bars, v)
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].Temporal.ObservedAt.Before(bars[j].Temporal.ObservedAt) })
	return bars, received, rejected, nil
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
