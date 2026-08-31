package tests

import (
	"fmt"
	"net/http"
	"testing"
)

func TestV1IterationUpdateDistinguishesOmittedEmptyAndNull(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	workspaceID, _ := CreateTestWorkspace(t, ts, "Iteration patch", "IPT")

	var typeID int
	if err := ts.DB().QueryRow(
		"INSERT INTO iteration_types (name, color) VALUES (?, ?) RETURNING id",
		"API patch type", "#123456",
	).Scan(&typeID); err != nil {
		t.Fatalf("insert iteration type: %v", err)
	}
	createBody := map[string]interface{}{
		"name":        "Original",
		"description": "Keep me",
		"start_date":  "2026-07-01",
		"end_date":    "2026-07-14",
		"status":      "planned",
		"type_id":     typeID,
	}
	resp := MakeBearerRequest(t, ts, http.MethodPost,
		fmt.Sprintf("/rest/api/v1/workspaces/%d/iterations", workspaceID), createBody)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var created map[string]interface{}
	DecodeJSON(t, resp, &created)
	iterationID := ExtractIDFromResponse(t, created)

	invalid := MakeBearerRequest(t, ts, http.MethodPut,
		fmt.Sprintf("/rest/api/v1/workspaces/%d/iterations/%d", workspaceID, iterationID),
		map[string]interface{}{"status": "planning"})
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
			fmt.Sprintf("/rest/api/v1/workspaces/%d/iterations/%d", workspaceID, iterationID), body)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusOK)
		var out map[string]interface{}
		DecodeJSON(t, response, &out)
		return out
	}

	renamed := update(map[string]interface{}{"name": "Renamed"})
	AssertJSONField(t, renamed, "description", "Keep me")
	AssertJSONField(t, renamed, "type_id", float64(typeID))
	AssertJSONField(t, renamed, "status", "planned")

	clearedDescription := update(map[string]interface{}{"description": ""})
	if value, ok := clearedDescription["description"]; ok && value != "" {
		t.Fatalf("description = %v, want empty/omitted response field", value)
	}
	AssertJSONField(t, clearedDescription, "type_id", float64(typeID))

	clearedType := update(map[string]interface{}{"type_id": nil})
	if value, ok := clearedType["type_id"]; ok && value != nil {
		t.Fatalf("type_id = %v, want null/omitted", value)
	}
	AssertJSONField(t, clearedType, "name", "Renamed")
}
