//go:build test

package services

import (
	"errors"
	"testing"

	"windshift/internal/testutils"
)

func TestPageApplicationService_UpdateContentHashPrecondition(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	permissionService, err := NewPermissionService(tdb.GetDatabase(), DefaultPermissionCacheConfig())
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	pageService := NewPageService(tdb.GetDatabase())
	application := NewPageApplicationService(
		pageService,
		NewPagePermissionService(tdb.GetDatabase(), permissionService),
	)
	actor := AuditActor{UserID: data.UserID, Username: "testuser", Source: "test"}
	page, err := application.Create(actor, CreatePageInput{
		WorkspaceID: data.WorkspaceID,
		Title:       "Concurrency",
		Content:     "original content",
	})
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	assertCounts := func(wantRevisions, wantChunks int) {
		t.Helper()
		var revisions, chunks int
		if err := tdb.QueryRow(`SELECT COUNT(*) FROM page_revisions WHERE page_id = ?`, page.ID).Scan(&revisions); err != nil {
			t.Fatalf("count revisions: %v", err)
		}
		if err := tdb.QueryRow(`SELECT COUNT(*) FROM page_chunks WHERE page_id = ?`, page.ID).Scan(&chunks); err != nil {
			t.Fatalf("count chunks: %v", err)
		}
		if revisions != wantRevisions || chunks != wantChunks {
			t.Fatalf("revision/chunk counts = %d/%d, want %d/%d", revisions, chunks, wantRevisions, wantChunks)
		}
	}

	assertCounts(1, 1)
	staleHash := "stale"
	staleContent := "must not be written"
	_, err = application.Update(actor, data.WorkspaceID, PageApplicationUpdateInput{
		ID:                  page.ID,
		Content:             &staleContent,
		ExpectedContentHash: &staleHash,
	})
	if !errors.Is(err, ErrPageContentConflict) {
		t.Fatalf("stale update error = %v, want ErrPageContentConflict", err)
	}
	afterStale, err := pageService.GetByID(page.ID)
	if err != nil {
		t.Fatalf("get page after stale update: %v", err)
	}
	if afterStale.Content != page.Content || afterStale.ContentHash != page.ContentHash {
		t.Fatalf("stale update changed page to content/hash %q/%q", afterStale.Content, afterStale.ContentHash)
	}
	assertCounts(1, 1)

	matchingContent := "matching update"
	matching, err := application.Update(actor, data.WorkspaceID, PageApplicationUpdateInput{
		ID:                  page.ID,
		Content:             &matchingContent,
		ExpectedContentHash: &page.ContentHash,
	})
	if err != nil {
		t.Fatalf("matching update: %v", err)
	}
	if matching.Content != matchingContent || matching.ContentHash == "" || matching.ContentHash == page.ContentHash {
		t.Fatalf("matching update content/hash = %q/%q", matching.Content, matching.ContentHash)
	}
	assertCounts(2, 1)

	legacyContent := "precondition omitted"
	legacy, err := application.Update(actor, data.WorkspaceID, PageApplicationUpdateInput{
		ID:      page.ID,
		Content: &legacyContent,
	})
	if err != nil {
		t.Fatalf("update without precondition: %v", err)
	}
	if legacy.Content != legacyContent || legacy.ContentHash == matching.ContentHash {
		t.Fatalf("legacy update content/hash = %q/%q", legacy.Content, legacy.ContentHash)
	}
	assertCounts(3, 1)
}
