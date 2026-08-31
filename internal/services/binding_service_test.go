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
	"windshift/internal/llm"
	"windshift/internal/models"
	"windshift/internal/repository"
)

// validTestLLMConnectionID is the single enabled LLM connection the binding
// test stack's fake runtime resolves. Create requires an enabled connection;
// tests point LLMConnectionID at this id for the success path.
const validTestLLMConnectionID = 1

// fakeLLMRuntime implements LLMRuntimeResolver for binding tests. It resolves
// only ids in `valid` — mirroring ConnectionManager.ConnectionRuntime, which
// resolves only enabled connections — so Create's existence/enabled check is
// exercised without standing up a real llm_connections table. PromptConnection
// echoes the prompt so TestLLM can assert the round-trip without a provider.
type fakeLLMRuntime struct{ valid map[int]bool }

func (f *fakeLLMRuntime) ConnectionRuntime(_ context.Context, id int) (*llm.ConnectionRuntimeConfig, error) {
	if f.valid[id] {
		return &llm.ConnectionRuntimeConfig{Model: "test-model"}, nil
	}
	return nil, fmt.Errorf("llm connection %d not found or disabled", id)
}

func (f *fakeLLMRuntime) PromptConnection(_ context.Context, id int, prompt string) (string, error) {
	if f.valid[id] {
		return "echo: " + prompt, nil
	}
	return "", fmt.Errorf("llm connection %d not found or disabled", id)
}

type fakeStandardRunDispatcher struct{}

func (fakeStandardRunDispatcher) StartItemRun(context.Context, *models.WorkspaceAgentBinding, int, int, int, *models.RunTrigger) error {
	return nil
}

func (fakeStandardRunDispatcher) CancelForBinding(context.Context, int) error {
	return nil
}

// seedItem creates an item through the production create path for the given
// workspace and returns the new id. The binding-service trigger tests need a
// real item row so the agent_runs.item_id FK resolves.
func seedItem(t *testing.T, db database.Database, workspaceID int) int {
	t.Helper()
	id, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: workspaceID,
		Title:       "test item",
	})
	if err != nil {
		t.Fatalf("seed item: %v", err)
	}
	return int(id)
}

// bindingTestStack assembles a fresh DB, identity service, binding repo,
// binding service, and (optionally) a wired RunService driven by a stub
// runner. The stub runner records every Run call's input via the supplied
// observer so tests can assert what the trigger passed through.
type bindingTestStack struct {
	BS        *BindingService
	Bindings  *repository.WorkspaceAgentBindingRepository
	DB        database.Database
	AdminID   int
	AgentID   int
	SvcUserID int
	RunCalls  *int32
	LastInput func() RunInput
	// Continuations is the programmable open-PR continuation resolver wired into
	// the service. By default it returns no target (a no-op), so triggers cut a
	// fresh branch; a test sets Fn to make an item resolve to an open PR.
	Continuations *stubContinuationResolver
}

// stubContinuationResolver is a programmable ItemPRContinuationResolver. Fn nil
// means "no open PR" (the resolver is a no-op); Calls records the item ids it
// was asked about so a test can assert a trigger consulted it.
type stubContinuationResolver struct {
	Fn    func(itemID int) (*ContinuationTarget, error)
	Calls []int
}

func (s *stubContinuationResolver) ContinuationForItem(_ context.Context, itemID int) (*ContinuationTarget, error) {
	s.Calls = append(s.Calls, itemID)
	if s.Fn == nil {
		return nil, nil
	}
	return s.Fn(itemID)
}

