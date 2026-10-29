package tests

import (
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/logger"
)

func TestAdminUserManagementIsSessionOnly(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	userID, username, _ := CreateTestUserWithCredentials(t, server, shortKey("auc"), shortKey("auc")+"@example.test")

	deactivated := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/users/%d/deactivate", userID), nil)
	defer deactivated.Body.Close()
	AssertStatusCode(t, deactivated, http.StatusOK)

	adminList := MakeAuthRequest(t, server, http.MethodGet, "/v2/admin/users?page_size=100", nil)
	defer adminList.Body.Close()
	AssertStatusCode(t, adminList, http.StatusOK)
	var page struct {
		Data []map[string]any `json:"data"`
	}
	DecodeJSON(t, adminList, &page)
	foundInactive := false
	for _, user := range page.Data {
		if int(user["id"].(float64)) == userID {
			foundInactive = user["is_active"] == false
		}
	}
	if !foundInactive {
		t.Fatalf("session admin list did not include inactive user %d", userID)
	}

	sessionUpdate := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/v2/admin/users/%d", userID), map[string]any{
		"email":      shortKey("updated") + "@example.test",
		"username":   username,
		"first_name": "Session",
		"last_name":  "Updated",
		"timezone":   "Europe/Zurich",
		"language":   "en",
	})
	defer sessionUpdate.Body.Close()
	AssertStatusCode(t, sessionUpdate, http.StatusOK)

	// WI-1889: the v2 admin user PATCH must emit a user.update audit row with
	// the changed fields, matching the legacy surface.
	var updateCount int
	if err := server.server.DB().QueryRow(
		`SELECT COUNT(*) FROM audit_logs WHERE action_type = ? AND resource_id = ?`,
		logger.ActionUserUpdate, userID,
	).Scan(&updateCount); err != nil {
		t.Fatalf("query admin user update audit: %v", err)
	}
	if updateCount != 1 {
		t.Fatalf("admin user update audit rows = %d, want 1", updateCount)
	}

	bearerUpdate := MakeBearerRequest(t, server, http.MethodPatch,
		fmt.Sprintf("/rest/api/v2/admin/users/%d", userID), map[string]any{
			"first_name": "Bearer",
			"last_name":  "Updated",
		})
	defer bearerUpdate.Body.Close()
	AssertStatusCode(t, bearerUpdate, http.StatusNotFound)

	bearerList := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/admin/users?page_size=100", nil)
	defer bearerList.Body.Close()
	AssertStatusCode(t, bearerList, http.StatusNotFound)

	sessionRead := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/admin/users/%d", userID), nil)
	defer sessionRead.Body.Close()
	AssertStatusCode(t, sessionRead, http.StatusOK)
	var user map[string]any
	DecodeJSON(t, sessionRead, &user)
	AssertJSONField(t, user, "first_name", "Session")
	AssertJSONField(t, user, "last_name", "Updated")
	AssertJSONField(t, user, "is_active", false)
}
