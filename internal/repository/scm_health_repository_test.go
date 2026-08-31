//go:build test

package repository

import (
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

func TestSCMHealthRepositoryTracksFailuresRecoveryAndIsolation(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	firstConnection := createSCMHealthConnection(t, db, data.WorkspaceID, "first", true)
	secondConnection := createSCMHealthConnection(t, db, data.WorkspaceID, "second", false)
	repo := NewSCMHealthRepository(db)
	firstAttempt := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)

	for attempt := range 2 {
		transition, err := repo.RecordResult(t.Context(), SCMHealthResult{
			ConnectionID:     firstConnection,
			Operation:        SCMHealthOperationPRLinkRefresh,
			AttemptedAt:      firstAttempt.Add(time.Duration(attempt) * 15 * time.Minute),
			CheckedResources: 2,
			FailedResources:  1,
			LastError:        "link 7: GET https://user:secret@example.test/pull/7?access_token=plaintext: resource not found",
		})
		if err != nil {
			t.Fatalf("RecordResult() failure %d: %v", attempt+1, err)
		}
		if attempt == 0 && !transition.BecameUnhealthy {
			t.Fatal("first failure did not report unhealthy transition")
		}
		if attempt == 1 && (transition.BecameUnhealthy || transition.ErrorChanged) {
			t.Fatalf("unchanged repeated failure transition = %+v", transition)
		}
	}

	connections, err := repo.ListConnectionDiagnostics(t.Context())
	if err != nil {
		t.Fatalf("ListConnectionDiagnostics(): %v", err)
	}
	first := findSCMConnectionDiagnostic(t, connections, firstConnection)
	if first.State != SCMHealthStateUnhealthy || first.Healthy {
		t.Fatalf("first connection state = %q healthy=%v", first.State, first.Healthy)
	}
	refresh := findSCMOperationDiagnostic(t, first.Operations, SCMHealthOperationPRLinkRefresh)
	if refresh.ConsecutiveFailures != 2 || refresh.CheckedResources != 2 || refresh.FailedResources != 1 {
		t.Fatalf("refresh health = %+v", refresh)
	}
	if strings.Contains(refresh.LastError, "secret") || strings.Contains(refresh.LastError, "plaintext") || strings.Contains(refresh.LastError, "access_token") {
		t.Fatalf("last error was not sanitized: %q", refresh.LastError)
	}
	if refresh.LastFailureAt == nil || !refresh.LastFailureAt.Equal(firstAttempt.Add(15*time.Minute)) {
		t.Fatalf("last failure = %v", refresh.LastFailureAt)
	}

	second := findSCMConnectionDiagnostic(t, connections, secondConnection)
	if second.State != SCMHealthStateDisabled || second.Healthy {
		t.Fatalf("disabled connection state = %q healthy=%v", second.State, second.Healthy)
	}
	for _, operation := range second.Operations {
		if operation.State != SCMHealthStateNeverChecked {
			t.Fatalf("disabled operation %q state = %q", operation.Operation, operation.State)
		}
	}

	recoveredAt := firstAttempt.Add(30 * time.Minute)
	transition, err := repo.RecordResult(t.Context(), SCMHealthResult{
		ConnectionID:     firstConnection,
		Operation:        SCMHealthOperationPRLinkRefresh,
		AttemptedAt:      recoveredAt,
		CheckedResources: 2,
	})
	if err != nil {
		t.Fatalf("RecordResult() recovery: %v", err)
	}
	if !transition.Recovered {
		t.Fatalf("recovery transition = %+v", transition)
	}
	connections, err = repo.ListConnectionDiagnostics(t.Context())
	if err != nil {
		t.Fatalf("ListConnectionDiagnostics() after recovery: %v", err)
	}
	first = findSCMConnectionDiagnostic(t, connections, firstConnection)
	refresh = findSCMOperationDiagnostic(t, first.Operations, SCMHealthOperationPRLinkRefresh)
	if refresh.State != SCMHealthStateHealthy || refresh.ConsecutiveFailures != 0 || refresh.LastError != "" || refresh.LastSuccessAt == nil || !refresh.LastSuccessAt.Equal(recoveredAt) {
		t.Fatalf("recovered refresh health = %+v", refresh)
	}
}

func createSCMHealthConnection(t *testing.T, db database.Database, workspaceID int, suffix string, enabled bool) int {
	t.Helper()
	var providerID int
	if err := db.QueryRow(`
		INSERT INTO scm_providers (slug, name, provider_type, auth_method, enabled)
		VALUES (?, ?, 'github', 'pat', true)
		RETURNING id
	`, "health-"+suffix, "Health "+suffix).Scan(&providerID); err != nil {
		t.Fatalf("create SCM provider: %v", err)
	}
	var connectionID int
	if err := db.QueryRow(`
		INSERT INTO workspace_scm_connections (workspace_id, scm_provider_id, enabled)
		VALUES (?, ?, ?)
		RETURNING id
	`, workspaceID, providerID, enabled).Scan(&connectionID); err != nil {
		t.Fatalf("create SCM connection: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspace_repositories (
			workspace_scm_connection_id, repository_external_id, repository_name,
			repository_url, default_branch, is_active
		) VALUES (?, ?, ?, ?, 'main', true)
	`, connectionID, "repo-"+suffix, "owner/"+suffix, "https://example.test/owner/"+suffix); err != nil {
		t.Fatalf("create workspace repository: %v", err)
	}
	return connectionID
}

func findSCMConnectionDiagnostic(t *testing.T, connections []SCMConnectionDiagnostic, connectionID int) SCMConnectionDiagnostic {
	t.Helper()
	for _, connection := range connections {
		if connection.ID == connectionID {
			return connection
		}
	}
	t.Fatalf("connection %d not found in %+v", connectionID, connections)
	return SCMConnectionDiagnostic{}
}

func findSCMOperationDiagnostic(t *testing.T, operations []SCMOperationDiagnostic, operation string) SCMOperationDiagnostic {
	t.Helper()
	for _, candidate := range operations {
		if candidate.Operation == operation {
			return candidate
		}
	}
	t.Fatalf("operation %q not found in %+v", operation, operations)
	return SCMOperationDiagnostic{}
}
