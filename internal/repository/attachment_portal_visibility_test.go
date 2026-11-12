//go:build test

package repository

import (
	"errors"
	"testing"

	"windshift/internal/testutils"
)

func TestSetItemInternalTogglesPortalVisibility(t *testing.T) {
	t.Parallel()

	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	repo := NewAttachmentRepository(tdb.GetDatabase())

	workspaceID := testutils.InsertID(t, tdb, `INSERT INTO workspaces (name, key) VALUES ('Visibility Toggle', 'VTOG')`)
	itemID := testutils.InsertID(t, tdb, `INSERT INTO items (workspace_id, workspace_item_number, title, frac_index) VALUES (?, 1, 'Request', 'a0')`, workspaceID)

	publicID := testutils.InsertID(t, tdb, `
		INSERT INTO attachments (item_id, entity_type, filename, original_filename, file_path, mime_type, file_size, is_internal)
		VALUES (?, 'item', 'evidence.png', 'evidence.png', '/tmp/evidence.png', 'image/png', 1, false)
	`, itemID)
	// A page-scoped row shares the attachments table; the items-scoped setter
	// must refuse to reclassify it even though its item_id column is set.
	pageID := testutils.InsertID(t, tdb, `
		INSERT INTO attachments (item_id, entity_type, filename, original_filename, file_path, mime_type, file_size, is_internal)
		VALUES (?, 'page', 'diagram.json', 'diagram.json', '/tmp/diagram.json', 'application/json', 1, false)
	`, itemID)

	if err := repo.SetItemInternal(publicID, true); err != nil {
		t.Fatalf("SetItemInternal(item, true) error = %v", err)
	}

	// The portal list and download path must now hide the attachment.
	portal, total, err := repo.ListPortalItem(itemID, 50, 0)
	if err != nil {
		t.Fatalf("ListPortalItem() error = %v", err)
	}
	if total != 0 || len(portal) != 0 {
		t.Fatalf("ListPortalItem() = %d/%d, want 0/0 after hiding", len(portal), total)
	}
	if _, err := repo.GetPortalRequestAttachmentRecord(publicID, itemID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetPortalRequestAttachmentRecord(hidden) error = %v, want ErrNotFound", err)
	}

	// Workspace members still see it, flagged as internal.
	all, allTotal, err := repo.ListItem(itemID, 50, 0)
	if err != nil {
		t.Fatalf("ListItem() error = %v", err)
	}
	if allTotal != 1 || len(all) != 1 {
		t.Fatalf("ListItem() = %d/%d, want 1/1", len(all), allTotal)
	}
	if !all[0].IsInternal {
		t.Fatalf("ListItem() attachment is_internal = false, want true")
	}

	// Toggling back reveals it to the portal.
	if err := repo.SetItemInternal(publicID, false); err != nil {
		t.Fatalf("SetItemInternal(item, false) error = %v", err)
	}
	portal, total, err = repo.ListPortalItem(itemID, 50, 0)
	if err != nil {
		t.Fatalf("ListPortalItem() after reveal error = %v", err)
	}
	if total != 1 || len(portal) != 1 || portal[0].ID != publicID {
		t.Fatalf("ListPortalItem() after reveal = %d/%d, want 1/1 with id %d", len(portal), total, publicID)
	}
	if portal[0].IsInternal {
		t.Fatalf("ListPortalItem() attachment is_internal = true, want false")
	}

	// Page-scoped and missing rows are not reachable from the items surface.
	if err := repo.SetItemInternal(pageID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetItemInternal(page row) error = %v, want ErrNotFound", err)
	}
	if err := repo.SetItemInternal(999999, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetItemInternal(missing) error = %v, want ErrNotFound", err)
	}
}

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
