package tests

import (
	"fmt"
	"net/http"
	"testing"
)

type bulkLabelResponse struct {
	RequestedCount int `json:"requested_count"`
	UpdatedCount   int `json:"updated_count"`
	UnchangedCount int `json:"unchanged_count"`
	FailedCount    int `json:"failed_count"`
	Failures       []struct {
		ItemID  int    `json:"item_id"`
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"failures"`
}

func postBulkLabels(t *testing.T, ts *TestServer, body map[string]any) (*http.Response, bulkLabelResponse) {
	t.Helper()
	resp := MakeAuthRequest(t, ts, http.MethodPost, "/v2/items/bulk-labels", body)
	var result bulkLabelResponse
	if resp.StatusCode == http.StatusOK {
		DecodeJSON(t, resp, &result)
	}
	return resp, result
}

func itemLabelIDs(t *testing.T, ts *TestServer, itemID int) []int {
	t.Helper()
	resp := MakeAuthRequest(t, ts, http.MethodGet, fmt.Sprintf("/v2/items/%d/labels", itemID), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var labels []struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, resp, &labels)
	ids := make([]int, len(labels))
	for i := range labels {
		ids[i] = labels[i].ID
	}
	return ids
}

func hasLabelID(ids []int, want int) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// TestItemBulkLabelsHTTP drives the batch label endpoint: add, remove, and set
// apply to every selected item in one request and per-item failures come back
// without aborting the rest.
func TestItemBulkLabelsHTTP(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Bulk Labels", shortKey("BLB"))
	first := CreateTestItem(t, server, workspaceID, "Bulk labels first")
	second := CreateTestItem(t, server, workspaceID, "Bulk labels second")
	bug := createLabelFx(t, server, workspaceID, "bulk-bug", "#ef4444")
	feature := createLabelFx(t, server, workspaceID, "bulk-feature", "#22c55e")
	customer := createLabelFx(t, server, workspaceID, "bulk-customer", "#f59e0b")

	t.Run("add applies to every selected item", func(t *testing.T) {
		resp, result := postBulkLabels(t, server, map[string]any{
			"item_ids": []int{first, second},
			"add":      []int{bug.ID},
		})
		AssertStatusCode(t, resp, http.StatusOK)
		resp.Body.Close()
		if result.RequestedCount != 2 || result.UpdatedCount != 2 || result.FailedCount != 0 {
			t.Fatalf("add result = %+v, want 2/2/0", result)
		}
		for _, id := range []int{first, second} {
			if got := itemLabelIDs(t, server, id); len(got) != 1 || got[0] != bug.ID {
				t.Fatalf("item %d labels = %v, want [%d]", id, got, bug.ID)
			}
		}
	})

	t.Run("remove detaches the shared label", func(t *testing.T) {
		resp, result := postBulkLabels(t, server, map[string]any{
			"item_ids": []int{first, second},
			"remove":   []int{bug.ID},
		})
		AssertStatusCode(t, resp, http.StatusOK)
		resp.Body.Close()
		if result.UpdatedCount != 2 || result.FailedCount != 0 {
			t.Fatalf("remove result = %+v, want 2 updated", result)
		}
		for _, id := range []int{first, second} {
			if got := itemLabelIDs(t, server, id); len(got) != 0 {
				t.Fatalf("item %d labels = %v, want none", id, got)
			}
		}
	})

	t.Run("set replaces the full label set", func(t *testing.T) {
		setItemLabels(t, server, first, []int{bug.ID})
		setItemLabels(t, server, second, []int{bug.ID, feature.ID})
		resp, result := postBulkLabels(t, server, map[string]any{
			"item_ids": []int{first, second},
			"set":      []int{feature.ID, customer.ID},
		})
		AssertStatusCode(t, resp, http.StatusOK)
		resp.Body.Close()
		if result.UpdatedCount != 2 || result.FailedCount != 0 {
			t.Fatalf("set result = %+v, want 2 updated", result)
		}
		for _, id := range []int{first, second} {
			got := itemLabelIDs(t, server, id)
			if len(got) != 2 || !hasLabelID(got, feature.ID) || !hasLabelID(got, customer.ID) {
				t.Fatalf("item %d labels = %v, want feature+customer", id, got)
			}
		}
	})

	t.Run("unchanged items are reported separately", func(t *testing.T) {
		resp, result := postBulkLabels(t, server, map[string]any{
			"item_ids": []int{first, second},
			"add":      []int{feature.ID},
		})
		AssertStatusCode(t, resp, http.StatusOK)
		resp.Body.Close()
		if result.UpdatedCount != 0 || result.UnchangedCount != 2 || result.FailedCount != 0 {
			t.Fatalf("no-op result = %+v, want 0 updated / 2 unchanged", result)
		}
	})

	t.Run("missing item is a per-item failure", func(t *testing.T) {
		resp, result := postBulkLabels(t, server, map[string]any{
			"item_ids": []int{first, 99999999},
			"add":      []int{bug.ID},
		})
		AssertStatusCode(t, resp, http.StatusOK)
		resp.Body.Close()
		if result.UpdatedCount != 1 || result.FailedCount != 1 || len(result.Failures) != 1 {
			t.Fatalf("partial result = %+v, want one update and one failure", result)
		}
		if result.Failures[0].ItemID != 99999999 || result.Failures[0].Code != "not_found" {
			t.Fatalf("failure = %+v, want not_found for the missing item", result.Failures[0])
		}
		if got := itemLabelIDs(t, server, first); !hasLabelID(got, bug.ID) {
			t.Fatalf("existing item labels = %v, want the added label", got)
		}
	})
}

// TestItemBulkLabelsHTTPLargeSelection labels a whole multi-select batch in a
// single request, proving the endpoint does not degrade into a per-item loop
// as the selection grows.
func TestItemBulkLabelsHTTPLargeSelection(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Bulk Labels Large", shortKey("BLG"))
	label := createLabelFx(t, server, workspaceID, "bulk-large", "#0ea5e9")

	const count = 40
	ids := make([]int, count)
	for i := range ids {
		ids[i] = CreateTestItem(t, server, workspaceID, fmt.Sprintf("Bulk large item %d", i))
	}

	resp, result := postBulkLabels(t, server, map[string]any{"item_ids": ids, "add": []int{label.ID}})
	AssertStatusCode(t, resp, http.StatusOK)
	resp.Body.Close()
	if result.RequestedCount != count || result.UpdatedCount != count || result.FailedCount != 0 {
		t.Fatalf("large selection result = %+v, want %d/%d/0", result, count, count)
	}
	for _, id := range []int{ids[0], ids[count-1]} {
		if got := itemLabelIDs(t, server, id); len(got) != 1 || got[0] != label.ID {
			t.Fatalf("item %d labels = %v, want [%d]", id, got, label.ID)
		}
	}
}

// TestItemBulkLabelsHTTPValidation asserts the request-level rejections: an
// unknown label, an empty operation, a set mixed with a delta, and the item
// cap all fail before any item is touched.
func TestItemBulkLabelsHTTPValidation(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Bulk Labels Validation", shortKey("BLV"))
	item := CreateTestItem(t, server, workspaceID, "Bulk labels validation item")
	label := createLabelFx(t, server, workspaceID, "bulk-validation", "#2563eb")

	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{
			name: "unknown label",
			body: map[string]any{"item_ids": []int{item}, "add": []int{99999999}},
			want: http.StatusNotFound,
		},
		{
			name: "no operation",
			body: map[string]any{"item_ids": []int{item}},
			want: http.StatusBadRequest,
		},
		{
			name: "set combined with add",
			body: map[string]any{"item_ids": []int{item}, "set": []int{label.ID}, "add": []int{label.ID}},
			want: http.StatusBadRequest,
		},
		{
			name: "no items",
			body: map[string]any{"item_ids": []int{}, "add": []int{label.ID}},
			want: http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, _ := postBulkLabels(t, server, tc.body)
			defer resp.Body.Close()
			AssertStatusCode(t, resp, tc.want)
		})
	}

	t.Run("item cap", func(t *testing.T) {
		ids := make([]int, 501)
		for i := range ids {
			ids[i] = i + 1
		}
		resp, _ := postBulkLabels(t, server, map[string]any{"item_ids": ids, "add": []int{label.ID}})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	if got := itemLabelIDs(t, server, item); len(got) != 0 {
		t.Fatalf("rejected requests changed labels: %v", got)
	}
}

// TestItemBulkLabelsHTTPForbidden asserts the denial contract: a viewer who
// can see a locked-down workspace still cannot label its items, and the
// denial is reported per item without a 5xx.
func TestItemBulkLabelsHTTPForbidden(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	adminToken := CreateBearerToken(t, server)
	server.BearerToken = adminToken
	workspaceID, _ := CreateTestWorkspace(t, server, "Bulk Labels Locked", shortKey("BLL"))
	LockDownWorkspace(t, server, workspaceID)
	item := CreateTestItem(t, server, workspaceID, "Locked bulk labels item")
	label := createLabelFx(t, server, workspaceID, "bulk-locked", "#dc2626")

	viewerID, viewerUsername, viewerPassword := CreateTestUserWithCredentials(t, server, "bulk_label_viewer", "bulk_label_viewer@test.com")
	AssignWorkspaceRole(t, server, viewerID, workspaceID, "Viewer")
	viewerToken := CreateBearerTokenForUser(t, server, viewerUsername, viewerPassword)

	resp := MakeAuthRequestWithToken(t, server, viewerToken, http.MethodPost, "/v2/items/bulk-labels", map[string]any{
		"item_ids": []int{item},
		"add":      []int{label.ID},
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var result bulkLabelResponse
	DecodeJSON(t, resp, &result)
	if result.UpdatedCount != 0 || result.FailedCount != 1 || len(result.Failures) != 1 {
		t.Fatalf("viewer result = %+v, want one forbidden failure", result)
	}
	if result.Failures[0].ItemID != item || result.Failures[0].Code != "forbidden" {
		t.Fatalf("failure = %+v, want forbidden for item %d", result.Failures[0], item)
	}
	if got := itemLabelIDs(t, server, item); len(got) != 0 {
		t.Fatalf("denied request changed labels: %v", got)
	}
}
