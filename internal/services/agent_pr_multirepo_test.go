package services

import (
	"context"
	"sync"
	"testing"

	"windshift/internal/models"
)

// TestAgentPRService_OpensPRPerChangedRepo pins WI-449: a multi-repo run opens
// one PR per repo that has a branch, links only the PRIMARY repo's PR to the
// work item, and skips repos with no new commits (empty branch).
func TestAgentPRService_OpensPRPerChangedRepo(t *testing.T) {
	svc, bindingsRepo, db, bindingID, _, itemID, _, _ := newPRServiceTestStack(t)

	var connID int
	_ = db.QueryRow(`SELECT id FROM workspace_scm_connections LIMIT 1`).Scan(&connID)

	// Make the binding multi-repo: acme/widget (primary, already linked to a
	// workspace_repository in the stack) + acme/extra (secondary) + acme/quiet
	// (no changes this run).
	if err := bindingsRepo.ReplaceBindingRepos(context.Background(), bindingID, []models.BindingRepo{
		{SCMConnectionID: &connID, RepoSlug: "acme/widget", RepoBaseRef: "main", IsPrimary: true, Position: 0},
		{SCMConnectionID: &connID, RepoSlug: "acme/extra", RepoBaseRef: "develop", Position: 1},
		{SCMConnectionID: &connID, RepoSlug: "acme/quiet", RepoBaseRef: "main", Position: 2},
	}); err != nil {
		t.Fatalf("replace repos: %v", err)
	}

	var (
		mu   sync.Mutex
		reqs []OpenPRRequest
	)
	svc.openPR = func(_ context.Context, req OpenPRRequest) (*OpenedPR, error) {
		mu.Lock()
		reqs = append(reqs, req)
		n := len(reqs)
		mu.Unlock()
		return &OpenedPR{ID: "10" + string(rune('0'+n)), Number: 100 + n, URL: "https://gitea.example.com/" + req.Owner + "/" + req.Repo + "/pulls/1", Title: req.Title, State: "Open", Author: "agent"}, nil
	}

	itemIDPtr := itemID
	svc.AfterRun(context.Background(), PostRunInfo{
		RunID:             9,
		WorkspaceID:       1,
		ItemID:            &itemIDPtr,
		BindingID:         bindingID,
		Status:            models.AgentRunStatusSucceeded,
		Branch:            "agent-runs/run-9", // legacy mirror of primary
		BaseCommit:        "p000",
		TriggeredByUserID: 77,
		Repos: []PostRunRepo{
			{RepoSlug: "acme/widget", Branch: "agent-runs/run-9", BaseCommit: "p000"},
			{RepoSlug: "acme/extra", Branch: "agent-runs/run-9", BaseCommit: "e000"},
			{RepoSlug: "acme/quiet", Branch: "", BaseCommit: ""}, // no_changes → no PR
		},
	})

	mu.Lock()
	defer mu.Unlock()
	if len(reqs) != 2 {
		t.Fatalf("openPR calls: want 2 (widget + extra; quiet skipped), got %d", len(reqs))
	}
	seen := map[string]string{} // repo -> base branch
	for _, r := range reqs {
		seen[r.Repo] = r.BaseBranch
	}
	if _, ok := seen["widget"]; !ok {
		t.Errorf("expected a PR for widget")
	}
	if base, ok := seen["extra"]; !ok || base != "develop" {
		t.Errorf("expected a PR for extra on base develop, got base=%q ok=%v", base, ok)
	}
	if _, ok := seen["quiet"]; ok {
		t.Errorf("quiet had no changes; no PR should be opened")
	}

	// Only the primary repo (acme/widget) links to the work item.
	var links int
	if err := db.QueryRow(`SELECT COUNT(*) FROM item_scm_links WHERE link_type='pull_request' AND item_id=?`, itemID).Scan(&links); err != nil {
		t.Fatalf("count links: %v", err)
	}
	if links != 1 {
		t.Errorf("item links: want 1 (primary only), got %d", links)
	}
}
