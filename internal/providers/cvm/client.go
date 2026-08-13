package cvm

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/unicode/norm"
)

const (
	DefaultCADURL         = "https://dados.cvm.gov.br/dados/CIA_ABERTA/CAD/DADOS/cad_cia_aberta.csv"
	DefaultIPEMetadataURL = "https://dados.cvm.gov.br/dados/CIA_ABERTA/DOC/IPE/META/meta_ipe_cia_aberta.txt"
	DefaultIPEArchiveURL  = "https://dados.cvm.gov.br/dados/CIA_ABERTA/DOC/IPE/DADOS/ipe_cia_aberta_%04d.zip"

	ParserVersion = "cvm-v1"
	MinIPEYear    = 2003
	MaxIPEYears   = 10

	maxArchiveMemberBytes = 256 << 20
)

// Getter is the HTTP boundary used by the repository providers. Production
// callers should inject internal/httpx.Client, which supplies the shared
// limiter, timeout, bounded retries, and response-size limit. The adapter
// performs one GET per deterministic resource and does not add another retry
// loop around that policy.
type Getter interface {
	Get(context.Context, string) ([]byte, error)
}

type Client struct {
	http Getter
	now  func() time.Time
}

func NewClient(getter Getter) *Client {
	return &Client{http: getter, now: time.Now}
}

type Request struct {
	IncludeCAD bool
	IPEYears   []int
}

type ResourceKind string

const (
	ResourceCAD         ResourceKind = "cad"
	ResourceIPEMetadata ResourceKind = "ipe_metadata"
	ResourceIPEArchive  ResourceKind = "ipe"
)

type RawResource struct {
	Key            string
	Kind           ResourceKind
	Year           int
	URL            string
	Bytes          []byte
	SHA256         string
	ContentType    string
	ParserVersion  string
	ParserMetadata map[string]string
}

type ParseStats struct {
	RecordsReceived int
	RecordsRejected int
	Duplicates      int
}

type Result struct {
	Resources       []RawResource
	CAD             []CADRow
	IPE             []IPERow
	IPEMetadata     []FieldMetadata
	StatsByResource map[string]ParseStats

	RecordsReceived int
	RecordsRejected int
	Duplicates      int
}

type Precision string

const (
	PrecisionDate    Precision = "date"
	PrecisionUnknown Precision = "unknown"
)

type FieldMetadata struct {
	Name        string
	Description string
	Domain      string
	DataType    string
	Size        string
}

type CADDates struct {
	RegistrationDate      *time.Time
	IncorporationDate     *time.Time
	CancellationDate      *time.Time
	StatusStartDate       *time.Time
	CategoryStartDate     *time.Time
	IssuerStatusStartDate *time.Time
	ResponsibleStartDate  *time.Time
}

// CADRow is a typed representation of one current CVM issuer snapshot row.
// The source lexemes remain available in the *Text fields and RawFields; the
// parsed dates are convenience values and do not imply a historical issuer
// state or publication timestamp.
type CADRow struct {
	CNPJ                     string
	LegalName                string
	CommercialName           string
	RegistrationDateText     string
	IncorporationDateText    string
	CancellationDateText     string
	CancellationReason       string
	Status                   string
	StatusStartDateText      string
	CVMCode                  string
	ActivitySector           string
	MarketType               string
	RegistrationCategory     string
	CategoryStartDateText    string
	IssuerStatus             string
	IssuerStatusStartText    string
	ShareholderControl       string
	AddressType              string
	Street                   string
	Complement               string
	Neighborhood             string
	Municipality             string
	State                    string
	Country                  string
	PostalCode               string
	PhoneAreaCode            string
	Phone                    string
	FaxAreaCode              string
	Fax                      string
	Email                    string
	ResponsibleType          string
	ResponsibleName          string
	ResponsibleStartText     string
	ResponsibleStreet        string
	ResponsibleComplement    string
	ResponsibleNeighborhood  string
	ResponsibleMunicipality  string
	ResponsibleState         string
	ResponsibleCountry       string
	ResponsiblePostalCode    string
	ResponsiblePhoneAreaCode string
	ResponsiblePhone         string
	ResponsibleFaxAreaCode   string
	ResponsibleFax           string
	ResponsibleEmail         string
	AuditorCNPJ              string
	AuditorName              string

	Dates            CADDates
	RawFields        []string
	RawRecordLocator string
}

