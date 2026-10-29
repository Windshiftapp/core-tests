package tests

import (
	"fmt"
	"net/http"
	"testing"
)

func TestGroupMutationsAreSessionOnly(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	createdResponse := MakeAuthRequest(t, server, http.MethodPost, "/v2/admin/groups", map[string]any{
		"name": "Shared group", "description": "session value",
	})
	defer createdResponse.Body.Close()
	AssertStatusCode(t, createdResponse, http.StatusCreated)
	var created map[string]any
	DecodeJSON(t, createdResponse, &created)
	groupID := ExtractIDFromResponse(t, created)

	duplicateResponse := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/admin/groups", map[string]any{
		"name": "Shared group", "description": "duplicate",
	})
	defer duplicateResponse.Body.Close()
	AssertStatusCode(t, duplicateResponse, http.StatusNotFound)

	updateResponse := MakeAuthRequest(t, server, http.MethodPatch,
		fmt.Sprintf("/v2/admin/groups/%d", groupID), map[string]any{"description": "session update"})
	defer updateResponse.Body.Close()
	AssertStatusCode(t, updateResponse, http.StatusOK)

	getResponse := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/admin/groups/%d", groupID), nil)
	defer getResponse.Body.Close()
	AssertStatusCode(t, getResponse, http.StatusOK)
	var group map[string]any
	DecodeJSON(t, getResponse, &group)
	if group["name"] != "Shared group" || group["description"] != "session update" {
		t.Fatalf("session read after update = %#v", group)
	}

	bearerDelete := MakeBearerRequest(t, server, http.MethodDelete,
		fmt.Sprintf("/rest/api/v2/admin/groups/%d", groupID), nil)
	defer bearerDelete.Body.Close()
	AssertStatusCode(t, bearerDelete, http.StatusNotFound)

	deleteResponse := MakeAuthRequest(t, server, http.MethodDelete,
		fmt.Sprintf("/v2/admin/groups/%d", groupID), nil)
	defer deleteResponse.Body.Close()
	AssertStatusCode(t, deleteResponse, http.StatusNoContent)

	missingResponse := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/admin/groups/%d", groupID), nil)
	defer missingResponse.Body.Close()
	AssertStatusCode(t, missingResponse, http.StatusNotFound)
}

func TestSessionGroupDeletePreservesManagedGroupProtection(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	var id int
	err := server.server.DB().QueryRow(`
		INSERT INTO groups (name, description, scim_managed, is_active)
		VALUES ('Managed group', '', TRUE, TRUE)
		RETURNING id
	`).Scan(&id)
	if err != nil {
		t.Fatalf("insert managed group: %v", err)
	}

	response := MakeAuthRequest(t, server, http.MethodDelete,
		fmt.Sprintf("/v2/admin/groups/%d", id), nil)
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusForbidden)
}
