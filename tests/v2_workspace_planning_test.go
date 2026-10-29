package tests

import (
	"fmt"
	"net/http"
	"testing"
)

func TestV2WorkspacePlanningItemScopes(t *testing.T) {
	s, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, s)
	uid, username, password := CreateTestUserWithCredentials(t, s, "planning-editor", "planning-editor@example.com")
	wid, _ := CreateTestWorkspace(t, s, "Planning scope", "PSC")
	other, _ := CreateTestWorkspace(t, s, "Other planning scope", "PSO")
	AssignWorkspaceRole(t, s, uid, wid, "Editor")
	AssignWorkspaceRole(t, s, uid, other, "Editor")
	token := createTokenWithScopesAsUser(t, s, username, password, []string{"items:read", "items:write", "items:delete"})
	readOnly := createTokenWithScopesAsUser(t, s, username, password, []string{"items:read"})
	planningToken := createTokenWithScopesAsUser(t, s, username, password, []string{"milestones:read", "milestones:write", "milestones:delete", "iterations:read", "iterations:write", "iterations:delete"})
	viewerID, viewerName, viewerPassword := CreateTestUserWithCredentials(t, s, "planning-viewer", "planning-viewer@example.com")
	AssignWorkspaceRole(t, s, viewerID, wid, "Viewer")
	viewer := createTokenWithScopesAsUser(t, s, viewerName, viewerPassword, []string{"items:read", "items:write", "items:delete"})
	request := func(t *testing.T, token, method, path string, body any, status int) map[string]any {
		t.Helper()
		response := MakeBearerRequestWithToken(t, s, token, method, "/rest/api/v2"+path, body)
		defer response.Body.Close()
		if response.StatusCode != status {
			AssertStatusCode(t, response, status)
			t.FailNow()
		}
		var result map[string]any
		if status != http.StatusNoContent {
			DecodeJSON(t, response, &result)
		}
		return result
	}
	for _, resource := range []string{"milestones", "iterations"} {
		t.Run(resource, func(t *testing.T) {
			body := map[string]any{"name": "Workspace planning", "description": "Scoped"}
			if resource == "iterations" {
				body["start_date"], body["end_date"] = "2026-09-01", "2026-09-14"
			}
			base := fmt.Sprintf("/workspaces/%d/%s", wid, resource)
			created := request(t, token, "POST", base, body, 201)
			id := ExtractIDFromResponse(t, created)
			path := fmt.Sprintf("%s/%d", base, id)
			got := request(t, token, "GET", path, nil, 200)
			AssertJSONField(t, got, "name", "Workspace planning")
			denied := request(t, readOnly, "PATCH", path, map[string]any{"name": "Forbidden"}, 403)
			if err, ok := denied["error"].(map[string]any); !ok || err["code"] != "insufficient_permission" || err["message"] != "Token lacks a required scope" {
				t.Fatalf("scope denial = %#v", denied)
			}
			page := request(t, token, "GET", base, nil, 200)
			if rows, ok := page["items"].([]any); !ok || len(rows) != 1 || int(rows[0].(map[string]any)["id"].(float64)) != id {
				t.Fatalf("workspace page = %#v", page)
			}
			for _, method := range []string{"PATCH", "DELETE"} {
				denied := request(t, viewer, method, path, map[string]any{"name": "Forbidden"}, 404)
				if err, ok := denied["error"].(map[string]any); !ok || err["code"] != "not_found" || err["message"] != "Planning object was not found" {
					t.Fatalf("workspace permission denial = %#v", denied)
				}
			}
			global := request(t, s.BearerToken, "POST", "/"+resource, body, 201)
			globalID := ExtractIDFromResponse(t, global)
			for _, method := range []string{"PATCH", "DELETE"} {
				denied := request(t, planningToken, method, fmt.Sprintf("/%s/%d", resource, globalID), map[string]any{"name": "Forbidden"}, 404)
				if err, ok := denied["error"].(map[string]any); !ok || err["code"] != "not_found" || err["message"] != "Planning object was not found" {
					t.Fatalf("global permission denial = %#v", denied)
				}
			}
			for _, wrong := range []string{fmt.Sprintf("/workspaces/%d/%s/%d", other, resource, id), fmt.Sprintf("%s/%d", base, globalID)} {
				for _, method := range []string{"GET", "PATCH", "DELETE"} {
					denied := request(t, token, method, wrong, map[string]any{"name": "Forbidden"}, 404)
					err, ok := denied["error"].(map[string]any)
					if !ok || err["code"] != "not_found" || err["message"] != "Planning object was not found" {
						t.Fatalf("denial = %#v", denied)
					}
				}
			}
			updated := request(t, token, "PATCH", path, map[string]any{"name": "Updated"}, 200)
			AssertJSONField(t, updated, "name", "Updated")
			got = request(t, token, "GET", path, nil, 200)
			AssertJSONField(t, got, "name", "Updated")
			global = request(t, s.BearerToken, "GET", fmt.Sprintf("/%s/%d", resource, globalID), nil, 200)
			AssertJSONField(t, global, "name", "Workspace planning")
			permission := "milestone.create"
			if resource == "iterations" {
				permission = "iteration.manage"
			}
			GrantGlobalPermission(t, s, uid, permission)
			global = request(t, planningToken, "PATCH", fmt.Sprintf("/%s/%d", resource, globalID), map[string]any{"name": "Explicitly granted"}, 200)
			AssertJSONField(t, global, "name", "Explicitly granted")
			request(t, token, "DELETE", path, nil, 204)
			request(t, token, "GET", path, nil, 404)
		})
	}
}
