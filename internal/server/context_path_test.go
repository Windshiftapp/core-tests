package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWithContextPathStripsPrefix(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/setup/status" {
			t.Fatalf("path = %q, want stripped internal path", r.URL.Path)
		}
		if got := r.Header.Get(contextPathHeader); got != "/windshift" {
			t.Fatalf("%s = %q", contextPathHeader, got)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	r := httptest.NewRequest(http.MethodGet, "http://example.test/windshift/api/setup/status?x=1", nil)
	w := httptest.NewRecorder()
	withContextPath(next, "/windshift").ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestWithContextPathRejectsOutsidePrefix(t *testing.T) {
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not run")
	})

	for _, path := range []string{"/", "/api/setup/status", "/windshifted/api/setup/status"} {
		r := httptest.NewRequest(http.MethodGet, "http://example.test"+path, nil)
		w := httptest.NewRecorder()
		withContextPath(next, "/windshift").ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, w.Code)
		}
	}
}

func TestWithContextPathRedirectsBarePrefix(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://example.test/windshift?x=1", nil)
	w := httptest.NewRecorder()
	withContextPath(http.NotFoundHandler(), "/windshift").ServeHTTP(w, r)

	if w.Code != http.StatusPermanentRedirect {
		t.Fatalf("status = %d", w.Code)
	}
	if got := w.Header().Get("Location"); got != "/windshift/?x=1" {
		t.Fatalf("Location = %q", got)
	}
}

func TestWithContextPathRoutesOAuthDiscoveryForms(t *testing.T) {
	tests := []struct {
		name         string
		externalPath string
		internalPath string
	}{
		{
			name:         "context-prefixed authorization metadata",
			externalPath: "/windshift/.well-known/oauth-authorization-server",
			internalPath: "/.well-known/oauth-authorization-server",
		},
		{
			name:         "RFC 8414 issuer-path metadata",
			externalPath: "/.well-known/oauth-authorization-server/windshift",
			internalPath: "/.well-known/oauth-authorization-server",
		},
		{
			name:         "context-prefixed resource metadata",
			externalPath: "/windshift/.well-known/oauth-protected-resource/mcp",
			internalPath: "/.well-known/oauth-protected-resource/mcp",
		},
		{
			name:         "RFC 9728 resource-path metadata",
			externalPath: "/.well-known/oauth-protected-resource/windshift/mcp",
			internalPath: "/.well-known/oauth-protected-resource/mcp",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path != test.internalPath {
					t.Fatalf("internal path = %q, want %q", request.URL.Path, test.internalPath)
				}
				if got := request.Header.Get(contextPathHeader); got != "/windshift" {
					t.Fatalf("%s = %q, want /windshift", contextPathHeader, got)
				}
				if got := request.Header.Get("X-Forwarded-Prefix"); got != "/windshift" {
					t.Fatalf("X-Forwarded-Prefix = %q, want /windshift", got)
				}
				w.WriteHeader(http.StatusNoContent)
			})
			request := httptest.NewRequest(
				http.MethodGet,
				"http://example.test"+test.externalPath,
				nil,
			)
			recorder := httptest.NewRecorder()

			withContextPath(next, "/windshift").ServeHTTP(recorder, request)

			if recorder.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204", recorder.Code)
			}
		})
	}
}

func TestWithContextPathPrefixesRedirectLocation(t *testing.T) {
	cases := []struct {
		name string
		loc  string
		want string
	}{
		{"root relative", "/?auth_error=boom", "/windshift/?auth_error=boom"},
		{"spa route", "/profile?tab=connected-accounts", "/windshift/profile?tab=connected-accounts"},
		{"already prefixed", "/windshift/profile", "/windshift/profile"},
		{"bare prefix", "/windshift", "/windshift"},
		{"absolute url untouched", "https://idp.example.test/authorize?x=1", "https://idp.example.test/authorize?x=1"},
		{"protocol relative untouched", "//evil.example.test/phish", "//evil.example.test/phish"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, tc.loc, http.StatusFound)
			})

			r := httptest.NewRequest(http.MethodGet, "http://example.test/windshift/api/auth/callback", nil)
			w := httptest.NewRecorder()
			withContextPath(next, "/windshift").ServeHTTP(w, r)

			if w.Code != http.StatusFound {
				t.Fatalf("status = %d, want 302", w.Code)
			}
			if got := w.Header().Get("Location"); got != tc.want {
				t.Fatalf("Location = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWithContextPathDisabledLeavesLocationAlone(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/?auth_error=boom", http.StatusFound)
	})

	r := httptest.NewRequest(http.MethodGet, "http://example.test/api/auth/callback", nil)
	w := httptest.NewRecorder()
	withContextPath(next, "").ServeHTTP(w, r)

	if got := w.Header().Get("Location"); got != "/?auth_error=boom" {
		t.Fatalf("Location = %q, want unchanged", got)
	}
}

func TestPrepareIndexHTMLPrefixesRootAssetsAndInjectsContextPath(t *testing.T) {
	input := []byte(`<!doctype html><head><script>theme()</script><script type="module" src="/_app/app.js"></script><link href="/_app/app.css"><link href="/windshift-3.svg"></head><body></body>`)
	got := string(prepareIndexHTML(input, "nonce123", "/windshift"))

	for _, want := range []string{
		`<script nonce="nonce123">theme()</script>`,
		`window.__WINDSHIFT_CONTEXT_PATH__="/windshift"`,
		`src="/windshift/_app/app.js"`,
		`href="/windshift/_app/app.css"`,
		`href="/windshift/windshift-3.svg"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prepared HTML missing %q in %s", want, got)
		}
	}
}
