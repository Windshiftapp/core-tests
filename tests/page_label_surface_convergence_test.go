package tests

import (
	"fmt"
	"net/http"
	"testing"
)

type surfacePageLabel struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	WorkspaceID int    `json:"workspace_id"`
}

func TestPageLabelMutationsConvergeAcrossSessionAndBearerSurfaces(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Page-label convergence", shortKey("PLC"))

	create := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/v2/workspaces/%d/page-labels", workspaceID), map[string]any{
			"name": "  Shared page label  ",
		})
	defer create.Body.Close()
	AssertStatusCode(t, create, http.StatusCreated)
	var label surfacePageLabel
	DecodeJSON(t, create, &label)
	if label.ID == 0 || label.Name != "Shared page label" || label.Color != "#3B82F6" || label.WorkspaceID != workspaceID {
		t.Fatalf("session-created page label = %+v", label)
	}

	update := MakeBearerRequest(t, server, http.MethodPatch,
		fmt.Sprintf("/rest/api/v2/workspaces/%d/page-labels/%d", workspaceID, label.ID), map[string]any{
			"name": "Bearer page label",
		})
	defer update.Body.Close()
	AssertStatusCode(t, update, http.StatusOK)
	DecodeJSON(t, update, &label)
	if label.Name != "Bearer page label" || label.Color != "#3B82F6" {
		t.Fatalf("bearer-updated page label = %+v", label)
	}

	get := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/v2/workspaces/%d/page-labels/%d", workspaceID, label.ID), nil)
	defer get.Body.Close()
	AssertStatusCode(t, get, http.StatusOK)
	var sessionLabel surfacePageLabel
	DecodeJSON(t, get, &sessionLabel)
	if sessionLabel != label {
		t.Fatalf("session label = %+v, bearer label = %+v", sessionLabel, label)
	}

	otherWorkspaceID, _ := CreateTestWorkspace(t, server, "Other page-label workspace", shortKey("PLO"))
	denied := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/workspaces/%d/page-labels/%d", otherWorkspaceID, label.ID), nil)
	defer denied.Body.Close()
	AssertStatusCode(t, denied, http.StatusNotFound)

	remove := MakeBearerRequest(t, server, http.MethodDelete,
		fmt.Sprintf("/rest/api/v2/workspaces/%d/page-labels/%d", workspaceID, label.ID), nil)
	defer remove.Body.Close()
	AssertStatusCode(t, remove, http.StatusNoContent)

	deleted := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/v2/workspaces/%d/page-labels/%d", workspaceID, label.ID), nil)
	defer deleted.Body.Close()
	AssertStatusCode(t, deleted, http.StatusNotFound)
}
