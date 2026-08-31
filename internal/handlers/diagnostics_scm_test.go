//go:build test

package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestDiagnosticsSCMConnectionsReturnsOperationalHealth(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	connectionID := createDiagnosticsSCMConnection(t, db, data.WorkspaceID)
	repo := repository.NewSCMHealthRepository(db)
	attemptedAt := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
	if _, err := repo.RecordResult(t.Context(), repository.SCMHealthResult{
		ConnectionID:     connectionID,
		Operation:        repository.SCMHealthOperationRepositorySync,
		AttemptedAt:      attemptedAt,
		CheckedResources: 2,
		FailedResources:  1,
		LastError:        "repository owner/broken: authentication failed",
	}); err != nil {
		t.Fatalf("record SCM health: %v", err)
	}

	handler := &DiagnosticsHandler{scmHealthRepo: repo}
	recorder := httptest.NewRecorder()
	handler.GetSCMConnections(recorder, httptest.NewRequest("GET", "/api/admin/diagnostics/scm-connections", nil))
	if recorder.Code != 200 {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response []repository.SCMConnectionDiagnostic
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response) != 1 {
		t.Fatalf("connections = %d, want 1: %+v", len(response), response)
	}
	connection := response[0]
	if connection.ID != connectionID || connection.WorkspaceKey != "TEST" || connection.ProviderName != "Diagnostics GitHub" || connection.ProviderSlug != "diagnostics-github" || connection.ProviderBaseURL != "https://github.example.test/api/v3" || connection.State != repository.SCMHealthStateUnhealthy {
		t.Fatalf("connection = %+v", connection)
	}
	if connection.RepositoryCount != 2 || connection.ActiveRepositoryCount != 1 {
		t.Fatalf("repository counts = %d/%d", connection.ActiveRepositoryCount, connection.RepositoryCount)
	}
	if len(connection.Repositories) != 2 || connection.Repositories[0].Name != "owner/active" || !connection.Repositories[0].Active || connection.Repositories[1].Name != "owner/inactive" || connection.Repositories[1].Active {
		t.Fatalf("repositories = %+v", connection.Repositories)
	}
	operation := findDiagnosticsSCMOperation(t, connection.Operations, repository.SCMHealthOperationRepositorySync)
	if operation.LastAttemptAt == nil || !operation.LastAttemptAt.Equal(attemptedAt) || operation.LastError != "repository owner/broken: authentication failed" {
		t.Fatalf("repository operation = %+v", operation)
	}
}

func createDiagnosticsSCMConnection(t *testing.T, db database.Database, workspaceID int) int {
	t.Helper()
	var providerID int
	if err := db.QueryRow(`
		INSERT INTO scm_providers (slug, name, provider_type, auth_method, base_url, enabled)
		VALUES ('diagnostics-github', 'Diagnostics GitHub', 'github', 'github_app', 'https://github.example.test/api/v3', true)
		RETURNING id
	`).Scan(&providerID); err != nil {
		t.Fatalf("create SCM provider: %v", err)
	}
	var connectionID int
	if err := db.QueryRow(`
		INSERT INTO workspace_scm_connections (workspace_id, scm_provider_id, enabled)
		VALUES (?, ?, true)
		RETURNING id
	`, workspaceID, providerID).Scan(&connectionID); err != nil {
		t.Fatalf("create SCM connection: %v", err)
	}
	for index, active := range []bool{true, false} {
		if _, err := db.Exec(`
			INSERT INTO workspace_repositories (
				workspace_scm_connection_id, repository_external_id, repository_name,
				repository_url, is_active
			) VALUES (?, ?, ?, ?, ?)
		`, connectionID, index+1, []string{"owner/active", "owner/inactive"}[index], "https://example.test/owner/repo", active); err != nil {
			t.Fatalf("create repository %d: %v", index, err)
		}
	}
	return connectionID
}

func findDiagnosticsSCMOperation(t *testing.T, operations []repository.SCMOperationDiagnostic, operation string) repository.SCMOperationDiagnostic {
	t.Helper()
	for _, candidate := range operations {
		if candidate.Operation == operation {
			return candidate
		}
	}
	t.Fatalf("operation %q not found", operation)
	return repository.SCMOperationDiagnostic{}
}
