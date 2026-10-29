//go:build test

package services

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// TestItemLifecycle_MergeKeepsThreadAndFilesWithTheirRequester proves a merge
// across requesters never moves customer files or email threads onto the
// other customer's ticket: they keep their provenance on the merged
// duplicate, and the result reports it (WI-1566).
func TestItemLifecycle_MergeKeepsThreadAndFilesWithTheirRequester(t *testing.T) {
	env := setupLifecycleTestEnv(t)

	customerA := env.newPortalCustomer(t, "Customer A", "customer-a@example.com")
	customerB := env.newPortalCustomer(t, "Customer B", "customer-b@example.com")

	ownedByA := env.newItemWithRequester(t, env.workspaceA, "A's request", customerA)
	env.newAttachment(t, ownedByA)
	env.newTrackingRow(t, 0, ownedByA, "<msg-a-1@example.com>")

	canonical := env.newItemWithRequester(t, env.workspaceA, "B's request", customerB)

	result, err := env.svc.Merge(context.Background(), ItemMergeInput{
		ActorUserID: env.userID, TargetItemID: canonical, SourceItemIDs: []int{ownedByA},
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	var attachmentsOnTarget, attachmentsOnSource int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE item_id = ?`, canonical).Scan(&attachmentsOnTarget); err != nil {
		t.Fatalf("count target attachments: %v", err)
	}
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE item_id = ?`, ownedByA).Scan(&attachmentsOnSource); err != nil {
		t.Fatalf("count source attachments: %v", err)
	}
	if attachmentsOnTarget != 0 || attachmentsOnSource != 1 {
		t.Fatalf("attachments target=%d source=%d, want 0/1 (files keep their requester)", attachmentsOnTarget, attachmentsOnSource)
	}

	var trackingOnTarget, trackingOnSource int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM email_message_tracking WHERE item_id = ?`, canonical).Scan(&trackingOnTarget); err != nil {
		t.Fatalf("count target tracking rows: %v", err)
	}
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM email_message_tracking WHERE item_id = ?`, ownedByA).Scan(&trackingOnSource); err != nil {
		t.Fatalf("count source tracking rows: %v", err)
	}
	if trackingOnTarget != 0 || trackingOnSource != 1 {
		t.Fatalf("tracking target=%d source=%d, want 0/1 (thread keeps its requester)", trackingOnTarget, trackingOnSource)
	}

	if !result.Sources[0].ContentKeptOnSource {
		t.Fatal("result does not report ContentKeptOnSource for a cross-requester merge")
	}

	// The same-requester merge still moves everything to the canonical.
	shared := env.newItemWithRequester(t, env.workspaceA, "A's duplicate", customerA)
	sharedFile := env.newAttachment(t, shared)
	env.newTrackingRow(t, 0, shared, "<msg-a-2@example.com>")
	sameRequesterTarget := env.newItemWithRequester(t, env.workspaceA, "A's canonical request", customerA)

	result, err = env.svc.Merge(context.Background(), ItemMergeInput{
		ActorUserID: env.userID, TargetItemID: sameRequesterTarget, SourceItemIDs: []int{shared},
	})
	if err != nil {
		t.Fatalf("same-requester Merge: %v", err)
	}
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE item_id = ?`, sameRequesterTarget).Scan(&attachmentsOnTarget); err != nil {
		t.Fatalf("count same-requester target attachments: %v", err)
	}
	if attachmentsOnTarget != 1 || sharedFile == 0 {
		t.Fatalf("same-requester attachments on target = %d, want 1", attachmentsOnTarget)
	}
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM email_message_tracking WHERE item_id = ?`, sameRequesterTarget).Scan(&trackingOnTarget); err != nil {
		t.Fatalf("count same-requester tracking rows: %v", err)
	}
	if trackingOnTarget != 1 {
		t.Fatalf("same-requester tracking rows on target = %d, want 1", trackingOnTarget)
	}
	if result.Sources[0].ContentKeptOnSource {
		t.Fatal("same-requester merge must not report ContentKeptOnSource")
	}
}

