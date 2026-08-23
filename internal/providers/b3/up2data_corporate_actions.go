package b3

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/luisdourado/invs/internal/providers"
	"golang.org/x/text/encoding/charmap"
)

const (
	// UP2DataCorporateActionLifecycleParserVersion identifies the documented
	// CorporateActionLifeCycleFileV2 layout. This parser is deliberately
	// transport-agnostic: UP2DATA delivery and credentials are not part of the
	// public B3 provider boundary.
	UP2DataCorporateActionLifecycleParserVersion = "b3-up2data-corporate-action-lifecycle-v2"
	UP2DataCorporateActionLifecycleResourceKind  = "corporate_action_lifecycle"
)

var up2DataCorporateActionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

var up2DataCorporateActionLifecycleHeader = []string{
	"RptDt", "CorpActnCtrlNb", "PblctnDt", "OrgnInf", "TckrSymb", "SctyId", "SctySrc", "MktIdrCd",
	"OrgnNgtnFctr", "ISINPdct", "DstnNgtnFctr", "ISINRqst", "ISINRslt", "DstrbtnId", "CrpnNm",
	"SpcfctnCd", "CorpActnEvtTpCd", "CorpActnDesc", "NtceTp", "NtceDt", "RefDt", "SpclExDt", "PmtDt",
	"SbcptStartDt", "SbcptEndDt", "TradgEndDt", "AssgnmtEndDt", "TrfEndDt", "EvtVal", "SbcptFinVal",
	"BonusVal", "FrctnTrtmnt", "EvtActnTpCd", "PricFctr", "ClctnSeq", "Lk", "DstrbtnPdct", "DstrbtnDstn",
	"MtgUpdRsnTxt", "CrtApprvlDt", "CorpActnCrrctnInd", "IndxShrtNm", "UpdtdEvtVal", "ErngVal",
	"StartDtCrrctn", "EndDtCrrctn", "PmtInstlmtNb", "PmtInstlmtQty", "EvtInstlmtVal", "TtlShrBFROEvt",
	"TtlShrAftrEvt", "ShrPpsnBFROEvt", "ShrPpsnAftrEvt", "DaysToPosAdjstmnt", "ShrSpltRghtPrtcptnTp",
	"AuctnShrQty", "AuctnDt",
}

// UP2DataCorporateActionLifecycleResult is source-native evidence from one
// CorporateActionLifeCycleFileV2 CSV member. It is not a canonical
// CorporateAction result: publication is date-only, and the source action
// fields still require an explicit mapping and availability policy.
type UP2DataCorporateActionLifecycleResult struct {
	providers.ResourceResult
	Events []UP2DataCorporateActionLifecycleEvent
	Stats  UP2DataCorporateActionLifecycleParseStats
}

type UP2DataCorporateActionLifecycleParseStats struct {
	RecordsReceived int
	RecordsRejected int
	Duplicates      int
}

