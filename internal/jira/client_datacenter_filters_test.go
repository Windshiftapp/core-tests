package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"
)

// TestDataCenterListFiltersUsesFavoriteEndpoint is the WI-2076 regression:
// Data Center has no Cloud-style project filter search, so discovery must use
// the supported favorite-filter endpoint and report the enumeration boundary.
func TestDataCenterListFiltersUsesFavoriteEndpoint(t *testing.T) {
	t.Parallel()

	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/2/filter/favourite":
			_ = json.NewEncoder(w).Encode([]JiraFilter{{
				ID: "10001", Name: "My open bugs", JQL: "project = HELP AND status = Open",
			}})
		case "/rest/api/2/filter/search":
			http.Error(w, "unsupported", http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client := &dataCenterClient{
		baseURL:    server.URL + "/rest/api/2",
		authHeader: "Bearer pat",
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}

	result, err := client.ListFilters(context.Background(), "HELP")
	if err != nil {
		t.Fatalf("ListFilters() error = %v", err)
	}
	if len(result.Values) != 1 || result.Values[0].ID != "10001" {
		t.Fatalf("filters = %#v, want the favorite filter", result.Values)
	}
	if !result.Partial || result.PartialReason == "" {
		t.Fatalf("result = %#v, want partial discovery with a reason", result)
	}
	if len(paths) != 1 || paths[0] != "/rest/api/2/filter/favourite" {
		t.Fatalf("request paths = %v, want only /rest/api/2/filter/favourite", paths)
	}
}
