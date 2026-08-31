package tests

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// TestAnonymousBaseline asserts that every registered HTTP route refuses
// to **succeed** for an unauthenticated request. "Refuses to succeed" is
// the right invariant rather than strict 401, because:
//
//   - Auth middleware returns 401 (the canonical case)
//   - Some handlers run unauthenticated, look up a resource by URL token,
//     and return 404 when the lookup fails (acceptable — anonymous still
//     got nothing useful)
//   - Some handlers return 400 because the body is missing (acceptable —
//     the handler ran but couldn't proceed)
//   - 405 happens when the method dispatcher rejects pre-auth (acceptable)
//
// The high-signal regression to catch is a route that *returns 2xx to an
// anonymous request*. That means it's reachable without auth, which is
// either a missing RequireAuth middleware or an intentionally-public
// route that landed without being declared in
// anonymousBaselineKnownPublic.
//
// Net effect: every non-exempt route gets a trivial smoke-check; any landing that
// makes a previously-gated route publicly readable (or accidentally adds
// a public-readable endpoint) fails this test loudly.
func TestAnonymousBaseline(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	routes := EnumerateRegisteredRoutes(t)
	if len(routes) == 0 {
		t.Fatal("EnumerateRegisteredRoutes returned 0 routes — fix the enumerator before relying on this test")
	}

	var skipped, checked int
	for _, r := range routes {
		// Skip CORS preflight — handled by middleware, not by the auth chain.
		if r.Method == http.MethodOptions {
			skipped++
			continue
		}
		if isAnonymousBaselineExempt(r.Method, r.Path) {
			skipped++
			continue
		}

		// Substitute placeholders ({id}, {slug}, etc.) with harmless literals.
		// 401 returns *before* any handler logic runs, so the values don't
		// have to resolve to real DB rows.
		concretePath := expandPlaceholders(r.Path)

		checked++
		t.Run(r.Method+" "+r.Path, func(t *testing.T) {
			resp := makeAnonymousRequest(t, server, r.Method, concretePath)
			defer resp.Body.Close()

			// 2xx to anonymous = the route is reachable without auth.
			// Either it forgot RequireAuth, or it's an intended-public
			// route that needs to land in anonymousBaselineKnownPublic.
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				t.Errorf("%s %s succeeded as anonymous (status %d, path=%s) — add to anonymousBaselineKnownPublic if intentionally public, or wire auth.RequireAuth",
					r.Method, r.Path, resp.StatusCode, concretePath)
			}
		})
	}

	t.Logf("anonymous baseline: checked %d routes, skipped %d (exempt/OPTIONS) out of %d total",
		checked, skipped, len(routes))
}

