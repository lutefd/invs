package sec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/luisdourado/invs/internal/model"
)

const defaultBaseURL = "https://data.sec.gov"

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

type RawDocument struct {
	Kind   string
	Data   []byte
	SHA256 string
}
type CompanyResult struct {
	Issuer                           model.Issuer
	StateOfIncorporation             string
	Filings                          []model.Filing
	Facts                            []model.FundamentalObservation
	Raw                              []RawDocument
	RecordsReceived, RecordsRejected int
}

func (c *Client) CollectCompany(ctx context.Context, issuerID string, cik int64) (CompanyResult, error) {
	if issuerID == "" || cik <= 0 {
		return CompanyResult{}, fmt.Errorf("issuer ID and positive CIK are required")
	}
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return CompanyResult{}, err
	}
	subURL := base.ResolveReference(&url.URL{Path: fmt.Sprintf("/submissions/CIK%010d.json", cik)}).String()
	factsURL := base.ResolveReference(&url.URL{Path: fmt.Sprintf("/api/xbrl/companyfacts/CIK%010d.json", cik)}).String()
	subBytes, err := c.http.Get(ctx, subURL)
	if err != nil {
		return CompanyResult{}, fmt.Errorf("SEC submissions CIK %d: %w", cik, err)
	}
	rawDocuments := []RawDocument{
		{Kind: "submissions", Data: subBytes, SHA256: digest(subBytes)},
	}
	factBytes, err := c.http.Get(ctx, factsURL)
	if err != nil {
		return CompanyResult{Raw: rawDocuments}, fmt.Errorf("SEC companyfacts CIK %d: %w", cik, err)
	}
	rawDocuments = append(rawDocuments, RawDocument{Kind: "companyfacts", Data: factBytes, SHA256: digest(factBytes)})
	ingested := c.now().UTC().Truncate(time.Microsecond)
	issuer, filings, receivedFilings, rejectedFilings, err := parseSubmissions(subBytes, issuerID, cik, ingested)
	if err != nil {
		return CompanyResult{Raw: rawDocuments}, err
	}
	acceptedByAccession := make(map[string]time.Time, len(filings))
	for _, filing := range filings {
		if filing.AcceptedAt != nil {
			acceptedByAccession[filing.AccessionNumber] = *filing.AcceptedAt
		}
	}
	facts, receivedFacts, rejectedFacts, err := parseCompanyFacts(factBytes, issuerID, cik, ingested, acceptedByAccession)
	if err != nil {
		return CompanyResult{Raw: rawDocuments}, err
	}
	return CompanyResult{
		Issuer: issuer, StateOfIncorporation: submissionState(subBytes), Filings: filings, Facts: facts,
		Raw:             rawDocuments,
		RecordsReceived: receivedFilings + receivedFacts, RecordsRejected: rejectedFilings + rejectedFacts,
	}, nil
}

func submissionState(b []byte) string {
	var raw struct {
		State string `json:"stateOfIncorporation"`
	}
	_ = json.Unmarshal(b, &raw)
	return raw.State
}

type submissions struct {
	Name, StateOfIncorporation, SICDescription string
	CIK                                        flexibleNumber `json:"cik"`
	SIC                                        flexibleNumber `json:"sic"`
	Filings                                    struct {
		Recent recentFilings `json:"recent"`
	} `json:"filings"`
}
type flexibleNumber string

func (n *flexibleNumber) UnmarshalJSON(b []byte) error {
	var s string
	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
	} else {
		s = string(b)
	}
	if _, err := strconv.ParseInt(s, 10, 64); err != nil {
		return fmt.Errorf("invalid numeric value %q", s)
	}
	*n = flexibleNumber(s)
	return nil
}

type recentFilings struct {
	AccessionNumber, FilingDate, AcceptanceDateTime, Form, PrimaryDocument []string
}

func parseSubmissions(b []byte, issuerID string, cik int64, ingested time.Time) (model.Issuer, []model.Filing, int, int, error) {
	ingested = ingested.UTC().Truncate(time.Microsecond)
	var raw submissions
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return model.Issuer{}, nil, 0, 0, fmt.Errorf("decode SEC submissions: %w", err)
	}
	parsedCIK, err := strconv.ParseInt(string(raw.CIK), 10, 64)
	if err != nil || parsedCIK != cik {
		return model.Issuer{}, nil, 0, 0, fmt.Errorf("SEC submissions CIK mismatch: got %q want %d", raw.CIK, cik)
	}
	if strings.TrimSpace(raw.Name) == "" {
		return model.Issuer{}, nil, 0, 0, fmt.Errorf("SEC submissions missing legal name")
	}
	// StateOfIncorporation is retained as source metadata, never interpreted as an ISO country.
	issuer := model.Issuer{ID: issuerID, LegalName: raw.Name, Industry: raw.SICDescription, CIK: cik}
	r := raw.Filings.Recent
	received := len(r.AccessionNumber)
	rejected := 0
	seen := make(map[string]struct{}, received)
	filings := make([]model.Filing, 0, received)
	for i, accession := range r.AccessionNumber {
		if accession == "" || i >= len(r.FilingDate) || i >= len(r.Form) {
			rejected++
			continue
		}
		filed, err := time.Parse("2006-01-02", r.FilingDate[i])
		if err != nil {
			rejected++
			continue
		}
		if _, exists := seen[accession]; exists {
			continue
		}
		seen[accession] = struct{}{}
		var accepted *time.Time
		if i < len(r.AcceptanceDateTime) && r.AcceptanceDateTime[i] != "" {
			if at, err := time.Parse("2006-01-02T15:04:05.000Z", r.AcceptanceDateTime[i]); err == nil {
				at = at.UTC()
				accepted = &at
			}
		}
		primary := ""
		if i < len(r.PrimaryDocument) {
			primary = r.PrimaryDocument[i]
		}
		filings = append(filings, model.Filing{Source: "sec", IssuerID: issuerID, AccessionNumber: accession, Form: r.Form[i], PrimaryDocument: primary, FiledDate: filed.UTC(), AcceptedAt: accepted, IngestedAt: ingested})
	}
	sort.Slice(filings, func(i, j int) bool { return filings[i].AccessionNumber < filings[j].AccessionNumber })
	return issuer, filings, received, rejected, nil
}

