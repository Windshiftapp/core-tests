package tests

import (
	"fmt"
	"net/http"
	"testing"
)

type itemBulkResult struct {
	RequestedCount    int `json:"requested_count"`
	TransitionedCount int `json:"transitioned_count"`
	UnchangedCount    int `json:"unchanged_count"`
	DeletedCount      int `json:"deleted_count"`
	FailedCount       int `json:"failed_count"`
	Failures          []struct {
		ItemID  int    `json:"item_id"`
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"failures"`
}

// TestItemBulkTransitionAndDeleteHTTP drives the two batch endpoints end to
// end: one request moves every selected ticket, one request deletes the rest,
// and per-item workflow rejections come back as failures rather than aborting
// the batch.
func TestItemBulkTransitionAndDeleteHTTP(t *testing.T) {
	env := setupConditionTestData(t)

	first := env.itemID
	second := CreateTestItem(t, env.server, env.workspaceID, "Bulk second")
	third := CreateTestItem(t, env.server, env.workspaceID, "Bulk third")

	resp := MakeAuthRequest(t, env.server, http.MethodPost, "/v2/items/bulk-transition", map[string]any{
		"item_ids":     []int{first, second},
		"to_status_id": env.doneID,
	})
	AssertStatusCode(t, resp, http.StatusOK)
	var transitioned itemBulkResult
	DecodeJSON(t, resp, &transitioned)
	resp.Body.Close()
	if transitioned.RequestedCount != 2 || transitioned.TransitionedCount != 2 || transitioned.FailedCount != 0 {
		t.Fatalf("bulk transition result = %+v, want 2/2/0", transitioned)
	}
	for _, id := range []int{first, second} {
		getResp := MakeAuthRequest(t, env.server, http.MethodGet, fmt.Sprintf("/v2/items/%d", id), nil)
		AssertStatusCode(t, getResp, http.StatusOK)
		var item map[string]any
		DecodeJSON(t, getResp, &item)
		getResp.Body.Close()
		if int(item["status_id"].(float64)) != env.doneID {
			t.Fatalf("item %d status = %v, want %d", id, item["status_id"], env.doneID)
		}
	}

	// Done has no outgoing edge in the fixture workflow, so a second bulk
	// transition reports a per-item failure without touching the rest.
	resp = MakeAuthRequest(t, env.server, http.MethodPost, "/v2/items/bulk-transition", map[string]any{
		"item_ids":     []int{first, third},
		"to_status_id": env.inProgressID,
	})
	AssertStatusCode(t, resp, http.StatusOK)
	var rejected itemBulkResult
	DecodeJSON(t, resp, &rejected)
	resp.Body.Close()
	if rejected.TransitionedCount != 1 || rejected.FailedCount != 1 || len(rejected.Failures) != 1 {
		t.Fatalf("partial bulk transition result = %+v, want one move and one failure", rejected)
	}
	if rejected.Failures[0].ItemID != first || rejected.Failures[0].Code != "workflow_invalid" {
		t.Fatalf("failure = %+v, want workflow_invalid for item %d", rejected.Failures[0], first)
	}

	resp = MakeAuthRequest(t, env.server, http.MethodPost, "/v2/items/bulk-delete", map[string]any{
		"item_ids": []int{first, second, third},
	})
	AssertStatusCode(t, resp, http.StatusOK)
	var deleted itemBulkResult
	DecodeJSON(t, resp, &deleted)
	resp.Body.Close()
	if deleted.RequestedCount != 3 || deleted.DeletedCount != 3 || deleted.FailedCount != 0 {
		t.Fatalf("bulk delete result = %+v, want 3/3/0", deleted)
	}
	for _, id := range []int{first, second, third} {
		getResp := MakeAuthRequest(t, env.server, http.MethodGet, fmt.Sprintf("/v2/items/%d", id), nil)
		AssertStatusCode(t, getResp, http.StatusNotFound)
		getResp.Body.Close()
	}
}
