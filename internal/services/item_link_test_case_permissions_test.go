package services

import (
	"path/filepath"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
)

type testCaseWorkspacePermissions struct {
	allow map[string]map[int]bool
	calls map[string]int
}

func (p *testCaseWorkspacePermissions) HasWorkspacePermission(_ int, workspaceID int, permission string) (bool, error) {
	p.calls[permission]++
	return p.allow[permission][workspaceID], nil
}

func (p *testCaseWorkspacePermissions) AccessibleWorkspaceIDs(int) ([]int, error) {
	return nil, nil
}

func (p *testCaseWorkspacePermissions) AccessibleWorkspaceIDKeys(int) ([]repository.IDKey, error) {
	return nil, nil
}

func newTestCaseLinkPermissionDB(t *testing.T) database.Database {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "test-case-link-permissions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return db
}

func insertTestCaseLinkPermissionRow(t *testing.T, db database.Database, query string, args ...any) int {
	t.Helper()
	result, err := db.ExecWrite(query, args...)
	if err != nil {
		t.Fatalf("insert fixture row: %v\nquery: %s", err, query)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	return int(id)
}

// insertTestCaseLinkItem creates an item through the production CreateItem
// path so the fixture row carries a canonical rank and workflow defaults.
func insertTestCaseLinkItem(t *testing.T, db database.Database, workspaceID int, title string) int {
	t.Helper()
	id, err := CreateItem(db, ItemCreationParams{WorkspaceID: workspaceID, Title: title})
	if err != nil {
		t.Fatalf("create fixture item %q: %v", title, err)
	}
	return int(id)
}

func TestItemLinkServiceTestCaseUsesTestPermissions(t *testing.T) {
	db := newTestCaseLinkPermissionDB(t)
	workspaceID := insertTestCaseLinkPermissionRow(t, db,
		`INSERT INTO workspaces (name, key, description, active) VALUES ('Test links', 'TL', '', true)`,
	)
	itemID := insertTestCaseLinkItem(t, db, workspaceID, "Visible item")
	testCaseID := insertTestCaseLinkPermissionRow(t, db,
		`INSERT INTO test_cases (workspace_id, title, priority, status) VALUES (?, 'Restricted case', 'medium', 'active')`, workspaceID,
	)
	var testsLinkTypeID int
	if err := db.QueryRow(`SELECT id FROM link_types WHERE name = 'Tests'`).Scan(&testsLinkTypeID); err != nil {
		t.Fatalf("load Tests link type: %v", err)
	}
	linkID := insertTestCaseLinkPermissionRow(t, db, `
		INSERT INTO item_links (link_type_id, source_type, source_id, target_type, target_id)
		VALUES (?, 'item', ?, 'test_case', ?)
	`, testsLinkTypeID, itemID, testCaseID)

	permissions := &testCaseWorkspacePermissions{
		allow: map[string]map[int]bool{
			models.PermissionItemView:   {workspaceID: true},
			models.PermissionItemEdit:   {workspaceID: true},
			models.PermissionTestView:   {workspaceID: false},
			models.PermissionTestManage: {workspaceID: false},
		},
		calls: map[string]int{},
	}
	service := NewItemLinkService(db).WithPermissionService(permissions)

	if err := service.CheckEntityPermission(7, "test_case", testCaseID, models.PermissionItemView, ""); !IsEntityNotAccessible(err) {
		t.Fatalf("test case view with item.view only = %v, want inaccessible", err)
	}
	if err := service.CheckEntityPermission(7, "test_case", testCaseID, models.PermissionItemEdit, ""); !IsEntityNotAccessible(err) {
		t.Fatalf("test case edit with item.edit only = %v, want inaccessible", err)
	}
	outgoing, incoming, err := service.ListLinksForEntityWithChecks(7, "item", itemID)
	if err != nil {
		t.Fatalf("ListLinksForEntityWithChecks denied viewer item access: %v", err)
	}
	if len(outgoing) != 0 || len(incoming) != 0 {
		t.Fatalf("links without test.view = outgoing %+v incoming %+v, want both hidden", outgoing, incoming)
	}

	permissions.allow[models.PermissionTestView][workspaceID] = true
	permissions.allow[models.PermissionTestManage][workspaceID] = true
	if err := service.CheckEntityPermission(7, "test_case", testCaseID, models.PermissionItemView, ""); err != nil {
		t.Fatalf("test case view with test.view = %v", err)
	}
	if err := service.CheckEntityPermission(7, "test_case", testCaseID, models.PermissionItemEdit, ""); err != nil {
		t.Fatalf("test case edit with test.manage = %v", err)
	}
	outgoing, _, err = service.ListLinksForEntityWithChecks(7, "item", itemID)
	if err != nil {
		t.Fatalf("ListLinksForEntityWithChecks with test.view: %v", err)
	}
	if len(outgoing) != 1 || outgoing[0].ID != linkID {
		t.Fatalf("links with test.view = %+v, want link %d", outgoing, linkID)
	}
	if permissions.calls[models.PermissionTestView] == 0 {
		t.Fatal("test.view was never checked")
	}
}
