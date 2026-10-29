//go:build test

package services

import (
	"errors"
	"strings"
	"testing"

	"windshift/internal/models"
)

// A collection-scoped board write must require current workspace access in
// addition to ownership, so revoking access also blocks mutations even for the
// collection's creator.
func TestCollectionBoardWriteDeniedAfterWorkspaceAccessRevoked(t *testing.T) {
	tdb, permissions, seed := convergenceFixture(t, false)
	db := tdb.GetDatabase()
	service := NewCollectionApplicationService(db, permissions)
	actor := AuditActor{UserID: seed.UserID}

	collection, err := service.Create(actor, models.Collection{Name: "Board collection", WorkspaceID: &seed.WorkspaceID})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}
	scope := BoardConfigurationScope{CollectionID: &collection.ID}

	created, err := service.PutBoardConfiguration(actor, scope, models.BoardConfigurationRequest{
		ViewSettings: boardViewSettingsOverride("board", "list"),
	})
	if err != nil {
		t.Fatalf("initial PutBoardConfiguration: %v", err)
	}

	// Revoke the creator's workspace access while retaining the account: drop
	// the explicit role and strip item.view from the default viewer role so the
	// Everyone grant no longer applies either.
	if _, err := db.Exec(`DELETE FROM user_workspace_roles WHERE user_id = ? AND workspace_id = ?`, seed.UserID, seed.WorkspaceID); err != nil {
		t.Fatalf("revoke workspace access: %v", err)
	}
	// item.view is granted to every default role and merged into the Everyone
	// set, so strip it from all roles to truly revoke access.
	if _, err := db.Exec(`
		DELETE FROM role_permissions
		WHERE permission_id = (SELECT id FROM permissions WHERE permission_key = ?)
	`, models.PermissionItemView); err != nil {
		t.Fatalf("revoke item.view: %v", err)
	}
	if err := permissions.InvalidateUserCache(seed.UserID); err != nil {
		t.Fatalf("invalidate permission cache: %v", err)
	}

	if _, err := service.PutBoardConfiguration(actor, scope, models.BoardConfigurationRequest{
		ViewSettings: boardViewSettingsOverride("board"),
	}); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("PutBoardConfiguration after revocation error = %v, want ErrCollectionNotFound", err)
	}
	if err := service.DeleteBoardConfiguration(actor, scope); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("DeleteBoardConfiguration after revocation error = %v, want ErrCollectionNotFound", err)
	}

	var views string
	if err := db.QueryRow(`SELECT view_settings FROM board_configurations WHERE id = ?`, created.ID).Scan(&views); err != nil {
		t.Fatalf("load board configuration: %v", err)
	}
	if !strings.Contains(views, "board") || !strings.Contains(views, "list") {
		t.Fatalf("view_settings = %q, want the original board/list override preserved", views)
	}
}

// A non-owner without workspace access is still denied, and an authorized
// owner can write.
func TestCollectionBoardWriteRequiresOwnershipAndAccess(t *testing.T) {
	tdb, permissions, seed := convergenceFixture(t, false)
	db := tdb.GetDatabase()
	service := NewCollectionApplicationService(db, permissions)
	actor := AuditActor{UserID: seed.UserID}

	collection, err := service.Create(actor, models.Collection{Name: "Owned board", WorkspaceID: &seed.WorkspaceID})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}
	scope := BoardConfigurationScope{CollectionID: &collection.ID}
	if _, err := service.PutBoardConfiguration(actor, scope, models.BoardConfigurationRequest{
		ViewSettings: boardViewSettingsOverride("board"),
	}); err != nil {
		t.Fatalf("authorized owner PutBoardConfiguration: %v", err)
	}

	var otherUserID int
	if err := db.QueryRow(`INSERT INTO users (email, username, first_name, last_name, is_active) VALUES ('other@example.test', 'other', 'Other', 'User', true) RETURNING id`).Scan(&otherUserID); err != nil {
		t.Fatalf("insert other user: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id)
		SELECT ?, ?, id FROM workspace_roles WHERE builtin_key = ?
	`, otherUserID, seed.WorkspaceID, models.RoleBuiltinAdministrator); err != nil {
		t.Fatalf("grant other user workspace access: %v", err)
	}
	if err := permissions.InvalidateUserCache(otherUserID); err != nil {
		t.Fatalf("invalidate other user cache: %v", err)
	}

	if _, err := service.PutBoardConfiguration(AuditActor{UserID: otherUserID}, scope, models.BoardConfigurationRequest{
		ViewSettings: boardViewSettingsOverride("board"),
	}); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("non-owner PutBoardConfiguration error = %v, want ErrCollectionNotFound", err)
	}
}
