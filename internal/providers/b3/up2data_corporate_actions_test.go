package b3

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/luisdourado/invs/internal/httpx"
	"github.com/luisdourado/invs/internal/providers"
)

const up2DataCorporateActionSampleURL = "https://b3.com.br/data/files/CE/F5/F6/71/2643881036DB3088AC094EA8/Eventos%20Corporativos-Corporate%20Action.zip"
const up2DataCorporateActionLifecycleSampleMember = "Corporate_Action/LifeCycle/Corporate_Action_CorporateActionLifeCycleFileV2_20230419_1.csv"

func up2DataCorporateActionLifecycleFixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/up2data-corporate-action-lifecycle-v2.csv")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestParseUP2DataCorporateActionLifecycleRetainsVersionedEvidence(t *testing.T) {
	body := up2DataCorporateActionLifecycleFixture(t)
	fetchedAt := time.Date(2026, 8, 23, 16, 17, 18, 123456789, time.FixedZone("BRT", -3*60*60))
	result, err := ParseUP2DataCorporateActionLifecycle(body, "Corporate_Action/LifeCycle/Corporate_Action_CorporateActionLifeCycleFileV2_20230419_1.csv", fetchedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Resources) != 1 {
		t.Fatalf("resources = %+v", result.Resources)
	}
	resource := result.Resources[0]
	if resource.Kind != UP2DataCorporateActionLifecycleResourceKind || resource.ParserVersion != UP2DataCorporateActionLifecycleParserVersion {
		t.Fatalf("resource = %+v", resource)
	}
	if !bytes.Equal(resource.Bytes, body) || resource.SHA256 != providers.SHA256(body) {
		t.Fatal("UP2DATA lifecycle bytes were not retained exactly")
	}
	if !resource.FetchedAt.Equal(fetchedAt.UTC().Truncate(time.Microsecond)) {
		t.Fatalf("fetched_at = %s", resource.FetchedAt)
	}
	if resource.ParserMetadata["format"] != "CorporateActionLifeCycleFileV2" || resource.ParserMetadata["event_rows"] != "2" || resource.ParserMetadata["report_dates"] != "2022-12-14,2023-01-04" {
		t.Fatalf("parser metadata = %+v", resource.ParserMetadata)
	}
	if result.Stats.RecordsReceived != 2 || result.Stats.RecordsRejected != 0 || result.Stats.Duplicates != 0 || len(result.Events) != 2 {
		t.Fatalf("stats/events = %+v/%+v", result.Stats, result.Events)
	}

	first := result.Events[0]
	if first.ReportDate.Format(time.DateOnly) != "2022-12-14" || first.PublicationDate.Format(time.DateOnly) != "2022-12-14" || first.SourceEventID != "1E12917D1E504313951A81787F2272A3" {
		t.Fatalf("first identity = %+v", first)
	}
	if first.ProductISIN != "BRA1APBDR001" || first.Ticker != "A1AP34" || first.EventTypeCode != "10" || first.EventActionType != "I" || first.EventValue != "0.33617853300" {
		t.Fatalf("first source fields = %+v", first)
	}
	if first.ReferenceDate == nil || first.PaymentDate == nil || first.ReferenceDate.Format(time.DateOnly) != "2022-12-14" || first.PaymentDate.Format(time.DateOnly) != "2023-01-09" {
		t.Fatalf("first dates = %+v", first)
	}
	if !strings.Contains(first.RawRecordLocator, "row=2/source_event_id=1E12917D1E504313951A81787F2272A3") || len(first.RawFields) != 57 {
		t.Fatalf("first locator/raw fields = %q/%d", first.RawRecordLocator, len(first.RawFields))
	}

	second := result.Events[1]
	if second.OriginInformation != "Schedule" || second.EventActionType != "U" || second.Link == "" || second.ProductISIN != "" {
		t.Fatalf("second update evidence = %+v", second)
	}
}

func TestParseUP2DataCorporateActionLifecyclePreservesRawOnRowError(t *testing.T) {
	body := up2DataCorporateActionLifecycleFixture(t)
	malformed := bytes.Replace(body, []byte("2022-12-14;1E12917D1E504313951A81787F2272A3"), []byte("not-a-date;1E12917D1E504313951A81787F2272A3"), 1)
	result, err := ParseUP2DataCorporateActionLifecycle(malformed, "lifecycle.csv", time.Now())
	if err != nil {
		t.Fatalf("error = %v, want row-level rejection without file failure", err)
	}
	if len(result.Resources) != 1 || !bytes.Equal(result.Resources[0].Bytes, malformed) {
		t.Fatal("malformed lifecycle bytes were not retained")
	}
	if result.Stats.RecordsReceived != 2 || result.Stats.RecordsRejected != 1 || len(result.Events) != 1 {
		t.Fatalf("stats/events = %+v/%+v", result.Stats, result.Events)
	}
}

func TestParseUP2DataCorporateActionLifecycleRejectsUnsafeEvidence(t *testing.T) {
	body := up2DataCorporateActionLifecycleFixture(t)
	for name, input := range map[string][]byte{
		"missing member":    body,
		"unexpected header": []byte("not-a-lifecycle-file\n"),
	} {
		t.Run(name, func(t *testing.T) {
			member := "lifecycle.csv"
			if name == "missing member" {
				member = ""
			}
			result, err := ParseUP2DataCorporateActionLifecycle(input, member, time.Now())
			if err == nil {
				t.Fatal("unsafe lifecycle evidence accepted")
			}
			if name == "unexpected header" && (len(result.Resources) != 1 || !bytes.Equal(result.Resources[0].Bytes, input)) {
				t.Fatal("header failure did not retain raw evidence")
			}
		})
	}
}

func TestLiveUP2DataCorporateActionLifecycleSample(t *testing.T) {
	if os.Getenv("INVS_B3_UP2DATA_LIVE") != "1" {
		t.Skip("set INVS_B3_UP2DATA_LIVE=1 to fetch the public B3 UP2DATA sample")
	}
	getter, err := httpx.New(httpx.Config{
		UserAgent:         "invs-b3-up2data-sample-acceptance research@example.com",
		Timeout:           120 * time.Second,
		RequestsPerSecond: 1,
		Burst:             1,
		MaxAttempts:       2,
		InitialBackoff:    250 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := getter.Get(context.Background(), up2DataCorporateActionSampleURL)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	var memberBytes []byte
	for _, member := range archive.File {
		if member.Name != up2DataCorporateActionLifecycleSampleMember {
			continue
		}
		stream, openErr := member.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		memberBytes, err = io.ReadAll(stream)
		closeErr := stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		break
	}
	if len(memberBytes) == 0 {
		t.Fatalf("sample member %q was not found", up2DataCorporateActionLifecycleSampleMember)
	}
	result, err := ParseUP2DataCorporateActionLifecycle(memberBytes, up2DataCorporateActionLifecycleSampleMember, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) == 0 {
		t.Fatalf("live sample stats/events = %+v/%d", result.Stats, len(result.Events))
	}
	t.Logf("sample_zip_bytes=%d sample_zip_sha256=%s member_bytes=%d member_sha256=%s events=%d rejected=%d duplicates=%d first_event_id=%s first_report_date=%s", len(body), providers.SHA256(body), len(memberBytes), providers.SHA256(memberBytes), len(result.Events), result.Stats.RecordsRejected, result.Stats.Duplicates, result.Events[0].SourceEventID, result.Events[0].ReportDate.Format(time.DateOnly))
}
