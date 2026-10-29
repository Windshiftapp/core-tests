package jira

import (
	"context"
	"encoding/json"
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
			if len(delays) != 2 || delays[0] != 2*time.Second || delays[1] != 500*time.Millisecond {
				t.Fatalf("retry delays = %#v, want [2s 500ms]", delays)
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
