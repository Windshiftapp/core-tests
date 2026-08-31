package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/services"
)

func wirePageDiagramHandler(t *testing.T) (*PageHandler, database.Database, int) {
	t.Helper()
	h, db, permissions := newPageHandler(t)
	const userID = 1
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	seedWorkspaceWithRole(t, db, 1, userID, "Editor")
	h.SetPageDiagramService(services.NewPageDiagramService(
		db,
		t.TempDir(),
		h.PageApplicationService(),
		h.pageAuth,
		permissions,
	))
	return h, db, userID
}

func TestPageHandler_DiagramCreateListGetAndUpdate(t *testing.T) {
	h, _, userID := wirePageDiagramHandler(t)
	page, err := h.service.Create(userID, services.CreatePageInput{
		WorkspaceID: 1,
		Title:       "Architecture",
		Content:     "# Architecture",
	})
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	createBody := map[string]interface{}{
		"name":                  "Flow",
		"mermaid":               "graph TD; A-->B",
		"placement":             "end",
		"expected_content_hash": page.ContentHash,
	}
	createReq := authedRequest(http.MethodPost, "/workspaces/1/pages/"+strconv.Itoa(page.ID)+"/diagrams", userID, createBody)
	setPath(createReq, map[string]string{"workspaceId": "1", "pageId": strconv.Itoa(page.ID)})
	createRR := httptest.NewRecorder()
	h.CreateDiagram(createRR, createReq)
	if createRR.Code != http.StatusCreated {
		t.Fatalf("create status: want 201, got %d body=%s", createRR.Code, createRR.Body.String())
	}
	var created services.PageDiagram
	decodeJSONBody(t, createRR, &created)
	if created.AttachmentID == 0 || created.Kind != services.DiagramKindMermaid || created.ContentHash == "" {
		t.Fatalf("created diagram: %+v", created)
	}

	listReq := authedRequest(http.MethodGet, "/workspaces/1/pages/"+strconv.Itoa(page.ID)+"/diagrams", userID, nil)
	setPath(listReq, map[string]string{"workspaceId": "1", "pageId": strconv.Itoa(page.ID)})
	listRR := httptest.NewRecorder()
	h.ListDiagrams(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list status: want 200, got %d body=%s", listRR.Code, listRR.Body.String())
	}
	var listed struct {
		Items []services.PageDiagram `json:"items"`
	}
	decodeJSONBody(t, listRR, &listed)
	if len(listed.Items) != 1 || listed.Items[0].AttachmentID != created.AttachmentID {
		t.Fatalf("listed diagrams: %+v", listed.Items)
	}

	getReq := authedRequest(http.MethodGet, "/workspaces/1/pages/"+strconv.Itoa(page.ID)+"/diagrams/"+strconv.Itoa(created.AttachmentID), userID, nil)
	setPath(getReq, map[string]string{
		"workspaceId":  "1",
		"pageId":       strconv.Itoa(page.ID),
		"attachmentId": strconv.Itoa(created.AttachmentID),
	})
	getRR := httptest.NewRecorder()
	h.GetDiagram(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("get status: want 200, got %d body=%s", getRR.Code, getRR.Body.String())
	}
	var fetched services.PageDiagram
	decodeJSONBody(t, getRR, &fetched)
	var seed map[string]string
	if err := json.Unmarshal(fetched.Payload, &seed); err != nil {
		t.Fatalf("decode seed: %v", err)
	}
	if seed["source"] != "graph TD; A-->B" {
		t.Fatalf("fetched seed: %#v", seed)
	}

	scene := map[string]interface{}{
		"elements": []map[string]interface{}{{"id": "one", "type": "rectangle"}},
		"appState": map[string]interface{}{},
		"files":    map[string]interface{}{},
	}
	updateBody := map[string]interface{}{
		"name":                  "Renamed flow",
		"excalidraw":            scene,
		"expected_content_hash": created.ContentHash,
	}
	updateReq := authedRequest(http.MethodPut, "/workspaces/1/pages/"+strconv.Itoa(page.ID)+"/diagrams/"+strconv.Itoa(created.AttachmentID), userID, updateBody)
	setPath(updateReq, map[string]string{
		"workspaceId":  "1",
		"pageId":       strconv.Itoa(page.ID),
		"attachmentId": strconv.Itoa(created.AttachmentID),
	})
	updateRR := httptest.NewRecorder()
	h.UpdateDiagram(updateRR, updateReq)
	if updateRR.Code != http.StatusOK {
		t.Fatalf("update status: want 200, got %d body=%s", updateRR.Code, updateRR.Body.String())
	}
	var updated services.PageDiagram
	decodeJSONBody(t, updateRR, &updated)
	if updated.AttachmentID == created.AttachmentID || updated.Name != "Renamed flow" ||
		updated.Kind != services.DiagramKindExcalidraw {
		t.Fatalf("updated diagram: %+v", updated)
	}
	current, err := h.service.GetByID(page.ID)
	if err != nil {
		t.Fatalf("get updated page: %v", err)
	}
	if strings.Contains(current.Content, `"attachmentId":`+strconv.Itoa(created.AttachmentID)) ||
		!strings.Contains(current.Content, `"attachmentId":`+strconv.Itoa(updated.AttachmentID)) ||
		!strings.Contains(current.Content, `"name":"Renamed flow"`) {
		t.Fatalf("updated page fence: %q", current.Content)
	}
}

func TestPageHandler_DiagramMutationMasksCrossWorkspaceAndViewerDenials(t *testing.T) {
	h, db, editorID := wirePageDiagramHandler(t)
	page, err := h.service.Create(editorID, services.CreatePageInput{
		WorkspaceID: 1,
		Title:       "Protected",
	})
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	crossReq := authedRequest(http.MethodPost, "/workspaces/2/pages/"+strconv.Itoa(page.ID)+"/diagrams", editorID, map[string]interface{}{
		"name": "Nope", "mermaid": "graph TD; A-->B", "placement": "end",
	})
	setPath(crossReq, map[string]string{"workspaceId": "2", "pageId": strconv.Itoa(page.ID)})
	crossRR := httptest.NewRecorder()
	h.CreateDiagram(crossRR, crossReq)
	if crossRR.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace status: want 404, got %d body=%s", crossRR.Code, crossRR.Body.String())
	}

	const viewerID = 2
	seedNegativeTestUser(t, db, viewerID)
	assignWorkspaceRole(t, db, 1, viewerID, "Viewer")
	viewerReq := authedRequest(http.MethodPost, "/workspaces/1/pages/"+strconv.Itoa(page.ID)+"/diagrams", viewerID, map[string]interface{}{
		"name": "Nope", "mermaid": "graph TD; A-->B", "placement": "end",
	})
	setPath(viewerReq, map[string]string{"workspaceId": "1", "pageId": strconv.Itoa(page.ID)})
	viewerRR := httptest.NewRecorder()
	h.CreateDiagram(viewerRR, viewerReq)
	if viewerRR.Code != http.StatusNotFound {
		t.Fatalf("viewer status: want 404, got %d body=%s", viewerRR.Code, viewerRR.Body.String())
	}
}
