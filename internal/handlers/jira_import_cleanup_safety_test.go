//go:build test

package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/testutils"
	"windshift/internal/utils"
)

func TestJiraImportMappingWasCreatedRequiresExplicitValidProvenance(t *testing.T) {
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
			if got := jiraImportMappingWasCreated(test.metadata); got != test.want {
				t.Fatalf("jiraImportMappingWasCreated(%#v) = %t, want %t", test.metadata, got, test.want)
			}
		})
	}
}

func TestRecordJiraImportMappingAlwaysPersistsCreationProvenance(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	insertJiraImportConnection(t, tdb.Database, "connection-provenance")
	insertJiraImportJob(t, tdb.Database, "job-provenance", "connection-provenance", "running", []string{"SAFE"})
	handler := NewJiraImportHandler(tdb.Database, "jira-import-provenance-test-secret", "", blobstore.Disabled())

	handler.recordMapping("job-provenance", "item", "created", "SAFE-1", 101, nil)
	handler.recordMapping("job-provenance", "item", "reused", "SAFE-2", 102, map[string]any{"action": "reuse_existing"})
	handler.recordMapping("job-provenance", "item", "explicit-reuse", "SAFE-3", 103, map[string]any{"was_created": false})

	for _, test := range []struct {
		jiraID string
		want   bool
	}{
		{jiraID: "created", want: true},
		{jiraID: "reused", want: false},
		{jiraID: "explicit-reuse", want: false},
	} {
		var metadata sql.NullString
		if err := tdb.QueryRow(`
			SELECT metadata_json
			FROM jira_import_id_mappings
			WHERE job_id = 'job-provenance' AND entity_type = 'item' AND jira_id = ?
		`, test.jiraID).Scan(&metadata); err != nil {
			t.Fatalf("load %s mapping metadata: %v", test.jiraID, err)
		}
		if got := jiraImportMappingWasCreated(metadata); got != test.want {
			t.Fatalf("%s mapping was_created = %t, want %t; metadata=%s", test.jiraID, got, test.want, metadata.String)
		}
	}
}

