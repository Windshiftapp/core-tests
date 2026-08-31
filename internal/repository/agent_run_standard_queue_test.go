package repository

import (
	"context"
	"testing"
	"time"

	"windshift/internal/models"
)

func TestAgentRunRepositoryStandardQueueSerializesByBindingAndItem(t *testing.T) {
	ctx := context.Background()
	db := openAgentRunTestDB(t)
	repo := NewAgentRunRepository(db)
	for _, itemID := range []int{101, 102} {
		if _, err := db.Exec(`
			INSERT INTO items(id, workspace_id, workspace_item_number, title, is_task, frac_index, created_at, updated_at)
			VALUES (?, 1, ?, 'Standard queue item', FALSE, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`, itemID, itemID, itemID); err != nil {
			t.Fatalf("seed item %d: %v", itemID, err)
		}
	}

	insert := func(bindingID, itemID int) int {
		t.Helper()
		acting, root, immediate := 31, 41, 41
		id, err := repo.Insert(ctx, &models.AgentRun{
			WorkspaceID:            1,
			ItemID:                 &itemID,
			BindingID:              &bindingID,
			JobKind:                models.JobKindStandardAgent,
			ActingUserID:           &acting,
			RootInitiatorUserID:    &root,
			ImmediateTriggerUserID: &immediate,
			ProfileVersion:         7,
			GrantsJSON:             `{"tools":["get_item"]}`,
			ProfileSnapshotJSON:    `{"profile_version":7}`,
		})
		if err != nil {
			t.Fatalf("insert Standard run: %v", err)
		}
		return id
	}

	first := insert(11, 101)
	second := insert(11, 101)
	otherItem := insert(11, 102)
	now := time.Now().UTC()

	claimed, err := repo.ClaimNextStandard(ctx, 11, 101, now)
	if err != nil {
		t.Fatalf("claim first: %v", err)
	}
	if claimed == nil || claimed.ID != first {
		t.Fatalf("first claim = %#v, want run %d", claimed, first)
	}
	if claimed.ProfileVersion != 7 || claimed.GrantsJSON != `{"tools":["get_item"]}` {
		t.Fatalf("execution snapshot did not round-trip: %#v", claimed)
	}
	blocked, err := repo.ClaimNextStandard(ctx, 11, 101, now)
	if err != nil {
		t.Fatalf("claim while running: %v", err)
	}
	if blocked != nil {
		t.Fatalf("same profile/item queue ran concurrently: %#v", blocked)
	}

	parallel, err := repo.ClaimNextStandard(ctx, 11, 102, now)
	if err != nil {
		t.Fatalf("claim other item: %v", err)
	}
	if parallel == nil || parallel.ID != otherItem {
		t.Fatalf("different item should have an independent serial lane: %#v", parallel)
	}

	if _, err := repo.FinalizeRunning(ctx, first, models.AgentRunStatusSucceeded, "", now); err != nil {
		t.Fatalf("finalize first: %v", err)
	}
	next, err := repo.ClaimNextStandard(ctx, 11, 101, now.Add(time.Second))
	if err != nil {
		t.Fatalf("claim second: %v", err)
	}
	if next == nil || next.ID != second {
		t.Fatalf("second claim = %#v, want run %d", next, second)
	}
}

func TestAgentRunRepositoryFailOrphanedStandardRunsPreservesQueuedAndCodingRuns(t *testing.T) {
	ctx := context.Background()
	db := openAgentRunTestDB(t)
	repo := NewAgentRunRepository(db)

	standardID, err := repo.Insert(ctx, &models.AgentRun{WorkspaceID: 1, JobKind: models.JobKindStandardAgent})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.MarkRunningIfQueued(ctx, standardID, "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	queuedID, err := repo.Insert(ctx, &models.AgentRun{WorkspaceID: 1, JobKind: models.JobKindStandardAgent})
	if err != nil {
		t.Fatal(err)
	}
	codingID, err := repo.Insert(ctx, &models.AgentRun{WorkspaceID: 1, JobKind: models.JobKindCodingAgent})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.MarkRunningIfQueued(ctx, codingID, "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	affected, err := repo.FailOrphanedStandardRuns(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("fail orphaned: %v", err)
	}
	if affected != 1 {
		t.Fatalf("affected = %d, want 1", affected)
	}
	for id, want := range map[int]string{
		standardID: models.AgentRunStatusFailed,
		queuedID:   models.AgentRunStatusQueued,
		codingID:   models.AgentRunStatusRunning,
	} {
		got, err := repo.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != want {
			t.Errorf("run %d status = %q, want %q", id, got.Status, want)
		}
	}
}