func newBindingTestStack(t *testing.T, withRunService bool) *bindingTestStack {
	t.Helper()
	db, sec := openIdentityTestDB(t)
	identitySvc, err := NewAgentActingIdentityService(NewUserReadService(db), sec)
	if err != nil {
		t.Fatalf("identity svc: %v", err)
	}
	bindings := repository.NewWorkspaceAgentBindingRepository(db)
	for _, poolID := range []int{4, 5, 7, 99} {
		if _, err := db.Exec(`
			INSERT INTO action_capabilities
				(id, name, capability_type, config, is_enabled, applies_to_all_workspaces)
			VALUES (?, ?, ?, '{}', true, true)
		`, poolID, fmt.Sprintf("Runner pool %d", poolID), models.CapabilityRunnerPool); err != nil {
			t.Fatalf("seed runner pool %d: %v", poolID, err)
		}
	}

	admin := seedIdentityUser(t, db, "alice@example.com", "alice", "Alice", "Hu", false, nil, true)
	agent := seedIdentityUser(t, db, "alice-agent@agents.local", "alice-agent", "Alice", "Agent", true, &admin, true)
	svcUser := seedIdentityUser(t, db, "svc@agents.local", "svc", "Svc", "One", true, nil, true)

	continuations := &stubContinuationResolver{}
	opts := BindingServiceOptions{
		DB:            db,
		Repo:          bindings,
		Identity:      identitySvc,
		Prompts:       llm.NewPromptStore(""),
		StandardRuns:  fakeStandardRunDispatcher{},
		LLMRuntime:    &fakeLLMRuntime{valid: map[int]bool{validTestLLMConnectionID: true}},
		Pools:         repository.NewActionRepository(db),
		Logger:        silentLogger(t),
		Continuations: continuations,
	}
	permissionService, err := NewPermissionService(db, PermissionCacheConfig{
		TTL:             time.Minute,
		MaxCacheSize:    32,
		WarmupOnStartup: false,
		PreWarmActive:   false,
		BatchSize:       10,
	})
	if err != nil {
		t.Fatalf("permission service: %v", err)
	}
	t.Cleanup(func() { _ = permissionService.Close() })
	opts.Permissions = permissionService

	var (
		calls  int32
		lastIn RunInput
	)
	if withRunService {
		runRepo := repository.NewAgentRunRepository(db)
		runner := RunnerFunc(func(ctx context.Context, in RunInput, _ EventSink) RunnerResult {
			atomic.AddInt32(&calls, 1)
			lastIn = in
			return RunnerResult{Status: models.AgentRunStatusSucceeded}
		})
		runSvc, err := NewRunService(runRepo, RunServiceOptions{
			Runner: runner,
			Logger: silentLogger(t),
		})
		if err != nil {
			t.Fatalf("run service: %v", err)
		}
		opts.Runs = runSvc
		t.Cleanup(func() { runSvc.Wait() })
	}

	bs, err := NewBindingService(opts)
	if err != nil {
		t.Fatalf("binding svc: %v", err)
	}
	return &bindingTestStack{
		BS:            bs,
		Bindings:      bindings,
		DB:            db,
		AdminID:       admin,
		AgentID:       agent,
		SvcUserID:     svcUser,
		RunCalls:      &calls,
		LastInput:     func() RunInput { return lastIn },
		Continuations: continuations,
	}
}

func TestBindingService_CreateOwnedAgentPersistsAgentKind(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)

	// Seed an SCM connection so the binding can opt into per-run
	// worktree preparation. RepoSlug + SCMConnectionID are now both
	// required for HasRepo() — the orchestrator derives the clone URL
	// from the connection, no free-form URL is accepted.
	if _, err := st.DB.Exec(`INSERT INTO scm_providers(slug, name, provider_type, auth_method, base_url) VALUES ('test-github', 'Test GitHub', 'github', 'oauth', '')`); err != nil {
		t.Fatalf("seed scm provider: %v", err)
	}
	if _, err := st.DB.Exec(`INSERT INTO workspace_scm_connections(workspace_id, scm_provider_id) VALUES (1, 1)`); err != nil {
		t.Fatalf("seed scm connection: %v", err)
	}
	var scmConn int
	if err := st.DB.QueryRow(`SELECT id FROM workspace_scm_connections LIMIT 1`).Scan(&scmConn); err != nil {
		t.Fatalf("read connection id: %v", err)
	}

	llmConn := validTestLLMConnectionID
	binding, err := st.BS.Create(ctx, CreateBindingRequest{
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		CreatedByUserID: st.AdminID,
		RepoSlug:        "acme/widget",
		SCMConnectionID: &scmConn,
		LLMConnectionID: &llmConn,
		TokenScopes:     []string{"items:read"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if binding.LLMConnectionID == nil || *binding.LLMConnectionID != llmConn {
		t.Errorf("LLMConnectionID: want %d, got %v", llmConn, binding.LLMConnectionID)
	}
	if binding.ActingUserKind != ActingIdentityKindAgent {
		t.Errorf("kind: want %q, got %q", ActingIdentityKindAgent, binding.ActingUserKind)
	}
	if !binding.HasRepo() {
		t.Errorf("HasRepo should be true; got %+v", binding)
	}
}

// A binding with no LLM connection can't run an agent (the llm-proxy 403s a
// run with no LLM grant), so Create must reject it rather than persist a
// non-functional binding.
func TestBindingService_CreateRequiresLLMConnection(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)

	_, err := st.BS.Create(ctx, CreateBindingRequest{
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		CreatedByUserID: st.AdminID,
	})
	if !errors.Is(err, ErrLLMConnectionRequired) {
		t.Errorf("err: want ErrLLMConnectionRequired, got %v", err)
	}
}

// A chosen connection that doesn't resolve to an enabled row (missing or
// disabled) is rejected — ConnectionRuntime only resolves enabled connections.
func TestBindingService_CreateRejectsDisabledLLMConnection(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)

	missing := validTestLLMConnectionID + 99
	_, err := st.BS.Create(ctx, CreateBindingRequest{
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		CreatedByUserID: st.AdminID,
		LLMConnectionID: &missing,
	})
	if !errors.Is(err, ErrLLMConnectionInvalid) {
		t.Errorf("err: want ErrLLMConnectionInvalid, got %v", err)
	}
}

