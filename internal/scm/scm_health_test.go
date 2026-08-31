//go:build test

package scm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

func TestRefreshAllPRLinkStatesPersistsFailureAndRecovery(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	itemID, err := services.CreateItem(db, services.ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "SCM health item",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	repoID := createSCMTriggerRepository(t, db, data.WorkspaceID, nil)
	var connectionID int
	if err := db.QueryRow("SELECT workspace_scm_connection_id FROM workspace_repositories WHERE id = ?", repoID).Scan(&connectionID); err != nil {
		t.Fatalf("load connection: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO item_scm_links (
			item_id, workspace_repository_id, link_type, external_id,
			external_url, title, state
		) VALUES (?, ?, 'pull_request', '7', 'https://example.test/owner/repo/pull/7', 'PR 7', 'open')
	`, itemID, repoID); err != nil {
		t.Fatalf("create PR link: %v", err)
	}

	provider := &fakeProvider{pullRequestErr: errors.New("GET https://user:secret@example.test/pull/7?access_token=plaintext: resource not found")}
	service := NewSyncService(db, nil)
	service.resolveProviderOverride = func(_ context.Context, gotConnectionID int) (Provider, error) {
		if gotConnectionID != connectionID {
			t.Fatalf("resolved connection = %d, want %d", gotConnectionID, connectionID)
		}
		return provider, nil
	}

	for range 2 {
		if err := service.RefreshAllPRLinkStates(t.Context()); err != nil {
			t.Fatalf("RefreshAllPRLinkStates() failure pass: %v", err)
		}
	}
	healthRepo := repository.NewSCMHealthRepository(db)
	connections, err := healthRepo.ListConnectionDiagnostics(t.Context())
	if err != nil {
		t.Fatalf("list connection health: %v", err)
	}
	diagnostic := findConnectionHealth(t, connections, connectionID, repository.SCMHealthOperationPRLinkRefresh)
	if diagnostic.State != repository.SCMHealthStateUnhealthy || diagnostic.ConsecutiveFailures != 2 {
		t.Fatalf("failure health = %+v", diagnostic)
	}
	if strings.Contains(diagnostic.LastError, "secret") || strings.Contains(diagnostic.LastError, "plaintext") {
		t.Fatalf("persisted error leaked credentials: %q", diagnostic.LastError)
	}
	if !strings.Contains(diagnostic.LastError, "owner/repo") || !strings.Contains(diagnostic.LastError, "PR #7") || strings.Contains(diagnostic.LastError, "link 1") {
		t.Fatalf("persisted error lacks actionable PR identity: %q", diagnostic.LastError)
	}

	provider.pullRequestErr = nil
	provider.pullRequest = &PullRequest{
		Number: 7, Title: "PR 7 recovered", State: "open",
		URL: "https://example.test/owner/repo/pull/7", Author: User{ID: "7", Name: "SCM User"},
	}
	if err := service.RefreshAllPRLinkStates(t.Context()); err != nil {
		t.Fatalf("RefreshAllPRLinkStates() recovery pass: %v", err)
	}
	connections, err = healthRepo.ListConnectionDiagnostics(t.Context())
	if err != nil {
		t.Fatalf("list recovered connection health: %v", err)
	}
	diagnostic = findConnectionHealth(t, connections, connectionID, repository.SCMHealthOperationPRLinkRefresh)
	if diagnostic.State != repository.SCMHealthStateHealthy || diagnostic.ConsecutiveFailures != 0 || diagnostic.LastError != "" || diagnostic.LastSuccessAt == nil {
		t.Fatalf("recovered health = %+v", diagnostic)
	}
}

func TestSyncAllRepositoriesClearsStaleHealthWhenNoActiveRepositoriesRemain(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	repoID := createSCMTriggerRepository(t, db, data.WorkspaceID, nil)
	var connectionID int
	if err := db.QueryRow("SELECT workspace_scm_connection_id FROM workspace_repositories WHERE id = ?", repoID).Scan(&connectionID); err != nil {
		t.Fatalf("load connection: %v", err)
	}
	if _, err := db.Exec("UPDATE workspace_repositories SET is_active = false WHERE id = ?", repoID); err != nil {
		t.Fatalf("deactivate repository: %v", err)
	}
	healthRepo := repository.NewSCMHealthRepository(db)
	if _, err := healthRepo.RecordResult(t.Context(), repository.SCMHealthResult{
		ConnectionID:     connectionID,
		Operation:        repository.SCMHealthOperationRepositorySync,
		CheckedResources: 1,
		FailedResources:  1,
		LastError:        "repository unavailable",
	}); err != nil {
		t.Fatalf("seed stale health: %v", err)
	}

	service := NewSyncService(db, nil)
	service.resolveProviderOverride = func(context.Context, int) (Provider, error) {
		t.Fatal("provider should not be resolved with no active repositories")
		return nil, nil
	}
	if err := service.SyncAllRepositories(t.Context()); err != nil {
		t.Fatalf("SyncAllRepositories(): %v", err)
	}
	connections, err := healthRepo.ListConnectionDiagnostics(t.Context())
	if err != nil {
		t.Fatalf("list connection health: %v", err)
	}
	diagnostic := findConnectionHealth(t, connections, connectionID, repository.SCMHealthOperationRepositorySync)
	if diagnostic.State != repository.SCMHealthStateHealthy || diagnostic.CheckedResources != 0 || diagnostic.ConsecutiveFailures != 0 {
		t.Fatalf("zero-repository health = %+v", diagnostic)
	}
}

func findConnectionHealth(t *testing.T, connections []repository.SCMConnectionDiagnostic, connectionID int, operation string) repository.SCMOperationDiagnostic {
	t.Helper()
	for _, connection := range connections {
		if connection.ID != connectionID {
			continue
		}
		for _, candidate := range connection.Operations {
			if candidate.Operation == operation {
				return candidate
			}
		}
	}
	t.Fatalf("connection %d operation %q not found", connectionID, operation)
	return repository.SCMOperationDiagnostic{}
}
