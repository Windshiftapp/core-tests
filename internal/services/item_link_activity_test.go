//go:build test

package services

import (
	"encoding/json"
	"testing"

	"windshift/internal/database"
	"windshift/internal/itemevents"
)

const itemLinkActivitySentinel = "2000-01-01 00:00:00"

type itemLinkActivityBaseline struct {
	updatedAt    string
	lastActiveAt string
}

func stampLinkItemActivity(t *testing.T, db database.Database, itemIDs ...int) (int64, map[int]itemLinkActivityBaseline) {
	t.Helper()
	baselines := make(map[int]itemLinkActivityBaseline, len(itemIDs))
	for _, itemID := range itemIDs {
		if _, err := db.ExecWrite(
			"UPDATE items SET updated_at = ?, last_active_at = ? WHERE id = ?",
			itemLinkActivitySentinel, itemLinkActivitySentinel, itemID,
		); err != nil {
			t.Fatalf("stamp item %d activity: %v", itemID, err)
		}
		var baseline itemLinkActivityBaseline
		if err := db.QueryRow(
			"SELECT updated_at, last_active_at FROM items WHERE id = ?", itemID,
		).Scan(&baseline.updatedAt, &baseline.lastActiveAt); err != nil {
			t.Fatalf("read item %d activity baseline: %v", itemID, err)
		}
		baselines[itemID] = baseline
	}
	var watermark int64
	if err := db.QueryRow("SELECT COALESCE(MAX(id), 0) FROM item_change_log").Scan(&watermark); err != nil {
		t.Fatalf("read change watermark: %v", err)
	}
	return watermark, baselines
}

func assertLinkItemTouched(t *testing.T, db database.Database, itemID int, watermark int64, baseline itemLinkActivityBaseline) {
	t.Helper()
	var updatedAt, lastActiveAt string
	if err := db.QueryRow(
		"SELECT updated_at, last_active_at FROM items WHERE id = ?", itemID,
	).Scan(&updatedAt, &lastActiveAt); err != nil {
		t.Fatalf("read item %d activity: %v", itemID, err)
	}
	if updatedAt == baseline.updatedAt {
		t.Errorf("item %d updated_at was not bumped", itemID)
	}
	if lastActiveAt == baseline.lastActiveAt {
		t.Errorf("item %d last_active_at was not bumped", itemID)
	}

	var changes int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM item_change_log WHERE item_id = ? AND id > ?",
		itemID, watermark,
	).Scan(&changes); err != nil {
		t.Fatalf("count item %d changes: %v", itemID, err)
	}
	if changes != 1 {
		t.Errorf("item %d change rows = %d, want 1", itemID, changes)
	}
}

func assertCanonicalLinkEvent(t *testing.T, db database.Database, itemID int, eventType, direction string, otherID int) {
	t.Helper()
	var storedType, actorKind, actorRef, payloadJSON string
	if err := db.QueryRow(`
		SELECT event_type, actor_kind, actor_ref, payload
		FROM domain_events
		WHERE aggregate_type = 'item' AND aggregate_id = ?
		ORDER BY aggregate_sequence DESC LIMIT 1
	`, itemID).Scan(&storedType, &actorKind, &actorRef, &payloadJSON); err != nil {
		t.Fatalf("load canonical link event for item %d: %v", itemID, err)
	}
	var payload itemevents.LinkChangedV1
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("decode canonical link event: %v", err)
	}
	if storedType != eventType || actorKind != "user" || actorRef == "" || payload.ItemID != itemID || payload.Direction != direction || payload.OtherID != otherID {
		t.Fatalf("link event = type:%s actor:%s/%s payload:%+v", storedType, actorKind, actorRef, payload)
	}
}

func newItemLinkActivityFixture(t *testing.T) (database.Database, *ItemLinkService, int, int, int, int) {
	t.Helper()
	db := newItemLinkBatchTestDB(t)
	actorID := insertItemLinkBatchRow(t, db, `
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('activity@example.com', 'activity-actor', 'Activity', 'Actor')
	`)
	workspaceID := insertItemLinkBatchRow(t, db,
		`INSERT INTO workspaces (name, key, description, active) VALUES ('Link activity', 'LA', '', true)`,
	)
	sourceID := insertBatchItem(t, db, workspaceID, "Source")
	targetID := insertBatchItem(t, db, workspaceID, "Target")
	linkTypeID := insertItemLinkBatchRow(t, db, `
		INSERT INTO link_types (name, forward_label, reverse_label, active)
		VALUES ('Activity relation', 'relates to', 'relates to', true)
	`)
	permissions := &countingWorkspacePermissions{
		allowed: map[int]bool{workspaceID: true},
		calls:   map[int]int{},
	}
	service := NewItemLinkService(db).WithPermissionService(permissions)
	return db, service, actorID, sourceID, targetID, linkTypeID
}

