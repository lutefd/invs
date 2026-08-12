package yahoo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"sort"
	"strconv"
	"strings"
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
	result := Result{Raw: b, SHA256: digest(b)}
	bars, received, rejected, err := parse(b, req.SecurityID, req.Currency, c.now().UTC())
	result.Bars, result.RecordsReceived, result.RecordsRejected = bars, received, rejected
	if err != nil {
		return result, err
	}
	return result, nil
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
	Open   []*json.Number `json:"open"`
	High   []*json.Number `json:"high"`
	Low    []*json.Number `json:"low"`
	Close  []*json.Number `json:"close"`
	Volume []*json.Number `json:"volume"`
}

func parse(b []byte, securityID, currency string, ingested time.Time) ([]model.PriceBar, int, int, error) {
	var raw response
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
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
		open, e0 := model.CanonicalDecimal(q.Open[i].String(), true)
		high, e1 := model.CanonicalDecimal(q.High[i].String(), true)
		low, e2 := model.CanonicalDecimal(q.Low[i].String(), true)
		closeValue, e3 := model.CanonicalDecimal(q.Close[i].String(), true)
		volume, e4 := model.CanonicalDecimal(q.Volume[i].String(), true)
		if e0 != nil || e1 != nil || e2 != nil || e3 != nil || e4 != nil || compareDecimal(low, high) > 0 || compareDecimal(open, low) < 0 || compareDecimal(open, high) > 0 || compareDecimal(closeValue, low) < 0 || compareDecimal(closeValue, high) > 0 {
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
		unique[key] = model.PriceBar{Source: "yahoo", SecurityID: securityID, Currency: currency, Interval: "1d", PriceBasis: "raw", Open: open, High: high, Low: low, Close: closeValue, Volume: volume, RawPayloadHash: hash, Provenance: model.Provenance{RawPayloadHash: hash, RawRecordLocator: "chart/date=" + key, IngestedAt: ingested, NormalizerVersion: model.NormalizerVersion}, Temporal: model.Temporal{ObservedAt: closeAt, PublishedAt: ingested, PublishedPrecision: model.PrecisionSecond, AvailableAt: ingested, IngestedAt: ingested}}
	}
	bars := make([]model.PriceBar, 0, len(unique))
	for _, v := range unique {
		bars = append(bars, v)
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].Temporal.ObservedAt.Before(bars[j].Temporal.ObservedAt) })
	return bars, received, rejected, nil
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func compareDecimal(a, b string) int {
	x, _ := new(big.Rat).SetString(a)
	y, _ := new(big.Rat).SetString(b)
	return x.Cmp(y)
}