// IPERow is document-index metadata from CVM's IPE archive. Data_Entrega is
// retained as a source date, but it is deliberately not promoted to
// PublishedAt: CVM does not expose an exact publication instant here.
type IPERow struct {
	CompanyCNPJ       string
	CompanyName       string
	CVMCode           string
	ReferenceDateText string
	ReferenceDate     *time.Time
	Category          string
	Type              string
	Species           string
	Subject           string
	DeliveryDateText  string
	DeliveryDate      time.Time
	PresentationType  string
	Protocol          string
	Version           string
	DownloadURL       string

	SourceDocumentID string
	AccessionNumber  string
	FormType         string

	ObservedAt         *time.Time
	ObservedPrecision  Precision
	PublishedAt        *time.Time
	PublishedPrecision Precision
	AvailableAt        time.Time
	IngestedAt         time.Time

	RawFields        []string
	RawRecordLocator string
}

var cadHeader = []string{
	"CNPJ_CIA", "DENOM_SOCIAL", "DENOM_COMERC", "DT_REG", "DT_CONST", "DT_CANCEL", "MOTIVO_CANCEL", "SIT", "DT_INI_SIT", "CD_CVM", "SETOR_ATIV", "TP_MERC", "CATEG_REG", "DT_INI_CATEG", "SIT_EMISSOR", "DT_INI_SIT_EMISSOR", "CONTROLE_ACIONARIO", "TP_ENDER", "LOGRADOURO", "COMPL", "BAIRRO", "MUN", "UF", "PAIS", "CEP", "DDD_TEL", "TEL", "DDD_FAX", "FAX", "EMAIL", "TP_RESP", "RESP", "DT_INI_RESP", "LOGRADOURO_RESP", "COMPL_RESP", "BAIRRO_RESP", "MUN_RESP", "UF_RESP", "PAIS_RESP", "CEP_RESP", "DDD_TEL_RESP", "TEL_RESP", "DDD_FAX_RESP", "FAX_RESP", "EMAIL_RESP", "CNPJ_AUDITOR", "AUDITOR",
}

var ipeHeader = []string{
	"CNPJ_Companhia", "Nome_Companhia", "Codigo_CVM", "Data_Referencia", "Categoria", "Tipo", "Especie", "Assunto", "Data_Entrega", "Tipo_Apresentacao", "Protocolo_Entrega", "Versao", "Link_Download",
}

