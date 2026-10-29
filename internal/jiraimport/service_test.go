//go:build test

package jiraimport

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"uuid"
	"windshift/internal/blobstore"
	"windshift/internal/testutils"
)

func TestMappingWasCreatedRequiresExplicitValidProvenance(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		metadata sql.NullString
		want     bool
	}{
		{name: "null", metadata: sql.NullString{}, want: false},
		{name: "empty", metadata: sql.NullString{String: " ", Valid: true}, want: false},
		{name: "malformed", metadata: sql.NullString{String: "{", Valid: true}, want: false},
		{name: "missing flag", metadata: sql.NullString{String: `{"action":"create"}`, Valid: true}, want: false},
		{name: "explicit reuse", metadata: sql.NullString{String: `{"was_created":false}`, Valid: true}, want: false},
		{name: "explicit creation", metadata: sql.NullString{String: `{"was_created":true}`, Valid: true}, want: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := MappingWasCreated(test.metadata); got != test.want {
				t.Fatalf("MappingWasCreated(%#v) = %t, want %t", test.metadata, got, test.want)
			}
		})
	}
}

func TestServiceOwnsJobLifecycleAndConflictDetection(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	insertConnection(t, tdb, "connection-lifecycle")
	service := New(tdb.Database, blobstore.Disabled())

	config := []byte(`{"project_keys":[" WS ","OPS"],"open_issues_only":false}`)
	jobID, err := service.CreateJob(CreateJobInput{ConnectionID: "connection-lifecycle", ConfigJSON: config})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	status, err := service.GetJobStatus(jobID)
	if err != nil {
		t.Fatalf("get queued job: %v", err)
	}
	if status.Status != "queued" {
		t.Fatalf("queued job status = %q, want queued", status.Status)
	}

	token := "lifecycle-worker"
	if err := service.ClaimJob(jobID, token); err != nil {
		t.Fatalf("claim job: %v", err)
	}
	progress := &Progress{Phase: "issues", TotalProjects: 2, ImportedIssues: 3}
	if err := service.UpdateStatus(jobID, token, "running", "issues", progress, ""); err != nil {
		t.Fatalf("mark job running: %v", err)
	}
	status, err = service.GetJobStatus(jobID)
	if err != nil {
		t.Fatalf("get running job: %v", err)
	}
	if status.Status != "running" || status.Phase != "issues" || status.StartedAt == nil {
		t.Fatalf("running status = %#v", status)
	}
	if got := status.Progress["imported_issues"]; got != float64(3) {
		t.Fatalf("imported issue progress = %#v, want 3", got)
	}

	conflicts, err := service.Conflicts("connection-lifecycle", []string{"ws"})
	if err != nil {
		t.Fatalf("find conflicts: %v", err)
	}
	if len(conflicts) != 1 || conflicts[0].JobID != jobID {
		t.Fatalf("conflicts = %#v, want job %s", conflicts, jobID)
	}
	if got, want := conflicts[0].ProjectKeys, []string{"WS", "OPS"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("normalized project keys = %#v, want %#v", got, want)
	}
}

func TestServiceFindsWorkspaceMilestoneThroughPlanningService(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	var workspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key) VALUES ('Jira milestone workspace', 'JMW') RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if _, err := tdb.ExecWrite(`
		INSERT INTO milestones (name, status, is_global, workspace_id, position)
		VALUES ('Jira Release', 'planning', false, ?, 1000)
	`, workspaceID); err != nil {
		t.Fatalf("insert milestone: %v", err)
	}
	id, found, err := New(tdb.Database, blobstore.Disabled()).FindMilestone("Jira Release", workspaceID)
	if err != nil || !found || id == 0 {
		t.Fatalf("find milestone = %d, %t, %v", id, found, err)
	}
}

