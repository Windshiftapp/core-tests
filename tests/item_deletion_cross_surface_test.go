package tests

import (
	"fmt"
	"net/http"
	"testing"
)

func createItemTreeForDeletion(t *testing.T, server *TestServer, workspaceID int, prefix string) (parentID, childID int) {
	t.Helper()
	parentResponse := MakeAuthRequest(t, server, http.MethodPost, "/items", map[string]interface{}{
		"workspace_id": workspaceID,
		"title":        prefix + " parent",
	})
	defer parentResponse.Body.Close()
	AssertStatusCode(t, parentResponse, http.StatusCreated)
	var parent map[string]interface{}
	DecodeJSON(t, parentResponse, &parent)
	parentID = ExtractIDFromResponse(t, parent)

	childResponse := MakeAuthRequest(t, server, http.MethodPost, "/items", map[string]interface{}{
		"workspace_id": workspaceID,
		"parent_id":    parentID,
		"title":        prefix + " child",
	})
	defer childResponse.Body.Close()
	AssertStatusCode(t, childResponse, http.StatusCreated)
	var child map[string]interface{}
	DecodeJSON(t, childResponse, &child)
	childID = ExtractIDFromResponse(t, child)
	return parentID, childID
}

func TestItemDeletion_CookieCascadeAndRESTV1Contract(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Item Deletion Contract", shortKey("IDC"))

	for _, surface := range []string{"cookie", "v1"} {
		t.Run(surface, func(t *testing.T) {
			parentID, childID := createItemTreeForDeletion(t, server, workspaceID, surface)

			var response *http.Response
			if surface == "cookie" {
				response = MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/items/%d/cascade", parentID), nil)
			} else {
				response = MakeBearerRequest(t, server, http.MethodDelete, fmt.Sprintf("/rest/api/v1/items/%d", parentID), nil)
			}
			defer response.Body.Close()

			if surface == "cookie" {
				AssertStatusCode(t, response, http.StatusOK)
				var result map[string]interface{}
				DecodeJSON(t, response, &result)
				if result["deletedCount"] != float64(2) {
					t.Fatalf("cookie deletedCount = %v, want 2", result["deletedCount"])
				}
			} else {
				AssertStatusCode(t, response, http.StatusNoContent)
			}

			for _, itemID := range []int{parentID, childID} {
				getResponse := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d", itemID), nil)
				AssertStatusCode(t, getResponse, http.StatusNotFound)
				getResponse.Body.Close()
			}

			var auditCount int
			if err := server.server.DB().QueryRow(`
				SELECT COUNT(*) FROM audit_logs
				WHERE action_type = 'item.delete_cascade'
				  AND resource_type = 'item'
				  AND resource_id = ?
				  AND success = true
			`, parentID).Scan(&auditCount); err != nil {
				t.Fatalf("%s load deletion audit: %v", surface, err)
			}
			if auditCount != 1 {
				t.Fatalf("%s cascade deletion audit rows = %d, want 1", surface, auditCount)
			}
		})
	}
}
