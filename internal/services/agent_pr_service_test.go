package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
)

func newPRServiceTestStack(t *testing.T) (
	*AgentPRService,
	*repository.WorkspaceAgentBindingRepository,
	database.Database,
	int, // bindingID for a binding with SCM connection
	int, // workspaceRepositoryID
	int, // itemID
	*int32, // openPR call count
	func() OpenPRRequest, // last captured request
) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s/pr_service.db?mode=memory&cache=shared", t.TempDir())
	db, err := database.NewSQLiteDBWithPoolSizes(dsn, 4, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("init db: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces(id, name, key, active) VALUES (1, 'WS', 'WS', true)`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users(email, username, first_name, last_name, is_agent) VALUES ('admin@example.com','admin','A','',FALSE)`); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users(email, username, first_name, last_name, is_agent) VALUES ('agent@agents.local','agent','Ag','Ent',TRUE)`); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	var adminID, agentID int
	_ = db.QueryRow(`SELECT id FROM users WHERE username='admin'`).Scan(&adminID)
	_ = db.QueryRow(`SELECT id FROM users WHERE username='agent'`).Scan(&agentID)

	if _, err := db.Exec(`INSERT INTO scm_providers(slug, name, provider_type, auth_method, base_url) VALUES ('gitea1','Gitea','gitea','oauth','https://gitea.example.com')`); err != nil {
		t.Fatalf("seed scm provider: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO workspace_scm_connections(workspace_id, scm_provider_id) VALUES (1, 1)`); err != nil {
		t.Fatalf("seed scm connection: %v", err)
	}
	var connID int
	_ = db.QueryRow(`SELECT id FROM workspace_scm_connections LIMIT 1`).Scan(&connID)

	if _, err := db.Exec(
		`INSERT INTO workspace_repositories(workspace_scm_connection_id, repository_external_id, repository_name, repository_url) VALUES (?, ?, ?, ?)`,
		connID, "rep-1", "acme/widget", "https://gitea.example.com/acme/widget",
	); err != nil {
		t.Fatalf("seed workspace_repository: %v", err)
	}
	var wsRepoID int
	_ = db.QueryRow(`SELECT id FROM workspace_repositories LIMIT 1`).Scan(&wsRepoID)

	itemID64, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: 1,
		Title:       "Add recently-viewed work items sub-palette",
	})
	if err != nil {
		t.Fatalf("seed item: %v", err)
	}
	itemID := int(itemID64)
	// The PR-title assertions below reference the fixture's item key
	// (WS-595); pin the number after the production-backed create.
	if _, err := db.Exec(`UPDATE items SET workspace_item_number = 595 WHERE id = ?`, itemID); err != nil {
		t.Fatalf("pin fixture item number: %v", err)
	}

	bindingsRepo := repository.NewWorkspaceAgentBindingRepository(db)
	bindingID, err := bindingsRepo.Insert(context.Background(), &models.WorkspaceAgentBinding{
		WorkspaceID:     1,
		ActingUserID:    agentID,
		ActingUserKind:  ActingIdentityKindAgent,
		RepoSlug:        "acme/widget",
		RepoBaseRef:     "main",
		SCMConnectionID: &connID,
		CreatedByUserID: adminID,
	})
	if err != nil {
		t.Fatalf("seed binding: %v", err)
	}

	var calls int32
	var lastReq OpenPRRequest
	openPR := func(_ context.Context, req OpenPRRequest) (*OpenedPR, error) {
		atomic.AddInt32(&calls, 1)
		lastReq = req
		return &OpenedPR{
			ID:     "42",
			Number: 42,
			URL:    "https://gitea.example.com/acme/widget/pulls/42",
			Title:  req.Title,
			State:  "Open",
			Author: "agent",
		}, nil
	}

	svc, err := NewAgentPRService(AgentPRServiceOptions{
		Bindings: bindingsRepo,
		OpenPR:   openPR,
		DB:       db,
		Logger:   silentLogger(t),
	})
	if err != nil {
		t.Fatalf("new svc: %v", err)
	}
	return svc, bindingsRepo, db, bindingID, wsRepoID, itemID, &calls, func() OpenPRRequest { return lastReq }
}

