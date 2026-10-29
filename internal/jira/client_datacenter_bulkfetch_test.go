package jira

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/time/rate"
)

// dataCenterBulkFetchClient points a Data Center client at a local search
// server that caps every page at serverCap results.
func dataCenterBulkFetchClient(t *testing.T, handler http.HandlerFunc) (*dataCenterClient, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &dataCenterClient{
		baseURL:    server.URL + "/rest/api/2",
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}, server
}

func writeDataCenterSearchPage(w http.ResponseWriter, allKeys []string, startAt, pageSize int) {
	if startAt > len(allKeys) {
		startAt = len(allKeys)
	}
	end := startAt + pageSize
	if end > len(allKeys) {
		end = len(allKeys)
	}
	issues := make([]string, 0, end-startAt)
	for _, key := range allKeys[startAt:end] {
		issues = append(issues, fmt.Sprintf(`{"id":%q,"key":%q}`, key, key))
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"startAt":%d,"maxResults":%d,"total":%d,"issues":[%s]}`,
		startAt, pageSize, len(allKeys), strings.Join(issues, ","))
}

// TestDataCenterBulkFetchIssuesWalksServerCappedPages is the WI-1702
// regression: when the Data Center search caps a page below the requested
// maxResults, the client must follow startAt/len offsets until every key is
// fetched instead of silently dropping the rest of the batch.
func TestDataCenterBulkFetchIssuesWalksServerCappedPages(t *testing.T) {
	t.Parallel()

	allKeys := []string{"APP-1", "APP-2", "APP-3", "APP-4", "APP-5"}
	const serverCap = 2

	requests := 0
	client, _ := dataCenterBulkFetchClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/2/search" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		requests++
		startAt, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
		writeDataCenterSearchPage(w, allKeys, startAt, serverCap)
	})

	result, err := client.BulkFetchIssues(context.Background(), BulkFetchRequest{IssueIdsOrKeys: allKeys})
	if err != nil {
		t.Fatalf("BulkFetchIssues() error = %v", err)
	}
	if len(result.Issues) != len(allKeys) {
		t.Fatalf("len(issues) = %d, want %d", len(result.Issues), len(allKeys))
	}
	for i, key := range allKeys {
		if result.Issues[i].Key != key {
			t.Errorf("issues[%d].Key = %q, want %q", i, result.Issues[i].Key, key)
		}
	}
	if requests != 3 {
		t.Fatalf("search requests = %d, want 3 (2+2+1)", requests)
	}
}

// TestDataCenterBulkFetchIssuesStopsOnPrematureEmptyPage keeps a truncated
// server response from looping forever: the client returns what it safely
// fetched and the caller's key reconciliation counts the rest as failures.
func TestDataCenterBulkFetchIssuesStopsOnPrematureEmptyPage(t *testing.T) {
	t.Parallel()

	allKeys := []string{"APP-1", "APP-2", "APP-3", "APP-4", "APP-5"}
	client, _ := dataCenterBulkFetchClient(t, func(w http.ResponseWriter, r *http.Request) {
		startAt, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
		if startAt > 0 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"startAt":2,"maxResults":2,"total":5,"issues":[]}`))
			return
		}
		writeDataCenterSearchPage(w, allKeys, 0, 2)
	})

	result, err := client.BulkFetchIssues(context.Background(), BulkFetchRequest{IssueIdsOrKeys: allKeys})
	if err != nil {
		t.Fatalf("BulkFetchIssues() error = %v", err)
	}
	if len(result.Issues) != 2 {
		t.Fatalf("len(issues) = %d, want the 2 safely fetched issues", len(result.Issues))
	}
}

func TestDataCenterBulkFetchIssuesPropagatesLaterPageFailure(t *testing.T) {
	t.Parallel()

	allKeys := []string{"APP-1", "APP-2", "APP-3", "APP-4"}
	client, _ := dataCenterBulkFetchClient(t, func(w http.ResponseWriter, r *http.Request) {
		startAt, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
		if startAt > 0 {
			http.Error(w, `{"errorMessages":["search failed"]}`, http.StatusInternalServerError)
			return
		}
		writeDataCenterSearchPage(w, allKeys, 0, 2)
	})

	if _, err := client.BulkFetchIssues(context.Background(), BulkFetchRequest{IssueIdsOrKeys: allKeys}); err == nil {
		t.Fatal("BulkFetchIssues() error = nil, want later-page failure")
	}
}
