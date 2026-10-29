package tests

import (
	"fmt"
	"net/http"
	"testing"
)

type surfaceItemTemplate struct {
	ID              int    `json:"id"`
	WorkspaceID     int    `json:"workspace_id"`
	Name            string `json:"name"`
	DescriptionBody string `json:"description_body"`
	Mode            string `json:"mode"`
	IsActive        bool   `json:"is_active"`
	ItemTypeIDs     []int  `json:"item_type_ids"`
}

func TestItemTemplateMutationsConvergeAcrossSessionAndBearerSurfaces(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Template surface convergence", shortKey("TSC"))
	itemTypeID := RequireItemTypeID(t, GetItemTypes(t, server, GetDefaultConfigurationSet(t, server)), "Task")

	create := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/v2/workspaces/%d/item-templates", workspaceID), map[string]any{
			"name":             "  Shared template  ",
			"description_body": "Session body", "item_type_ids": []int{itemTypeID},
		})
	defer create.Body.Close()
	AssertStatusCode(t, create, http.StatusCreated)
	var template surfaceItemTemplate
	DecodeJSON(t, create, &template)
	if template.ID == 0 || template.Name != "Shared template" || template.Mode != "selectable" || !template.IsActive {
		t.Fatalf("session-created template = %+v", template)
	}

	bearerUpdate := MakeBearerRequest(t, server, http.MethodPatch,
		fmt.Sprintf("/rest/api/v2/workspaces/%d/item-templates/%d", workspaceID, template.ID), map[string]any{
			"name": "Bearer template", "description_body": "Bearer body",
		})
	defer bearerUpdate.Body.Close()
	AssertStatusCode(t, bearerUpdate, http.StatusOK)
	DecodeJSON(t, bearerUpdate, &template)
	if template.Name != "Bearer template" || template.DescriptionBody != "Bearer body" || len(template.ItemTypeIDs) != 1 || template.ItemTypeIDs[0] != itemTypeID {
		t.Fatalf("bearer-updated template = %+v", template)
	}

	sessionList := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/v2/workspaces/%d/item-templates", workspaceID), nil)
	defer sessionList.Body.Close()
	AssertStatusCode(t, sessionList, http.StatusOK)
	var templates []surfaceItemTemplate
	DecodeJSON(t, sessionList, &templates)
	if len(templates) != 1 || templates[0].ID != template.ID || templates[0].Name != "Bearer template" {
		t.Fatalf("session list after bearer update = %+v", templates)
	}

	bearerDelete := MakeBearerRequest(t, server, http.MethodDelete,
		fmt.Sprintf("/rest/api/v2/workspaces/%d/item-templates/%d", workspaceID, template.ID), nil)
	defer bearerDelete.Body.Close()
	AssertStatusCode(t, bearerDelete, http.StatusNoContent)

	deleted := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/workspaces/%d/item-templates/%d", workspaceID, template.ID), nil)
	defer deleted.Body.Close()
	AssertStatusCode(t, deleted, http.StatusNotFound)
}