// UP2DataCorporateActionLifecycleEvent retains the fields needed to inspect
// event identity, source publication/report dates, security linkage, action
// state, and economic dates. Decimal and source code fields remain lexemes so
// the parser cannot silently round or reinterpret the product file.
type UP2DataCorporateActionLifecycleEvent struct {
	ReportDate                   time.Time
	SourceEventID                string
	PublicationDate              time.Time
	OriginInformation            string
	Ticker                       string
	SecurityID                   string
	SecuritySource               string
	MarketIdentifierCode         string
	OriginNegotiationFactor      string
	ProductISIN                  string
	DestinationNegotiationFactor string
	RequestISIN                  string
	ResultISIN                   string
	DistributionID               string
	CompanyName                  string
	Specification                string
	EventTypeCode                string
	EventDescription             string
	NoticeType                   string
	NoticeDate                   *time.Time
	ReferenceDate                *time.Time
	SpecialExDate                *time.Time
	PaymentDate                  *time.Time
	SubscriptionStartDate        *time.Time
	SubscriptionEndDate          *time.Time
	TradingEndDate               *time.Time
	AssignmentEndDate            *time.Time
	TransferEndDate              *time.Time
	EventValue                   string
	SubscriptionFinalValue       string
	BonusValue                   string
	FractionTreatment            string
	EventActionType              string
	PriceFactor                  string
	CollectionSequence           string
	Link                         string
	ProductDistributionID        string
	DestinationDistributionID    string
	MeetingUpdateReason          string
	ApprovalDate                 *time.Time
	CorrectionIndicator          string
	IndexShortName               string
	UpdatedEventValue            string
	EarningsValue                string
	CorrectionStartDate          *time.Time
	CorrectionEndDate            *time.Time
	PaymentInstallmentNumber     string
	PaymentInstallmentQuantity   string
	EventInstallmentValue        string
	TotalSharesBeforeEvent       string
	TotalSharesAfterEvent        string
	SharePositionBeforeEvent     string
	SharePositionAfterEvent      string
	DaysToPositionAdjustment     string
	ShareSplitRightParticipation string
	AuctionShareQuantity         string
	AuctionDate                  *time.Time
	RawFields                    []string
	RawRecordLocator             string
}

// ParseUP2DataCorporateActionLifecycle parses one B3 UP2DATA
// CorporateActionLifeCycleFileV2 CSV member and retains the exact bytes as a
// raw resource before returning any parse error. member is the product file
// name or archive member used to make row locators reproducible.
func ParseUP2DataCorporateActionLifecycle(body []byte, member string, fetchedAt time.Time) (UP2DataCorporateActionLifecycleResult, error) {
	member = strings.TrimSpace(member)
	if member == "" {
		return UP2DataCorporateActionLifecycleResult{}, errors.New("B3 UP2DATA corporate-action lifecycle member is required")
	}
	resource := providers.NewRawResource(
		UP2DataCorporateActionLifecycleResourceKind,
		member,
		body,
		fetchedAt,
		"text/csv; charset=windows-1252",
	)
	resource.ParserVersion = UP2DataCorporateActionLifecycleParserVersion
	resource.ParserMetadata = map[string]string{
		"format":           "CorporateActionLifeCycleFileV2",
		"charset":          "windows-1252",
		"member":           member,
		"source_semantics": "B3 UP2DATA product/sample file; parser does not establish configured production access or canonical availability",
	}
	result := UP2DataCorporateActionLifecycleResult{
		ResourceResult: providers.ResourceResult{Resources: []providers.RawResource{resource}},
	}
	var err error
	result.Events, result.Stats, err = parseUP2DataCorporateActionLifecycle(body, member)
	if err != nil {
		return result, fmt.Errorf("B3 UP2DATA corporate-action lifecycle: %w", err)
	}
	resource.ParserMetadata["event_rows"] = fmt.Sprint(len(result.Events))
	resource.ParserMetadata["report_dates"] = distinctUP2DataReportDates(result.Events)
	return result, nil
}