func TestRecordMappingPreservesCreationOwnershipAcrossRetry(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	insertConnection(t, tdb, "connection-provenance")
	insertJob(t, tdb, "job-provenance", "connection-provenance", "running")
	service := New(tdb.Database, blobstore.Disabled())

	if err := service.RecordMapping("job-provenance", "item", "created", "WS-1", 101, nil); err != nil {
		t.Fatalf("record created mapping: %v", err)
	}
	if err := service.RecordMapping("job-provenance", "item", "created", "WS-1", 101, map[string]interface{}{"action": "reuse_existing"}); err != nil {
		t.Fatalf("record retry mapping: %v", err)
	}
	if err := service.RecordMapping("job-provenance", "item", "reused", "WS-2", 102, map[string]interface{}{"action": "reuse_existing"}); err != nil {
		t.Fatalf("record reused mapping: %v", err)
	}

	assertMappingOwnership(t, tdb, "job-provenance", "created", true)
	assertMappingOwnership(t, tdb, "job-provenance", "reused", false)
}

func TestDeleteImportedDataEnforcesStateCountAndProvenance(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	insertConnection(t, tdb, "connection-cleanup")
	insertJob(t, tdb, "active-job", "connection-cleanup", "running")
	insertJob(t, tdb, "cleanup-job", "connection-cleanup", "completed")
	service := New(tdb.Database, blobstore.Disabled())

	if _, err := service.DeleteImportedData("active-job", 0); !errors.Is(err, ErrJobActive) {
		t.Fatalf("active cleanup error = %v, want ErrJobActive", err)
	}

	var reusedFieldID, createdFieldID int
	if err := tdb.QueryRow(`
		INSERT INTO custom_field_definitions (name, field_type)
		VALUES ('Existing field', 'text') RETURNING id
	`).Scan(&reusedFieldID); err != nil {
		t.Fatalf("insert reused field: %v", err)
	}
	if err := tdb.QueryRow(`
		INSERT INTO custom_field_definitions (name, field_type)
		VALUES ('Imported field', 'text') RETURNING id
	`).Scan(&createdFieldID); err != nil {
		t.Fatalf("insert created field: %v", err)
	}
	if err := service.RecordMapping("cleanup-job", "custom_field", "reused", "WS", reusedFieldID, map[string]interface{}{"action": "map"}); err != nil {
		t.Fatalf("record reused field: %v", err)
	}
	if err := service.RecordMapping("cleanup-job", "custom_field", "created", "WS", createdFieldID, map[string]interface{}{"action": "create"}); err != nil {
		t.Fatalf("record created field: %v", err)
	}
	if err := service.RecordMapping("cleanup-job", "workspace", "WS", "WS", 999999, map[string]interface{}{"action": "create"}); err != nil {
		t.Fatalf("record workspace ownership: %v", err)
	}

	if _, err := service.DeleteImportedData("cleanup-job", 0); err == nil {
		t.Fatal("cleanup with stale workspace count succeeded")
	} else {
		var mismatch *WorkspaceCountMismatchError
		if !errors.As(err, &mismatch) || mismatch.Current != 1 {
			t.Fatalf("cleanup count error = %#v, want current count 1", err)
		}
	}

	deleted, err := service.DeleteImportedData("cleanup-job", 1)
	if err != nil {
		t.Fatalf("delete imported data: %v", err)
	}
	if deleted["custom_field"] != 1 {
		t.Fatalf("deleted custom fields = %d, want 1", deleted["custom_field"])
	}
	assertRowExists(t, tdb, "custom_field_definitions", reusedFieldID, true)
	assertRowExists(t, tdb, "custom_field_definitions", createdFieldID, false)

	var status, resultJSON string
	if err := tdb.QueryRow(`
		SELECT status, result_json FROM jira_import_jobs WHERE id = 'cleanup-job'
	`).Scan(&status, &resultJSON); err != nil {
		t.Fatalf("load cleanup result: %v", err)
	}
	if status != "data_deleted" {
		t.Fatalf("cleanup job status = %q, want data_deleted", status)
	}
	var result map[string]map[string]int
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		t.Fatalf("decode cleanup result %q: %v", resultJSON, err)
	}
	if result["deleted"]["custom_field"] != 1 {
		t.Fatalf("stored cleanup result = %#v", result)
	}

	deleted, err = service.DeleteImportedData("cleanup-job", 0)
	if err != nil {
		t.Fatalf("repeat cleanup: %v", err)
	}
	if len(deleted) != 0 {
		t.Fatalf("repeat cleanup deleted = %#v, want empty idempotent result", deleted)
	}
}