func TestDeleteImportedDataPreservesReusedEntitiesAndStoresValidResultJSON(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	insertJiraImportConnection(t, tdb.Database, "connection-cleanup")
	insertJiraImportJob(t, tdb.Database, "job-cleanup-safety", "connection-cleanup", "completed", []string{"SAFE"})

	var timeProjectID int
	if err := tdb.QueryRow(`
		INSERT INTO time_projects (name, status, active)
		VALUES ('Existing time project', 'Active', true)
		RETURNING id
	`).Scan(&timeProjectID); err != nil {
		t.Fatalf("insert reused time project: %v", err)
	}
	var workspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key, time_project_id)
		VALUES ('Existing workspace', 'SAFE', ?)
		RETURNING id
	`, timeProjectID).Scan(&workspaceID); err != nil {
		t.Fatalf("insert reused workspace: %v", err)
	}
	var statusID int
	if err := tdb.QueryRow(`
		INSERT INTO statuses (name, category_id)
		VALUES ('Existing cleanup status', 1)
		RETURNING id
	`).Scan(&statusID); err != nil {
		t.Fatalf("insert reused status: %v", err)
	}
	var itemTypeID int
	if err := tdb.QueryRow(`
		INSERT INTO item_types (name)
		VALUES ('Existing cleanup item type')
		RETURNING id
	`).Scan(&itemTypeID); err != nil {
		t.Fatalf("insert reused item type: %v", err)
	}
	var reusedFieldID int
	if err := tdb.QueryRow(`
		INSERT INTO custom_field_definitions (name, field_type)
		VALUES ('Existing cleanup field', 'text')
		RETURNING id
	`).Scan(&reusedFieldID); err != nil {
		t.Fatalf("insert reused custom field: %v", err)
	}
	var unknownFieldID int
	if err := tdb.QueryRow(`
		INSERT INTO custom_field_definitions (name, field_type)
		VALUES ('Unknown provenance field', 'text')
		RETURNING id
	`).Scan(&unknownFieldID); err != nil {
		t.Fatalf("insert unknown-provenance custom field: %v", err)
	}
	var createdFieldID int
	if err := tdb.QueryRow(`
		INSERT INTO custom_field_definitions (name, field_type)
		VALUES ('Created cleanup field', 'text')
		RETURNING id
	`).Scan(&createdFieldID); err != nil {
		t.Fatalf("insert created custom field: %v", err)
	}
	var reusedScreenID int
	if err := tdb.QueryRow(`
		INSERT INTO screens (name, description)
		VALUES ('Existing cleanup screen', '')
		RETURNING id
	`).Scan(&reusedScreenID); err != nil {
		t.Fatalf("insert reused screen: %v", err)
	}
	var createdScreenID int
	if err := tdb.QueryRow(`
		INSERT INTO screens (name, description)
		VALUES ('Created cleanup screen', '')
		RETURNING id
	`).Scan(&createdScreenID); err != nil {
		t.Fatalf("insert created screen: %v", err)
	}

	mappings := []struct {
		entityType string
		jiraID     string
		windshift  int
		metadata   string
	}{
		{entityType: "workspace", jiraID: "SAFE", windshift: workspaceID, metadata: `{"was_created":false,"action":"reuse_existing"}`},
		{entityType: "status", jiraID: "status-1", windshift: statusID, metadata: `{"was_created":false,"action":"reuse_existing"}`},
		{entityType: "item_type", jiraID: "type-1", windshift: itemTypeID, metadata: `{"was_created":false,"action":"reuse_existing"}`},
		{entityType: "custom_field", jiraID: "field-reused", windshift: reusedFieldID, metadata: `{"was_created":false,"action":"map"}`},
		{entityType: "custom_field", jiraID: "field-unknown", windshift: unknownFieldID, metadata: `{}`},
		{entityType: "custom_field", jiraID: "field-created", windshift: createdFieldID, metadata: `{"was_created":true,"action":"create"}`},
		{entityType: "screen", jiraID: "screen-reused", windshift: reusedScreenID, metadata: `{"was_created":false,"action":"reuse_existing"}`},
		{entityType: "screen", jiraID: "screen-created", windshift: createdScreenID, metadata: `{"was_created":true,"action":"create"}`},
		{entityType: "time_project", jiraID: "time-1", windshift: timeProjectID, metadata: `{"was_created":false,"action":"reuse_workspace_default"}`},
	}
	for _, mapping := range mappings {
		if _, err := tdb.ExecWrite(`
			INSERT INTO jira_import_id_mappings
				(job_id, entity_type, jira_id, jira_key, windshift_id, metadata_json)
			VALUES ('job-cleanup-safety', ?, ?, 'SAFE', ?, ?)
		`, mapping.entityType, mapping.jiraID, mapping.windshift, mapping.metadata); err != nil {
			t.Fatalf("insert %s/%s mapping: %v", mapping.entityType, mapping.jiraID, err)
		}
	}

	handler := NewJiraImportHandler(tdb.Database, "jira-import-cleanup-safety-secret", "", blobstore.Disabled())
	workspaceCount, _ := handler.imports.EntityCounts("job-cleanup-safety")
	if workspaceCount != 0 {
		t.Fatalf("created workspace count = %d, want 0 for a reused workspace", workspaceCount)
	}

	req := httptest.NewRequest(
		http.MethodDelete,
		"/api/admin/jira-import/jobs/job-cleanup-safety/data",
		bytes.NewBufferString(`{"confirm_job_id":"job-cleanup-safety","confirm_workspace_count":0,"confirm_delete_imported_data":true}`),
	)
	req.SetPathValue("jobId", "job-cleanup-safety")
	w := httptest.NewRecorder()
	handler.DeleteImportedData(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DeleteImportedData() status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	for _, check := range []struct {
		name  string
		table string
		id    int
		want  int
	}{
		{name: "reused workspace", table: "workspaces", id: workspaceID, want: 1},
		{name: "reused status", table: "statuses", id: statusID, want: 1},
		{name: "reused item type", table: "item_types", id: itemTypeID, want: 1},
		{name: "reused custom field", table: "custom_field_definitions", id: reusedFieldID, want: 1},
		{name: "unknown-provenance custom field", table: "custom_field_definitions", id: unknownFieldID, want: 1},
		{name: "created custom field", table: "custom_field_definitions", id: createdFieldID, want: 0},
		{name: "reused screen", table: "screens", id: reusedScreenID, want: 1},
		{name: "created screen", table: "screens", id: createdScreenID, want: 0},
		{name: "reused time project", table: "time_projects", id: timeProjectID, want: 1},
	} {
		var count int
		if err := tdb.QueryRow("SELECT COUNT(*) FROM "+check.table+" WHERE id = ?", check.id).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if count != check.want {
			t.Fatalf("%s count = %d, want %d", check.name, count, check.want)
		}
	}

	var linkedTimeProject sql.NullInt64
	if err := tdb.QueryRow(`SELECT time_project_id FROM workspaces WHERE id = ?`, workspaceID).Scan(&linkedTimeProject); err != nil {
		t.Fatalf("load reused workspace time project: %v", err)
	}
	if !linkedTimeProject.Valid || int(linkedTimeProject.Int64) != timeProjectID {
		t.Fatalf("reused workspace time_project_id = %#v, want %d", linkedTimeProject, timeProjectID)
	}

	var status, resultJSON string
	if err := tdb.QueryRow(`
		SELECT status, result_json
		FROM jira_import_jobs
		WHERE id = 'job-cleanup-safety'
	`).Scan(&status, &resultJSON); err != nil {
		t.Fatalf("load cleanup job result: %v", err)
	}
	if status != "data_deleted" {
		t.Fatalf("job status = %q, want data_deleted", status)
	}
	var result struct {
		Deleted map[string]int `json:"deleted"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		t.Fatalf("result_json is invalid JSON: %v; result=%q", err, resultJSON)
	}
	if got := result.Deleted["custom_field"]; got != 1 {
		t.Fatalf("deleted custom fields = %d, want 1; result=%s", got, resultJSON)
	}
	if got := result.Deleted["screen"]; got != 1 {
		t.Fatalf("deleted screens = %d, want 1; result=%s", got, resultJSON)
	}
}

