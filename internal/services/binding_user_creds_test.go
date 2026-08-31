//go:build test

package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/auth"
	"windshift/internal/models"
	"windshift/internal/repository"
)

// These tests pin the WI-275 invariant: a run's SCM operations use the
// triggering user's credential (resolved via ResolveForRunAsUser), and a
// triggering user with no connected SCM account produces a VISIBLE failed
// run — never a silent fallback to the workspace connection credential.

// seedRepoBackedBinding inserts the scm provider + connection + a
// repo-backed binding for workspace 1 and returns the binding id.
func seedRepoBackedBinding(t *testing.T, st *bindingTestStack, targetPoolID *int) int {
	t.Helper()
	ctx := context.Background()
	if _, err := st.DB.Exec(`INSERT INTO scm_providers(slug, name, provider_type, auth_method, base_url) VALUES ('uc-gitea', 'Test Gitea', 'gitea', 'oauth', 'https://gitea.example.com')`); err != nil {
		t.Fatalf("seed scm provider: %v", err)
	}
	if _, err := st.DB.Exec(`INSERT INTO workspace_scm_connections(workspace_id, scm_provider_id) VALUES (1, 1)`); err != nil {
		t.Fatalf("seed scm connection: %v", err)
	}
	var scmConn int
	if err := st.DB.QueryRow(`SELECT id FROM workspace_scm_connections LIMIT 1`).Scan(&scmConn); err != nil {
		t.Fatalf("read connection id: %v", err)
	}
	id, err := st.Bindings.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		ActingUserKind:  ActingIdentityKindAgent,
		RepoSlug:        "acme/widget",
		SCMConnectionID: &scmConn,
		TargetPoolID:    targetPoolID,
		TokenTTLMinutes: 15,
		CreatedByUserID: st.AdminID,
	})
	if err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	return id
}

