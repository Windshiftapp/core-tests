//go:build test

package services

import (
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"windshift/internal/repository"
	"windshift/internal/testutils"
)

type pageDiagramTestHarness struct {
	tdb          *testutils.TestDB
	actor        AuditActor
	workspaceID  int
	pages        *PageApplicationService
	pageDiagrams *PageDiagramService
}

func newPageDiagramTestHarness(t *testing.T) pageDiagramTestHarness {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)
	permissions, err := NewPermissionService(tdb.GetDatabase(), DefaultPermissionCacheConfig())
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	pageAuth := NewPagePermissionService(tdb.GetDatabase(), permissions)
	pageService := NewPageService(tdb.GetDatabase())
	pages := NewPageApplicationService(pageService, pageAuth)
	return pageDiagramTestHarness{
		tdb:          tdb,
		actor:        AuditActor{UserID: data.UserID, Username: "testuser", Source: "test"},
		workspaceID:  data.WorkspaceID,
		pages:        pages,
		pageDiagrams: NewPageDiagramService(tdb.GetDatabase(), t.TempDir(), pages, pageAuth, permissions),
	}
}

func (h pageDiagramTestHarness) createPage(t *testing.T, title, content string) *PageDiagram {
	t.Helper()
	page, err := h.pages.Create(h.actor, CreatePageInput{
		WorkspaceID: h.workspaceID,
		Title:       title,
		Content:     content,
	})
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	diagram, err := h.pageDiagrams.Create(h.actor, CreatePageDiagramInput{
		PageID:              page.ID,
		Name:                "Flow",
		Mermaid:             "graph TD; A-->B",
		Placement:           PageDiagramPlacementEnd,
		ExpectedContentHash: &page.ContentHash,
	})
	if err != nil {
		t.Fatalf("create page diagram: %v", err)
	}
	return diagram
}