type companyFacts struct {
	CIK   int64 `json:"cik"`
	Facts map[string]map[string]struct {
		Label string            `json:"label"`
		Units map[string][]fact `json:"units"`
	} `json:"facts"`
}
type fact struct {
	Start, End, Filed, Accn, FP, Form, Frame string
	FY                                       json.Number `json:"fy"`
	Val                                      json.Number `json:"val"`
}

func parseCompanyFacts(b []byte, issuerID string, expectedCIK int64, ingested time.Time, acceptedByAccession map[string]time.Time) ([]model.FundamentalObservation, int, int, error) {
	ingested = ingested.UTC().Truncate(time.Microsecond)
	var raw companyFacts
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, 0, 0, fmt.Errorf("decode SEC companyfacts: %w", err)
	}
	if raw.CIK != expectedCIK {
		return nil, 0, 0, fmt.Errorf("SEC companyfacts CIK mismatch: got %d want %d", raw.CIK, expectedCIK)
	}
	hash := digest(b)
	received, rejected := 0, 0
	unique := map[string]model.FundamentalObservation{}
	for taxonomy, concepts := range raw.Facts {
		for concept, definition := range concepts {
			for unit, facts := range definition.Units {
				for _, f := range facts {
					received++
					end, endErr := time.Parse("2006-01-02", f.End)
					filed, filedErr := time.Parse("2006-01-02", f.Filed)
					value, decimalErr := model.CanonicalDecimal(f.Val.String(), false)
					if endErr != nil || filedErr != nil || decimalErr != nil || f.Accn == "" {
						rejected++
						continue
					}
					var start *time.Time
					if f.Start != "" {
						if v, err := time.Parse("2006-01-02", f.Start); err == nil {
							v = v.UTC()
							start = &v
						} else {
							rejected++
							continue
						}
					}
					fy, _ := strconv.Atoi(f.FY.String())
					filed = filed.UTC()
					published, available, precision := filed.Add(48*time.Hour), filed.Add(48*time.Hour), model.PrecisionDate
					if accepted, ok := acceptedByAccession[f.Accn]; ok {
						published, available, precision = accepted.UTC(), accepted.UTC(), model.PrecisionSecond
					}
					currency := ""
					if isISOCurrency(unit) {
						currency = unit
					}
					o := model.FundamentalObservation{
						Source: "sec", IssuerID: issuerID, Taxonomy: taxonomy, Concept: concept, Unit: unit, Value: value,
						Currency: currency, Revision: 0,
						Temporal:    model.Temporal{ObservedAt: end.UTC(), ObservedPrecision: model.PrecisionDate, PublishedAt: published, PublishedPrecision: precision, AvailableAt: available, IngestedAt: ingested},
						PeriodStart: start, PeriodEnd: end.UTC(), AccessionNumber: f.Accn, Form: f.Form, FiscalYear: fy, FiscalPeriod: fiscalPeriod(f.FP, start), Frame: f.Frame, RawPayloadHash: hash, Provenance: model.Provenance{RawPayloadHash: hash, IngestedAt: ingested, NormalizerVersion: model.NormalizerVersion},
					}
					o.Provenance.RawRecordLocator = strings.Join([]string{"companyfacts", "taxonomy=" + taxonomy, "concept=" + concept, "unit=" + unit, "accn=" + f.Accn, "start=" + f.Start, "end=" + f.End, "frame=" + f.Frame}, "/")
					key := strings.Join([]string{issuerID, taxonomy, concept, unit, f.Accn, f.Start, f.End, f.Frame}, "\x1f")
					unique[key] = o
				}
			}
		}
	}
	result := make([]model.FundamentalObservation, 0, len(unique))
	for _, o := range unique {
		result = append(result, o)
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if !a.PeriodEnd.Equal(b.PeriodEnd) {
			return a.PeriodEnd.Before(b.PeriodEnd)
		}
		if a.Concept != b.Concept {
			return a.Concept < b.Concept
		}
		if a.Unit != b.Unit {
			return a.Unit < b.Unit
		}
		return a.AccessionNumber < b.AccessionNumber
	})
	return result, received, rejected, nil
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func isISOCurrency(v string) bool {
	if len(v) != 3 {
		return false
	}
	for _, r := range v {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}
func fiscalPeriod(v string, start *time.Time) string {
	switch v {
	case "FY", "Q1", "Q2", "Q3", "Q4", "H1", "H2", "YTD":
		return v
	}
	if start == nil {
		return "instant"
	}
	return "other"
}
