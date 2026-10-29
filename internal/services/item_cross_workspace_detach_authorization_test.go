//go:build test

package services

import (
	"errors"
	"testing"
)

// Cross-workspace children exist by design (validateParentHierarchy permits
// them), so detaching them mutates a workspace the actor may not be allowed to
// touch. These tests cover WI-1811 (single delete) and WI-1812 (workspace
// move): the whole operation must be refused, not silently performed.

func TestSingleDeleteRefusesCrossWorkspaceChildWithoutPermission(t *testing.T) {
	f := newHierarchyCountFixture(t)
	parentID := f.insertItem(t, f.openWorkspace, "Parent", nil, 0)
	childID := f.insertItem(t, f.restricted, "Restricted child", &parentID, 0)

	err := f.application.Delete(AuditActor{UserID: f.callerID}, parentID)
	if !errors.Is(err, ErrItemCascadeForbidden) {
		t.Fatalf("Delete error = %v, want ErrItemCascadeForbidden", err)
	}

	var rootCount, childParent int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM items WHERE id = ?`, parentID).Scan(&rootCount); err != nil {
		t.Fatalf("count root: %v", err)
	}
	if rootCount != 1 {
		t.Fatal("refused delete removed the root item")
	}
	if err := f.db.QueryRow(`SELECT parent_id FROM items WHERE id = ?`, childID).Scan(&childParent); err != nil {
		t.Fatalf("load child: %v", err)
	}
	if childParent != parentID {
		t.Fatalf("child parent = %d, want %d (child must stay attached)", childParent, parentID)
	}
}

func TestSingleDeleteDetachesChildWhenCallerHasChildWorkspacePermission(t *testing.T) {
	f := newHierarchyCountFixture(t)
	parentID := f.insertItem(t, f.openWorkspace, "Parent", nil, 0)
	childID := f.insertItem(t, f.restricted, "Child", &parentID, 0)
	assignWorkspaceRoleByName(t, f.db, f.callerID, f.restricted, "Administrator")

	if err := f.application.Delete(AuditActor{UserID: f.callerID}, parentID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	var rootCount int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM items WHERE id = ?`, parentID).Scan(&rootCount); err != nil {
		t.Fatalf("count root: %v", err)
	}
	if rootCount != 0 {
		t.Fatal("authorized delete left the root item behind")
	}
	var childParent *int
	if err := f.db.QueryRow(`SELECT parent_id FROM items WHERE id = ?`, childID).Scan(&childParent); err != nil {
		t.Fatalf("load child: %v", err)
	}
	if childParent != nil {
		t.Fatalf("child parent = %v, want NULL after detach", childParent)
	}
}

func TestWorkspaceMoveRefusesCrossWorkspaceChildWithoutPermission(t *testing.T) {
	f := newHierarchyCountFixture(t)
	parentID := f.insertItem(t, f.openWorkspace, "Parent", nil, 0)
	childID := f.insertItem(t, f.restricted, "Restricted child", &parentID, 0)

	destination, err := NewWorkspaceService(f.db).Create(t.Context(), CreateWorkspaceParams{
		Name:      "Move Destination",
		Key:       "MDST",
		CreatorID: f.callerID,
	})
	if err != nil {
		t.Fatalf("create destination workspace: %v", err)
	}

	_, err = f.application.MoveWorkspace(t.Context(), AuditActor{UserID: f.callerID}, parentID,
		ItemWorkspaceMoveInput{DestinationWorkspaceID: destination.Workspace.ID})
	if !errors.Is(err, ErrItemForbidden) {
		t.Fatalf("MoveWorkspace error = %v, want ErrItemForbidden", err)
	}

	var workspaceID int
	if err := f.db.QueryRow(`SELECT workspace_id FROM items WHERE id = ?`, parentID).Scan(&workspaceID); err != nil {
		t.Fatalf("load root workspace: %v", err)
	}
	if workspaceID != f.openWorkspace {
		t.Fatalf("root workspace = %d, want %d (move must be refused)", workspaceID, f.openWorkspace)
	}
	var childParent int
	if err := f.db.QueryRow(`SELECT parent_id FROM items WHERE id = ?`, childID).Scan(&childParent); err != nil {
		t.Fatalf("load child: %v", err)
	}
	if childParent != parentID {
		t.Fatalf("child parent = %d, want %d (child must stay attached)", childParent, parentID)
	}
}

// TestWorkspaceMoveRefusesChildWorkspaceWithoutItemEdit pins the permission
// level: a workspace role that can view but not edit the child must not be
// enough to detach it.
func TestWorkspaceMoveRefusesChildWorkspaceWithoutItemEdit(t *testing.T) {
	f := newHierarchyCountFixture(t)
	parentID := f.insertItem(t, f.openWorkspace, "Parent", nil, 0)
	f.insertItem(t, f.restricted, "Restricted child", &parentID, 0)

	destination, err := NewWorkspaceService(f.db).Create(t.Context(), CreateWorkspaceParams{
		Name:      "Move Destination",
		Key:       "MDST",
		CreatorID: f.callerID,
	})
	if err != nil {
		t.Fatalf("create destination workspace: %v", err)
	}

	// Give the caller the same workspace role the fixture already gives the
	// other user: enough to make it non-open, not enough to edit the child.
	assignWorkspaceRoleByName(t, f.db, f.callerID, f.restricted, "Viewer")

	_, err = f.application.MoveWorkspace(t.Context(), AuditActor{UserID: f.callerID}, parentID,
		ItemWorkspaceMoveInput{DestinationWorkspaceID: destination.Workspace.ID})
	if !errors.Is(err, ErrItemForbidden) {
		t.Fatalf("MoveWorkspace error = %v, want ErrItemForbidden", err)
	}
}