// anonymousBaselineExemptions is the canonical list of path prefixes that
// the anonymous-baseline test skips entirely. These are routes where the
// expected response to anonymous is either 2xx (genuinely public read) or
// where the route's purpose is *to be reached anonymously* (login flows,
// magic-link consumption).
//
// Adding a route here is a security decision — review carefully. The
// test's intent is "non-exempt routes must not return 2xx to anonymous,"
// so widening this list narrows the safety net.
//
// Each entry is (method, path-prefix). Method "*" matches any method.
// path-prefix matches the route's source path (NOT the request URL), so
// "/api/auth/login" matches the registered "/api/auth/login" route.
var anonymousBaselineExemptions = []struct {
	method string // "*" for any
	prefix string
}{
	// Setup wizard — bypasses auth while setupCompleted == false. After
	// setup the routes still serve but reject via the setup-completed
	// guard, not the auth middleware, so they're exempt from the 401
	// invariant.
	{"*", "/api/setup/"},

	// Auth flows are by definition reachable without an existing session:
	// login, magic-link consumption, and password reset. The SSO prefix is
	// outside the public suite's feature boundary and is not audited here.
	{"*", "/api/auth/"},
	{"*", "/api/sso/"},
	{"*", "/api/webauthn/"},
	{"*", "/api/passkey/"},
	{"*", "/api/invitations/"}, // invitation acceptance carries token in URL
	{"*", "/api/email/verify"}, // email-verification token flow
	{"*", "/api/password-reset"},

	// Portal surface — multiple endpoints are public by design (anonymous
	// users browsing portals). The portal customer auth gate runs only
	// on the request-submission endpoints, not on the read endpoints.
	{"*", "/api/portal/"},
	{"*", "/api/portal-assets/"},

	// Public board surface — explicitly anonymous.
	{"*", "/api/public-board/"},

	// Calendar feed — token-in-URL.
	{"*", "/api/calendar/feed/"},

	// Health / readiness / version are unauthenticated by intent.
	{"*", "/api/health"},
	{"*", "/api/ready"},
	{"*", "/api/version"},
	{"*", "/api/setup-status"},
	{"*", "/api/capabilities"},

	// OAuth 2.0 token + discovery endpoints — anonymous per RFC 6749/8414.
	{"*", "/api/oauth/"},
	{"*", "/api/.well-known/"},

	// Public form submission surface — anonymous by intent (the embedded
	// portal/form widget).
	{"*", "/api/forms/"},

	// Public read-only board surface.
	{"*", "/api/public/board/"},

	// CLI device-code auth + capabilities probe — anonymous bootstrap path.
	{"*", "/api/cli/"},

	// Capabilities probe + feature flags — exposed pre-auth so the frontend
	// can decide which login surface to render.
	{"GET", "/api/features"},
	{"GET", "/api/capabilities"},

	// OAuth/SCM/email-provider OAuth callbacks — token-in-URL flows, no
	// session yet.
	{"*", "/api/integrations/oauth/"},
	{"*", "/api/scm/oauth/"},
	{"*", "/api/email/oauth/"},
	{"*", "/api/channels/inline-oauth/"},

	// Plugin static assets — served pre-auth (the plugin loader doesn't
	// have a session when fetching its own JS/CSS).
	{"*", "/api/plugins/"},

	// Hosted runner install script — public by intent, like /version
	// (templated curl|bash script, no secrets; the registration token is
	// supplied by the operator, never embedded).
	{"GET", "/api/runner-install.sh"},
}

// isAnonymousBaselineExempt reports whether the given route is excluded
// from the anonymous-401 invariant. Used to filter the route list before
// the per-route subtest runs.
func isAnonymousBaselineExempt(method, path string) bool {
	for _, e := range anonymousBaselineExemptions {
		if e.method != "*" && e.method != method {
			continue
		}
		// Exact-suffix-after-slash exemption: "/api/auth/" matches
		// "/api/auth/login" but NOT "/api/authority". Strict prefix match
		// after dropping the trailing slash is too permissive, so we
		// require either equality or that the next char in path is '/'.
		if path == strings.TrimSuffix(e.prefix, "/") {
			return true
		}
		if strings.HasPrefix(path, e.prefix) {
			return true
		}
	}
	return false
}

// placeholderRE matches Go 1.22 path-pattern placeholders like {id} or
// {workspaceId}. Used to swap them for a harmless concrete value when
// building the request URL.
var placeholderRE = regexp.MustCompile(`\{[^/}]+\}`)

// expandPlaceholders substitutes "{name}" segments with "1". The exact
// value doesn't matter: 401 returns before any handler logic runs, so
// no DB lookup happens. Using "1" rather than a longer literal keeps the
// URL short for test output.
func expandPlaceholders(path string) string {
	return placeholderRE.ReplaceAllString(path, "1")
}

// makeAnonymousRequest fires an unauthenticated request at an absolute
// path on the test server. Unlike MakeUnauthenticatedRequest (which
// hard-codes the /api prefix via APIBase), this lets the caller target
// /rest/api/v1/* surfaces as well.
func makeAnonymousRequest(t *testing.T, server *TestServer, method, path string) *http.Response {
	t.Helper()
	url := server.BaseURL + path
	return makeRequest(t, method, url, "", nil, nil)
}
