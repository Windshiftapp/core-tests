package tests

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"windshift/internal/scheduler"
)

// TestCFVCleanupScheduler exercises the async cleanup path end-to-end:
// enqueue a field-scrub job for a field referenced by many items, drive the
// scheduler, assert every item's cfv has the orphan key removed.
//
// Field deletion itself is now blocked while a field is still in use (see
// TestCustomFieldDeleteGuard), so the scrub path only fires on the
// concurrent-write race — modelled here by enqueuing the job directly.
//
// The scheduler's batchSize is 500 by default; this test creates 12
// items, which is enough to exercise the loop without making the test
// slow.
func TestCFVCleanupScheduler(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "CFV Cleanup WS", shortKey("CCWS"))

	t.Run("scheduler_drains_pending_jobs_and_scrubs_every_referencing_item", func(t *testing.T) {
		fieldID := CreateTestCustomField(t, server, "Bulk Cleanup Field "+ts(), "text", "")
		fieldKey := strconv.Itoa(fieldID)

		const itemCount = 12
		itemIDs := make([]int, 0, itemCount)
		for i := 0; i < itemCount; i++ {
			itemID := createItemWithCFV(t, server, workspaceID,
				fmt.Sprintf("Bulk item %d %s", i, ts()),
				map[string]interface{}{fieldKey: fmt.Sprintf("v%d", i)})
			itemIDs = append(itemIDs, itemID)
		}

		// Sanity-check one item has the key.
		got := getItemCFV(t, server, itemIDs[0])
		if _, present := got[fieldKey]; !present {
			t.Fatalf("precondition: cfv[%s] missing from item %d", fieldKey, itemIDs[0])
		}

		// Enqueue a scrub job directly (the delete handler would refuse while
		// items reference the field) and drive the scheduler synchronously.
		if err := scheduler.EnqueueFieldCleanup(server.DB(), fieldID); err != nil {
			t.Fatalf("EnqueueFieldCleanup: %v", err)
		}
		runCFVCleanupOnce(t, server)

		// Every item must have the orphan key removed.
		for _, id := range itemIDs {
			got := getItemCFV(t, server, id)
			if _, present := got[fieldKey]; present {
				t.Errorf("expected cfv[%s] to be scrubbed from item %d, still present: %v", fieldKey, id, got[fieldKey])
			}
		}
	})

	t.Run("scheduler_is_idempotent_when_no_jobs_pending", func(t *testing.T) {
		// Should be a fast no-op; just make sure it doesn't panic when
		// the queue is empty.
		sch := scheduler.NewCFVCleanupScheduler(server.DB())
		sch.RunOnceForTests()
		sch.RunOnceForTests()
	})

	t.Run("duplicate_enqueue_does_not_create_duplicate_pending_jobs", func(t *testing.T) {
		// Field deletion enqueues a job; manually deleting again (or some
		// other code path) should not pile up identical jobs while the
		// first is still pending or running. The handler calls
		// scheduler.EnqueueFieldCleanup which de-dupes.
		fieldID := CreateTestCustomField(t, server, "Idempotent Cleanup Field "+ts(), "text", "")

		// First enqueue (via Delete).
		resp := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/admin/custom-fields/%d", fieldID), nil)
		_ = resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNoContent)

		// Second enqueue: directly call the API. Idempotent.
		if err := scheduler.EnqueueFieldCleanup(server.DB(), fieldID); err != nil {
			t.Fatalf("second EnqueueFieldCleanup: %v", err)
		}

		// Count pending+running jobs for this field.
		var count int
		row := server.DB().QueryRow(
			`SELECT COUNT(*) FROM pending_custom_field_cleanups WHERE field_id = ? AND status IN ('pending', 'running')`,
			fieldID,
		)
		if err := row.Scan(&count); err != nil {
			t.Fatalf("count pending jobs: %v", err)
		}
		if count != 1 {
			t.Errorf("expected exactly 1 pending/running job for field %d, got %d", fieldID, count)
		}
	})
}
