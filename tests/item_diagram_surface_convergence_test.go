package tests

import (
	"fmt"
	"net/http"
	"testing"
)

type surfaceItemDiagram struct {
	ID          int    `json:"id"`
	ItemID      int    `json:"item_id"`
	Name        string `json:"name"`
	DiagramData string `json:"diagram_data"`
}

func TestItemDiagramMutationsConvergeAcrossSessionAndBearerSurfaces(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Diagram surface convergence", shortKey("DSC"))
	itemID := CreateTestItem(t, server, workspaceID, "Cross-surface diagram")

	createResponse := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/v2/items/%d/diagrams", itemID), map[string]any{
			"name": "  Shared flow  ", "diagram_data": `{"nodes":[]}`,
		})
	defer createResponse.Body.Close()
	AssertStatusCode(t, createResponse, http.StatusCreated)
	var diagram surfaceItemDiagram
	DecodeJSON(t, createResponse, &diagram)
	if diagram.ID == 0 || diagram.ItemID != itemID || diagram.Name != "Shared flow" || diagram.DiagramData != `{"nodes":[]}` {
		t.Fatalf("session-created diagram = %+v", diagram)
	}

	bearerGetResponse := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/item-diagrams/%d", diagram.ID), nil)
	defer bearerGetResponse.Body.Close()
	AssertStatusCode(t, bearerGetResponse, http.StatusOK)
	var fetched surfaceItemDiagram
	DecodeJSON(t, bearerGetResponse, &fetched)
	if fetched != diagram {
		t.Fatalf("bearer read after session create = %+v, want %+v", fetched, diagram)
	}

	bearerUpdateResponse := MakeBearerRequest(t, server, http.MethodPatch,
		fmt.Sprintf("/rest/api/v2/item-diagrams/%d", diagram.ID), map[string]any{
			"name": "  Bearer flow  ", "diagram_data": `{"nodes":[{"id":"a"}]}`,
		})
	defer bearerUpdateResponse.Body.Close()
	AssertStatusCode(t, bearerUpdateResponse, http.StatusOK)
	DecodeJSON(t, bearerUpdateResponse, &diagram)
	if diagram.Name != "Bearer flow" || diagram.DiagramData != `{"nodes":[{"id":"a"}]}` {
		t.Fatalf("bearer-updated diagram = %+v", diagram)
	}

	sessionGetResponse := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/v2/item-diagrams/%d", diagram.ID), nil)
	defer sessionGetResponse.Body.Close()
	AssertStatusCode(t, sessionGetResponse, http.StatusOK)
	DecodeJSON(t, sessionGetResponse, &fetched)
	if fetched.ID != diagram.ID || fetched.ItemID != itemID || fetched.Name != diagram.Name || fetched.DiagramData != diagram.DiagramData {
		t.Fatalf("session read after bearer update = %+v, want %+v", fetched, diagram)
	}

	bearerDeleteResponse := MakeBearerRequest(t, server, http.MethodDelete,
		fmt.Sprintf("/rest/api/v2/item-diagrams/%d", diagram.ID), nil)
	defer bearerDeleteResponse.Body.Close()
	AssertStatusCode(t, bearerDeleteResponse, http.StatusNoContent)

	deletedResponse := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/v2/item-diagrams/%d", diagram.ID), nil)
	defer deletedResponse.Body.Close()
	AssertStatusCode(t, deletedResponse, http.StatusNotFound)
}
