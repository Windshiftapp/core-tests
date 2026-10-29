//go:build test

package services

import (
	"context"
	"testing"
)

// TestBatchIncludesApproverVisibleItem reproduces WI-1813: the single-item read
// grants view to an approver with an active pool membership on the item, but
// the batch read only checked workspace item.view and dropped those rows.
func TestBatchIncludesApproverVisibleItem(t *testing.T) {
	env := newApprovalTestEnv(t)

	// An explicit Viewer assignment makes the workspace non-open, so the
	// approver has no workspace item.view and must rely on the fallback. The
	// viewer is a third user so the tested approver stays a non-member.
	assignWorkspaceRoleByName(t, env.db, env.requestor, env.workspaceID, "Viewer")

	env.createApprovalSet(approvalSetSpec{
		steps: []approvalStepSpec{userStep(0, "First", env.approver1)},
	})
	application := newItemApplicationForApproverTest(env)
	ctx := context.Background()

	// Before the request opens there is no approver fallback.
	items, err := application.Batch(ctx, env.approver1, []int{env.itemID}, false)
	if err != nil {
		t.Fatalf("Batch before request: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("batch before request returned %d items, want 0", len(items))
	}

	if _, err := env.approvalService.RequestApproval(ctx, env.itemID, env.statusReviewID, env.statusOpenID, env.requestor); err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}

	items, err = application.Batch(ctx, env.approver1, []int{env.itemID}, false)
	if err != nil {
		t.Fatalf("Batch after request: %v", err)
	}
	if len(items) != 1 || items[0].ID != env.itemID {
		t.Fatalf("batch after request = %#v, want the approver-visible item %d", items, env.itemID)
	}

	// A non-member still gets nothing.
	items, err = application.Batch(ctx, env.approver2, []int{env.itemID}, false)
	if err != nil {
		t.Fatalf("Batch for non-member: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("batch for non-member returned %d items, want 0", len(items))
	}
}

func newItemApplicationForApproverTest(env *approvalTestEnv) *ItemApplicationService {
	creation := NewItemCreationService(env.db, env.permService)
	update := NewItemUpdateApplicationService(env.db, env.permService)
	deletion := NewItemDeletionApplicationService(env.db, env.permService)
	return NewItemApplicationService(env.db, env.permService, creation, update, deletion).
		WithReads(nil, env.approvalService)
}