func parseUP2DataCorporateActionLifecycle(body []byte, member string) ([]UP2DataCorporateActionLifecycleEvent, UP2DataCorporateActionLifecycleParseStats, error) {
	decoded, err := charmap.Windows1252.NewDecoder().Bytes(body)
	if err != nil {
		return nil, UP2DataCorporateActionLifecycleParseStats{}, fmt.Errorf("decode CSV as Windows-1252: %w", err)
	}
	reader := csv.NewReader(bytes.NewReader(decoded))
	reader.Comma = ';'
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	header, err := reader.Read()
	if err != nil {
		return nil, UP2DataCorporateActionLifecycleParseStats{}, fmt.Errorf("decode CSV header: %w", err)
	}
	if !sameUP2DataColumns(header, up2DataCorporateActionLifecycleHeader) {
		return nil, UP2DataCorporateActionLifecycleParseStats{}, fmt.Errorf("unexpected CorporateActionLifeCycleFileV2 columns")
	}
	columns := make(map[string]int, len(header))
	for index, name := range header {
		columns[name] = index
	}
	events := make([]UP2DataCorporateActionLifecycleEvent, 0)
	stats := UP2DataCorporateActionLifecycleParseStats{}
	seenRows := make(map[string]struct{})
	rowNumber := 1
	for {
		row, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		rowNumber++
		stats.RecordsReceived++
		if readErr != nil {
			stats.RecordsRejected++
			continue
		}
		if len(row) > len(header) && allUP2DataFieldsEmpty(row[len(header):]) {
			// A small number of official sample rows carry one or two extra
			// trailing separators. They add no data; retain the exact CSV in
			// the raw resource while normalizing only these empty columns.
			row = row[:len(header)]
		}
		if len(row) != len(header) {
			stats.RecordsRejected++
			continue
		}
		rowKey := strings.Join(row, "\x00")
		if _, exists := seenRows[rowKey]; exists {
			stats.Duplicates++
			continue
		}
		seenRows[rowKey] = struct{}{}
		event, parseErr := parseUP2DataCorporateActionLifecycleRow(row, columns, member, rowNumber)
		if parseErr != nil {
			stats.RecordsRejected++
			continue
		}
		events = append(events, event)
	}
	return events, stats, nil
}