func TestAgentPRService_OpensPRAndWritesItemLink(t *testing.T) {
	svc, _, db, bindingID, wsRepoID, itemID, calls, lastReq := newPRServiceTestStack(t)

	itemIDPtr := itemID
	svc.AfterRun(context.Background(), PostRunInfo{
		RunID:             7,
		WorkspaceID:       1,
		ItemID:            &itemIDPtr,
		BindingID:         bindingID,
		Status:            models.AgentRunStatusSucceeded,
		Branch:            "agent-runs/run-7",
		BaseCommit:        "abc123",
		TriggeredByUserID: 77,
	})
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Fatalf("openPR calls: want 1, got %d", got)
	}
	req := lastReq()
	if req.Owner != "acme" || req.Repo != "widget" {
		t.Errorf("owner/repo: want acme/widget, got %s/%s", req.Owner, req.Repo)
	}
	if req.HeadBranch != "agent-runs/run-7" || req.BaseBranch != "main" {
		t.Errorf("branches: want head=agent-runs/run-7 base=main, got head=%s base=%s", req.HeadBranch, req.BaseBranch)
	}
	if !req.Draft {
		t.Errorf("expected draft=true; got %v", req.Draft)
	}
	if req.UserID != 77 {
		t.Errorf("credential principal: want triggering user 77, got %d", req.UserID)
	}
	// The PR title is derived from the bound work item, not the generic
	// "agent: work item N (run M)" form.
	if want := "WS-595: Add recently-viewed work items sub-palette"; req.Title != want {
		t.Errorf("PR title: want %q, got %q", want, req.Title)
	}

	// Verify the item_scm_links row landed.
	var (
		linkType, externalURL, state string
		linkedItemID, linkedRepoID   int
	)
	if err := db.QueryRow(`
		SELECT item_id, workspace_repository_id, link_type, external_url, state
		FROM item_scm_links
		WHERE link_type = 'pull_request' AND external_id = '42'
	`).Scan(&linkedItemID, &linkedRepoID, &linkType, &externalURL, &state); err != nil {
		t.Fatalf("read link: %v", err)
	}
	if linkedItemID != itemID {
		t.Errorf("item_id: want %d, got %d", itemID, linkedItemID)
	}
	if linkedRepoID != wsRepoID {
		t.Errorf("workspace_repository_id: want %d, got %d", wsRepoID, linkedRepoID)
	}
	if state != "open" {
		t.Errorf("state lowercased: want open, got %q", state)
	}
}

func TestAgentPRService_SkipsOnNonSuccessStatus(t *testing.T) {
	svc, _, _, bindingID, _, itemID, calls, _ := newPRServiceTestStack(t)
	itemIDPtr := itemID
	svc.AfterRun(context.Background(), PostRunInfo{
		RunID:     7,
		BindingID: bindingID,
		ItemID:    &itemIDPtr,
		Status:    models.AgentRunStatusFailed,
		Branch:    "agent-runs/run-7",
	})
	if got := atomic.LoadInt32(calls); got != 0 {
		t.Errorf("openPR should not be called on failed status, got %d", got)
	}
}

func TestAgentPRService_SkipsWhenBindingHasNoSCMConnection(t *testing.T) {
	svc, bindingsRepo, db, _, _, itemID, calls, _ := newPRServiceTestStack(t)

	// Make a second binding with no SCM connection.
	if _, err := db.Exec(`INSERT INTO users(email, username, first_name, last_name, is_agent) VALUES ('agent2@agents.local','agent2','Ag2','',TRUE)`); err != nil {
		t.Fatalf("seed agent2: %v", err)
	}
	var agent2ID, adminID int
	_ = db.QueryRow(`SELECT id FROM users WHERE username='agent2'`).Scan(&agent2ID)
	_ = db.QueryRow(`SELECT id FROM users WHERE username='admin'`).Scan(&adminID)
	otherID, err := bindingsRepo.Insert(context.Background(), &models.WorkspaceAgentBinding{
		WorkspaceID: 1, ActingUserID: agent2ID, ActingUserKind: ActingIdentityKindAgent, CreatedByUserID: adminID,
	})
	if err != nil {
		t.Fatalf("seed binding 2: %v", err)
	}
	itemIDPtr := itemID
	svc.AfterRun(context.Background(), PostRunInfo{
		RunID:     7,
		BindingID: otherID,
		ItemID:    &itemIDPtr,
		Status:    models.AgentRunStatusSucceeded,
		Branch:    "agent-runs/run-7",
	})
	if got := atomic.LoadInt32(calls); got != 0 {
		t.Errorf("openPR should not be called when SCM connection absent, got %d", got)
	}
}

