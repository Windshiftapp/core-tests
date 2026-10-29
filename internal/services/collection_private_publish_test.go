//go:build test

package services

import (
	"errors"
	"fmt"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
)

// A private collection must never become publicly readable. The dedicated
// sharing endpoint is the only publish path that bypassed the private override
// in Update, so it must reject an explicit publish instead of persisting an
// is_public flag the public board would never honor.
func TestUpdateSharingRejectsPublishWhilePrivate(t *testing.T) {
	tdb, permissions, seed := convergenceFixture(t, false)
	db := tdb.GetDatabase()
	service := NewCollectionApplicationService(db, permissions)
	actor := AuditActor{UserID: seed.UserID}

	grantPublicBoardManage(t, db, seed.UserID)
	if err := permissions.InvalidateUserCache(seed.UserID); err != nil {
		t.Fatalf("invalidate permission cache: %v", err)
	}

	collection, err := service.Create(actor, models.Collection{
		Name:    "Private",
		QLQuery: fmt.Sprintf("workspace_id = %d", seed.WorkspaceID),
	})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}
	if _, err := service.Update(actor, collection.ID, CollectionUpdate{IsPrivateSet: true, IsPrivate: true}); err != nil {
		t.Fatalf("mark private: %v", err)
	}

	slug := "should-not-publish"
	_, err = service.UpdateSharing(actor, collection.ID, CollectionSharingUpdate{IsPublic: true, PublicSlug: &slug})
	var validation *CollectionValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("UpdateSharing error = %v, want CollectionValidationError", err)
	}

	stored, err := service.repository.GetModel(collection.ID)
	if err != nil {
		t.Fatalf("reload collection: %v", err)
	}
	if stored.IsPublic {
		t.Fatal("private collection was published")
	}
	if stored.PublicSlug != nil {
		t.Fatalf("public_slug = %v, want nil", *stored.PublicSlug)
	}
	if !stored.IsPrivate {
		t.Fatal("collection lost its private flag")
	}
}

// A private collection that somehow carries a public slug must have it cleared
// when sharing is updated. The API cannot create that state, so seed it
// directly to prove the service repairs it.
func TestUpdateSharingClearsSlugOnPrivateCollection(t *testing.T) {
	tdb, permissions, seed := convergenceFixture(t, false)
	db := tdb.GetDatabase()
	service := NewCollectionApplicationService(db, permissions)
	actor := AuditActor{UserID: seed.UserID}

	grantPublicBoardManage(t, db, seed.UserID)
	if err := permissions.InvalidateUserCache(seed.UserID); err != nil {
		t.Fatalf("invalidate permission cache: %v", err)
	}

	collection, err := service.Create(actor, models.Collection{
		Name:    "Private with stray slug",
		QLQuery: fmt.Sprintf("workspace_id = %d", seed.WorkspaceID),
	})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}
	if _, err := service.Update(actor, collection.ID, CollectionUpdate{IsPrivateSet: true, IsPrivate: true}); err != nil {
		t.Fatalf("mark private: %v", err)
	}
	if _, err := db.ExecWrite(`UPDATE collections SET public_slug = ? WHERE id = ?`, "stray-slug", collection.ID); err != nil {
		t.Fatalf("seed stray slug: %v", err)
	}

	stray := "stray-slug"
	if _, err := service.UpdateSharing(actor, collection.ID, CollectionSharingUpdate{IsPublic: false, PublicSlug: &stray}); err != nil {
		t.Fatalf("UpdateSharing unpublish: %v", err)
	}

	stored, err := service.repository.GetModel(collection.ID)
	if err != nil {
		t.Fatalf("reload collection: %v", err)
	}
	if stored.PublicSlug != nil {
		t.Fatalf("public_slug = %v, want nil", *stored.PublicSlug)
	}
	if stored.IsPublic {
		t.Fatal("collection is still marked public")
	}
}

func grantPublicBoardManage(t *testing.T, db database.Database, userID int) {
	t.Helper()
	permissionRepo := repository.NewPermissionRepository(db)
	all, err := permissionRepo.ListAll()
	if err != nil {
		t.Fatalf("list permissions: %v", err)
	}
	for _, permission := range all {
		if permission.PermissionKey == models.PermissionPublicBoardManage {
			if err := permissionRepo.GrantGlobalToUser(userID, permission.ID, userID); err != nil {
				t.Fatalf("grant public_board.manage: %v", err)
			}
			return
		}
	}
	t.Fatal("public_board.manage permission not found")
}