func parseUP2DataCorporateActionLifecycleRow(row []string, columns map[string]int, member string, rowNumber int) (UP2DataCorporateActionLifecycleEvent, error) {
	field := func(name string) string { return strings.TrimSpace(row[columns[name]]) }
	reportDate, err := requiredUP2DataDate(field("RptDt"), "RptDt")
	if err != nil {
		return UP2DataCorporateActionLifecycleEvent{}, fmt.Errorf("CSV row %d: %w", rowNumber, err)
	}
	publicationDate, err := requiredUP2DataDate(field("PblctnDt"), "PblctnDt")
	if err != nil {
		return UP2DataCorporateActionLifecycleEvent{}, fmt.Errorf("CSV row %d: %w", rowNumber, err)
	}
	sourceEventID := field("CorpActnCtrlNb")
	if !up2DataCorporateActionIDPattern.MatchString(sourceEventID) {
		return UP2DataCorporateActionLifecycleEvent{}, fmt.Errorf("CSV row %d CorpActnCtrlNb is invalid: %q", rowNumber, sourceEventID)
	}
	productISIN, err := optionalUP2DataISIN(field("ISINPdct"), "ISINPdct")
	if err != nil {
		return UP2DataCorporateActionLifecycleEvent{}, fmt.Errorf("CSV row %d: %w", rowNumber, err)
	}
	requestISIN, err := optionalUP2DataISIN(field("ISINRqst"), "ISINRqst")
	if err != nil {
		return UP2DataCorporateActionLifecycleEvent{}, fmt.Errorf("CSV row %d: %w", rowNumber, err)
	}
	resultISIN, err := optionalUP2DataISIN(field("ISINRslt"), "ISINRslt")
	if err != nil {
		return UP2DataCorporateActionLifecycleEvent{}, fmt.Errorf("CSV row %d: %w", rowNumber, err)
	}
	noticeDate, err := optionalUP2DataDate(field("NtceDt"), "NtceDt")
	if err != nil {
		return UP2DataCorporateActionLifecycleEvent{}, fmt.Errorf("CSV row %d: %w", rowNumber, err)
	}
	referenceDate, err := optionalUP2DataDate(field("RefDt"), "RefDt")
	if err != nil {
		return UP2DataCorporateActionLifecycleEvent{}, fmt.Errorf("CSV row %d: %w", rowNumber, err)
	}
	specialExDate, err := optionalUP2DataDate(field("SpclExDt"), "SpclExDt")
	if err != nil {
		return UP2DataCorporateActionLifecycleEvent{}, fmt.Errorf("CSV row %d: %w", rowNumber, err)
	}
	paymentDate, err := optionalUP2DataDate(field("PmtDt"), "PmtDt")
	if err != nil {
		return UP2DataCorporateActionLifecycleEvent{}, fmt.Errorf("CSV row %d: %w", rowNumber, err)
	}
	subscriptionStartDate, err := optionalUP2DataDate(field("SbcptStartDt"), "SbcptStartDt")
	if err != nil {
		return UP2DataCorporateActionLifecycleEvent{}, fmt.Errorf("CSV row %d: %w", rowNumber, err)
	}
	subscriptionEndDate, err := optionalUP2DataDate(field("SbcptEndDt"), "SbcptEndDt")
	if err != nil {
		return UP2DataCorporateActionLifecycleEvent{}, fmt.Errorf("CSV row %d: %w", rowNumber, err)
	}
	tradingEndDate, err := optionalUP2DataDate(field("TradgEndDt"), "TradgEndDt")
	if err != nil {
		return UP2DataCorporateActionLifecycleEvent{}, fmt.Errorf("CSV row %d: %w", rowNumber, err)
	}
	assignmentEndDate, err := optionalUP2DataDate(field("AssgnmtEndDt"), "AssgnmtEndDt")
	if err != nil {
		return UP2DataCorporateActionLifecycleEvent{}, fmt.Errorf("CSV row %d: %w", rowNumber, err)
	}
	transferEndDate, err := optionalUP2DataDate(field("TrfEndDt"), "TrfEndDt")
	if err != nil {
		return UP2DataCorporateActionLifecycleEvent{}, fmt.Errorf("CSV row %d: %w", rowNumber, err)
	}
	approvalDate, err := optionalUP2DataDate(field("CrtApprvlDt"), "CrtApprvlDt")
	if err != nil {
		return UP2DataCorporateActionLifecycleEvent{}, fmt.Errorf("CSV row %d: %w", rowNumber, err)
	}
	correctionStartDate, err := optionalUP2DataDate(field("StartDtCrrctn"), "StartDtCrrctn")
	if err != nil {
		return UP2DataCorporateActionLifecycleEvent{}, fmt.Errorf("CSV row %d: %w", rowNumber, err)
	}
	correctionEndDate, err := optionalUP2DataDate(field("EndDtCrrctn"), "EndDtCrrctn")
	if err != nil {
		return UP2DataCorporateActionLifecycleEvent{}, fmt.Errorf("CSV row %d: %w", rowNumber, err)
	}
	auctionDate, err := optionalUP2DataDate(field("AuctnDt"), "AuctnDt")
	if err != nil {
		return UP2DataCorporateActionLifecycleEvent{}, fmt.Errorf("CSV row %d: %w", rowNumber, err)
	}
	return UP2DataCorporateActionLifecycleEvent{
		ReportDate:                   reportDate,
		SourceEventID:                sourceEventID,
		PublicationDate:              publicationDate,
		OriginInformation:            field("OrgnInf"),
		Ticker:                       field("TckrSymb"),
		SecurityID:                   field("SctyId"),
		SecuritySource:               field("SctySrc"),
		MarketIdentifierCode:         field("MktIdrCd"),
		OriginNegotiationFactor:      field("OrgnNgtnFctr"),
		ProductISIN:                  productISIN,
		DestinationNegotiationFactor: field("DstnNgtnFctr"),
		RequestISIN:                  requestISIN,
		ResultISIN:                   resultISIN,
		DistributionID:               field("DstrbtnId"),
		CompanyName:                  field("CrpnNm"),
		Specification:                field("SpcfctnCd"),
		EventTypeCode:                field("CorpActnEvtTpCd"),
		EventDescription:             field("CorpActnDesc"),
		NoticeType:                   field("NtceTp"),
		NoticeDate:                   noticeDate,
		ReferenceDate:                referenceDate,
		SpecialExDate:                specialExDate,
		PaymentDate:                  paymentDate,
		SubscriptionStartDate:        subscriptionStartDate,
		SubscriptionEndDate:          subscriptionEndDate,
		TradingEndDate:               tradingEndDate,
		AssignmentEndDate:            assignmentEndDate,
		TransferEndDate:              transferEndDate,
		EventValue:                   field("EvtVal"),
		SubscriptionFinalValue:       field("SbcptFinVal"),
		BonusValue:                   field("BonusVal"),
		FractionTreatment:            field("FrctnTrtmnt"),
		EventActionType:              field("EvtActnTpCd"),
		PriceFactor:                  field("PricFctr"),
		CollectionSequence:           field("ClctnSeq"),
		Link:                         field("Lk"),
		ProductDistributionID:        field("DstrbtnPdct"),
		DestinationDistributionID:    field("DstrbtnDstn"),
		MeetingUpdateReason:          field("MtgUpdRsnTxt"),
		ApprovalDate:                 approvalDate,
		CorrectionIndicator:          field("CorpActnCrrctnInd"),
		IndexShortName:               field("IndxShrtNm"),
		UpdatedEventValue:            field("UpdtdEvtVal"),
		EarningsValue:                field("ErngVal"),
		CorrectionStartDate:          correctionStartDate,
		CorrectionEndDate:            correctionEndDate,
		PaymentInstallmentNumber:     field("PmtInstlmtNb"),
		PaymentInstallmentQuantity:   field("PmtInstlmtQty"),
		EventInstallmentValue:        field("EvtInstlmtVal"),
		TotalSharesBeforeEvent:       field("TtlShrBFROEvt"),
		TotalSharesAfterEvent:        field("TtlShrAftrEvt"),
		SharePositionBeforeEvent:     field("ShrPpsnBFROEvt"),
		SharePositionAfterEvent:      field("ShrPpsnAftrEvt"),
		DaysToPositionAdjustment:     field("DaysToPosAdjstmnt"),
		ShareSplitRightParticipation: field("ShrSpltRghtPrtcptnTp"),
		AuctionShareQuantity:         field("AuctnShrQty"),
		AuctionDate:                  auctionDate,
		RawFields:                    append([]string(nil), row...),
		RawRecordLocator:             fmt.Sprintf("%s#row=%d/source_event_id=%s", member, rowNumber, sourceEventID),
	}, nil
}

