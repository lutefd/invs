// Package calendarartifact retains immutable official calendar artifacts behind
// an exact-hash, declarative transcription boundary.
package calendarartifact

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/luisdourado/invs/internal/providers"
)

const ParserVersion = "historical-calendar-artifact-v1"

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

type ResourceRequest struct {
	Kind           string
	URL            string
	ExpectedSHA256 string
	ContentType    string
}

type Request struct {
	Source    string
	MIC       string
	Year      int
	Revision  int
	Resources []ResourceRequest
}

type Result struct {
	providers.ResourceResult
}

func (c *Client) Collect(ctx context.Context, request Request) (Result, error) {
	if c == nil || c.http == nil {
		return Result{}, errors.New("historical calendar artifact HTTP getter is required")
	}
	if strings.TrimSpace(request.Source) == "" || len(strings.TrimSpace(request.MIC)) != 4 {
		return Result{}, errors.New("historical calendar artifact source and four-character MIC are required")
	}
	if request.Year < 2000 || request.Year > 2100 || request.Revision < 0 || len(request.Resources) == 0 {
		return Result{}, errors.New("historical calendar artifact year, revision, and resources are invalid")
	}
	result := Result{ResourceResult: providers.ResourceResult{Resources: make([]providers.RawResource, 0, len(request.Resources))}}
	for index, configured := range request.Resources {
		requestURL, err := normalizeURL(configured.URL)
		if err != nil {
			return result, fmt.Errorf("historical calendar resource %d: %w", index, err)
		}
		body, err := c.http.Get(ctx, requestURL)
		if err != nil {
			return result, fmt.Errorf("historical calendar resource %d request: %w", index, err)
		}
		resource := providers.NewRawResource(
			configured.Kind,
			fmt.Sprintf("%s-%d-revision-%03d-%s", strings.ToLower(request.MIC), request.Year, request.Revision, configured.Kind),
			body,
			c.now().UTC().Truncate(time.Microsecond),
			configured.ContentType,
		)
		resource.Year = request.Year
		resource.URL = requestURL
		resource.ParserVersion = ParserVersion
		resource.ParserMetadata = map[string]string{
			"source":           request.Source,
			"mic":              strings.ToUpper(request.MIC),
			"year":             fmt.Sprint(request.Year),
			"revision":         fmt.Sprint(request.Revision),
			"expected_sha256":  configured.ExpectedSHA256,
			"source_semantics": "immutable official artifact; exact-hash declarative calendar transcription",
		}
		result.Resources = append(result.Resources, resource)
		if err := validateBody(resource, configured.ExpectedSHA256); err != nil {
			return result, fmt.Errorf("historical calendar resource %d: %w", index, err)
		}
	}
	return result, nil
}

func normalizeURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid official artifact URL %q", value)
	}
	return parsed.String(), nil
}

func validateBody(resource providers.RawResource, expectedSHA256 string) error {
	if resource.SHA256 != expectedSHA256 {
		return fmt.Errorf("SHA-256 %s does not match pinned %s", resource.SHA256, expectedSHA256)
	}
	switch resource.ContentType {
	case "application/pdf":
		if !bytes.HasPrefix(resource.Bytes, []byte("%PDF-")) {
			return errors.New("pinned PDF resource has no PDF signature")
		}
	case "text/html; charset=utf-8":
		prefix := strings.ToLower(string(resource.Bytes[:min(len(resource.Bytes), 1024)]))
		if !strings.Contains(prefix, "<html") && !strings.Contains(prefix, "<!doctype html") {
			return errors.New("pinned HTML resource has no HTML document marker")
		}
	default:
		return fmt.Errorf("unsupported pinned content type %q", resource.ContentType)
	}
	return nil
}
