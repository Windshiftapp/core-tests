//go:build test

package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/itemevents"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

func TestCanonicalItemEventsCommitWithSourceMutations(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	fixedTime := time.Date(2026, time.August, 27, 10, 30, 0, 0, time.UTC)

	rollbackErr := errors.New("reject source transaction")
	_, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID, Title: "rolled back item",
		StatusID: &data.StatusID, CreatorID: &data.UserID,
		AfterCreate: func(context.Context, database.Tx, int) error { return rollbackErr },
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("CreateItem() rollback error = %v, want sentinel", err)
	}
	assertDomainEventCounts(t, db, 0, 0)

	metadata := itemevents.User(data.UserID, "rest")
	metadata.SourceRef = "v1"
	metadata.OccurredAt = fixedTime
	itemID64, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID, Title: "durable item", Description: "initial",
		StatusID: &data.StatusID, PriorityID: &data.PriorityID,
		CreatorID: &data.UserID, EventMetadata: metadata,
	})
	if err != nil {
		t.Fatalf("CreateItem() error = %v", err)
	}
	itemID := int(itemID64)

	updateResult, err := NewItemUpdateService(db).UpdateItem(UpdateItemRequest{
		ItemID: itemID, UserID: data.UserID,
		UpdateData:    map[string]any{"title": "durable item updated"},
		EventMetadata: itemevents.User(data.UserID, "rest"),
	})
	if err != nil {
		t.Fatalf("UpdateItem() error = %v", err)
	}
	if updateResult.Item.Title != "durable item updated" {
		t.Fatalf("updated title = %q", updateResult.Item.Title)
	}

	commentResult, err := NewCommentService(db).Create(CreateCommentParams{
		ItemID: itemID, AuthorID: data.UserID, ActorUserID: data.UserID,
		Content: "durable comment", EventMetadata: itemevents.User(data.UserID, "rest"),
	})
	if err != nil {
		t.Fatalf("Create comment: %v", err)
	}
	if commentResult.CommentID == 0 {
		t.Fatal("comment ID is zero")
	}

	if err := NewItemCRUDService(db).DeleteSingleWithMetadata(itemID, itemevents.User(data.UserID, "rest")); err != nil {
		t.Fatalf("DeleteSingleWithMetadata() error = %v", err)
	}

	rows, err := db.Query(`
		SELECT aggregate_sequence, event_type, actor_kind, actor_ref, source_kind, source_ref, payload
		FROM domain_events
		WHERE aggregate_type = 'item' AND aggregate_id = ?
		ORDER BY aggregate_sequence
	`, itemID)
	if err != nil {
		t.Fatalf("query item events: %v", err)
	}
	defer func() { _ = rows.Close() }()
	wantTypes := []string{itemevents.Created, itemevents.Updated, itemevents.CommentCreated, itemevents.Deleted}
	for index, wantType := range wantTypes {
		if !rows.Next() {
			t.Fatalf("event %d missing", index+1)
		}
		var sequence int64
		var eventType, actorKind, actorRef, sourceKind, payloadJSON string
		var sourceRef sql.NullString
		if err := rows.Scan(&sequence, &eventType, &actorKind, &actorRef, &sourceKind, &sourceRef, &payloadJSON); err != nil {
			t.Fatalf("scan event %d: %v", index+1, err)
		}
		if sequence != int64(index+1) || eventType != wantType || actorKind != "user" || actorRef != "1" || sourceKind != "rest" {
			t.Fatalf("event %d = seq:%d type:%s actor:%s/%s source:%s/%s", index+1, sequence, eventType, actorKind, actorRef, sourceKind, sourceRef.String)
		}
		if index == 0 && sourceRef.String != "v1" {
			t.Fatalf("created source ref = %q, want v1", sourceRef.String)
		}
		if index == 1 {
			var payload itemevents.UpdatedV1
			if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
				t.Fatalf("decode item.updated payload: %v", err)
			}
			if len(payload.Changes) != 1 || payload.Changes[0].Field != "title" || payload.Changes[0].OldValue != "durable item" || payload.Changes[0].NewValue != "durable item updated" {
				t.Fatalf("item.updated changes = %+v", payload.Changes)
			}
		}
	}
	if rows.Next() {
		t.Fatal("unexpected extra item event")
	}
	assertDomainEventCounts(t, db, len(wantTypes), 1)
}

func TestCanonicalItemUpdateRollsBackWithTransactionHook(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	itemID64, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID, Title: "before rollback", StatusID: &data.StatusID,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	itemID := int(itemID64)

	sentinel := errors.New("abort update")
	_, err = NewItemUpdateService(db).updateItem(context.Background(), UpdateItemRequest{
		ItemID: itemID, UserID: data.UserID, UpdateData: map[string]any{"title": "must not commit"},
	}, itemUpdateOptions{
		recordHistory: true,
		afterUpdateTransaction: func(context.Context, database.Tx, *models.Item, *models.Item) error {
			return sentinel
		},
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("update error = %v, want sentinel", err)
	}
	var title string
	if err := db.QueryRow("SELECT title FROM items WHERE id = ?", itemID).Scan(&title); err != nil {
		t.Fatalf("load rolled-back item: %v", err)
	}
	if title != "before rollback" {
		t.Fatalf("title after rollback = %q", title)
	}
	assertDomainEventCounts(t, db, 1, 1)
}

func assertDomainEventCounts(t *testing.T, db database.Database, events, streams int) {
	t.Helper()
	var eventCount, streamCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM domain_events").Scan(&eventCount); err != nil {
		t.Fatalf("count domain events: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM domain_event_streams").Scan(&streamCount); err != nil {
		t.Fatalf("count domain event streams: %v", err)
	}
	if eventCount != events || streamCount != streams {
		t.Fatalf("domain event counts = events:%d streams:%d, want %d/%d", eventCount, streamCount, events, streams)
	}
}
