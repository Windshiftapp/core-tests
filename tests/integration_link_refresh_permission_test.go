package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestIntegrationLinkRefreshRequiresEditPermission verifies that refreshing an
// integration item link requires item edit permission. Refreshing writes the
// shared title, icon, and external URL, so it must match the create/delete
// permission contract; view-only users must be denied (with 404 to avoid
// existence leakage), while editors pass the permission check and only fail
// later on the missing provider connection.
func TestIntegrationLinkRefreshRequiresEditPermission(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Link Refresh Perms Workspace", "")
	itemID := CreateTestItem(t, server, wsID, "Item with Notion link")

	// Notion is the provider type whose generic item links support refresh.
	resp := MakeAuthRequest(t, server, http.MethodPost, "/admin/integration-providers", map[string]interface{}{
		"slug":          "notion-refresh-perms",
		"name":          "Notion Refresh Perms",
		"provider_type": "notion",
		"enabled":       true,
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var provider struct {
		ID string `json:"id"`
	}
	DecodeJSON(t, resp, &provider)
	if provider.ID == "" {
		t.Fatal("Expected non-empty provider ID")
	}

	createResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/integration-links", itemID), map[string]interface{}{
		"provider_id":  provider.ID,
		"external_id":  "page-123",
		"title":        "Linked page",
		"link_type":    "page",
		"external_url": "https://notion.so/page-123",
	})
	defer createResp.Body.Close()
	AssertStatusCode(t, createResp, http.StatusCreated)
	var created struct {
		ID string `json:"id"`
	}
	DecodeJSON(t, createResp, &created)
	if created.ID == "" {
		t.Fatal("Expected non-empty link ID")
	}
	linkID := created.ID

	// View-only user: denied with 404 so the link's existence is not leaked.
	viewerID, _, viewerPass := CreateTestUserWithCredentials(t, server, "linkrefreshviewer", "linkrefreshviewer@test.local")
	AssignWorkspaceRole(t, server, viewerID, wsID, "Viewer")
	viewerSession, _ := CreateAuthCredentialsForUser(t, server, "linkrefreshviewer", viewerPass)

	viewerResp := MakeAuthRequestWithToken(t, server, viewerSession, http.MethodPost, fmt.Sprintf("/item-integration-links/%s/refresh", linkID), nil)
	defer viewerResp.Body.Close()
	AssertStatusCode(t, viewerResp, http.StatusNotFound)

	// Editor user: passes the permission check and reaches the provider
	// connection lookup, which fails with 400 because no Notion account is
	// connected. Reaching that failure proves the permission check passed.
	editorID, _, editorPass := CreateTestUserWithCredentials(t, server, "linkrefresheditor", "linkrefresheditor@test.local")
	AssignWorkspaceRole(t, server, editorID, wsID, "Editor")
	editorSession, _ := CreateAuthCredentialsForUser(t, server, "linkrefresheditor", editorPass)

	editorResp := MakeAuthRequestWithToken(t, server, editorSession, http.MethodPost, fmt.Sprintf("/item-integration-links/%s/refresh", linkID), nil)
	defer editorResp.Body.Close()
	AssertStatusCode(t, editorResp, http.StatusBadRequest)
}
