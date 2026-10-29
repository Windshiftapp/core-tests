package tests

import (
	"fmt"
	"net/http"
	"testing"
)

func TestCommentAuthorCannotMutateAfterWorkspaceAccessIsRevoked(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Revoked comment access", shortKey("RCA"))
	LockDownWorkspace(t, server, workspaceID)
	itemID := CreateTestItem(t, server, workspaceID, "Revoked comment target")
	authorID, username, password := CreateTestUserWithCredentials(t, server, "revoked_comment_author", "revoked_comment_author@test.com")
	AssignWorkspaceRole(t, server, authorID, workspaceID, "Editor")
	token := createTokenWithScopesAsUser(t, server, username, password, []string{"items:read", "items:write", "items:delete"})

	create := MakeBearerRequestWithToken(t, server, token, http.MethodPost,
		fmt.Sprintf("/rest/api/v1/items/%d/comments", itemID), map[string]any{"content": "must remain unchanged"})
	defer create.Body.Close()
	AssertStatusCode(t, create, http.StatusCreated)
	var comment map[string]any
	DecodeJSON(t, create, &comment)
	commentID := ExtractIDFromResponse(t, comment)

	RevokeWorkspaceRole(t, server, authorID, workspaceID, GetWorkspaceRoles(t, server)["Editor"])

	mutations := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"v1 update", http.MethodPut, fmt.Sprintf("/rest/api/v1/comments/%d", commentID), map[string]any{"content": "v1 unauthorized update"}},
		{"v1 delete", http.MethodDelete, fmt.Sprintf("/rest/api/v1/comments/%d", commentID), nil},
		{"v2 update", http.MethodPatch, fmt.Sprintf("/rest/api/v2/comments/%d", commentID), map[string]any{"content": "v2 unauthorized update"}},
		{"v2 delete", http.MethodDelete, fmt.Sprintf("/rest/api/v2/comments/%d", commentID), nil},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			response := MakeBearerRequestWithToken(t, server, token, mutation.method, mutation.path, mutation.body)
			defer response.Body.Close()
			AssertStatusCode(t, response, http.StatusNotFound)
		})
	}

	persisted := MakeBearerRequestWithToken(t, server, server.BearerToken, http.MethodGet,
		fmt.Sprintf("/rest/api/v1/comments/%d", commentID), nil)
	defer persisted.Body.Close()
	AssertStatusCode(t, persisted, http.StatusOK)
	DecodeJSON(t, persisted, &comment)
	if comment["content"] != "must remain unchanged" {
		t.Fatalf("comment content = %q, want unchanged", comment["content"])
	}
}
