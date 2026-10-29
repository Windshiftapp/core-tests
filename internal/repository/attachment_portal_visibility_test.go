//go:build test

package repository

import (
	"errors"
	"testing"

	"windshift/internal/testutils"
)

// TestPortalAttachmentVisibilityExcludesInternal is the WI-1700 persistence
// contract: internal attachments stay visible to workspace members but are
// hidden from the portal list and denied on the portal download path.
func TestPortalAttachmentVisibilityExcludesInternal(t *testing.T) {
	t.Parallel()

	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	repo := NewAttachmentRepository(tdb.GetDatabase())

	workspaceID := testutils.InsertID(t, tdb, `INSERT INTO workspaces (name, key) VALUES ('Portal Visibility', 'PVIS')`)
	itemID := testutils.InsertID(t, tdb, `INSERT INTO items (workspace_id, workspace_item_number, title, frac_index) VALUES (?, 1, 'Request', 'a0')`, workspaceID)

	insert := func(filename string, isInternal bool) int {
		t.Helper()
		return testutils.InsertID(t, tdb, `
			INSERT INTO attachments (item_id, entity_type, filename, original_filename, file_path, mime_type, file_size, is_internal)
			VALUES (?, 'item', ?, ?, ?, 'image/png', 1, ?)
		`, itemID, filename, filename, "/tmp/"+filename, isInternal)
	}
	publicID := insert("public.png", false)
	internalID := insert("internal.png", true)

	all, allTotal, err := repo.ListItem(itemID, 50, 0)
	if err != nil {
		t.Fatalf("ListItem() error = %v", err)
	}
	if allTotal != 2 || len(all) != 2 {
		t.Fatalf("ListItem() = %d/%d, want 2/2 (internal files stay visible to members)", len(all), allTotal)
	}

	portal, portalTotal, err := repo.ListPortalItem(itemID, 50, 0)
	if err != nil {
		t.Fatalf("ListPortalItem() error = %v", err)
	}
	if portalTotal != 1 || len(portal) != 1 {
		t.Fatalf("ListPortalItem() = %d/%d, want 1/1", len(portal), portalTotal)
	}
	if portal[0].ID != publicID {
		t.Fatalf("ListPortalItem() returned attachment %d, want public %d", portal[0].ID, publicID)
	}

	if _, err := repo.GetPortalRequestAttachmentRecord(publicID, itemID); err != nil {
		t.Fatalf("GetPortalRequestAttachmentRecord(public) error = %v", err)
	}
	if _, err := repo.GetPortalRequestAttachmentRecord(internalID, itemID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetPortalRequestAttachmentRecord(internal) error = %v, want ErrNotFound", err)
	}
}
