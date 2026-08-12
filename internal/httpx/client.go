package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

const maxResponseBytes = 256 << 20

type Config struct {
	UserAgent          string
	Timeout            time.Duration
	RequestsPerSecond  float64
	Burst, MaxAttempts int
	InitialBackoff     time.Duration
}

type Client struct {
	http           *http.Client
	limiter        *rate.Limiter
	userAgent      string
	maxAttempts    int
	initialBackoff time.Duration
}

func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.UserAgent) == "" {
		return nil, errors.New("user agent is required")
	}
	if cfg.Timeout <= 0 || cfg.RequestsPerSecond <= 0 || cfg.Burst <= 0 || cfg.MaxAttempts <= 0 || cfg.InitialBackoff <= 0 {
		return nil, errors.New("HTTP timeout, rate, burst, attempts and backoff must be positive")
	}
	return &Client{
		http:      &http.Client{Timeout: cfg.Timeout},
		limiter:   rate.NewLimiter(rate.Limit(cfg.RequestsPerSecond), cfg.Burst),
		userAgent: cfg.UserAgent, maxAttempts: cfg.MaxAttempts, initialBackoff: cfg.InitialBackoff,
	}, nil
}

func (c *Client) Get(ctx context.Context, url string) ([]byte, error) {
	var last error
	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit wait: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("User-Agent", c.userAgent)
		req.Header.Set("Accept-Encoding", "identity")
		resp, err := c.http.Do(req)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
			closeErr := resp.Body.Close()
			if readErr != nil {
				err = readErr
			} else if closeErr != nil {
				err = closeErr
			}
			if len(body) > maxResponseBytes {
				return nil, fmt.Errorf("response exceeded %d bytes", maxResponseBytes)
			}
			if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return body, nil
			}
			if err == nil {
				snippet := strings.TrimSpace(string(body))
				if len(snippet) > 300 {
					snippet = snippet[:300]
				}
				err = &StatusError{Code: resp.StatusCode, URL: url, Body: snippet}
				if !retryable(resp.StatusCode) {
					return nil, err
				}
				if delay := retryAfter(resp.Header.Get("Retry-After")); delay > 0 {
					if waitErr := wait(ctx, delay); waitErr != nil {
						return nil, waitErr
					}
					last = err
					continue
				}
			}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		last = err
		if attempt < c.maxAttempts {
			if err := wait(ctx, c.initialBackoff*time.Duration(1<<(attempt-1))); err != nil {
				return nil, err
			}
		}
	}
	return nil, fmt.Errorf("request failed after %d attempts: %w", c.maxAttempts, last)
}

type StatusError struct {
	Code      int
	URL, Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("GET %s: HTTP %d: %s", e.URL, e.Code, e.Body)
}

func retryable(code int) bool {
	return code == http.StatusTooManyRequests || code == http.StatusRequestTimeout || code >= 500
}
func retryAfter(v string) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(v); err == nil {
		if d := time.Until(at); d > 0 {
			return d
		}
	}
	return 0
}
func wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
