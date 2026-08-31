package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"windshift/internal/scheduler"
	"windshift/internal/services"
)

// TestCFVOptionRemovalScheduler covers the async option-removal job (WI-419):
// removing a select/multiselect option enqueues a job that CFVCleanupScheduler
// drains, stripping the removed option ids from items in bounded keyset-
// paginated batches.
func TestCFVOptionRemovalScheduler(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Option Removal WS", shortKey("ORWS"))

	t.Run("scrubs_select_refs_across_keyset_batch_boundary", func(t *testing.T) {
		opts := mustSerializeOptions(t, 4, []selectOption{
			{ID: 1, Label: "Low"}, {ID: 2, Label: "Medium"}, {ID: 3, Label: "High"},
		})
		fieldID := CreateTestCustomField(t, server, "Bulk Priority "+ts(), "select", opts)
		fieldKey := strconv.Itoa(fieldID)

		// Insert more than the scheduler's batchSize (500) rows that mention
		// the field key so the worker must page across the keyset boundary.
		// Half reference the option we remove (id=2); half reference a
		// survivor (id=3).
		token := "orm-" + ts()
		const removeCount = 300
		const surviveCount = 260
		bulkInsertItemsWithCFV(t, server, workspaceID, token+"-remove", removeCount, fmt.Sprintf(`{"%s":2}`, fieldKey))
		bulkInsertItemsWithCFV(t, server, workspaceID, token+"-survive", surviveCount, fmt.Sprintf(`{"%s":3}`, fieldKey))

		if err := scheduler.EnqueueOptionRemoval(server.DB(), fieldID, "select", []int{2}); err != nil {
			t.Fatalf("EnqueueOptionRemoval: %v", err)
		}
		scheduler.NewCFVCleanupScheduler(server.DB()).RunOnceForTests()

		// Removed-option rows: the key must be gone from every row.
		if n := countItemsWithKey(t, server, token+"-remove-%", fieldKey); n != 0 {
			t.Errorf("expected 0 remove-rows to still carry key %s, got %d", fieldKey, n)
		}
		// Survivor rows: every row must still carry the key.
		if n := countItemsWithKey(t, server, token+"-survive-%", fieldKey); n != surviveCount {
			t.Errorf("expected all %d survivor rows to keep key %s, got %d", surviveCount, fieldKey, n)
		}
	})

	// Multiselect filtering correctness (array element removal, empty-array
	// deletion) is asserted precisely in TestCustomFieldOptionDeletionCleansItems
	// via the API; this file focuses on the keyset/scheduler/dedup behavior.

	t.Run("option_removal_jobs_do_not_dedup", func(t *testing.T) {
		fieldID := CreateTestCustomField(t, server, "Dedup Check "+ts(), "select",
			mustSerializeOptions(t, 3, []selectOption{{ID: 1, Label: "A"}, {ID: 2, Label: "B"}}))
		for i := 0; i < 2; i++ {
			if err := scheduler.EnqueueOptionRemoval(server.DB(), fieldID, "select", []int{1}); err != nil {
				t.Fatalf("EnqueueOptionRemoval #%d: %v", i, err)
			}
		}
		if got := countPendingJobsOfType(t, server, fieldID, "option_removal"); got != 2 {
			t.Errorf("expected 2 distinct option_removal jobs (no dedup), got %d", got)
		}
	})
}

