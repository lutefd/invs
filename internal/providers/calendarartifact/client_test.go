package calendarartifact

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luisdourado/invs/internal/providers"
)

type getterFake struct {
	bodies map[string][]byte
	errURL string
}

func (g getterFake) Get(_ context.Context, requestURL string) ([]byte, error) {
	if requestURL == g.errURL {
		return nil, errors.New("offline")
	}
	return append([]byte(nil), g.bodies[requestURL]...), nil
}

func TestCollectRetainsAndVerifiesPinnedArtifacts(t *testing.T) {
	pdfURL := "https://exchange.test/calendar.pdf"
	htmlURL := "https://exchange.test/notice"
	pdfBody := []byte("%PDF-1.7\npinned")
	htmlBody := []byte("<!doctype html><html><body>notice</body></html>")
	client := NewClient(getterFake{bodies: map[string][]byte{pdfURL: pdfBody, htmlURL: htmlBody}})
	client.now = func() time.Time { return time.Date(2026, 8, 24, 2, 0, 0, 999, time.UTC) }
	result, err := client.Collect(context.Background(), Request{
		Source: "nasdaq", MIC: "XNAS", Year: 2025, Revision: 0,
		Resources: []ResourceRequest{
			{Kind: "annual_calendar", URL: pdfURL, ExpectedSHA256: providers.SHA256(pdfBody), ContentType: "application/pdf"},
			{Kind: "publication_notice", URL: htmlURL, ExpectedSHA256: providers.SHA256(htmlBody), ContentType: "text/html; charset=utf-8"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Resources) != 2 || result.Resources[0].ParserVersion != ParserVersion || result.Resources[0].Year != 2025 {
		t.Fatalf("resources = %+v", result.Resources)
	}
	if got := result.Resources[0].FetchedAt.Nanosecond(); got != 0 {
		t.Fatalf("fetched_at was not truncated to microseconds: %d", got)
	}
	if result.Resources[1].ParserMetadata["expected_sha256"] != providers.SHA256(htmlBody) {
		t.Fatalf("metadata = %+v", result.Resources[1].ParserMetadata)
	}
}

func TestCollectReturnsDownloadedEvidenceOnVerificationOrLaterTransportFailure(t *testing.T) {
	firstURL := "https://exchange.test/calendar.pdf"
	secondURL := "https://exchange.test/notice"
	pdfBody := []byte("%PDF-1.7\npinned")
	base := Request{Source: "b3", MIC: "BVMF", Year: 2026, Resources: []ResourceRequest{{
		Kind: "calendar", URL: firstURL, ExpectedSHA256: providers.SHA256(pdfBody), ContentType: "application/pdf",
	}}}

	mismatch := base
	mismatch.Resources = append([]ResourceRequest(nil), base.Resources...)
	mismatch.Resources[0].ExpectedSHA256 = providers.SHA256([]byte("other"))
	result, err := NewClient(getterFake{bodies: map[string][]byte{firstURL: pdfBody}}).Collect(context.Background(), mismatch)
	if err == nil || len(result.Resources) != 1 || result.Resources[0].SHA256 != providers.SHA256(pdfBody) {
		t.Fatalf("mismatch error/resources = %v/%+v", err, result.Resources)
	}

	partial := base
	partial.Resources = append(partial.Resources, ResourceRequest{
		Kind: "correction", URL: secondURL, ExpectedSHA256: providers.SHA256([]byte("unused")), ContentType: "text/html; charset=utf-8",
	})
	result, err = NewClient(getterFake{bodies: map[string][]byte{firstURL: pdfBody}, errURL: secondURL}).Collect(context.Background(), partial)
	if err == nil || len(result.Resources) != 1 {
		t.Fatalf("partial error/resources = %v/%+v", err, result.Resources)
	}
}
