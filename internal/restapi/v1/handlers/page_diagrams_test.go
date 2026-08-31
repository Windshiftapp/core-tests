//go:build test

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"windshift/internal/models"
	"windshift/internal/restapi"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

func TestPageDiagramHandler_CRUDAndOwnership(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)
	permissions, err := services.NewPermissionService(tdb.GetDatabase(), services.DefaultPermissionCacheConfig())
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	pageAuth := services.NewPagePermissionService(tdb.GetDatabase(), permissions)
	pageService := services.NewPageService(tdb.GetDatabase())
	pageApplication := services.NewPageApplicationService(pageService, pageAuth)
	diagramService := services.NewPageDiagramService(
		tdb.GetDatabase(),
		t.TempDir(),
		pageApplication,
		pageAuth,
		permissions,
	)
	handler := NewPageDiagramHandler(
		NewBaseHandler(tdb.GetDatabase(), permissions),
		diagramService,
		pageService,
	)
	actor := services.AuditActor{UserID: data.UserID, Username: "testuser", Source: "test"}
	page, err := pageApplication.Create(actor, services.CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		Title:       "REST diagrams",
		Content:     "# Page",
	})
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	other, err := pageApplication.Create(actor, services.CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		Title:       "Other page",
	})
	if err != nil {
		t.Fatalf("create other page: %v", err)
	}
	user := &models.User{ID: data.UserID, Username: "testuser"}

	create := pageDiagramHandlerRequest(t, http.MethodPost, data.WorkspaceID, page.ID, 0, user, map[string]any{
		"name":                  "REST flow",
		"mermaid":               "graph TD; A-->B",
		"placement":             "end",
		"expected_content_hash": page.ContentHash,
	})
	createRecorder := httptest.NewRecorder()
	handler.Create(createRecorder, create)
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", createRecorder.Code, createRecorder.Body.String())
	}
	var created services.PageDiagram
	if err := json.Unmarshal(createRecorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created diagram: %v", err)
	}
	if created.AttachmentID == 0 || created.Kind != services.DiagramKindMermaid || created.ContentHash == "" {
		t.Fatalf("created diagram = %+v", created)
	}

	get := pageDiagramHandlerRequest(t, http.MethodGet, data.WorkspaceID, page.ID, created.AttachmentID, user, nil)
	getRecorder := httptest.NewRecorder()
	handler.Get(getRecorder, get)
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", getRecorder.Code, getRecorder.Body.String())
	}
	var fetched services.PageDiagram
	if err := json.Unmarshal(getRecorder.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode fetched diagram: %v", err)
	}
	var seed map[string]string
	if err := json.Unmarshal(fetched.Payload, &seed); err != nil {
		t.Fatalf("decode Mermaid seed: %v", err)
	}
	if fetched.AttachmentID != created.AttachmentID || fetched.Kind != services.DiagramKindMermaid ||
		seed["source"] != "graph TD; A-->B" {
		t.Fatalf("fetched diagram = %+v payload=%s", fetched, fetched.Payload)
	}

	crossPage := pageDiagramHandlerRequest(t, http.MethodGet, data.WorkspaceID, other.ID, created.AttachmentID, user, nil)
	crossRecorder := httptest.NewRecorder()
	handler.Get(crossRecorder, crossPage)
	if crossRecorder.Code != http.StatusNotFound {
		t.Fatalf("cross-page get status = %d body=%s", crossRecorder.Code, crossRecorder.Body.String())
	}

	stale := pageDiagramHandlerRequest(t, http.MethodPut, data.WorkspaceID, page.ID, created.AttachmentID, user, map[string]any{
		"mermaid":               "graph TD; B-->C",
		"expected_content_hash": page.ContentHash,
	})
	staleRecorder := httptest.NewRecorder()
	handler.Update(staleRecorder, stale)
	if staleRecorder.Code != http.StatusConflict {
		t.Fatalf("stale update status = %d body=%s", staleRecorder.Code, staleRecorder.Body.String())
	}

	update := pageDiagramHandlerRequest(t, http.MethodPut, data.WorkspaceID, page.ID, created.AttachmentID, user, map[string]any{
		"excalidraw": map[string]any{
			"elements": []map[string]any{{"id": "one", "type": "rectangle"}},
			"appState": map[string]any{},
			"files":    map[string]any{},
		},
		"expected_content_hash": created.ContentHash,
	})
	updateRecorder := httptest.NewRecorder()
	handler.Update(updateRecorder, update)
	if updateRecorder.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", updateRecorder.Code, updateRecorder.Body.String())
	}
	var updated services.PageDiagram
	if err := json.Unmarshal(updateRecorder.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated diagram: %v", err)
	}
	if updated.AttachmentID == created.AttachmentID || updated.Kind != services.DiagramKindExcalidraw {
		t.Fatalf("updated diagram = %+v", updated)
	}

	wrongWorkspace := pageDiagramHandlerRequest(t, http.MethodGet, data.WorkspaceID+999, page.ID, updated.AttachmentID, user, nil)
	wrongWorkspaceRecorder := httptest.NewRecorder()
	handler.Get(wrongWorkspaceRecorder, wrongWorkspace)
	if wrongWorkspaceRecorder.Code != http.StatusNotFound {
		t.Fatalf("wrong-workspace get status = %d body=%s", wrongWorkspaceRecorder.Code, wrongWorkspaceRecorder.Body.String())
	}
}

func pageDiagramHandlerRequest(
	t *testing.T,
	method string,
	workspaceID, pageID, attachmentID int,
	user *models.User,
	body any,
) *http.Request {
	t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
	}
	req := httptest.NewRequest(method, "/page-diagram", bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.Itoa(workspaceID))
	req.SetPathValue("pageId", strconv.Itoa(pageID))
	if attachmentID > 0 {
		req.SetPathValue("attachmentId", strconv.Itoa(attachmentID))
	}
	ctx := context.WithValue(req.Context(), restapi.ContextKeyUser, user)
	return req.WithContext(ctx)
}
