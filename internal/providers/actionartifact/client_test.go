package actionartifact

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

func TestCollectRetainsAndVerifiesCorporateActionArtifacts(t *testing.T) {
	htmlURL := "https://www.sec.gov/Archives/edgar/data/1/action.html"
	zipURL := "https://www.b3.com.br/data/files/action.zip"
	html := []byte("<!doctype html><html><body>action</body></html>")
	zipBody := []byte("PK\x03\x04pinned")
	client := NewClient(getterFake{bodies: map[string][]byte{htmlURL: html, zipURL: zipBody}})
	client.now = func() time.Time { return time.Date(2026, 8, 24, 3, 0, 0, 999, time.UTC) }
	result, err := client.Collect(context.Background(), "sec", []ResourceRequest{
		{Kind: "filing", URL: htmlURL, ExpectedSHA256: providers.SHA256(html), ContentType: "text/html; charset=utf-8"},
		{Kind: "sample", URL: zipURL, ExpectedSHA256: providers.SHA256(zipBody), ContentType: "application/zip"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Resources) != 2 || result.Resources[0].ParserVersion != ParserVersion {
		t.Fatalf("resources = %+v", result.Resources)
	}
	if !result.Resources[0].FetchedAt.Equal(time.Date(2026, 8, 24, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("fetched_at = %s", result.Resources[0].FetchedAt)
	}
}

func TestCollectPreservesDownloadedResourceOnFailure(t *testing.T) {
	requestURL := "https://www.sec.gov/Archives/edgar/data/1/action.html"
	body := []byte("<html>action</html>")
	result, err := NewClient(getterFake{bodies: map[string][]byte{requestURL: body}}).Collect(
		context.Background(), "sec", []ResourceRequest{{
			Kind: "filing", URL: requestURL, ExpectedSHA256: providers.SHA256([]byte("other")),
			ContentType: "text/html; charset=utf-8",
		}},
	)
	if err == nil || len(result.Resources) != 1 || string(result.Resources[0].Bytes) != string(body) {
		t.Fatalf("error/resources = %v/%+v", err, result.Resources)
	}
}