func TestAgentPRService_OpenPRErrorIsContained(t *testing.T) {
	shrinkOpenPRRetryTiming(t)
	svc, _, _, bindingID, _, itemID, _, _ := newPRServiceTestStack(t)
	svc.openPR = func(context.Context, OpenPRRequest) (*OpenedPR, error) {
		return nil, errors.New("upstream 500")
	}
	itemIDPtr := itemID
	// Must not panic / propagate the error.
	svc.AfterRun(context.Background(), PostRunInfo{
		RunID:     7,
		BindingID: bindingID,
		ItemID:    &itemIDPtr,
		Status:    models.AgentRunStatusSucceeded,
		Branch:    "agent-runs/run-7",
	})
}

// shrinkOpenPRRetryTiming collapses the open-PR retry backoff/timeout so the
// retry tests don't sleep the real 2s/4s between attempts. Restored on cleanup.
func shrinkOpenPRRetryTiming(t *testing.T) {
	t.Helper()
	origBackoff, origTimeout := openPRRetryBackoff, openPRAttemptTimeout
	openPRRetryBackoff = time.Millisecond
	openPRAttemptTimeout = time.Second
	t.Cleanup(func() {
		openPRRetryBackoff = origBackoff
		openPRAttemptTimeout = origTimeout
	})
}

// TestAgentPRService_RetriesTransientOpenPRFailure pins the WI-426 retry: a
// transient OpenPR error (a Codeberg/Gitea timeout) is re-attempted, and a later
// attempt that succeeds still opens the PR and writes the item link — so a flaky
// upstream no longer forces a human to open the branch's PR by hand.
func TestAgentPRService_RetriesTransientOpenPRFailure(t *testing.T) {
	shrinkOpenPRRetryTiming(t)
	svc, _, db, bindingID, wsRepoID, itemID, calls, _ := newPRServiceTestStack(t)
	svc.openPR = func(_ context.Context, req OpenPRRequest) (*OpenedPR, error) {
		if atomic.AddInt32(calls, 1) < 3 {
			return nil, errors.New("Post \"...\": context deadline exceeded")
		}
		return &OpenedPR{ID: "42", Number: 42, URL: "https://gitea.example.com/acme/widget/pulls/42", Title: req.Title, State: "Open", Author: "agent"}, nil
	}

	itemIDPtr := itemID
	svc.AfterRun(context.Background(), PostRunInfo{
		RunID:     7,
		BindingID: bindingID,
		ItemID:    &itemIDPtr,
		Status:    models.AgentRunStatusSucceeded,
		Branch:    "agent-runs/run-7",
	})

	if got := atomic.LoadInt32(calls); got != 3 {
		t.Fatalf("openPR calls: want 3 (two transient failures then success), got %d", got)
	}
	// The PR opened on the third attempt must still produce the item link.
	var links int
	_ = db.QueryRow(`SELECT COUNT(*) FROM item_scm_links WHERE item_id=? AND workspace_repository_id=? AND link_type='pull_request'`, itemID, wsRepoID).Scan(&links)
	if links != 1 {
		t.Fatalf("item_scm_links rows: want 1 after retried success, got %d", links)
	}
}

// TestAgentPRService_StopsRetryingPermanentOpenPRError pins the other half: a
// permanent failure (bad credentials, repo not found, PR already exists) is
// surfaced on the first attempt — retrying it only burns the post-run budget.
func TestAgentPRService_StopsRetryingPermanentOpenPRError(t *testing.T) {
	shrinkOpenPRRetryTiming(t)
	svc, _, _, bindingID, _, itemID, calls, _ := newPRServiceTestStack(t)
	svc.openPR = func(context.Context, OpenPRRequest) (*OpenedPR, error) {
		atomic.AddInt32(calls, 1)
		return nil, NewPermanentOpenPRError(errors.New("resource not found"))
	}

	itemIDPtr := itemID
	svc.AfterRun(context.Background(), PostRunInfo{
		RunID:     7,
		BindingID: bindingID,
		ItemID:    &itemIDPtr,
		Status:    models.AgentRunStatusSucceeded,
		Branch:    "agent-runs/run-7",
	})

	if got := atomic.LoadInt32(calls); got != 1 {
		t.Fatalf("openPR calls: want 1 (permanent error, no retry), got %d", got)
	}
}

