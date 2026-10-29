package tests

import (
	"fmt"
	"net/http"
	"testing"
)

func TestActionDefinitionLifecycleConvergesAcrossSessionAndBearerSurfaces(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Action convergence", shortKey("ASC"))

	created := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/v2/workspaces/%d/actions", workspaceID), map[string]any{
			"name": "Shared action", "description": "created by session",
			"trigger_type": "status_transition", "nodes": []any{}, "edges": []any{},
		})
	defer created.Body.Close()
	AssertStatusCode(t, created, http.StatusCreated)
	var action map[string]any
	DecodeJSON(t, created, &action)
	actionID := ExtractIDFromResponse(t, action)

	bearerUpdate := MakeBearerRequest(t, server, http.MethodPatch,
		fmt.Sprintf("/rest/api/v2/workspaces/%d/actions/%d", workspaceID, actionID), map[string]any{
			"name": "Updated action", "description": "updated by bearer",
			"trigger_type": "status_transition", "nodes": []any{}, "edges": []any{},
		})
	defer bearerUpdate.Body.Close()
	AssertStatusCode(t, bearerUpdate, http.StatusOK)

	sessionRead := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/v2/workspaces/%d/actions/%d", workspaceID, actionID), nil)
	defer sessionRead.Body.Close()
	AssertStatusCode(t, sessionRead, http.StatusOK)
	var updated map[string]any
	DecodeJSON(t, sessionRead, &updated)
	AssertJSONField(t, updated, "name", "Updated action")
	AssertJSONField(t, updated, "description", "updated by bearer")

	deleted := MakeBearerRequest(t, server, http.MethodDelete,
		fmt.Sprintf("/rest/api/v2/workspaces/%d/actions/%d", workspaceID, actionID), nil)
	defer deleted.Body.Close()
	AssertStatusCode(t, deleted, http.StatusNoContent)

	missing := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/v2/workspaces/%d/actions/%d", workspaceID, actionID), nil)
	defer missing.Body.Close()
	AssertStatusCode(t, missing, http.StatusNotFound)
}
