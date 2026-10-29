package jira

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"
)

func TestJiraClientsGetIssueWatchers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		apiPath string
		client  func(string, *http.Client) IssueWatchersClient
	}{
		{
			name:    "Cloud",
			apiPath: "/rest/api/3",
			client: func(baseURL string, httpClient *http.Client) IssueWatchersClient {
				return &cloudClient{
					baseURL:    baseURL,
					httpClient: httpClient,
					limiter:    rate.NewLimiter(rate.Inf, 1),
				}
			},
		},
		{
			name:    "Data Center",
			apiPath: "/rest/api/2",
			client: func(baseURL string, httpClient *http.Client) IssueWatchersClient {
				return &dataCenterClient{
					baseURL:    baseURL,
					httpClient: httpClient,
					limiter:    rate.NewLimiter(rate.Inf, 1),
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != test.apiPath+"/issue/APP-1/watchers" {
					http.Error(w, "unexpected request "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{
					"watchCount": 2,
					"isWatching": false,
					"watchers": [
						{"accountId":"cloud-1","name":"dc-1","displayName":"First"},
						{"accountId":"cloud-2","name":"dc-2","displayName":"Second"}
					]
				}`))
			}))
			t.Cleanup(server.Close)

			result, err := test.client(server.URL+test.apiPath, server.Client()).
				GetIssueWatchers(context.Background(), "APP-1")
			if err != nil {
				t.Fatalf("GetIssueWatchers() error = %v", err)
			}
			if result.WatchCount != 2 || len(result.Watchers) != 2 ||
				result.Watchers[0].DisplayName != "First" {
				t.Fatalf("watchers = %#v", result)
			}
		})
	}
}
