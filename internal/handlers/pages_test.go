package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"windshift/internal/database"
	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/services"
)

// newPageHandler wires a fully-initialized SQLite DB, the real
// PermissionService, and the page services into a PageHandler suitable for
// integration tests.
func newPageHandler(t *testing.T) (*PageHandler, database.Database, *services.PermissionService) {
	t.Helper()
	db := newNegativeTestDB(t)
	perm := newNegativeTestPermissionService(t, db)
	svc := services.NewPageService(db)
	auth := services.NewPagePermissionService(db, perm)
	h := NewPageHandler(svc, auth, perm, logger.NewAuditor(db))
	return h, db, perm
}

// seedWorkspaceWithRole inserts a workspace and assigns userID the named
// role, plus a second admin user so the workspace is in gated mode (memory:
// open-by-default workspaces don't exercise the 404-not-403 invariant).
func seedWorkspaceWithRole(t *testing.T, db database.Database, workspaceID, userID int, role string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO workspaces (id, name, key, active) VALUES (?, 'WS', ?, TRUE)`, workspaceID, "WS"+strconv.Itoa(workspaceID)); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	var roleID int
	if err := db.QueryRow(`SELECT id FROM workspace_roles WHERE name = ?`, role).Scan(&roleID); err != nil {
		t.Fatalf("look up role %s: %v", role, err)
	}
	var adminRoleID int
	if err := db.QueryRow(`SELECT id FROM workspace_roles WHERE name = 'Administrator'`).Scan(&adminRoleID); err != nil {
		t.Fatalf("look up Administrator role: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
	`, userID, workspaceID, roleID); err != nil {
		t.Fatalf("assign workspace role: %v", err)
	}
	// Ensure the workspace is in gated mode by guaranteeing at least one
	// Administrator membership distinct from userID. Seed a stable phantom
	// admin (uid 999) — the only thing that matters is the workspace_roles
	// table sees a non-empty role grant.
	if userID != 999 {
		if _, err := db.Exec(`
			INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_at)
			VALUES (999, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT (user_id, workspace_id, role_id) DO NOTHING
		`, workspaceID, adminRoleID); err != nil {
			t.Fatalf("assign phantom admin: %v", err)
		}
	}
}

