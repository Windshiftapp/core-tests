package services

import (
	"context"
	"errors"
	"testing"

	"windshift/internal/auth"
	"windshift/internal/models"
	"windshift/internal/repoprep"
)

// llmPtr returns a pointer to the single enabled test LLM connection id, for
// the binding-create success path.
func llmPtr() *int { v := validTestLLMConnectionID; return &v }

// seedSCMConnForBindings inserts a provider + workspace connection and returns
// the connection id, so a multi-repo binding create can satisfy the per-repo
// scm_connection_id FK.
func seedSCMConnForBindings(t *testing.T, st *bindingTestStack) int {
	t.Helper()
	if _, err := st.DB.Exec(`INSERT INTO scm_providers(slug, name, provider_type, auth_method, base_url) VALUES ('mr-gitea', 'MR Gitea', 'gitea', 'oauth', 'https://gitea.example.com')`); err != nil {
		t.Fatalf("seed scm provider: %v", err)
	}
	if _, err := st.DB.Exec(`INSERT INTO workspace_scm_connections(workspace_id, scm_provider_id) VALUES (1, 1)`); err != nil {
		t.Fatalf("seed scm connection: %v", err)
	}
	var id int
	if err := st.DB.QueryRow(`SELECT id FROM workspace_scm_connections LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("read connection id: %v", err)
	}
	return id
}

func TestBindingService_CreateMultiRepoPersistsAndHydrates(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)
	conn := seedSCMConnForBindings(t, st)

	binding, err := st.BS.Create(ctx, CreateBindingRequest{
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		CreatedByUserID: st.AdminID,
		LLMConnectionID: llmPtr(),
		Repos: []RepoInput{
			{RepoSlug: "acme/core", RepoBaseRef: "main", SCMConnectionID: &conn, IsPrimary: true},
			{RepoSlug: "acme/core-tests", RepoBaseRef: "develop", SCMConnectionID: &conn},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(binding.Repos) != 2 {
		t.Fatalf("repos: want 2, got %d", len(binding.Repos))
	}
	// Reload through the repository to confirm persistence + hydration.
	got, err := st.Bindings.Get(ctx, binding.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Repos) != 2 {
		t.Fatalf("hydrated repos: want 2, got %d", len(got.Repos))
	}
	if p := got.PrimaryRepo(); p == nil || p.RepoSlug != "acme/core" {
		t.Fatalf("primary: want acme/core, got %+v", p)
	}
	if got.RepoSlug != "acme/core" {
		t.Errorf("scalar mirror: want acme/core, got %q", got.RepoSlug)
	}
}

func TestBindingService_CreateSingleRepoDefaultsPrimary(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)
	conn := seedSCMConnForBindings(t, st)

	binding, err := st.BS.Create(ctx, CreateBindingRequest{
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		CreatedByUserID: st.AdminID,
		LLMConnectionID: llmPtr(),
		Repos: []RepoInput{
			{RepoSlug: "acme/core", SCMConnectionID: &conn}, // no IsPrimary
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p := binding.PrimaryRepo(); p == nil || !p.IsPrimary {
		t.Fatalf("sole repo should be defaulted primary, got %+v", p)
	}
}

func TestBindingService_CreateRejectsMultiRepoIssues(t *testing.T) {
	ctx := context.Background()
	scmConn := 1 // rejection happens before insert; need not be seeded

	cases := []struct {
		name  string
		repos []RepoInput
		want  error
	}{
		{
			name: "duplicate slug",
			repos: []RepoInput{
				{RepoSlug: "acme/core", SCMConnectionID: &scmConn, IsPrimary: true},
				{RepoSlug: "acme/core", SCMConnectionID: &scmConn},
			},
			want: ErrBindingDuplicateRepoSlug,
		},
		{
			name: "no primary among many",
			repos: []RepoInput{
				{RepoSlug: "acme/core", SCMConnectionID: &scmConn},
				{RepoSlug: "acme/core-tests", SCMConnectionID: &scmConn},
			},
			want: ErrBindingPrimaryRepoRequired,
		},
		{
			name: "two primaries",
			repos: []RepoInput{
				{RepoSlug: "acme/core", SCMConnectionID: &scmConn, IsPrimary: true},
				{RepoSlug: "acme/core-tests", SCMConnectionID: &scmConn, IsPrimary: true},
			},
			want: ErrBindingPrimaryRepoRequired,
		},
		{
			name: "repo missing scm connection",
			repos: []RepoInput{
				{RepoSlug: "acme/core", IsPrimary: true},
			},
			want: ErrBindingRepoNeedsSCMConnection,
		},
		{
			name: "malformed slug",
			repos: []RepoInput{
				{RepoSlug: "not-a-slug", SCMConnectionID: &scmConn, IsPrimary: true},
			},
			want: ErrBindingInvalidRepoSlug,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newBindingTestStack(t, false)
			_, err := st.BS.Create(ctx, CreateBindingRequest{
				WorkspaceID:     1,
				ActingUserID:    st.AgentID,
				CreatedByUserID: st.AdminID,
				LLMConnectionID: llmPtr(),
				Repos:           tc.repos,
			})
			if !errors.Is(err, tc.want) {
				t.Errorf("err: want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestBindingService_CreateRejectsTooManyRepos(t *testing.T) {
	ctx := context.Background()
	st := newBindingTestStack(t, false)
	scmConn := 1
	repos := make([]RepoInput, 0, maxBindingRepos+1)
	for i := 0; i <= maxBindingRepos; i++ {
		repos = append(repos, RepoInput{
			RepoSlug:        "acme/repo" + string(rune('a'+i)),
			SCMConnectionID: &scmConn,
			IsPrimary:       i == 0,
		})
	}
	_, err := st.BS.Create(ctx, CreateBindingRequest{
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		CreatedByUserID: st.AdminID,
		LLMConnectionID: llmPtr(),
		Repos:           repos,
	})
	if !errors.Is(err, ErrBindingTooManyRepos) {
		t.Errorf("err: want ErrBindingTooManyRepos, got %v", err)
	}
}

// TestBindingService_TokenAndGrants_MultiRepo pins WI-449: a multi-repo binding
// produces one git grant per repo (primary first), and the deprecated Git field
// mirrors the primary for back-compat.
func TestBindingService_TokenAndGrants_MultiRepo(t *testing.T) {
	st := newBindingTestStack(t, true)
	tm := auth.NewTokenManager(st.DB, nil)
	tokens, err := NewRunTokenService(tm)
	if err != nil {
		t.Fatalf("token svc: %v", err)
	}
	st.BS.runs.tokens = tokens

	connA, connB := 3, 4
	binding := &models.WorkspaceAgentBinding{
		ID:              5,
		WorkspaceID:     1,
		ActingUserID:    st.AgentID,
		TokenTTLMinutes: 15,
		Repos: []models.BindingRepo{
			{RepoSlug: "acme/core", SCMConnectionID: &connA, IsPrimary: true, Position: 0},
			{RepoSlug: "acme/core-tests", SCMConnectionID: &connB, Position: 1},
		},
	}
	spec, grants := st.BS.bindingTokenAndGrants(binding, 7, st.AdminID, nil)
	if spec == nil || grants == nil {
		t.Fatalf("expected token spec + grants, got spec=%v grants=%v", spec, grants)
	}
	if len(grants.GitRepos) != 2 {
		t.Fatalf("git repos: want 2, got %d", len(grants.GitRepos))
	}
	if grants.GitRepos[0].Repo != "acme/core" || grants.GitRepos[0].ConnectionID != connA {
		t.Errorf("primary grant: want acme/core conn %d, got %+v", connA, grants.GitRepos[0])
	}
	if grants.GitRepos[1].Repo != "acme/core-tests" || grants.GitRepos[1].ConnectionID != connB {
		t.Errorf("secondary grant: want acme/core-tests conn %d, got %+v", connB, grants.GitRepos[1])
	}
	if grants.GitRepos[0].UserID != st.AdminID {
		t.Errorf("grant principal: want %d, got %d", st.AdminID, grants.GitRepos[0].UserID)
	}
	// Deprecated Git mirrors the primary.
	if grants.Git == nil || grants.Git.Repo != "acme/core" {
		t.Errorf("Git mirror: want acme/core, got %+v", grants.Git)
	}
}

// TestRepoDirNames pins the multi-repo workspace subdir naming (WI-449):
// last slug segment, with a numeric suffix to disambiguate same-named repos
// from different owners.
func TestRepoDirNames(t *testing.T) {
	repos := []*repoprep.RepoSpec{
		{RepoSlug: "acme/core"},
		{RepoSlug: "acme/core-tests"},
		{RepoSlug: "other/core"}, // collides with acme/core's "core"
	}
	got := repoDirNames(repos)
	want := []string{"core", "core-tests", "core-2"}
	if len(got) != len(want) {
		t.Fatalf("len: want %d, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("dir[%d]: want %q, got %q", i, want[i], got[i])
		}
	}
}

// Model accessor unit coverage that doesn't need a DB.
func TestBindingRepo_DirName(t *testing.T) {
	cases := map[string]string{
		"acme/core":       "core",
		"acme/core-tests": "core-tests",
		"core":            "core",
	}
	for slug, want := range cases {
		r := models.BindingRepo{RepoSlug: slug}
		if got := r.DirName(); got != want {
			t.Errorf("DirName(%q): want %q, got %q", slug, want, got)
		}
	}
}
