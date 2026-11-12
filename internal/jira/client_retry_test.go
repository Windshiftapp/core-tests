package jira

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestJiraClientsRetryRateLimitsAndServiceUnavailable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(
			context.Context,
			*http.Client,
			*rate.Limiter,
			func(context.Context, time.Duration) error,
			string,
		) (*http.Response, error)
	}{
		{
			name: "cloud",
			call: func(
				ctx context.Context,
				httpClient *http.Client,
				limiter *rate.Limiter,
				wait func(context.Context, time.Duration) error,
				requestURL string,
			) (*http.Response, error) {
				return (&cloudClient{
					httpClient:    httpClient,
					limiter:       limiter,
					retryAttempts: 3,
					retryWait:     wait,
				}).do(ctx, http.MethodGet, requestURL, nil)
			},
		},
		{
			name: "data center",
			call: func(
				ctx context.Context,
				httpClient *http.Client,
				limiter *rate.Limiter,
				wait func(context.Context, time.Duration) error,
				requestURL string,
			) (*http.Response, error) {
				return (&dataCenterClient{
					httpClient:    httpClient,
					limiter:       limiter,
					retryAttempts: 3,
					retryWait:     wait,
				}).do(ctx, http.MethodGet, requestURL, nil)
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				switch requests {
				case 1:
					w.Header().Set("Retry-After", "2")
					http.Error(w, "rate limited", http.StatusTooManyRequests)
				case 2:
					http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
				default:
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"ok":true}`))
				}
			}))
			t.Cleanup(server.Close)

			var delays []time.Duration
			response, err := tt.call(
				context.Background(),
				server.Client(),
				rate.NewLimiter(rate.Inf, 1),
				func(_ context.Context, delay time.Duration) error {
					delays = append(delays, delay)
					return nil
				},
				server.URL+"/rest/api/3/field",
			)
			if err != nil {
				t.Fatalf("request error = %v", err)
			}
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", response.StatusCode)
			}
			var body map[string]bool
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if !body["ok"] {
				t.Fatalf("response = %#v, want ok", body)
			}
			if requests != 3 {
				t.Fatalf("requests = %d, want 3", requests)
			}
			if len(delays) != 2 {
				t.Fatalf("retry delays = %#v, want 2 delays", delays)
			}
			if delays[0] != 2*time.Second {
				t.Fatalf("Retry-After delay = %v, want the exact 2s", delays[0])
			}
			// Without Retry-After the backoff is jittered, so only its bounds are
			// contractual: equal jitter over the 500ms base for attempt 1.
			if delays[1] < 250*time.Millisecond || delays[1] > 500*time.Millisecond {
				t.Fatalf("backoff delay = %v, want within [250ms, 500ms]", delays[1])
			}
		})
	}
}

func TestCloudClientRetriesReadOnlyPostWithOriginalBody(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request %d: %v", requests, err)
		}
		if body["query"] != "project = APP" {
			t.Fatalf("request %d body = %#v", requests, body)
		}
		if requests == 1 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)

	client := &cloudClient{
		httpClient:    server.Client(),
		limiter:       rate.NewLimiter(rate.Inf, 1),
		retryAttempts: 1,
		retryWait:     func(context.Context, time.Duration) error { return nil },
	}
	response, err := client.do(
		context.Background(),
		http.MethodPost,
		server.URL+"/rest/api/3/search/jql",
		map[string]string{"query": "project = APP"},
	)
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK || requests != 2 {
		t.Fatalf("status = %d requests = %d, want 200 and 2", response.StatusCode, requests)
	}
}

// TestJiraDownloadAttachmentRetriesRateLimit is the WI-2010 contract: the
// attachment download path shares the JSON path's retry behavior instead of
// dropping the file on the first 429.
func TestJiraDownloadAttachmentRetriesRateLimit(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 1 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("attachment body"))
	}))
	t.Cleanup(server.Close)

	reader, contentType, err := jiraDownloadAttachment(
		context.Background(),
		server.Client(),
		rate.NewLimiter(rate.Inf, 1),
		&jiraThrottle{},
		"Bearer token",
		server.URL+"/attachment",
	)
	if err != nil {
		t.Fatalf("download error = %v", err)
	}
	defer func() { _ = reader.Close() }()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if requests != 2 || string(body) != "attachment body" || contentType != "text/plain" {
		t.Fatalf("requests=%d body=%q contentType=%q, want a retried download", requests, body, contentType)
	}
}

type failingTransport struct {
	err   error
	calls int
}

func (f *failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.calls++
	return nil, f.err
}

// TestJiraRetryRequestRetriesTransientStatuses covers the broader retryable set
// so a proxy 500/502/504 does not fail the import immediately.
func TestJiraRetryRequestRetriesTransientStatuses(t *testing.T) {
	t.Parallel()

	statuses := []int{
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	}
	for _, status := range statuses {
		status := status
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				if requests == 1 {
					http.Error(w, "transient", status)
					return
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			t.Cleanup(server.Close)

			var delays []time.Duration
			response, err := jiraRetryRequest(
				context.Background(),
				server.Client(),
				rate.NewLimiter(rate.Inf, 1),
				&jiraThrottle{},
				1,
				func(_ context.Context, delay time.Duration) error {
					delays = append(delays, delay)
					return nil
				},
				func() (*http.Request, error) {
					return http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/rest/api/3/field", http.NoBody)
				},
			)
			if err != nil {
				t.Fatalf("request error = %v", err)
			}
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != http.StatusOK || requests != 2 || len(delays) != 1 {
				t.Fatalf("status=%d requests=%d delays=%v, want one retry", response.StatusCode, requests, delays)
			}
		})
	}
}

// TestJiraRetryRequestRetriesNetworkErrors covers a dropped connection, which
// previously failed the request outright.
func TestJiraRetryRequestRetriesNetworkErrors(t *testing.T) {
	t.Parallel()

	transport := &failingTransport{err: errors.New("connection reset by peer")}
	client := &http.Client{Transport: transport}
	var delays []time.Duration
	_, err := jiraRetryRequest(
		context.Background(),
		client,
		rate.NewLimiter(rate.Inf, 1),
		&jiraThrottle{},
		2,
		func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		},
		func() (*http.Request, error) {
			return http.NewRequestWithContext(context.Background(), http.MethodGet, "https://jira.example.invalid/rest/api/3/field", http.NoBody)
		},
	)
	if err == nil {
		t.Fatal("request error = nil, want the network error after retries")
	}
	if transport.calls != 3 || len(delays) != 2 {
		t.Fatalf("calls=%d delays=%v, want 3 attempts and 2 backoffs", transport.calls, delays)
	}
}

// TestJiraThrottleCooldownSemantics pins the shared cooldown contract: it never
// shortens, and wait observes an active cooldown.
func TestJiraThrottleCooldownSemantics(t *testing.T) {
	t.Parallel()

	if err := (&jiraThrottle{}).wait(context.Background()); err != nil {
		t.Fatalf("idle throttle wait = %v, want nil", err)
	}

	throttle := &jiraThrottle{}
	before := time.Now()
	throttle.pause(2 * time.Second)
	throttle.mu.Lock()
	until := throttle.until
	throttle.mu.Unlock()
	if until.Before(before.Add(2*time.Second)) || until.After(time.Now().Add(2*time.Second)) {
		t.Fatalf("cooldown until = %v, want ~now+2s", until)
	}

	throttle.pause(time.Millisecond)
	throttle.mu.Lock()
	afterShortPause := throttle.until
	throttle.mu.Unlock()
	if !afterShortPause.Equal(until) {
		t.Fatalf("shorter pause moved cooldown from %v to %v", until, afterShortPause)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := throttle.wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait during cooldown = %v, want context.Canceled", err)
	}
}

// TestJiraRetryRequestPausesSharedThrottleOnRateLimit proves one worker's 429
// pauses the whole client instead of only itself.
func TestJiraRetryRequestPausesSharedThrottleOnRateLimit(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Retry-After", "30")
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	throttle := &jiraThrottle{}
	var delays []time.Duration
	_, err := jiraRetryRequest(
		ctx,
		server.Client(),
		rate.NewLimiter(rate.Inf, 1),
		throttle,
		1,
		func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			// Cancel so the next attempt's cooldown wait returns immediately
			// instead of blocking for the real 30s window.
			cancel()
			return nil
		},
		func() (*http.Request, error) {
			return http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/rest/api/3/field", http.NoBody)
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("request error = %v, want context.Canceled from the cooldown wait", err)
	}
	if len(delays) != 1 || delays[0] != 30*time.Second {
		t.Fatalf("delays = %v, want [30s]", delays)
	}
	throttle.mu.Lock()
	until := throttle.until
	throttle.mu.Unlock()
	if !until.After(time.Now()) {
		t.Fatalf("throttle cooldown until = %v, want a future pause from the Retry-After window", until)
	}
}