// assignWorkspaceRole adds a role grant for an existing workspace without
// re-inserting the workspace row, which seedWorkspaceWithRole always does.
func assignWorkspaceRole(t *testing.T, db database.Database, workspaceID, userID int, role string) {
	t.Helper()
	var roleID int
	if err := db.QueryRow(`SELECT id FROM workspace_roles WHERE name = ?`, role).Scan(&roleID); err != nil {
		t.Fatalf("look up role %s: %v", role, err)
	}
	if _, err := db.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
	`, userID, workspaceID, roleID); err != nil {
		t.Fatalf("assign workspace role: %v", err)
	}
}

func setPath(req *http.Request, kv map[string]string) {
	for k, v := range kv {
		req.SetPathValue(k, v)
	}
}

func TestPageHandler_GetTree_FiltersToWorkspace(t *testing.T) {
	h, db, _ := newPageHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")
	seedWorkspaceWithRole(t, db, 2, 999, "Administrator")

	// Seed a page in workspace 1 and one in workspace 2.
	if _, err := h.service.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "Hello"}); err != nil {
		t.Fatalf("create page ws1: %v", err)
	}
	if _, err := h.service.Create(999, services.CreatePageInput{WorkspaceID: 2, Title: "Other"}); err != nil {
		t.Fatalf("create page ws2: %v", err)
	}

	req := authedRequest(http.MethodGet, "/workspaces/1/pages/tree", userID, nil)
	setPath(req, map[string]string{"workspaceId": "1"})
	rr := httptest.NewRecorder()
	h.GetTree(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp pageTreeResponse
	decodeJSONBody(t, rr, &resp)
	if len(resp.Pages) != 1 || resp.Pages[0].Title != "Hello" {
		t.Errorf("expected exactly the ws1 page, got %+v", resp.Pages)
	}
}

func TestPageHandler_Get_404WhenCrossWorkspace(t *testing.T) {
	h, db, _ := newPageHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Viewer")
	seedWorkspaceWithRole(t, db, 2, 999, "Administrator")

	other, err := h.service.Create(999, services.CreatePageInput{WorkspaceID: 2, Title: "Secret"})
	if err != nil {
		t.Fatalf("seed secret page: %v", err)
	}

	req := authedRequest(http.MethodGet, "/workspaces/1/pages/"+strconv.Itoa(other.ID), userID, nil)
	setPath(req, map[string]string{"workspaceId": "1", "pageId": strconv.Itoa(other.ID)})
	rr := httptest.NewRecorder()
	h.Get(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("cross-workspace get: want 404, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPageHandler_Create_RejectsViewer(t *testing.T) {
	h, db, _ := newPageHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Viewer")

	body := map[string]interface{}{"title": "ShouldFail"}
	req := authedRequest(http.MethodPost, "/workspaces/1/pages", userID, body)
	setPath(req, map[string]string{"workspaceId": "1"})
	rr := httptest.NewRecorder()
	h.Create(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("viewer create: want 404 (no leak), got %d body=%s", rr.Code, rr.Body.String())
	}

	pages, _ := h.service.ListTree(1, false)
	if len(pages) != 0 {
		t.Errorf("page should not have been created by a viewer, got %d", len(pages))
	}
}

func TestPageHandler_Create_AllowsEditor(t *testing.T) {
	h, db, _ := newPageHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")

	body := map[string]interface{}{"title": "Onboarding", "content": "# hi"}
	req := authedRequest(http.MethodPost, "/workspaces/1/pages", userID, body)
	setPath(req, map[string]string{"workspaceId": "1"})
	rr := httptest.NewRecorder()
	h.Create(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("editor create: want 201, got %d body=%s", rr.Code, rr.Body.String())
	}
	var got models.Page
	decodeJSONBody(t, rr, &got)
	if got.Title != "Onboarding" {
		t.Errorf("title: want Onboarding, got %q", got.Title)
	}
}

func TestPageHandler_Delete_RejectsWhenDescendantRestrictsAdmin(t *testing.T) {
	// Bug-hunt finding #3: handler previously checked page.admin only on
	// the root, so a user with admin on the root could archive a subtree
	// containing pages they couldn't admin individually.
	//
	// Setup: editor gets page.delete (workspace-wide) but NOT page.admin.
	// We grant the editor an explicit page.admin via ACL on the root, so
	// they pass the root admin check, but we then break inheritance on
	// the child without granting them admin — they must NOT be able to
	// archive the subtree.
	h, db, _ := newPageHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")

	// Editor needs page.delete to reach the subtree-admin check at all.
	// We deliberately do NOT grant page.admin at the workspace level —
	// otherwise the workspace role short-circuits Can() and the per-page
	// ACL on child becomes irrelevant.
	var deleteID, editorRoleID int
	if err := db.QueryRow(`SELECT id FROM permissions WHERE permission_key='page.delete'`).Scan(&deleteID); err != nil {
		t.Fatalf("perm delete: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM workspace_roles WHERE name='Editor'`).Scan(&editorRoleID); err != nil {
		t.Fatalf("editor role: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO role_permissions (role_id, permission_id) VALUES (?, ?) ON CONFLICT (role_id, permission_id) DO NOTHING`, editorRoleID, deleteID); err != nil {
		t.Fatalf("grant editor page.delete: %v", err)
	}

	parent, err := h.service.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "Parent"})
	if err != nil {
		t.Fatalf("parent: %v", err)
	}
	// Grant the editor explicit page.admin on the parent via ACL so the
	// root admin check passes.
	if _, err := h.service.GrantPermission(userID, parent.ID, "user", userID, "admin"); err != nil {
		t.Fatalf("grant root admin: %v", err)
	}
	child, err := h.service.Create(userID, services.CreatePageInput{WorkspaceID: 1, ParentID: &parent.ID, Title: "Child"})
	if err != nil {
		t.Fatalf("child: %v", err)
	}
	// Break child inheritance — child now has its own ACL (empty), so
	// the editor's grant on the parent does NOT propagate. Child is
	// admin-only with no admin grant for our editor.
	if _, err := h.service.SetInheritPermissions(userID, child.ID, false); err != nil {
		t.Fatalf("break child inheritance: %v", err)
	}

	req := authedRequest(http.MethodDelete, "/workspaces/1/pages/"+strconv.Itoa(parent.ID), userID, nil)
	setPath(req, map[string]string{"workspaceId": "1", "pageId": strconv.Itoa(parent.ID)})
	rr := httptest.NewRecorder()
	h.Delete(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("subtree archive should be refused when a descendant denies: want 404, got %d body=%s", rr.Code, rr.Body.String())
	}

	// Parent must still be live.
	got, _ := h.service.GetByID(parent.ID)
	if got.ArchivedAt != nil {
		t.Error("parent should NOT have been archived when descendant denied admin")
	}
}

