package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"
)

// TestCloudSearchObjectsHonorsExplicitStartAt is the WI-2070 regression: the
// Assets search must be able to continue from an exact offset rather than only
// from a requested page size.
func TestCloudSearchObjectsHonorsExplicitStartAt(t *testing.T) {
	t.Parallel()

	var startAts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/jsm/assets/workspace/ws/v1/object/aql" {
			t.Fatalf("path = %s, want the Assets AQL path", r.URL.Path)
		}
		startAts = append(startAts, r.URL.Query().Get("startAt"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"values":     []any{},
			"startAt":    25,
			"maxResults": 100,
			"total":      0,
			"isLast":     true,
		})
	}))
	t.Cleanup(server.Close)

	client := &cloudClient{
		assetsURL:  server.URL + "/jsm/assets/workspace/ws/v1",
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}

	result, err := client.SearchObjects(context.Background(), ObjectSearchOptions{
		ObjectSchemaID: "7", StartAt: 25, Page: 1, PageSize: 100, IncludeAttributes: true,
	})
	if err != nil {
		t.Fatalf("SearchObjects() error = %v", err)
	}
	if len(startAts) != 1 || startAts[0] != "25" {
		t.Fatalf("requested startAt = %v, want [25]", startAts)
	}
	if result.StartIndex != 25 || !result.IsLast {
		t.Fatalf("result = %#v, want startIndex 25 and isLast", result)
	}
}
