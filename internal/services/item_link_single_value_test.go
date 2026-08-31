package services

import (
	"testing"
)

func TestReplaceSingleValueFieldLinkRollsBackWhenCreateFails(t *testing.T) {
	db := newItemLinkBatchTestDB(t)
	actorID := insertItemLinkBatchRow(t, db, `
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('rollback@example.com', 'rollback-actor', 'Rollback', 'Actor')
	`)
	workspaceID := insertItemLinkBatchRow(t, db,
		`INSERT INTO workspaces (name, key, description, active) VALUES ('Field links', 'FL', '', true)`,
	)
	sourceID := insertBatchItem(t, db, workspaceID, "Source")
	oldTargetID := insertBatchItem(t, db, workspaceID, "Old target")
	newTargetID := insertBatchItem(t, db, workspaceID, "New target")
	fieldID := insertItemLinkBatchRow(t, db,
		`INSERT INTO custom_field_definitions (name, field_type) VALUES ('Single relation', 'linking')`,
	)

	linkTypeID := insertItemLinkBatchRow(t, db, `
		INSERT INTO link_types (name, forward_label, reverse_label, active)
		VALUES ('Field relation', 'relates to', 'relates to', true)
	`)
	oldLinkID := insertItemLinkBatchRow(t, db, `
		INSERT INTO item_links (link_type_id, source_type, source_id, target_type, target_id, custom_field_id)
		VALUES (?, 'item', ?, 'item', ?, ?)
	`, linkTypeID, sourceID, oldTargetID, fieldID)

	permissions := &countingWorkspacePermissions{
		allowed: map[int]bool{workspaceID: true},
		calls:   map[int]int{},
	}
	service := NewItemLinkService(db).WithPermissionService(permissions)
	_, err := service.ReplaceSingleValueFieldLinkWithChecks(actorID, CreateItemLinkParams{
		LinkTypeID:    999999,
		SourceType:    "item",
		SourceID:      sourceID,
		TargetType:    "item",
		TargetID:      newTargetID,
		CustomFieldID: &fieldID,
	})
	if err == nil {
		t.Fatal("ReplaceSingleValueFieldLinkWithChecks succeeded with a missing link type")
	}

	var gotID, gotTargetID int
	if err := db.QueryRow(`
		SELECT id, target_id FROM item_links
		WHERE custom_field_id = ? AND source_type = 'item' AND source_id = ?
	`, fieldID, sourceID).Scan(&gotID, &gotTargetID); err != nil {
		t.Fatalf("load preserved field link: %v", err)
	}
	if gotID != oldLinkID || gotTargetID != oldTargetID {
		t.Fatalf("field link after rollback = id %d target %d, want id %d target %d", gotID, gotTargetID, oldLinkID, oldTargetID)
	}
}

func TestReplaceSingleValueFieldLinkCommitsOneNewValue(t *testing.T) {
	db := newItemLinkBatchTestDB(t)
	actorID := insertItemLinkBatchRow(t, db, `
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('commit@example.com', 'commit-actor', 'Commit', 'Actor')
	`)
	workspaceID := insertItemLinkBatchRow(t, db,
		`INSERT INTO workspaces (name, key, description, active) VALUES ('Field replace', 'FR', '', true)`,
	)
	sourceID := insertBatchItem(t, db, workspaceID, "Source")
	oldTargetID := insertBatchItem(t, db, workspaceID, "Old target")
	newTargetID := insertBatchItem(t, db, workspaceID, "New target")
	fieldID := insertItemLinkBatchRow(t, db,
		`INSERT INTO custom_field_definitions (name, field_type) VALUES ('Single relation', 'linking')`,
	)

	linkTypeID := insertItemLinkBatchRow(t, db, `
		INSERT INTO link_types (name, forward_label, reverse_label, active)
		VALUES ('Field replacement', 'relates to', 'relates to', true)
	`)
	insertItemLinkBatchRow(t, db, `
		INSERT INTO item_links (link_type_id, source_type, source_id, target_type, target_id, custom_field_id)
		VALUES (?, 'item', ?, 'item', ?, ?)
	`, linkTypeID, sourceID, oldTargetID, fieldID)

	permissions := &countingWorkspacePermissions{
		allowed: map[int]bool{workspaceID: true},
		calls:   map[int]int{},
	}
	service := NewItemLinkService(db).WithPermissionService(permissions)
	created, err := service.ReplaceSingleValueFieldLinkWithChecks(actorID, CreateItemLinkParams{
		LinkTypeID:    linkTypeID,
		SourceType:    "item",
		SourceID:      sourceID,
		TargetType:    "item",
		TargetID:      newTargetID,
		CustomFieldID: &fieldID,
	})
	if err != nil {
		t.Fatalf("ReplaceSingleValueFieldLinkWithChecks: %v", err)
	}
	if created == nil || created.TargetID != newTargetID {
		t.Fatalf("created link = %+v, want target %d", created, newTargetID)
	}

	var count, gotTargetID int
	if err := db.QueryRow(`
		SELECT COUNT(*), MAX(target_id) FROM item_links
		WHERE custom_field_id = ? AND source_type = 'item' AND source_id = ?
	`, fieldID, sourceID).Scan(&count, &gotTargetID); err != nil {
		t.Fatalf("load replaced field link: %v", err)
	}
	if count != 1 || gotTargetID != newTargetID {
		t.Fatalf("field links after replacement = count %d target %d, want 1/%d", count, gotTargetID, newTargetID)
	}
}
