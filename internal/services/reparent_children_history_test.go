//go:build test

package services_test

import (
	"strconv"
	"testing"

	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

// TestReparentChildrenRecordsParentIDHistory asserts that reparenting writes
// a parent_id item_history row per child, attributed to the acting user —
// the same contract a plain parent_id edit through the update path honors.
func TestReparentChildrenRecordsParentIDHistory(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	permissions, err := services.NewPermissionService(tdb.GetDatabase(), services.DefaultPermissionCacheConfig())
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	t.Cleanup(func() { _ = permissions.Close() })
	application := services.NewItemApplicationService(
		tdb.GetDatabase(), permissions,
		services.NewItemCreationService(tdb.GetDatabase(), permissions),
		services.NewItemUpdateApplicationService(tdb.GetDatabase(), permissions),
		services.NewItemDeletionApplicationService(tdb.GetDatabase(), permissions),
	)
	actor := services.AuditActor{UserID: data.UserID, Username: "testuser"}

	// Hierarchy contract: Initiative items are roots (level 0), Epic items
	// are their children (level 1). The reparent moves an Epic between roots.
	var initiativeType, epicType int
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE name = 'Initiative'`).Scan(&initiativeType); err != nil {
		t.Fatalf("find Initiative item type: %v", err)
	}
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE name = 'Epic'`).Scan(&epicType); err != nil {
		t.Fatalf("find Epic item type: %v", err)
	}

	f := factory.NewTestFactory(tdb.GetDatabase())
	oldParent, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: data.WorkspaceID, Title: "Old parent", StatusID: &data.StatusID,
		CreatorID: &data.UserID, ItemTypeID: &initiativeType,
	})
	if err != nil {
		t.Fatalf("insert old parent: %v", err)
	}
	newParent, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: data.WorkspaceID, Title: "New parent", StatusID: &data.StatusID,
		CreatorID: &data.UserID, ItemTypeID: &initiativeType,
	})
	if err != nil {
		t.Fatalf("insert new parent: %v", err)
	}
	child, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: data.WorkspaceID, Title: "Child", StatusID: &data.StatusID,
		CreatorID: &data.UserID, ItemTypeID: &epicType, ParentID: &oldParent,
	})
	if err != nil {
		t.Fatalf("insert child: %v", err)
	}

	result, err := application.ReparentChildren(t.Context(), actor, oldParent, &newParent)
	if err != nil {
		t.Fatalf("reparent children: %v", err)
	}
	if result.Count != 1 {
		t.Fatalf("reparented count = %d, want 1", result.Count)
	}

	var fieldName string
	var oldValue, newValue string
	var userID int
	if err := tdb.QueryRow(`
		SELECT field_name, COALESCE(old_value, ''), COALESCE(new_value, ''), user_id
		FROM item_history
		WHERE item_id = ? AND field_name = 'parent_id' AND new_value = ?
	`, child, newParent).Scan(&fieldName, &oldValue, &newValue, &userID); err != nil {
		t.Fatalf("read parent_id history row: %v", err)
	}
	if oldValue != strconv.Itoa(oldParent) || newValue != strconv.Itoa(newParent) {
		t.Fatalf("parent history values = %q -> %q, want %q -> %q", oldValue, newValue, strconv.Itoa(oldParent), strconv.Itoa(newParent))
	}
	if userID != data.UserID {
		t.Fatalf("history actor = %d, want %d", userID, data.UserID)
	}
}
