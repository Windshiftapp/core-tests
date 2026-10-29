package handlers

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/testutils"
)

func TestStartImportForceReimportRejectsActiveOverlappingJob(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	tdb.SeedTestData(t)

	insertJiraImportConnection(t, tdb.Database, "conn-active")
	insertJiraImportJob(t, tdb.Database, "job-active", "conn-active", "running", []string{"APP"})

	handler := NewJiraImportHandler(tdb.Database, "jira-idempotency-test-secret", "", blobstore.Disabled())
	req := testutils.CreateJSONRequest(t, http.MethodPost, "/api/admin/jira-import/start", StartImportRequest{
		ConnectionID:  "conn-active",
		ProjectKeys:   []string{"APP"},
		ForceReimport: true,
		Mappings: ImportMappings{Workspaces: []WorkspaceMapping{{
			JiraKey:          "APP",
			JiraName:         "Application",
			CreateNew:        true,
			NewWorkspaceName: "Application",
			NewWorkspaceKey:  "APP",
		}}},
	})
	response := testutils.ExecuteAuthenticatedRequest(t, handler.StartImport, req, nil)

	response.AssertStatusCode(http.StatusConflict)
	if !strings.Contains(response.Body.String(), `"code":"JIRA_IMPORT_CONFLICT"`) ||
		!strings.Contains(response.Body.String(), `"job_id":"job-active"`) {
		t.Fatalf("conflict response = %s, want stable code and active job details", response.Body.String())
	}

	var jobs int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM jira_import_jobs WHERE connection_id = ?`, "conn-active").Scan(&jobs); err != nil {
		t.Fatalf("count import jobs: %v", err)
	}
	if jobs != 1 {
		t.Fatalf("import job count = %d, want only the active job", jobs)
	}
}

func TestEnqueueJiraImportSerializesConcurrentRequestsForSameScope(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	insertJiraImportConnection(t, tdb.Database, "conn-concurrent")

	handler := NewJiraImportHandler(tdb.Database, "jira-idempotency-test-secret", "", blobstore.Disabled())
	req := StartImportRequest{
		ConnectionID: "conn-concurrent",
		ProjectKeys:  []string{"APP"},
	}
	fingerprint, err := jiraImportPlanFingerprint(req)
	if err != nil {
		t.Fatalf("fingerprint import request: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := handler.enqueueJiraImport(req, fingerprint, nil)
			results <- err
		}()
	}
	close(start)

	var created, rejected int
	for range 2 {
		err := <-results
		if err == nil {
			created++
			continue
		}
		var conflict *jiraImportConflictError
		if errors.As(err, &conflict) {
			rejected++
			if len(conflict.Conflicts) != 1 || conflict.Conflicts[0].Status != "queued" {
				t.Fatalf("conflict details = %+v, want the queued winning job", conflict.Conflicts)
			}
			continue
		}
		t.Fatalf("enqueue error = %v, want nil or jiraImportConflictError", err)
	}
	if created != 1 || rejected != 1 {
		t.Fatalf("created/rejected = %d/%d, want 1/1", created, rejected)
	}

	var jobs int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM jira_import_jobs WHERE connection_id = ?`, "conn-concurrent").Scan(&jobs); err != nil {
		t.Fatalf("count import jobs: %v", err)
	}
	if jobs != 1 {
		t.Fatalf("import job count = %d, want 1", jobs)
	}
}

func TestEnqueueJiraImportUsesProjectOverlapForActiveConflicts(t *testing.T) {
	tests := []struct {
		name         string
		activeKeys   []string
		requestKeys  []string
		wantConflict bool
	}{
		{
			name:         "overlapping subset",
			activeKeys:   []string{"APP", "OPS"},
			requestKeys:  []string{"FIN", "OPS"},
			wantConflict: true,
		},
		{
			name:         "disjoint projects",
			activeKeys:   []string{"APP", "OPS"},
			requestKeys:  []string{"FIN"},
			wantConflict: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tdb := testutils.CreateTestDB(t, true)
			defer func() { _ = tdb.Close() }()
			insertJiraImportConnection(t, tdb.Database, "conn-overlap")
			insertJiraImportJob(t, tdb.Database, "job-running", "conn-overlap", "running", tt.activeKeys)

			handler := NewJiraImportHandler(tdb.Database, "jira-idempotency-test-secret", "", blobstore.Disabled())
			req := StartImportRequest{
				ConnectionID:  "conn-overlap",
				ProjectKeys:   tt.requestKeys,
				ForceReimport: true,
			}
			fingerprint, err := jiraImportPlanFingerprint(req)
			if err != nil {
				t.Fatalf("fingerprint import request: %v", err)
			}
			_, err = handler.enqueueJiraImport(req, fingerprint, nil)

			var conflict *jiraImportConflictError
			if gotConflict := errors.As(err, &conflict); gotConflict != tt.wantConflict {
				t.Fatalf("enqueue error = %v, conflict = %v, want %v", err, gotConflict, tt.wantConflict)
			}
		})
	}
}

func TestEnqueueJiraImportForceReimportAllowsCompletedHistory(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	insertJiraImportConnection(t, tdb.Database, "conn-completed")
	insertJiraImportJob(t, tdb.Database, "job-completed", "conn-completed", "completed", []string{"APP"})

	handler := NewJiraImportHandler(tdb.Database, "jira-idempotency-test-secret", "", blobstore.Disabled())
	req := StartImportRequest{
		ConnectionID:  "conn-completed",
		ProjectKeys:   []string{"APP"},
		ForceReimport: true,
	}
	fingerprint, err := jiraImportPlanFingerprint(req)
	if err != nil {
		t.Fatalf("fingerprint import request: %v", err)
	}
	result, err := handler.enqueueJiraImport(req, fingerprint, nil)
	if err != nil {
		t.Fatalf("force completed reimport: %v", err)
	}
	if len(result.Conflicts) != 1 || result.Conflicts[0].JobID != "job-completed" {
		t.Fatalf("historical conflicts = %+v, want job-completed", result.Conflicts)
	}

	var configJSON string
	if err := tdb.QueryRow(`SELECT config_json FROM jira_import_jobs WHERE id = ?`, result.JobID).Scan(&configJSON); err != nil {
		t.Fatalf("load forced import config: %v", err)
	}
	if !strings.Contains(configJSON, `"job_id":"job-completed"`) ||
		!strings.Contains(configJSON, `"force_reimport":true`) {
		t.Fatalf("forced import config = %s, want completed provenance and force flag", configJSON)
	}
}