// TestLLM round-trips a prompt through the binding's connection (default prompt
// when none is given) and refuses to probe a binding outside the workspace.
func TestBindingService_TestLLM(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)

	llmConn := validTestLLMConnectionID
	binding, err := st.BS.Create(ctx, CreateBindingRequest{
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		CreatedByUserID: st.AdminID,
		LLMConnectionID: &llmConn,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	answer, err := st.BS.TestLLM(ctx, binding.ID, 1, "")
	if err != nil {
		t.Fatalf("test llm: %v", err)
	}
	if !strings.Contains(answer, DefaultLLMTestPrompt) {
		t.Errorf("answer should echo the default prompt, got %q", answer)
	}

	// A binding in another workspace must not be probable by id.
	if _, err := st.BS.TestLLM(ctx, binding.ID, 999, ""); !errors.Is(err, ErrBindingNotFound) {
		t.Errorf("cross-workspace: want ErrBindingNotFound, got %v", err)
	}

	// Unknown binding id.
	if _, err := st.BS.TestLLM(ctx, binding.ID+999, 1, ""); !errors.Is(err, ErrBindingNotFound) {
		t.Errorf("missing binding: want ErrBindingNotFound, got %v", err)
	}
}

// Regression for WI-136: a binding that wants per-run worktree
// preparation must reference an SCM connection. The orchestrator must
// not accept a free-form clone URL (would otherwise enable SSRF /
// file:// / ext:: attacks via git clone on the host).
func TestBindingService_CreateRejectsRepoSlugWithoutSCMConnection(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)

	_, err := st.BS.Create(ctx, CreateBindingRequest{
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		CreatedByUserID: st.AdminID,
		RepoSlug:        "acme/widget",
		// no SCMConnectionID
	})
	if !errors.Is(err, ErrBindingRepoNeedsSCMConnection) {
		t.Errorf("err: want ErrBindingRepoNeedsSCMConnection, got %v", err)
	}
}

func TestBindingService_CreateRejectsMalformedSlug(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)
	scmConn := 1 // not seeded; rejection happens before insert
	cases := []string{
		"not-owner-repo",
		"acme/widget/extra",
		"../etc/passwd",
		"/acme/widget",
		"https://github.com/acme/widget",
		"acme/widget;rm -rf /",
	}
	for _, slug := range cases {
		_, err := st.BS.Create(ctx, CreateBindingRequest{
			WorkspaceID:     1,
			ActingUserID:    st.AgentID,
			CreatedByUserID: st.AdminID,
			RepoSlug:        slug,
			SCMConnectionID: &scmConn,
		})
		if !errors.Is(err, ErrBindingInvalidRepoSlug) {
			t.Errorf("slug %q: want ErrBindingInvalidRepoSlug, got %v", slug, err)
		}
	}
}

