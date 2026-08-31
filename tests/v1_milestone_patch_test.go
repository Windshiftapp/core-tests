package tests

import (
	"fmt"
	"net/http"
	"testing"
)

func TestV1MilestoneUpdateDistinguishesOmittedEmptyAndNull(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	workspaceID, _ := CreateTestWorkspace(t, ts, "Milestone patch", "MPT")

	var categoryID int
	if err := ts.DB().QueryRow(
		"INSERT INTO milestone_categories (name, color) VALUES (?, ?) RETURNING id",
		"API patch category", "#123456",
	).Scan(&categoryID); err != nil {
		t.Fatalf("insert milestone category: %v", err)
	}
	createBody := map[string]interface{}{
		"name":        "Original",
		"description": "Keep me",
		"target_date": "2026-07-14",
		"status":      "planning",
		"category_id": categoryID,
	}
	resp := MakeBearerRequest(t, ts, http.MethodPost,
		fmt.Sprintf("/rest/api/v1/workspaces/%d/milestones", workspaceID), createBody)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var created map[string]interface{}
	DecodeJSON(t, resp, &created)
	milestoneID := ExtractIDFromResponse(t, created)
	// The API echoes the stored column ("2026-07-14T00:00:00Z"), not the
	// YYYY-MM-DD it accepts on write, so assert against what create returned.
	storedTargetDate := created["target_date"]

	invalid := MakeBearerRequest(t, ts, http.MethodPut,
		fmt.Sprintf("/rest/api/v1/workspaces/%d/milestones/%d", workspaceID, milestoneID),
		map[string]interface{}{"status": "planned"})
	defer invalid.Body.Close()
	AssertStatusCode(t, invalid, http.StatusBadRequest)
	var invalidBody map[string]interface{}
	DecodeJSON(t, invalid, &invalidBody)
	AssertJSONField(t, invalidBody, "code", "VALIDATION_FAILED")
	details, ok := invalidBody["details"].(map[string]interface{})
	if !ok || details["field"] != "status" {
		t.Fatalf("validation details = %v, want field=status", invalidBody["details"])
	}

	update := func(body map[string]interface{}) map[string]interface{} {
		t.Helper()
		response := MakeBearerRequest(t, ts, http.MethodPut,
			fmt.Sprintf("/rest/api/v1/workspaces/%d/milestones/%d", workspaceID, milestoneID), body)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusOK)
		var out map[string]interface{}
		DecodeJSON(t, response, &out)
		return out
	}

	// A status-only edit is the CLI's `ws milestone update <id> --status ...`:
	// everything it did not send has to survive.
	transitioned := update(map[string]interface{}{"status": "in-progress"})
	AssertJSONField(t, transitioned, "name", "Original")
	AssertJSONField(t, transitioned, "description", "Keep me")
	AssertJSONField(t, transitioned, "target_date", storedTargetDate)
	AssertJSONField(t, transitioned, "category_id", float64(categoryID))
	AssertJSONField(t, transitioned, "status", "in-progress")

	renamed := update(map[string]interface{}{"name": "Renamed"})
	AssertJSONField(t, renamed, "description", "Keep me")
	AssertJSONField(t, renamed, "status", "in-progress")
	AssertJSONField(t, renamed, "target_date", storedTargetDate)

	clearedDescription := update(map[string]interface{}{"description": ""})
	if value, ok := clearedDescription["description"]; ok && value != "" {
		t.Fatalf("description = %v, want empty/omitted response field", value)
	}
	AssertJSONField(t, clearedDescription, "name", "Renamed")
	AssertJSONField(t, clearedDescription, "category_id", float64(categoryID))

	clearedTargetDate := update(map[string]interface{}{"target_date": nil})
	if value, ok := clearedTargetDate["target_date"]; ok && value != "" && value != nil {
		t.Fatalf("target_date = %v, want null/omitted", value)
	}
	AssertJSONField(t, clearedTargetDate, "name", "Renamed")

	clearedCategory := update(map[string]interface{}{"category_id": nil})
	if value, ok := clearedCategory["category_id"]; ok && value != nil {
		t.Fatalf("category_id = %v, want null/omitted", value)
	}
	AssertJSONField(t, clearedCategory, "name", "Renamed")
	AssertJSONField(t, clearedCategory, "status", "in-progress")
}

// The global /rest/api/v1/milestones/{id} route shares the merge helper with
// the workspace-scoped route; this pins the same patch semantics there.
func TestV1GlobalMilestoneUpdateKeepsOmittedFields(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)

	resp := MakeBearerRequest(t, ts, http.MethodPost, "/rest/api/v1/milestones", map[string]interface{}{
		"name":        "Global original",
		"description": "Keep me",
		"target_date": "2026-08-01",
		"status":      "planning",
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var created map[string]interface{}
	DecodeJSON(t, resp, &created)
	milestoneID := ExtractIDFromResponse(t, created)

	updated := MakeBearerRequest(t, ts, http.MethodPut,
		fmt.Sprintf("/rest/api/v1/milestones/%d", milestoneID),
		map[string]interface{}{"status": "completed"})
	defer updated.Body.Close()
	AssertStatusCode(t, updated, http.StatusOK)
	var out map[string]interface{}
	DecodeJSON(t, updated, &out)
	AssertJSONField(t, out, "name", "Global original")
	AssertJSONField(t, out, "description", "Keep me")
	AssertJSONField(t, out, "target_date", created["target_date"])
	AssertJSONField(t, out, "status", "completed")
	AssertJSONField(t, out, "is_global", true)
}
