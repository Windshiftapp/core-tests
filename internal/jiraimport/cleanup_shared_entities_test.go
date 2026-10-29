//go:build test

package jiraimport

import (
	"encoding/json"
	"errors"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/testutils"
)

func TestDeleteImportedDataPreservesGlobalEntitiesMappedByAnotherJob(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	insertConnection(t, tdb, "connection-shared-cleanup")
	insertJob(t, tdb, "job-shared-owner", "connection-shared-cleanup", "completed")
	insertJob(t, tdb, "job-shared-consumer", "connection-shared-cleanup", "completed")
	service := New(tdb.Database, blobstore.Disabled())

	entities := createCleanupGlobalEntities(t, tdb, "Cross-job")
	for entityType, id := range entities {
		if err := service.RecordMapping("job-shared-owner", entityType, entityType+"-owner", "SHARED", id, map[string]any{"action": "create"}); err != nil {
			t.Fatalf("record owner %s mapping: %v", entityType, err)
		}
		if err := service.RecordMapping("job-shared-consumer", entityType, entityType+"-consumer", "SHARED", id, map[string]any{"action": "reuse_existing"}); err != nil {
			t.Fatalf("record consumer %s mapping: %v", entityType, err)
		}
	}

	deleted, err := service.DeleteImportedData("job-shared-owner", 0)
	if err != nil {
		t.Fatalf("delete shared owner data: %v", err)
	}
	if len(deleted) != 0 {
		t.Fatalf("deleted shared entities = %#v, want none", deleted)
	}
	assertCleanupGlobalEntities(t, tdb, entities, true)
	assertCleanupMappingCount(t, tdb, "job-shared-owner", 0)
	assertCleanupMappingCount(t, tdb, "job-shared-consumer", len(entities))
	assertCleanupResult(t, tdb, "job-shared-owner", "data_deleted", len(entities), 0)
}

func TestDeleteImportedDataPreservesLiveGlobalEntitiesAndDeletesUnusedOnes(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	insertConnection(t, tdb, "connection-live-cleanup")
	insertJob(t, tdb, "job-live-cleanup", "connection-live-cleanup", "completed")
	service := New(tdb.Database, blobstore.Disabled())

	live := createCleanupGlobalEntities(t, tdb, "Live")
	unused := createCleanupGlobalEntities(t, tdb, "Unused")
	createCleanupGlobalReferences(t, tdb, live)
	for entityType, id := range live {
		if err := service.RecordMapping("job-live-cleanup", entityType, "live-"+entityType, "LIVE", id, map[string]any{"action": "create"}); err != nil {
			t.Fatalf("record live %s mapping: %v", entityType, err)
		}
	}
	for entityType, id := range unused {
		if err := service.RecordMapping("job-live-cleanup", entityType, "unused-"+entityType, "UNUSED", id, map[string]any{"action": "create"}); err != nil {
			t.Fatalf("record unused %s mapping: %v", entityType, err)
		}
	}

	deleted, err := service.DeleteImportedData("job-live-cleanup", 0)
	if err != nil {
		t.Fatalf("delete imported data: %v", err)
	}
	for entityType := range unused {
		if deleted[entityType] != 1 {
			t.Fatalf("deleted[%q] = %d, want 1; all=%#v", entityType, deleted[entityType], deleted)
		}
	}
	assertCleanupGlobalEntities(t, tdb, live, true)
	assertCleanupGlobalEntities(t, tdb, unused, false)
	assertCleanupResult(t, tdb, "job-live-cleanup", "data_deleted", len(live), 0)
}

func TestDeleteImportedDataRetainsMappingsAndJobStatusAfterFailure(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	insertConnection(t, tdb, "connection-retry-cleanup")
	insertJob(t, tdb, "job-retry-cleanup", "connection-retry-cleanup", "completed")
	service := New(tdb.Database, blobstore.Disabled())

	if err := service.RecordMapping(
		"job-retry-cleanup",
		"portal_customer_channel",
		"missing-channel",
		"RETRY",
		999999,
		map[string]any{"action": "create"},
	); err != nil {
		t.Fatalf("record invalid cleanup mapping: %v", err)
	}

	deleted, err := service.DeleteImportedData("job-retry-cleanup", 0)
	var cleanupErr *CleanupError
	if !errors.As(err, &cleanupErr) {
		t.Fatalf("cleanup error = %v, want CleanupError", err)
	}
	if len(deleted) != 0 {
		t.Fatalf("deleted after failed cleanup = %#v, want none", deleted)
	}
	if len(cleanupErr.Failures) != 1 || cleanupErr.Failures[0].EntityType != "portal_customer_channel" {
		t.Fatalf("cleanup failures = %#v, want portal_customer_channel", cleanupErr.Failures)
	}
	assertCleanupMappingCount(t, tdb, "job-retry-cleanup", 1)
	assertCleanupResult(t, tdb, "job-retry-cleanup", "completed", 0, 1)

	if _, err := tdb.ExecWrite(`
		UPDATE jira_import_id_mappings
		SET metadata_json = '{"action":"create","was_created":true,"channel_id":999998}'
		WHERE job_id = 'job-retry-cleanup'
	`); err != nil {
		t.Fatalf("repair cleanup mapping: %v", err)
	}
	if _, err := service.DeleteImportedData("job-retry-cleanup", 0); err != nil {
		t.Fatalf("retry cleanup: %v", err)
	}
	assertCleanupMappingCount(t, tdb, "job-retry-cleanup", 0)
	assertCleanupResult(t, tdb, "job-retry-cleanup", "data_deleted", 0, 0)
}

