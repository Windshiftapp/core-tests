package handlers

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"windshift/internal/contextkeys"
	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/services"
)

// Tests for the entity_type='page' branches of AttachmentHandler.Download
// and AttachmentHandler.Delete. The legacy attachment_test.go uses a
// hand-rolled minimal schema and a permission-less handler; pages
// integration needs the full Initialize() schema (pages, workspace_roles,
// user_workspace_roles, …) and the wired PagePermissionService.

// newPageAttachmentHandler builds an AttachmentHandler against the full
// schema with the PagePermissionService wired so entity_type='page'
// downloads/deletes go through the page ACL evaluator. Returns the
// PageService too so tests can seed pages through the same surface the
// production code uses.
func newPageAttachmentHandler(t *testing.T) (*AttachmentHandler, *services.PageService, database.Database) {
	t.Helper()
	db := newNegativeTestDB(t)
	perm := newNegativeTestPermissionService(t, db)
	pageSvc := services.NewPageService(db)
	pageAuth := services.NewPagePermissionService(db, perm)
	h := &AttachmentHandler{
		db:                    db,
		permissionService:     perm,
		attachmentPath:        t.TempDir(),
		attachmentService:     services.NewAttachmentServiceWithPermissions(db, perm),
		pagePermissionService: pageAuth,
	}
	return h, pageSvc, db
}

