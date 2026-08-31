//go:build test

package actionevents

import (
	"context"
	"testing"
	"time"

	"windshift/internal/events"
	"windshift/internal/testutils"
)

func TestActivateCutoverRecordsTheNextEventBoundaryOnce(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	ctx := context.Background()
	stored, err := events.NewStore(db).AppendStandalone(ctx, events.NewEvent{
		Key: "cutover-predecessor", AggregateType: "test", AggregateID: "cutover",
		Type: "test.cutover.v1", PayloadVersion: 1, OccurredAt: time.Now().UTC(),
		ActorKind: "system", SourceKind: "test", Payload: []byte(`{"value":1}`),
	})
	if err != nil {
		t.Fatalf("append predecessor: %v", err)
	}

	first, err := ActivateCutover(ctx, db, "test.cutover", "test")
	if err != nil {
		t.Fatalf("first ActivateCutover() error = %v", err)
	}
	second, err := ActivateCutover(ctx, db, "test.cutover", "test")
	if err != nil {
		t.Fatalf("second ActivateCutover() error = %v", err)
	}
	current, err := CurrentCutover(ctx, db, "test.cutover")
	if err != nil {
		t.Fatalf("CurrentCutover() error = %v", err)
	}

	wantStart := stored.ID + 1
	if first.StartEventID != wantStart || second.StartEventID != wantStart || current == nil || current.StartEventID != wantStart {
		t.Fatalf("cutover starts = first:%d second:%d current:%v, want %d", first.StartEventID, second.StartEventID, current, wantStart)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM action_event_cutovers WHERE cutover_key = ?", "test.cutover").Scan(&count); err != nil {
		t.Fatalf("count cutovers: %v", err)
	}
	if count != 1 {
		t.Fatalf("cutover row count = %d, want 1", count)
	}
}
