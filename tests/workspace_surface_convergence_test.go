package tests

import (
	"fmt"
	"net/http"
	"testing"
)

func TestWorkspaceLifecycleConvergesAcrossSessionAndBearerSurfaces(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	created := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/workspaces", map[string]any{
		"name": "Workspace convergence", "key": shortKey("WSC"), "description": "created by bearer",
	})
	defer created.Body.Close()
	AssertStatusCode(t, created, http.StatusCreated)
	var workspace map[string]any
	DecodeJSON(t, created, &workspace)
	workspaceID := ExtractIDFromResponse(t, workspace)

	sessionUpdate := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/v2/workspaces/%d", workspaceID), map[string]any{
		"name": "Workspace updated", "description": "updated by session",
	})
	defer sessionUpdate.Body.Close()
	AssertStatusCode(t, sessionUpdate, http.StatusOK)

	bearerRead := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/workspaces/%d", workspaceID), nil)
	defer bearerRead.Body.Close()
	AssertStatusCode(t, bearerRead, http.StatusOK)
	var bearerWorkspace map[string]any
	DecodeJSON(t, bearerRead, &bearerWorkspace)
	AssertJSONField(t, bearerWorkspace, "name", "Workspace updated")
	AssertJSONField(t, bearerWorkspace, "description", "updated by session")
	for _, field := range []string{"time_project_name", "time_project_categories"} {
		if _, present := bearerWorkspace[field]; present {
			t.Fatalf("restricted workspace field %q leaked: %#v", field, bearerWorkspace[field])
		}
	}

	// is_personal and owner_id are undocumented patch fields — schema
	// validation rejects them as malformed (400). key is a documented field
	// since 0.8.9 (personal workspaces only), so a regular-workspace key
	// change is well-formed but semantically rejected (422).
	for _, field := range []string{"is_personal", "owner_id"} {
		patch := MakeBearerRequest(t, server, http.MethodPatch,
			fmt.Sprintf("/rest/api/v2/workspaces/%d", workspaceID), map[string]any{field: "blocked"})
		AssertStatusCode(t, patch, http.StatusBadRequest)
		patch.Body.Close()
	}
	keyPatch := MakeBearerRequest(t, server, http.MethodPatch,
		fmt.Sprintf("/rest/api/v2/workspaces/%d", workspaceID), map[string]any{"key": "BLOCKD"})
	AssertStatusCode(t, keyPatch, http.StatusUnprocessableEntity)
	keyPatch.Body.Close()

	deleted := MakeBearerRequest(t, server, http.MethodDelete,
		fmt.Sprintf("/rest/api/v2/workspaces/%d", workspaceID), nil)
	defer deleted.Body.Close()
	AssertStatusCode(t, deleted, http.StatusNoContent)

	missing := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/workspaces/%d", workspaceID), nil)
	defer missing.Body.Close()
	AssertStatusCode(t, missing, http.StatusNotFound)
}
