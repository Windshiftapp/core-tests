//go:build test

package repository

import (
	"context"
	"testing"

	"windshift/internal/models"
)

// HasQueuedStandardRun is the targeted worker-exit recheck: it must answer for
// the exact queue key and status only.
func TestHasQueuedStandardRunMatchesKeyAndStatus(t *testing.T) {
	ctx := context.Background()
	db := openAgentRunTestDB(t)
	repo := NewAgentRunRepository(db)
	if _, err := db.Exec(`
		INSERT INTO items(id, workspace_id, workspace_item_number, title, is_task, frac_index, created_at, updated_at)
		VALUES (101, 1, 101, 'Standard queue item', FALSE, '101', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
		       (102, 1, 102, 'Standard queue item two', FALSE, '102', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("seed items: %v", err)
	}

	insert := func(t *testing.T, bindingID, itemID int, jobKind, status string) {
		t.Helper()
		acting, root, immediate := 31, 41, 41
		id, err := repo.Insert(ctx, &models.AgentRun{
			WorkspaceID:            1,
			ItemID:                 &itemID,
			BindingID:              &bindingID,
			JobKind:                jobKind,
			Status:                 status,
			ActingUserID:           &acting,
			RootInitiatorUserID:    &root,
			ImmediateTriggerUserID: &immediate,
			ProfileVersion:         7,
			ProfileSnapshotJSON:    `{"profile_version":7}`,
		})
		if err != nil {
			t.Fatalf("insert run: %v", err)
		}
		_ = id
	}

	cases := []struct {
		name       string
		bindingID  int
		itemID     int
		insertKey  func(t *testing.T)
		wantExists bool
	}{
		{
			name:       "queued run for the key exists",
			bindingID:  11,
			itemID:     101,
			insertKey:  func(t *testing.T) { insert(t, 11, 101, models.JobKindStandardAgent, models.AgentRunStatusQueued) },
			wantExists: true,
		},
		{
			name:      "only a running run for the key",
			bindingID: 12,
			itemID:    101,
			insertKey: func(t *testing.T) {
				insert(t, 12, 101, models.JobKindStandardAgent, models.AgentRunStatusRunning)
			},
			wantExists: false,
		},
		{
			name:      "queued run for another item",
			bindingID: 13,
			itemID:    101,
			insertKey: func(t *testing.T) {
				insert(t, 13, 102, models.JobKindStandardAgent, models.AgentRunStatusQueued)
			},
			wantExists: false,
		},
		{
			name:      "queued run for another binding",
			bindingID: 14,
			itemID:    101,
			insertKey: func(t *testing.T) {
				insert(t, 15, 101, models.JobKindStandardAgent, models.AgentRunStatusQueued)
			},
			wantExists: false,
		},
		{
			name:      "queued run of another job kind",
			bindingID: 16,
			itemID:    101,
			insertKey: func(t *testing.T) {
				insert(t, 16, 101, models.JobKindCodingAgent, models.AgentRunStatusQueued)
			},
			wantExists: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.insertKey(t)
			exists, err := repo.HasQueuedStandardRun(ctx, tc.bindingID, tc.itemID)
			if err != nil {
				t.Fatalf("HasQueuedStandardRun: %v", err)
			}
			if exists != tc.wantExists {
				t.Fatalf("HasQueuedStandardRun(%d, %d) = %v, want %v", tc.bindingID, tc.itemID, exists, tc.wantExists)
			}
		})
	}
}
