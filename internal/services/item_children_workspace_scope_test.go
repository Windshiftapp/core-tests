//go:build test

package services

import (
	"fmt"
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

func assignWorkspaceRoleByName(t *testing.T, db database.Database, userID, workspaceID int, role string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_at)
		SELECT ?, ?, id, CURRENT_TIMESTAMP FROM workspace_roles WHERE name = ?
	`, userID, workspaceID, role); err != nil {
		t.Fatalf("assign workspace role %s: %v", role, err)
	}
}

// TestItemChildrenExcludeInaccessibleWorkspaces reproduces the cross-workspace
// child disclosure: a parent visible in one workspace can have children in
// another (validateParentHierarchy permits it), and those children must not be
// returned to a caller who cannot view their workspace.
func TestItemChildrenExcludeInaccessibleWorkspaces(t *testing.T) {
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

	// The seeded workspace is open to every authenticated user, so the caller
	// can view the parent there.
	parentWorkspaceID := data.WorkspaceID

	// A second workspace is restricted by giving another user its Viewer role,
	// which flips it out of the everyone-visible set.
	otherUserID := testutils.InsertID(t, db, `
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('child-scope-other@example.test', 'child-scope-other', 'Other', 'User', true)
	`)
	restricted, err := NewWorkspaceService(db).Create(t.Context(), CreateWorkspaceParams{
		Name:      "Restricted Child Workspace",
		Key:       "RCW",
		CreatorID: otherUserID,
	})
	if err != nil {
		t.Fatalf("create restricted workspace: %v", err)
	}
	restrictedID := restricted.Workspace.ID
	assignWorkspaceRoleByName(t, db, otherUserID, restrictedID, "Viewer")

	nextFrac := 0
	insertItem := func(workspaceID int, title string, parentID *int) int {
		t.Helper()
		nextFrac++
		var id int
		if err := db.QueryRow(`
			INSERT INTO items (workspace_id, workspace_item_number, title, description, parent_id, frac_index, created_at, updated_at)
			VALUES (?, ?, ?, '', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
			RETURNING id
		`, workspaceID, workspaceID*1000+nextFrac, title, parentID, fmt.Sprintf("a%06d", nextFrac)).Scan(&id); err != nil {
			t.Fatalf("insert item %q: %v", title, err)
		}
		return id
	}

	parentID := insertItem(parentWorkspaceID, "Visible parent", nil)
	sameWorkspaceChildID := insertItem(parentWorkspaceID, "Same-workspace child", &parentID)
	restrictedChildID := insertItem(restrictedID, "Restricted child", &parentID)

	children, err := application.Children(t.Context(), data.UserID, parentID)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}

	found := make(map[int]bool, len(children))
	for i := range children {
		found[children[i].ID] = true
	}
	if !found[sameWorkspaceChildID] {
		t.Fatalf("children %#v missing same-workspace child %d", children, sameWorkspaceChildID)
	}
	if found[restrictedChildID] {
		t.Fatalf("children %#v leaked restricted-workspace child %d", children, restrictedChildID)
	}
}