// TestCFVIndexBuildScheduler covers the async Postgres index build (WI-416):
// enabling an index records the desired state and enqueues a build job that the
// scheduler runs CONCURRENTLY off the request thread. Postgres-only — SQLite
// materializes recorded indexes at startup instead.
func TestCFVIndexBuildScheduler(t *testing.T) {
	if GetDBType() != "postgres" {
		t.Skip("index_build job is Postgres-only; SQLite defers to startup materialization")
	}
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	t.Run("enable_index_defers_then_scheduler_builds_it_concurrently", func(t *testing.T) {
		fieldID := CreateTestCustomField(t, server, "Async Indexed "+ts(), "text", "")
		indexName := fmt.Sprintf("idx_cf_items_%d", fieldID)

		resp, body := tryEnableIndexOnItems(t, server, fieldID, "text", "")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("enable index: status=%d body=%s", resp.StatusCode, body)
		}
		// The response must report the build as deferred for items.
		var enableResp struct {
			IndexingDeferred *struct {
				Items  bool `json:"items"`
				Assets bool `json:"assets"`
			} `json:"indexing_deferred"`
		}
		if err := json.Unmarshal([]byte(body), &enableResp); err != nil {
			t.Fatalf("decode enable response: %v (body=%s)", err, body)
		}
		if enableResp.IndexingDeferred == nil || !enableResp.IndexingDeferred.Items {
			t.Fatalf("expected indexing_deferred.items=true, got %s", body)
		}
		// Intent is recorded synchronously (so the limit check / UI reflect it)…
		if !indexRecorded(t, server, fieldID, "items") {
			t.Fatal("expected custom_field_indexes row immediately after enable")
		}
		// …but the physical index does not exist yet.
		if pgIndexExists(t, server, indexName) {
			t.Fatal("physical index should not exist before the scheduler runs")
		}

		scheduler.NewCFVCleanupScheduler(server.DB()).RunOnceForTests()

		if !pgIndexExists(t, server, indexName) {
			t.Errorf("expected physical index %s after scheduler tick", indexName)
		}

		// Self-healing: a second build over an existing index drops and
		// recreates it (the DROP IF EXISTS path that cleans an INVALID stub).
		if err := scheduler.EnqueueIndexBuild(server.DB(), fieldID, "text", "items", indexName); err != nil {
			t.Fatalf("re-enqueue index build: %v", err)
		}
		scheduler.NewCFVCleanupScheduler(server.DB()).RunOnceForTests()
		if !pgIndexExists(t, server, indexName) {
			t.Errorf("expected index %s to survive a rebuild", indexName)
		}
	})

	t.Run("index_build_jobs_dedup_on_index_name", func(t *testing.T) {
		fieldID := CreateTestCustomField(t, server, "Index Dedup "+ts(), "text", "")
		indexName := fmt.Sprintf("idx_cf_items_%d", fieldID)
		for i := 0; i < 2; i++ {
			if err := scheduler.EnqueueIndexBuild(server.DB(), fieldID, "text", "items", indexName); err != nil {
				t.Fatalf("EnqueueIndexBuild #%d: %v", i, err)
			}
		}
		if got := countPendingJobsOfType(t, server, fieldID, "index_build"); got != 1 {
			t.Errorf("expected index_build to dedup to 1 pending job, got %d", got)
		}
	})
}

// --- helpers ---------------------------------------------------------------

// bulkInsertItemsWithCFV creates n items through the production create path
// with the given custom_field_values JSON, titled "<titlePrefix>-<i>" so
// tests can query them back by title. Workspace item numbers are
// production-assigned, so repeated calls in one workspace never collide.
func bulkInsertItemsWithCFV(t *testing.T, server *TestServer, workspaceID int, titlePrefix string, n int, cfvJSON string) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := services.CreateItem(server.DB(), services.ItemCreationParams{
			WorkspaceID:           workspaceID,
			Title:                 fmt.Sprintf("%s-%d", titlePrefix, i),
			CustomFieldValuesJSON: cfvJSON,
		}); err != nil {
			t.Fatalf("bulk insert item: %v", err)
		}
	}
}

// countItemsWithKey counts items whose title matches titleLike and whose cfv
// JSON still contains the "<fieldKey>": entry.
func countItemsWithKey(t *testing.T, server *TestServer, titleLike, fieldKey string) int {
	t.Helper()
	return countItemsMatching(t, server, titleLike, `%"`+fieldKey+`":%`)
}

func countItemsMatching(t *testing.T, server *TestServer, titleLike, cfvLike string) int {
	t.Helper()
	var n int
	// CAST so the cfv LIKE works on both SQLite (TEXT) and Postgres (JSONB).
	row := server.DB().QueryRow(
		`SELECT COUNT(*) FROM items WHERE title LIKE ? AND CAST(custom_field_values AS TEXT) LIKE ?`,
		titleLike, cfvLike,
	)
	if err := row.Scan(&n); err != nil {
		t.Fatalf("count items: %v", err)
	}
	return n
}

func countPendingJobsOfType(t *testing.T, server *TestServer, fieldID int, jobType string) int {
	t.Helper()
	var n int
	row := server.DB().QueryRow(
		`SELECT COUNT(*) FROM pending_custom_field_cleanups
		  WHERE field_id = ? AND job_type = ? AND status IN ('pending', 'running')`,
		fieldID, jobType,
	)
	if err := row.Scan(&n); err != nil {
		t.Fatalf("count pending jobs: %v", err)
	}
	return n
}

func indexRecorded(t *testing.T, server *TestServer, fieldID int, targetTable string) bool {
	t.Helper()
	var n int
	row := server.DB().QueryRow(
		`SELECT COUNT(*) FROM custom_field_indexes WHERE custom_field_id = ? AND target_table = ?`,
		fieldID, targetTable,
	)
	if err := row.Scan(&n); err != nil {
		t.Fatalf("check index record: %v", err)
	}
	return n > 0
}

// pgIndexExists reports whether a physical index of the given name exists in
// the public schema (Postgres only).
func pgIndexExists(t *testing.T, server *TestServer, indexName string) bool {
	t.Helper()
	var n int
	row := server.DB().QueryRow(
		`SELECT COUNT(*) FROM pg_indexes WHERE indexname = ?`,
		indexName,
	)
	if err := row.Scan(&n); err != nil {
		t.Fatalf("check pg index: %v", err)
	}
	return n > 0
}