func TestDeleteJiraConnectionSoftDeletesAndKeepsImportHistory(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	insertJiraImportConnection(t, tdb.Database, "connection-with-history")
	insertJiraImportJob(t, tdb.Database, "job-history", "connection-with-history", "completed", []string{"SAFE"})
	if _, err := tdb.ExecWrite(`
		INSERT INTO jira_import_id_mappings
			(job_id, entity_type, jira_id, jira_key, windshift_id, metadata_json)
		VALUES ('job-history', 'item', 'issue-1', 'SAFE-1', 123, '{"was_created":true}')
	`); err != nil {
		t.Fatalf("insert import provenance: %v", err)
	}

	handler := NewJiraImportHandler(tdb.Database, "jira-import-connection-delete-secret", "", blobstore.Disabled())
	req := httptest.NewRequest(http.MethodDelete, "/api/admin/jira-import/connections/connection-with-history", nil)
	req.SetPathValue("connectionId", "connection-with-history")
	w := httptest.NewRecorder()
	handler.DeleteConnection(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DeleteConnection() status = %d, want %d; body=%s", w.Code, http.StatusNoContent, w.Body.String())
	}

	// The connection row survives as provenance but is hidden and its secret is cleared.
	var deletedAt sql.NullTime
	var credentials string
	if err := tdb.QueryRow(`
		SELECT deleted_at, encrypted_credentials
		FROM jira_import_connections WHERE id = 'connection-with-history'
	`).Scan(&deletedAt, &credentials); err != nil {
		t.Fatalf("load deleted connection: %v", err)
	}
	if !deletedAt.Valid {
		t.Fatal("deleted connection deleted_at is NULL, want a timestamp")
	}
	if credentials != "" {
		t.Fatalf("deleted connection credentials = %q, want cleared", credentials)
	}

	connections, err := handler.imports.ListConnections()
	if err != nil {
		t.Fatalf("list connections: %v", err)
	}
	if len(connections) != 0 {
		t.Fatalf("ListConnections() = %#v, want deleted connection hidden", connections)
	}

	for _, check := range []struct {
		name  string
		query string
	}{
		{name: "job", query: `SELECT COUNT(*) FROM jira_import_jobs WHERE id = 'job-history'`},
		{name: "mapping", query: `SELECT COUNT(*) FROM jira_import_id_mappings WHERE job_id = 'job-history'`},
	} {
		var count int
		if err := tdb.QueryRow(check.query).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if count != 1 {
			t.Fatalf("%s count = %d, want 1", check.name, count)
		}
	}
}