func TestPageDiagramService_CreateListGetAndUpdate(t *testing.T) {
	h := newPageDiagramTestHarness(t)
	diagram := h.createPage(t, "Page diagrams", "# Intro")
	if diagram.Kind != DiagramKindMermaid || diagram.AttachmentID == 0 ||
		diagram.ContentHash == "" || diagram.RevisionNumber != 2 {
		t.Fatalf("created diagram = %+v", diagram)
	}

	page, err := h.pages.PageService().GetByID(diagram.PageID)
	if err != nil {
		t.Fatalf("get page: %v", err)
	}
	wantFence := "```excalidraw\n" +
		`{"attachmentId":` + strconv.Itoa(diagram.AttachmentID) + `,"name":"Flow"}` +
		"\n```"
	if page.Content != "# Intro\n\n"+wantFence {
		t.Fatalf("page content = %q, want %q", page.Content, "# Intro\n\n"+wantFence)
	}

	listed, err := h.pageDiagrams.List(h.actor, page.ID)
	if err != nil {
		t.Fatalf("list diagrams: %v", err)
	}
	if len(listed) != 1 || listed[0].AttachmentID != diagram.AttachmentID ||
		listed[0].Kind != DiagramKindMermaid {
		t.Fatalf("listed diagrams = %+v", listed)
	}
	fetched, err := h.pageDiagrams.Get(h.actor, page.ID, diagram.AttachmentID)
	if err != nil {
		t.Fatalf("get diagram: %v", err)
	}
	var seed map[string]string
	if err := json.Unmarshal(fetched.Payload, &seed); err != nil {
		t.Fatalf("decode fetched payload: %v", err)
	}
	if seed["type"] != DiagramKindMermaid || seed["source"] != "graph TD; A-->B" {
		t.Fatalf("fetched seed = %#v", seed)
	}

	scene := json.RawMessage(`{"elements":[{"id":"one","type":"rectangle"}],"appState":{},"files":{}}`)
	updated, err := h.pageDiagrams.Update(h.actor, UpdatePageDiagramInput{
		PageID:              page.ID,
		AttachmentID:        diagram.AttachmentID,
		Excalidraw:          scene,
		ExpectedContentHash: &page.ContentHash,
	})
	if err != nil {
		t.Fatalf("update diagram: %v", err)
	}
	if updated.AttachmentID == diagram.AttachmentID || updated.Kind != DiagramKindExcalidraw ||
		updated.RevisionNumber != 3 {
		t.Fatalf("updated diagram = %+v", updated)
	}

	current, err := h.pages.PageService().GetByID(page.ID)
	if err != nil {
		t.Fatalf("get updated page: %v", err)
	}
	if len(matchingPageDiagramBlocks(current.Content, diagram.AttachmentID)) != 0 ||
		len(matchingPageDiagramBlocks(current.Content, updated.AttachmentID)) != 1 {
		t.Fatalf("updated page content = %q", current.Content)
	}
	attachments := repository.NewAttachmentRepository(h.tdb.GetDatabase())
	if _, err := attachments.GetPageAttachmentRecord(page.ID, diagram.AttachmentID); err != nil {
		t.Fatalf("old immutable attachment was removed: %v", err)
	}
	if _, err := attachments.GetPageAttachmentRecord(page.ID, updated.AttachmentID); err != nil {
		t.Fatalf("new attachment missing: %v", err)
	}
	revisions, err := h.pages.PageService().ListRevisions(page.ID, 10, 0)
	if err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	if len(revisions) != 3 ||
		len(matchingPageDiagramBlocks(revisions[1].Content, diagram.AttachmentID)) != 1 {
		t.Fatalf("revision history did not retain old diagram reference: %+v", revisions)
	}

	restored, err := h.pages.Restore(h.actor, h.workspaceID, page.ID, revisions[1].ID)
	if err != nil {
		t.Fatalf("restore pre-update revision: %v", err)
	}
	if len(matchingPageDiagramBlocks(restored.Content, diagram.AttachmentID)) != 1 ||
		len(matchingPageDiagramBlocks(restored.Content, updated.AttachmentID)) != 0 {
		t.Fatalf("restored page did not point to older attachment: %q", restored.Content)
	}
	restoredDiagram, err := h.pageDiagrams.Get(h.actor, restored.ID, diagram.AttachmentID)
	if err != nil {
		t.Fatalf("older diagram did not remain readable after restore: %v", err)
	}
	if restoredDiagram.Kind != DiagramKindMermaid {
		t.Fatalf("restored diagram kind = %q, want %q", restoredDiagram.Kind, DiagramKindMermaid)
	}
}

func TestPageDiagramService_StaleMutationCleansNewAttachment(t *testing.T) {
	h := newPageDiagramTestHarness(t)
	page, err := h.pages.Create(h.actor, CreatePageInput{
		WorkspaceID: h.workspaceID,
		Title:       "Cleanup",
		Content:     "before",
	})
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	staleHash := page.ContentHash
	concurrentContent := "concurrent edit"
	if _, err := h.pages.Update(h.actor, h.workspaceID, PageApplicationUpdateInput{
		ID:      page.ID,
		Content: &concurrentContent,
	}); err != nil {
		t.Fatalf("concurrent update: %v", err)
	}

	var before int
	if err := h.tdb.QueryRow(`SELECT COUNT(*) FROM attachments WHERE entity_type = 'page' AND item_id = ?`, page.ID).Scan(&before); err != nil {
		t.Fatalf("count attachments before stale create: %v", err)
	}
	_, err = h.pageDiagrams.Create(h.actor, CreatePageDiagramInput{
		PageID:              page.ID,
		Name:                "Stale",
		Mermaid:             "graph TD; A-->B",
		Placement:           PageDiagramPlacementEnd,
		ExpectedContentHash: &staleHash,
	})
	if !errors.Is(err, ErrPageContentConflict) {
		t.Fatalf("stale create error = %v, want ErrPageContentConflict", err)
	}
	var after int
	if err := h.tdb.QueryRow(`SELECT COUNT(*) FROM attachments WHERE entity_type = 'page' AND item_id = ?`, page.ID).Scan(&after); err != nil {
		t.Fatalf("count attachments after stale create: %v", err)
	}
	if after != before {
		t.Fatalf("attachments after stale create = %d, want %d", after, before)
	}
	current, err := h.pages.PageService().GetByID(page.ID)
	if err != nil {
		t.Fatalf("get page after stale create: %v", err)
	}
	if current.Content != concurrentContent {
		t.Fatalf("stale create changed page content to %q", current.Content)
	}
}