func (c *Client) Collect(ctx context.Context, request Request) (Result, error) {
	if c == nil || c.http == nil {
		return Result{}, fmt.Errorf("CVM client HTTP getter is required")
	}
	normalized, err := normalizeRequest(request)
	if err != nil {
		return Result{}, fmt.Errorf("CVM request: %w", err)
	}
	ingested, err := normalizedIngested(c.now())
	if err != nil {
		return Result{}, err
	}
	result := Result{StatsByResource: make(map[string]ParseStats)}

	if normalized.IncludeCAD {
		if err := validateEndpoint(DefaultCADURL, "/dados/CIA_ABERTA/CAD/DADOS/cad_cia_aberta.csv"); err != nil {
			return result, err
		}
		body, err := c.http.Get(ctx, DefaultCADURL)
		if err != nil {
			return result, fmt.Errorf("CVM CAD: %w", err)
		}
		resource := newRawResource(ResourceCAD, 0, "cvm/cad", DefaultCADURL, "text/csv", body)
		result.Resources = append(result.Resources, resource)
		index := len(result.Resources) - 1
		rows, stats, charset, parseErr := parseCAD(body)
		result.CAD = rows
		result.StatsByResource[resource.Key] = stats
		result.addStats(stats)
		result.Resources[index].ParserMetadata["charset"] = charset
		if parseErr != nil {
			return result, fmt.Errorf("CVM CAD: %w", parseErr)
		}
	}

	if len(normalized.IPEYears) == 0 {
		return result, nil
	}

	if err := validateEndpoint(DefaultIPEMetadataURL, "/dados/CIA_ABERTA/DOC/IPE/META/meta_ipe_cia_aberta.txt"); err != nil {
		return result, err
	}
	metadataBytes, err := c.http.Get(ctx, DefaultIPEMetadataURL)
	if err != nil {
		return result, fmt.Errorf("CVM IPE metadata: %w", err)
	}
	metadataResource := newRawResource(ResourceIPEMetadata, 0, "cvm/ipe/metadata", DefaultIPEMetadataURL, "text/plain", metadataBytes)
	result.Resources = append(result.Resources, metadataResource)
	metadataIndex := len(result.Resources) - 1
	metadata, charset, parseErr := parseIPEMetadata(metadataBytes)
	result.IPEMetadata = metadata
	result.Resources[metadataIndex].ParserMetadata["charset"] = charset
	if parseErr != nil {
		return result, fmt.Errorf("CVM IPE metadata: %w", parseErr)
	}

	seenIPE := make(map[string]string)
	for _, year := range normalized.IPEYears {
		archiveURL := fmt.Sprintf(DefaultIPEArchiveURL, year)
		if err := validateEndpoint(archiveURL, fmt.Sprintf("/dados/CIA_ABERTA/DOC/IPE/DADOS/ipe_cia_aberta_%04d.zip", year)); err != nil {
			return result, err
		}
		body, err := c.http.Get(ctx, archiveURL)
		if err != nil {
			return result, fmt.Errorf("CVM IPE archive %d: %w", year, err)
		}
		key := fmt.Sprintf("cvm/ipe/year=%04d", year)
		resource := newRawResource(ResourceIPEArchive, year, key, archiveURL, "application/zip", body)
		result.Resources = append(result.Resources, resource)
		index := len(result.Resources) - 1
		rows, stats, charset, member, parseErr := parseIPEArchive(body, year, ingested)
		rows, stats = deduplicateIPE(rows, stats, seenIPE)
		result.IPE = append(result.IPE, rows...)
		result.StatsByResource[resource.Key] = stats
		result.addStats(stats)
		result.Resources[index].ParserMetadata["charset"] = charset
		if member != "" {
			result.Resources[index].ParserMetadata["archive_member"] = member
		}
		if parseErr != nil {
			return result, fmt.Errorf("CVM IPE archive %d: %w", year, parseErr)
		}
	}
	sort.Slice(result.IPE, func(i, j int) bool {
		return result.IPE[i].SourceDocumentID < result.IPE[j].SourceDocumentID
	})

	return result, nil
}

func (r *Result) addStats(stats ParseStats) {
	r.RecordsReceived += stats.RecordsReceived
	r.RecordsRejected += stats.RecordsRejected
	r.Duplicates += stats.Duplicates
}

func normalizeRequest(request Request) (Request, error) {
	if !request.IncludeCAD && len(request.IPEYears) == 0 {
		return Request{}, fmt.Errorf("at least one CAD or IPE resource must be requested")
	}
	years := append([]int(nil), request.IPEYears...)
	sort.Ints(years)
	unique := years[:0]
	for _, year := range years {
		if year < MinIPEYear || year > 9999 {
			return Request{}, fmt.Errorf("IPE year %d is outside the supported range %d..9999", year, MinIPEYear)
		}
		if len(unique) == 0 || unique[len(unique)-1] != year {
			unique = append(unique, year)
		}
	}
	if len(unique) > MaxIPEYears {
		return Request{}, fmt.Errorf("at most %d IPE years may be requested", MaxIPEYears)
	}
	request.IPEYears = unique
	return request, nil
}

