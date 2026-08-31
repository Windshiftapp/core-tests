//go:build test

package models

import "testing"

// TestRunGrants_AllowsHTTP_Boundaries pins the URL-boundary matching that
// replaced raw strings.HasPrefix (WI-168): a host grant must not leak to
// look-alike hosts, userinfo-smuggled targets, or path-prefix siblings.
func TestRunGrants_AllowsHTTP_Boundaries(t *testing.T) {
	g := &RunGrants{HTTP: []string{"https://api.example.com/v1"}}

	allowed := []string{
		"https://api.example.com/v1",
		"https://api.example.com/v1/models",
		"https://API.Example.com/v1/models", // host is case-insensitive
	}
	for _, u := range allowed {
		if !g.AllowsHTTP(u) {
			t.Errorf("expected %q to be allowed", u)
		}
	}

	denied := []string{
		"https://api.example.com.evil/v1",            // suffix-extended host
		"https://api.example.com@169.254.169.254/v1", // userinfo smuggling
		"https://api.example.comX/v1",                // host not boundary-terminated
		"https://api.example.com/v1evil",             // path not boundary-terminated
		"https://api.example.com/v2",                 // different path
		"http://api.example.com/v1",                  // scheme mismatch
		"https://api.example.com:8443/v1",            // port mismatch
		"https://evil.com/v1",                        // different host
	}
	for _, u := range denied {
		if g.AllowsHTTP(u) {
			t.Errorf("expected %q to be DENIED", u)
		}
	}
}

// TestRunGrants_AllowsHTTP_HostGrant: a grant with no path (or "/") permits
// any path on that exact host, but still nothing on another host.
func TestRunGrants_AllowsHTTP_HostGrant(t *testing.T) {
	g := &RunGrants{HTTP: []string{"https://api.example.com"}}
	if !g.AllowsHTTP("https://api.example.com/anything/goes") {
		t.Error("host grant should allow any path on the host")
	}
	if g.AllowsHTTP("https://api.example.com.evil/") {
		t.Error("host grant must not leak to a look-alike host")
	}
}

// TestRunGrants_AllowsHTTP_Empty: nil grant and empty patterns deny by default.
func TestRunGrants_AllowsHTTP_Empty(t *testing.T) {
	var nilG *RunGrants
	if nilG.AllowsHTTP("https://api.example.com/v1") {
		t.Error("nil grant must deny")
	}
	g := &RunGrants{HTTP: []string{"", "not a url"}}
	if g.AllowsHTTP("https://api.example.com/v1") {
		t.Error("empty / malformed patterns must not match")
	}
}

// TestRunGrants_AllowsGitPush pins exact single-ref push authorization.
func TestRunGrants_AllowsGitPush(t *testing.T) {
	g := &RunGrants{Git: &GitGrant{Repo: "acme/widgets", Ref: "refs/heads/run-42"}}

	if !g.AllowsGitPush("acme/widgets", "refs/heads/run-42") {
		t.Error("granted ref must be allowed")
	}
	for _, ref := range []string{
		"refs/heads/main",
		"refs/heads/run-42x",
		"refs/tags/v1",
		"",
	} {
		if g.AllowsGitPush("acme/widgets", ref) {
			t.Errorf("ref %q must be denied", ref)
		}
	}
	if g.AllowsGitPush("other/repo", "refs/heads/run-42") {
		t.Error("push to a non-granted repo must be denied")
	}

	// Empty grant ref authorizes no push at all.
	noPush := &RunGrants{Git: &GitGrant{Repo: "acme/widgets"}}
	if noPush.AllowsGitPush("acme/widgets", "refs/heads/run-42") {
		t.Error("empty grant ref must deny all pushes")
	}
}

// TestRunGrants_AllowsGitPush_ShortGrantRef pins the real-world shape: the run
// service mints the grant with a SHORT branch name (agent-runs/run-N) while
// git-receive-pack sends the fully-qualified ref. The gate must normalize both
// to refs/heads/<branch> so the run's own push is authorized, without widening
// to a different branch or to a same-named tag.
func TestRunGrants_AllowsGitPush_ShortGrantRef(t *testing.T) {
	g := &RunGrants{Git: &GitGrant{Repo: "acme/widgets", Ref: "agent-runs/run-20"}}

	if !g.AllowsGitPush("acme/widgets", "refs/heads/agent-runs/run-20") {
		t.Error("short grant ref must authorize the fully-qualified branch push")
	}
	if !g.AllowsGitPush("acme/widgets", "agent-runs/run-20") {
		t.Error("short grant ref must also match a bare branch name")
	}
	for _, ref := range []string{
		"refs/heads/agent-runs/run-200", // longer name, not the granted branch
		"refs/heads/agent-runs/run-2",   // shorter name, not the granted branch
		"refs/tags/agent-runs/run-20",   // same name but a tag, not a branch
		"refs/heads/main",
	} {
		if g.AllowsGitPush("acme/widgets", ref) {
			t.Errorf("ref %q must be denied for short grant ref", ref)
		}
	}
}
