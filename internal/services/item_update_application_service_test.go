//go:build test

package services

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/testutils"
	"windshift/internal/validation"
)

func TestItemUpdateData_PreservesNullableAndDatePatchSemantics(t *testing.T) {
	fields := map[string]json.RawMessage{
		"priority_id": []byte("null"),
		"parent_id":   []byte("42"),
		"due_date":    []byte(`"2026-08-07T00:00:00Z"`),
		"title":       []byte(`"New title"`),
	}

	data, err := itemUpdateData(fields)
	if err != nil {
		t.Fatalf("itemUpdateData() error = %v", err)
	}
	if value, ok := data["priority_id"]; !ok || value != nil {
		t.Fatalf("priority_id = %#v, want explicit nil", data["priority_id"])
	}
	if value, ok := data["parent_id"]; !ok || value != 42 {
		t.Fatalf("parent_id = %#v, want 42", data["parent_id"])
	}
	if value, ok := data["due_date"].(time.Time); !ok || !value.Equal(time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("due_date = %#v, want UTC date", data["due_date"])
	}
	if data["title"] != "New title" {
		t.Fatalf("title = %#v, want exact title", data["title"])
	}
}

func TestItemUpdateApplicationServiceRejectsAssigneeWithoutWorkspaceAccess(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	testData := setupUpdateServiceTestData(t, tdb)

	deniedUserID := testutils.InsertID(t, tdb.GetDatabase(), `
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('update-denied@example.test', 'update-denied', 'Update', 'Denied', true)
	`)
	if _, err := tdb.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id)
		VALUES (?, ?, (SELECT id FROM workspace_roles WHERE name = 'Viewer'))
		ON CONFLICT (user_id, workspace_id, role_id) DO NOTHING
	`, testData.UserID, testData.WorkspaceID); err != nil {
		t.Fatalf("restrict workspace to actor: %v", err)
	}

	config := DefaultPermissionCacheConfig()
	config.WarmupOnStartup = false
	config.PreWarmActive = false
	perm, err := NewPermissionService(tdb.GetDatabase(), config)
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	t.Cleanup(func() { _ = perm.Close() })
	service := NewItemUpdateApplicationService(tdb.GetDatabase(), perm)

	_, err = service.Update(testData.UserID, "testuser", testData.ItemID, map[string]any{
		"assignee_id": deniedUserID,
	})
	var validationErr *validation.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("update error = %v, want ValidationError", err)
	}
	if validationErr.Field != "assignee_id" || validationErr.Message != "Assignee user not found" {
		t.Fatalf("validation error = %#v, want assignee_id/Assignee user not found", validationErr)
	}

	var assigneeID *int
	if err := tdb.QueryRow(`SELECT assignee_id FROM items WHERE id = ?`, testData.ItemID).Scan(&assigneeID); err != nil {
		t.Fatalf("load persisted assignee: %v", err)
	}
	if assigneeID != nil {
		t.Fatalf("persisted assignee = %v, want nil", assigneeID)
	}
}

func TestItemUpdateApplicationService_UpdateJSONFieldsRejectsStatusMutation(t *testing.T) {
	service := &ItemUpdateApplicationService{}
	_, err := service.UpdateJSONFields(1, "actor", 2, map[string]json.RawMessage{
		"status_id": []byte("3"),
	})
	if err == nil {
		t.Fatal("status update succeeded")
	}
	validationErr, ok := err.(*validation.ValidationError)
	if !ok || validationErr.Field != "status_id" {
		t.Fatalf("error = %#v, want status_id validation error", err)
	}
}

type recordingItemUpdatedEmitter struct {
	calls         int
	original      *models.Item
	updated       *models.Item
	actorUserID   int
	actorUsername string
	fieldChanges  []HistoryEntry
}

func (e *recordingItemUpdatedEmitter) EmitItemUpdated(
	original, updated *models.Item,
	_ bool,
	_ bool,
	actorUserID int,
	fieldChanges []HistoryEntry,
	actorUsername ...string,
) {
	e.calls++
	e.original = original
	e.updated = updated
	e.actorUserID = actorUserID
	e.fieldChanges = fieldChanges
	if len(actorUsername) > 0 {
		e.actorUsername = actorUsername[0]
	}
}

func TestItemUpdateApplicationService_PersistsAndEmitsCommittedItemOnce(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	testData := setupUpdateServiceTestData(t, tdb)

	perm, err := NewPermissionService(tdb.GetDatabase(), DefaultPermissionCacheConfig())
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	emitter := &recordingItemUpdatedEmitter{}
	service := NewItemUpdateApplicationService(tdb.GetDatabase(), perm)
	service.SetEmitter(emitter)

	result, err := service.Update(testData.UserID, "testuser", testData.ItemID, map[string]interface{}{
		"title": "Application service update",
	})
	if err != nil {
		t.Fatalf("update item: %v", err)
	}
	if result.Item.Title != "Application service update" {
		t.Fatalf("updated title = %q", result.Item.Title)
	}
	if emitter.calls != 1 {
		t.Fatalf("updated-item event calls = %d, want 1", emitter.calls)
	}
	if emitter.original == nil || emitter.original.Title == result.Item.Title {
		t.Fatalf("original item = %+v, want pre-update title", emitter.original)
	}
	if emitter.updated == nil || emitter.updated.ID != testData.ItemID || emitter.updated.Title != result.Item.Title {
		t.Fatalf("emitted updated item = %+v", emitter.updated)
	}
	if emitter.actorUserID != testData.UserID || emitter.actorUsername != "testuser" {
		t.Fatalf("emitted actor = %d/%q", emitter.actorUserID, emitter.actorUsername)
	}
	if len(emitter.fieldChanges) != 1 || emitter.fieldChanges[0].FieldName != "title" {
		t.Fatalf("emitted field changes = %+v", emitter.fieldChanges)
	}

	_, err = service.Update(testData.UserID, "testuser", testData.ItemID, map[string]interface{}{
		"title": "   ",
	})
	if err == nil {
		t.Fatal("empty title update succeeded")
	}
	if emitter.calls != 1 {
		t.Fatalf("failed update emitted event; calls = %d", emitter.calls)
	}
}
