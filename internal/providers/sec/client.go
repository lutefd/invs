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
	factBytes, err := c.http.Get(ctx, factsURL)
	if err != nil {
		return CompanyResult{}, fmt.Errorf("SEC companyfacts CIK %d: %w", cik, err)
	}
	ingested := c.now().UTC()
	issuer, filings, receivedFilings, rejectedFilings, err := parseSubmissions(subBytes, issuerID, cik, ingested)
	if err != nil {
		return CompanyResult{}, err
	}
	facts, receivedFacts, rejectedFacts, err := parseCompanyFacts(factBytes, issuerID, ingested)
	if err != nil {
		return CompanyResult{}, err
	}
	return CompanyResult{
		Issuer: issuer, Filings: filings, Facts: facts,
		Raw:             []RawDocument{{Kind: "submissions", Data: subBytes, SHA256: digest(subBytes)}, {Kind: "companyfacts", Data: factBytes, SHA256: digest(factBytes)}},
		RecordsReceived: receivedFilings + receivedFacts, RecordsRejected: rejectedFilings + rejectedFacts,
	}, nil
}

type submissions struct {
	Name, StateOfIncorporation, SICDescription string
	SIC                                        json.Number `json:"sic"`
	Filings                                    struct {
		Recent recentFilings `json:"recent"`
	} `json:"filings"`
}
type recentFilings struct {
	AccessionNumber, FilingDate, AcceptanceDateTime, Form, PrimaryDocument []string
}

func parseSubmissions(b []byte, issuerID string, cik int64, ingested time.Time) (model.Issuer, []model.Filing, int, int, error) {
	var raw submissions
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return model.Issuer{}, nil, 0, 0, fmt.Errorf("decode SEC submissions: %w", err)
	}
	issuer := model.Issuer{ID: issuerID, LegalName: raw.Name, Country: raw.StateOfIncorporation, Industry: raw.SICDescription, CIK: cik}
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

func parseCompanyFacts(b []byte, issuerID string, ingested time.Time) ([]model.FundamentalObservation, int, int, error) {
	var raw companyFacts
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, 0, 0, fmt.Errorf("decode SEC companyfacts: %w", err)
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
					value, valueErr := strconv.ParseFloat(f.Val.String(), 64)
					if endErr != nil || filedErr != nil || valueErr != nil || f.Accn == "" {
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
					available := filed.AddDate(0, 0, 1)
					o := model.FundamentalObservation{
						Source: "sec", IssuerID: issuerID, Taxonomy: taxonomy, Concept: concept, Unit: unit, Value: value,
						Temporal:    model.Temporal{ObservedAt: end.UTC(), PublishedAt: filed, PublishedPrecision: model.PrecisionDate, AvailableAt: available, IngestedAt: ingested},
						PeriodStart: start, PeriodEnd: end.UTC(), AccessionNumber: f.Accn, Form: f.Form, FiscalYear: fy, FiscalPeriod: f.FP, Frame: f.Frame, RawPayloadHash: hash,
					}
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
