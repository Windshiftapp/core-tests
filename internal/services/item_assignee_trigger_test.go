//go:build test

package services

import (
	"context"
	"testing"

	"windshift/internal/testutils"
)

// stubAssigneeTrigger records every forwarded trigger call so tests can
// assert which item writes reach the coding-agent binding trigger.
type stubAssigneeTrigger struct {
	calls []assigneeTriggerCall
}

type assigneeTriggerCall struct {
	WorkspaceID int
	ItemID      int
	OldAssignee *int
	NewAssignee *int
	TriggeredBy int
}

func (s *stubAssigneeTrigger) MaybeStartRunForAssignee(_ context.Context, workspaceID, itemID int, oldAssignee, newAssignee *int, triggeredByUserID int) error {
	s.calls = append(s.calls, assigneeTriggerCall{
		WorkspaceID: workspaceID,
		ItemID:      itemID,
		OldAssignee: oldAssignee,
		NewAssignee: newAssignee,
		TriggeredBy: triggeredByUserID,
	})
	return nil
}

func installStubTrigger(t *testing.T) *stubAssigneeTrigger {
	t.Helper()
	stub := &stubAssigneeTrigger{}
	SetItemAssigneeTrigger(stub)
	t.Cleanup(func() { SetItemAssigneeTrigger(nil) })
	return stub
}

// TestCreateItemFiresAssigneeTrigger covers the regression where an item
// created with an agent assignee already set never started a run: only the
// update path fired the trigger.
func TestCreateItemFiresAssigneeTrigger(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	testData := setupUpdateServiceTestData(t, tdb)

	var itemTypeID int
	if err := tdb.DB.QueryRow("SELECT id FROM item_types LIMIT 1").Scan(&itemTypeID); err != nil {
		t.Fatalf("Failed to get item type: %v", err)
	}

	t.Run("CreateWithAssignee", func(t *testing.T) {
		stub := installStubTrigger(t)

		itemID, err := CreateItem(tdb.GetDatabase(), ItemCreationParams{
			WorkspaceID: testData.WorkspaceID,
			Title:       "Assigned at creation",
			ItemTypeID:  &itemTypeID,
			AssigneeID:  &testData.UserID,
			CreatorID:   &testData.UserID,
		})
		if err != nil {
			t.Fatalf("CreateItem failed: %v", err)
		}

		if len(stub.calls) != 1 {
			t.Fatalf("expected 1 trigger call, got %d", len(stub.calls))
		}
		call := stub.calls[0]
		if call.ItemID != int(itemID) || call.WorkspaceID != testData.WorkspaceID {
			t.Errorf("trigger fired for item=%d ws=%d, want item=%d ws=%d", call.ItemID, call.WorkspaceID, itemID, testData.WorkspaceID)
		}
		if call.OldAssignee != nil {
			t.Errorf("old assignee should be nil on create, got %v", *call.OldAssignee)
		}
		if call.NewAssignee == nil || *call.NewAssignee != testData.UserID {
			t.Errorf("new assignee should be %d, got %v", testData.UserID, call.NewAssignee)
		}
		if call.TriggeredBy != testData.UserID {
			t.Errorf("triggeredBy should fall back to creator %d, got %d", testData.UserID, call.TriggeredBy)
		}
	})

	t.Run("CreateWithoutAssignee", func(t *testing.T) {
		stub := installStubTrigger(t)

		if _, err := CreateItem(tdb.GetDatabase(), ItemCreationParams{
			WorkspaceID: testData.WorkspaceID,
			Title:       "Unassigned",
			ItemTypeID:  &itemTypeID,
			CreatorID:   &testData.UserID,
		}); err != nil {
			t.Fatalf("CreateItem failed: %v", err)
		}

		if len(stub.calls) != 0 {
			t.Fatalf("expected no trigger calls for unassigned create, got %d", len(stub.calls))
		}
	})

	t.Run("SkipAssigneeTrigger", func(t *testing.T) {
		stub := installStubTrigger(t)

		if _, err := CreateItem(tdb.GetDatabase(), ItemCreationParams{
			WorkspaceID:         testData.WorkspaceID,
			Title:               "Bulk imported",
			ItemTypeID:          &itemTypeID,
			AssigneeID:          &testData.UserID,
			CreatorID:           &testData.UserID,
			SkipAssigneeTrigger: true,
		}); err != nil {
			t.Fatalf("CreateItem failed: %v", err)
		}

		if len(stub.calls) != 0 {
			t.Fatalf("expected no trigger calls with SkipAssigneeTrigger, got %d", len(stub.calls))
		}
	})
}

// TestUpdateItemFiresAssigneeTrigger asserts the trigger now lives inside
// ItemUpdateService.UpdateItem, so every update surface (cookie handlers,
// REST v1, MCP tools, automation actions) inherits it.
func TestUpdateItemFiresAssigneeTrigger(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	service := NewItemUpdateService(tdb.GetDatabase())
	testData := setupUpdateServiceTestData(t, tdb)

	t.Run("AssigneeSet", func(t *testing.T) {
		stub := installStubTrigger(t)

		result, err := service.UpdateItem(UpdateItemRequest{
			ItemID:     testData.ItemID,
			UpdateData: map[string]interface{}{"assignee_id": testData.UserID},
			UserID:     testData.UserID,
		})
		if err != nil {
			t.Fatalf("UpdateItem failed: %v", err)
		}

		if len(stub.calls) != 1 {
			t.Fatalf("expected 1 trigger call, got %d", len(stub.calls))
		}
		call := stub.calls[0]
		if call.ItemID != result.Item.ID || call.WorkspaceID != testData.WorkspaceID {
			t.Errorf("trigger fired for item=%d ws=%d, want item=%d ws=%d", call.ItemID, call.WorkspaceID, result.Item.ID, testData.WorkspaceID)
		}
		if call.OldAssignee != nil {
			t.Errorf("old assignee should be nil, got %v", *call.OldAssignee)
		}
		if call.NewAssignee == nil || *call.NewAssignee != testData.UserID {
			t.Errorf("new assignee should be %d, got %v", testData.UserID, call.NewAssignee)
		}
		if call.TriggeredBy != testData.UserID {
			t.Errorf("triggeredBy should be the updating user %d, got %d", testData.UserID, call.TriggeredBy)
		}
	})

	t.Run("AssigneeCleared", func(t *testing.T) {
		stub := installStubTrigger(t)

		if _, err := service.UpdateItem(UpdateItemRequest{
			ItemID:     testData.ItemID,
			UpdateData: map[string]interface{}{"assignee_id": nil},
			UserID:     testData.UserID,
		}); err != nil {
			t.Fatalf("UpdateItem failed: %v", err)
		}

		// Clearing the assignee must not reach the trigger at all
		// (maybeTriggerAssigneeRun guards newAssignee == nil).
		if len(stub.calls) != 0 {
			t.Fatalf("expected no trigger calls when clearing assignee, got %d", len(stub.calls))
		}
	})

	t.Run("UnrelatedFieldChange", func(t *testing.T) {
		stub := installStubTrigger(t)

		if _, err := service.UpdateItem(UpdateItemRequest{
			ItemID:     testData.ItemID,
			UpdateData: map[string]interface{}{"title": "Renamed"},
			UserID:     testData.UserID,
		}); err != nil {
			t.Fatalf("UpdateItem failed: %v", err)
		}

		if len(stub.calls) != 0 {
			t.Fatalf("expected no trigger calls for title-only update on unassigned item, got %d", len(stub.calls))
		}
	})
}
