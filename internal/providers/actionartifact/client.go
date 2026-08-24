// Package actionartifact retains exact official corporate-action resources
// behind configured SHA-256 pins before declarative canonical publication.
package actionartifact

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

const ParserVersion = "corporate-action-artifact-v1"

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

type Result struct {
	providers.ResourceResult
}

func (c *Client) Collect(ctx context.Context, source string, requests []ResourceRequest) (Result, error) {
	if c == nil || c.http == nil {
		return Result{}, errors.New("corporate-action artifact HTTP getter is required")
	}
	if strings.TrimSpace(source) == "" || len(requests) == 0 {
		return Result{}, errors.New("corporate-action artifact source and resources are required")
	}
	result := Result{ResourceResult: providers.ResourceResult{
		Resources: make([]providers.RawResource, 0, len(requests)),
	}}
	for index, request := range requests {
		requestURL, err := normalizeURL(request.URL)
		if err != nil {
			return result, fmt.Errorf("corporate-action resource %d: %w", index, err)
		}
		body, err := c.http.Get(ctx, requestURL)
		if err != nil {
			return result, fmt.Errorf("corporate-action resource %d request: %w", index, err)
		}
		resource := providers.NewRawResource(
			request.Kind,
			strings.ToLower(source)+"-corporate-action-"+request.Kind,
			body,
			c.now().UTC().Truncate(time.Microsecond),
			request.ContentType,
		)
		resource.URL = requestURL
		resource.ParserVersion = ParserVersion
		resource.ParserMetadata = map[string]string{
			"source":           source,
			"expected_sha256":  request.ExpectedSHA256,
			"source_semantics": "exact official artifact with source-located declarative action transcription",
		}
		result.Resources = append(result.Resources, resource)
		if err := validateBody(resource, request.ExpectedSHA256); err != nil {
			return result, fmt.Errorf("corporate-action resource %d: %w", index, err)
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
	case "application/zip":
		if !bytes.HasPrefix(resource.Bytes, []byte("PK\x03\x04")) {
			return errors.New("pinned ZIP resource has no ZIP signature")
		}
	case "text/html; charset=utf-8":
		prefix := strings.ToLower(string(resource.Bytes[:min(len(resource.Bytes), 2048)]))
		if !strings.Contains(prefix, "<html") && !strings.Contains(prefix, "<!doctype html") {
			return errors.New("pinned HTML resource has no HTML document marker")
		}
	default:
		return fmt.Errorf("unsupported pinned content type %q", resource.ContentType)
	}
	return nil
}
