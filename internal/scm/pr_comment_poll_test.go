//go:build test

package scm

import (
	"context"
	"testing"

	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

// prCommentProvider is an IssueProvider whose ListIssueComments returns a fixed
// set of comments. It embeds syncCommitProvider for the base Provider surface
// (all of which panic if unexpectedly called); only the issue-comment path is
// real.
type prCommentProvider struct {
	*syncCommitProvider
	comments  []IssueComment
	listCalls int
}

func (p *prCommentProvider) ListIssueComments(_ context.Context, _, _ string, _ int) ([]IssueComment, error) {
	p.listCalls++
	return p.comments, nil
}
func (p *prCommentProvider) ListIssues(_ context.Context, _, _ string, _ ListIssueOptions) ([]Issue, error) {
	panic("ListIssues not implemented for prCommentProvider")
}
func (p *prCommentProvider) GetIssue(_ context.Context, _, _ string, _ int) (*Issue, error) {
	panic("GetIssue not implemented for prCommentProvider")
}
func (p *prCommentProvider) UpdateIssue(_ context.Context, _, _ string, _ int, _ UpdateIssueOptions) (*Issue, error) {
	panic("UpdateIssue not implemented for prCommentProvider")
}
func (p *prCommentProvider) CreateIssueComment(_ context.Context, _, _ string, _ int, _ string) (int64, error) {
	panic("CreateIssueComment not implemented for prCommentProvider")
}
func (p *prCommentProvider) UpdateIssueComment(_ context.Context, _, _ string, _ int64, _ string) error {
	panic("UpdateIssueComment not implemented for prCommentProvider")
}
func (p *prCommentProvider) ListRepoLabels(_ context.Context, _, _ string) ([]IssueLabel, error) {
	panic("ListRepoLabels not implemented for prCommentProvider")
}
func (p *prCommentProvider) ListRepoMilestones(_ context.Context, _, _ string) ([]IssueMilestone, error) {
	panic("ListRepoMilestones not implemented for prCommentProvider")
}

var _ IssueProvider = (*prCommentProvider)(nil)

// recordingStarter records every continuation request and reports it as started.
type recordingStarter struct {
	calls []services.PRCommentContinuation
}

func (r *recordingStarter) StartPRCommentContinuation(_ context.Context, in services.PRCommentContinuation) (bool, error) {
	r.calls = append(r.calls, in)
	return true, nil
}

// newPollTestService builds a SyncService over a test DB seeded with one
// workspace/connection/repo (id 5) + item (id 10), with the recording starter
// wired. Returns the service, the starter, and the repo id.
func newPollTestService(t *testing.T) (*SyncService, *recordingStarter) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	if tdb.Engine == "sqlite" {
		t.Cleanup(func() { _ = tdb.Close() })
	}
	stmts := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO workspaces(id, name, key) VALUES (?, ?, ?)`, []any{1, "Windshift", "WI"}},
		{`INSERT INTO scm_providers(id, slug, name, provider_type, auth_method, enabled) VALUES (?, ?, ?, ?, ?, ?)`,
			[]any{1, "poll-test", "GitHub", string(models.SCMProviderTypeGitHub), string(models.SCMAuthMethodPAT), true}},
		{`INSERT INTO workspace_scm_connections(id, workspace_id, scm_provider_id, enabled) VALUES (?, ?, ?, ?)`,
			[]any{2, 1, 1, true}},
		{`INSERT INTO workspace_repositories(id, workspace_scm_connection_id, repository_external_id, repository_name, repository_url, default_branch, is_active) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			[]any{5, 2, "repo-5", "acme/repo", "https://github.com/acme/repo", "main", true}},
		{`INSERT INTO items(id, workspace_id, workspace_item_number, title, frac_index) VALUES (?, ?, ?, ?, ?)`,
			[]any{10, 1, 71, "Continue me", testutils.NextTestFracIndex()}},
	}
	for _, s := range stmts {
		if _, err := tdb.Exec(s.query, s.args...); err != nil {
			t.Fatalf("seed poll fixture: %v", err)
		}
	}
	svc := NewSyncService(tdb.DB, nil)
	starter := &recordingStarter{}
	svc.SetContinuationStarter(starter)
	return svc, starter
}