func TestBindingService_CreateRejectsBlockedIdentity(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)

	// Master flag is off → centralized service user is blocked.
	_, err := st.BS.Create(ctx, CreateBindingRequest{
		WorkspaceID:     1,
		ActingUserID:    st.SvcUserID,
		CreatedByUserID: st.AdminID,
	})
	if !errors.Is(err, ErrActingIdentityCentralizedGated) {
		t.Errorf("err: want ErrActingIdentityCentralizedGated, got %v", err)
	}
}

func TestBindingService_MaybeStartRun_FiresWhenAssigneeMatchesBinding(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true /* withRunService */)

	// Binding with no Repo fields — the threading-through-of-RepoSpec is
	// covered by RunService's worktree tests; here we just verify the
	// trigger dispatched.
	if _, err := st.Bindings.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		ActingUserKind:  ActingIdentityKindAgent,
		TokenScopes:     []string{"items:read"},
		TokenTTLMinutes: 15,
		CreatedByUserID: st.AdminID,
	}); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	// Seed an item so the agent_runs FK to items resolves.
	itemID := seedItem(t, st.DB, 1)
	newAssignee := st.AgentID
	if err := st.BS.MaybeStartRunForAssignee(ctx, 1, itemID, nil, &newAssignee, st.AdminID); err != nil {
		t.Fatalf("maybe start: %v", err)
	}
	// MaybeStartRunForAssignee returns once the run is dispatched (the
	// goroutine inside RunService.execute does the real work). Wait for it.
	st.BS.runs.Wait()

	if got := atomic.LoadInt32(st.RunCalls); got != 1 {
		t.Fatalf("expected 1 runner invocation, got %d", got)
	}
	in := st.LastInput()
	if in.RunID == 0 {
		t.Errorf("RunInput.RunID should be set; got %+v", in)
	}
}

// TestBindingService_MaybeStartRun_RoutesToRemotePool verifies WI-195
// routing: a binding with a TargetPoolID queues the run for that remote pool
// (claimable by a runner scoped to the pool) instead of handing it to the
// local in-process runner.
func TestBindingService_MaybeStartRun_RoutesToRemotePool(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true /* withRunService */)

	pool := 99
	if _, err := st.Bindings.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		ActingUserKind:  ActingIdentityKindAgent,
		TargetPoolID:    &pool,
		TokenScopes:     []string{"items:read"},
		TokenTTLMinutes: 15,
		CreatedByUserID: st.AdminID,
	}); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	itemID := seedItem(t, st.DB, 1)
	newAssignee := st.AgentID
	if err := st.BS.MaybeStartRunForAssignee(ctx, 1, itemID, nil, &newAssignee, st.AdminID); err != nil {
		t.Fatalf("maybe start: %v", err)
	}

	// The local runner must not have been invoked — the run is queued for the
	// remote pool, not executed in-process.
	if got := atomic.LoadInt32(st.RunCalls); got != 0 {
		t.Fatalf("remote binding should not invoke the local runner, got %d calls", got)
	}

	// A runner scoped to the pool can claim exactly this run.
	repo := repository.NewAgentRunRepository(st.DB)
	claimed, err := repo.ClaimQueuedForRunner(ctx, pool, 1 /* runnerID */, time.Now().UTC())
	if err != nil {
		t.Fatalf("claim queued: %v", err)
	}
	if claimed == nil {
		t.Fatal("expected a queued run for the pool, got none")
	}
	if claimed.TargetPoolID == nil || *claimed.TargetPoolID != pool {
		t.Errorf("target_pool_id: want %d, got %v", pool, claimed.TargetPoolID)
	}
	if claimed.JobKind != models.JobKindCodingAgent {
		t.Errorf("job_kind: want coding_agent, got %q", claimed.JobKind)
	}
	if claimed.BindingID == nil {
		t.Error("binding_id should be persisted on the remote run")
	}
	if claimed.TriggeredByUserID == nil || *claimed.TriggeredByUserID != st.AdminID {
		t.Errorf("triggered_by_user_id: want %d, got %v", st.AdminID, claimed.TriggeredByUserID)
	}
}