func normalizedIngested(now time.Time) (time.Time, error) {
	if now.IsZero() {
		return time.Time{}, fmt.Errorf("CVM ingestion time is required")
	}
	return now.UTC().Truncate(time.Microsecond), nil
}

func newRawResource(kind ResourceKind, year int, key, resourceURL, contentType string, body []byte) RawResource {
	copyBody := append([]byte(nil), body...)
	return RawResource{
		Key: key, Kind: kind, Year: year, URL: resourceURL, Bytes: copyBody,
		SHA256: digest(copyBody), ContentType: contentType, ParserVersion: ParserVersion,
		ParserMetadata: map[string]string{"resource_key": key, "parser_version": ParserVersion},
	}
}

func parseCAD(body []byte) ([]CADRow, ParseStats, string, error) {
	text, charset, err := decodeCVMText(body)
	if err != nil {
		return nil, ParseStats{}, "", err
	}
	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma = ';'
	reader.FieldsPerRecord = -1
	// CVM's CAD export can contain bare quotes inside otherwise unquoted
	// fields. Preserve those quotes as field text and reject only the affected
	// row when the CSV reader cannot recover its record shape.
	reader.LazyQuotes = true
	header, err := reader.Read()
	if err != nil {
		return nil, ParseStats{}, charset, fmt.Errorf("decode CSV header: %w", err)
	}
	if !equalStrings(header, cadHeader) {
		return nil, ParseStats{}, charset, fmt.Errorf("unexpected CAD columns")
	}
	indexes := make(map[string]int, len(header))
	for i, name := range header {
		indexes[name] = i
	}
	rows := make([]CADRow, 0)
	stats := ParseStats{}
	rowNumber := 1
	for {
		row, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return rows, stats, charset, fmt.Errorf("decode CSV row %d: %w", rowNumber+1, readErr)
		}
		rowNumber++
		stats.RecordsReceived++
		if readErr != nil {
			stats.RecordsRejected++
			continue
		}
		if len(row) != len(cadHeader) {
			stats.RecordsRejected++
			continue
		}
		parsed, rowErr := parseCADRow(row, indexes, rowNumber)
		if rowErr != nil {
			stats.RecordsRejected++
			continue
		}
		rows = append(rows, parsed)
	}
	return rows, stats, charset, nil
}

