package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, attempts int) *Client {
	t.Helper()
	c, err := New(Config{UserAgent: "invs-test test@example.com", Timeout: time.Second, RequestsPerSecond: 1000, Burst: 1, MaxAttempts: attempts, InitialBackoff: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGetRedactsQueryCredentialsFromStatusError(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	}))
	defer s.Close()
	const secret = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	_, err := testClient(t, 1).Get(context.Background(), s.URL+"/observations?series_id=CPIAUCSL&api_key="+secret)
	if err == nil {
		t.Fatal("expected status error")
	}
	message := err.Error()
	if strings.Contains(message, secret) || !strings.Contains(message, "api_key=%5BREDACTED%5D") || !strings.Contains(message, "series_id=CPIAUCSL") {
		t.Fatalf("status error did not safely preserve request context: %v", err)
	}
}

func TestGetRetriesAndSendsUserAgent(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.UserAgent() != "invs-test test@example.com" {
			t.Errorf("unexpected UA %q", r.UserAgent())
		}
		if calls.Add(1) < 2 {
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer s.Close()
	b, err := testClient(t, 3).Get(context.Background(), s.URL)
	if err != nil || string(b) != "ok" || calls.Load() != 2 {
		t.Fatalf("body=%q calls=%d err=%v", b, calls.Load(), err)
	}
}

func TestGetCancellationStopsBackoff(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", 503) }))
	defer s.Close()
	c := testClient(t, 4)
	c.initialBackoff = time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Get(ctx, s.URL)
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("cancellation ignored: %v", err)
	}
}