func TestPageDiagramService_RejectsDuplicateAndCrossPageReferences(t *testing.T) {
	h := newPageDiagramTestHarness(t)
	diagram := h.createPage(t, "Owner", "")
	owner, err := h.pages.PageService().GetByID(diagram.PageID)
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	duplicate := owner.Content + "\n\n" + owner.Content
	owner, err = h.pages.Update(h.actor, h.workspaceID, PageApplicationUpdateInput{
		ID:      owner.ID,
		Content: &duplicate,
	})
	if err != nil {
		t.Fatalf("duplicate reference: %v", err)
	}
	_, err = h.pageDiagrams.Update(h.actor, UpdatePageDiagramInput{
		PageID:              owner.ID,
		AttachmentID:        diagram.AttachmentID,
		Mermaid:             "graph TD; B-->C",
		ExpectedContentHash: &owner.ContentHash,
	})
	if !errors.Is(err, ErrPageDiagramReferenceConflict) {
		t.Fatalf("duplicate update error = %v, want ErrPageDiagramReferenceConflict", err)
	}

	other, err := h.pages.Create(h.actor, CreatePageInput{
		WorkspaceID: h.workspaceID,
		Title:       "Other",
		Content:     renderPageDiagramFence(diagram.AttachmentID, "Cross-page"),
	})
	if err != nil {
		t.Fatalf("create other page: %v", err)
	}
	listed, err := h.pageDiagrams.List(h.actor, other.ID)
	if err != nil {
		t.Fatalf("list cross-page reference: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("cross-page reference leaked diagram: %+v", listed)
	}
	if _, err := h.pageDiagrams.Get(h.actor, other.ID, diagram.AttachmentID); !errors.Is(err, ErrPageDiagramNotFound) {
		t.Fatalf("cross-page get error = %v, want ErrPageDiagramNotFound", err)
	}
}

func TestPageDiagramService_EnforcesPageViewAndEdit(t *testing.T) {
	h := newPageDiagramTestHarness(t)
	diagram := h.createPage(t, "Permissions", "")

	var viewerID int
	if err := h.tdb.QueryRow(`
		INSERT INTO users (username, email, first_name, last_name, password_hash, is_active)
		VALUES ('diagram_viewer', 'diagram_viewer@test.com', 'Diagram', 'Viewer', 'hash', true)
		RETURNING id
	`).Scan(&viewerID); err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	var viewerRoleID int
	if err := h.tdb.QueryRow(`SELECT id FROM workspace_roles WHERE name = 'Viewer'`).Scan(&viewerRoleID); err != nil {
		t.Fatalf("load viewer role: %v", err)
	}
	if _, err := h.tdb.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
	`, viewerID, h.workspaceID, viewerRoleID); err != nil {
		t.Fatalf("assign viewer role: %v", err)
	}
	viewer := AuditActor{UserID: viewerID, Username: "diagram_viewer", Source: "test"}
	if _, err := h.pageDiagrams.Get(viewer, diagram.PageID, diagram.AttachmentID); err != nil {
		t.Fatalf("viewer get diagram: %v", err)
	}
	_, err := h.pageDiagrams.Update(viewer, UpdatePageDiagramInput{
		PageID:       diagram.PageID,
		AttachmentID: diagram.AttachmentID,
		Mermaid:      "graph TD; B-->C",
	})
	if !errors.Is(err, ErrPageDiagramNotFound) {
		t.Fatalf("viewer update error = %v, want masked ErrPageDiagramNotFound", err)
	}
}
