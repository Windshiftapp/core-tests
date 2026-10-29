package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

// TestStoryPointRollupRecursive pins the WI-1341 contract: the item detail
// summary carries story_points_rollup summed across ALL descendant levels
// (not just direct children), counting only point-carrying descendants.
func TestStoryPointRollupRecursive(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "SP Recursive WS", shortKey("SPRC"))
	parent := createItemWithParentAndPoints(t, server, wsID, "Parent Story", "Story", 0, 0)
	level2 := createItemWithParentAndPoints(t, server, wsID, "Child A", "Task", parent, 3)
	createItemWithParentAndPoints(t, server, wsID, "Child B", "Task", parent, 5)
	createItemWithParentAndPoints(t, server, wsID, "Grandchild", "Sub-task", level2, 2)

	// A points-less intermediate level must not break the rollup: its own
	// subtree still contributes.
	createItemWithParentAndPoints(t, server, wsID, "Child C no points", "Task", parent, 0)

	// Detail summaries live on the bearer v2 surface.
	summaryResp := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/items/%d/detail-summary", parent), nil)
	defer summaryResp.Body.Close()
	AssertStatusCode(t, summaryResp, http.StatusOK)

	raw, err := io.ReadAll(summaryResp.Body)
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	var doc struct {
		Data struct {
			StoryPointsRollup *struct {
				Points       float64 `json:"points"`
				Contributors int     `json:"contributors"`
			} `json:"story_points_rollup"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if doc.Data.StoryPointsRollup == nil {
		t.Fatal("story_points_rollup missing from detail summary")
	}
	if doc.Data.StoryPointsRollup.Points != 10 || doc.Data.StoryPointsRollup.Contributors != 3 {
		t.Fatalf("rollup = %+v, want points 10 across 3 contributors", doc.Data.StoryPointsRollup)
	}
}

// TestStoryPointRollupAbsentFromListCalls pins the performance contract: the
// rollup is a per-item detail load, never computed on list projections.
func TestStoryPointRollupAbsentFromListCalls(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "SP List WS", shortKey("SPLC"))
	CreateTestItem(t, server, wsID, "Plain item A")
	createItemWithParentAndPoints(t, server, wsID, "Story with points", "Story", 0, 5)

	listResp := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/items?workspace_id=%d", wsID), nil)
	defer listResp.Body.Close()
	AssertStatusCode(t, listResp, http.StatusOK)

	raw, err := io.ReadAll(listResp.Body)
	if err != nil {
		t.Fatalf("read list: %v", err)
	}
	if bytes.Contains(raw, []byte("story_points_rollup")) {
		t.Errorf("list response contains a story_points_rollup key — the rollup leaked into a list projection")
	}
}

// createItemWithParentAndPoints creates an item of the given type with an
// optional story-point value.
func createItemWithParentAndPoints(t *testing.T, server *TestServer, workspaceID int, title, itemTypeName string, parentID, points int) int {
	t.Helper()
	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)
	itemTypeID := RequireItemTypeID(t, itemTypes, itemTypeName)

	body := map[string]interface{}{
		"title":        title,
		"workspace_id": workspaceID,
		"item_type_id": itemTypeID,
	}
	if parentID != 0 {
		body["parent_id"] = parentID
	}
	if points != 0 {
		body["story_points"] = points
	}
	resp := MakeAuthRequest(t, server, http.MethodPost, "/items", body)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)

	var result map[string]interface{}
	DecodeJSON(t, resp, &result)
	return ExtractIDFromResponse(t, result)
}