func parseCADRow(row []string, indexes map[string]int, rowNumber int) (CADRow, error) {
	get := func(name string) string { return row[indexes[name]] }
	for _, name := range []string{"CNPJ_CIA", "DENOM_SOCIAL", "CD_CVM"} {
		value := get(name)
		if value == "" || strings.TrimSpace(value) != value {
			return CADRow{}, fmt.Errorf("CAD row %d missing or padded %s", rowNumber, name)
		}
	}
	if !decimalLexeme(get("CD_CVM")) {
		return CADRow{}, fmt.Errorf("CAD row %d has invalid CD_CVM", rowNumber)
	}

	parseDate := func(name string) (*time.Time, error) {
		value := get(name)
		if value == "" {
			return nil, nil
		}
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			return nil, fmt.Errorf("CAD row %d has invalid %s", rowNumber, name)
		}
		parsed = parsed.UTC()
		return &parsed, nil
	}
	registration, err := parseDate("DT_REG")
	if err != nil {
		return CADRow{}, err
	}
	incorporation, err := parseDate("DT_CONST")
	if err != nil {
		return CADRow{}, err
	}
	cancellation, err := parseDate("DT_CANCEL")
	if err != nil {
		return CADRow{}, err
	}
	statusStart, err := parseDate("DT_INI_SIT")
	if err != nil {
		return CADRow{}, err
	}
	categoryStart, err := parseDate("DT_INI_CATEG")
	if err != nil {
		return CADRow{}, err
	}
	issuerStatusStart, err := parseDate("DT_INI_SIT_EMISSOR")
	if err != nil {
		return CADRow{}, err
	}
	responsibleStart, err := parseDate("DT_INI_RESP")
	if err != nil {
		return CADRow{}, err
	}

	return CADRow{
		CNPJ: get("CNPJ_CIA"), LegalName: get("DENOM_SOCIAL"), CommercialName: get("DENOM_COMERC"),
		RegistrationDateText: get("DT_REG"), IncorporationDateText: get("DT_CONST"), CancellationDateText: get("DT_CANCEL"),
		CancellationReason: get("MOTIVO_CANCEL"), Status: get("SIT"), StatusStartDateText: get("DT_INI_SIT"), CVMCode: get("CD_CVM"),
		ActivitySector: get("SETOR_ATIV"), MarketType: get("TP_MERC"), RegistrationCategory: get("CATEG_REG"), CategoryStartDateText: get("DT_INI_CATEG"),
		IssuerStatus: get("SIT_EMISSOR"), IssuerStatusStartText: get("DT_INI_SIT_EMISSOR"), ShareholderControl: get("CONTROLE_ACIONARIO"),
		AddressType: get("TP_ENDER"), Street: get("LOGRADOURO"), Complement: get("COMPL"), Neighborhood: get("BAIRRO"), Municipality: get("MUN"), State: get("UF"), Country: get("PAIS"), PostalCode: get("CEP"), PhoneAreaCode: get("DDD_TEL"), Phone: get("TEL"), FaxAreaCode: get("DDD_FAX"), Fax: get("FAX"), Email: get("EMAIL"),
		ResponsibleType: get("TP_RESP"), ResponsibleName: get("RESP"), ResponsibleStartText: get("DT_INI_RESP"), ResponsibleStreet: get("LOGRADOURO_RESP"), ResponsibleComplement: get("COMPL_RESP"), ResponsibleNeighborhood: get("BAIRRO_RESP"), ResponsibleMunicipality: get("MUN_RESP"), ResponsibleState: get("UF_RESP"), ResponsibleCountry: get("PAIS_RESP"), ResponsiblePostalCode: get("CEP_RESP"), ResponsiblePhoneAreaCode: get("DDD_TEL_RESP"), ResponsiblePhone: get("TEL_RESP"), ResponsibleFaxAreaCode: get("DDD_FAX_RESP"), ResponsibleFax: get("FAX_RESP"), ResponsibleEmail: get("EMAIL_RESP"), AuditorCNPJ: get("CNPJ_AUDITOR"), AuditorName: get("AUDITOR"),
		Dates:     CADDates{RegistrationDate: registration, IncorporationDate: incorporation, CancellationDate: cancellation, StatusStartDate: statusStart, CategoryStartDate: categoryStart, IssuerStatusStartDate: issuerStatusStart, ResponsibleStartDate: responsibleStart},
		RawFields: append([]string(nil), row...), RawRecordLocator: fmt.Sprintf("csv/row=%d", rowNumber),
	}, nil
}

func parseIPEMetadata(body []byte) ([]FieldMetadata, string, error) {
	text, charset, err := decodeCVMText(body)
	if err != nil {
		return nil, "", err
	}
	var fields []FieldMetadata
	var current *FieldMetadata
	appendCurrent := func() {
		if current != nil {
			fields = append(fields, *current)
		}
	}
	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(rawLine, "\r"))
		if line == "" || onlyDashes(line) {
			continue
		}
		separator := strings.IndexByte(line, ':')
		if separator < 0 {
			continue
		}
		key := normalizedMetadataKey(line[:separator])
		value := strings.TrimSpace(line[separator+1:])
		switch key {
		case "campo":
			if value == "" {
				return fields, charset, fmt.Errorf("metadata contains an empty field name")
			}
			appendCurrent()
			current = &FieldMetadata{Name: value}
		case "descricao":
			if current != nil {
				current.Description = value
			}
		case "dominio":
			if current != nil {
				current.Domain = value
			}
		case "tipo dados":
			if current != nil {
				current.DataType = value
			}
		case "tamanho":
			if current != nil {
				current.Size = value
			}
		}
	}
	appendCurrent()
	if len(fields) == 0 {
		return nil, charset, fmt.Errorf("metadata contains no fields")
	}
	return fields, charset, nil
}

