//go:build test

package services

import (
	"errors"
	"testing"

	"windshift/internal/models"
)

type relatedItemMaskPermissionChecker struct {
	allowed map[int]bool
	errors  map[int]error
	calls   map[int]int
}

func (c *relatedItemMaskPermissionChecker) HasWorkspacePermission(_, workspaceID int, _ string) (bool, error) {
	c.calls[workspaceID]++
	return c.allowed[workspaceID], c.errors[workspaceID]
}

func TestMaskInaccessibleRelatedWorkItems(t *testing.T) {
	allowedID := 41
	deniedID := 42
	errorID := 43
	missingWorkspaceID := 0
	items := []models.Item{
		relatedItemFixture(1, allowedID, "Allowed"),
		relatedItemFixture(2, deniedID, "Denied"),
		relatedItemFixture(3, deniedID, "Also denied"),
		relatedItemFixture(4, errorID, "Permission error"),
		relatedItemFixture(5, missingWorkspaceID, "Missing workspace"),
		{Title: "Unrelated"},
	}
	checker := &relatedItemMaskPermissionChecker{
		allowed: map[int]bool{allowedID: true},
		errors:  map[int]error{errorID: errors.New("permission backend unavailable")},
		calls:   map[int]int{},
	}

	MaskInaccessibleRelatedWorkItems(7, items, checker)

	if items[0].RelatedWorkItemID == nil || items[0].RelatedWorkItemTitle != "Allowed" {
		t.Fatalf("allowed related item was masked: %+v", items[0])
	}
	for i := 1; i <= 4; i++ {
		if items[i].RelatedWorkItemID != nil || items[i].RelatedWorkItemTitle != "" ||
			items[i].RelatedWorkItemWorkspaceKey != "" || items[i].RelatedWorkItemWorkspaceID != 0 ||
			items[i].RelatedWorkItemNumber != 0 {
			t.Fatalf("item %d retained inaccessible related metadata: %+v", i, items[i])
		}
	}
	if items[5].Title != "Unrelated" {
		t.Fatalf("unrelated item changed: %+v", items[5])
	}
	if checker.calls[allowedID] != 1 || checker.calls[deniedID] != 1 || checker.calls[errorID] != 1 {
		t.Fatalf("permission calls = %v, want one per referenced workspace", checker.calls)
	}
}

func TestMaskInaccessibleRelatedWorkItemsFailsClosedWithoutChecker(t *testing.T) {
	items := []models.Item{relatedItemFixture(1, 41, "Private")}

	MaskInaccessibleRelatedWorkItems(7, items, nil)

	if items[0].RelatedWorkItemID != nil || items[0].RelatedWorkItemTitle != "" {
		t.Fatalf("related metadata survived without a permission checker: %+v", items[0])
	}
}

func relatedItemFixture(itemID, workspaceID int, title string) models.Item {
	return models.Item{
		RelatedWorkItemID:           &itemID,
		RelatedWorkItemTitle:        title,
		RelatedWorkItemWorkspaceKey: "PRIVATE",
		RelatedWorkItemWorkspaceID:  workspaceID,
		RelatedWorkItemNumber:       9,
	}
}
