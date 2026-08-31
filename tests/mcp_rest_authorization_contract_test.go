package tests

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func dialMCPWithToken(t *testing.T, ts *TestServer, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "authorization-contract-tests", Version: "1.0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: ts.BaseURL + "/mcp",
		HTTPClient: &http.Client{Transport: &bearerRoundTripper{
			token: token,
		}},
	}, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callMCPForContract(session *mcp.ClientSession, tool string, args map[string]interface{}) (string, error) {
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return "", err
	}
	return joinTextContent(result.Content), nil
}

func TestMCPAndRESTV1_ItemAuthorizationContract(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Cross Surface Auth", shortKey("CSA"))
	LockDownWorkspace(t, server, workspaceID)
	itemID := CreateTestItem(t, server, workspaceID, "Protected contract item")

	viewerID, viewerUsername, viewerPassword := CreateTestUserWithCredentials(t, server, "contract_viewer", "contract_viewer@test.com")
	AssignWorkspaceRole(t, server, viewerID, workspaceID, "Viewer")

	t.Run("missing read scope is denied by both token gates", func(t *testing.T) {
		token := createTokenWithScopesAsUser(t, server, viewerUsername, viewerPassword, []string{"mcp:access"})
		response := MakeBearerRequestWithToken(t, server, token, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/items/%d", itemID), nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusForbidden)

		session := dialMCPWithToken(t, server, token)
		body, err := callMCPForContract(session, "get_item", map[string]interface{}{"item_id": itemID})
		if err == nil && !strings.Contains(body, "items:read") {
			t.Fatalf("MCP get_item missing-scope denial = %q, want items:read", body)
		}
		if err != nil && !strings.Contains(err.Error(), "403") && !strings.Contains(err.Error(), "Forbidden") && !strings.Contains(err.Error(), "items:read") && !strings.Contains(err.Error(), "scope") {
			t.Fatalf("MCP get_item missing-scope error = %v", err)
		}
	})

	t.Run("viewer read succeeds on both surfaces", func(t *testing.T) {
		token := createTokenWithScopesAsUser(t, server, viewerUsername, viewerPassword, []string{"mcp:access", "items:read"})
		response := MakeBearerRequestWithToken(t, server, token, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/items/%d", itemID), nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusOK)

		body, err := callMCPForContract(dialMCPWithToken(t, server, token), "get_item", map[string]interface{}{"item_id": itemID})
		if err != nil {
			t.Fatalf("MCP get_item: %v", err)
		}
		if !strings.Contains(body, "Protected contract item") {
			t.Fatalf("MCP get_item response = %q", body)
		}
	})

	t.Run("viewer write and delete are denied despite token scopes", func(t *testing.T) {
		token := createTokenWithScopesAsUser(t, server, viewerUsername, viewerPassword,
			[]string{"mcp:access", "items:read", "items:write", "items:delete"})

		updateResponse := MakeBearerRequestWithToken(t, server, token, http.MethodPut,
			fmt.Sprintf("/rest/api/v1/items/%d", itemID), map[string]interface{}{"title": "forbidden update"})
		defer updateResponse.Body.Close()
		AssertStatusCode(t, updateResponse, http.StatusNotFound)

		session := dialMCPWithToken(t, server, token)
		updateBody, err := callMCPForContract(session, "update_item", map[string]interface{}{
			"item_id": itemID,
			"title":   "forbidden update",
		})
		if err != nil {
			t.Fatalf("MCP update_item: %v", err)
		}
		if !strings.Contains(updateBody, "permission denied") {
			t.Fatalf("MCP update denial = %q", updateBody)
		}

		deleteResponse := MakeBearerRequestWithToken(t, server, token, http.MethodDelete,
			fmt.Sprintf("/rest/api/v1/items/%d", itemID), nil)
		defer deleteResponse.Body.Close()
		AssertStatusCode(t, deleteResponse, http.StatusNotFound)

		deleteBody, err := callMCPForContract(session, "delete_item", map[string]interface{}{"item_id": itemID})
		if err != nil {
			t.Fatalf("MCP delete_item: %v", err)
		}
		if !strings.Contains(deleteBody, "permission denied") {
			t.Fatalf("MCP delete denial = %q", deleteBody)
		}
	})

	t.Run("inaccessible workspace is existence-masked on both surfaces", func(t *testing.T) {
		_, outsiderUsername, outsiderPassword := CreateTestUserWithCredentials(t, server, "contract_outsider", "contract_outsider@test.com")
		token := createTokenWithScopesAsUser(t, server, outsiderUsername, outsiderPassword,
			[]string{"mcp:access", "items:read"})

		response := MakeBearerRequestWithToken(t, server, token, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/items/%d", itemID), nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusNotFound)

		body, err := callMCPForContract(dialMCPWithToken(t, server, token), "get_item", map[string]interface{}{"item_id": itemID})
		if err != nil {
			t.Fatalf("MCP get_item: %v", err)
		}
		if !strings.Contains(body, "item not found") {
			t.Fatalf("MCP cross-workspace denial = %q", body)
		}
	})
}

func TestMCPAndRESTV1_DestructiveItemAuthorizationContract(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Destructive Item Contract", shortKey("DIC"))
	LockDownWorkspace(t, server, workspaceID)
	protectedItemID := CreateTestItem(t, server, workspaceID, "Protected destructive item")

	assertItemExists := func(t *testing.T, itemID int) {
		t.Helper()
		response := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d", itemID), nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusOK)
	}
	assertItemDeleted := func(t *testing.T, itemID int) {
		t.Helper()
		response := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d", itemID), nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusNotFound)
	}

	t.Run("missing delete scope is denied by both token gates", func(t *testing.T) {
		token := createTokenWithScopesAsUser(t, server, "admin", "testpass123",
			[]string{"mcp:access", "items:read"})
		response := MakeBearerRequestWithToken(t, server, token, http.MethodDelete,
			fmt.Sprintf("/rest/api/v1/items/%d", protectedItemID), nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusForbidden)

		body, err := callMCPForContract(dialMCPWithToken(t, server, token), "delete_item",
			map[string]interface{}{"item_id": protectedItemID})
		if err == nil && !strings.Contains(body, "items:delete") {
			t.Fatalf("MCP delete_item missing-scope denial = %q, want items:delete", body)
		}
		if err != nil && !strings.Contains(err.Error(), "403") && !strings.Contains(err.Error(), "Forbidden") &&
			!strings.Contains(err.Error(), "items:delete") && !strings.Contains(err.Error(), "scope") {
			t.Fatalf("MCP delete_item missing-scope error = %v", err)
		}
		assertItemExists(t, protectedItemID)
	})

	editorID, editorUsername, editorPassword := CreateTestUserWithCredentials(t, server,
		"destructive_editor", "destructive_editor@test.com")
	AssignWorkspaceRole(t, server, editorID, workspaceID, "Editor")
	editorToken := createTokenWithScopesAsUser(t, server, editorUsername, editorPassword,
		[]string{"mcp:access", "items:read", "items:delete"})

	t.Run("item edit permission does not imply delete permission", func(t *testing.T) {
		response := MakeBearerRequestWithToken(t, server, editorToken, http.MethodDelete,
			fmt.Sprintf("/rest/api/v1/items/%d", protectedItemID), nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusNotFound)

		body, err := callMCPForContract(dialMCPWithToken(t, server, editorToken), "delete_item",
			map[string]interface{}{"item_id": protectedItemID})
		if err != nil {
			t.Fatalf("MCP delete_item: %v", err)
		}
		if !strings.Contains(body, "permission denied") {
			t.Fatalf("MCP editor delete denial = %q", body)
		}
		assertItemExists(t, protectedItemID)
	})

	_, outsiderUsername, outsiderPassword := CreateTestUserWithCredentials(t, server,
		"destructive_outsider", "destructive_outsider@test.com")
	outsiderToken := createTokenWithScopesAsUser(t, server, outsiderUsername, outsiderPassword,
		[]string{"mcp:access", "items:read", "items:delete"})

	t.Run("inaccessible workspace is existence-masked on both surfaces", func(t *testing.T) {
		response := MakeBearerRequestWithToken(t, server, outsiderToken, http.MethodDelete,
			fmt.Sprintf("/rest/api/v1/items/%d", protectedItemID), nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusNotFound)

		body, err := callMCPForContract(dialMCPWithToken(t, server, outsiderToken), "delete_item",
			map[string]interface{}{"item_id": protectedItemID})
		if err != nil {
			t.Fatalf("MCP delete_item: %v", err)
		}
		if !strings.Contains(body, "item not found") {
			t.Fatalf("MCP cross-workspace delete denial = %q", body)
		}
		assertItemExists(t, protectedItemID)
	})

	adminToken := createTokenWithScopesAsUser(t, server, "admin", "testpass123",
		[]string{"mcp:access", "items:read", "items:delete"})
	t.Run("administrator cascade deletes persist through both surfaces", func(t *testing.T) {
		restParentID, restChildID := createItemTreeForDeletion(t, server, workspaceID, "REST destructive")
		response := MakeBearerRequestWithToken(t, server, adminToken, http.MethodDelete,
			fmt.Sprintf("/rest/api/v1/items/%d", restParentID), nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusNoContent)
		assertItemDeleted(t, restParentID)
		assertItemDeleted(t, restChildID)

		mcpParentID, mcpChildID := createItemTreeForDeletion(t, server, workspaceID, "MCP destructive")
		body, err := callMCPForContract(dialMCPWithToken(t, server, adminToken), "delete_item",
			map[string]interface{}{"item_id": mcpParentID})
		if err != nil {
			t.Fatalf("MCP delete_item: %v", err)
		}
		if !strings.Contains(body, `"deleted":true`) || !strings.Contains(body, `"deleted_count":2`) {
			t.Fatalf("MCP cascade deletion response = %q", body)
		}
		assertItemDeleted(t, mcpParentID)
		assertItemDeleted(t, mcpChildID)
	})
}

func TestMCPAndRESTV1_TimeProjectAuthorizationContract(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	projectID := createTimeProjectFixture(t, server, "Restricted Time Contract")

	allowedID, allowedUsername, allowedPassword := CreateTestUserWithCredentials(t, server,
		"time_contract_member", "time_contract_member@test.com")
	_, deniedUsername, deniedPassword := CreateTestUserWithCredentials(t, server,
		"time_contract_outsider", "time_contract_outsider@test.com")

	for endpoint, body := range map[string]map[string]interface{}{
		fmt.Sprintf("/time/projects/%d/managers", projectID): {
			"manager_type": "user",
			"manager_id":   allowedID,
		},
		fmt.Sprintf("/time/projects/%d/members", projectID): {
			"member_type": "user",
			"member_id":   allowedID,
		},
	} {
		response := MakeAuthRequest(t, server, http.MethodPost, endpoint, body)
		AssertStatusCode(t, response, http.StatusCreated)
		response.Body.Close()
	}

	t.Run("missing read scope is denied by both token gates", func(t *testing.T) {
		token := createTokenWithScopesAsUser(t, server, allowedUsername, allowedPassword, []string{"mcp:access"})
		response := MakeBearerRequestWithToken(t, server, token, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/time/projects/%d", projectID), nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusForbidden)

		body, err := callMCPForContract(dialMCPWithToken(t, server, token), "list_time_projects", map[string]interface{}{})
		if err == nil && !strings.Contains(body, "time:read") {
			t.Fatalf("MCP list_time_projects missing-scope denial = %q, want time:read", body)
		}
		if err != nil && !strings.Contains(err.Error(), "403") && !strings.Contains(err.Error(), "Forbidden") &&
			!strings.Contains(err.Error(), "time:read") && !strings.Contains(err.Error(), "scope") {
			t.Fatalf("MCP list_time_projects missing-scope error = %v", err)
		}
	})

	deniedToken := createTokenWithScopesAsUser(t, server, deniedUsername, deniedPassword,
		[]string{"mcp:access", "time:read", "time:write"})
	t.Run("project ACL hides reads and denies booking on both surfaces", func(t *testing.T) {
		getResponse := MakeBearerRequestWithToken(t, server, deniedToken, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/time/projects/%d", projectID), nil)
		defer getResponse.Body.Close()
		AssertStatusCode(t, getResponse, http.StatusNotFound)

		listResponse := MakeBearerRequestWithToken(t, server, deniedToken, http.MethodGet,
			"/rest/api/v1/time/projects", nil)
		defer listResponse.Body.Close()
		AssertStatusCode(t, listResponse, http.StatusOK)
		var projects []map[string]interface{}
		DecodeJSON(t, listResponse, &projects)
		for _, project := range projects {
			if int(project["id"].(float64)) == projectID {
				t.Fatalf("REST time-project list disclosed restricted project %d", projectID)
			}
		}

		session := dialMCPWithToken(t, server, deniedToken)
		listBody, err := callMCPForContract(session, "list_time_projects", map[string]interface{}{})
		if err != nil {
			t.Fatalf("MCP list_time_projects: %v", err)
		}
		if strings.Contains(listBody, "Restricted Time Contract") {
			t.Fatalf("MCP time-project list disclosed restricted project: %q", listBody)
		}

		worklog := map[string]interface{}{
			"project_id":       projectID,
			"description":      "forbidden booking",
			"date":             "2026-07-23",
			"duration_minutes": 30,
		}
		createResponse := MakeBearerRequestWithToken(t, server, deniedToken, http.MethodPost,
			"/rest/api/v1/time/worklogs", worklog)
		defer createResponse.Body.Close()
		AssertStatusCode(t, createResponse, http.StatusForbidden)

		createBody, err := callMCPForContract(session, "log_time", worklog)
		if err != nil {
			t.Fatalf("MCP log_time: %v", err)
		}
		if !strings.Contains(createBody, "no permission to book time on this project") {
			t.Fatalf("MCP time booking denial = %q", createBody)
		}
	})

	allowedToken := createTokenWithScopesAsUser(t, server, allowedUsername, allowedPassword,
		[]string{"mcp:access", "time:read", "time:write"})
	t.Run("explicit project membership enables reads and booking on both surfaces", func(t *testing.T) {
		getResponse := MakeBearerRequestWithToken(t, server, allowedToken, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/time/projects/%d", projectID), nil)
		defer getResponse.Body.Close()
		AssertStatusCode(t, getResponse, http.StatusOK)

		session := dialMCPWithToken(t, server, allowedToken)
		listBody, err := callMCPForContract(session, "list_time_projects", map[string]interface{}{})
		if err != nil {
			t.Fatalf("MCP list_time_projects: %v", err)
		}
		if !strings.Contains(listBody, "Restricted Time Contract") {
			t.Fatalf("MCP granted time-project list = %q", listBody)
		}

		restWorklog := map[string]interface{}{
			"project_id":       projectID,
			"description":      "REST authorized booking",
			"date":             "2026-07-23",
			"duration_minutes": 30,
		}
		createResponse := MakeBearerRequestWithToken(t, server, allowedToken, http.MethodPost,
			"/rest/api/v1/time/worklogs", restWorklog)
		defer createResponse.Body.Close()
		AssertStatusCode(t, createResponse, http.StatusCreated)

		mcpBody, err := callMCPForContract(session, "log_time", map[string]interface{}{
			"project_id":       projectID,
			"description":      "MCP authorized booking",
			"date":             "2026-07-23",
			"duration_minutes": 45,
		})
		if err != nil {
			t.Fatalf("MCP log_time: %v", err)
		}
		if !strings.Contains(mcpBody, "MCP authorized booking") {
			t.Fatalf("MCP authorized booking response = %q", mcpBody)
		}

		var worklogCount, durationTotal int
		if err := server.server.DB().QueryRow(`
			SELECT COUNT(*), COALESCE(SUM(duration_minutes), 0)
			FROM time_worklogs WHERE project_id = ? AND user_id = ?
		`, projectID, allowedID).Scan(&worklogCount, &durationTotal); err != nil {
			t.Fatalf("load authorized worklogs: %v", err)
		}
		if worklogCount != 2 || durationTotal != 75 {
			t.Fatalf("authorized worklogs count/duration = %d/%d, want 2/75", worklogCount, durationTotal)
		}
	})
}

func TestMCPAndRESTV1_PageACLAuthorizationContract(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Page ACL Contract", shortKey("PAC"))
	LockDownWorkspace(t, server, workspaceID)

	createResponse := MakeBearerRequestWithToken(t, server, server.BearerToken, http.MethodPost,
		fmt.Sprintf("/rest/api/v1/workspaces/%d/pages", workspaceID), map[string]interface{}{
			"title":   "Restricted page",
			"content": "original content",
		})
	defer createResponse.Body.Close()
	AssertStatusCode(t, createResponse, http.StatusCreated)
	var createdPage map[string]interface{}
	DecodeJSON(t, createResponse, &createdPage)
	pageID := ExtractIDFromResponse(t, createdPage)

	inheritanceResponse := MakeBearerRequestWithToken(t, server, server.BearerToken, http.MethodPatch,
		fmt.Sprintf("/rest/api/v1/workspaces/%d/pages/%d/inheritance", workspaceID, pageID),
		map[string]interface{}{"inherit_permissions": false})
	defer inheritanceResponse.Body.Close()
	AssertStatusCode(t, inheritanceResponse, http.StatusOK)

	viewerID, viewerUsername, viewerPassword := CreateTestUserWithCredentials(t, server, "page_acl_viewer", "page_acl_viewer@test.com")
	AssignWorkspaceRole(t, server, viewerID, workspaceID, "Viewer")

	t.Run("missing read scope is denied by both token gates", func(t *testing.T) {
		token := createTokenWithScopesAsUser(t, server, viewerUsername, viewerPassword, []string{"mcp:access"})
		response := MakeBearerRequestWithToken(t, server, token, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/workspaces/%d/pages/%d", workspaceID, pageID), nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusForbidden)

		body, err := callMCPForContract(dialMCPWithToken(t, server, token), "get_page", map[string]interface{}{"page_id": pageID})
		if err == nil && !strings.Contains(body, "pages:read") {
			t.Fatalf("MCP get_page missing-scope denial = %q, want pages:read", body)
		}
		if err != nil && !strings.Contains(err.Error(), "403") && !strings.Contains(err.Error(), "Forbidden") && !strings.Contains(err.Error(), "pages:read") && !strings.Contains(err.Error(), "scope") {
			t.Fatalf("MCP get_page missing-scope error = %v", err)
		}
	})

	viewerToken := createTokenWithScopesAsUser(t, server, viewerUsername, viewerPassword,
		[]string{"mcp:access", "pages:read", "pages:write"})

	t.Run("page without an ACL grant is hidden on both surfaces", func(t *testing.T) {
		response := MakeBearerRequestWithToken(t, server, viewerToken, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/workspaces/%d/pages/%d", workspaceID, pageID), nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusNotFound)

		body, err := callMCPForContract(dialMCPWithToken(t, server, viewerToken), "get_page", map[string]interface{}{"page_id": pageID})
		if err != nil {
			t.Fatalf("MCP get_page: %v", err)
		}
		if !strings.Contains(body, "page not found") {
			t.Fatalf("MCP page ACL denial = %q", body)
		}
	})

	grantResponse := MakeBearerRequestWithToken(t, server, server.BearerToken, http.MethodPost,
		fmt.Sprintf("/rest/api/v1/workspaces/%d/pages/%d/permissions", workspaceID, pageID),
		map[string]interface{}{
			"principal_type":   "user",
			"principal_id":     viewerID,
			"permission_level": "view",
		})
	defer grantResponse.Body.Close()
	AssertStatusCode(t, grantResponse, http.StatusCreated)

	t.Run("explicit view grant enables reads on both surfaces", func(t *testing.T) {
		response := MakeBearerRequestWithToken(t, server, viewerToken, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/workspaces/%d/pages/%d", workspaceID, pageID), nil)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusOK)

		body, err := callMCPForContract(dialMCPWithToken(t, server, viewerToken), "get_page", map[string]interface{}{"page_id": pageID})
		if err != nil {
			t.Fatalf("MCP get_page: %v", err)
		}
		if !strings.Contains(body, "Restricted page") || !strings.Contains(body, "original content") {
			t.Fatalf("MCP granted page response = %q", body)
		}
	})

	t.Run("view grant still denies edits and preserves the page", func(t *testing.T) {
		response := MakeBearerRequestWithToken(t, server, viewerToken, http.MethodPut,
			fmt.Sprintf("/rest/api/v1/workspaces/%d/pages/%d", workspaceID, pageID),
			map[string]interface{}{"title": "forbidden REST update"})
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusNotFound)

		body, err := callMCPForContract(dialMCPWithToken(t, server, viewerToken), "update_page", map[string]interface{}{
			"page_id": pageID,
			"title":   "forbidden MCP update",
		})
		if err != nil {
			t.Fatalf("MCP update_page: %v", err)
		}
		if !strings.Contains(body, "page not found") {
			t.Fatalf("MCP page edit denial = %q", body)
		}

		adminReadResponse := MakeBearerRequestWithToken(t, server, server.BearerToken, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/workspaces/%d/pages/%d", workspaceID, pageID), nil)
		defer adminReadResponse.Body.Close()
		AssertStatusCode(t, adminReadResponse, http.StatusOK)
		var persistedPage map[string]interface{}
		DecodeJSON(t, adminReadResponse, &persistedPage)
		if persistedPage["title"] != "Restricted page" || persistedPage["content"] != "original content" {
			t.Fatalf("page changed after denied edits: title=%v content=%v", persistedPage["title"], persistedPage["content"])
		}
	})
}

func TestMCPAndRESTV1_CommentOwnershipAuthorizationContract(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Comment Ownership Contract", shortKey("COC"))
	LockDownWorkspace(t, server, workspaceID)
	itemID := CreateTestItem(t, server, workspaceID, "Comment ownership item")

	authorID, authorUsername, authorPassword := CreateTestUserWithCredentials(t, server, "comment_author", "comment_author@test.com")
	peerID, peerUsername, peerPassword := CreateTestUserWithCredentials(t, server, "comment_peer", "comment_peer@test.com")
	adminID, adminUsername, adminPassword := CreateTestUserWithCredentials(t, server, "comment_admin", "comment_admin@test.com")
	AssignWorkspaceRole(t, server, authorID, workspaceID, "Editor")
	AssignWorkspaceRole(t, server, peerID, workspaceID, "Editor")
	AssignWorkspaceRole(t, server, adminID, workspaceID, "Administrator")

	scopes := []string{"mcp:access", "items:read", "items:write", "items:delete"}
	authorToken := createTokenWithScopesAsUser(t, server, authorUsername, authorPassword, scopes)
	peerToken := createTokenWithScopesAsUser(t, server, peerUsername, peerPassword, scopes)
	adminToken := createTokenWithScopesAsUser(t, server, adminUsername, adminPassword, scopes)

	createComment := func(content string) int {
		t.Helper()
		response := MakeBearerRequestWithToken(t, server, authorToken, http.MethodPost,
			fmt.Sprintf("/rest/api/v1/items/%d/comments", itemID), map[string]interface{}{"content": content})
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusCreated)
		var comment map[string]interface{}
		DecodeJSON(t, response, &comment)
		return ExtractIDFromResponse(t, comment)
	}

	t.Run("author can update through both surfaces", func(t *testing.T) {
		commentID := createComment("author original")
		response := MakeBearerRequestWithToken(t, server, authorToken, http.MethodPut,
			fmt.Sprintf("/rest/api/v1/comments/%d", commentID), map[string]interface{}{"content": "author REST update"})
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusOK)

		body, err := callMCPForContract(dialMCPWithToken(t, server, authorToken), "update_comment", map[string]interface{}{
			"comment_id": commentID,
			"content":    "author MCP update",
		})
		if err != nil {
			t.Fatalf("MCP update_comment as author: %v", err)
		}
		if !strings.Contains(body, "author MCP update") {
			t.Fatalf("MCP author update response = %q", body)
		}
	})

	t.Run("peer editor is denied despite item edit permission", func(t *testing.T) {
		commentID := createComment("peer must not change this")

		updateResponse := MakeBearerRequestWithToken(t, server, peerToken, http.MethodPut,
			fmt.Sprintf("/rest/api/v1/comments/%d", commentID), map[string]interface{}{"content": "forbidden REST update"})
		defer updateResponse.Body.Close()
		AssertStatusCode(t, updateResponse, http.StatusNotFound)

		updateBody, err := callMCPForContract(dialMCPWithToken(t, server, peerToken), "update_comment", map[string]interface{}{
			"comment_id": commentID,
			"content":    "forbidden MCP update",
		})
		if err != nil {
			t.Fatalf("MCP update_comment as peer: %v", err)
		}
		if !strings.Contains(updateBody, "comment not found") {
			t.Fatalf("MCP peer update denial = %q", updateBody)
		}

		deleteResponse := MakeBearerRequestWithToken(t, server, peerToken, http.MethodDelete,
			fmt.Sprintf("/rest/api/v1/comments/%d", commentID), nil)
		defer deleteResponse.Body.Close()
		AssertStatusCode(t, deleteResponse, http.StatusNotFound)

		deleteBody, err := callMCPForContract(dialMCPWithToken(t, server, peerToken), "delete_comment", map[string]interface{}{"comment_id": commentID})
		if err != nil {
			t.Fatalf("MCP delete_comment as peer: %v", err)
		}
		if !strings.Contains(deleteBody, "comment not found") {
			t.Fatalf("MCP peer delete denial = %q", deleteBody)
		}

		readResponse := MakeBearerRequestWithToken(t, server, authorToken, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/comments/%d", commentID), nil)
		defer readResponse.Body.Close()
		AssertStatusCode(t, readResponse, http.StatusOK)
		var persisted map[string]interface{}
		DecodeJSON(t, readResponse, &persisted)
		if persisted["content"] != "peer must not change this" {
			t.Fatalf("comment changed after denied mutations: %v", persisted)
		}
	})

	t.Run("workspace administrator can edit and delete others comments", func(t *testing.T) {
		updateID := createComment("administrator update target")
		deleteID := createComment("administrator delete target")

		response := MakeBearerRequestWithToken(t, server, adminToken, http.MethodPut,
			fmt.Sprintf("/rest/api/v1/comments/%d", updateID), map[string]interface{}{"content": "administrator REST update"})
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusOK)

		body, err := callMCPForContract(dialMCPWithToken(t, server, adminToken), "update_comment", map[string]interface{}{
			"comment_id": updateID,
			"content":    "administrator MCP update",
		})
		if err != nil {
			t.Fatalf("MCP update_comment as administrator: %v", err)
		}
		if !strings.Contains(body, "administrator MCP update") {
			t.Fatalf("MCP administrator update response = %q", body)
		}

		deleteResponse := MakeBearerRequestWithToken(t, server, adminToken, http.MethodDelete,
			fmt.Sprintf("/rest/api/v1/comments/%d", deleteID), nil)
		defer deleteResponse.Body.Close()
		AssertStatusCode(t, deleteResponse, http.StatusNoContent)

		mcpDeleteID := createComment("administrator MCP delete target")
		deleteBody, err := callMCPForContract(dialMCPWithToken(t, server, adminToken), "delete_comment", map[string]interface{}{"comment_id": mcpDeleteID})
		if err != nil {
			t.Fatalf("MCP delete_comment as administrator: %v", err)
		}
		if !strings.Contains(deleteBody, "\"deleted\":true") {
			t.Fatalf("MCP administrator delete response = %q", deleteBody)
		}
	})
}
