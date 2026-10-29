//go:build test

package server

import (
	"testing"

	"windshift/internal/scm"
	"windshift/internal/testutils"
)

func TestIssueSyncSchedulerRunsOnlyWhileSyncIsEnabled(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	var providerID int
	if err := db.QueryRow(`
		INSERT INTO scm_providers (slug, name, provider_type, auth_method, enabled)
		VALUES ('issue-sync-scheduler', 'Issue Sync Scheduler', 'github', 'pat', true)
		RETURNING id
	`).Scan(&providerID); err != nil {
		t.Fatalf("create SCM provider: %v", err)
	}
	var connectionID int
	if err := db.QueryRow(`
		INSERT INTO workspace_scm_connections (workspace_id, scm_provider_id, enabled)
		VALUES (?, ?, true) RETURNING id
	`, data.WorkspaceID, providerID).Scan(&connectionID); err != nil {
		t.Fatalf("create SCM connection: %v", err)
	}
	var repositoryID int
	if err := db.QueryRow(`
		INSERT INTO workspace_repositories (
			workspace_scm_connection_id, repository_external_id, repository_name, repository_url
		) VALUES (?, 'scheduler', 'windshift/scheduler', 'https://example.com/windshift/scheduler') RETURNING id
	`, connectionID).Scan(&repositoryID); err != nil {
		t.Fatalf("create repository: %v", err)
	}
	var configID int
	if err := db.QueryRow(`
		INSERT INTO issue_sync_configs (workspace_repository_id, sync_enabled)
		VALUES (?, false) RETURNING id
	`, repositoryID).Scan(&configID); err != nil {
		t.Fatalf("create issue sync config: %v", err)
	}

	server := &Server{
		issueSyncStopChan: make(chan struct{}),
		cleanupStopChan:   make(chan struct{}),
		magicLinkStopChan: make(chan struct{}),
		scmSyncStopChan:   make(chan struct{}),
		jiraHostStopChan:  make(chan struct{}),
	}
	defer server.stopBackgroundLoops()
	issueSync := scm.NewIssueSyncService(db, nil)

	server.refreshIssueSyncLoop(issueSync)
	if server.issueSyncLoopStop != nil {
		t.Fatal("scheduler started while issue sync was disabled")
	}
	if _, err := db.ExecWrite(`UPDATE issue_sync_configs SET sync_enabled = true WHERE id = ?`, configID); err != nil {
		t.Fatalf("enable issue sync: %v", err)
	}
	server.refreshIssueSyncLoop(issueSync)
	if server.issueSyncLoopStop == nil {
		t.Fatal("scheduler did not start after issue sync was enabled")
	}
	if _, err := db.ExecWrite(`UPDATE issue_sync_configs SET sync_enabled = false WHERE id = ?`, configID); err != nil {
		t.Fatalf("disable issue sync: %v", err)
	}
	server.refreshIssueSyncLoop(issueSync)
	if server.issueSyncLoopStop != nil {
		t.Fatal("scheduler remained active after issue sync was disabled")
	}
}
