package repository

import (
	"context"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
)

func intPtr(v int) *int { return &v }

// seedSCMConnection inserts an scm_providers + workspace_scm_connections row so
// the binding-repos FK (scm_connection_id → workspace_scm_connections) is
// satisfiable under PRAGMA foreign_keys=ON.
func seedSCMConnection(t *testing.T, db database.Database, workspaceID int) int {
	t.Helper()
	pres, err := db.Exec(
		`INSERT INTO scm_providers(slug, name, provider_type, auth_method, enabled) VALUES ('gh', 'GitHub', 'github', 'pat', TRUE)`)
	if err != nil {
		t.Fatalf("seed scm provider: %v", err)
	}
	providerID, _ := pres.LastInsertId()
	cres, err := db.Exec(
		`INSERT INTO workspace_scm_connections(workspace_id, scm_provider_id, enabled) VALUES (?, ?, TRUE)`,
		workspaceID, providerID)
	if err != nil {
		t.Fatalf("seed scm connection: %v", err)
	}
	id, _ := cres.LastInsertId()
	return int(id)
}

func TestReplaceBindingRepos_RoundTripAndHydrate(t *testing.T) {
	ctx := context.Background()
	db, admin, agentA, _ := openBindingTestDB(t)
	repo := NewWorkspaceAgentBindingRepository(db)
	conn := seedSCMConnection(t, db, 1)

	id, err := repo.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID: 1, ActingUserID: agentA, ActingUserKind: "agent", CreatedByUserID: admin,
	})
	if err != nil {
		t.Fatalf("insert binding: %v", err)
	}

	repos := []models.BindingRepo{
		{SCMConnectionID: intPtr(conn), RepoSlug: "acme/core", RepoBaseRef: "main", IsPrimary: true, Position: 0},
		{SCMConnectionID: intPtr(conn), RepoSlug: "acme/core-tests", RepoBaseRef: "develop", IsPrimary: false, Position: 1},
	}
	if err := repo.ReplaceBindingRepos(ctx, id, repos); err != nil {
		t.Fatalf("replace repos: %v", err)
	}

	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Repos) != 2 {
		t.Fatalf("repos count: want 2, got %d", len(got.Repos))
	}
	if !got.IsMultiRepo() {
		t.Errorf("IsMultiRepo: want true")
	}
	p := got.PrimaryRepo()
	if p == nil || p.RepoSlug != "acme/core" {
		t.Fatalf("primary: want acme/core, got %+v", p)
	}
	// Primary must mirror onto the deprecated scalar fields.
	if got.RepoSlug != "acme/core" || got.RepoBaseRef != "main" {
		t.Errorf("scalar mirror: want acme/core@main, got %q@%q", got.RepoSlug, got.RepoBaseRef)
	}
	if !got.HasRepoSlug("acme/core-tests") {
		t.Errorf("HasRepoSlug(core-tests): want true")
	}
	if got.HasRepoSlug("acme/other") {
		t.Errorf("HasRepoSlug(other): want false")
	}

	// Replace is destructive: a second replace with one repo leaves exactly one.
	if err := repo.ReplaceBindingRepos(ctx, id, repos[:1]); err != nil {
		t.Fatalf("second replace: %v", err)
	}
	again, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("get after second replace: %v", err)
	}
	if len(again.Repos) != 1 {
		t.Fatalf("repos count after replace: want 1, got %d", len(again.Repos))
	}
}

func TestReplaceBindingRepos_CascadeOnBindingDelete(t *testing.T) {
	ctx := context.Background()
	db, admin, agentA, _ := openBindingTestDB(t)
	repo := NewWorkspaceAgentBindingRepository(db)
	conn := seedSCMConnection(t, db, 1)

	id, err := repo.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID: 1, ActingUserID: agentA, ActingUserKind: "agent", CreatedByUserID: admin,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := repo.ReplaceBindingRepos(ctx, id, []models.BindingRepo{
		{SCMConnectionID: intPtr(conn), RepoSlug: "acme/core", IsPrimary: true},
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if _, err := repo.Delete(ctx, id, 1); err != nil {
		t.Fatalf("delete binding: %v", err)
	}
	rows, err := repo.ListReposForBinding(ctx, id)
	if err != nil {
		t.Fatalf("list repos after delete: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("cascade: want 0 repo rows after binding delete, got %d", len(rows))
	}
}

func TestReplaceBindingRepos_RejectsTwoPrimaries(t *testing.T) {
	ctx := context.Background()
	db, admin, agentA, _ := openBindingTestDB(t)
	repo := NewWorkspaceAgentBindingRepository(db)
	conn := seedSCMConnection(t, db, 1)

	id, err := repo.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID: 1, ActingUserID: agentA, ActingUserKind: "agent", CreatedByUserID: admin,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	// The partial unique index idx_wab_repos_one_primary must reject two
	// primaries on the same binding.
	err = repo.ReplaceBindingRepos(ctx, id, []models.BindingRepo{
		{SCMConnectionID: intPtr(conn), RepoSlug: "acme/core", IsPrimary: true},
		{SCMConnectionID: intPtr(conn), RepoSlug: "acme/core-tests", IsPrimary: true},
	})
	if err == nil {
		t.Fatal("expected error inserting two primary repos, got nil")
	}
}

func TestListForWorkspace_BatchHydratesRepos(t *testing.T) {
	ctx := context.Background()
	db, admin, agentA, agentB := openBindingTestDB(t)
	repo := NewWorkspaceAgentBindingRepository(db)
	conn := seedSCMConnection(t, db, 1)

	idA, _ := repo.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID: 1, ActingUserID: agentA, ActingUserKind: "agent", CreatedByUserID: admin,
	})
	idB, _ := repo.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID: 1, ActingUserID: agentB, ActingUserKind: "agent", CreatedByUserID: admin,
	})
	if err := repo.ReplaceBindingRepos(ctx, idA, []models.BindingRepo{
		{SCMConnectionID: intPtr(conn), RepoSlug: "acme/a1", IsPrimary: true},
		{SCMConnectionID: intPtr(conn), RepoSlug: "acme/a2"},
	}); err != nil {
		t.Fatalf("replace A: %v", err)
	}
	if err := repo.ReplaceBindingRepos(ctx, idB, []models.BindingRepo{
		{SCMConnectionID: intPtr(conn), RepoSlug: "acme/b1", IsPrimary: true},
	}); err != nil {
		t.Fatalf("replace B: %v", err)
	}

	list, err := repo.ListForWorkspace(ctx, 1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byID := map[int]*models.WorkspaceAgentBinding{}
	for _, b := range list {
		byID[b.ID] = b
	}
	if got := len(byID[idA].Repos); got != 2 {
		t.Errorf("binding A repos: want 2, got %d", got)
	}
	if got := len(byID[idB].Repos); got != 1 {
		t.Errorf("binding B repos: want 1, got %d", got)
	}
}