func insertConnection(t *testing.T, db interface {
	ExecWrite(string, ...interface{}) (sql.Result, error)
}, id string) {
	t.Helper()
	if _, err := db.ExecWrite(`
		INSERT INTO jira_import_connections
			(id, instance_url, email, encrypted_credentials, instance_name, deployment_type)
		VALUES (?, 'https://jira.example.invalid', 'user@example.invalid', 'encrypted', 'Test Jira', 'cloud')
	`, id); err != nil {
		t.Fatalf("insert connection: %v", err)
	}
}

func insertJob(t *testing.T, db interface {
	ExecWrite(string, ...interface{}) (sql.Result, error)
}, id, connectionID, status string) {
	t.Helper()
	// Active fixtures represent a live worker, so they carry a fresh lease;
	// reconciliation tests overwrite it with an expired value.
	var lease any
	if status == "queued" || status == "running" {
		lease = time.Now().Add(2 * time.Minute).Unix()
	}
	if _, err := db.ExecWrite(`
		INSERT INTO jira_import_jobs (id, connection_id, status, scope, config_json, lease_expires_at)
		VALUES (?, ?, ?, 'work_items', '{"project_keys":["WS"]}', ?)
	`, id, connectionID, status, lease); err != nil {
		t.Fatalf("insert job: %v", err)
	}
}

func assertMappingOwnership(t *testing.T, db interface {
	QueryRow(string, ...interface{}) *sql.Row
}, jobID, jiraID string, want bool) {
	t.Helper()
	var metadata sql.NullString
	if err := db.QueryRow(`
		SELECT metadata_json FROM jira_import_id_mappings
		WHERE job_id = ? AND entity_type = 'item' AND jira_id = ?
	`, jobID, jiraID).Scan(&metadata); err != nil {
		t.Fatalf("load mapping %s: %v", jiraID, err)
	}
	if got := MappingWasCreated(metadata); got != want {
		t.Fatalf("mapping %s ownership = %t, want %t; metadata=%s", jiraID, got, want, metadata.String)
	}
}

func assertRowExists(t *testing.T, db interface {
	QueryRow(string, ...interface{}) *sql.Row
}, table string, id int, want bool) {
	t.Helper()
	var count int
	query := "SELECT COUNT(*) FROM " + table + " WHERE id = ?" //nolint:gosec // test helper caller supplies a fixed schema table.
	if err := db.QueryRow(query, id).Scan(&count); err != nil {
		t.Fatalf("count %s row %d: %v", table, id, err)
	}
	if got := count == 1; got != want {
		t.Fatalf("%s row %d exists = %t, want %t", table, id, got, want)
	}
}

func (s *Service) CreateJob(input CreateJobInput) (string, error) {
	jobID := uuid.New().String()
	_, err := s.db.ExecWrite(`
		INSERT INTO jira_import_jobs (id, connection_id, status, scope, config_json, created_by)
		VALUES (?, ?, 'queued', 'work_items', ?, ?)
	`, jobID, input.ConnectionID, string(input.ConfigJSON), input.CreatedBy)
	if err != nil {
		return "", err
	}
	return jobID, nil
}

func (s *Service) Conflicts(connectionID string, projectKeys []string) ([]Conflict, error) {
	requested := projectKeySet(projectKeys)
	if len(requested) == 0 || connectionID == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT id, status, config_json, created_at, completed_at
		FROM jira_import_jobs
		WHERE connection_id = ? AND scope = 'work_items' AND status <> 'data_deleted'
		ORDER BY created_at DESC
	`, connectionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var conflicts []Conflict
	for rows.Next() {
		var conflict Conflict
		var configJSON string
		var completedAt sql.NullTime
		if err := rows.Scan(&conflict.JobID, &conflict.Status, &configJSON, &conflict.CreatedAt, &completedAt); err != nil {
			return nil, err
		}
		conflict.ProjectKeys = ProjectKeys(configJSON)
		if !projectKeysOverlap(requested, conflict.ProjectKeys) {
			continue
		}
		if completedAt.Valid {
			conflict.CompletedAt = &completedAt.Time
		}
		conflicts = append(conflicts, conflict)
	}
	return conflicts, rows.Err()
}