func createCleanupGlobalEntities(t *testing.T, tdb *testutils.TestDB, prefix string) map[string]int {
	t.Helper()
	entities := make(map[string]int)
	queries := map[string]string{
		"status":       "INSERT INTO statuses (name, category_id) VALUES (?, 1) RETURNING id",
		"item_type":    "INSERT INTO item_types (name) VALUES (?) RETURNING id",
		"screen":       "INSERT INTO screens (name, description) VALUES (?, '') RETURNING id",
		"custom_field": "INSERT INTO custom_field_definitions (name, field_type) VALUES (?, 'text') RETURNING id",
	}
	for entityType, query := range queries {
		var id int
		if err := tdb.QueryRow(query, prefix+" cleanup "+entityType).Scan(&id); err != nil {
			t.Fatalf("create %s: %v", entityType, err)
		}
		entities[entityType] = id
	}
	return entities
}

func createCleanupGlobalReferences(t *testing.T, tdb *testutils.TestDB, entities map[string]int) {
	t.Helper()
	var workflowID int
	if err := tdb.QueryRow(`
		INSERT INTO workflows (name, description) VALUES ('Live cleanup workflow', '') RETURNING id
	`).Scan(&workflowID); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	if _, err := tdb.ExecWrite(`
		INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id)
		VALUES (?, NULL, ?)
	`, workflowID, entities["status"]); err != nil {
		t.Fatalf("reference status: %v", err)
	}

	var configurationSetID int
	if err := tdb.QueryRow(`
		INSERT INTO configuration_sets (name, description, default_item_type_id)
		VALUES ('Live cleanup configuration', '', ?)
		RETURNING id
	`, entities["item_type"]).Scan(&configurationSetID); err != nil {
		t.Fatalf("reference item type: %v", err)
	}
	if _, err := tdb.ExecWrite(`
		INSERT INTO configuration_set_screens (configuration_set_id, screen_id, context)
		VALUES (?, ?, 'create')
	`, configurationSetID, entities["screen"]); err != nil {
		t.Fatalf("reference screen: %v", err)
	}
	if _, err := tdb.ExecWrite(`
		INSERT INTO screen_fields (screen_id, field_type, field_identifier)
		VALUES (?, 'custom', ?)
	`, entities["screen"], entities["custom_field"]); err != nil {
		t.Fatalf("reference custom field: %v", err)
	}
}

func assertCleanupGlobalEntities(t *testing.T, tdb *testutils.TestDB, entities map[string]int, want bool) {
	t.Helper()
	tables := map[string]string{
		"status":       "statuses",
		"item_type":    "item_types",
		"screen":       "screens",
		"custom_field": "custom_field_definitions",
	}
	for entityType, id := range entities {
		assertRowExists(t, tdb, tables[entityType], id, want)
	}
}

func assertCleanupMappingCount(t *testing.T, tdb *testutils.TestDB, jobID string, want int) {
	t.Helper()
	var count int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM jira_import_id_mappings WHERE job_id = ?`, jobID).Scan(&count); err != nil {
		t.Fatalf("count cleanup mappings: %v", err)
	}
	if count != want {
		t.Fatalf("cleanup mapping count = %d, want %d", count, want)
	}
}

func assertCleanupResult(t *testing.T, tdb *testutils.TestDB, jobID, wantStatus string, wantRetained, wantFailed int) {
	t.Helper()
	var status, raw string
	if err := tdb.QueryRow(`SELECT status, result_json FROM jira_import_jobs WHERE id = ?`, jobID).Scan(&status, &raw); err != nil {
		t.Fatalf("load cleanup result: %v", err)
	}
	if status != wantStatus {
		t.Fatalf("cleanup status = %q, want %q", status, wantStatus)
	}
	var result cleanupResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode cleanup result %q: %v", raw, err)
	}
	retained := 0
	for _, count := range result.Retained {
		retained += count
	}
	if retained != wantRetained {
		t.Fatalf("retained entity count = %d, want %d; result=%s", retained, wantRetained, raw)
	}
	if len(result.Failed) != wantFailed {
		t.Fatalf("failed entity count = %d, want %d; result=%s", len(result.Failed), wantFailed, raw)
	}
}