func TestDeleteJiraConnectionRejectsActiveImport(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	insertJiraImportConnection(t, tdb.Database, "connection-active")
	insertJiraImportJob(t, tdb.Database, "job-active", "connection-active", "running", []string{"SAFE"})

	handler := NewJiraImportHandler(tdb.Database, "jira-import-connection-active-secret", "", blobstore.Disabled())
	req := httptest.NewRequest(http.MethodDelete, "/api/admin/jira-import/connections/connection-active", nil)
	req.SetPathValue("connectionId", "connection-active")
	w := httptest.NewRecorder()
	handler.DeleteConnection(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("DeleteConnection() status = %d, want %d; body=%s", w.Code, http.StatusConflict, w.Body.String())
	}

	var deletedAt sql.NullTime
	if err := tdb.QueryRow(`SELECT deleted_at FROM jira_import_connections WHERE id = 'connection-active'`).Scan(&deletedAt); err != nil {
		t.Fatalf("load connection: %v", err)
	}
	if deletedAt.Valid {
		t.Fatal("active connection was soft-deleted, want it retained")
	}
}

func TestUpdateJiraConnectionReplacesCredentials(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/2/serverInfo":
			_ = json.NewEncoder(w).Encode(map[string]string{"baseUrl": server.URL, "serverTitle": "Rotated Jira"})
		case "/rest/api/2/myself":
			_ = json.NewEncoder(w).Encode(map[string]string{"displayName": "Rotated User"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	if _, err := tdb.ExecWrite(`
		INSERT INTO jira_import_connections
			(id, instance_url, email, encrypted_credentials, instance_name, deployment_type)
		VALUES ('connection-rotate', ?, 'dc@example.invalid', 'old-ciphertext', 'Old Jira', 'datacenter')
	`, server.URL); err != nil {
		t.Fatalf("insert data center connection: %v", err)
	}

	previousAllowLocal := utils.AllowLocalConnections()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(previousAllowLocal) })

	handler := NewJiraImportHandler(tdb.Database, "jira-import-connection-rotate-secret", "", blobstore.Disabled())
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/admin/jira-import/connections/connection-rotate",
		strings.NewReader(`{"email":"dc@example.invalid","api_token":"rotated-pat"}`),
	)
	req.SetPathValue("connectionId", "connection-rotate")
	w := httptest.NewRecorder()
	handler.UpdateConnection(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateConnection() status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var credentials, instanceName string
	if err := tdb.QueryRow(`
		SELECT encrypted_credentials, instance_name
		FROM jira_import_connections WHERE id = 'connection-rotate'
	`).Scan(&credentials, &instanceName); err != nil {
		t.Fatalf("load updated connection: %v", err)
	}
	if credentials == "" || credentials == "old-ciphertext" {
		t.Fatalf("stored credentials = %q, want a replacement ciphertext", credentials)
	}
	if instanceName != "Rotated User" {
		t.Fatalf("instance name = %q, want Rotated User", instanceName)
	}
}

func TestUpdateJiraConnectionRequiresToken(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	insertJiraImportConnection(t, tdb.Database, "connection-no-token")
	handler := NewJiraImportHandler(tdb.Database, "jira-import-connection-token-secret", "", blobstore.Disabled())
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/admin/jira-import/connections/connection-no-token",
		strings.NewReader(`{"email":"redacted@example.invalid","api_token":""}`),
	)
	req.SetPathValue("connectionId", "connection-no-token")
	w := httptest.NewRecorder()
	handler.UpdateConnection(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateConnection() status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}

	var credentials string
	if err := tdb.QueryRow(`SELECT encrypted_credentials FROM jira_import_connections WHERE id = 'connection-no-token'`).Scan(&credentials); err != nil {
		t.Fatalf("load connection: %v", err)
	}
	if credentials != "{}" {
		t.Fatalf("stored credentials = %q, want unchanged fixture value", credentials)
	}
}

