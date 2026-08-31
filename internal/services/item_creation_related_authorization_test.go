//go:build test

package services

import (
	"errors"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

func TestItemCreationServiceRejectsInaccessibleRelatedWorkItem(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	var personalWorkspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key, active, is_personal, owner_id)
		VALUES ('Personal', 'PERSONAL-CREATE-AUTHZ', true, true, ?)
		RETURNING id
	`, data.UserID).Scan(&personalWorkspaceID); err != nil {
		t.Fatalf("create personal workspace: %v", err)
	}

	var privateWorkspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key, active)
		VALUES ('Private', 'PRIVATE-CREATE-AUTHZ', true)
		RETURNING id
	`).Scan(&privateWorkspaceID); err != nil {
		t.Fatalf("create private workspace: %v", err)
	}

	privateItemID64, err := CreateItem(tdb.GetDatabase(), ItemCreationParams{
		WorkspaceID: privateWorkspaceID,
		Title:       "Private item",
	})
	if err != nil {
		t.Fatalf("create private item: %v", err)
	}
	privateItemID := int(privateItemID64)

	permissions, err := NewPermissionService(tdb.GetDatabase(), PermissionCacheConfig{
		TTL:          time.Minute,
		MaxCacheSize: 8,
		BatchSize:    10,
	})
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	t.Cleanup(func() { _ = permissions.Close() })
	now := time.Now()
	if err := permissions.storeUserPermissionCache(data.UserID, &models.UserPermissionCache{
		UserID:               data.UserID,
		WorkspacePermissions: map[int]map[string]bool{},
		CachedAt:             now,
		ExpiresAt:            now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("store permission snapshot: %v", err)
	}
	service := NewItemCreationService(tdb.GetDatabase(), permissions)

	create := func(relatedItemID int) error {
		_, createErr := service.Create(data.UserID, "testuser", ItemCreateInput{
			WorkspaceID:       personalWorkspaceID,
			Title:             "Personal task",
			IsTask:            true,
			RelatedWorkItemID: &relatedItemID,
		})
		return createErr
	}

	assertDenied := func(t *testing.T, err error) {
		t.Helper()
		var validationErr *ItemCreationValidationError
		if !errors.As(err, &validationErr) {
			t.Fatalf("error = %v, want ItemCreationValidationError", err)
		}
		if validationErr.Message != "related_work_item_id: Related work item not found or access denied" {
			t.Fatalf("message = %q", validationErr.Message)
		}
	}

	t.Run("private item", func(t *testing.T) {
		assertDenied(t, create(privateItemID))
	})

	t.Run("missing item has the same denial", func(t *testing.T) {
		assertDenied(t, create(privateItemID+100000))
	})

	var created int
	if err := tdb.QueryRow("SELECT COUNT(*) FROM items WHERE workspace_id = ?", personalWorkspaceID).Scan(&created); err != nil {
		t.Fatalf("count personal items: %v", err)
	}
	if created != 0 {
		t.Fatalf("created personal items = %d, want 0", created)
	}
}
