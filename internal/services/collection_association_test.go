//go:build test

package services

import (
	"errors"
	"testing"

	"windshift/internal/models"
)

// Associating a collection with a workspace is not a publishing operation, so
// it must not require public_board.manage. Actual public-sharing changes must
// still be denied for an ordinary owner.
func TestCollectionAssociationDoesNotRequirePublishingPermission(t *testing.T) {
	tdb, permissions, seed := convergenceFixture(t, false)
	service := NewCollectionApplicationService(tdb.GetDatabase(), permissions)
	actor := AuditActor{UserID: seed.UserID}

	collection, err := service.Create(actor, models.Collection{Name: "Global collection"})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}

	workspaceID := seed.WorkspaceID
	associated, err := service.Update(actor, collection.ID, CollectionUpdate{
		QLQuerySet:     true,
		QLQuery:        "status_id = 1",
		WorkspaceIDSet: true,
		WorkspaceID:    &workspaceID,
	})
	if err != nil {
		t.Fatalf("association update without public_board.manage: %v", err)
	}
	if associated.WorkspaceID == nil || *associated.WorkspaceID != workspaceID {
		t.Fatalf("workspace_id = %v, want %d", associated.WorkspaceID, workspaceID)
	}

	slug := "global-collection"
	if _, err := service.Update(actor, collection.ID, CollectionUpdate{
		IsPublicSet:   true,
		IsPublic:      true,
		PublicSlugSet: true,
		PublicSlug:    &slug,
	}); !errors.Is(err, ErrCollectionForbidden) {
		t.Fatalf("publishing update error = %v, want ErrCollectionForbidden", err)
	}
}
