//go:build test

package data

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientGetWorkspacesLoadsEveryPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if got := r.URL.Query().Get("page_size"); got != "100" {
			t.Errorf("page_size = %q, want 100", got)
		}
		data := []map[string]any{{"id": 1, "key": "ONE", "name": "One"}}
		pageNumber := 1
		if page == "2" {
			data = []map[string]any{{"id": 2, "key": "TWO", "name": "Two"}}
			pageNumber = 2
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":       data,
			"pagination": map[string]any{"page": pageNumber, "page_size": 100, "total_items": 2, "total_pages": 2},
		})
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.URL)
	workspaces, err := client.getWorkspaces()
	if err != nil {
		t.Fatalf("getWorkspaces: %v", err)
	}
	if len(workspaces) != 2 || workspaces[0].Key != "ONE" || workspaces[1].Key != "TWO" {
		t.Fatalf("workspaces = %+v, want both pages in order", workspaces)
	}
}

func TestClientGetWorkspacesDoesNotReturnPartialResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			http.Error(w, "second page failed", http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":       []map[string]any{{"id": 1, "key": "ONE", "name": "One"}},
			"pagination": map[string]any{"page": 1, "page_size": 100, "total_items": 2, "total_pages": 2},
		})
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.URL)
	workspaces, err := client.getWorkspaces()
	if err == nil || workspaces != nil {
		t.Fatalf("getWorkspaces = (%+v, %v), want nil results and an error", workspaces, err)
	}
}

func TestWorkItemFromDTORetainsCanonicalKey(t *testing.T) {
	item := workItemFromDTO(itemDTO{
		ID: 900, WorkspaceID: 7, WorkspaceKey: "WI", Key: "WI-42", WorkspaceItemNumber: 42,
		Title: "Canonical", CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(2, 0),
		Transitions: []transitionDTO{{ToStatusID: 3, ToStatus: &statusSummaryDTO{ID: 3, Name: "Done"}}},
	})
	if item.Key != "WI-42" || item.WorkspaceItemNumber != 42 {
		t.Fatalf("item = %+v, want canonical key fields", item)
	}
	if got := item.DisplayKey("WRONG"); got != "WI-42" {
		t.Fatalf("DisplayKey = %q, want WI-42", got)
	}
	if len(item.Transitions) != 1 || item.Transitions[0].ID != 3 || item.Transitions[0].Name != "Done" {
		t.Fatalf("transitions = %+v, want server-filtered Done transition", item.Transitions)
	}
}
