//go:build test

package services

import (
	"fmt"
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// hierarchyCountFixture builds a caller who can view the seeded workspace plus
// a restricted workspace they cannot view, so tests can assert that aggregate
// descendant counts never leak cross-workspace volume.
type hierarchyCountFixture struct {
	db            database.Database
	application   *ItemApplicationService
	callerID      int
	openWorkspace int
	restricted    int
	nextFrac      int
}

func newHierarchyCountFixture(t *testing.T) *hierarchyCountFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	config := DefaultPermissionCacheConfig()
	config.WarmupOnStartup = false
	permissions, err := NewPermissionService(db, config)
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	t.Cleanup(func() { _ = permissions.Close() })
	creation := NewItemCreationService(db, permissions)
	update := NewItemUpdateApplicationService(db, permissions)
	deletion := NewItemDeletionApplicationService(db, permissions)
	application := NewItemApplicationService(db, permissions, creation, update, deletion)

	otherUserID := testutils.InsertID(t, db, `
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('count-scope-other@example.test', 'count-scope-other', 'Other', 'User', true)
	`)
	restricted, err := NewWorkspaceService(db).Create(t.Context(), CreateWorkspaceParams{
		Name:      "Restricted Count Workspace",
		Key:       "RCW",
		CreatorID: otherUserID,
	})
	if err != nil {
		t.Fatalf("create restricted workspace: %v", err)
	}
	assignWorkspaceRoleByName(t, db, otherUserID, restricted.Workspace.ID, "Viewer")

	return &hierarchyCountFixture{
		db:            db,
		application:   application,
		callerID:      data.UserID,
		openWorkspace: data.WorkspaceID,
		restricted:    restricted.Workspace.ID,
	}
}

func (f *hierarchyCountFixture) insertItem(t *testing.T, workspaceID int, title string, parentID *int, estimateMinutes int) int {
	t.Helper()
	f.nextFrac++
	var id int
	if err := f.db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, description, parent_id, estimate_minutes, frac_index, created_at, updated_at)
		VALUES (?, ?, ?, '', ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`, workspaceID, workspaceID*1000+f.nextFrac, title, parentID, estimateMinutes, fmt.Sprintf("a%06d", f.nextFrac)).Scan(&id); err != nil {
		t.Fatalf("insert item %q: %v", title, err)
	}
	return id
}

func (f *hierarchyCountFixture) insertWorklog(t *testing.T, itemID, durationMinutes int) {
	t.Helper()
	customerID := testutils.InsertID(t, f.db, `INSERT INTO customer_organisations (name) VALUES (?)`, fmt.Sprintf("Customer %d", itemID))
	projectID := testutils.InsertID(t, f.db, `INSERT INTO time_projects (customer_id, name) VALUES (?, ?)`, customerID, fmt.Sprintf("Project %d", itemID))
	if _, err := f.db.Exec(`
		INSERT INTO time_worklogs (project_id, customer_id, item_id, description, date, start_time, end_time, duration_minutes, created_at, updated_at)
		VALUES (?, ?, ?, 'Work', 100, 100, 200, ?, 100, 100)
	`, projectID, customerID, itemID, durationMinutes); err != nil {
		t.Fatalf("insert worklog for item %d: %v", itemID, err)
	}
}

// TestDeleteInfoExcludesInaccessibleWorkspaceDescendants reproduces WI-1815:
// the delete-info descendant count must only cover descendants the caller can
// view, even though the parent itself is visible.
func TestDeleteInfoExcludesInaccessibleWorkspaceDescendants(t *testing.T) {
	f := newHierarchyCountFixture(t)

	parentID := f.insertItem(t, f.openWorkspace, "Visible parent", nil, 0)
	f.insertItem(t, f.openWorkspace, "Same-workspace child", &parentID, 0)
	f.insertItem(t, f.restricted, "Restricted child", &parentID, 0)

	info, err := f.application.DeleteInfo(f.callerID, parentID)
	if err != nil {
		t.Fatalf("DeleteInfo: %v", err)
	}
	if info.DescendantCount != 1 {
		t.Fatalf("DescendantCount = %d, want 1 (restricted child must not be counted)", info.DescendantCount)
	}
	if !info.HasChildren {
		t.Fatal("HasChildren = false, want true for the accessible child")
	}
}

func TestDeleteInfoCountsAccessibleDescendantBeneathInaccessibleIntermediate(t *testing.T) {
	f := newHierarchyCountFixture(t)

	parentID := f.insertItem(t, f.openWorkspace, "Visible parent", nil, 0)
	intermediateID := f.insertItem(t, f.restricted, "Restricted intermediate", &parentID, 0)
	f.insertItem(t, f.openWorkspace, "Accessible grandchild", &intermediateID, 0)

	info, err := f.application.DeleteInfo(f.callerID, parentID)
	if err != nil {
		t.Fatalf("DeleteInfo: %v", err)
	}
	if info.DescendantCount != 1 {
		t.Fatalf("DescendantCount = %d, want 1 (only the accessible grandchild)", info.DescendantCount)
	}
}

// TestTimeRollupExcludesInaccessibleWorkspaceDescendants reproduces WI-1815:
// estimates and logged minutes from workspaces the caller cannot view must not
// contribute to the rollup.
func TestTimeRollupExcludesInaccessibleWorkspaceDescendants(t *testing.T) {
	f := newHierarchyCountFixture(t)

	parentID := f.insertItem(t, f.openWorkspace, "Visible parent", nil, 10)
	childID := f.insertItem(t, f.openWorkspace, "Same-workspace child", &parentID, 20)
	restrictedChildID := f.insertItem(t, f.restricted, "Restricted child", &parentID, 40)
	f.insertWorklog(t, parentID, 60)
	f.insertWorklog(t, restrictedChildID, 120)

	rollup, err := f.application.TimeRollup(t.Context(), f.callerID, parentID, 10)
	if err != nil {
		t.Fatalf("TimeRollup: %v", err)
	}
	if rollup.ItemCount != 2 {
		t.Fatalf("ItemCount = %d, want 2 (root + accessible child %d)", rollup.ItemCount, childID)
	}
	if rollup.TotalEstimateMinutes != 30 {
		t.Fatalf("TotalEstimateMinutes = %d, want 30", rollup.TotalEstimateMinutes)
	}
	if rollup.TotalLoggedMinutes != 60 {
		t.Fatalf("TotalLoggedMinutes = %d, want 60", rollup.TotalLoggedMinutes)
	}
}

func TestTimeRollupCountsAccessibleDescendantBeneathInaccessibleIntermediate(t *testing.T) {
	f := newHierarchyCountFixture(t)

	parentID := f.insertItem(t, f.openWorkspace, "Visible parent", nil, 5)
	intermediateID := f.insertItem(t, f.restricted, "Restricted intermediate", &parentID, 100)
	f.insertItem(t, f.openWorkspace, "Accessible grandchild", &intermediateID, 7)

	rollup, err := f.application.TimeRollup(t.Context(), f.callerID, parentID, 10)
	if err != nil {
		t.Fatalf("TimeRollup: %v", err)
	}
	if rollup.ItemCount != 2 {
		t.Fatalf("ItemCount = %d, want 2 (root + accessible grandchild)", rollup.ItemCount)
	}
	if rollup.TotalEstimateMinutes != 12 {
		t.Fatalf("TotalEstimateMinutes = %d, want 12", rollup.TotalEstimateMinutes)
	}
}
