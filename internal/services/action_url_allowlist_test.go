package services

import "testing"

// Regression coverage for the http_client URL allowlist. The allowlist must
// be evaluated against the URL's parsed authority, not its raw text: RFC
// 3986 terminates the authority at # or ?, so a wildcard that matches those
// characters can approve a URL that the HTTP client sends to a different
// host, leaking credential-backed headers off the allowlist.
func TestIsURLAllowedMatchesParsedAuthorityNotRawString(t *testing.T) {
	t.Parallel()

	allowed := []string{"https://*.windshift.dev/**"}

	denied := []struct {
		name string
		url  string
	}{
		{"fragment terminates authority", "https://evil.com#.windshift.dev/api"},
		{"query terminates authority", "https://evil.com?.windshift.dev/api"},
		{"fragment with empty path", "https://evil.com#.windshift.dev/"},
		{"query with credentials in userinfo", "https://key.windshift.dev@evil.com/api"},
		{"unrelated host", "https://evil.com/api"},
		{"suffix lookalike host", "https://notwindshift.dev/api"},
		{"wrong scheme", "http://api.windshift.dev/api"},
		{"missing host", "https:///api"},
		{"opaque URL", "https:evil.com"},
		{"unparseable URL", "https://[::1/api"},
	}
	for _, tc := range denied {
		t.Run("denies "+tc.name, func(t *testing.T) {
			t.Parallel()
			if isURLAllowed(tc.url, allowed) {
				t.Fatalf("isURLAllowed(%q) = true, want false", tc.url)
			}
		})
	}

	granted := []struct {
		name string
		url  string
	}{
		{"subdomain path", "https://api.windshift.dev/v1/items"},
		{"nested subdomain with query", "https://a.b.windshift.dev/v1/search?q=1"},
		{"uppercase host", "https://API.WINDSHIFT.DEV/v1/items"},
		{"userinfo on allowed host", "https://user@api.windshift.dev/v1/items"},
		{"query containing question mark", "https://api.windshift.dev/v1/search?q=a?b"},
	}
	for _, tc := range granted {
		t.Run("allows "+tc.name, func(t *testing.T) {
			t.Parallel()
			if !isURLAllowed(tc.url, allowed) {
				t.Fatalf("isURLAllowed(%q) = false, want true", tc.url)
			}
		})
	}
}

func TestIsURLAllowedExactPathPattern(t *testing.T) {
	t.Parallel()

	allowed := []string{"https://api.windshift.dev/v1/**"}

	if !isURLAllowed("https://api.windshift.dev/v1/items?include=tags", allowed) {
		t.Fatal("expected exact-prefix pattern to allow its path with a query")
	}
	if isURLAllowed("https://api.windshift.dev/v2/items", allowed) {
		t.Fatal("expected exact-prefix pattern to reject a sibling path")
	}
	if isURLAllowed("https://api.windshift.dev/v1", allowed) {
		t.Fatal("expected exact-prefix pattern to reject the bare prefix without a slash")
	}
	if len(allowed) == 0 || isURLAllowed("https://api.windshift.dev/v1/x", nil) {
		t.Fatal("expected empty pattern list to deny everything")
	}
}

func TestIsURLAllowedPortsMustMatchPattern(t *testing.T) {
	t.Parallel()

	if !isURLAllowed("https://api.windshift.dev:8443/v1/items", []string{"https://api.windshift.dev:8443/**"}) {
		t.Fatal("expected a pattern naming the port to allow it")
	}
	if isURLAllowed("https://api.windshift.dev:8443/v1/items", []string{"https://api.windshift.dev/**"}) {
		t.Fatal("expected a pattern without a port to reject a ported URL, since the port changes the origin")
	}
}

func TestIsURLAllowedSchemeWideDoubleWildcard(t *testing.T) {
	t.Parallel()

	if !isURLAllowed("https://evil.example.test/any/path?q=1", []string{"https://**"}) {
		t.Fatal("expected https://** to keep allowing every https URL")
	}
	if isURLAllowed("http://evil.example.test/", []string{"https://**"}) {
		t.Fatal("expected https://** to still reject other schemes")
	}
	if isURLAllowed("https://evil.com#.windshift.dev/api", []string{"https://**.windshift.dev**"}) {
		t.Fatal("expected a trailing ** on the authority to stay confined to the real host")
	}
	if !isURLAllowed("https://a.b.windshift.dev:9090/any/path", []string{"https://**.windshift.dev**"}) {
		t.Fatal("expected a trailing ** on the authority to keep covering port and path")
	}
}