func TestBindingService_MaybeStartRun_NoOpWhenAssigneeUnchanged(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)
	if _, err := st.Bindings.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID: 1, ActingUserID: st.AgentID, ActingUserKind: ActingIdentityKindAgent, CreatedByUserID: st.AdminID,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	old := st.AgentID
	newVal := st.AgentID
	if err := st.BS.MaybeStartRunForAssignee(ctx, 1, 7, &old, &newVal, st.AdminID); err != nil {
		t.Fatalf("maybe start: %v", err)
	}
	if got := atomic.LoadInt32(st.RunCalls); got != 0 {
		t.Fatalf("expected 0 runner invocations (assignee unchanged), got %d", got)
	}
}

// embedTokenInRemoteURL was deleted in WI-137: tokens travel on
// RepoSpec.Token and are injected into git via a per-clone GIT_ASKPASS
// helper, never embedded in the URL. The previous EmbedTokenInRemoteURL
// table-test moves to redact_test.go (RedactString must strip those
// shapes if a stray one ever appears in an error string).

// fakeSCMCreds is a deterministic stand-in for scm.CredentialResolver.
// userErr, when set, is returned from ResolveForRunAsUser — tests use it
// with ErrTriggerUserSCMNotConnected to simulate a triggering user who has
// not connected an SCM account (WI-275).
type fakeSCMCreds struct {
	token        string
	providerType string
	baseURL      string
	calls        int
	userCalls    int
	lastUserID   int
	userErr      error
}

func (f *fakeSCMCreds) ResolveForRun(ctx context.Context, _ int) (string, string, string, error) {
	f.calls++
	return f.token, f.providerType, f.baseURL, nil
}

func (f *fakeSCMCreds) ResolveForRunAsUser(ctx context.Context, _, userID int) (string, string, string, error) {
	f.calls++
	f.userCalls++
	f.lastUserID = userID
	if f.userErr != nil {
		return "", "", "", f.userErr
	}
	return f.token, f.providerType, f.baseURL, nil
}

// TestBindingService_MaybeStartRun_DerivesURLFromSCMConnection asserts
// the trigger resolves SCM credentials and constructs the clone URL
// from the connection's provider host + the binding's slug — not from a
// client-supplied URL. This is the load-bearing security property of
// WI-136: a workspace admin who can create a binding cannot make the
// orchestrator clone from an arbitrary host.
func TestBindingService_MaybeStartRun_DerivesURLFromSCMConnection(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)
	// Seed a minimal scm_providers + workspace_scm_connections row so
	// the FK in workspace_agent_bindings.scm_connection_id resolves.
	if _, err := st.DB.Exec(`INSERT INTO scm_providers(slug, name, provider_type, auth_method, base_url) VALUES ('test-gitea', 'Test Gitea', 'gitea', 'oauth', 'https://gitea.example.com')`); err != nil {
		t.Fatalf("seed scm provider: %v", err)
	}
	if _, err := st.DB.Exec(`INSERT INTO workspace_scm_connections(workspace_id, scm_provider_id) VALUES (1, 1)`); err != nil {
		t.Fatalf("seed scm connection: %v", err)
	}
	var scmConn int
	if err := st.DB.QueryRow(`SELECT id FROM workspace_scm_connections LIMIT 1`).Scan(&scmConn); err != nil {
		t.Fatalf("read connection id: %v", err)
	}

	if _, err := st.Bindings.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		ActingUserKind:  ActingIdentityKindAgent,
		RepoSlug:        "acme/widget",
		SCMConnectionID: &scmConn,
		TokenTTLMinutes: 15,
		CreatedByUserID: st.AdminID,
	}); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	itemID := seedItem(t, st.DB, 1)

	creds := &fakeSCMCreds{
		token:        "gt-secret",
		providerType: "gitea",
		baseURL:      "https://gitea.example.com",
	}
	st.BS.scmCreds = creds

	// Repo preparer required because HasRepo() is true. A real clone will
	// fail (gitea.example.com is not reachable) but that happens
	// asynchronously inside RunService.execute and is not what this test
	// asserts.
	prep := newTestPreparer(t)
	st.BS.runs.preparer = prep

	newAssignee := st.AgentID
	if err := st.BS.MaybeStartRunForAssignee(ctx, 1, itemID, nil, &newAssignee, st.AdminID); err != nil {
		t.Fatalf("maybe start: %v", err)
	}
	st.BS.runs.Wait()

	if creds.calls != 1 {
		t.Errorf("expected credential resolution to be called once, got %d", creds.calls)
	}
	if creds.userCalls != 1 || creds.lastUserID != st.AdminID {
		t.Errorf("credential principal: want one user-aware resolution for user %d, got calls=%d user=%d", st.AdminID, creds.userCalls, creds.lastUserID)
	}
}