func requiredUP2DataDate(value, field string) (time.Time, error) {
	parsed, err := optionalUP2DataDate(value, field)
	if err != nil {
		return time.Time{}, err
	}
	if parsed == nil {
		return time.Time{}, fmt.Errorf("%s is required", field)
	}
	return *parsed, nil
}

func optionalUP2DataDate(value, field string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "9999-12-31" || value == "31/12/9999" {
		return nil, nil
	}
	layouts := []string{time.DateOnly, time.RFC3339, "2006-01-02 15:04:05", "02/01/2006"}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			parsed = parsed.UTC()
			return &parsed, nil
		}
	}
	return nil, fmt.Errorf("%s has unsupported date %q", field, value)
}

func optionalUP2DataISIN(value, field string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	if !isinPattern.MatchString(value) {
		return "", fmt.Errorf("%s must be a valid uppercase ISIN, got %q", field, value)
	}
	return value, nil
}

func sameUP2DataColumns(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if strings.TrimSpace(left[index]) != right[index] {
			return false
		}
	}
	return true
}

func allUP2DataFieldsEmpty(fields []string) bool {
	for _, value := range fields {
		if strings.TrimSpace(value) != "" {
			return false
		}
	}
	return true
}

func distinctUP2DataReportDates(events []UP2DataCorporateActionLifecycleEvent) string {
	seen := make(map[string]struct{})
	for _, event := range events {
		seen[event.ReportDate.Format(time.DateOnly)] = struct{}{}
	}
	values := make([]string, 0, len(seen))
	for value := range seen {
		values = append(values, value)
	}
	sort.Strings(values)
	return strings.Join(values, ",")
}