func TestItemLinkCreateTouchesBothItemsAndEmitsChanges(t *testing.T) {
	db, service, actorID, sourceID, targetID, linkTypeID := newItemLinkActivityFixture(t)
	watermark, baselines := stampLinkItemActivity(t, db, sourceID, targetID)

	if _, err := service.CreateLinkWithChecks(actorID, CreateItemLinkParams{
		LinkTypeID: linkTypeID,
		SourceType: "item",
		SourceID:   sourceID,
		TargetType: "item",
		TargetID:   targetID,
	}); err != nil {
		t.Fatalf("CreateLinkWithChecks: %v", err)
	}

	assertLinkItemTouched(t, db, sourceID, watermark, baselines[sourceID])
	assertLinkItemTouched(t, db, targetID, watermark, baselines[targetID])
	assertCanonicalLinkEvent(t, db, sourceID, itemevents.Linked, "outgoing", targetID)
	assertCanonicalLinkEvent(t, db, targetID, itemevents.Linked, "incoming", sourceID)
}

func TestItemLinkDeleteTouchesBothItemsAndEmitsChanges(t *testing.T) {
	db, service, actorID, sourceID, targetID, linkTypeID := newItemLinkActivityFixture(t)
	linkID := insertItemLinkBatchRow(t, db, `
		INSERT INTO item_links (link_type_id, source_type, source_id, target_type, target_id, created_by)
		VALUES (?, 'item', ?, 'item', ?, ?)
	`, linkTypeID, sourceID, targetID, actorID)
	watermark, baselines := stampLinkItemActivity(t, db, sourceID, targetID)

	if err := service.DeleteLinkWithChecks(actorID, linkID); err != nil {
		t.Fatalf("DeleteLinkWithChecks: %v", err)
	}

	assertLinkItemTouched(t, db, sourceID, watermark, baselines[sourceID])
	assertLinkItemTouched(t, db, targetID, watermark, baselines[targetID])
	assertCanonicalLinkEvent(t, db, sourceID, itemevents.Unlinked, "outgoing", targetID)
	assertCanonicalLinkEvent(t, db, targetID, itemevents.Unlinked, "incoming", sourceID)
}

func TestItemLinkSingleValueReplacementTouchesOldAndNewEndpoints(t *testing.T) {
	db, service, actorID, sourceID, oldTargetID, linkTypeID := newItemLinkActivityFixture(t)
	var workspaceID int
	if err := db.QueryRow("SELECT workspace_id FROM items WHERE id = ?", sourceID).Scan(&workspaceID); err != nil {
		t.Fatalf("load source workspace: %v", err)
	}
	newTargetID := insertBatchItem(t, db, workspaceID, "New target")
	fieldID := insertItemLinkBatchRow(t, db,
		`INSERT INTO custom_field_definitions (name, field_type) VALUES ('Related item', 'linking')`,
	)
	insertItemLinkBatchRow(t, db, `
		INSERT INTO item_links (link_type_id, source_type, source_id, target_type, target_id, created_by, custom_field_id)
		VALUES (?, 'item', ?, 'item', ?, ?, ?)
	`, linkTypeID, sourceID, oldTargetID, actorID, fieldID)
	watermark, baselines := stampLinkItemActivity(t, db, sourceID, oldTargetID, newTargetID)

	if _, err := service.ReplaceSingleValueFieldLinkWithChecks(actorID, CreateItemLinkParams{
		LinkTypeID:    linkTypeID,
		SourceType:    "item",
		SourceID:      sourceID,
		TargetType:    "item",
		TargetID:      newTargetID,
		CustomFieldID: &fieldID,
	}); err != nil {
		t.Fatalf("ReplaceSingleValueFieldLinkWithChecks: %v", err)
	}

	assertLinkItemTouched(t, db, sourceID, watermark, baselines[sourceID])
	assertLinkItemTouched(t, db, oldTargetID, watermark, baselines[oldTargetID])
	assertLinkItemTouched(t, db, newTargetID, watermark, baselines[newTargetID])
}
