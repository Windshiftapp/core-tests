package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"windshift/internal/database"
	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
)

// newPageLabelHandler wires a fully-initialized SQLite DB, the real
// PermissionService, and the page services into a PageLabelHandler suitable
// for integration tests. Also returns a PageHandler so tests can seed pages.
func newPageLabelHandler(t *testing.T) (*PageLabelHandler, *PageHandler, database.Database) {
	t.Helper()
	db := newNegativeTestDB(t)
	perm := newNegativeTestPermissionService(t, db)
	auth := services.NewPagePermissionService(db, perm)
	pageRepo := repository.NewPageLabelRepository(db)
	pageSvc := services.NewPageService(db)
	pageSvc.SetPageLabelRepository(pageRepo)
	pageHandler := NewPageHandler(pageSvc, auth, perm, logger.NewAuditor(db))
	labelHandler := NewPageLabelHandler(pageRepo, auth, logger.NewAuditor(db))
	return labelHandler, pageHandler, db
}

func TestPageLabels_Create_RejectsViewer(t *testing.T) {
	h, _, db := newPageLabelHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Viewer")

	body := map[string]interface{}{"name": "design", "color": "#3B82F6"}
	req := authedRequest(http.MethodPost, "/workspaces/1/page-labels", userID, body)
	setPath(req, map[string]string{"workspaceId": "1"})
	rr := httptest.NewRecorder()
	h.Create(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("viewer create: want 404 (no leak), got %d body=%s", rr.Code, rr.Body.String())
	}

	labels, err := h.repo.ListByWorkspace(1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(labels) != 0 {
		t.Errorf("viewer should not have created any labels, got %d", len(labels))
	}
}

func TestPageLabels_Create_EditorSucceeds(t *testing.T) {
	h, _, db := newPageLabelHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")

	body := map[string]interface{}{"name": "design"}
	req := authedRequest(http.MethodPost, "/workspaces/1/page-labels", userID, body)
	setPath(req, map[string]string{"workspaceId": "1"})
	rr := httptest.NewRecorder()
	h.Create(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("editor create: want 201, got %d body=%s", rr.Code, rr.Body.String())
	}
	var created models.PageLabel
	decodeJSONBody(t, rr, &created)
	if created.Name != "design" || created.Color != "#3B82F6" || created.WorkspaceID != 1 {
		t.Errorf("unexpected label payload: %+v", created)
	}
}

func TestPageLabels_Get_404ForCrossWorkspace(t *testing.T) {
	h, _, db := newPageLabelHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")
	seedWorkspaceWithRole(t, db, 2, 999, "Administrator")

	id, _, err := h.repo.Create("secret", "#FF0000", 2)
	if err != nil {
		t.Fatalf("seed label: %v", err)
	}

	// Requesting workspace 1's URL for a workspace-2 label must 404 so
	// existence doesn't leak across workspaces.
	req := authedRequest(http.MethodGet, "/workspaces/1/page-labels/"+strconv.Itoa(id), userID, nil)
	setPath(req, map[string]string{"workspaceId": "1", "labelId": strconv.Itoa(id)})
	rr := httptest.NewRecorder()
	h.Get(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("cross-workspace get: want 404, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPageLabels_Delete_CascadesAssignments(t *testing.T) {
	h, pageHandler, db := newPageLabelHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")

	page, err := pageHandler.service.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "Home"})
	if err != nil {
		t.Fatalf("seed page: %v", err)
	}
	labelID, _, err := h.repo.Create("urgent", "#FF0000", 1)
	if err != nil {
		t.Fatalf("seed label: %v", err)
	}
	if err := h.repo.AddAssignment(page.ID, labelID); err != nil {
		t.Fatalf("attach label: %v", err)
	}

	// Sanity: assignment present before delete.
	attached, _ := h.repo.ListForPage(page.ID)
	if len(attached) != 1 {
		t.Fatalf("setup: want 1 attached label, got %d", len(attached))
	}

	req := authedRequest(http.MethodDelete, "/workspaces/1/page-labels/"+strconv.Itoa(labelID), userID, nil)
	setPath(req, map[string]string{"workspaceId": "1", "labelId": strconv.Itoa(labelID)})
	rr := httptest.NewRecorder()
	h.Delete(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d body=%s", rr.Code, rr.Body.String())
	}

	// FK CASCADE on the junction table removes the assignment row.
	after, _ := h.repo.ListForPage(page.ID)
	if len(after) != 0 {
		t.Errorf("delete should cascade junction: want 0 assignments, got %d", len(after))
	}
}

func TestPageLabels_AttachDetach_PerPagePermission(t *testing.T) {
	h, pageHandler, db := newPageLabelHandler(t)
	const editorID = 1
	const viewerID = 2
	seedNegativeTestUser(t, db, editorID)
	seedNegativeTestUser(t, db, viewerID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, editorID, "Editor")
	assignWorkspaceRole(t, db, 1, viewerID, "Viewer")

	page, err := pageHandler.service.Create(editorID, services.CreatePageInput{WorkspaceID: 1, Title: "Home"})
	if err != nil {
		t.Fatalf("seed page: %v", err)
	}
	labelID, _, err := h.repo.Create("design", "#3B82F6", 1)
	if err != nil {
		t.Fatalf("seed label: %v", err)
	}

	// Viewer cannot attach.
	body := map[string]interface{}{"label_id": labelID}
	req := authedRequest(http.MethodPost, "/workspaces/1/pages/"+strconv.Itoa(page.ID)+"/labels", viewerID, body)
	setPath(req, map[string]string{"workspaceId": "1", "pageId": strconv.Itoa(page.ID)})
	rr := httptest.NewRecorder()
	h.AddToPage(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("viewer attach: want 404, got %d body=%s", rr.Code, rr.Body.String())
	}

	// Editor can attach.
	req = authedRequest(http.MethodPost, "/workspaces/1/pages/"+strconv.Itoa(page.ID)+"/labels", editorID, body)
	setPath(req, map[string]string{"workspaceId": "1", "pageId": strconv.Itoa(page.ID)})
	rr = httptest.NewRecorder()
	h.AddToPage(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("editor attach: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var attached []models.PageLabel
	decodeJSONBody(t, rr, &attached)
	if len(attached) != 1 || attached[0].ID != int(labelID) {
		t.Errorf("unexpected attached payload: %+v", attached)
	}
}

func TestPageLabels_AttachRejectsCrossWorkspaceLabel(t *testing.T) {
	h, pageHandler, db := newPageLabelHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")
	seedWorkspaceWithRole(t, db, 2, 999, "Administrator")

	page, err := pageHandler.service.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "Home"})
	if err != nil {
		t.Fatalf("seed page: %v", err)
	}
	// Label lives in workspace 2.
	otherLabelID, _, err := h.repo.Create("other", "#FF0000", 2)
	if err != nil {
		t.Fatalf("seed label: %v", err)
	}

	body := map[string]interface{}{"label_id": otherLabelID}
	req := authedRequest(http.MethodPost, "/workspaces/1/pages/"+strconv.Itoa(page.ID)+"/labels", userID, body)
	setPath(req, map[string]string{"workspaceId": "1", "pageId": strconv.Itoa(page.ID)})
	rr := httptest.NewRecorder()
	h.AddToPage(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("cross-workspace attach: want 404, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPageLabels_List_RequiresWorkspaceView(t *testing.T) {
	h, _, db := newPageLabelHandler(t)
	const userID = 1
	const outsiderID = 2
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, outsiderID)
	seedNegativeTestUser(t, db, 999)
	// Workspaces are "Viewer-open" until Viewer is explicitly assigned;
	// without an explicit Viewer grant the outsider implicitly inherits
	// page.view and the 404-not-403 check is vacuous. Assigning Viewer to
	// the phantom admin closes Viewer so the outsider genuinely has no grant.
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")
	assignWorkspaceRole(t, db, 1, 999, "Viewer")

	if _, _, err := h.repo.Create("design", "#3B82F6", 1); err != nil {
		t.Fatalf("seed label: %v", err)
	}

	// Outsider with no role on workspace 1 gets 404, not the label list.
	req := authedRequest(http.MethodGet, "/workspaces/1/page-labels", outsiderID, nil)
	setPath(req, map[string]string{"workspaceId": "1"})
	rr := httptest.NewRecorder()
	h.List(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("outsider list: want 404, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPageLabels_NameUniquenessPerWorkspace(t *testing.T) {
	h, _, db := newPageLabelHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")

	body := map[string]interface{}{"name": "design"}
	req := authedRequest(http.MethodPost, "/workspaces/1/page-labels", userID, body)
	setPath(req, map[string]string{"workspaceId": "1"})
	rr := httptest.NewRecorder()
	h.Create(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("first create: want 201, got %d", rr.Code)
	}

	req = authedRequest(http.MethodPost, "/workspaces/1/page-labels", userID, body)
	setPath(req, map[string]string{"workspaceId": "1"})
	rr = httptest.NewRecorder()
	h.Create(rr, req)
	if rr.Code != http.StatusConflict {
		t.Errorf("duplicate create: want 409, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPageLabels_PreloadLabelsOnPageDetail(t *testing.T) {
	h, pageHandler, db := newPageLabelHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")

	page, err := pageHandler.service.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "Home"})
	if err != nil {
		t.Fatalf("seed page: %v", err)
	}
	labelID, _, err := h.repo.Create("design", "#3B82F6", 1)
	if err != nil {
		t.Fatalf("seed label: %v", err)
	}
	if err := h.repo.AddAssignment(page.ID, int(labelID)); err != nil {
		t.Fatalf("attach: %v", err)
	}

	req := authedRequest(http.MethodGet, "/workspaces/1/pages/"+strconv.Itoa(page.ID), userID, nil)
	setPath(req, map[string]string{"workspaceId": "1", "pageId": strconv.Itoa(page.ID)})
	rr := httptest.NewRecorder()
	pageHandler.Get(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("page get: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	// The page-detail response must include the attached label so the
	// frontend doesn't need a second round-trip to render chips.
	var resp struct {
		Labels []models.PageLabel `json:"labels"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Labels) != 1 || resp.Labels[0].Name != "design" {
		t.Errorf("preload missing: %+v", resp.Labels)
	}
}

// Bug-hunt #5: SetForPage used to 500 when a client submitted the same
// label id twice, because the junction's UNIQUE(page_id, page_label_id)
// rejected the duplicate INSERT and the wrapped error reached the
// handler unchanged. ReplaceAssignments now treats the input as a set,
// so the request succeeds and the attached labels list is
// deterministic.
func TestPageLabels_SetForPage_DedupesDuplicateIDs(t *testing.T) {
	h, pageHandler, db := newPageLabelHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")

	page, err := pageHandler.service.Create(userID, services.CreatePageInput{WorkspaceID: 1, Title: "Home"})
	if err != nil {
		t.Fatalf("seed page: %v", err)
	}
	labelID, _, err := h.repo.Create("design", "#3B82F6", 1)
	if err != nil {
		t.Fatalf("seed label: %v", err)
	}

	body := map[string]interface{}{"label_ids": []int{int(labelID), int(labelID)}}
	req := authedRequest(http.MethodPut, "/workspaces/1/pages/"+strconv.Itoa(page.ID)+"/labels", userID, body)
	setPath(req, map[string]string{"workspaceId": "1", "pageId": strconv.Itoa(page.ID)})
	rr := httptest.NewRecorder()
	h.SetForPage(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("set: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var attached []models.PageLabel
	decodeJSONBody(t, rr, &attached)
	if len(attached) != 1 || attached[0].ID != int(labelID) {
		t.Errorf("expected exactly one attached label, got %+v", attached)
	}
}

// Bug-hunt #6: handlers pre-check label-name uniqueness via
// NameExistsInWorkspace, but the check is racy across concurrent
// requests — the loser of the race hits the DB UNIQUE(workspace_id,
// name) constraint directly. Repository must translate that to
// ErrDuplicateEntry so the handler responds 409 instead of 500.
func TestPageLabelRepository_Create_TranslatesUniqueViolation(t *testing.T) {
	h, _, db := newPageLabelHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")

	if _, _, err := h.repo.Create("design", "#3B82F6", 1); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, _, err := h.repo.Create("design", "#3B82F6", 1)
	if !errors.Is(err, repository.ErrDuplicateEntry) {
		t.Errorf("second create: want ErrDuplicateEntry, got %v", err)
	}
}

// Bug-hunt #6 (rename half): repository.Update must surface the
// workspace-name uniqueness constraint as ErrDuplicateEntry too.
func TestPageLabelRepository_Update_TranslatesUniqueViolation(t *testing.T) {
	h, _, db := newPageLabelHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")

	idA, _, err := h.repo.Create("design", "#3B82F6", 1)
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	if _, _, err := h.repo.Create("eng", "#22C55E", 1); err != nil {
		t.Fatalf("create B: %v", err)
	}
	// Rename A onto B's name.
	err = h.repo.Update(idA, "eng", "#3B82F6")
	if !errors.Is(err, repository.ErrDuplicateEntry) {
		t.Errorf("rename onto sibling name: want ErrDuplicateEntry, got %v", err)
	}
}
