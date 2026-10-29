//go:build test

package handlers

import (
	"context"
	"testing"

	"windshift/internal/testutils"
)

// TestHubHasPortalsCountsDisabledPortals pins the customer/organisation
// navigation guard's availability probe: a portal that exists but is disabled
// must still count as "a portal exists" so the management entry stays
// reachable, while HasActivePortals keeps reporting false.
func TestHubHasPortalsCountsDisabledPortals(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	handler := newHubHandlerForTest(t, tdb)
	ctx := context.Background()

	anyPortals, err := handler.HasPortals(ctx)
	if err != nil {
		t.Fatalf("HasPortals: %v", err)
	}
	if anyPortals {
		t.Fatalf("HasPortals = true with no portal channels")
	}
	activePortals, err := handler.HasActivePortals(ctx)
	if err != nil {
		t.Fatalf("HasActivePortals: %v", err)
	}
	if activePortals {
		t.Fatalf("HasActivePortals = true with no portal channels")
	}

	var channelID int
	if err := tdb.QueryRow(`
		INSERT INTO channels (name, type, direction, status)
		VALUES ('Disabled portal', 'portal', 'inbound', 'disabled')
		RETURNING id
	`).Scan(&channelID); err != nil {
		t.Fatalf("insert disabled portal channel: %v", err)
	}

	anyPortals, err = handler.HasPortals(ctx)
	if err != nil {
		t.Fatalf("HasPortals after insert: %v", err)
	}
	if !anyPortals {
		t.Fatalf("HasPortals = false with a disabled portal channel")
	}
	activePortals, err = handler.HasActivePortals(ctx)
	if err != nil {
		t.Fatalf("HasActivePortals after insert: %v", err)
	}
	if activePortals {
		t.Fatalf("HasActivePortals = true with only a disabled portal channel")
	}
}
