//go:build test

package scm

import (
	"context"
	"testing"

	"windshift/internal/models"
	"windshift/internal/sso"
	"windshift/internal/testutils"
)

// These tests pin the invariant that the repository sync scheduler includes
// OAuth connections. Credential resolution reads the workspace connection's
// OAuth token, so an OAuth connection is syncable exactly like a PAT
// connection; excluding it silently froze every repository on such a
// connection (no commit links, no PR links, last_synced_at never set).

// oauthSyncProvider is a Provider that returns empty results everywhere and
// records ListCommits calls so tests can assert whether a sync reached the
// repository.
type oauthSyncProvider struct {
	commitRequests []ListCommitsOptions
}

func (p *oauthSyncProvider) GetType() models.SCMProviderType        { return models.SCMProviderTypeGitLab }
func (p *oauthSyncProvider) TestConnection(_ context.Context) error { return nil }
func (p *oauthSyncProvider) ListRepositories(_ context.Context, _ ListRepositoriesOptions) ([]Repository, error) {
	return nil, nil
}
func (p *oauthSyncProvider) GetRepository(_ context.Context, _, _ string) (*Repository, error) {
	return &Repository{}, nil
}
func (p *oauthSyncProvider) ListPullRequests(_ context.Context, _, _ string, _ ListPROptions) ([]PullRequest, error) {
	return nil, nil
}
func (p *oauthSyncProvider) GetPullRequest(_ context.Context, _, _ string, _ int) (*PullRequest, error) {
	return &PullRequest{}, nil
}
func (p *oauthSyncProvider) ListPullRequestCommits(_ context.Context, _, _ string, _ int) ([]Commit, error) {
	return nil, nil
}
func (p *oauthSyncProvider) CreateBranch(_ context.Context, _, _, _, _ string) error { return nil }
func (p *oauthSyncProvider) CreatePullRequest(_ context.Context, _, _ string, _ CreatePROptions) (*PullRequest, error) {
	return &PullRequest{}, nil
}
func (p *oauthSyncProvider) GetCommit(_ context.Context, _, _, _ string) (*Commit, error) {
	return &Commit{}, nil
}
func (p *oauthSyncProvider) ListBranches(_ context.Context, _, _ string) ([]Branch, error) {
	return nil, nil
}
func (p *oauthSyncProvider) RegisterWebhook(_ context.Context, _, _ string, _ WebhookOptions) (*WebhookRegistration, error) {
	return &WebhookRegistration{}, nil
}
func (p *oauthSyncProvider) DeleteWebhook(_ context.Context, _, _, _ string) error { return nil }
func (p *oauthSyncProvider) ListCommits(_ context.Context, _, _ string, opts ListCommitsOptions) ([]Commit, error) {
	p.commitRequests = append(p.commitRequests, opts)
	if opts.Page > 1 {
		return nil, nil
	}
	return []Commit{{
		SHA:     "cafe123",
		Message: "feat: add chat surface (WI-71)",
		URL:     "https://gitlab.example.com/acme/repo/-/commit/cafe123",
		Author:  User{ID: "42", Username: "dev", Name: "Dev User"},
	}}, nil
}

var _ Provider = (*oauthSyncProvider)(nil)

type oauthSyncFixture struct {
	svc      *SyncService
	provider *oauthSyncProvider
}

