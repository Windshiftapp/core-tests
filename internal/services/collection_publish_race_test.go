//go:build test

package services

import (
	"errors"
	"fmt"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
)

// Publishing must be conditional on the authorized query still being current.
// A query edit between authorization and the write must not make a scope the
// actor cannot administer anonymously readable.
func TestUpdateSharingRejectsQueryChangedAfterAuthorization(t *testing.T) {
	tdb, permissions, seed := convergenceFixture(t, false)
	db := tdb.GetDatabase()
	service := NewCollectionApplicationService(db, permissions)
	actor := AuditActor{UserID: seed.UserID}

	// The actor may publish workspace A and manage public boards, but is not an
	// administrator of workspace B.
	permissionRepo := repository.NewPermissionRepository(db)
	all, err := permissionRepo.ListAll()
	if err != nil {
		t.Fatalf("list permissions: %v", err)
	}
	for _, permission := range all {
		if permission.PermissionKey == models.PermissionPublicBoardManage {
			if err := permissionRepo.GrantGlobalToUser(seed.UserID, permission.ID, seed.UserID); err != nil {
				t.Fatalf("grant public_board.manage: %v", err)
			}
		}
	}
	var workspaceBID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Workspace B', 'WSB') RETURNING id`).Scan(&workspaceBID); err != nil {
		t.Fatalf("insert workspace B: %v", err)
	}
	if err := permissions.InvalidateUserCache(seed.UserID); err != nil {
		t.Fatalf("invalidate permission cache: %v", err)
	}

	queryA := fmt.Sprintf("workspace_id = %d", seed.WorkspaceID)
	collection, err := service.Create(actor, models.Collection{Name: "Scoped", QLQuery: queryA})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}

	// Simulate a concurrent query edit to workspace B, which the actor cannot
	// publish, landing between scope authorization and the write.
	queryB := fmt.Sprintf("workspace_id = %d", workspaceBID)
	var hookErr error
	service.beforePublicSharingWrite = func() {
		_, hookErr = db.ExecWrite(`UPDATE collections SET ql_query = ? WHERE id = ?`, queryB, collection.ID)
	}
	slug := "scoped-public"
	_, err = service.UpdateSharing(actor, collection.ID, CollectionSharingUpdate{IsPublic: true, PublicSlug: &slug})
	if hookErr != nil {
		t.Fatalf("concurrent query edit: %v", hookErr)
	}
	if !errors.Is(err, ErrCollectionConflict) {
		t.Fatalf("UpdateSharing error = %v, want ErrCollectionConflict", err)
	}

	var isPublic bool
	var storedQuery string
	if err := db.QueryRow(`SELECT is_public, ql_query FROM collections WHERE id = ?`, collection.ID).Scan(&isPublic, &storedQuery); err != nil {
		t.Fatalf("reload collection: %v", err)
	}
	if isPublic {
		t.Fatal("collection was published with a query that was never authorized")
	}
	if storedQuery != queryB {
		t.Fatalf("stored query = %q, want the concurrent edit %q", storedQuery, queryB)
	}
}

// When nothing changes between authorization and the write, publishing still
// succeeds and persists the scope.
func TestUpdateSharingPublishesUnchangedScope(t *testing.T) {
	tdb, permissions, seed := convergenceFixture(t, false)
	db := tdb.GetDatabase()
	service := NewCollectionApplicationService(db, permissions)
	actor := AuditActor{UserID: seed.UserID}

	permissionRepo := repository.NewPermissionRepository(db)
	all, err := permissionRepo.ListAll()
	if err != nil {
		t.Fatalf("list permissions: %v", err)
	}
	for _, permission := range all {
		if permission.PermissionKey == models.PermissionPublicBoardManage {
			if err := permissionRepo.GrantGlobalToUser(seed.UserID, permission.ID, seed.UserID); err != nil {
				t.Fatalf("grant public_board.manage: %v", err)
			}
		}
	}
	if err := permissions.InvalidateUserCache(seed.UserID); err != nil {
		t.Fatalf("invalidate permission cache: %v", err)
	}

	collection, err := service.Create(actor, models.Collection{
		Name: "Scoped", QLQuery: fmt.Sprintf("workspace_id = %d", seed.WorkspaceID),
	})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}
	slug := "scoped-public"
	published, err := service.UpdateSharing(actor, collection.ID, CollectionSharingUpdate{IsPublic: true, PublicSlug: &slug})
	if err != nil {
		t.Fatalf("UpdateSharing: %v", err)
	}
	if !published.IsPublic {
		t.Fatal("collection was not published")
	}
}
