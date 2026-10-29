//go:build test

package services

import (
	"context"
	"testing"
)

// TestItemLifecycle_SameRequesterMergeMovesParticipants covers the bug hunt
// finding that source-only participants were left behind, losing canonical
// portal access and future reply notifications.
func TestItemLifecycle_SameRequesterMergeMovesParticipants(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	requester := env.newPortalCustomer(t, "Requester", "requester@example.com")
	sourceOnly := env.newPortalCustomer(t, "Source Only", "source-only@example.com")
	shared := env.newPortalCustomer(t, "Shared", "shared@example.com")

	source := env.newItemWithRequester(t, env.workspaceA, "Duplicate", requester)
	target := env.newItemWithRequester(t, env.workspaceA, "Canonical", requester)
	addParticipant(t, env, source, sourceOnly)
	addParticipant(t, env, source, shared)
	addParticipant(t, env, target, shared)

	if _, err := env.svc.Merge(context.Background(), ItemMergeInput{
		ActorUserID: env.userID, TargetItemID: target, SourceItemIDs: []int{source},
	}); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	assertParticipant(t, env, target, sourceOnly, true)
	assertParticipant(t, env, target, shared, true)
	assertParticipant(t, env, source, sourceOnly, false)
	assertParticipant(t, env, source, shared, false)

	var sharedCount int
	if err := env.db.QueryRow(
		`SELECT COUNT(*) FROM item_participants WHERE item_id = ? AND portal_customer_id = ?`, target, shared,
	).Scan(&sharedCount); err != nil {
		t.Fatalf("count shared participant: %v", err)
	}
	if sharedCount != 1 {
		t.Fatalf("shared participant rows on target = %d, want 1", sharedCount)
	}
}

// TestItemLifecycle_CrossRequesterMergeKeepsParticipantsOnSource guards privacy:
// a cross-requester merge must not expose the duplicate's participants on the
// canonical ticket.
func TestItemLifecycle_CrossRequesterMergeKeepsParticipantsOnSource(t *testing.T) {
	env := setupLifecycleTestEnv(t)
	requesterA := env.newPortalCustomer(t, "Requester A", "requester-a@example.com")
	requesterB := env.newPortalCustomer(t, "Requester B", "requester-b@example.com")
	participantA := env.newPortalCustomer(t, "Participant A", "participant-a@example.com")

	source := env.newItemWithRequester(t, env.workspaceA, "A's duplicate", requesterA)
	target := env.newItemWithRequester(t, env.workspaceA, "B's canonical", requesterB)
	addParticipant(t, env, source, participantA)

	if _, err := env.svc.Merge(context.Background(), ItemMergeInput{
		ActorUserID: env.userID, TargetItemID: target, SourceItemIDs: []int{source},
	}); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	assertParticipant(t, env, source, participantA, true)
	assertParticipant(t, env, target, participantA, false)
}

func addParticipant(t *testing.T, env lifecycleTestEnv, itemID, customerID int) {
	t.Helper()
	if _, err := env.db.Exec(
		`INSERT INTO item_participants (item_id, portal_customer_id) VALUES (?, ?)`, itemID, customerID,
	); err != nil {
		t.Fatalf("add participant: %v", err)
	}
}

func assertParticipant(t *testing.T, env lifecycleTestEnv, itemID, customerID int, want bool) {
	t.Helper()
	var count int
	if err := env.db.QueryRow(
		`SELECT COUNT(*) FROM item_participants WHERE item_id = ? AND portal_customer_id = ?`, itemID, customerID,
	).Scan(&count); err != nil {
		t.Fatalf("count participant: %v", err)
	}
	if (count > 0) != want {
		t.Fatalf("participant item=%d customer=%d present=%v, want %v", itemID, customerID, count > 0, want)
	}
}