// newOAuthSyncFixture seeds an OAuth provider connection on workspace WI with
// one active repo (acme/repo, default branch main) and item WI-71. When
// withToken is true the workspace connection carries an encrypted OAuth
// access token. withAgentOwnership additionally seeds an agent PR ownership
// for the repo.
func newOAuthSyncFixture(t *testing.T, withToken, withAgentOwnership bool) *oauthSyncFixture {
	t.Helper()

	const secret = "oauth-sync-test-secret-with-enough-length"
	enc := sso.NewSecretEncryption(secret)

	tdb := testutils.CreateTestDB(t, true)
	if tdb.Engine == "sqlite" {
		t.Cleanup(func() { _ = tdb.Close() })
	}

	var tokenEnc any
	if withToken {
		encrypted, err := enc.Encrypt("workspace-oauth-access-token")
		if err != nil {
			t.Fatalf("encrypt access token: %v", err)
		}
		tokenEnc = encrypted
	}

	stmts := []struct {
		query string
		args  []any
	}{
		{query: `INSERT INTO users(id, email, username, first_name, last_name) VALUES (?, ?, ?, ?, ?)`,
			args: []any{901, "oauth-sync-dev@example.com", "oauth-sync-dev", "Dev", "User"}},
		{query: `INSERT INTO workspaces(id, name, key) VALUES (?, ?, ?)`,
			args: []any{1, "Windshift", "WI"}},
		{query: `INSERT INTO scm_providers(id, slug, name, provider_type, auth_method, enabled, oauth_client_id, oauth_client_secret_encrypted)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			args: []any{1, "gitlab-oauth-test", "GitLab", string(models.SCMProviderTypeGitLab), string(models.SCMAuthMethodOAuth), true, "client-id", tokenEnc}},
		{query: `INSERT INTO workspace_scm_connections(id, workspace_id, scm_provider_id, enabled, oauth_access_token_encrypted)
			VALUES (?, ?, ?, ?, ?)`,
			args: []any{2, 1, 1, true, tokenEnc}},
		{query: `INSERT INTO workspace_repositories(id, workspace_scm_connection_id, repository_external_id, repository_name, repository_url, default_branch, is_active)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			args: []any{5, 2, "repo-5", "acme/repo", "https://gitlab.example.com/acme/repo", "main", true}},
		{query: `INSERT INTO items(id, workspace_id, workspace_item_number, title, frac_index) VALUES (?, ?, ?, ?, ?)`,
			args: []any{10, 1, 71, "Expose labels", testutils.NextTestFracIndex()}},
	}
	for _, stmt := range stmts {
		if _, err := tdb.Exec(stmt.query, stmt.args...); err != nil {
			t.Fatalf("seed oauth sync fixture: %v", err)
		}
	}

	if withAgentOwnership {
		if _, err := tdb.Exec(`
			INSERT INTO agent_pr_ownerships(workspace_repository_id, pr_number, item_id, agent_run_id, binding_id, triggered_by_user_id, head_repo, head_branch)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, 5, 3, 10, 1, 1, 901, "acme/repo", "feature/wi-71"); err != nil {
			t.Fatalf("seed agent pr ownership: %v", err)
		}
	}

	provider := &oauthSyncProvider{}
	svc := NewSyncService(tdb.DB, enc)
	svc.resolveProviderOverride = func(_ context.Context, _ int) (Provider, error) {
		return provider, nil
	}
	return &oauthSyncFixture{svc: svc, provider: provider}
}

func TestSyncAllRepositories_SyncsOAuthConnectionWithWorkspaceToken(t *testing.T) {
	fx := newOAuthSyncFixture(t, true, false)

	if err := fx.svc.SyncAllRepositories(context.Background()); err != nil {
		t.Fatalf("SyncAllRepositories: %v", err)
	}

	if len(fx.provider.commitRequests) == 0 {
		t.Fatal("sync never listed commits: OAuth connection excluded from scheduler")
	}
	for _, req := range fx.provider.commitRequests {
		if req.Sha != "main" {
			t.Fatalf("ListCommits sha = %q, want main", req.Sha)
		}
	}

	var linkCount int
	if err := fx.svc.db.QueryRow(`SELECT COUNT(*) FROM item_scm_links WHERE item_id = 10 AND link_type = 'commit' AND external_id = 'cafe123'`).Scan(&linkCount); err != nil {
		t.Fatalf("query commit link: %v", err)
	}
	if linkCount != 1 {
		t.Fatalf("commit links = %d, want 1", linkCount)
	}

	var lastSynced any
	if err := fx.svc.db.QueryRow(`SELECT last_synced_at FROM workspace_repositories WHERE id = 5`).Scan(&lastSynced); err != nil {
		t.Fatalf("query repo: %v", err)
	}
	if lastSynced == nil {
		t.Fatal("last_synced_at not set: repository was not synced")
	}
}

func TestSyncAllRepositories_OAuthConnectionWithoutWorkspaceTokenRecordsHealth(t *testing.T) {
	fx := newOAuthSyncFixture(t, false, false)

	// Real resolver path: the connection has no workspace token, so provider
	// resolution must fail and be reported, not panic or silently sync.
	fx.svc.resolveProviderOverride = nil

	if err := fx.svc.SyncAllRepositories(context.Background()); err != nil {
		t.Fatalf("SyncAllRepositories: %v", err)
	}

	var linkCount int
	if err := fx.svc.db.QueryRow(`SELECT COUNT(*) FROM item_scm_links WHERE item_id = 10`).Scan(&linkCount); err != nil {
		t.Fatalf("query links: %v", err)
	}
	if linkCount != 0 {
		t.Fatalf("links = %d, want 0 for a connection that cannot resolve credentials", linkCount)
	}

	var lastError *string
	if err := fx.svc.db.QueryRow(`
		SELECT last_error FROM scm_connection_health
		WHERE workspace_scm_connection_id = 2 AND operation = 'repository_sync'
	`).Scan(&lastError); err != nil {
		t.Fatalf("query connection health: %v", err)
	}
	if lastError == nil {
		t.Fatal("expected health record with last_error for unresolvable OAuth connection")
	}
}

func TestSyncAllRepositories_AgentOwnedOAuthRepoSyncedOnceWhenWorkspaceTokenExists(t *testing.T) {
	fx := newOAuthSyncFixture(t, true, true)

	if err := fx.svc.SyncAllRepositories(context.Background()); err != nil {
		t.Fatalf("SyncAllRepositories: %v", err)
	}

	// The main loop covers the repo via the workspace token; the agent
	// ownership path must not sync it a second time.
	if got := len(fx.provider.commitRequests); got != 1 {
		t.Fatalf("ListCommits called %d times across a full sync tick, want 1", got)
	}
}
