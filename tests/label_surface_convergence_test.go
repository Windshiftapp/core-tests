package tests

import (
	"fmt"
	"net/http"
	"testing"
)

type surfaceLabel struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

func TestLabelMutationsConvergeAcrossSessionAndBearerSurfaces(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	adminCookie := CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Label surface convergence", "LSC")

	createResponse := MakeAuthRequestWithToken(t, server, adminCookie, http.MethodPost, fmt.Sprintf("/v2/workspaces/%d/labels", workspaceID), map[string]any{
		"name": "  Shared label  ", "color": "#112233",
	})
	defer createResponse.Body.Close()
	AssertStatusCode(t, createResponse, http.StatusCreated)
	var label surfaceLabel
	DecodeJSON(t, createResponse, &label)
	if label.ID == 0 || label.Name != "Shared label" || label.Color != "#112233" {
		t.Fatalf("session-created label = %+v", label)
	}

	listResponse := MakeBearerRequestWithToken(t, server, server.BearerToken, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/workspaces/%d/labels", workspaceID), nil)
	defer listResponse.Body.Close()
	AssertStatusCode(t, listResponse, http.StatusOK)
	var labels []surfaceLabel
	DecodeJSON(t, listResponse, &labels)
	assertLabelSurfaceValue(t, labels, label.ID, "Shared label", "#112233")

	v1UpdateResponse := MakeBearerRequestWithToken(t, server, server.BearerToken, http.MethodPatch,
		fmt.Sprintf("/rest/api/v2/workspaces/%d/labels/%d", workspaceID, label.ID),
		map[string]any{"name": "Bearer updated", "color": ""})
	defer v1UpdateResponse.Body.Close()
	AssertStatusCode(t, v1UpdateResponse, http.StatusOK)

	sessionGetResponse := MakeAuthRequestWithToken(t, server, adminCookie, http.MethodGet,
		fmt.Sprintf("/v2/workspaces/%d/labels/%d", workspaceID, label.ID), nil)
	defer sessionGetResponse.Body.Close()
	AssertStatusCode(t, sessionGetResponse, http.StatusOK)
	DecodeJSON(t, sessionGetResponse, &label)
	if label.Name != "Bearer updated" || label.Color != "#3B82F6" {
		t.Fatalf("session read after bearer update = %+v", label)
	}

	itemID := CreateTestItem(t, server, workspaceID, "Cross-surface labels")
	sessionAddResponse := MakeAuthRequestWithToken(t, server, adminCookie, http.MethodPost,
		fmt.Sprintf("/v2/items/%d/labels", itemID), map[string]any{"label_id": label.ID})
	defer sessionAddResponse.Body.Close()
	AssertStatusCode(t, sessionAddResponse, http.StatusOK)

	v1ItemResponse := MakeBearerRequestWithToken(t, server, server.BearerToken, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/items/%d/labels", itemID), nil)
	defer v1ItemResponse.Body.Close()
	AssertStatusCode(t, v1ItemResponse, http.StatusOK)
	labels = nil
	DecodeJSON(t, v1ItemResponse, &labels)
	assertLabelSurfaceValue(t, labels, label.ID, "Bearer updated", "#3B82F6")

	v1SetResponse := MakeBearerRequestWithToken(t, server, server.BearerToken, http.MethodPut,
		fmt.Sprintf("/rest/api/v2/items/%d/labels", itemID), map[string]any{"label_ids": []int{}})
	defer v1SetResponse.Body.Close()
	AssertStatusCode(t, v1SetResponse, http.StatusOK)

	sessionItemResponse := MakeAuthRequestWithToken(t, server, adminCookie, http.MethodGet,
		fmt.Sprintf("/v2/items/%d/labels", itemID), nil)
	defer sessionItemResponse.Body.Close()
	AssertStatusCode(t, sessionItemResponse, http.StatusOK)
	var sessionLabels []map[string]any
	DecodeJSON(t, sessionItemResponse, &sessionLabels)
	if len(sessionLabels) != 0 {
		t.Fatalf("session labels after bearer replacement = %#v, want empty", sessionLabels)
	}
}

func assertLabelSurfaceValue(t *testing.T, labels []surfaceLabel, id int, name, color string) {
	t.Helper()
	for _, label := range labels {
		if label.ID == id {
			if label.Name != name || label.Color != color {
				t.Fatalf("label %d = %+v, want name %q and color %q", id, label, name, color)
			}
			return
		}
	}
	t.Fatalf("label %d missing from %#v", id, labels)
}
