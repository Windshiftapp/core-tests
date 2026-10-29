//go:build test

package services_test

import (
	"context"
	"strings"
	"testing"

	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

func newBundleExportTestService(t *testing.T) (*services.WorkspaceBundleExportService, *testutils.TestDB) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	svc := services.NewWorkspaceBundleExportService(
		tdb.GetDatabase(),
		services.NewConfigSetExportService(tdb.GetDatabase(), repository.NewConfigurationSetRepository(tdb.GetDatabase())),
	)
	return svc, tdb
}

func defaultItemTypeID(t *testing.T, tdb *testutils.TestDB) int {
	t.Helper()
	var id int
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE is_default = true ORDER BY id LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("load default item type: %v", err)
	}
	return id
}

// TestWorkspaceBundleExportOrdersParentsBeforeChildren verifies WI-1530's
// ordering fix: a child whose parent has a higher id (possible after
// re-parenting) is still emitted after its parent and keeps its parent_ref,
// instead of silently losing the subtree because the export assumed id order.
func TestWorkspaceBundleExportOrdersParentsBeforeChildren(t *testing.T) {
	svc, tdb := newBundleExportTestService(t)
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	f := factory.NewTestFactory(db)
	typeID := defaultItemTypeID(t, tdb)

	// Child first, then parent, then re-parent the child onto the later item so
	// id order no longer matches tree order.
	childID, err := f.CreateItem(factory.CreateItemOpts{WorkspaceID: data.WorkspaceID, Title: "Child", ItemTypeID: &typeID})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	parentID, err := f.CreateItem(factory.CreateItemOpts{WorkspaceID: data.WorkspaceID, Title: "Parent", ItemTypeID: &typeID})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if err := repository.NewItemRepository(db).SetParentDirect(childID, parentID); err != nil {
		t.Fatalf("re-parent child: %v", err)
	}

	bundle, err := svc.Export(context.Background(), data.WorkspaceID, nil)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(bundle.Payload.Items) != 2 {
		t.Fatalf("exported items = %d, want 2", len(bundle.Payload.Items))
	}
	parent, child := bundle.Payload.Items[0], bundle.Payload.Items[1]
	if parent.Title != "Parent" || child.Title != "Child" {
		t.Fatalf("export order = [%q, %q], want [Parent, Child]", parent.Title, child.Title)
	}
	if child.ParentRef != parent.Ref {
		t.Fatalf("child parent_ref = %q, want parent ref %q", child.ParentRef, parent.Ref)
	}
}

// TestWorkspaceBundleExportWarnsOnItemsWithoutItemType verifies WI-1530's
// reporting fix: an item whose item type was deleted (item_type_id NULL) is
// skipped with a warning instead of disappearing silently through an inner
// join.
func TestWorkspaceBundleExportWarnsOnItemsWithoutItemType(t *testing.T) {
	svc, tdb := newBundleExportTestService(t)
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	f := factory.NewTestFactory(db)
	typeID := defaultItemTypeID(t, tdb)

	orphanID, err := f.CreateItem(factory.CreateItemOpts{WorkspaceID: data.WorkspaceID, Title: "Orphaned type item", ItemTypeID: &typeID})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	// The ON DELETE SET NULL state a deleted item type leaves behind. The
	// production API cannot create it, so this is a documented direct write.
	if _, err := db.ExecWrite(`UPDATE items SET item_type_id = NULL WHERE id = ?`, orphanID); err != nil {
		t.Fatalf("clear item type: %v", err)
	}

	bundle, err := svc.Export(context.Background(), data.WorkspaceID, nil)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	for _, item := range bundle.Payload.Items {
		if item.Title == "Orphaned type item" {
			t.Fatalf("item without an item type must be skipped: %+v", item)
		}
	}
	found := false
	for _, warning := range bundle.Warnings {
		if strings.Contains(warning, "Orphaned type item") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v, want one naming the skipped item", bundle.Warnings)
	}
}
