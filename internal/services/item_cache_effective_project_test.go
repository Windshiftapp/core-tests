//go:build test

package services

import (
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

func newEffectiveProjectCacheService(t *testing.T, db database.Database) *ItemCacheService {
	t.Helper()
	service := NewItemCacheService(db, ItemCacheConfig{
		HierarchyTTL:  time.Hour,
		MaxCacheSize:  12,
		EnablePreWarm: false,
	})
	t.Cleanup(func() { _ = service.Close() })
	return service
}

func insertTimeProject(t *testing.T, db database.Database, name string) int {
	t.Helper()
	return testutils.InsertID(t, db, `
		INSERT INTO time_projects (name, description, status, created_at, updated_at)
		VALUES (?, 'test', 'Active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, name)
}

func createProjectItem(t *testing.T, db database.Database, env hierarchyTestEnv, title string, projectID *int, inherit bool, parentID *int) int {
	t.Helper()
	id, err := CreateItem(db, ItemCreationParams{
		WorkspaceID:    env.WorkspaceID,
		Title:          title,
		StatusID:       &env.StatusID,
		ProjectID:      projectID,
		InheritProject: inherit,
		ParentID:       parentID,
	})
	if err != nil {
		t.Fatalf("create item %q: %v", title, err)
	}
	return int(id)
}

// createItemOfType creates an item with a named item type. Inheritance only
// walks up when the item's type has a non-zero hierarchy_level, so an inheriting
// child needs a deeper type than its parent.
func createItemOfType(t *testing.T, db database.Database, env hierarchyTestEnv, title, typeName string, inherit bool, parentID *int) int {
	t.Helper()
	var typeID int
	if err := db.QueryRow("SELECT id FROM item_types WHERE name = ?", typeName).Scan(&typeID); err != nil {
		t.Fatalf("find item type %s: %v", typeName, err)
	}
	id, err := CreateItem(db, ItemCreationParams{
		WorkspaceID:    env.WorkspaceID,
		Title:          title,
		StatusID:       &env.StatusID,
		ItemTypeID:     &typeID,
		InheritProject: inherit,
		ParentID:       parentID,
	})
	if err != nil {
		t.Fatalf("create item %q: %v", title, err)
	}
	return int(id)
}

func TestItemCacheServesDirectProjectUntilInvalidated(t *testing.T) {
	db := createHierarchyTestDB(t)
	env := setupHierarchyTestEnv(t, db)
	service := newEffectiveProjectCacheService(t, db)

	projectA := insertTimeProject(t, db, "Cache A")
	projectB := insertTimeProject(t, db, "Cache B")
	itemID := createProjectItem(t, db, env, "Direct project item", &projectA, false, nil)

	got, mode, err := service.GetEffectiveProjectForItem(itemID)
	if err != nil || got == nil || *got != projectA || mode != "direct" {
		t.Fatalf("cold resolve = %v, %q, %v; want %d, direct", got, mode, err, projectA)
	}

	// Mutate the row behind the cache: a warm read must serve the cached value.
	if _, err := db.Exec(`UPDATE items SET project_id = ? WHERE id = ?`, projectB, itemID); err != nil {
		t.Fatalf("update project: %v", err)
	}
	got, mode, err = service.GetEffectiveProjectForItem(itemID)
	if err != nil || got == nil || *got != projectA || mode != "direct" {
		t.Fatalf("warm resolve = %v, %q, %v; want cached %d", got, mode, err, projectA)
	}

	if err := service.InvalidateItemHierarchy(itemID, nil); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	got, mode, err = service.GetEffectiveProjectForItem(itemID)
	if err != nil || got == nil || *got != projectB {
		t.Fatalf("post-invalidation resolve = %v, %q, %v; want %d", got, mode, err, projectB)
	}
}

func TestItemCacheServesInheritedProjectUntilDescendantInvalidated(t *testing.T) {
	db := createHierarchyTestDB(t)
	env := setupHierarchyTestEnv(t, db)
	service := newEffectiveProjectCacheService(t, db)

	projectA := insertTimeProject(t, db, "Inherit A")
	projectB := insertTimeProject(t, db, "Inherit B")

	// Give the Epic parent a direct project, then create a Story child that
	// inherits it. The Story type's non-zero hierarchy_level is what lets the
	// resolver walk up.
	if _, err := db.Exec(`UPDATE items SET project_id = ? WHERE id = ?`, projectA, env.ChildItem1); err != nil {
		t.Fatalf("set parent project: %v", err)
	}
	childID := createItemOfType(t, db, env, "Inheriting story", "Story", true, &env.ChildItem1)

	got, mode, err := service.GetEffectiveProjectForItem(childID)
	if err != nil || got == nil || *got != projectA || mode != "inherit" {
		t.Fatalf("cold child resolve = %v, %q, %v; want %d, inherit", got, mode, err, projectA)
	}

	// Change the parent's project behind the cache; a warm child read keeps the
	// cached inherited value.
	if _, err := db.Exec(`UPDATE items SET project_id = ? WHERE id = ?`, projectB, env.ChildItem1); err != nil {
		t.Fatalf("update parent project: %v", err)
	}
	got, _, _ = service.GetEffectiveProjectForItem(childID)
	if got == nil || *got != projectA {
		t.Fatalf("warm child resolve = %v, want cached %d", got, projectA)
	}

	// The production mutation path invalidates descendants after a parent
	// change; the next read resolves the new inherited project.
	if err := service.InvalidateItemHierarchy(childID, nil); err != nil {
		t.Fatalf("invalidate child: %v", err)
	}
	got, mode, err = service.GetEffectiveProjectForItem(childID)
	if err != nil || got == nil || *got != projectB || mode != "inherit" {
		t.Fatalf("post-invalidation child resolve = %v, %q, %v; want %d, inherit", got, mode, err, projectB)
	}
}

func TestItemCRUDServiceUsesInjectedEffectiveProjectCache(t *testing.T) {
	db := createHierarchyTestDB(t)
	env := setupHierarchyTestEnv(t, db)
	service := newEffectiveProjectCacheService(t, db)

	projectA := insertTimeProject(t, db, "CRUD A")
	projectB := insertTimeProject(t, db, "CRUD B")
	itemID := createProjectItem(t, db, env, "CRUD cache item", &projectA, false, nil)

	cached := NewItemCRUDService(db).WithCache(service)
	item, err := cached.GetWithEffectiveProject(itemID)
	if err != nil {
		t.Fatalf("cached GetWithEffectiveProject: %v", err)
	}
	if item.EffectiveProjectID == nil || *item.EffectiveProjectID != projectA || item.ProjectInheritanceMode != "direct" {
		t.Fatalf("cached read = %v/%q, want %d/direct", item.EffectiveProjectID, item.ProjectInheritanceMode, projectA)
	}

	if _, err := db.Exec(`UPDATE items SET project_id = ? WHERE id = ?`, projectB, itemID); err != nil {
		t.Fatalf("update project: %v", err)
	}
	item, err = cached.GetWithEffectiveProject(itemID)
	if err != nil {
		t.Fatalf("warm cached GetWithEffectiveProject: %v", err)
	}
	if item.EffectiveProjectID == nil || *item.EffectiveProjectID != projectA {
		t.Fatalf("warm cached read = %v, want cached %d", item.EffectiveProjectID, projectA)
	}

	// Without a cache the same service resolves fresh.
	uncached := NewItemCRUDService(db)
	item, err = uncached.GetWithEffectiveProject(itemID)
	if err != nil {
		t.Fatalf("uncached GetWithEffectiveProject: %v", err)
	}
	if item.EffectiveProjectID == nil || *item.EffectiveProjectID != projectB {
		t.Fatalf("uncached read = %v, want fresh %d", item.EffectiveProjectID, projectB)
	}
}
