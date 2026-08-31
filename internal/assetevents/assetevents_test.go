//go:build test

package assetevents

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

func TestRecorderCommitsAndRollsBackWithAssetMutation(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	recorder := NewRecorder(db)
	ctx := context.Background()
	snapshot := AssetSnapshot{ID: 41, SetID: 7, AssetTypeID: 3, Title: "Durable asset", AssetTag: "AST-41"}

	errRollback := errors.New("force rollback")
	err := database.WithTx(db, func(tx database.Tx) error {
		if _, err := recorder.Created(ctx, tx, snapshot, map[string]any{"title": snapshot.Title}, User(9, "test")); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("rolled-back Created() error = %v, want %v", err, errRollback)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM domain_events WHERE aggregate_type = 'asset' AND aggregate_id = '41'").Scan(&count); err != nil {
		t.Fatalf("count rolled-back events: %v", err)
	}
	if count != 0 {
		t.Fatalf("rolled-back event count = %d, want 0", count)
	}

	err = database.WithTx(db, func(tx database.Tx) error {
		_, err := recorder.Created(ctx, tx, snapshot, map[string]any{"title": snapshot.Title}, User(9, "test"))
		return err
	})
	if err != nil {
		t.Fatalf("committed Created() error = %v", err)
	}
	var eventType, actorKind, actorRef string
	var payloadJSON []byte
	if err := db.QueryRow(`
		SELECT event_type, actor_kind, actor_ref, payload
		FROM domain_events
		WHERE aggregate_type = 'asset' AND aggregate_id = '41'
	`).Scan(&eventType, &actorKind, &actorRef, &payloadJSON); err != nil {
		t.Fatalf("load committed event: %v", err)
	}
	if eventType != Created || actorKind != "user" || actorRef != "9" {
		t.Fatalf("committed event = type:%q actor:%s/%s, want %q user/9", eventType, actorKind, actorRef, Created)
	}
	var payload CreatedV1
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		t.Fatalf("decode committed payload: %v", err)
	}
	if payload.Asset != snapshot || payload.NewValues["title"] != snapshot.Title {
		t.Fatalf("committed payload = %#v, want snapshot %#v", payload, snapshot)
	}
}
