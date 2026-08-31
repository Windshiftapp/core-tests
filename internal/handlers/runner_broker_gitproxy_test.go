package handlers

import "testing"

// TestAllowedGitProxyPath guards the git-proxy path allow-list (WI-238). The
// {gitpath...} tail is appended raw into the upstream URL the broker fetches
// with the injected SCM credential, so anything other than the three git
// smart-HTTP endpoints must be rejected. In particular a URL-encoded "%2e%2e"
// tail reaches the handler decoded as ".." (ServeMux's cleanPath redirect only
// catches *literal* dot-segments), which — without this allow-list — would let
// a run authorized for owner/repo traverse to a different repo on the SCM host.
// TestGitProxyBaseURL pins the clone-host defaulting: a GitHub-cloud
// connection with no stored base_url must proxy to https://github.com (the
// same default deriveCloneURL applies for local runs) instead of 503ing,
// while explicit bases (GitHub Enterprise) pass through and Gitea keeps
// empty-base as a config error.
func TestGitProxyBaseURL(t *testing.T) {
	cases := []struct{ providerType, stored, want string }{
		{"github", "", "https://github.com"},
		{"github", "https://github.example-corp.com", "https://github.example-corp.com"},
		{"gitea", "", ""}, // no well-known default — stays a config error
		{"gitea", "https://gitea.example.com", "https://gitea.example.com"},
	}
	for _, c := range cases {
		if got := gitProxyBaseURL(c.providerType, c.stored); got != c.want {
			t.Errorf("gitProxyBaseURL(%q, %q) = %q, want %q", c.providerType, c.stored, got, c.want)
		}
	}
}

func TestAllowedGitProxyPath(t *testing.T) {
	allowed := []string{
		"info/refs",
		"git-upload-pack",
		"git-receive-pack",
		"/info/refs", // leading slash is trimmed before the switch
	}
	for _, p := range allowed {
		if !allowedGitProxyPath(p) {
			t.Errorf("allowedGitProxyPath(%q) = false, want true", p)
		}
	}

	blocked := []string{
		"",
		"..",
		"../../evil/repo/git-upload-pack", // decoded traversal tail
		"info/refs/../../evil/repo/git-upload-pack",
		"git-upload-pack/../../../etc/passwd",
		"objects/info/packs", // dumb-http path, not brokered
		"git-upload-pack ",   // trailing space must not match
		"GIT-UPLOAD-PACK",    // case-sensitive
	}
	for _, p := range blocked {
		if allowedGitProxyPath(p) {
			t.Errorf("allowedGitProxyPath(%q) = true, want false", p)
		}
	}
}