// TestAgentPRService_GivesUpAfterMaxRetries pins that a never-recovering
// transient failure is bounded at openPRRetryAttempts and contained (no panic /
// propagation) — the run's terminal status is unaffected.
func TestAgentPRService_GivesUpAfterMaxRetries(t *testing.T) {
	shrinkOpenPRRetryTiming(t)
	svc, _, _, bindingID, _, itemID, calls, _ := newPRServiceTestStack(t)
	svc.openPR = func(context.Context, OpenPRRequest) (*OpenedPR, error) {
		atomic.AddInt32(calls, 1)
		return nil, errors.New("context deadline exceeded")
	}

	itemIDPtr := itemID
	svc.AfterRun(context.Background(), PostRunInfo{
		RunID:     7,
		BindingID: bindingID,
		ItemID:    &itemIDPtr,
		Status:    models.AgentRunStatusSucceeded,
		Branch:    "agent-runs/run-7",
	})

	if got := atomic.LoadInt32(calls); got != openPRRetryAttempts {
		t.Fatalf("openPR calls: want %d (max attempts), got %d", openPRRetryAttempts, got)
	}
}

// TestIsPermanentOpenPRError pins the wrapper's round-trip: only errors wrapped
// by NewPermanentOpenPRError report permanent; bare errors and nil do not.
func TestIsPermanentOpenPRError(t *testing.T) {
	if IsPermanentOpenPRError(errors.New("transient")) {
		t.Error("bare error must not be permanent")
	}
	if IsPermanentOpenPRError(nil) {
		t.Error("nil must not be permanent")
	}
	if NewPermanentOpenPRError(nil) != nil {
		t.Error("NewPermanentOpenPRError(nil) must return nil")
	}
	wrapped := NewPermanentOpenPRError(errors.New("not found"))
	if !IsPermanentOpenPRError(wrapped) {
		t.Error("wrapped error must report permanent")
	}
	if !IsPermanentOpenPRError(fmt.Errorf("context: %w", wrapped)) {
		t.Error("permanence must survive further wrapping")
	}
}

func TestAgentPRService_SplitRepoSlug(t *testing.T) {
	owner, repo, ok := splitRepoSlug("acme/widget")
	if !ok || owner != "acme" || repo != "widget" {
		t.Errorf("good slug: got owner=%q repo=%q ok=%v", owner, repo, ok)
	}
	if _, _, ok := splitRepoSlug("not-a-slug"); ok {
		t.Errorf("missing slash should fail")
	}
	if _, _, ok := splitRepoSlug("/widget"); ok {
		t.Errorf("missing owner should fail")
	}
}

// TestAgentPRService_RendersAgentSummaryAsPRNote pins WI-400: the agent's
// finish summary leads the PR body as the note, with the harness footer below.
func TestAgentPRService_RendersAgentSummaryAsPRNote(t *testing.T) {
	svc, _, _, bindingID, _, itemID, calls, lastReq := newPRServiceTestStack(t)

	itemIDPtr := itemID
	svc.AfterRun(context.Background(), PostRunInfo{
		RunID:       9,
		WorkspaceID: 1,
		ItemID:      &itemIDPtr,
		BindingID:   bindingID,
		Status:      models.AgentRunStatusSucceeded,
		Branch:      "agent-runs/run-9",
		BaseCommit:  "deadbeef",
		Summary:     "Refactored the widget loader and added tests.",
	})
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Fatalf("openPR calls: want 1, got %d", got)
	}
	body := lastReq().Body
	note := strings.Index(body, "Refactored the widget loader and added tests.")
	footer := strings.Index(body, "Opened by the Windshift coding-agent harness.")
	if note < 0 {
		t.Fatalf("PR body missing the agent note:\n%s", body)
	}
	if footer < 0 || note > footer {
		t.Errorf("note must precede the harness footer; note=%d footer=%d body:\n%s", note, footer, body)
	}
	if !strings.Contains(body, "Run id: 9") {
		t.Errorf("footer metadata missing:\n%s", body)
	}
}

