package b3

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/luisdourado/invs/internal/providers"
)

const (
	// DefaultListedCompaniesBaseURL is B3's public listed-company web
	// application API. It is separate from the public daily-file API used by
	// InstrumentsConsolidated.
	DefaultListedCompaniesBaseURL = "https://sistemaswebb3-listados.b3.com.br/listedCompaniesProxy/CompanyCall/"
	CorporateActionsParserVersion = "b3-listed-corporate-actions-v1"
	CorporateActionsResourceKind  = "corporate_actions"
)

var corporateActionCompanyPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._ -]*$`)

// CorporateActionRequest deliberately takes the B3 issuer/company code used
// by the listed-company application. It is not derived from a ticker: one
// issuer can have several listed securities and ticker-to-issuer inference
// would make the source boundary ambiguous.
type CorporateActionRequest struct {
	IssuingCompany string
	Language       string
}

// CorporateActionResult is source-native evidence. The typed rows retain
// B3's labels and decimal lexemes; they are not canonical CorporateAction
// records and must not be published as adjustments without a separate source
// mapping for announcement/publication and revision semantics.
type CorporateActionResult struct {
	providers.ResourceResult
	Company  CorporateActionCompany
	Actions  []CorporateAction
	Stats    CorporateActionParseStats
	RawCount int
}

type CorporateActionParseStats struct {
	RecordsReceived int
	RecordsRejected int
	Duplicates      int
}

type CorporateActionCompany struct {
	Code                  string
	CVMCode               string
	TradingName           string
	Segment               string
	QuotedPerShareSince   string
	StockCapital          string
	TotalNumberShares     string
	NumberCommonShares    string
	NumberPreferredShares string
	CommonSharesForm      string
	PreferredSharesForm   string
	HasCommon             string
	HasPreferred          string
	RoundLot              string
}

// CorporateAction is a normalized source-native row. Dates are parsed only
// to make the source evidence inspectable; the original date lexemes remain
// in the raw response retained by ResourceResult.
type CorporateAction struct {
	CompanyCode      string
	CompanyCVMCode   string
	Kind             string
	ActionLabel      string
	ISINCode         string
	AssetIssued      string
	ApprovedOn       time.Time
	LastDatePrior    *time.Time
	PaymentDate      *time.Time
	RelatedTo        string
	Rate             string
	Factor           string
	Percentage       string
	PriceUnit        string
	TradingPeriod    string
	SubscriptionDate *time.Time
	Remarks          string
	RawRecordLocator string
}

type corporateActionsResponse struct {
	Code                  string          `json:"code"`
	CodeCVM               string          `json:"codeCVM"`
	TradingName           string          `json:"tradingName"`
	Segment               string          `json:"segment"`
	QuotedPerShareSince   string          `json:"quotedPerSharSince"`
	StockCapital          string          `json:"stockCapital"`
	TotalNumberShares     string          `json:"totalNumberShares"`
	NumberCommonShares    string          `json:"numberCommonShares"`
	NumberPreferredShares string          `json:"numberPreferredShares"`
	CommonSharesForm      string          `json:"commonSharesForm"`
	PreferredSharesForm   string          `json:"preferredSharesForm"`
	HasCommon             string          `json:"hasCommom"`
	HasPreferred          string          `json:"hasPreferred"`
	RoundLot              string          `json:"roundLot"`
	CashDividends         []cashDividend  `json:"cashDividends"`
	StockDividends        []stockDividend `json:"stockDividends"`
	Subscriptions         []subscription  `json:"subscriptions"`
}

type cashDividend struct {
	AssetIssued   string `json:"assetIssued"`
	PaymentDate   string `json:"paymentDate"`
	Rate          string `json:"rate"`
	RelatedTo     string `json:"relatedTo"`
	ApprovedOn    string `json:"approvedOn"`
	ISINCode      string `json:"isinCode"`
	Label         string `json:"label"`
	LastDatePrior string `json:"lastDatePrior"`
	Remarks       string `json:"remarks"`
}

type stockDividend struct {
	AssetIssued   string `json:"assetIssued"`
	Factor        string `json:"factor"`
	ApprovedOn    string `json:"approvedOn"`
	ISINCode      string `json:"isinCode"`
	Label         string `json:"label"`
	LastDatePrior string `json:"lastDatePrior"`
	Remarks       string `json:"remarks"`
}

type subscription struct {
	AssetIssued      string `json:"assetIssued"`
	Percentage       string `json:"percentage"`
	PriceUnit        string `json:"priceUnit"`
	TradingPeriod    string `json:"tradingPeriod"`
	SubscriptionDate string `json:"subscriptionDate"`
	ApprovedOn       string `json:"approvedOn"`
	ISINCode         string `json:"isinCode"`
	Label            string `json:"label"`
	LastDatePrior    string `json:"lastDatePrior"`
	Remarks          string `json:"remarks"`
}

type corporateActionRequestPayload struct {
	IssuingCompany string `json:"issuingCompany"`
	Language       string `json:"language"`
}

func (c *Client) CollectCorporateActions(ctx context.Context, request CorporateActionRequest) (CorporateActionResult, error) {
	if c == nil || c.http == nil {
		return CorporateActionResult{}, errors.New("B3 client HTTP getter is required")
	}
	normalized, err := normalizeCorporateActionRequest(request)
	if err != nil {
		return CorporateActionResult{}, fmt.Errorf("B3 corporate-actions request: %w", err)
	}
	requestURL, err := corporateActionsURL(c.corporateActionsBaseURL(), normalized)
	if err != nil {
		return CorporateActionResult{}, err
	}
	body, err := c.http.Get(ctx, requestURL)
	if err != nil {
		return CorporateActionResult{}, fmt.Errorf("B3 corporate-actions request: %w", err)
	}
	fetchedAt := canonicalTime(c.now())
	resource := providers.NewRawResource(
		CorporateActionsResourceKind,
		"issuing_company="+normalized.IssuingCompany+".json",
		body,
		fetchedAt,
		"application/json",
	)
	resource.URL = requestURL
	resource.ParserVersion = CorporateActionsParserVersion
	resource.ParserMetadata = map[string]string{
		"issuing_company":  normalized.IssuingCompany,
		"language":         normalized.Language,
		"request_url":      requestURL,
		"source_semantics": "current listed-company corporate-action display; source does not expose event publication timestamp or correction revision",
	}
	result := CorporateActionResult{ResourceResult: providers.ResourceResult{Resources: []providers.RawResource{resource}}}
	result.Company, result.Actions, result.Stats, err = parseCorporateActions(body, normalized)
	result.RawCount = result.Stats.RecordsReceived
	if err != nil {
		return result, fmt.Errorf("B3 corporate-actions: %w", err)
	}
	resource.ParserMetadata["cash_dividend_rows"] = fmt.Sprint(countCorporateActions(result.Actions, "cash_dividend"))
	resource.ParserMetadata["stock_action_rows"] = fmt.Sprint(countCorporateActions(result.Actions, "stock_action"))
	resource.ParserMetadata["subscription_rows"] = fmt.Sprint(countCorporateActions(result.Actions, "subscription"))
	return result, nil
}

func (c *Client) corporateActionsBaseURL() string {
	if strings.TrimSpace(c.listedCompaniesBaseURL) != "" {
		return c.listedCompaniesBaseURL
	}
	return DefaultListedCompaniesBaseURL
}

func normalizeCorporateActionRequest(request CorporateActionRequest) (CorporateActionRequest, error) {
	request.IssuingCompany = strings.ToUpper(strings.TrimSpace(request.IssuingCompany))
	if request.IssuingCompany == "" || !corporateActionCompanyPattern.MatchString(request.IssuingCompany) {
		return CorporateActionRequest{}, fmt.Errorf("issuing company %q is not a canonical B3 company code", request.IssuingCompany)
	}
	request.Language = strings.TrimSpace(request.Language)
	if request.Language == "" {
		request.Language = "en-US"
	}
	if request.Language != "en-US" && request.Language != "pt-BR" {
		return CorporateActionRequest{}, fmt.Errorf("language %q must be en-US or pt-BR", request.Language)
	}
	return request, nil
}

func corporateActionsURL(base string, request CorporateActionRequest) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid B3 listed-company base URL %q", base)
	}
	payload, err := json.Marshal(corporateActionRequestPayload(request))
	if err != nil {
		return "", fmt.Errorf("encode B3 corporate-actions request: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(payload)
	pathPrefix := strings.TrimRight(parsed.Path, "/") + "/GetListedSupplementCompany/"
	parsed.Path = pathPrefix + encoded
	parsed.RawPath = strings.TrimRight(parsed.EscapedPath(), "/") + url.PathEscape(encoded)
	return parsed.String(), nil
}

func parseCorporateActions(body []byte, request CorporateActionRequest) (CorporateActionCompany, []CorporateAction, CorporateActionParseStats, error) {
	var response []corporateActionsResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return CorporateActionCompany{}, nil, CorporateActionParseStats{}, fmt.Errorf("decode JSON: %w", err)
	}
	if len(response) != 1 {
		return CorporateActionCompany{}, nil, CorporateActionParseStats{}, fmt.Errorf("expected exactly one listed-company record, got %d", len(response))
	}
	raw := response[0]
	companyCode := strings.ToUpper(strings.TrimSpace(raw.Code))
	if companyCode == "" {
		return CorporateActionCompany{}, nil, CorporateActionParseStats{}, errors.New("listed-company record has no code")
	}
	if companyCode != request.IssuingCompany {
		return CorporateActionCompany{}, nil, CorporateActionParseStats{}, fmt.Errorf("listed-company code %q does not match requested %q", companyCode, request.IssuingCompany)
	}
	company := CorporateActionCompany{
		Code:                  companyCode,
		CVMCode:               strings.TrimSpace(raw.CodeCVM),
		TradingName:           strings.TrimSpace(raw.TradingName),
		Segment:               strings.TrimSpace(raw.Segment),
		QuotedPerShareSince:   strings.TrimSpace(raw.QuotedPerShareSince),
		StockCapital:          strings.TrimSpace(raw.StockCapital),
		TotalNumberShares:     strings.TrimSpace(raw.TotalNumberShares),
		NumberCommonShares:    strings.TrimSpace(raw.NumberCommonShares),
		NumberPreferredShares: strings.TrimSpace(raw.NumberPreferredShares),
		CommonSharesForm:      strings.TrimSpace(raw.CommonSharesForm),
		PreferredSharesForm:   strings.TrimSpace(raw.PreferredSharesForm),
		HasCommon:             strings.TrimSpace(raw.HasCommon),
		HasPreferred:          strings.TrimSpace(raw.HasPreferred),
		RoundLot:              strings.TrimSpace(raw.RoundLot),
	}
	stats := CorporateActionParseStats{}
	actions := make([]CorporateAction, 0, len(raw.CashDividends)+len(raw.StockDividends)+len(raw.Subscriptions))
	for index, item := range raw.CashDividends {
		stats.RecordsReceived++
		action, err := parseCashDividend(item, company, request.Language, index+1)
		if err != nil {
			stats.RecordsRejected++
			return company, nil, stats, err
		}
		actions = append(actions, action)
	}
	for index, item := range raw.StockDividends {
		stats.RecordsReceived++
		action, err := parseStockDividend(item, company, request.Language, index+1)
		if err != nil {
			stats.RecordsRejected++
			return company, nil, stats, err
		}
		actions = append(actions, action)
	}
	for index, item := range raw.Subscriptions {
		stats.RecordsReceived++
		action, err := parseSubscription(item, company, request.Language, index+1)
		if err != nil {
			stats.RecordsRejected++
			return company, nil, stats, err
		}
		actions = append(actions, action)
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i].RawRecordLocator < actions[j].RawRecordLocator })
	return company, actions, stats, nil
}

func parseCashDividend(raw cashDividend, company CorporateActionCompany, language string, row int) (CorporateAction, error) {
	approved, err := requiredCorporateActionDate(raw.ApprovedOn, language)
	if err != nil {
		return CorporateAction{}, fmt.Errorf("cash_dividends row %d approvedOn: %w", row, err)
	}
	isin, err := requiredCorporateActionISIN(raw.ISINCode, "isinCode")
	if err != nil {
		return CorporateAction{}, fmt.Errorf("cash_dividends row %d: %w", row, err)
	}
	lastDate, err := parseCorporateActionDate(raw.LastDatePrior, language, true)
	if err != nil {
		return CorporateAction{}, fmt.Errorf("cash_dividends row %d lastDatePrior: %w", row, err)
	}
	paymentDate, err := parseCorporateActionDate(raw.PaymentDate, language, true)
	if err != nil {
		return CorporateAction{}, fmt.Errorf("cash_dividends row %d paymentDate: %w", row, err)
	}
	return CorporateAction{
		CompanyCode:      company.Code,
		CompanyCVMCode:   company.CVMCode,
		Kind:             "cash_dividend",
		ActionLabel:      strings.TrimSpace(raw.Label),
		ISINCode:         isin,
		AssetIssued:      canonicalISIN(raw.AssetIssued),
		ApprovedOn:       approved,
		LastDatePrior:    lastDate,
		PaymentDate:      paymentDate,
		RelatedTo:        strings.TrimSpace(raw.RelatedTo),
		Rate:             strings.TrimSpace(raw.Rate),
		Remarks:          strings.TrimSpace(raw.Remarks),
		RawRecordLocator: corporateActionLocator(company.Code, "cash_dividends", row, raw.ISINCode, raw.Label, raw.ApprovedOn),
	}, nil
}

func parseStockDividend(raw stockDividend, company CorporateActionCompany, language string, row int) (CorporateAction, error) {
	approved, err := requiredCorporateActionDate(raw.ApprovedOn, language)
	if err != nil {
		return CorporateAction{}, fmt.Errorf("stock_dividends row %d approvedOn: %w", row, err)
	}
	isin, err := requiredCorporateActionISIN(raw.ISINCode, "isinCode")
	if err != nil {
		return CorporateAction{}, fmt.Errorf("stock_dividends row %d: %w", row, err)
	}
	lastDate, err := parseCorporateActionDate(raw.LastDatePrior, language, true)
	if err != nil {
		return CorporateAction{}, fmt.Errorf("stock_dividends row %d lastDatePrior: %w", row, err)
	}
	return CorporateAction{
		CompanyCode:      company.Code,
		CompanyCVMCode:   company.CVMCode,
		Kind:             "stock_action",
		ActionLabel:      strings.TrimSpace(raw.Label),
		ISINCode:         isin,
		AssetIssued:      canonicalISIN(raw.AssetIssued),
		ApprovedOn:       approved,
		LastDatePrior:    lastDate,
		Factor:           strings.TrimSpace(raw.Factor),
		Remarks:          strings.TrimSpace(raw.Remarks),
		RawRecordLocator: corporateActionLocator(company.Code, "stock_dividends", row, raw.ISINCode, raw.Label, raw.ApprovedOn),
	}, nil
}

func parseSubscription(raw subscription, company CorporateActionCompany, language string, row int) (CorporateAction, error) {
	approved, err := requiredCorporateActionDate(raw.ApprovedOn, language)
	if err != nil {
		return CorporateAction{}, fmt.Errorf("subscriptions row %d approvedOn: %w", row, err)
	}
	isin, err := requiredCorporateActionISIN(raw.ISINCode, "isinCode")
	if err != nil {
		return CorporateAction{}, fmt.Errorf("subscriptions row %d: %w", row, err)
	}
	lastDate, err := parseCorporateActionDate(raw.LastDatePrior, language, true)
	if err != nil {
		return CorporateAction{}, fmt.Errorf("subscriptions row %d lastDatePrior: %w", row, err)
	}
	subscriptionDate, err := parseCorporateActionDate(raw.SubscriptionDate, language, true)
	if err != nil {
		return CorporateAction{}, fmt.Errorf("subscriptions row %d subscriptionDate: %w", row, err)
	}
	return CorporateAction{
		CompanyCode:      company.Code,
		CompanyCVMCode:   company.CVMCode,
		Kind:             "subscription",
		ActionLabel:      strings.TrimSpace(raw.Label),
		ISINCode:         isin,
		AssetIssued:      canonicalISIN(raw.AssetIssued),
		ApprovedOn:       approved,
		LastDatePrior:    lastDate,
		Percentage:       strings.TrimSpace(raw.Percentage),
		PriceUnit:        strings.TrimSpace(raw.PriceUnit),
		TradingPeriod:    strings.TrimSpace(raw.TradingPeriod),
		SubscriptionDate: subscriptionDate,
		Remarks:          strings.TrimSpace(raw.Remarks),
		RawRecordLocator: corporateActionLocator(company.Code, "subscriptions", row, raw.ISINCode, raw.Label, raw.ApprovedOn),
	}, nil
}

func requiredCorporateActionDate(value, language string) (time.Time, error) {
	parsed, err := parseCorporateActionDate(value, language, false)
	if err != nil {
		return time.Time{}, err
	}
	if parsed == nil {
		return time.Time{}, errors.New("date is required")
	}
	return *parsed, nil
}

func requiredCorporateActionISIN(value, field string) (string, error) {
	value = canonicalISIN(value)
	if !isinPattern.MatchString(value) {
		return "", fmt.Errorf("%s must be a valid uppercase ISIN, got %q", field, value)
	}
	return value, nil
}

func parseCorporateActionDate(value, language string, optional bool) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		if optional {
			return nil, nil
		}
		return nil, errors.New("date is required")
	}
	if value == "01/01/1900" || value == "31/12/9999" || value == "12/31/9999" {
		if optional {
			return nil, nil
		}
		return nil, fmt.Errorf("date %q is a source sentinel", value)
	}
	layout := "01/02/2006"
	if language == "pt-BR" {
		layout = "02/01/2006"
	}
	parsed, err := time.Parse(layout, value)
	if err != nil {
		return nil, fmt.Errorf("must be %s: %w", layout, err)
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func corporateActionLocator(company, section string, row int, isin, label, approved string) string {
	return fmt.Sprintf("corporate_actions/issuing_company=%s/section=%s/row=%d/isin=%s/label=%s/approved_on=%s", company, section, row, sanitizeLocatorValue(canonicalISIN(isin)), sanitizeLocatorValue(label), sanitizeLocatorValue(approved))
}

func sanitizeLocatorValue(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return "unspecified"
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func canonicalISIN(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func countCorporateActions(actions []CorporateAction, kind string) int {
	count := 0
	for _, action := range actions {
		if action.Kind == kind {
			count++
		}
	}
	return count
}
