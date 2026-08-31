//go:build test

package services

import (
	"encoding/json"
	"errors"
	"testing"

	"windshift/internal/itemevents"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

type recordingItemDeletedEmitter struct {
	calls           int
	item            *models.Item
	actorUserID     int
	actorUsername   string
	descendantCount int
}

func (e *recordingItemDeletedEmitter) EmitItemDeleted(item *models.Item, actorUserID, descendantCount int, actorUsername ...string) {
	e.calls++
	e.item = item
	e.actorUserID = actorUserID
	e.descendantCount = descendantCount
	if len(actorUsername) > 0 {
		e.actorUsername = actorUsername[0]
	}
}

func TestItemDeletionApplicationService_RequiresDeletePermissionAndEmitsCascadeOnce(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	perm, err := NewPermissionService(tdb.GetDatabase(), DefaultPermissionCacheConfig())
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}

	var parentTypeID, childTypeID int
	if err := tdb.QueryRow(`
		SELECT id FROM item_types
		WHERE hierarchy_level >= 0
		ORDER BY hierarchy_level, id LIMIT 1
	`).Scan(&parentTypeID); err != nil {
		t.Fatalf("load parent item type: %v", err)
	}
	if err := tdb.QueryRow(`
		SELECT id FROM item_types
		WHERE hierarchy_level > (SELECT hierarchy_level FROM item_types WHERE id = ?)
		ORDER BY hierarchy_level, id LIMIT 1
	`, parentTypeID).Scan(&childTypeID); err != nil {
		t.Fatalf("load child item type: %v", err)
	}

	parentID64, err := CreateItem(tdb.GetDatabase(), ItemCreationParams{
		WorkspaceID:      data.WorkspaceID,
		Title:            "Deletion parent",
		ItemTypeID:       &parentTypeID,
		CreatorID:        &data.UserID,
		ValidatingUserID: data.UserID,
		PermService:      perm,
	})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	parentID := int(parentID64)
	childID64, err := CreateItem(tdb.GetDatabase(), ItemCreationParams{
		WorkspaceID:      data.WorkspaceID,
		Title:            "Deletion child",
		ItemTypeID:       &childTypeID,
		ParentID:         &parentID,
		CreatorID:        &data.UserID,
		ValidatingUserID: data.UserID,
		PermService:      perm,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	childID := int(childID64)

	var viewerID int
	if err := tdb.QueryRow(`
		INSERT INTO users (username, email, first_name, last_name, password_hash, is_active)
		VALUES ('delete_viewer', 'delete_viewer@test.com', 'Delete', 'Viewer', 'hash', true)
		RETURNING id
	`).Scan(&viewerID); err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	var viewerRoleID int
	if err := tdb.QueryRow(`SELECT id FROM workspace_roles WHERE name = 'Viewer'`).Scan(&viewerRoleID); err != nil {
		t.Fatalf("load Viewer role: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
	`, viewerID, data.WorkspaceID, viewerRoleID); err != nil {
		t.Fatalf("assign Viewer role: %v", err)
	}

	emitter := &recordingItemDeletedEmitter{}
	service := NewItemDeletionApplicationService(tdb.GetDatabase(), perm)
	service.SetEmitter(emitter)

	_, err = service.Delete(ItemDeletionRequest{
		ItemID:        parentID,
		ActorUserID:   viewerID,
		ActorUsername: "delete_viewer",
		Mode:          ItemDeletionCascade,
	})
	if !errors.Is(err, ErrItemDeletionForbidden) {
		t.Fatalf("viewer delete error = %v, want ErrItemDeletionForbidden", err)
	}
	if emitter.calls != 0 {
		t.Fatalf("denied deletion emitted %d events", emitter.calls)
	}
	if _, err := repository.NewItemRepository(tdb.GetDatabase()).FindByID(childID); err != nil {
		t.Fatalf("denied deletion removed child: %v", err)
	}

	result, err := service.Delete(ItemDeletionRequest{
		ItemID:        parentID,
		ActorUserID:   data.UserID,
		ActorUsername: "testuser",
		Mode:          ItemDeletionCascade,
	})
	if err != nil {
		t.Fatalf("administrator cascade delete: %v", err)
	}
	if result.DeletedCount != 2 || result.DescendantCount != 1 {
		t.Fatalf("delete count/descendants = %d/%d, want 2/1", result.DeletedCount, result.DescendantCount)
	}
	if emitter.calls != 1 || emitter.item == nil || emitter.item.ID != parentID ||
		emitter.actorUserID != data.UserID || emitter.actorUsername != "testuser" || emitter.descendantCount != 1 {
		t.Fatalf("delete event = calls:%d item:%+v actor:%d/%q descendants:%d",
			emitter.calls, emitter.item, emitter.actorUserID, emitter.actorUsername, emitter.descendantCount)
	}
	for _, itemID := range []int{parentID, childID} {
		if _, err := repository.NewItemRepository(tdb.GetDatabase()).FindByID(itemID); !errors.Is(err, repository.ErrNotFound) {
			t.Fatalf("deleted item %d lookup error = %v, want not found", itemID, err)
		}
	}
	for _, expected := range []struct {
		itemID, descendants int
	}{{parentID, 1}, {childID, 0}} {
		var eventType, actorKind, actorRef, payloadJSON string
		if err := tdb.QueryRow(`
			SELECT event_type, actor_kind, actor_ref, payload
			FROM domain_events
			WHERE aggregate_type = 'item' AND aggregate_id = ?
			ORDER BY aggregate_sequence DESC LIMIT 1
		`, expected.itemID).Scan(&eventType, &actorKind, &actorRef, &payloadJSON); err != nil {
			t.Fatalf("load deleted event for item %d: %v", expected.itemID, err)
		}
		var payload itemevents.DeletedV1
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			t.Fatalf("decode deleted event for item %d: %v", expected.itemID, err)
		}
		if eventType != itemevents.Deleted || actorKind != "user" || actorRef != "1" || payload.Item.ID != expected.itemID || payload.DescendantCount != expected.descendants {
			t.Fatalf("deleted event for item %d = %s %s/%s %+v", expected.itemID, eventType, actorKind, actorRef, payload)
		}
	}
}
