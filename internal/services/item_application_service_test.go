//go:build test

package services

import (
	"encoding/json"
	"errors"
	"testing"

	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestItemApplicationServiceSharesCRUDContractAndMasksDeniedReads(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	permissions, err := NewPermissionService(tdb.GetDatabase(), DefaultPermissionCacheConfig())
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	t.Cleanup(func() { _ = permissions.Close() })
	creation := NewItemCreationService(tdb.GetDatabase(), permissions)
	update := NewItemUpdateApplicationService(tdb.GetDatabase(), permissions)
	deletion := NewItemDeletionApplicationService(tdb.GetDatabase(), permissions)
	application := NewItemApplicationService(tdb.GetDatabase(), permissions, creation, update, deletion)
	actor := AuditActor{UserID: data.UserID, Username: "testuser"}

	created, err := application.Create(t.Context(), actor, ItemCreateInput{
		WorkspaceID: data.WorkspaceID,
		Title:       "Canonical v2 item",
		Description: "Initial description",
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	if created.ID == 0 || created.DescriptionHTML == "" {
		t.Fatalf("created item = %#v, want persisted enriched item", created)
	}

	patched, err := application.Patch(t.Context(), actor, created.ID, map[string]json.RawMessage{
		"title":       json.RawMessage(`"Updated through v2"`),
		"description": json.RawMessage(`"Updated description"`),
	})
	if err != nil {
		t.Fatalf("patch item: %v", err)
	}
	if patched.Title != "Updated through v2" || patched.DescriptionHTML == "" {
		t.Fatalf("patched item = %#v, want updated enriched item", patched)
	}

	page, err := application.List(t.Context(), ItemListRequest{
		UserID: data.UserID,
		Pagination: PaginationParams{
			Limit:  1,
			Offset: 0,
		},
		SortBy:  "created_at",
		SortAsc: false,
	})
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if page.Total < 1 || len(page.Items) != 1 || page.Items[0].ID != created.ID {
		t.Fatalf("list result = %#v, want created item in one-item page", page)
	}

	deniedUserID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('v2-denied@example.test', 'v2-denied', 'V2', 'Denied', true)
	`)
	if _, err := tdb.Exec(`UPDATE workspaces SET active = false WHERE id = ?`, data.WorkspaceID); err != nil {
		t.Fatalf("deactivate workspace: %v", err)
	}
	_, err = application.Get(t.Context(), deniedUserID, created.ID, false)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("denied get error = %v, want repository.ErrNotFound", err)
	}

	if err := application.Delete(actor, created.ID); err != nil {
		t.Fatalf("delete item: %v", err)
	}
	_, err = application.Get(t.Context(), data.UserID, created.ID, false)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("get after delete error = %v, want repository.ErrNotFound", err)
	}
}