func parseIPEArchive(body []byte, year int, ingested time.Time) ([]IPERow, ParseStats, string, string, error) {
	if year < MinIPEYear || year > 9999 {
		return nil, ParseStats{}, "", "", fmt.Errorf("invalid IPE year %d", year)
	}
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, ParseStats{}, "", "", fmt.Errorf("decode ZIP: %w", err)
	}
	var csvFiles []*zip.File
	seenNames := make(map[string]struct{}, len(reader.File))
	for _, file := range reader.File {
		if !safeArchiveMember(file.Name) {
			return nil, ParseStats{}, "", "", fmt.Errorf("unsafe archive member %q", file.Name)
		}
		if _, exists := seenNames[file.Name]; exists {
			return nil, ParseStats{}, "", "", fmt.Errorf("duplicate archive member %q", file.Name)
		}
		seenNames[file.Name] = struct{}{}
		if file.FileInfo().IsDir() {
			continue
		}
		if strings.EqualFold(path.Ext(file.Name), ".csv") {
			csvFiles = append(csvFiles, file)
		}
	}
	if len(csvFiles) != 1 {
		return nil, ParseStats{}, "", "", fmt.Errorf("expected exactly one IPE CSV member, found %d", len(csvFiles))
	}
	file := csvFiles[0]
	if file.UncompressedSize64 > maxArchiveMemberBytes {
		return nil, ParseStats{}, "", file.Name, fmt.Errorf("archive member %q exceeds size limit", file.Name)
	}
	stream, err := file.Open()
	if err != nil {
		return nil, ParseStats{}, "", file.Name, fmt.Errorf("open archive member %q: %w", file.Name, err)
	}
	memberBytes, readErr := io.ReadAll(io.LimitReader(stream, maxArchiveMemberBytes+1))
	closeErr := stream.Close()
	if readErr != nil {
		return nil, ParseStats{}, "", file.Name, fmt.Errorf("read archive member %q: %w", file.Name, readErr)
	}
	if closeErr != nil {
		return nil, ParseStats{}, "", file.Name, fmt.Errorf("close archive member %q: %w", file.Name, closeErr)
	}
	if int64(len(memberBytes)) > maxArchiveMemberBytes {
		return nil, ParseStats{}, "", file.Name, fmt.Errorf("archive member %q exceeds size limit", file.Name)
	}
	text, charset, err := decodeCVMText(memberBytes)
	if err != nil {
		return nil, ParseStats{}, "", file.Name, err
	}
	rows, stats, err := parseIPECSV(text, year, file.Name, ingested)
	return rows, stats, charset, file.Name, err
}

