package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"golang.org/x/time/rate"
)

// TestCloudClientListProjectsPaginatesProjectSearch is the regression for
// WI-1704: Cloud discovery must walk the supported paginated
// GET /rest/api/3/project/search endpoint, enumerate every page even when the
// server caps the page size below the requested one, and preserve
// descriptions. The deprecated unpaginated GET /project endpoint must not be
// called.
func TestCloudClientListProjectsPaginatesProjectSearch(t *testing.T) {
	t.Parallel()

	all := []map[string]any{
		{"id": "1", "key": "ALPHA", "name": "Alpha", "description": "First project"},
		{"id": "2", "key": "BRAVO", "name": "Bravo", "description": "Second project"},
		{"id": "3", "key": "CHARLIE", "name": "Charlie", "description": "Third project"},
		{"id": "4", "key": "DELTA", "name": "Delta", "description": "Fourth project"},
		{"id": "5", "key": "ECHO", "name": "Echo", "description": "Fifth project"},
	}
	const serverPageSize = 2

	deprecatedCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/project" {
			deprecatedCalls++
			http.Error(w, "deprecated endpoint", http.StatusGone)
			return
		}
		if r.URL.Path != "/rest/api/3/project/search" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		if got := r.URL.Query().Get("expand"); got != "description" {
			t.Errorf("expand = %q, want description", got)
		}

		startAt, err := strconv.Atoi(r.URL.Query().Get("startAt"))
		if err != nil {
			t.Errorf("startAt = %q: %v", r.URL.Query().Get("startAt"), err)
		}
		end := startAt + serverPageSize
		if end > len(all) {
			end = len(all)
		}
		if startAt > len(all) {
			startAt = len(all)
		}
		values, err := json.Marshal(all[startAt:end])
		if err != nil {
			t.Errorf("marshal values: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"startAt":%d,"maxResults":%d,"total":%d,"isLast":%v,"values":%s}`,
			startAt, serverPageSize, len(all), end >= len(all), values)
	}))
	t.Cleanup(server.Close)

	client := &cloudClient{
		baseURL:    server.URL + "/rest/api/3",
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}

	projects, err := client.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(projects) != len(all) {
		t.Fatalf("len(projects) = %d, want %d", len(projects), len(all))
	}
	for i, want := range all {
		got := projects[i]
		if got.ID != want["id"] || got.Key != want["key"] || got.Name != want["name"] || got.Description != want["description"] {
			t.Errorf("projects[%d] = %#v, want %#v", i, got, want)
		}
	}
	if deprecatedCalls != 0 {
		t.Errorf("deprecated GET /project called %d times, want 0", deprecatedCalls)
	}
}

// TestCloudClientListProjectsPropagatesPageError ensures a later-page failure
// surfaces instead of silently returning the first page as the full list.
func TestCloudClientListProjectsPropagatesPageError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("startAt") == "0" {
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":1,"total":2,"isLast":false,"values":[{"id":"1","key":"ALPHA","name":"Alpha"}]}`))
			return
		}
		http.Error(w, `{"errorMessages":["server error"]}`, http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	client := &cloudClient{
		baseURL:    server.URL + "/rest/api/3",
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}

	if _, err := client.ListProjects(context.Background()); err == nil {
		t.Fatal("ListProjects() error = nil, want later-page failure")
	}
}