// TestAgentPRService_EmptySummaryLeavesFooterOnlyBody pins that a blank summary
// produces the original footer-only body with no dangling rule separator.
func TestAgentPRService_EmptySummaryLeavesFooterOnlyBody(t *testing.T) {
	svc, _, _, bindingID, _, _, calls, lastReq := newPRServiceTestStack(t)

	svc.AfterRun(context.Background(), PostRunInfo{
		RunID:       10,
		WorkspaceID: 1,
		BindingID:   bindingID,
		Status:      models.AgentRunStatusSucceeded,
		Branch:      "agent-runs/run-10",
		BaseCommit:  "cafe",
		Summary:     "   ", // whitespace-only: not a note
	})
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Fatalf("openPR calls: want 1, got %d", got)
	}
	body := lastReq().Body
	if !strings.HasPrefix(body, "Opened by the Windshift coding-agent harness.") {
		t.Errorf("blank summary must yield footer-only body, got:\n%s", body)
	}
	if strings.Contains(body, "---") {
		t.Errorf("no rule separator expected without a note:\n%s", body)
	}
}

// TestBoundPRNote covers trimming and the rune-safe length cap.
func TestBoundPRNote(t *testing.T) {
	if got := boundPRNote("  hello  "); got != "hello" {
		t.Errorf("trim: got %q", got)
	}
	if got := boundPRNote("   "); got != "" {
		t.Errorf("whitespace-only: got %q", got)
	}
	long := strings.Repeat("é", maxPRNoteBytes) // 2 bytes/rune: well over the byte cap
	got := boundPRNote(long)
	if len(got) > maxPRNoteBytes+len("\n\n…(truncated)") {
		t.Errorf("note not bounded: len=%d", len(got))
	}
	if !strings.HasSuffix(got, "(truncated)") {
		t.Errorf("missing truncation flag, tail=%q", got[len(got)-16:])
	}
	if !utf8ValidString(got) {
		t.Errorf("truncation split a rune: %q", got)
	}
}

// TestAgentPRService_FallsBackToGenericTitleWhenItemTitleEmpty pins that an
// item with no usable title yields the generic "agent: work item N (run M)"
// form rather than a bare "WS-N: " with a dangling colon.
func TestAgentPRService_FallsBackToGenericTitleWhenItemTitleEmpty(t *testing.T) {
	svc, _, db, bindingID, _, _, calls, lastReq := newPRServiceTestStack(t)

	itemID64, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: 1,
		Title:       "   ",
	})
	if err != nil {
		t.Fatalf("seed blank-title item: %v", err)
	}
	blankItemID := int(itemID64)

	svc.AfterRun(context.Background(), PostRunInfo{
		RunID:       11,
		WorkspaceID: 1,
		ItemID:      &blankItemID,
		BindingID:   bindingID,
		Status:      models.AgentRunStatusSucceeded,
		Branch:      "agent-runs/run-11",
		BaseCommit:  "abc",
	})
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Fatalf("openPR calls: want 1, got %d", got)
	}
	if want := fmt.Sprintf("agent: work item %d (run 11)", blankItemID); lastReq().Title != want {
		t.Errorf("fallback title: want %q, got %q", want, lastReq().Title)
	}
}

// TestBoundPRTitle covers trimming and the rune-safe length cap.
func TestBoundPRTitle(t *testing.T) {
	if got := boundPRTitle("  WS-1: hi  "); got != "WS-1: hi" {
		t.Errorf("trim: got %q", got)
	}
	long := strings.Repeat("é", maxPRTitleBytes) // 2 bytes/rune: well over the byte cap
	got := boundPRTitle(long)
	if len(got) > maxPRTitleBytes+len("…") {
		t.Errorf("title not bounded: len=%d", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("missing truncation ellipsis, tail=%q", got)
	}
	if !utf8ValidString(got) {
		t.Errorf("truncation split a rune: %q", got)
	}
}

func utf8ValidString(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			return false
		}
	}
	return true
}
