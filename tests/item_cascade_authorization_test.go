package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestItemCascadeDeletionCrossWorkspaceAuthorization(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	rootWorkspace, _ := CreateTestWorkspace(t, server, "Cascade root", shortKey("CROOT"))
	childWorkspace, _ := CreateTestWorkspace(t, server, "Cascade child", shortKey("CCHILD"))
	LockDownWorkspace(t, server, rootWorkspace)
	LockDownWorkspace(t, server, childWorkspace)
	actorID, username, password := CreateTestUserWithCredentials(t, server, "cascade_actor", "cascade_actor@example.test")
	AssignWorkspaceRole(t, server, actorID, rootWorkspace, "Administrator")
	cookie := CreateBearerTokenForUser(t, server, username, password)
	token := createTokenWithScopesAsUser(t, server, username, password, []string{"items:read", "items:delete"})

	for _, access := range []string{"hidden", "viewer", "administrator"} {
		if access == "viewer" {
			AssignWorkspaceRole(t, server, actorID, childWorkspace, "Viewer")
		}
		if access == "administrator" {
			AssignWorkspaceRole(t, server, actorID, childWorkspace, "Administrator")
		}
		for _, mount := range []string{"/rest/api/v2", "/api/v2"} {
			t.Run(access+mount, func(t *testing.T) {
				parentID, childID := createItemTreeForDeletion(t, server, rootWorkspace, "Cross workspace")
				created := MakeAuthRequest(t, server, http.MethodPost, "/items", map[string]any{"workspace_id": childWorkspace, "parent_id": childID, "title": "Foreign grandchild"})
				AssertStatusCode(t, created, http.StatusCreated)
				var grandchild map[string]any
				DecodeJSON(t, created, &grandchild)
				created.Body.Close()
				grandchildID := ExtractIDFromResponse(t, grandchild)
				request := func(method, path string) *http.Response {
					if mount == "/api/v2" {
						return MakeAuthRequestWithToken(t, server, cookie, method, strings.TrimPrefix(path, "/api"), nil)
					}
					return MakeBearerRequestWithToken(t, server, token, method, path, nil)
				}
				path, method := fmt.Sprintf("%s/items/%d?cascade=true", mount, parentID), http.MethodDelete
				response := request(method, path)
				defer response.Body.Close()
				if access != "administrator" {
					AssertStatusCode(t, response, http.StatusForbidden)
					var body struct {
						Error struct{ Code, Message string }
					}
					DecodeJSON(t, response, &body)
					if body.Error.Code != "forbidden" || body.Error.Message != "This item can be deleted on its own, but cascade deletion is not permitted." {
						t.Fatalf("denial=%+v", body)
					}
					for _, id := range []int{parentID, childID, grandchildID} {
						get := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d", id), nil)
						AssertStatusCode(t, get, http.StatusOK)
						get.Body.Close()
						var count int
						if err := server.server.DB().QueryRow("SELECT COUNT(*) FROM domain_events WHERE aggregate_type = 'item' AND aggregate_id = ? AND event_type = 'item.deleted'", id).Scan(&count); err != nil {
							t.Fatal(err)
						}
						if count != 0 {
							t.Fatalf("denied item %d emitted %d deletion events", id, count)
						}
					}
					single := request(http.MethodDelete, fmt.Sprintf("%s/items/%d", mount, parentID))
					AssertStatusCode(t, single, http.StatusNoContent)
					single.Body.Close()
					for _, expected := range []struct {
						id     int
						parent any
					}{{childID, nil}, {grandchildID, float64(childID)}} {
						get := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d", expected.id), nil)
						AssertStatusCode(t, get, http.StatusOK)
						var preserved map[string]any
						DecodeJSON(t, get, &preserved)
						get.Body.Close()
						if preserved["parent_id"] != expected.parent {
							t.Fatalf("preserved item %d parent=%v, want %v", expected.id, preserved["parent_id"], expected.parent)
						}
					}
					return
				}
				AssertStatusCode(t, response, http.StatusNoContent)
				for _, id := range []int{parentID, childID, grandchildID} {
					get := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d", id), nil)
					AssertStatusCode(t, get, http.StatusNotFound)
					get.Body.Close()
				}
			})
		}
	}
}

func TestItemDeleteCascadeParameterContract(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Delete modes", shortKey("DMODE"))
	for _, query := range []string{"", "?cascade=false", "?cascade=true", "?cascade=invalid", "?cascade=", "?cascade=true&cascade=false"} {
		t.Run(query, func(t *testing.T) {
			parentID, childID := createItemTreeForDeletion(t, server, workspaceID, "Delete mode")
			response := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/v2/items/%d%s", parentID, query), nil)
			defer response.Body.Close()
			invalid := query == "?cascade=invalid" || query == "?cascade=" || query == "?cascade=true&cascade=false"
			if invalid {
				AssertStatusCode(t, response, http.StatusBadRequest)
			} else {
				AssertStatusCode(t, response, http.StatusNoContent)
			}
			get := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d", childID), nil)
			defer get.Body.Close()
			if query == "?cascade=true" {
				AssertStatusCode(t, get, http.StatusNotFound)
				return
			}
			AssertStatusCode(t, get, http.StatusOK)
			var child map[string]any
			DecodeJSON(t, get, &child)
			if !invalid && child["parent_id"] != nil {
				t.Fatalf("preserved child parent=%v, want null", child["parent_id"])
			}
			if invalid && child["parent_id"] != float64(parentID) {
				t.Fatalf("invalid request changed parent=%v", child["parent_id"])
			}
		})
	}
}