// insertPageAttachment seeds one attachment row pointing at the given
// page id (entity_type='page', item_id=pageID). Returns the attachment
// id. The file path is recorded but no file is written — Delete tolerates
// missing files (os.Remove → IsNotExist → silent), and the negative
// (auth-rejection) Download paths short-circuit before file IO.
func insertPageAttachment(t *testing.T, db database.Database, pageID, uploaderID int) int {
	t.Helper()
	res, err := db.Exec(`
		INSERT INTO attachments
			(item_id, entity_type, filename, original_filename, file_path,
			 mime_type, file_size, uploaded_by, thumbnail_path)
		VALUES (?, 'page', ?, ?, ?, 'text/plain', 12, ?, NULL)
	`, pageID, "page-attach.txt", "page-attach.txt",
		"pages/"+strconv.Itoa(pageID)+"/page-attach.txt", uploaderID)
	if err != nil {
		t.Fatalf("seed page attachment: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return int(id)
}

func TestAttachmentDelete_Page_RejectsViewer(t *testing.T) {
	h, pageSvc, db := newPageAttachmentHandler(t)
	const viewerID = 1
	seedNegativeTestUser(t, db, viewerID)
	seedNegativeTestUser(t, db, 999)
	// Viewer role in a gated workspace.
	seedWorkspaceWithRole(t, db, 1, viewerID, "Viewer")

	page, err := pageSvc.Create(999, services.CreatePageInput{
		WorkspaceID: 1, Title: "Docs", Content: "body",
	})
	if err != nil {
		t.Fatalf("seed page: %v", err)
	}
	attID := insertPageAttachment(t, db, page.ID, 999)

	req := authedRequest(http.MethodDelete, "/attachments/"+strconv.Itoa(attID), viewerID, nil)
	req.SetPathValue("id", strconv.Itoa(attID))
	rec := httptest.NewRecorder()
	h.Delete(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("viewer delete: want 404, got %d body=%s", rec.Code, rec.Body.String())
	}
	// And the row must still exist.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE id = ?`, attID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("row should remain after rejected delete, got %d", n)
	}
}

func TestAttachmentDelete_Page_AllowsEditor(t *testing.T) {
	h, pageSvc, db := newPageAttachmentHandler(t)
	const editorID = 1
	seedNegativeTestUser(t, db, editorID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, editorID, "Editor")

	page, err := pageSvc.Create(editorID, services.CreatePageInput{
		WorkspaceID: 1, Title: "Docs", Content: "body",
	})
	if err != nil {
		t.Fatalf("seed page: %v", err)
	}
	attID := insertPageAttachment(t, db, page.ID, editorID)

	req := authedRequest(http.MethodDelete, "/attachments/"+strconv.Itoa(attID), editorID, nil)
	req.SetPathValue("id", strconv.Itoa(attID))
	rec := httptest.NewRecorder()
	h.Delete(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("editor delete: want 204, got %d body=%s", rec.Code, rec.Body.String())
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE id = ?`, attID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("row should be gone after delete, got %d", n)
	}
}

// A restricted page (inherit_permissions=false, no ACL grant) only
// admins can edit. A workspace Editor without an explicit grant should
// be denied with 404 — the security invariant the page-permission
// branch in attachment.go was added for.
func TestAttachmentDelete_Page_RestrictedPageRejectsEditor(t *testing.T) {
	h, pageSvc, db := newPageAttachmentHandler(t)
	const editorID = 1
	const adminID = 999
	seedNegativeTestUser(t, db, editorID)
	seedNegativeTestUser(t, db, adminID)
	// Workspace gating: editorID is Editor, phantom 999 is Admin.
	seedWorkspaceWithRole(t, db, 1, editorID, "Editor")

	page, err := pageSvc.Create(adminID, services.CreatePageInput{
		WorkspaceID: 1, Title: "Confidential", Content: "secret",
	})
	if err != nil {
		t.Fatalf("seed page: %v", err)
	}
	if _, err := pageSvc.SetInheritPermissions(adminID, page.ID, false); err != nil {
		t.Fatalf("break inheritance: %v", err)
	}
	attID := insertPageAttachment(t, db, page.ID, adminID)

	req := authedRequest(http.MethodDelete, "/attachments/"+strconv.Itoa(attID), editorID, nil)
	req.SetPathValue("id", strconv.Itoa(attID))
	rec := httptest.NewRecorder()
	h.Delete(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("editor on restricted page: want 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// Download authorization: a non-member of the workspace must get 404,
// no different from a missing attachment row. Page-view permission is
// the gate; we deliberately don't write a file to disk because the
// auth check short-circuits before file IO.
func TestAttachmentDownload_Page_RejectsNonMember(t *testing.T) {
	h, pageSvc, db := newPageAttachmentHandler(t)
	const strangerID = 1
	const adminID = 999
	seedNegativeTestUser(t, db, strangerID)
	seedNegativeTestUser(t, db, adminID)
	// Workspace 1 has only adminID as a member (via the phantom-admin
	// auto-seed in seedWorkspaceWithRole when userID == 999).
	seedWorkspaceWithRole(t, db, 1, adminID, "Administrator")

	page, err := pageSvc.Create(adminID, services.CreatePageInput{
		WorkspaceID: 1, Title: "Docs", Content: "body",
	})
	if err != nil {
		t.Fatalf("seed page: %v", err)
	}
	attID := insertPageAttachment(t, db, page.ID, adminID)

	req := authedRequest(http.MethodGet, "/attachments/"+strconv.Itoa(attID)+"/download", strangerID, nil)
	req.SetPathValue("id", strconv.Itoa(attID))
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("non-member download: want 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// Download on a restricted page (inherit=false, no ACL): a workspace
// Viewer with no explicit grant must be rejected with 404. Pins the
// per-page ACL gate on the download path (the service-level test for
// the same invariant lives in services/page_permission_service_test.go).
func TestAttachmentDownload_Page_RestrictedPageRejectsViewer(t *testing.T) {
	h, pageSvc, db := newPageAttachmentHandler(t)
	const viewerID = 1
	const adminID = 999
	seedNegativeTestUser(t, db, viewerID)
	seedNegativeTestUser(t, db, adminID)
	seedWorkspaceWithRole(t, db, 1, viewerID, "Viewer")

	page, err := pageSvc.Create(adminID, services.CreatePageInput{
		WorkspaceID: 1, Title: "Confidential", Content: "secret",
	})
	if err != nil {
		t.Fatalf("seed page: %v", err)
	}
	if _, err := pageSvc.SetInheritPermissions(adminID, page.ID, false); err != nil {
		t.Fatalf("break inheritance: %v", err)
	}
	attID := insertPageAttachment(t, db, page.ID, adminID)

	req := authedRequest(http.MethodGet, "/attachments/"+strconv.Itoa(attID)+"/download", viewerID, nil)
	req.SetPathValue("id", strconv.Itoa(attID))
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("viewer on restricted page: want 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// A NULL item_id on an entity_type='page' row is treated as a
// missing/corrupt row and rejected. This mirrors the WI-46 invariant
// for items and keeps the page branch defensive.
func TestAttachmentDownload_Page_NullItemIDRejected(t *testing.T) {
	h, _, db := newPageAttachmentHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")

	// Insert directly — service won't let us, so go around it.
	res, err := db.Exec(`
		INSERT INTO attachments
			(item_id, entity_type, filename, original_filename, file_path,
			 mime_type, file_size, uploaded_by, thumbnail_path)
		VALUES (NULL, 'page', 'x.txt', 'x.txt', 'pages/x.txt', 'text/plain', 1, ?, NULL)
	`, userID)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	id64, _ := res.LastInsertId()

	req := authedRequest(http.MethodGet, "/attachments/"+strconv.FormatInt(id64, 10)+"/download", userID, nil)
	req.SetPathValue("id", strconv.FormatInt(id64, 10))
	rec := httptest.NewRecorder()
	h.Download(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("null item_id page attachment: want 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestAttachmentUpload_Page_AcceptsJSONForDiagram pins the JSON-content
// rescue in verifyFileContentFromBytes: page diagrams upload Excalidraw
// scenes as application/json blobs, which http.DetectContentType reports
// as text/plain. The verifier must allow this when the extension is .json
// and the body parses as JSON.
func TestAttachmentUpload_Page_AcceptsJSONForDiagram(t *testing.T) {
	h, pageSvc, db := newPageAttachmentHandler(t)
	const editorID = 1
	seedNegativeTestUser(t, db, editorID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, editorID, "Editor")

	page, err := pageSvc.Create(editorID, services.CreatePageInput{
		WorkspaceID: 1, Title: "Diagrams", Content: "body",
	})
	if err != nil {
		t.Fatalf("seed page: %v", err)
	}

	scene := []byte(`{"elements":[],"appState":{},"files":{}}`)
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	if err := mw.WriteField("entity_type", "page"); err != nil {
		t.Fatalf("write entity_type: %v", err)
	}
	if err := mw.WriteField("entity_id", strconv.Itoa(page.ID)); err != nil {
		t.Fatalf("write entity_id: %v", err)
	}
	fw, err := mw.CreateFormFile("file", "diagram.json")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(scene); err != nil {
		t.Fatalf("write scene: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close mw: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/attachments/upload", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	user := &models.User{ID: editorID, Email: "neg@example.com", Username: "neguser", IsActive: true}
	req = req.WithContext(context.WithValue(req.Context(), contextkeys.User, user))
	rec := httptest.NewRecorder()
	h.Upload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("upload diagram: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var n int
	var mimeType string
	if err := db.QueryRow(`
		SELECT COUNT(*), COALESCE(MAX(mime_type), '')
		FROM attachments WHERE item_id = ? AND entity_type = 'page'
	`, page.ID).Scan(&n, &mimeType); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if n != 1 {
		t.Fatalf("attachments row count: want 1, got %d", n)
	}
	if mimeType != "application/json" {
		t.Errorf("attachment mime: want application/json, got %q", mimeType)
	}
}