func TestUpdateJiraConnectionRejectsUnknownConnection(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	handler := NewJiraImportHandler(tdb.Database, "jira-import-connection-missing-secret", "", blobstore.Disabled())
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/admin/jira-import/connections/missing",
		strings.NewReader(`{"email":"redacted@example.invalid","api_token":"token"}`),
	)
	req.SetPathValue("connectionId", "missing")
	w := httptest.NewRecorder()
	handler.UpdateConnection(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("UpdateConnection() status = %d, want %d; body=%s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestHideJiraImportJobClearsHistoryWithoutDeletingData(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	insertJiraImportConnection(t, tdb.Database, "connection-hide-job")
	insertJiraImportJob(t, tdb.Database, "job-hide-handler", "connection-hide-job", "completed", []string{"SAFE"})
	if _, err := tdb.ExecWrite(`
		INSERT INTO jira_import_id_mappings
			(job_id, entity_type, jira_id, jira_key, windshift_id, metadata_json)
		VALUES ('job-hide-handler', 'item', 'issue-1', 'SAFE-1', 321, '{"was_created":true}')
	`); err != nil {
		t.Fatalf("insert import provenance: %v", err)
	}

	handler := NewJiraImportHandler(tdb.Database, "jira-import-job-hide-secret", "", blobstore.Disabled())
	req := httptest.NewRequest(http.MethodDelete, "/api/admin/jira-import/jobs/job-hide-handler", nil)
	req.SetPathValue("jobId", "job-hide-handler")
	w := httptest.NewRecorder()
	handler.HideImportJob(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("HideImportJob() status = %d, want %d; body=%s", w.Code, http.StatusNoContent, w.Body.String())
	}

	var hiddenAt sql.NullTime
	if err := tdb.QueryRow(`SELECT hidden_at FROM jira_import_jobs WHERE id = 'job-hide-handler'`).Scan(&hiddenAt); err != nil {
		t.Fatalf("load hidden job: %v", err)
	}
	if !hiddenAt.Valid {
		t.Fatal("job hidden_at is NULL, want a timestamp")
	}

	var mappingCount int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM jira_import_id_mappings WHERE job_id = 'job-hide-handler'`).Scan(&mappingCount); err != nil {
		t.Fatalf("count mappings: %v", err)
	}
	if mappingCount != 1 {
		t.Fatalf("mapping count = %d, want 1 (data must survive hiding)", mappingCount)
	}

	jobs, err := handler.imports.ListJobs()
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("ListJobs() = %#v, want hidden job excluded", jobs)
	}
}

func TestHideJiraImportJobRejectsActiveImport(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	insertJiraImportConnection(t, tdb.Database, "connection-hide-active")
	insertJiraImportJob(t, tdb.Database, "job-hide-active", "connection-hide-active", "running", []string{"SAFE"})

	handler := NewJiraImportHandler(tdb.Database, "jira-import-job-hide-active-secret", "", blobstore.Disabled())
	req := httptest.NewRequest(http.MethodDelete, "/api/admin/jira-import/jobs/job-hide-active", nil)
	req.SetPathValue("jobId", "job-hide-active")
	w := httptest.NewRecorder()
	handler.HideImportJob(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("HideImportJob() status = %d, want %d; body=%s", w.Code, http.StatusConflict, w.Body.String())
	}
}

func TestPlanJiraWorkspaceKeysAssignsUniqueAliasesForCollisions(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	if _, err := tdb.ExecWrite(`
		INSERT INTO workspaces (name, key)
		VALUES ('Existing application', 'APP'),
		       ('Existing Jira alias', 'JIRA_APP')
	`); err != nil {
		t.Fatalf("insert colliding workspaces: %v", err)
	}

	handler := NewJiraImportHandler(tdb.Database, "jira-workspace-plan-test-secret", "", blobstore.Disabled())
	plans, err := handler.planJiraWorkspaceKeys([]string{"APP", "FREE"})
	if err != nil {
		t.Fatalf("plan Jira workspace keys: %v", err)
	}
	if got := plans["APP"]; !got.Collision || got.Key != "JIRA_APP_2" {
		t.Fatalf("APP plan = %#v, want collision alias JIRA_APP_2", got)
	}
	if got := plans["FREE"]; got.Collision || got.Key != "FREE" {
		t.Fatalf("FREE plan = %#v, want unchanged non-collision key", got)
	}
}

func TestStartJiraImportRequiresWorkspaceAliasAcknowledgement(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	insertJiraImportConnection(t, tdb.Database, "connection-alias")
	if _, err := tdb.ExecWrite(`
		INSERT INTO workspaces (name, key)
		VALUES ('Existing application', 'APP')
	`); err != nil {
		t.Fatalf("insert colliding workspace: %v", err)
	}

	handler := NewJiraImportHandler(tdb.Database, "jira-workspace-alias-test-secret", "", blobstore.Disabled())
	body := `{
		"connection_id":"connection-alias",
		"project_keys":["APP"],
		"mappings":{"workspaces":[{
			"jiraKey":"APP",
			"jiraName":"Jira application",
			"createNew":true,
			"newWorkspaceName":"Jira application",
			"newWorkspaceKey":"JIRA_APP",
			"keyAliasAcknowledged":false
		}]},
		"xray":{"import_tests":false}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/jira-import/start", strings.NewReader(body))
	w := httptest.NewRecorder()
	handler.StartImport(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("StartImport() status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "acknowledge") ||
		!strings.Contains(w.Body.String(), "JIRA_APP") {
		t.Fatalf("StartImport() body does not explain required alias acknowledgement: %s", w.Body.String())
	}

	var jobs int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM jira_import_jobs`).Scan(&jobs); err != nil {
		t.Fatalf("count Jira import jobs: %v", err)
	}
	if jobs != 0 {
		t.Fatalf("Jira import jobs = %d, want 0 after missing acknowledgement", jobs)
	}
}

func TestEnsureJiraWorkspaceCreatesAliasWithoutChangingExistingWorkspace(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	insertJiraImportConnection(t, tdb.Database, "connection-create-alias")
	insertJiraImportJob(t, tdb.Database, "job-create-alias", "connection-create-alias", "running", []string{"APP"})
	var existingID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key)
		VALUES ('Existing application', 'APP')
		RETURNING id
	`).Scan(&existingID); err != nil {
		t.Fatalf("insert existing workspace: %v", err)
	}

	handler := NewJiraImportHandler(tdb.Database, "jira-workspace-create-alias-secret", "", blobstore.Disabled())
	createdID, err := handler.ensureWorkspace(context.Background(), "job-create-alias", &WorkspaceMapping{
		JiraKey:              "APP",
		JiraName:             "Jira application",
		CreateNew:            true,
		NewWorkspaceName:     "Jira application",
		NewWorkspaceKey:      "JIRA_APP",
		KeyAliasAcknowledged: true,
	}, 0)
	if err != nil {
		t.Fatalf("ensure aliased Jira workspace: %v", err)
	}
	if createdID == existingID {
		t.Fatalf("created workspace ID = existing workspace ID = %d", existingID)
	}

	var existingName, createdKey string
	if err := tdb.QueryRow(`SELECT name FROM workspaces WHERE id = ?`, existingID).Scan(&existingName); err != nil {
		t.Fatalf("load existing workspace: %v", err)
	}
	if existingName != "Existing application" {
		t.Fatalf("existing workspace name = %q, want unchanged", existingName)
	}
	if err := tdb.QueryRow(`SELECT key FROM workspaces WHERE id = ?`, createdID).Scan(&createdKey); err != nil {
		t.Fatalf("load created workspace: %v", err)
	}
	if createdKey != "JIRA_APP" {
		t.Fatalf("created workspace key = %q, want JIRA_APP", createdKey)
	}

	var metadata sql.NullString
	if err := tdb.QueryRow(`
		SELECT metadata_json
		FROM jira_import_id_mappings
		WHERE job_id = 'job-create-alias' AND entity_type = 'workspace' AND jira_id = 'APP'
	`).Scan(&metadata); err != nil {
		t.Fatalf("load created workspace mapping: %v", err)
	}
	if !jiraImportMappingWasCreated(metadata) {
		t.Fatalf("created workspace mapping lacks creation provenance: %s", metadata.String)
	}
}