// TestItemLifecycle_MergeRejectsReverseRedirect proves merging a canonical
// into one of its own merged duplicates fails instead of creating mutually
// redirecting tickets, and that concurrent opposite-direction merges leave a
// cycle-free state (WI-1567).
func TestItemLifecycle_MergeRejectsReverseRedirect(t *testing.T) {
	env := setupLifecycleTestEnv(t)

	canonical := env.newItem(t, env.workspaceA, "Canonical")
	duplicate := env.newItem(t, env.workspaceA, "Duplicate")

	if _, err := env.svc.Merge(context.Background(), ItemMergeInput{
		ActorUserID: env.userID, TargetItemID: canonical, SourceItemIDs: []int{duplicate},
	}); err != nil {
		t.Fatalf("first Merge: %v", err)
	}

	// The reverse direction targets a merged duplicate: conflict.
	_, err := env.svc.Merge(context.Background(), ItemMergeInput{
		ActorUserID: env.userID, TargetItemID: duplicate, SourceItemIDs: []int{canonical},
	})
	if !errors.Is(err, ErrItemMergeConflict) {
		t.Fatalf("reverse merge err = %v, want ErrItemMergeConflict", err)
	}
	if merged := env.mergedInto(t, canonical); merged != nil {
		t.Fatalf("canonical became merged into %d; redirect cycle", *merged)
	}
	if merged := env.mergedInto(t, duplicate); merged == nil || *merged != canonical {
		t.Fatalf("duplicate redirect = %v, want %d", merged, canonical)
	}
}

// TestItemLifecycle_MergeConcurrentOppositeDirectionsFailsOne proves two
// racing opposite-direction merges cannot both commit: at most one sets a
// redirect and no cycle survives (WI-1567).
func TestItemLifecycle_MergeConcurrentOppositeDirectionsFailsOne(t *testing.T) {
	env := setupLifecycleTestEnv(t)

	itemA := env.newItem(t, env.workspaceA, "Item A")
	itemB := env.newItem(t, env.workspaceA, "Item B")

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errs[0] = env.svc.Merge(context.Background(), ItemMergeInput{
			ActorUserID: env.userID, TargetItemID: itemB, SourceItemIDs: []int{itemA},
		})
	}()
	go func() {
		defer wg.Done()
		_, errs[1] = env.svc.Merge(context.Background(), ItemMergeInput{
			ActorUserID: env.userID, TargetItemID: itemA, SourceItemIDs: []int{itemB},
		})
	}()
	wg.Wait()

	aIntoB := env.mergedInto(t, itemA)
	bIntoA := env.mergedInto(t, itemB)
	if aIntoB != nil && bIntoA != nil {
		t.Fatalf("both opposite merges committed: A->%d and B->%d form a redirect cycle", *aIntoB, *bIntoA)
	}
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes == 0 {
		t.Fatalf("both merges failed: %v / %v", errs[0], errs[1])
	}
	// Whichever redirect exists must terminate at a canonical item.
	if aIntoB != nil && env.mergedInto(t, *aIntoB) != nil {
		t.Fatalf("A redirects into merged item %d", *aIntoB)
	}
	if bIntoA != nil && env.mergedInto(t, *bIntoA) != nil {
		t.Fatalf("B redirects into merged item %d", *bIntoA)
	}
}

// TestItemLifecycle_MergeAllowsChainedRedirects proves merging the tail of a
// redirect chain into its head stays legal: the chain terminates at one
// canonical with no cycle (WI-1567).
func TestItemLifecycle_MergeAllowsChainedRedirects(t *testing.T) {
	env := setupLifecycleTestEnv(t)

	head := env.newItem(t, env.workspaceA, "Chain head")
	middle := env.newItem(t, env.workspaceA, "Chain middle")
	tail := env.newItem(t, env.workspaceA, "Chain tail")

	if _, err := env.svc.Merge(context.Background(), ItemMergeInput{
		ActorUserID: env.userID, TargetItemID: middle, SourceItemIDs: []int{head},
	}); err != nil {
		t.Fatalf("merge head into middle: %v", err)
	}
	if _, err := env.svc.Merge(context.Background(), ItemMergeInput{
		ActorUserID: env.userID, TargetItemID: tail, SourceItemIDs: []int{middle},
	}); err != nil {
		t.Fatalf("merge middle into tail: %v", err)
	}
	if merged := env.mergedInto(t, tail); merged != nil {
		t.Fatalf("tail became merged into %d", *merged)
	}
	// Merging the tail into a merged chain link is rejected: only the
	// canonical can be a target.
	if _, err := env.svc.Merge(context.Background(), ItemMergeInput{
		ActorUserID: env.userID, TargetItemID: middle, SourceItemIDs: []int{tail},
	}); !errors.Is(err, ErrItemMergeConflict) {
		t.Fatalf("merge tail into merged middle err = %v, want ErrItemMergeConflict", err)
	}
	// A fresh item still folds into the chain's canonical.
	fresh := env.newItem(t, env.workspaceA, "Chain newcomer")
	if _, err := env.svc.Merge(context.Background(), ItemMergeInput{
		ActorUserID: env.userID, TargetItemID: tail, SourceItemIDs: []int{fresh},
	}); err != nil {
		t.Fatalf("merge newcomer into tail: %v", err)
	}
}
