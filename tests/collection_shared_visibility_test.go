package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestV2CollectionVisibility covers issue #318: collections are shared by
// default. Workspace-linked collections are visible to workspace members;
// global collections are visible to every authenticated caller. A private
// collection stays with its owner.
func TestV2CollectionVisibility(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	sharedWS, _ := CreateTestWorkspace(t, server, "Shared collection workspace", shortKey("CSW"))
	hiddenWS, _ := CreateTestWorkspace(t, server, "Hidden collection workspace", shortKey("CWH"))
	LockDownWorkspace(t, server, sharedWS)
	LockDownWorkspace(t, server, hiddenWS)

	memberID, memberName, memberPass := CreateTestUserWithCredentials(t, server, "collection_share_member", "collection_share_member@example.com")
	AssignWorkspaceRole(t, server, memberID, sharedWS, "Viewer")
	memberSession, _ := CreateAuthCredentialsForUser(t, server, memberName, memberPass)

	_, outsiderName, outsiderPass := CreateTestUserWithCredentials(t, server, "collection_share_outsider", "collection_share_outsider@example.com")
	outsiderSession, _ := CreateAuthCredentialsForUser(t, server, outsiderName, outsiderPass)

	create := func(name string, body map[string]any) int {
		t.Helper()
		body["name"] = name
		response := MakeAuthRequest(t, server, http.MethodPost, "/v2/collections", body)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusCreated)
		var document struct {
			Data struct {
				ID int `json:"id"`
			} `json:"data"`
		}
		DecodeJSON(t, response, &document)
		return document.Data.ID
	}

	shared := create("Workspace shared", map[string]any{"workspace_id": sharedWS})
	hidden := create("Hidden workspace", map[string]any{"workspace_id": hiddenWS})
	hiddenPublic := create("Hidden workspace public", map[string]any{
		"workspace_id": hiddenWS,
		"ql_query":     fmt.Sprintf("workspace_id = %d", hiddenWS),
		"is_public":    true,
		"public_slug":  "hidden-workspace-public",
	})
	globalShared := create("Shared global", map[string]any{})
	globalPrivate := create("Private global", map[string]any{"is_private": true})

	listIDs := func(session string) map[int]bool {
		t.Helper()
		response := MakeAuthRequestWithToken(t, server, session, http.MethodGet, "/v2/collections", nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusOK)
		var document struct {
			Data []struct {
				ID int `json:"id"`
			} `json:"data"`
			Pagination struct {
				TotalItems int `json:"total_items"`
			} `json:"pagination"`
		}
		DecodeJSON(t, response, &document)
		if document.Pagination.TotalItems != len(document.Data) {
			t.Fatalf("total_items = %d, page length = %d", document.Pagination.TotalItems, len(document.Data))
		}
		ids := make(map[int]bool, len(document.Data))
		for _, collection := range document.Data {
			ids[collection.ID] = true
		}
		return ids
	}

	getStatus := func(session string, id int) int {
		t.Helper()
		response := MakeAuthRequestWithToken(t, server, session, http.MethodGet, fmt.Sprintf("/v2/collections/%d", id), nil)
		defer response.Body.Close()
		return response.StatusCode
	}

	setPrivate := func(id int, private bool) {
		t.Helper()
		response := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/v2/collections/%d", id), map[string]any{"is_private": private})
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusOK)
	}

	t.Run("workspace member sees shared workspace and global collections", func(t *testing.T) {
		ids := listIDs(memberSession)
		for _, want := range []int{shared, globalShared} {
			if !ids[want] {
				t.Fatalf("member list missing collection %d: %v", want, ids)
			}
		}
		for _, absent := range []int{hidden, hiddenPublic, globalPrivate} {
			if ids[absent] {
				t.Fatalf("member list leaked collection %d: %v", absent, ids)
			}
		}
		if status := getStatus(memberSession, shared); status != http.StatusOK {
			t.Fatalf("member GET shared workspace collection = %d, want 200", status)
		}
		if status := getStatus(memberSession, globalPrivate); status != http.StatusNotFound {
			t.Fatalf("member GET private global collection = %d, want 404", status)
		}
	})

	t.Run("outsider sees shared global collections only", func(t *testing.T) {
		ids := listIDs(outsiderSession)
		if !ids[globalShared] || len(ids) != 1 {
			t.Fatalf("outsider list = %v, want only shared global collection %d", ids, globalShared)
		}
		for _, denied := range []int{shared, hidden, hiddenPublic, globalPrivate} {
			if status := getStatus(outsiderSession, denied); status != http.StatusNotFound {
				t.Fatalf("outsider GET collection %d = %d, want 404", denied, status)
			}
		}
		if status := getStatus(outsiderSession, globalShared); status != http.StatusOK {
			t.Fatalf("outsider GET shared global collection = %d, want 200", status)
		}
	})

	t.Run("member can load the shared collection board bootstrap", func(t *testing.T) {
		response := MakeAuthRequestWithToken(t, server, memberSession, http.MethodGet, fmt.Sprintf("/v2/collections/%d/board-configuration/bootstrap", shared), nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusOK)
	})

	t.Run("privacy toggle hides and restores a global collection", func(t *testing.T) {
		setPrivate(globalShared, true)
		if ids := listIDs(outsiderSession); ids[globalShared] {
			t.Fatalf("private toggle leaked collection %d to outsider: %v", globalShared, ids)
		}
		if status := getStatus(outsiderSession, globalShared); status != http.StatusNotFound {
			t.Fatalf("outsider GET private global collection = %d, want 404", status)
		}
		setPrivate(globalShared, false)
		if ids := listIDs(outsiderSession); !ids[globalShared] {
			t.Fatalf("restored shared collection %d missing from outsider list: %v", globalShared, ids)
		}
	})
}
