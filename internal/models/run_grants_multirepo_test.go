package models

import "testing"

// TestRunGrants_GitReposList pins WI-449 multi-repo authorization: the broker
// authorizes each git request against the grant whose repo matches, and pushes
// only to that repo's single granted ref. Deny-by-default for unbound repos.
func TestRunGrants_GitReposList(t *testing.T) {
	g := &RunGrants{
		GitRepos: []GitGrant{
			{Repo: "acme/core", Ref: "agent-runs/run-7", ConnectionID: 1, UserID: 9},
			{Repo: "acme/core-tests", Ref: "agent-runs/run-7", ConnectionID: 2, UserID: 9},
		},
	}

	// Both bound repos are reachable; an unbound one is not.
	if !g.AllowsGitRepo("acme/core") || !g.AllowsGitRepo("acme/core-tests") {
		t.Fatal("both bound repos should be allowed")
	}
	if g.AllowsGitRepo("acme/secret") {
		t.Error("unbound repo must be denied")
	}

	// GitGrantFor selects the per-repo connection/principal.
	if gg := g.GitGrantFor("acme/core-tests"); gg == nil || gg.ConnectionID != 2 {
		t.Errorf("GitGrantFor(core-tests): want connection 2, got %+v", gg)
	}
	if gg := g.GitGrantFor("acme/secret"); gg != nil {
		t.Errorf("GitGrantFor(unbound): want nil, got %+v", gg)
	}

	// Push is gated to each repo's own ref.
	if !g.AllowsGitPush("acme/core", "refs/heads/agent-runs/run-7") {
		t.Error("push to core's granted ref should be allowed")
	}
	if g.AllowsGitPush("acme/core", "refs/heads/agent-runs/run-8") {
		t.Error("push to a non-granted ref must be denied")
	}
	if g.AllowsGitPush("acme/secret", "refs/heads/agent-runs/run-7") {
		t.Error("push to an unbound repo must be denied")
	}
}

// TestRunGrants_GitFallback pins back-compat: a grant carrying only the
// deprecated single Git field still authorizes that one repo (WI-449).
func TestRunGrants_GitFallback(t *testing.T) {
	g := &RunGrants{Git: &GitGrant{Repo: "acme/core", Ref: "run-1"}}
	if !g.AllowsGitRepo("acme/core") {
		t.Error("legacy single Git grant should authorize its repo")
	}
	if !g.AllowsGitPush("acme/core", "refs/heads/run-1") {
		t.Error("legacy single Git grant should authorize its ref push")
	}
	if g.AllowsGitRepo("acme/other") {
		t.Error("legacy single Git grant must not authorize other repos")
	}
}