func openPR() PullRequest {
	return PullRequest{Number: 7, State: "open", HeadBranch: "agent-runs/run-7", URL: "https://github.com/acme/repo/pull/7"}
}

func poll(t *testing.T, svc *SyncService, p *prCommentProvider) {
	t.Helper()
	svc.pollPRCommentTriggers(context.Background(), p, "acme", "repo", openPR(), 5, 1, []int{10})
}

// TestPoll_FirstSightBaselinesWithoutFiring: the first time a PR is polled, the
// cursor is set to the newest comment id and NO continuation fires, so a backlog
// of old @agent comments is never replayed.
func TestPoll_FirstSightBaselinesWithoutFiring(t *testing.T) {
	svc, starter := newPollTestService(t)
	p := &prCommentProvider{syncCommitProvider: &syncCommitProvider{}, comments: []IssueComment{
		{ID: 100, Body: "looks good"},
		{ID: 101, Body: "@agent please tweak the error handling"},
	}}
	poll(t, svc, p)
	if len(starter.calls) != 0 {
		t.Fatalf("first sight must not fire, got %d continuation(s)", len(starter.calls))
	}
}

// TestPoll_NewTokenCommentFiresOnce: after the baseline, a newer @agent comment
// fires exactly one continuation carrying the PR head branch + comment id.
func TestPoll_NewTokenCommentFiresOnce(t *testing.T) {
	svc, starter := newPollTestService(t)
	base := []IssueComment{{ID: 101, Body: "@agent old request (already there at baseline)"}}
	p := &prCommentProvider{syncCommitProvider: &syncCommitProvider{}, comments: base}
	poll(t, svc, p) // baseline at 101

	p.comments = append(base, IssueComment{ID: 102, Body: "@agent now address the review"})
	poll(t, svc, p)

	if len(starter.calls) != 1 {
		t.Fatalf("want exactly 1 continuation, got %d", len(starter.calls))
	}
	got := starter.calls[0]
	if got.PRNumber != 7 || got.HeadBranch != "agent-runs/run-7" || got.CommentID != 102 || got.ItemID != 10 || got.RepoSlug != "acme/repo" {
		t.Fatalf("unexpected continuation: %+v", got)
	}

	// Idempotency: re-polling the same comments fires nothing new.
	poll(t, svc, p)
	if len(starter.calls) != 1 {
		t.Fatalf("re-poll must not re-fire; got %d total", len(starter.calls))
	}
}

// TestPoll_SkipsAgentMarkedComment: a comment that contains the trigger token
// AND the hidden agent marker (i.e. the agent's own comment) never fires — the
// loop guard that stops the agent re-triggering itself.
func TestPoll_SkipsAgentMarkedComment(t *testing.T) {
	svc, starter := newPollTestService(t)
	base := []IssueComment{{ID: 101, Body: "kickoff"}}
	p := &prCommentProvider{syncCommitProvider: &syncCommitProvider{}, comments: base}
	poll(t, svc, p) // baseline at 101

	// The agent's own progress comment echoes "@agent" but carries the marker.
	p.comments = append(base, IssueComment{ID: 102, Body: models.AgentCommentMarker + "\nPushed updates. @agent done."})
	poll(t, svc, p)

	if len(starter.calls) != 0 {
		t.Fatalf("agent-marked comment must not fire, got %d", len(starter.calls))
	}
}

// TestPoll_IgnoresCommentWithoutToken: a normal human comment with no trigger
// token does not start a continuation.
func TestPoll_IgnoresCommentWithoutToken(t *testing.T) {
	svc, starter := newPollTestService(t)
	base := []IssueComment{{ID: 101, Body: "kickoff"}}
	p := &prCommentProvider{syncCommitProvider: &syncCommitProvider{}, comments: base}
	poll(t, svc, p)

	p.comments = append(base, IssueComment{ID: 102, Body: "nice work, shipping this"})
	poll(t, svc, p)

	if len(starter.calls) != 0 {
		t.Fatalf("non-token comment must not fire, got %d", len(starter.calls))
	}
}
