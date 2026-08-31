package scm

import (
	"context"
	"path/filepath"
	"testing"

	"windshift/internal/database"
)

func TestMilestoneCommitLedgerIsIndependentAndObjectScoped(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "milestone-ledger.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	insertID := func(query string, args ...interface{}) int {
		t.Helper()
		result, err := db.ExecWrite(query, args...)
		if err != nil {
			t.Fatalf("fixture insert: %v", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("LastInsertId: %v", err)
		}
		return int(id)
	}

	workspaceID := insertID(`INSERT INTO workspaces (name, key, description, active, is_personal) VALUES ('SCM ledger', 'SCL', '', true, false)`)
	providerID := insertID(`
		INSERT INTO scm_providers (slug, name, provider_type, auth_method, enabled)
		VALUES ('ledger-provider', 'Ledger provider', 'gitea', 'pat', true)
	`)
	connectionID := insertID(`
		INSERT INTO workspace_scm_connections (workspace_id, scm_provider_id, enabled)
		VALUES (?, ?, true)
	`, workspaceID, providerID)
	repositoryID := insertID(`
		INSERT INTO workspace_repositories (
			workspace_scm_connection_id, repository_external_id, repository_name, repository_url
		) VALUES (?, 'ledger-repo', 'windshift/ledger', 'https://example.test/windshift/ledger')
	`, connectionID)
	milestoneOne := insertID(`
		INSERT INTO milestones (name, description, status, is_global, workspace_id)
		VALUES ('Release one', '', 'planning', true, NULL)
	`)
	milestoneTwo := insertID(`
		INSERT INTO milestones (name, description, status, is_global, workspace_id)
		VALUES ('Release two', '', 'planning', true, NULL)
	`)

	const sha = "0123456789abcdef"
	if _, err := db.ExecWrite(`
		INSERT INTO scm_processed_commits (commit_sha, workspace_repository_id, actions_applied)
		VALUES (?, ?, 1)
	`, sha, repositoryID); err != nil {
		t.Fatalf("seed smart-commit ledger: %v", err)
	}

	attacher := &MilestoneAttacher{sync: &SyncService{db: db}}
	ctx := context.Background()
	processed, err := attacher.commitAlreadyProcessed(ctx, milestoneOne, repositoryID, sha)
	if err != nil {
		t.Fatalf("check milestone ledger: %v", err)
	}
	if processed {
		t.Fatal("smart-commit ledger entry consumed milestone attachment")
	}

	if err := attacher.markCommitProcessed(ctx, milestoneOne, repositoryID, sha); err != nil {
		t.Fatalf("mark first milestone: %v", err)
	}
	processed, err = attacher.commitAlreadyProcessed(ctx, milestoneOne, repositoryID, sha)
	if err != nil || !processed {
		t.Fatalf("first milestone processed = %t, err = %v", processed, err)
	}
	processed, err = attacher.commitAlreadyProcessed(ctx, milestoneTwo, repositoryID, sha)
	if err != nil {
		t.Fatalf("check second milestone: %v", err)
	}
	if processed {
		t.Fatal("first milestone ledger entry consumed overlapping second milestone")
	}

	if err := attacher.markCommitProcessed(ctx, milestoneTwo, repositoryID, sha); err != nil {
		t.Fatalf("mark second milestone: %v", err)
	}
	if err := attacher.markCommitProcessed(ctx, milestoneTwo, repositoryID, sha); err != nil {
		t.Fatalf("duplicate milestone delivery was not idempotent: %v", err)
	}

	const milestoneFirstSHA = "fedcba9876543210"
	if err := attacher.markCommitProcessed(ctx, milestoneOne, repositoryID, milestoneFirstSHA); err != nil {
		t.Fatalf("mark milestone before smart commit: %v", err)
	}
	if attacher.sync.commitAlreadyProcessed(ctx, repositoryID, milestoneFirstSHA) {
		t.Fatal("milestone-first ledger entry consumed smart-commit processing")
	}
	attacher.sync.markCommitProcessed(ctx, repositoryID, milestoneFirstSHA, 1)
	if !attacher.sync.commitAlreadyProcessed(ctx, repositoryID, milestoneFirstSHA) {
		t.Fatal("smart commit was not recorded after milestone-first processing")
	}

	var smartCount, milestoneCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM scm_processed_commits WHERE workspace_repository_id = ?`, repositoryID).Scan(&smartCount); err != nil {
		t.Fatalf("count smart ledger: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM scm_milestone_processed_commits WHERE workspace_repository_id = ?`, repositoryID).Scan(&milestoneCount); err != nil {
		t.Fatalf("count milestone ledger: %v", err)
	}
	if smartCount != 2 || milestoneCount != 3 {
		t.Fatalf("ledger counts smart/milestone = %d/%d, want 2/3", smartCount, milestoneCount)
	}
}
