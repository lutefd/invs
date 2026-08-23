package b3

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/luisdourado/invs/internal/providers"
)

type corporateActionsGetter struct {
	body     []byte
	requests []string
}

func (g *corporateActionsGetter) Get(_ context.Context, requestURL string) ([]byte, error) {
	g.requests = append(g.requests, requestURL)
	return append([]byte(nil), g.body...), nil
}

func corporateActionsFixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/corporate-actions-petr.json")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestCollectCorporateActionsParsesAllB3SectionsAndRetainsRaw(t *testing.T) {
	body := corporateActionsFixture(t)
	getter := &corporateActionsGetter{body: body}
	client := NewClient(getter)
	client.listedCompaniesBaseURL = "https://b3.test/listedCompaniesProxy/CompanyCall/"
	client.now = func() time.Time {
		return time.Date(2026, 8, 23, 14, 15, 16, 123456789, time.FixedZone("BRT", -3*60*60))
	}

	result, err := client.CollectCorporateActions(context.Background(), CorporateActionRequest{IssuingCompany: "petr"})
	if err != nil {
		t.Fatal(err)
	}
	if len(getter.requests) != 1 {
		t.Fatalf("requests = %#v", getter.requests)
	}
	parsedURL, err := url.Parse(getter.requests[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(parsedURL.Path, "/GetListedSupplementCompany/") {
		t.Fatalf("corporate-actions path = %q", parsedURL.Path)
	}
	encoded := strings.TrimPrefix(parsedURL.Path, "/listedCompaniesProxy/CompanyCall/GetListedSupplementCompany/")
	requestPayload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode request payload: %v", err)
	}
	var request map[string]string
	if err := json.Unmarshal(requestPayload, &request); err != nil {
		t.Fatal(err)
	}
	if request["issuingCompany"] != "PETR" || request["language"] != "en-US" {
		t.Fatalf("request payload = %#v", request)
	}

	if len(result.Resources) != 1 || result.Resources[0].Kind != CorporateActionsResourceKind || result.Resources[0].ParserVersion != CorporateActionsParserVersion {
		t.Fatalf("resources = %+v", result.Resources)
	}
	if result.Resources[0].SHA256 != providers.SHA256(body) || string(result.Resources[0].Bytes) != string(body) {
		t.Fatal("raw corporate-action response was not retained exactly")
	}
	if result.Company.Code != "PETR" || result.Company.CVMCode != "9512" || result.Company.TradingName != "PETROBRAS" {
		t.Fatalf("company = %+v", result.Company)
	}
	if result.Stats.RecordsReceived != 3 || result.Stats.RecordsRejected != 0 || result.Stats.Duplicates != 0 || len(result.Actions) != 3 {
		t.Fatalf("stats/actions = %+v/%+v", result.Stats, result.Actions)
	}

	cash := result.Actions[0]
	if cash.Kind != "cash_dividend" || cash.ActionLabel != "DIVIDENDO" || cash.ISINCode != "BRPETRACNOR9" || cash.Rate != "0.47156696000" || cash.PaymentDate == nil || cash.LastDatePrior == nil {
		t.Fatalf("cash action = %+v", cash)
	}
	if !cash.ApprovedOn.Equal(time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)) || !cash.LastDatePrior.Equal(time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)) || !cash.PaymentDate.Equal(time.Date(2026, 12, 21, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("cash dates = %+v", cash)
	}
	stock := result.Actions[1]
	if stock.Kind != "stock_action" || stock.ActionLabel != "DESDOBRAMENTO" || stock.Factor != "100.00000000000" || stock.PaymentDate != nil {
		t.Fatalf("stock action = %+v", stock)
	}
	subscription := result.Actions[2]
	if subscription.Kind != "subscription" || subscription.ActionLabel != "SUBSCRICAO" || subscription.Percentage != "20.00000000000" || subscription.PriceUnit != "1.00000000000" || subscription.SubscriptionDate != nil {
		t.Fatalf("subscription = %+v", subscription)
	}
	if result.Resources[0].ParserMetadata["source_semantics"] == "" || result.Resources[0].ParserMetadata["cash_dividend_rows"] != "1" {
		t.Fatalf("parser metadata = %+v", result.Resources[0].ParserMetadata)
	}
}

func TestCollectCorporateActionsPreservesRawOnParseFailure(t *testing.T) {
	body := []byte(`[{"code":"PETR","cashDividends":[{"approvedOn":"not-a-date"}]}]`)
	getter := &corporateActionsGetter{body: body}
	client := NewClient(getter)
	result, err := client.CollectCorporateActions(context.Background(), CorporateActionRequest{IssuingCompany: "PETR", Language: "en-US"})
	if err == nil || !strings.Contains(err.Error(), "approvedOn") {
		t.Fatalf("error = %v, want approvedOn parse error", err)
	}
	if len(result.Resources) != 1 || string(result.Resources[0].Bytes) != string(body) {
		t.Fatalf("raw response was not retained on parse failure: %+v", result.Resources)
	}
	if result.Stats.RecordsReceived != 1 || result.Stats.RecordsRejected != 1 {
		t.Fatalf("stats = %+v", result.Stats)
	}
}

func TestNormalizeCorporateActionRequestRejectsAmbiguousInputs(t *testing.T) {
	for name, request := range map[string]CorporateActionRequest{
		"missing company":      {Language: "en-US"},
		"unsafe company":       {IssuingCompany: "PETR/4", Language: "en-US"},
		"unsupported language": {IssuingCompany: "PETR", Language: "es-ES"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeCorporateActionRequest(request); err == nil {
				t.Fatal("invalid corporate-action request accepted")
			}
		})
	}
}

func TestParseCorporateActionDateUsesLanguageAndRecognizesSentinels(t *testing.T) {
	pt, err := parseCorporateActionDate("21/08/2026", "pt-BR", false)
	if err != nil || pt == nil || !pt.Equal(time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("pt-BR date = %v, %v", pt, err)
	}
	nilDate, err := parseCorporateActionDate("12/31/9999", "en-US", true)
	if err != nil || nilDate != nil {
		t.Fatalf("sentinel date = %v, %v", nilDate, err)
	}
}
