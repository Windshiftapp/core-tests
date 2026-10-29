package tests

import (
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/restapi"
)

func TestWorkspaceRoleCanonicalUpdatePreservesIdentityAndRequiresAdmin(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	adminToken := CreateBearerToken(t, server)

	createResponse := MakeAuthRequestWithToken(t, server, adminToken, http.MethodPost,
		"/workspace-roles", map[string]any{
			"name": "Canonical role", "description": "Original description",
		})
	defer createResponse.Body.Close()
	AssertStatusCode(t, createResponse, http.StatusCreated)
	var created struct {
		ID                 int  `json:"id"`
		IsSystem           bool `json:"is_system"`
		PermissionsEnabled bool `json:"permissions_enabled"`
	}
	DecodeJSON(t, createResponse, &created)

	updatePath := fmt.Sprintf("/workspace-roles/%d", created.ID)
	updateResponse := MakeAuthRequestWithToken(t, server, adminToken, http.MethodPut,
		updatePath, map[string]any{
			"name": "Updated canonical role", "description": "Updated description",
		})
	defer updateResponse.Body.Close()
	AssertStatusCode(t, updateResponse, http.StatusOK)
	var updated struct {
		ID                 int    `json:"id"`
		Name               string `json:"name"`
		Description        string `json:"description"`
		IsSystem           bool   `json:"is_system"`
		PermissionsEnabled bool   `json:"permissions_enabled"`
	}
	DecodeJSON(t, updateResponse, &updated)
	if updated.ID != created.ID || updated.Name != "Updated canonical role" ||
		updated.Description != "Updated description" || updated.IsSystem != created.IsSystem ||
		updated.PermissionsEnabled != created.PermissionsEnabled {
		t.Fatalf("updated workspace role = %+v, created = %+v", updated, created)
	}

	listResponse := MakeAuthRequestWithToken(t, server, adminToken, http.MethodGet,
		"/workspace-roles", nil)
	defer listResponse.Body.Close()
	AssertStatusCode(t, listResponse, http.StatusOK)
	var roles []struct {
		ID         int    `json:"id"`
		BuiltinKey string `json:"builtin_key"`
		Name       string `json:"name"`
	}
	DecodeJSON(t, listResponse, &roles)
	var builtInRole struct {
		ID         int
		BuiltinKey string
		Name       string
	}
	for _, role := range roles {
		if role.BuiltinKey != "" {
			builtInRole.ID = role.ID
			builtInRole.BuiltinKey = role.BuiltinKey
			builtInRole.Name = role.Name
			break
		}
	}
	if builtInRole.ID == 0 {
		t.Fatal("expected a seeded built-in workspace role")
	}
	builtInUpdateResponse := MakeAuthRequestWithToken(t, server, adminToken, http.MethodPut,
		fmt.Sprintf("/workspace-roles/%d", builtInRole.ID), map[string]any{
			"name": builtInRole.Name + " canonical", "description": "Updated built-in fallback",
		})
	defer builtInUpdateResponse.Body.Close()
	AssertStatusCode(t, builtInUpdateResponse, http.StatusOK)
	var updatedBuiltIn struct {
		ID         int    `json:"id"`
		BuiltinKey string `json:"builtin_key"`
		Name       string `json:"name"`
		IsSystem   bool   `json:"is_system"`
	}
	DecodeJSON(t, builtInUpdateResponse, &updatedBuiltIn)
	if updatedBuiltIn.ID != builtInRole.ID || updatedBuiltIn.BuiltinKey != builtInRole.BuiltinKey ||
		updatedBuiltIn.Name != builtInRole.Name+" canonical" || !updatedBuiltIn.IsSystem {
		t.Fatalf("updated built-in workspace role = %+v, original = %+v", updatedBuiltIn, builtInRole)
	}

	_, username, password := CreateTestUserWithCredentials(
		t, server, "role_update_non_admin", "role-update-non-admin@test.com",
	)
	nonAdminToken := CreateBearerTokenForUser(t, server, username, password)
	deniedResponse := MakeAuthRequestWithToken(t, server, nonAdminToken, http.MethodPut,
		updatePath, map[string]any{"name": "Forbidden", "description": ""})
	defer deniedResponse.Body.Close()
	AssertStatusCode(t, deniedResponse, http.StatusForbidden)
	var apiError restapi.ErrorResponse
	DecodeJSON(t, deniedResponse, &apiError)
	if apiError.Code != restapi.ErrCodeInsufficientPermission {
		t.Fatalf("non-admin error = %+v, want code %q", apiError, restapi.ErrCodeInsufficientPermission)
	}
}
