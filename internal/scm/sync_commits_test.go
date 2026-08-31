//go:build test

package scm

import (
	"context"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

type syncCommitProvider struct {
	commitPages    [][]Commit
	commitRequests []ListCommitsOptions
}

func (p *syncCommitProvider) GetType() models.SCMProviderType        { return models.SCMProviderTypeGitHub }
func (p *syncCommitProvider) TestConnection(_ context.Context) error { return nil }
func (p *syncCommitProvider) ListRepositories(_ context.Context, _ ListRepositoriesOptions) ([]Repository, error) {
	panic("ListRepositories not implemented for syncCommitProvider")
}
func (p *syncCommitProvider) GetRepository(_ context.Context, _, _ string) (*Repository, error) {
	panic("GetRepository not implemented for syncCommitProvider")
}
func (p *syncCommitProvider) ListPullRequests(_ context.Context, _, _ string, _ ListPROptions) ([]PullRequest, error) {
	panic("ListPullRequests not implemented for syncCommitProvider")
}
func (p *syncCommitProvider) GetPullRequest(_ context.Context, _, _ string, _ int) (*PullRequest, error) {
	panic("GetPullRequest not implemented for syncCommitProvider")
}
func (p *syncCommitProvider) ListPullRequestCommits(_ context.Context, _, _ string, _ int) ([]Commit, error) {
	panic("ListPullRequestCommits not implemented for syncCommitProvider")
}
func (p *syncCommitProvider) CreateBranch(_ context.Context, _, _, _, _ string) error {
	panic("CreateBranch not implemented for syncCommitProvider")
}
func (p *syncCommitProvider) CreatePullRequest(_ context.Context, _, _ string, _ CreatePROptions) (*PullRequest, error) {
	panic("CreatePullRequest not implemented for syncCommitProvider")
}
func (p *syncCommitProvider) GetCommit(_ context.Context, _, _, _ string) (*Commit, error) {
	panic("GetCommit not implemented for syncCommitProvider")
}
func (p *syncCommitProvider) ListBranches(_ context.Context, _, _ string) ([]Branch, error) {
	panic("ListBranches not implemented for syncCommitProvider")
}
func (p *syncCommitProvider) RegisterWebhook(_ context.Context, _, _ string, _ WebhookOptions) (*WebhookRegistration, error) {
	panic("RegisterWebhook not implemented for syncCommitProvider")
}
func (p *syncCommitProvider) DeleteWebhook(_ context.Context, _, _, _ string) error {
	panic("DeleteWebhook not implemented for syncCommitProvider")
}
func (p *syncCommitProvider) ListCommits(_ context.Context, _, _ string, opts ListCommitsOptions) ([]Commit, error) {
	p.commitRequests = append(p.commitRequests, opts)
	idx := opts.Page - 1
	if idx < 0 || idx >= len(p.commitPages) {
		return nil, nil
	}
	return p.commitPages[idx], nil
}

var _ CommitProvider = (*syncCommitProvider)(nil)

func newSyncCommitTestService(t *testing.T) *SyncService {
	t.Helper()

	tdb := testutils.CreateTestDB(t, true)
	if tdb.Engine == "sqlite" {
		t.Cleanup(func() { _ = tdb.Close() })
	}

	stmts := []struct {
		query string
		args  []any
	}{
		{
			query: `INSERT INTO workspaces(id, name, key) VALUES (?, ?, ?)`,
			args:  []any{1, "Windshift", "WI"},
		},
		{
			query: `INSERT INTO scm_providers(id, slug, name, provider_type, auth_method, enabled) VALUES (?, ?, ?, ?, ?, ?)`,
			args:  []any{1, "sync-commit-test", "GitHub", string(models.SCMProviderTypeGitHub), string(models.SCMAuthMethodPAT), true},
		},
		{
			query: `INSERT INTO workspace_scm_connections(id, workspace_id, scm_provider_id, enabled) VALUES (?, ?, ?, ?)`,
			args:  []any{2, 1, 1, true},
		},
		{
			query: `INSERT INTO workspace_repositories(id, workspace_scm_connection_id, repository_external_id, repository_name, repository_url, default_branch, is_active) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			args:  []any{5, 2, "repo-5", "acme/repo", "https://github.com/acme/repo", "main", true},
		},
		{
			query: `INSERT INTO items(id, workspace_id, workspace_item_number, title, frac_index) VALUES (?, ?, ?, ?, ?)`,
			args:  []any{10, 1, 71, "Expose labels", testutils.NextTestFracIndex()},
		},
	}
	for _, stmt := range stmts {
		if _, err := tdb.Exec(stmt.query, stmt.args...); err != nil {
			t.Fatalf("seed sync commit fixture: %v", err)
		}
	}

	return NewSyncService(tdb.DB, nil)
}

func TestSyncCommits_CreatesCommitLinkFromMessage(t *testing.T) {
	svc := newSyncCommitTestService(t)
	provider := &syncCommitProvider{commitPages: [][]Commit{{{
		SHA:     "abc123",
		Message: "fix(api): expose labels (WI-71)\n\nDetails",
		URL:     "https://github.com/acme/repo/commit/abc123",
		Author:  User{ID: "42", Username: "dev", Name: "Dev User"},
	}}}}

	if err := svc.syncCommits(context.Background(), provider, "acme", "repo", 5, 1, "WI", "", "main", time.Time{}); err != nil {
		t.Fatalf("syncCommits: %v", err)
	}

	if got := len(provider.commitRequests); got != 1 {
		t.Fatalf("ListCommits called %d times, want 1", got)
	}
	if got := provider.commitRequests[0].Sha; got != "main" {
		t.Fatalf("ListCommits sha = %q, want main", got)
	}

	var linkType, externalID, title, source, author string
	if err := svc.db.QueryRow(`
		SELECT link_type, external_id, title, detection_source, author_name
		FROM item_scm_links WHERE item_id = 10
	`).Scan(&linkType, &externalID, &title, &source, &author); err != nil {
		t.Fatalf("query link: %v", err)
	}
	if linkType != string(models.SCMLinkTypeCommit) || externalID != "abc123" || title != "fix(api): expose labels (WI-71)" || source != string(DetectionSourceCommitMessage) || author != "Dev User" {
		t.Fatalf("unexpected link: type=%q external=%q title=%q source=%q author=%q", linkType, externalID, title, source, author)
	}
}

func TestSyncCommits_UsesCustomItemKeyPattern(t *testing.T) {
	svc := newSyncCommitTestService(t)
	provider := &syncCommitProvider{commitPages: [][]Commit{{{
		SHA:     "def456",
		Message: "fix(api): lower-case key (wi-71)",
		URL:     "https://github.com/acme/repo/commit/def456",
	}}}}

	pattern := `(?i)\b([a-z]{2})-(\d+)\b`
	if err := svc.syncCommits(context.Background(), provider, "acme", "repo", 5, 1, "WI", pattern, "main", time.Time{}); err != nil {
		t.Fatalf("syncCommits: %v", err)
	}

	var count int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM item_scm_links WHERE external_id = 'def456'`).Scan(&count); err != nil {
		t.Fatalf("query link count: %v", err)
	}
	if count != 1 {
		t.Fatalf("commit links = %d, want 1", count)
	}
}