func parseIPECSV(text string, year int, member string, ingested time.Time) ([]IPERow, ParseStats, error) {
	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma = ';'
	reader.FieldsPerRecord = -1
	// CVM's IPE export contains bare quotes inside otherwise unquoted fields.
	// LazyQuotes preserves those quotes as field text; exact field-count and
	// identity/date/URL validation below still gates whether the row is kept.
	reader.LazyQuotes = true
	header, err := reader.Read()
	if err != nil {
		return nil, ParseStats{}, fmt.Errorf("decode CSV header: %w", err)
	}
	if !equalStrings(header, ipeHeader) {
		return nil, ParseStats{}, fmt.Errorf("unexpected IPE columns")
	}
	ingested, err = normalizedIngested(ingested)
	if err != nil {
		return nil, ParseStats{}, err
	}
	rows := make([]IPERow, 0)
	stats := ParseStats{}
	identities := make(map[string]string)
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
		if len(row) != len(ipeHeader) {
			stats.RecordsRejected++
			continue
		}
		parsed, rowErr := parseIPERow(row, year, member, rowNumber, ingested)
		if rowErr != nil {
			stats.RecordsRejected++
			continue
		}
		identity := parsed.SourceDocumentID
		rawIdentity := strings.Join(row, "\x1f")
		if previous, exists := identities[identity]; exists {
			stats.Duplicates++
			if previous != rawIdentity {
				stats.RecordsRejected++
			}
			continue
		}
		identities[identity] = rawIdentity
		rows = append(rows, parsed)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].SourceDocumentID != rows[j].SourceDocumentID {
			return rows[i].SourceDocumentID < rows[j].SourceDocumentID
		}
		return rows[i].RawRecordLocator < rows[j].RawRecordLocator
	})
	return rows, stats, nil
}

func deduplicateIPE(rows []IPERow, stats ParseStats, seen map[string]string) ([]IPERow, ParseStats) {
	unique := make([]IPERow, 0, len(rows))
	for _, row := range rows {
		identity := row.SourceDocumentID
		rawIdentity := strings.Join(row.RawFields, "\x1f")
		if previous, exists := seen[identity]; exists {
			stats.Duplicates++
			if previous != rawIdentity {
				stats.RecordsRejected++
			}
			continue
		}
		seen[identity] = rawIdentity
		unique = append(unique, row)
	}
	return unique, stats
}

func parseIPERow(row []string, year int, member string, rowNumber int, ingested time.Time) (IPERow, error) {
	for _, index := range []int{0, 1, 2, 8, 11} {
		if row[index] == "" || strings.TrimSpace(row[index]) != row[index] {
			return IPERow{}, fmt.Errorf("IPE row %d has missing or padded key field", rowNumber)
		}
	}
	if row[10] != "" && strings.TrimSpace(row[10]) != row[10] {
		return IPERow{}, fmt.Errorf("IPE row %d has missing or padded key field", rowNumber)
	}
	if !decimalLexeme(row[2]) {
		return IPERow{}, fmt.Errorf("IPE row %d has invalid Codigo_CVM", rowNumber)
	}
	if row[12] != "" {
		if err := validateDocumentURL(row[12]); err != nil {
			return IPERow{}, fmt.Errorf("IPE row %d: %w", rowNumber, err)
		}
	}
	sourceDocumentID := "cvm-ipe:" + row[2] + ":" + row[10] + ":v" + row[11]
	if row[10] == "" {
		var err error
		sourceDocumentID, err = ipeURLDocumentID(row[12], row[2], row[11])
		if err != nil {
			return IPERow{}, fmt.Errorf("IPE row %d: %w", rowNumber, err)
		}
	}
	referenceDate, err := parseOptionalDate(row[3], "Data_Referencia")
	if err != nil {
		return IPERow{}, fmt.Errorf("IPE row %d: %w", rowNumber, err)
	}
	deliveryDate, err := parseOptionalDate(row[8], "Data_Entrega")
	if err != nil || deliveryDate == nil {
		if err == nil {
			err = fmt.Errorf("Data_Entrega is required")
		}
		return IPERow{}, fmt.Errorf("IPE row %d: %w", rowNumber, err)
	}
	ingested, err = normalizedIngested(ingested)
	if err != nil {
		return IPERow{}, err
	}
	var observedAt *time.Time
	observedPrecision := PrecisionUnknown
	if referenceDate != nil {
		copyDate := *referenceDate
		observedAt = &copyDate
		observedPrecision = PrecisionDate
	}
	return IPERow{
		CompanyCNPJ: row[0], CompanyName: row[1], CVMCode: row[2], ReferenceDateText: row[3], ReferenceDate: referenceDate,
		Category: row[4], Type: row[5], Species: row[6], Subject: row[7], DeliveryDateText: row[8], DeliveryDate: *deliveryDate,
		PresentationType: row[9], Protocol: row[10], Version: row[11], DownloadURL: row[12],
		SourceDocumentID: sourceDocumentID, AccessionNumber: row[10], FormType: "cvm_ipe",
		ObservedAt: observedAt, ObservedPrecision: observedPrecision, PublishedAt: nil, PublishedPrecision: PrecisionUnknown,
		AvailableAt: ingested, IngestedAt: ingested, RawFields: append([]string(nil), row...), RawRecordLocator: fmt.Sprintf("zip/year=%04d/member=%s/row=%d", year, member, rowNumber),
	}, nil
}