func TestPageHandler_Delete_RequiresPageDelete(t *testing.T) {
	h, db, _ := newPageHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")
	// Editor has page.view/create/edit but not page.delete or page.admin.

	page, err := h.service.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "Doomed"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	req := authedRequest(http.MethodDelete, "/workspaces/1/pages/"+strconv.Itoa(page.ID), userID, nil)
	setPath(req, map[string]string{"workspaceId": "1", "pageId": strconv.Itoa(page.ID)})
	rr := httptest.NewRecorder()
	h.Delete(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("editor delete: want 404, got %d body=%s", rr.Code, rr.Body.String())
	}

	got, _ := h.service.GetByID(page.ID)
	if got.ArchivedAt != nil {
		t.Error("page should not be archived after editor delete attempt")
	}
}

func TestPageHandler_Update_BumpsRevisionAndAllowsEditor(t *testing.T) {
	h, db, _ := newPageHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")

	page, err := h.service.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "T", Content: "v1"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	body := map[string]interface{}{"title": "T", "content": "v2"}
	req := authedRequest(http.MethodPut, "/workspaces/1/pages/"+strconv.Itoa(page.ID), userID, body)
	setPath(req, map[string]string{"workspaceId": "1", "pageId": strconv.Itoa(page.ID)})
	rr := httptest.NewRecorder()
	h.Update(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("editor update: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	revs, _ := h.service.ListRevisions(page.ID, 0, 0)
	if len(revs) != 2 {
		t.Errorf("expected 2 revisions after update, got %d", len(revs))
	}
}

func TestPageHandler_History_404OnCrossPageRevision(t *testing.T) {
	h, db, _ := newPageHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")

	a, _ := h.service.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "A"})
	b, _ := h.service.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "B"})

	bRevs, _ := h.service.ListRevisions(b.ID, 0, 0)
	if len(bRevs) == 0 {
		t.Fatal("B has no revisions")
	}
	otherRevID := bRevs[0].ID

	req := authedRequest(http.MethodGet, "/workspaces/1/pages/"+strconv.Itoa(a.ID)+"/history/"+strconv.Itoa(otherRevID), userID, nil)
	setPath(req, map[string]string{"workspaceId": "1", "pageId": strconv.Itoa(a.ID), "revisionId": strconv.Itoa(otherRevID)})
	rr := httptest.NewRecorder()
	h.GetRevision(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("cross-page revision get: want 404, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPageHandler_Permissions_ReturnsEffectiveLevel(t *testing.T) {
	h, db, _ := newPageHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")

	page, err := h.service.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "T"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	req := authedRequest(http.MethodGet, "/workspaces/1/pages/"+strconv.Itoa(page.ID)+"/permissions", userID, nil)
	setPath(req, map[string]string{"workspaceId": "1", "pageId": strconv.Itoa(page.ID)})
	rr := httptest.NewRecorder()
	h.GetPermissions(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("perms: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp pageEffectivePermissionsResponse
	decodeJSONBody(t, rr, &resp)
	if resp.EffectiveLevel != services.PageOpEdit {
		t.Errorf("effective level: want edit, got %q", resp.EffectiveLevel)
	}
	if !resp.InheritPermissions {
		t.Error("inherit_permissions should default to true on new page")
	}
}

func TestPageHandler_GrantPermission_RejectsEditor(t *testing.T) {
	h, db, _ := newPageHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")
	// Editor lacks page.admin, so ACL writes must 404.

	page, err := h.service.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "P"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	body := map[string]interface{}{
		"principal_type":   "user",
		"principal_id":     999,
		"permission_level": "view",
	}
	req := authedRequest(http.MethodPost, "/workspaces/1/pages/"+strconv.Itoa(page.ID)+"/permissions", userID, body)
	setPath(req, map[string]string{"workspaceId": "1", "pageId": strconv.Itoa(page.ID)})
	rr := httptest.NewRecorder()
	h.GrantPermission(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("editor grant: want 404, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPageHandler_GrantPermission_AdminAllowed(t *testing.T) {
	h, db, _ := newPageHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Administrator")

	page, err := h.service.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "P"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	body := map[string]interface{}{
		"principal_type":   "user",
		"principal_id":     999,
		"permission_level": "edit",
	}
	req := authedRequest(http.MethodPost, "/workspaces/1/pages/"+strconv.Itoa(page.ID)+"/permissions", userID, body)
	setPath(req, map[string]string{"workspaceId": "1", "pageId": strconv.Itoa(page.ID)})
	rr := httptest.NewRecorder()
	h.GrantPermission(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("admin grant: want 201, got %d body=%s", rr.Code, rr.Body.String())
	}

	acl, _ := h.service.ListOwnACL(page.ID)
	if len(acl) != 1 || acl[0].PrincipalID != 999 {
		t.Errorf("expected acl with principal_id=999, got %+v", acl)
	}
}

func TestPageHandler_SetInheritance_RejectsEditor(t *testing.T) {
	h, db, _ := newPageHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")

	page, err := h.service.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "P"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	body := map[string]interface{}{"inherit_permissions": false}
	req := authedRequest(http.MethodPatch, "/workspaces/1/pages/"+strconv.Itoa(page.ID)+"/inheritance", userID, body)
	setPath(req, map[string]string{"workspaceId": "1", "pageId": strconv.Itoa(page.ID)})
	rr := httptest.NewRecorder()
	h.SetInheritance(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("editor break inheritance: want 404, got %d body=%s", rr.Code, rr.Body.String())
	}
	after, _ := h.service.GetByID(page.ID)
	if !after.InheritPermissions {
		t.Error("page inheritance flag should not have flipped on editor's failed attempt")
	}
}

func TestPageHandler_RevokePermission_404OnCrossPageRow(t *testing.T) {
	h, db, _ := newPageHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Administrator")

	a, _ := h.service.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "A"})
	b, _ := h.service.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "B"})
	rowOnA, err := h.service.GrantPermission(userID, a.ID, "user", 999, "view")
	if err != nil {
		t.Fatalf("grant: %v", err)
	}

	req := authedRequest(http.MethodDelete,
		"/workspaces/1/pages/"+strconv.Itoa(b.ID)+"/permissions/"+strconv.Itoa(rowOnA.ID),
		userID, nil)
	setPath(req, map[string]string{
		"workspaceId":  "1",
		"pageId":       strconv.Itoa(b.ID),
		"permissionId": strconv.Itoa(rowOnA.ID),
	})
	rr := httptest.NewRecorder()
	h.RevokePermission(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("cross-page revoke: want 404, got %d body=%s", rr.Code, rr.Body.String())
	}
	acl, _ := h.service.ListOwnACL(a.ID)
	if len(acl) != 1 {
		t.Errorf("row should remain on page A, got %d", len(acl))
	}
}

// newKnowledgeSearchHandler builds a KnowledgeSearchHandler against the
// same wired stack as the page tests: real DB, real PermissionService, real
// PagePermissionService. Returns the page handler too so callers can seed
// pages via the service surface.
func newKnowledgeSearchHandler(t *testing.T) (*KnowledgeSearchHandler, *PageHandler, database.Database) {
	t.Helper()
	pageH, db, _ := newPageHandler(t)
	retrieval := services.NewKnowledgeRetrievalService(db, pageH.pageAuth)
	return NewKnowledgeSearchHandler(retrieval), pageH, db
}

// knowledgeSearchResponse mirrors the wire shape returned by Search.
type knowledgeSearchResponse struct {
	Query   string                     `json:"query"`
	Results []services.KnowledgeResult `json:"results"`
}

func TestKnowledgeSearchHandler_HappyPath(t *testing.T) {
	search, pageH, db := newKnowledgeSearchHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")

	if _, err := pageH.service.Create(userID, services.CreatePageInput{
		WorkspaceID: 1,
		Title:       "Runbook",
		Content:     "# Runbook\n\nDeployment procedure for onboarding new clients.",
	}); err != nil {
		t.Fatalf("seed page: %v", err)
	}

	req := authedRequest(http.MethodGet, "/workspaces/1/knowledge/search?q=onboarding", userID, nil)
	setPath(req, map[string]string{"workspaceId": "1"})
	rr := httptest.NewRecorder()
	search.Search(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp knowledgeSearchResponse
	decodeJSONBody(t, rr, &resp)
	if resp.Query != "onboarding" {
		t.Errorf("query echo: want 'onboarding', got %q", resp.Query)
	}
	if len(resp.Results) == 0 {
		t.Fatalf("expected at least one result, got 0")
	}
	first := resp.Results[0]
	if first.Source != services.KnowledgeSourcePage {
		t.Errorf("source: want %q, got %q", services.KnowledgeSourcePage, first.Source)
	}
	if first.Title != "Runbook" {
		t.Errorf("title: want 'Runbook', got %q", first.Title)
	}
	if first.WorkspaceID != 1 {
		t.Errorf("workspace_id: want 1, got %d", first.WorkspaceID)
	}
	if first.PageID == 0 || first.ChunkID == 0 {
		t.Errorf("page_id and chunk_id must be populated, got page=%d chunk=%d", first.PageID, first.ChunkID)
	}
}

// Empty query returns a 200 with an empty results array (never nil) so the
// frontend doesn't need a nil check.
func TestKnowledgeSearchHandler_EmptyQuery(t *testing.T) {
	search, pageH, db := newKnowledgeSearchHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")
	if _, err := pageH.service.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "X", Content: "body"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := authedRequest(http.MethodGet, "/workspaces/1/knowledge/search?q=", userID, nil)
	setPath(req, map[string]string{"workspaceId": "1"})
	rr := httptest.NewRecorder()
	search.Search(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d, body=%s", rr.Code, rr.Body.String())
	}

	// Decode as a map so we can prove the field is a JSON array, not null —
	// the handler explicitly normalizes nil to [] for this reason.
	var raw map[string]json.RawMessage
	decodeJSONBody(t, rr, &raw)
	if string(raw["results"]) != "[]" {
		t.Errorf("empty-query results should serialize as [], got %s", string(raw["results"]))
	}
}

// limit query-string is parsed by parseOffsetPagination (cap=100). Values
// above the cap or non-numeric fall back to the default. The service caps
// again at 100. A valid limit between 1 and 100 is honored end-to-end.
func TestKnowledgeSearchHandler_LimitParsing(t *testing.T) {
	search, pageH, db := newKnowledgeSearchHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")

	// Seed enough pages to exercise the limit. Each page has a heading-only
	// markdown body so it produces exactly one chunk.
	const total = 30
	for i := 0; i < total; i++ {
		title := fmt.Sprintf("Page %02d alpha", i)
		body := fmt.Sprintf("# %s\n\nbody mentions alpha keyword %d.", title, i)
		if _, err := pageH.service.Create(userID, services.CreatePageInput{
			WorkspaceID: 1,
			Title:       title,
			Content:     body,
		}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	for _, tc := range []struct {
		name    string
		limit   string
		wantMax int
	}{
		{"valid limit honored", "5", 5},
		{"above cap falls back to default 25", "1000", 25},
		{"non-numeric falls back to default 25", "abc", 25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url := "/workspaces/1/knowledge/search?q=alpha&limit=" + tc.limit
			req := authedRequest(http.MethodGet, url, userID, nil)
			setPath(req, map[string]string{"workspaceId": "1"})
			rr := httptest.NewRecorder()
			search.Search(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("status: want 200, got %d body=%s", rr.Code, rr.Body.String())
			}
			var resp knowledgeSearchResponse
			decodeJSONBody(t, rr, &resp)
			if len(resp.Results) != tc.wantMax {
				t.Errorf("limit=%s: want %d results, got %d", tc.limit, tc.wantMax, len(resp.Results))
			}
		})
	}
}

// A user without any role in the target workspace gets an empty result set
// even when chunks matching the query exist there — the per-page permission
// re-check denies every hit. Workspaces stay in "open" mode until a Viewer
// role is assigned, so we seed a phantom Viewer to gate workspace 2.
func TestKnowledgeSearchHandler_RespectsWorkspaceBoundary(t *testing.T) {
	search, pageH, db := newKnowledgeSearchHandler(t)
	const userID = 1
	const phantomAdmin = 999
	const phantomViewer = 998
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, phantomAdmin)
	seedNegativeTestUser(t, db, phantomViewer)
	// userID is the Editor of workspace 1; workspace 2 is gated by an
	// explicit Viewer role distinct from userID, so userID has no access.
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")
	seedWorkspaceWithRole(t, db, 2, phantomAdmin, "Administrator")
	assignWorkspaceRole(t, db, 2, phantomViewer, "Viewer")

	// Seed a page in workspace 2 the userID has no role in.
	if _, err := pageH.service.Create(phantomAdmin, services.CreatePageInput{
		WorkspaceID: 2,
		Title:       "Cross-ws secret",
		Content:     "alpha bravo charlie",
	}); err != nil {
		t.Fatalf("seed ws2: %v", err)
	}

	req := authedRequest(http.MethodGet, "/workspaces/2/knowledge/search?q=alpha", userID, nil)
	setPath(req, map[string]string{"workspaceId": "2"})
	rr := httptest.NewRecorder()
	search.Search(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp knowledgeSearchResponse
	decodeJSONBody(t, rr, &resp)
	if len(resp.Results) != 0 {
		t.Errorf("non-member should see zero hits, got %+v", resp.Results)
	}
}

// A restricted page (inherit_permissions=false, no ACL granting view) must
// not surface through the HTTP search endpoint even though the workspace
// Viewer would normally see open pages. The service-level test covers the
// same invariant; this test pins it at the HTTP layer where the wiring
// between handler, retrieval service, and permission evaluator lives.
func TestKnowledgeSearchHandler_SuppressesRestrictedPages(t *testing.T) {
	search, pageH, db := newKnowledgeSearchHandler(t)
	const viewerID = 1
	const adminID = 999
	seedNegativeTestUser(t, db, viewerID)
	seedNegativeTestUser(t, db, adminID)
	// seedWorkspaceWithRole gives viewerID the Viewer role and auto-seeds
	// uid 999 as Administrator to gate the workspace — so adminID is
	// already wired up as the admin without a second assignment.
	seedWorkspaceWithRole(t, db, 1, viewerID, "Viewer")

	// Admin creates an open page and a restricted (inherit=false) page,
	// both with the same matching keyword.
	if _, err := pageH.service.Create(adminID, services.CreatePageInput{
		WorkspaceID: 1, Title: "Open doc", Content: "openzebra term in open doc",
	}); err != nil {
		t.Fatalf("open page: %v", err)
	}
	restricted, err := pageH.service.Create(adminID, services.CreatePageInput{
		WorkspaceID: 1, Title: "Restricted", Content: "openzebra term in restricted doc",
	})
	if err != nil {
		t.Fatalf("restricted: %v", err)
	}
	if _, err := pageH.service.SetInheritPermissions(adminID, restricted.ID, false); err != nil {
		t.Fatalf("break inheritance: %v", err)
	}

	// Viewer sees the open one but not the restricted one.
	req := authedRequest(http.MethodGet, "/workspaces/1/knowledge/search?q=openzebra", viewerID, nil)
	setPath(req, map[string]string{"workspaceId": "1"})
	rr := httptest.NewRecorder()
	search.Search(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp knowledgeSearchResponse
	decodeJSONBody(t, rr, &resp)
	for _, r := range resp.Results {
		if r.PageID == restricted.ID {
			t.Errorf("restricted page %d must not appear in viewer's results, got %+v", restricted.ID, resp.Results)
		}
	}
	if len(resp.Results) == 0 {
		t.Error("viewer should still see the open page hit")
	}
}

// Unauthenticated requests are rejected by RequireAuth before reaching the
// retrieval service. Use a plain httptest.NewRequest (no user-in-context).
func TestKnowledgeSearchHandler_Unauthenticated_401(t *testing.T) {
	search, _, _ := newKnowledgeSearchHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/workspaces/1/knowledge/search?q=foo", nil)
	req.SetPathValue("workspaceId", "1")
	rr := httptest.NewRecorder()
	search.Search(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: want 401, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPageHandler_ListArchived_AdminAllowed(t *testing.T) {
	h, db, _ := newPageHandler(t)
	const adminID = 1
	seedNegativeTestUser(t, db, adminID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, adminID, "Administrator")

	page, err := h.service.Create(adminID, services.CreatePageInput{WorkspaceID: 1, Title: "Doomed", Content: "body"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := h.service.Archive(adminID, page.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}

	req := authedRequest(http.MethodGet, "/workspaces/1/pages/archived", adminID, nil)
	setPath(req, map[string]string{"workspaceId": "1"})
	rr := httptest.NewRecorder()
	h.ListArchived(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var rows []archivedPageResponse
	decodeJSONBody(t, rr, &rows)
	if len(rows) != 1 || rows[0].ID != page.ID {
		t.Fatalf("expected single archived row for page %d, got %+v", page.ID, rows)
	}
	if rows[0].Title != "Doomed" {
		t.Errorf("title: want Doomed, got %q", rows[0].Title)
	}
	if rows[0].ArchivedAt.IsZero() {
		t.Error("archived_at should be set")
	}
	if rows[0].ArchivedBy == nil || *rows[0].ArchivedBy != adminID {
		t.Errorf("archived_by: want %d, got %v", adminID, rows[0].ArchivedBy)
	}
	if rows[0].ArchivedByName != "Neg User" {
		t.Errorf("archived_by_name: want %q, got %q", "Neg User", rows[0].ArchivedByName)
	}
}

func TestPageHandler_ListArchived_RejectsNonAdmin(t *testing.T) {
	h, db, _ := newPageHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")

	// Admin archives a page so the list isn't trivially empty.
	page, err := h.service.Create(999, services.CreatePageInput{WorkspaceID: 1, Title: "Hidden"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := h.service.Archive(999, page.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}

	req := authedRequest(http.MethodGet, "/workspaces/1/pages/archived", userID, nil)
	setPath(req, map[string]string{"workspaceId": "1"})
	rr := httptest.NewRecorder()
	h.ListArchived(rr, req)

	// 404 not 403 — workspace-resource access checks must not leak
	// existence (project_workspace_permissions_open_default).
	if rr.Code != http.StatusNotFound {
		t.Errorf("editor list-archived: want 404, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPageHandler_Unarchive_ClearsFieldsAndWritesRevision(t *testing.T) {
	h, db, _ := newPageHandler(t)
	const adminID = 1
	seedNegativeTestUser(t, db, adminID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, adminID, "Administrator")

	page, err := h.service.Create(adminID, services.CreatePageInput{WorkspaceID: 1, Title: "Bring me back", Content: "v1"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	originalContent := page.Content
	if err := h.service.Archive(adminID, page.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	// Sanity: archive set the field.
	if archived, _ := h.service.GetByID(page.ID); archived.ArchivedAt == nil {
		t.Fatalf("archive did not set archived_at")
	}

	req := authedRequest(http.MethodPost, "/workspaces/1/pages/"+strconv.Itoa(page.ID)+"/unarchive", adminID, nil)
	setPath(req, map[string]string{"workspaceId": "1", "pageId": strconv.Itoa(page.ID)})
	rr := httptest.NewRecorder()
	h.Unarchive(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	got, err := h.service.GetByID(page.ID)
	if err != nil {
		t.Fatalf("re-fetch: %v", err)
	}
	if got.ArchivedAt != nil {
		t.Errorf("archived_at: want nil after unarchive, got %v", got.ArchivedAt)
	}
	if got.ArchivedBy != nil {
		t.Errorf("archived_by: want nil after unarchive, got %v", got.ArchivedBy)
	}
	if got.Content != originalContent {
		t.Errorf("content must NOT be overwritten by unarchive: want %q, got %q", originalContent, got.Content)
	}

	// History should now include an archive entry and the new "unarchived" restore entry.
	revs, err := h.service.ListRevisions(page.ID, 50, 0)
	if err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	var sawUnarchive bool
	for _, rev := range revs {
		if rev.ChangeType == models.PageRevisionChangeTypeRestore && rev.ChangeSummary == "unarchived" {
			sawUnarchive = true
			break
		}
	}
	if !sawUnarchive {
		t.Errorf("expected revision with change_type=restore summary=\"unarchived\", got %+v", revs)
	}
}

func TestPageHandler_Unarchive_RejectsNonAdmin(t *testing.T) {
	h, db, _ := newPageHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")

	page, err := h.service.Create(999, services.CreatePageInput{WorkspaceID: 1, Title: "Stays archived"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := h.service.Archive(999, page.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}

	req := authedRequest(http.MethodPost, fmt.Sprintf("/workspaces/1/pages/%d/unarchive", page.ID), userID, nil)
	setPath(req, map[string]string{"workspaceId": "1", "pageId": strconv.Itoa(page.ID)})
	rr := httptest.NewRecorder()
	h.Unarchive(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("editor unarchive: want 404, got %d body=%s", rr.Code, rr.Body.String())
	}
	got, _ := h.service.GetByID(page.ID)
	if got.ArchivedAt == nil {
		t.Error("page must remain archived when non-admin attempts unarchive")
	}
}

// Ensure JSON encoding stays stable across the handler boundary so the
// frontend can rely on these fields. A pure structural check.
func TestPageHandler_GetTree_JSONShape(t *testing.T) {
	h, db, _ := newPageHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")

	parent, _ := h.service.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "Parent"})
	_, _ = h.service.Create(userID, services.CreatePageInput{WorkspaceID: 1, ParentID: &parent.ID, Title: "Child"})

	req := authedRequest(http.MethodGet, "/workspaces/1/pages/tree", userID, nil)
	setPath(req, map[string]string{"workspaceId": "1"})
	rr := httptest.NewRecorder()
	h.GetTree(rr, req)

	var raw map[string]json.RawMessage
	decodeJSONBody(t, rr, &raw)
	if _, ok := raw["pages"]; !ok {
		t.Errorf("response missing pages field, got %v", raw)
	}
	if _, ok := raw["tree"]; !ok {
		t.Errorf("response missing tree field, got %v", raw)
	}
}

// TestPageHandler_GetTree_OmitsBody is the WI-407 regression guard: the tree
// endpoint renders titles + hierarchy only, so the heavy body fields must
// never ride along in the payload (they were previously shipped twice over,
// once per Page in the flat slice and again in the nested tree). The fix
// projects content out of the query entirely; this asserts the wire shape
// stays body-free even though the seeded pages have non-empty content.
func TestPageHandler_GetTree_OmitsBody(t *testing.T) {
	h, db, _ := newPageHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")

	parent, err := h.service.Create(userID, services.CreatePageInput{
		WorkspaceID: 1, Title: "Parent", Content: "# Heavy parent body\n\nlots of text",
	})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if _, err := h.service.Create(userID, services.CreatePageInput{
		WorkspaceID: 1, ParentID: &parent.ID, Title: "Child", Content: "# Heavy child body",
	}); err != nil {
		t.Fatalf("create child: %v", err)
	}

	req := authedRequest(http.MethodGet, "/workspaces/1/pages/tree", userID, nil)
	setPath(req, map[string]string{"workspaceId": "1"})
	rr := httptest.NewRecorder()
	h.GetTree(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp pageTreeResponse
	decodeJSONBody(t, rr, &resp)
	if len(resp.Pages) != 2 {
		t.Fatalf("want 2 pages, got %d", len(resp.Pages))
	}

	// Flat slice: titles present, bodies stripped.
	for _, p := range resp.Pages {
		if p.Title == "" {
			t.Errorf("page %d: title must survive, got empty", p.ID)
		}
		if p.Content != "" || p.ContentHash != "" || p.Excerpt != "" {
			t.Errorf("page %d: body fields must be empty, got content=%q hash=%q excerpt=%q",
				p.ID, p.Content, p.ContentHash, p.Excerpt)
		}
	}

	// Nested tree (PageNode embeds Page) must be body-free too.
	var walk func(nodes []*models.PageNode)
	walk = func(nodes []*models.PageNode) {
		for _, n := range nodes {
			if n.Content != "" || n.ContentHash != "" || n.Excerpt != "" {
				t.Errorf("tree node %d: body fields must be empty, got content=%q hash=%q excerpt=%q",
					n.ID, n.Content, n.ContentHash, n.Excerpt)
			}
			walk(n.Children)
		}
	}
	walk(resp.Tree)
}
