package tests

import (
	"fmt"
	"net/http"
	"testing"
)

type surfaceCollection struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	WorkspaceID *int   `json:"workspace_id"`
}

func TestCollectionMetadataConvergesAcrossSessionAndBearerSurfaces(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Collection convergence", shortKey("CLC"))

	create := MakeAuthRequest(t, server, http.MethodPost, "/v2/collections", map[string]any{
		"name": "Shared collection", "description": "Session collection", "workspace_id": workspaceID,
	})
	defer create.Body.Close()
	AssertStatusCode(t, create, http.StatusCreated)
	var sessionDocument struct {
		Data surfaceCollection `json:"data"`
	}
	DecodeJSON(t, create, &sessionDocument)
	session := sessionDocument.Data

	get := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/collections/%d", session.ID), nil)
	defer get.Body.Close()
	AssertStatusCode(t, get, http.StatusOK)
	var bearerDocument struct {
		Data surfaceCollection `json:"data"`
	}
	DecodeJSON(t, get, &bearerDocument)
	bearer := bearerDocument.Data
	if bearer.ID != session.ID || bearer.Name != session.Name || bearer.Description != session.Description ||
		bearer.WorkspaceID == nil || *bearer.WorkspaceID != workspaceID {
		t.Fatalf("bearer collection = %+v, session collection = %+v", bearer, session)
	}

	update := makeRequest(t, http.MethodPatch,
		fmt.Sprintf("%s/rest/api/v2/collections/%d", server.BaseURL, session.ID), server.BearerToken,
		map[string]any{"description": "Bearer collection"}, map[string]string{"Content-Type": "application/merge-patch+json"})
	defer update.Body.Close()
	AssertStatusCode(t, update, http.StatusOK)

	list := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/v2/collections?workspace_id=%d", workspaceID), nil)
	defer list.Body.Close()
	AssertStatusCode(t, list, http.StatusOK)
	var result struct {
		Data       []surfaceCollection `json:"data"`
		Pagination struct {
			TotalItems int `json:"total_items"`
		} `json:"pagination"`
	}
	DecodeJSON(t, list, &result)
	if result.Pagination.TotalItems != 1 || len(result.Data) != 1 || result.Data[0].ID != session.ID || result.Data[0].Description != "Bearer collection" {
		t.Fatalf("bearer collection search = %+v", result)
	}
}
