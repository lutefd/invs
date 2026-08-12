package cvm

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/charmap"
)

type fakeGetter struct {
	bodies map[string][]byte
	calls  []string
}

func (f *fakeGetter) Get(_ context.Context, requestURL string) ([]byte, error) {
	f.calls = append(f.calls, requestURL)
	body, ok := f.bodies[requestURL]
	if !ok {
		return nil, fmt.Errorf("unexpected URL %s", requestURL)
	}
	return append([]byte(nil), body...), nil
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func windows1252(t *testing.T, body []byte) []byte {
	t.Helper()
	encoded, err := charmap.Windows1252.NewEncoder().Bytes(body)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func zipFixture(t *testing.T, member string, body []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	entry, err := archive.Create(member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func hashHex(body []byte) string {
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}

func TestCollectPreservesRawResourcesAndConservativeIPETime(t *testing.T) {
	cadBody := windows1252(t, readFixture(t, "cad_cia_aberta.csv"))
	metadataBody := windows1252(t, readFixture(t, "meta_ipe_cia_aberta.txt"))
	ipeCSV := windows1252(t, readFixture(t, "ipe_cia_aberta_2026.csv"))
	ipeBody := zipFixture(t, "ipe_cia_aberta_2026.csv", ipeCSV)
	fake := &fakeGetter{bodies: map[string][]byte{
		DefaultCADURL:                           cadBody,
		DefaultIPEMetadataURL:                   metadataBody,
		fmt.Sprintf(DefaultIPEArchiveURL, 2025): ipeBody,
		fmt.Sprintf(DefaultIPEArchiveURL, 2026): ipeBody,
	}}
	c := NewClient(fake)
	receivedAt := time.Date(2026, 2, 1, 12, 30, 0, 123456789, time.FixedZone("BRT", -3*60*60))
	c.now = func() time.Time { return receivedAt }

	result, err := c.Collect(context.Background(), Request{IncludeCAD: true, IPEYears: []int{2026, 2025, 2026}})
	if err != nil {
		t.Fatal(err)
	}
	wantIngested := receivedAt.UTC().Truncate(time.Microsecond)
	wantCalls := []string{DefaultCADURL, DefaultIPEMetadataURL, fmt.Sprintf(DefaultIPEArchiveURL, 2025), fmt.Sprintf(DefaultIPEArchiveURL, 2026)}
	if len(fake.calls) != len(wantCalls) {
		t.Fatalf("GET calls = %v, want %v", fake.calls, wantCalls)
	}
	for i := range wantCalls {
		if fake.calls[i] != wantCalls[i] {
			t.Fatalf("GET call %d = %q, want %q", i, fake.calls[i], wantCalls[i])
		}
	}
	if len(result.Resources) != 4 {
		t.Fatalf("resources = %d, want 4", len(result.Resources))
	}
	wantKeys := []string{"cvm/cad", "cvm/ipe/metadata", "cvm/ipe/year=2025", "cvm/ipe/year=2026"}
	for i, key := range wantKeys {
		resource := result.Resources[i]
		if resource.Key != key || resource.ParserVersion != ParserVersion || resource.ParserMetadata["resource_key"] != key {
			t.Fatalf("resource %d metadata = %+v", i, resource)
		}
		if resource.SHA256 != hashHex(resource.Bytes) {
			t.Fatalf("resource %q hash = %q, want digest of untouched bytes", key, resource.SHA256)
		}
	}
	if !bytes.Equal(result.Resources[0].Bytes, cadBody) || result.Resources[0].ContentType != "text/csv" || result.Resources[0].ParserMetadata["charset"] != "windows-1252" {
		t.Fatalf("CAD resource was not preserved/decoded as expected: %+v", result.Resources[0])
	}
	if !bytes.Equal(result.Resources[2].Bytes, ipeBody) || result.Resources[2].Year != 2025 || result.Resources[2].ContentType != "application/zip" || result.Resources[2].ParserMetadata["archive_member"] != "ipe_cia_aberta_2026.csv" {
		t.Fatalf("IPE resource metadata = %+v", result.Resources[2])
	}

	if len(result.CAD) != 1 {
		t.Fatalf("CAD rows = %d, want 1", len(result.CAD))
	}
	cad := result.CAD[0]
	if cad.CVMCode != "000123" || cad.LegalName != "AÇÚCAR S.A." || cad.RegistrationDateText != "2020-01-02" || cad.Dates.RegistrationDate == nil || !cad.Dates.RegistrationDate.Equal(time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("CAD exact fields/dates = %+v", cad)
	}

	if len(result.IPE) != 2 {
		t.Fatalf("IPE rows = %d, want two versions after collapsing one duplicate", len(result.IPE))
	}
	if result.IPE[0].SourceDocumentID != "cvm-ipe:000123:0000000000000001:v01" || result.IPE[1].SourceDocumentID != "cvm-ipe:000123:0000000000000001:v02" {
		t.Fatalf("IPE identity/version order = %q, %q", result.IPE[0].SourceDocumentID, result.IPE[1].SourceDocumentID)
	}
	for _, row := range result.IPE {
		if row.CVMCode != "000123" || row.Protocol != "0000000000000001" || row.FormType != "cvm_ipe" || row.AccessionNumber != row.Protocol {
			t.Fatalf("IPE identity lexemes = %+v", row)
		}
		if row.PublishedAt != nil || row.PublishedPrecision != PrecisionUnknown || row.AvailableAt != wantIngested || row.IngestedAt != wantIngested {
			t.Fatalf("IPE availability semantics = %+v", row)
		}
		if row.ObservedAt == nil || !row.ObservedAt.Equal(time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)) || row.ObservedPrecision != PrecisionDate {
			t.Fatalf("IPE observed semantics = %+v", row)
		}
		if row.DeliveryDate.Equal(*row.ObservedAt) {
			t.Fatalf("delivery date should remain separate from observed period")
		}
		if !strings.HasPrefix(row.RawRecordLocator, "zip/year=2025/member=ipe_cia_aberta_2026.csv/row=") {
			t.Fatalf("raw locator = %q", row.RawRecordLocator)
		}
	}
	stats := result.StatsByResource["cvm/ipe/year=2025"]
	if stats.RecordsReceived != 5 || stats.RecordsRejected != 2 || stats.Duplicates != 1 {
		t.Fatalf("IPE stats = %+v, want received=5 rejected=2 duplicates=1", stats)
	}
	if secondStats := result.StatsByResource["cvm/ipe/year=2026"]; secondStats.RecordsReceived != 5 || secondStats.RecordsRejected != 2 || secondStats.Duplicates != 3 {
		t.Fatalf("second IPE stats = %+v, want received=5 rejected=2 duplicates=3", secondStats)
	}
	if result.RecordsReceived != 11 || result.RecordsRejected != 4 || result.Duplicates != 4 {
		t.Fatalf("aggregate stats = %+v", result)
	}
	if len(result.IPEMetadata) != 2 || result.IPEMetadata[0].Name != "Assunto" || result.IPEMetadata[1].DataType != "date" {
		t.Fatalf("IPE metadata = %+v", result.IPEMetadata)
	}
}

func TestCollectRetainsRawOnTopLevelParseFailure(t *testing.T) {
	badCAD := []byte("not;the;official;header\n")
	fake := &fakeGetter{bodies: map[string][]byte{DefaultCADURL: badCAD}}
	result, err := NewClient(fake).Collect(context.Background(), Request{IncludeCAD: true})
	if err == nil {
		t.Fatal("expected CAD schema error")
	}
	if len(result.Resources) != 1 || !bytes.Equal(result.Resources[0].Bytes, badCAD) || result.Resources[0].SHA256 != hashHex(badCAD) {
		t.Fatalf("raw CAD response was not retained: %+v", result.Resources)
	}

	metadata := windows1252(t, readFixture(t, "meta_ipe_cia_aberta.txt"))
	badArchive := []byte("not a zip")
	fake = &fakeGetter{bodies: map[string][]byte{DefaultIPEMetadataURL: metadata, fmt.Sprintf(DefaultIPEArchiveURL, 2026): badArchive}}
	result, err = NewClient(fake).Collect(context.Background(), Request{IPEYears: []int{2026}})
	if err == nil {
		t.Fatal("expected ZIP schema error")
	}
	if len(result.Resources) != 2 || !bytes.Equal(result.Resources[1].Bytes, badArchive) || result.Resources[1].SHA256 != hashHex(badArchive) {
		t.Fatalf("raw IPE response was not retained: %+v", result.Resources)
	}
}

func TestParseCADRejectsMalformedRows(t *testing.T) {
	body := readFixture(t, "cad_cia_aberta.csv")
	body = []byte(strings.Replace(string(body), "2020-01-02", "2020-02-31", 1))
	rows, stats, _, err := parseCAD(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 || stats.RecordsReceived != 1 || stats.RecordsRejected != 1 {
		t.Fatalf("CAD malformed row result = rows %d stats %+v", len(rows), stats)
	}
}

func TestParseIPERejectsUnsafeArchiveMembers(t *testing.T) {
	archive := zipFixture(t, "../escape.csv", []byte(strings.Join(ipeHeader, ";")))
	_, _, _, _, err := parseIPEArchive(archive, 2026, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "unsafe archive member") {
		t.Fatalf("error = %v, want unsafe-member rejection", err)
	}
}

func TestRequestAndDocumentURLValidation(t *testing.T) {
	if _, err := normalizeRequest(Request{}); err == nil {
		t.Fatal("empty request should be rejected")
	}
	if _, err := normalizeRequest(Request{IPEYears: []int{2026, 2025, 2026}}); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeRequest(Request{IPEYears: []int{2002}}); err == nil {
		t.Fatal("pre-history IPE year should be rejected")
	}
	for _, raw := range []string{"javascript:alert(1)", "https://example.test/document", "https://user@rad.cvm.gov.br/document", "https://rad.cvm.gov.br:8443/document", "https://rad.cvm.gov.br/document#fragment"} {
		if err := validateDocumentURL(raw); err == nil {
			t.Errorf("validateDocumentURL(%q) accepted unsafe URL", raw)
		}
	}
	if err := validateDocumentURL("https://rad.cvm.gov.br/document?id=1"); err != nil {
		t.Fatalf("valid CVM URL rejected: %v", err)
	}
}
