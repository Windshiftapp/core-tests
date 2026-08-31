package scm

import (
	"context"
	"fmt"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

type reviewEventProvider struct {
	comments    []IssueComment
	created     []string
	canWrite    bool
	nextComment int64
}

func (p *reviewEventProvider) GetType() models.SCMProviderType      { return models.SCMProviderTypeGitHub }
func (p *reviewEventProvider) TestConnection(context.Context) error { return nil }
func (p *reviewEventProvider) ListRepositories(context.Context, ListRepositoriesOptions) ([]Repository, error) {
	return nil, nil
}
func (p *reviewEventProvider) GetRepository(context.Context, string, string) (*Repository, error) {
	return nil, nil
}
func (p *reviewEventProvider) ListPullRequests(context.Context, string, string, ListPROptions) ([]PullRequest, error) {
	return nil, nil
}
func (p *reviewEventProvider) GetPullRequest(context.Context, string, string, int) (*PullRequest, error) {
	return nil, nil
}
func (p *reviewEventProvider) ListPullRequestCommits(context.Context, string, string, int) ([]Commit, error) {
	return nil, nil
}
func (p *reviewEventProvider) CreateBranch(context.Context, string, string, string, string) error {
	return nil
}
func (p *reviewEventProvider) CreatePullRequest(context.Context, string, string, CreatePROptions) (*PullRequest, error) {
	return nil, nil
}
func (p *reviewEventProvider) GetCommit(context.Context, string, string, string) (*Commit, error) {
	return nil, nil
}
func (p *reviewEventProvider) ListBranches(context.Context, string, string) ([]Branch, error) {
	return nil, nil
}
func (p *reviewEventProvider) RegisterWebhook(context.Context, string, string, WebhookOptions) (*WebhookRegistration, error) {
	return nil, nil
}
func (p *reviewEventProvider) DeleteWebhook(context.Context, string, string, string) error {
	return nil
}
func (p *reviewEventProvider) ListIssueComments(context.Context, string, string, int) ([]IssueComment, error) {
	return append([]IssueComment(nil), p.comments...), nil
}
func (p *reviewEventProvider) CreateIssueComment(_ context.Context, _, _ string, _ int, body string) (int64, error) {
	p.nextComment++
	p.created = append(p.created, body)
	p.comments = append(p.comments, IssueComment{ID: p.nextComment, Kind: "issue_comment", Body: body, CreatedAt: time.Now().UTC()})
	return p.nextComment, nil
}
func (p *reviewEventProvider) UpdateIssueComment(context.Context, string, string, int64, string) error {
	return nil
}
func (p *reviewEventProvider) CanUserWriteRepository(context.Context, string, string, string) (bool, error) {
	return p.canWrite, nil
}

type reviewEventStarter struct {
	calls   []services.PRCommentContinuation
	results []services.PRCommentStartResult
}

func (s *reviewEventStarter) StartPRCommentContinuation(ctx context.Context, in services.PRCommentContinuation) (bool, error) {
	result, err := s.StartPRCommentContinuationDetailed(ctx, in)
	return result.Started, err
}
func (s *reviewEventStarter) StartPRCommentContinuationDetailed(_ context.Context, in services.PRCommentContinuation) (services.PRCommentStartResult, error) {
	s.calls = append(s.calls, in)
	if len(s.results) == 0 {
		return services.PRCommentStartResult{Started: true, RunID: len(s.calls)}, nil
	}
	result := s.results[0]
	s.results = s.results[1:]
	return result, nil
}

func newReviewEventService(t *testing.T) (*SyncService, database.Database, *reviewEventStarter) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s/review-events.db?mode=memory&cache=shared", t.TempDir())
	db, err := database.NewSQLiteDBWithPoolSizes(dsn, 4, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO workspaces(id,name,key) VALUES (1,'Windshift','WI')`,
		`INSERT INTO scm_providers(id,slug,name,provider_type,auth_method,enabled) VALUES (1,'gh','GitHub','github','pat',TRUE)`,
		`INSERT INTO workspace_scm_connections(id,workspace_id,scm_provider_id,enabled) VALUES (2,1,1,TRUE)`,
		`INSERT INTO workspace_repositories(id,workspace_scm_connection_id,repository_external_id,repository_name,repository_url,default_branch,is_active) VALUES (5,2,'5','acme/repo','https://example.test/acme/repo','main',TRUE)`,
		`INSERT INTO items(id,workspace_id,workspace_item_number,title,frac_index) VALUES (10,1,10,'Review event','` + testutils.NextTestFracIndex() + `')`,
		`INSERT INTO agent_runs(id,workspace_id,item_id,status) VALUES (1,1,10,'queued')`,
		`INSERT INTO agent_runs(id,workspace_id,item_id,status) VALUES (42,1,10,'queued')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	service := NewSyncService(db, nil)
	starter := &reviewEventStarter{}
	service.SetContinuationStarter(starter)
	return service, db, starter
}

func TestPRReviewInbox_FirstSightKeepsRecentRequest(t *testing.T) {
	service, db, starter := newReviewEventService(t)
	now := time.Now().UTC()
	provider := &reviewEventProvider{nextComment: 100, comments: []IssueComment{
		{ID: 1, Kind: "issue_comment", Body: "@agent historical", User: User{ID: "1", Username: "maintainer"}, AuthorAssociation: "MEMBER", CreatedAt: now.Add(-time.Hour)},
		{ID: 2, Kind: "issue_comment", Body: "@agent live request", User: User{ID: "1", Username: "maintainer"}, AuthorAssociation: "MEMBER", CreatedAt: now.Add(-time.Minute)},
	}}
	service.pollPRCommentTriggers(context.Background(), provider, "acme", "repo", PullRequest{Number: 7, State: "open", HeadBranch: "agent-runs/run-1", HeadRepo: "acme/repo"}, 5, 1, []int{10})
	if len(starter.calls) != 1 || starter.calls[0].CommentID != 2 {
		t.Fatalf("calls=%+v", starter.calls)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_pr_review_events`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("event count=%d err=%v", count, err)
	}
	if len(provider.created) != 1 {
		t.Fatalf("ack comments=%d", len(provider.created))
	}
}

func TestPRReviewInbox_UnauthorizedCommentIsIgnored(t *testing.T) {
	service, db, starter := newReviewEventService(t)
	provider := &reviewEventProvider{canWrite: false, comments: []IssueComment{{
		ID: 2, Kind: "issue_comment", Body: "@agent run expensive work", User: User{ID: "99", Username: "outsider"}, CreatedAt: time.Now().UTC(),
	}}}
	service.pollPRCommentTriggers(context.Background(), provider, "acme", "repo", PullRequest{Number: 7, State: "open", HeadBranch: "branch"}, 5, 1, []int{10})
	if len(starter.calls) != 0 {
		t.Fatalf("unauthorized trigger started %d runs", len(starter.calls))
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM agent_pr_review_events`).Scan(&status); err != nil || status != "ignored" {
		t.Fatalf("status=%q err=%v", status, err)
	}
}

func TestPRReviewInbox_MultipleRequestsAreDurableWhenAgentBusy(t *testing.T) {
	service, db, starter := newReviewEventService(t)
	starter.results = []services.PRCommentStartResult{
		{Started: true, RunID: 42},
		{Reason: "The coding agent is already working; request remains queued."},
		{Reason: "The coding agent is already working; request remains queued."},
	}
	now := time.Now().UTC()
	provider := &reviewEventProvider{nextComment: 100, comments: []IssueComment{
		{ID: 2, Body: "@agent first", User: User{ID: "1", Username: "maintainer"}, AuthorAssociation: "MEMBER", CreatedAt: now},
		{ID: 3, Body: "@agent second", User: User{ID: "1", Username: "maintainer"}, AuthorAssociation: "MEMBER", CreatedAt: now.Add(time.Second)},
	}}
	service.pollPRCommentTriggers(context.Background(), provider, "acme", "repo", PullRequest{Number: 7, State: "open", HeadBranch: "branch"}, 5, 1, []int{10})
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_pr_review_events`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("event count=%d err=%v", count, err)
	}
	var pending int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_pr_review_events WHERE status='pending'`).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("pending=%d err=%v", pending, err)
	}
}

func TestPRReviewInbox_WebhookAndPollAdmissionDeduplicate(t *testing.T) {
	service, db, starter := newReviewEventService(t)
	provider := &reviewEventProvider{nextComment: 100}
	pr := PullRequest{Number: 7, State: "open", HeadBranch: "branch", HeadRepo: "acme/repo"}
	event := IssueComment{ID: 77, Kind: "review_comment", Body: "@agent fix this", User: User{ID: "1", Username: "maintainer"}, AuthorAssociation: "MEMBER", CreatedAt: time.Now().UTC()}
	for range 2 {
		if _, err := service.IngestPRReviewEvent(context.Background(), provider, "acme", "repo", pr, 5, 1, 10, event); err != nil {
			t.Fatal(err)
		}
	}
	if len(starter.calls) != 1 {
		t.Fatalf("duplicate delivery started %d runs", len(starter.calls))
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_pr_review_events`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("event count=%d err=%v", count, err)
	}
}