// TestDeriveCloneURL covers the URL-derivation chokepoint: GitHub uses
// github.com unless a base_url is set; Gitea always honours base_url;
// unknown provider types and malformed slugs are rejected.
func TestDeriveCloneURL(t *testing.T) {
	cases := []struct {
		name         string
		providerType string
		baseURL      string
		slug         string
		want         string
		wantErr      bool
	}{
		{"github.com default", "github", "", "acme/widget", "https://github.com/acme/widget.git", false},
		{"github enterprise base url", "github", "https://github.example-corp.com", "acme/widget", "https://github.example-corp.com/acme/widget.git", false},
		{"gitea self-hosted", "gitea", "https://gitea.example.com", "acme/widget", "https://gitea.example.com/acme/widget.git", false},
		{"gitea missing base url", "gitea", "", "acme/widget", "", true},
		{"unknown provider", "bitbucket", "https://bitbucket.org", "acme/widget", "", true},
		{"malformed slug", "github", "", "../../etc/passwd", "", true},
		{"scheme in slug", "github", "", "https://github.com/acme/widget", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := deriveCloneURL(tc.providerType, tc.baseURL, tc.slug)
			if tc.wantErr {
				if err == nil {
					t.Errorf("want error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBindingService_MaybeStartRun_NoOpWhenNoBindingMatches(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)
	newVal := st.AgentID // no binding configured
	if err := st.BS.MaybeStartRunForAssignee(ctx, 1, 7, nil, &newVal, st.AdminID); err != nil {
		t.Fatalf("maybe start: %v", err)
	}
	if got := atomic.LoadInt32(st.RunCalls); got != 0 {
		t.Fatalf("expected 0 runner invocations (no binding), got %d", got)
	}
}

func TestBindingService_CreateRejectsOverlongTTL(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)

	_, err := st.BS.Create(ctx, CreateBindingRequest{
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		CreatedByUserID: st.AdminID,
		TokenTTLMinutes: int((24 * time.Hour).Minutes()),
	})
	if !errors.Is(err, ErrBindingTokenTTLOverCap) {
		t.Errorf("err: want ErrBindingTokenTTLOverCap, got %v", err)
	}
}

func TestBindingService_CreateRejectsAdminScope(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)

	_, err := st.BS.Create(ctx, CreateBindingRequest{
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		CreatedByUserID: st.AdminID,
		TokenScopes:     []string{"items:read", "admin:users:write"},
	})
	if err == nil || !strings.Contains(err.Error(), "scopes not permitted for coding-agent tokens") {
		t.Errorf("want agent-scope error, got %v", err)
	}
}

func TestBindingService_MaybeStartRun_BlockedAtBudget(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, true)

	if _, err := st.Bindings.Insert(ctx, &models.WorkspaceAgentBinding{
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		ActingUserKind:  ActingIdentityKindAgent,
		MaxRunsPerDay:   1,
		CreatedByUserID: st.AdminID,
	}); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	itemID := seedItem(t, st.DB, 1)
	newAssignee := st.AgentID

	if err := st.BS.MaybeStartRunForAssignee(ctx, 1, itemID, nil, &newAssignee, st.AdminID); err != nil {
		t.Fatalf("maybe start (#1): %v", err)
	}
	st.BS.runs.Wait()
	if got := atomic.LoadInt32(st.RunCalls); got != 1 {
		t.Fatalf("after first trigger: want 1 run, got %d", got)
	}

	// Re-trigger on the same item by simulating a different prior
	// assignee, so the trigger fires again. The binding's budget for the
	// rolling 24h window is already spent, so this admission must fail.
	otherID := st.AdminID
	err := st.BS.MaybeStartRunForAssignee(ctx, 1, itemID, &otherID, &newAssignee, st.AdminID)
	if !errors.Is(err, ErrBindingBudgetExceeded) {
		t.Fatalf("after budget: want ErrBindingBudgetExceeded, got %v", err)
	}
	if got := atomic.LoadInt32(st.RunCalls); got != 1 {
		t.Errorf("budget breach must not invoke runner; got %d runs", got)
	}
}
