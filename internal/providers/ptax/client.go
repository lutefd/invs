package ptax

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/luisdourado/invs/internal/model"
	"github.com/luisdourado/invs/internal/providers"
)

const (
	defaultBaseURL = "https://olinda.bcb.gov.br/olinda/servico/PTAX/versao/v1/odata/"
	endpoint       = "CotacaoDolarPeriodo(dataInicial=@dataInicial,dataFinalCotacao=@dataFinalCotacao)"
)

var observationNamespace = uuid.MustParse("abecf59b-46fc-5c31-a284-dcf4bf70e57a")

type Getter interface {
	Get(context.Context, string) ([]byte, error)
}

type Client struct {
	http    Getter
	baseURL string
	now     func() time.Time
}

type Request struct {
	Start time.Time
	End   time.Time
}

type Result struct {
	providers.ResourceResult
	Observations    []model.FXObservation
	Raw             []byte
	SHA256          string
	RecordsReceived int
}

func NewClient(http Getter) *Client {
	return &Client{http: http, baseURL: defaultBaseURL, now: time.Now}
}

func (c *Client) Collect(ctx context.Context, request Request) (Result, error) {
	requestURL, err := periodURL(c.baseURL, request)
	if err != nil {
		return Result{}, err
	}
	body, err := c.http.Get(ctx, requestURL)
	if err != nil {
		return Result{}, fmt.Errorf("BCB PTAX USD/BRL: %w", err)
	}
	fetchedAt := c.now().UTC().Truncate(time.Microsecond)
	resource := providers.NewRawResource(
		"closing_bulletins", "USD-BRL", body, fetchedAt, "application/json",
	)
	result := Result{
		ResourceResult: providers.ResourceResult{Resources: []providers.RawResource{resource}},
		Raw:            resource.Bytes,
		SHA256:         resource.SHA256,
	}
	observations, received, err := parse(resource.Bytes, fetchedAt)
	result.Observations, result.RecordsReceived = observations, received
	if err != nil {
		return result, err
	}
	return result, nil
}

func periodURL(baseURL string, request Request) (string, error) {
	start, end, err := validateRequest(request)
	if err != nil {
		return "", err
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse PTAX base URL: %w", err)
	}
	resource, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse PTAX endpoint: %w", err)
	}
	u := base.ResolveReference(resource)
	query := u.Query()
	query.Set("@dataInicial", "'"+start.Format("01-02-2006")+"'")
	query.Set("@dataFinalCotacao", "'"+end.Format("01-02-2006")+"'")
	query.Set("$format", "json")
	query.Set("$select", "cotacaoCompra,cotacaoVenda,dataHoraCotacao")
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func validateRequest(request Request) (time.Time, time.Time, error) {
	if request.Start.IsZero() || request.End.IsZero() {
		return time.Time{}, time.Time{}, errors.New("PTAX start and end dates are required")
	}
	start := request.Start.UTC()
	end := request.End.UTC()
	if !isMidnight(start) || !isMidnight(end) {
		return time.Time{}, time.Time{}, errors.New("PTAX start and end must be UTC dates")
	}
	if start.Before(time.Date(1984, 11, 28, 0, 0, 0, 0, time.UTC)) {
		return time.Time{}, time.Time{}, errors.New("PTAX start precedes available USD/BRL history")
	}
	if end.Before(start) {
		return time.Time{}, time.Time{}, errors.New("PTAX end must not precede start")
	}
	if int(end.Sub(start).Hours()/24)+1 > 366 {
		return time.Time{}, time.Time{}, errors.New("PTAX range must not exceed 366 inclusive days")
	}
	return start, end, nil
}

func isMidnight(value time.Time) bool {
	return value.Hour() == 0 && value.Minute() == 0 && value.Second() == 0 && value.Nanosecond() == 0
}

type response struct {
	Context string `json:"@odata.context"`
	Value   []row  `json:"value"`
}

type row struct {
	Buy      json.Number `json:"cotacaoCompra"`
	Sell     json.Number `json:"cotacaoVenda"`
	FixingAt string      `json:"dataHoraCotacao"`
}

func parse(body []byte, fetchedAt time.Time) ([]model.FXObservation, int, error) {
	var document response
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, 0, fmt.Errorf("decode BCB PTAX response: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, 0, err
	}
	if document.Context == "" || document.Value == nil {
		return nil, 0, errors.New("unexpected BCB PTAX response")
	}
	location, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return nil, 0, fmt.Errorf("load PTAX timezone: %w", err)
	}
	seen := make(map[string]bool, len(document.Value))
	observations := make([]model.FXObservation, 0, len(document.Value))
	for index, candidate := range document.Value {
		buy, err := model.CanonicalDecimal(candidate.Buy.String(), true)
		if err != nil || buy == "0" {
			return nil, index + 1, fmt.Errorf("BCB PTAX row %d has invalid buy rate", index)
		}
		sell, err := model.CanonicalDecimal(candidate.Sell.String(), true)
		if err != nil || sell == "0" {
			return nil, index + 1, fmt.Errorf("BCB PTAX row %d has invalid sell rate", index)
		}
		if compareDecimal(buy, sell) > 0 {
			return nil, index + 1, fmt.Errorf("BCB PTAX row %d buy rate exceeds sell rate", index)
		}
		localFixing, err := time.ParseInLocation(
			"2006-01-02 15:04:05.999999", candidate.FixingAt, location,
		)
		if err != nil {
			return nil, index + 1, fmt.Errorf("BCB PTAX row %d has invalid fixing timestamp", index)
		}
		fixingAt := localFixing.UTC().Truncate(time.Microsecond)
		if fixingAt.After(fetchedAt) {
			return nil, index + 1, fmt.Errorf("BCB PTAX row %d is dated after receipt", index)
		}
		sourceRecordID := "ptax-closing/USD-BRL/" + localFixing.Format(time.RFC3339Nano)
		if seen[sourceRecordID] {
			return nil, index + 1, fmt.Errorf("BCB PTAX duplicate source record %s", sourceRecordID)
		}
		seen[sourceRecordID] = true
		id := uuid.NewSHA1(observationNamespace, []byte(sourceRecordID+"\x00revision=0"))
		observation := model.FXObservation{
			ID: id.String(), Source: "bcb_ptax", BaseCurrency: "USD", QuoteCurrency: "BRL",
			RateKind: "ptax_closing", FixingTimezone: "America/Sao_Paulo",
			BuyRate: buy, SellRate: sell, FixingAt: fixingAt, PublishedAt: fixingAt,
			AvailableAt: fixingAt, RecordedAt: fetchedAt, Revision: 0,
			SourceRecordID: sourceRecordID, RawPayloadHash: providers.SHA256(body),
			Provenance: model.Provenance{
				RawPayloadHash: providers.SHA256(body), RawRecordLocator: fmt.Sprintf("value/%d", index),
				IngestedAt: fetchedAt, NormalizerVersion: "bcb-ptax-v1",
			},
		}
		observations = append(observations, observation)
	}
	sort.Slice(observations, func(i, j int) bool {
		return observations[i].FixingAt.Before(observations[j].FixingAt)
	})
	return observations, len(document.Value), nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("decode BCB PTAX response: trailing JSON value")
	}
	return nil
}

func compareDecimal(left, right string) int {
	leftValue, _ := new(big.Rat).SetString(left)
	rightValue, _ := new(big.Rat).SetString(right)
	return leftValue.Cmp(rightValue)
}