func ipeURLDocumentID(rawURL, cvmCode, sourceVersion string) (string, error) {
	if err := validateDocumentURL(rawURL); err != nil {
		return "", err
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid Link_Download URL %q", rawURL)
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return "", fmt.Errorf("invalid Link_Download query: %w", err)
	}
	for _, name := range []string{"numProtocolo", "numSequencia", "numVersao"} {
		values, ok := query[name]
		if !ok || len(values) != 1 || values[0] == "" {
			return "", fmt.Errorf("Link_Download is missing %s", name)
		}
		if name != "numVersao" && !decimalLexeme(values[0]) {
			return "", fmt.Errorf("Link_Download has invalid %s", name)
		}
	}
	urlVersion := query.Get("numVersao")
	if !decimalLexeme(sourceVersion) || !decimalLexeme(urlVersion) {
		return "", fmt.Errorf("IPE row has invalid version")
	}
	if !sameDecimalLexeme(sourceVersion, urlVersion) {
		return "", fmt.Errorf("Link_Download numVersao %q does not match Versao %q", urlVersion, sourceVersion)
	}
	return "cvm-ipe:" + cvmCode + ":urlsha256-" + digest([]byte(rawURL)) + ":v" + sourceVersion, nil
}

func parseOptionalDate(value, field string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil, fmt.Errorf("invalid %s", field)
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func validateEndpoint(raw, expectedPath string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != "dados.cvm.gov.br" || parsed.Port() != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != expectedPath {
		return fmt.Errorf("unsafe CVM endpoint %q", raw)
	}
	return nil
}

func validateDocumentURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" || parsed.Port() != "" || parsed.User != nil || parsed.Fragment != "" || !isCVMHost(parsed.Hostname()) {
		return fmt.Errorf("unsafe Link_Download URL %q", raw)
	}
	return nil
}

func isCVMHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "cvm.gov.br" || strings.HasSuffix(host, ".cvm.gov.br")
}

func safeArchiveMember(name string) bool {
	if name == "" || strings.ContainsRune(name, '\x00') || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || path.Clean(name) != name {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func decodeCVMText(body []byte) (string, string, error) {
	body = bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf})
	if utf8.Valid(body) {
		return string(body), "utf-8", nil
	}
	decoded, err := charmap.Windows1252.NewDecoder().Bytes(body)
	if err != nil {
		return "", "", fmt.Errorf("decode CVM text: %w", err)
	}
	return string(decoded), "windows-1252", nil
}

func normalizedMetadataKey(value string) string {
	decomposed := norm.NFD.String(value)
	var builder strings.Builder
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		builder.WriteRune(unicode.ToLower(r))
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}

func onlyDashes(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r != '-' {
			return false
		}
	}
	return true
}

func decimalLexeme(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func sameDecimalLexeme(left, right string) bool {
	if !decimalLexeme(left) || !decimalLexeme(right) {
		return false
	}
	left = strings.TrimLeft(left, "0")
	right = strings.TrimLeft(right, "0")
	if left == "" {
		left = "0"
	}
	if right == "" {
		right = "0"
	}
	return left == right
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func digest(body []byte) string {
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}