// assertFailedRunRecorded asserts exactly one agent_runs row exists, in
// failed state, attributed to the triggering user, with the
// not-connected reason — the "fail visibly" contract.
func assertFailedRunRecorded(t *testing.T, st *bindingTestStack, wantUser int) {
	t.Helper()
	var (
		status      string
		errMsg      string
		triggeredBy int
		count       int
	)
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM agent_runs`).Scan(&count); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one recorded run, got %d", count)
	}
	if err := st.DB.QueryRow(`SELECT status, error, triggered_by_user_id FROM agent_runs LIMIT 1`).Scan(&status, &errMsg, &triggeredBy); err != nil {
		t.Fatalf("read run: %v", err)
	}
	if status != models.AgentRunStatusFailed {
		t.Errorf("status: want failed, got %q", status)
	}
	if !strings.Contains(errMsg, "no connected SCM account") {
		t.Errorf("error should name the missing SCM connection, got %q", errMsg)
	}
	if triggeredBy != wantUser {
		t.Errorf("triggered_by_user_id: want %d, got %d", wantUser, triggeredBy)
	}
}

func TestBindingService_MaybeStartRun_FailsVisiblyWhenTriggerUserNotConnected(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)
	seedRepoBackedBinding(t, st, nil)
	itemID := seedItem(t, st.DB, 1)

	st.BS.scmCreds = &fakeSCMCreds{
		userErr: fmt.Errorf("user %d on connection 1: %w", st.AdminID, ErrTriggerUserSCMNotConnected),
	}

	newAssignee := st.AgentID
	err := st.BS.MaybeStartRunForAssignee(ctx, 1, itemID, nil, &newAssignee, st.AdminID)
	if !errors.Is(err, ErrTriggerUserSCMNotConnected) {
		t.Fatalf("expected ErrTriggerUserSCMNotConnected, got %v", err)
	}
	if got := atomic.LoadInt32(st.RunCalls); got != 0 {
		t.Fatalf("no run must be dispatched without a credential, got %d runner calls", got)
	}
	assertFailedRunRecorded(t, st, st.AdminID)
}

func TestBindingService_MaybeStartRun_RemotePoolFailsVisiblyWhenTriggerUserNotConnected(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)
	pool := 99
	seedRepoBackedBinding(t, st, &pool)
	itemID := seedItem(t, st.DB, 1)

	st.BS.scmCreds = &fakeSCMCreds{
		userErr: fmt.Errorf("user %d on connection 1: %w", st.AdminID, ErrTriggerUserSCMNotConnected),
	}

	newAssignee := st.AgentID
	err := st.BS.MaybeStartRunForAssignee(ctx, 1, itemID, nil, &newAssignee, st.AdminID)
	if !errors.Is(err, ErrTriggerUserSCMNotConnected) {
		t.Fatalf("expected ErrTriggerUserSCMNotConnected, got %v", err)
	}
	assertFailedRunRecorded(t, st, st.AdminID)

	// The failed run must not be claimable by a pool runner.
	repo := repository.NewAgentRunRepository(st.DB)
	claimed, err := repo.ClaimQueuedForRunner(ctx, pool, 1, time.Now().UTC())
	if err != nil {
		t.Fatalf("claim queued: %v", err)
	}
	if claimed != nil {
		t.Fatalf("failed-start run must not be claimable, claimed run %d", claimed.ID)
	}
}

func TestBindingService_StartTestRun_FailsVisiblyWhenAdminNotConnected(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)
	bindingID := seedRepoBackedBinding(t, st, nil)

	st.BS.scmCreds = &fakeSCMCreds{
		userErr: fmt.Errorf("user %d on connection 1: %w", st.AdminID, ErrTriggerUserSCMNotConnected),
	}

	_, err := st.BS.StartTestRun(ctx, bindingID, 1, st.AdminID)
	if !errors.Is(err, ErrTriggerUserSCMNotConnected) {
		t.Fatalf("expected ErrTriggerUserSCMNotConnected, got %v", err)
	}
	assertFailedRunRecorded(t, st, st.AdminID)
}

// TestBindingService_TokenAndGrants_StampsGitGrantUserID pins the grant
// snapshot: the triggering user rides on GitGrant.UserID, which is what
// the git proxy resolves the credential principal from for remote runs.
func TestBindingService_TokenAndGrants_StampsGitGrantUserID(t *testing.T) {
	st := newBindingTestStack(t, true)

	// bindingTokenAndGrants requires a token-capable RunService; the token
	// manager's DB is never touched by this call, so the stack's DB works.
	tm := auth.NewTokenManager(st.DB, nil)
	tokens, err := NewRunTokenService(tm)
	if err != nil {
		t.Fatalf("token svc: %v", err)
	}
	st.BS.runs.tokens = tokens

	conn := 3
	spec, grants := st.BS.bindingTokenAndGrants(&models.WorkspaceAgentBinding{
		ID:              5,
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		RepoSlug:        "acme/widget",
		SCMConnectionID: &conn,
		TokenTTLMinutes: 15,
	}, 7, st.AdminID, nil)
	if spec == nil || grants == nil || grants.Git == nil {
		t.Fatalf("expected token spec + git grant, got spec=%v grants=%v", spec, grants)
	}
	if grants.Git.UserID != st.AdminID {
		t.Errorf("git grant user_id: want %d, got %d", st.AdminID, grants.Git.UserID)
	}
	if grants.Git.ConnectionID != conn || grants.Git.Repo != "acme/widget" {
		t.Errorf("git grant repo/connection unchanged: got %+v", grants.Git)
	}
}

// TestRunService_RecordFailedStart pins the fail-visibly primitive: the
// run row lands directly in failed state with the reason and the
// triggering user, plus queued + failed lifecycle events — and nothing
// is dispatched.
func TestRunService_RecordFailedStart(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)

	itemID := seedItem(t, st.DB, 1)
	runID, err := st.BS.runs.RecordFailedStart(ctx, RunRequest{
		WorkspaceID:       1,
		ItemID:            &itemID,
		BindingID:         0,
		TriggeredByUserID: st.AdminID,
	}, "assigning user has no connected SCM account")
	if err != nil {
		t.Fatalf("record failed start: %v", err)
	}
	if got := atomic.LoadInt32(st.RunCalls); got != 0 {
		t.Fatalf("RecordFailedStart must not dispatch, got %d runner calls", got)
	}

	repo := repository.NewAgentRunRepository(st.DB)
	run, err := repo.Get(ctx, runID)
	if err != nil {
		t.Fatalf("load run: %v", err)
	}
	if run.Status != models.AgentRunStatusFailed {
		t.Errorf("status: want failed, got %q", run.Status)
	}
	if !strings.Contains(run.Error, "no connected SCM account") {
		t.Errorf("error: got %q", run.Error)
	}
	if run.TriggeredByUserID == nil || *run.TriggeredByUserID != st.AdminID {
		t.Errorf("triggered_by_user_id: want %d, got %v", st.AdminID, run.TriggeredByUserID)
	}
	if run.EndedAt == nil {
		t.Error("ended_at should be stamped by finalize")
	}

	events, err := repo.ListEventsAfter(ctx, runID, 0, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	var phases []string
	for _, e := range events {
		phases = append(phases, e.PayloadJSON)
	}
	joined := strings.Join(phases, " ")
	if !strings.Contains(joined, `"queued"`) || !strings.Contains(joined, `"failed"`) {
		t.Errorf("expected queued + failed lifecycle events, got %v", phases)
	}
}

// TestBindingService_MaybeStartRun_RemotePoolFailsVisiblyOnBadSCMConfig pins
// the start-time dry-run added after the git-proxy 503 incident: a connection
// that resolves a credential fine but has unusable clone-host config (gitea
// with an empty base_url) must produce a visible failed run at trigger time —
// not a queued run that a remote runner claims and then watches die inside
// git with an opaque proxy 503.
func TestBindingService_MaybeStartRun_RemotePoolFailsVisiblyOnBadSCMConfig(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)
	pool := 99
	seedRepoBackedBinding(t, st, &pool)
	itemID := seedItem(t, st.DB, 1)

	// Credential resolves; the connection's clone host does not.
	st.BS.scmCreds = &fakeSCMCreds{token: "t", providerType: "gitea", baseURL: ""}

	newAssignee := st.AgentID
	if err := st.BS.MaybeStartRunForAssignee(ctx, 1, itemID, nil, &newAssignee, st.AdminID); err == nil {
		t.Fatal("expected a clone-config error, got nil")
	}

	var status, errMsg string
	var count int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM agent_runs`).Scan(&count); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one recorded run, got %d", count)
	}
	if err := st.DB.QueryRow(`SELECT status, error FROM agent_runs LIMIT 1`).Scan(&status, &errMsg); err != nil {
		t.Fatalf("read run: %v", err)
	}
	if status != models.AgentRunStatusFailed {
		t.Errorf("status: want failed, got %q", status)
	}
	if !strings.Contains(errMsg, "SCM connection") || !strings.Contains(errMsg, "base_url") {
		t.Errorf("error should name the SCM connection config problem, got %q", errMsg)
	}

	// The failed run must not be claimable by a pool runner.
	repo := repository.NewAgentRunRepository(st.DB)
	claimed, err := repo.ClaimQueuedForRunner(ctx, pool, 1, time.Now().UTC())
	if err != nil {
		t.Fatalf("claim queued: %v", err)
	}
	if claimed != nil {
		t.Fatalf("failed-start run must not be claimable, claimed run %d", claimed.ID)
	}
}
