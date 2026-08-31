//go:build test

package validation_test

import (
	"errors"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
	"windshift/internal/validation"
)

type relatedItemPermissionChecker struct {
	allowed bool
	err     error
	calls   int
}

func (c *relatedItemPermissionChecker) HasWorkspacePermission(_, _ int, _ string) (bool, error) {
	c.calls++
	return c.allowed, c.err
}

func TestRelatedWorkItemUpdateRequiresViewPermission(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	var personalWorkspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key, active, is_personal, owner_id)
		VALUES ('Personal', 'PERSONAL-AUTHZ', true, true, ?)
		RETURNING id
	`, data.UserID).Scan(&personalWorkspaceID); err != nil {
		t.Fatalf("create personal workspace: %v", err)
	}

	var privateWorkspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key, active)
		VALUES ('Private', 'PRIVATE-AUTHZ', true)
		RETURNING id
	`).Scan(&privateWorkspaceID); err != nil {
		t.Fatalf("create private workspace: %v", err)
	}

	privateItemID, err := factory.NewTestFactory(tdb.GetDatabase()).CreateItem(factory.CreateItemOpts{
		WorkspaceID: privateWorkspaceID,
		Title:       "Private item",
	})
	if err != nil {
		t.Fatalf("create private item: %v", err)
	}

	t.Run("denied", func(t *testing.T) {
		checker := &relatedItemPermissionChecker{}
		validator := validation.NewItemFieldValidator(tdb.GetDatabase()).WithPermissionChecker(checker)
		item := &models.Item{WorkspaceID: personalWorkspaceID}

		err := validator.ValidateAndApplyUpdates(item, map[string]any{
			"related_work_item_id": privateItemID,
		}, data.UserID)

		var validationErr *validation.ValidationError
		if !errors.As(err, &validationErr) {
			t.Fatalf("error = %v, want ValidationError", err)
		}
		if validationErr.Field != "related_work_item_id" || validationErr.Message != "Related work item not found or access denied" {
			t.Fatalf("validation error = %+v", validationErr)
		}
		if item.RelatedWorkItemID != nil {
			t.Fatalf("related_work_item_id = %v, want nil", item.RelatedWorkItemID)
		}
		if checker.calls != 1 {
			t.Fatalf("permission checks = %d, want 1", checker.calls)
		}
	})

	t.Run("allowed", func(t *testing.T) {
		checker := &relatedItemPermissionChecker{allowed: true}
		validator := validation.NewItemFieldValidator(tdb.GetDatabase()).WithPermissionChecker(checker)
		item := &models.Item{WorkspaceID: personalWorkspaceID}

		err := validator.ValidateAndApplyUpdates(item, map[string]any{
			"related_work_item_id": privateItemID,
		}, data.UserID)

		if err != nil {
			t.Fatalf("ValidateAndApplyUpdates: %v", err)
		}
		if item.RelatedWorkItemID == nil || *item.RelatedWorkItemID != privateItemID {
			t.Fatalf("related_work_item_id = %v, want %d", item.RelatedWorkItemID, privateItemID)
		}
		if checker.calls != 1 {
			t.Fatalf("permission checks = %d, want 1", checker.calls)
		}
	})

	t.Run("permission error", func(t *testing.T) {
		checkerErr := errors.New("permission backend unavailable")
		checker := &relatedItemPermissionChecker{err: checkerErr}
		validator := validation.NewItemFieldValidator(tdb.GetDatabase()).WithPermissionChecker(checker)
		item := &models.Item{WorkspaceID: personalWorkspaceID}

		err := validator.ValidateAndApplyUpdates(item, map[string]any{
			"related_work_item_id": privateItemID,
		}, data.UserID)

		if !errors.Is(err, checkerErr) {
			t.Fatalf("error = %v, want permission backend error", err)
		}
		if item.RelatedWorkItemID != nil {
			t.Fatalf("related_work_item_id = %v, want nil", item.RelatedWorkItemID)
		}
	})

	t.Run("missing checker", func(t *testing.T) {
		validator := validation.NewItemFieldValidator(tdb.GetDatabase())
		item := &models.Item{WorkspaceID: personalWorkspaceID}

		err := validator.ValidateAndApplyUpdates(item, map[string]any{
			"related_work_item_id": privateItemID,
		}, data.UserID)

		var validationErr *validation.ValidationError
		if !errors.As(err, &validationErr) || validationErr.Field != "related_work_item_id" {
			t.Fatalf("error = %v, want related_work_item_id ValidationError", err)
		}
		if item.RelatedWorkItemID != nil {
			t.Fatalf("related_work_item_id = %v, want nil", item.RelatedWorkItemID)
		}
	})
}
