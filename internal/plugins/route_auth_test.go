//go:build !noplugins

package plugins

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"windshift/internal/contextkeys"
	"windshift/internal/models"
)

func TestNormalizePluginRoutes(t *testing.T) {
	cases := []struct {
		name        string
		routes      []Route
		allowPublic bool
		wantAuth    []RouteAuth
		wantErr     bool
	}{
		{
			name:     "empty defaults to session",
			routes:   []Route{{Method: "GET", Path: "/x"}},
			wantAuth: []RouteAuth{RouteAuthSession},
		},
		{
			name:        "none preserved when public routes are allowed",
			routes:      []Route{{Method: "GET", Path: "/x", Auth: RouteAuthNone}},
			allowPublic: true,
			wantAuth:    []RouteAuth{RouteAuthNone},
		},
		{
			name:    "none rejected when public routes are disabled",
			routes:  []Route{{Method: "GET", Path: "/x", Auth: RouteAuthNone}},
			wantErr: true,
		},
		{
			name:     "admin preserved",
			routes:   []Route{{Method: "GET", Path: "/x", Auth: RouteAuthAdmin}},
			wantAuth: []RouteAuth{RouteAuthAdmin},
		},
		{
			name:    "unknown fails",
			routes:  []Route{{Method: "GET", Path: "/x", Auth: "root"}},
			wantErr: true,
		},
		{
			name: "wildcard overlapping exact with different auth fails",
			routes: []Route{
				{Method: "GET", Path: "/x*", Auth: RouteAuthNone},
				{Method: "GET", Path: "/x/y", Auth: RouteAuthSession},
			},
			allowPublic: true,
			wantErr:     true,
		},
		{
			name: "same path with different auth fails",
			routes: []Route{
				{Method: "GET", Path: "/x", Auth: RouteAuthNone},
				{Method: "GET", Path: "/x", Auth: RouteAuthSession},
			},
			allowPublic: true,
			wantErr:     true,
		},
		{
			name: "overlap with the same auth is allowed",
			routes: []Route{
				{Method: "GET", Path: "/x*", Auth: RouteAuthNone},
				{Method: "GET", Path: "/x/y", Auth: RouteAuthNone},
			},
			allowPublic: true,
			wantAuth:    []RouteAuth{RouteAuthNone, RouteAuthNone},
		},
		{
			name: "different methods do not overlap",
			routes: []Route{
				{Method: "GET", Path: "/x", Auth: RouteAuthNone},
				{Method: "POST", Path: "/x", Auth: RouteAuthSession},
			},
			allowPublic: true,
			wantAuth:    []RouteAuth{RouteAuthNone, RouteAuthSession},
		},
		{
			name: "distinct exact paths do not overlap",
			routes: []Route{
				{Method: "GET", Path: "/x", Auth: RouteAuthNone},
				{Method: "GET", Path: "/x/y", Auth: RouteAuthSession},
			},
			allowPublic: true,
			wantAuth:    []RouteAuth{RouteAuthNone, RouteAuthSession},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizePluginRoutes(tc.routes, tc.allowPublic)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.wantAuth) {
				t.Fatalf("routes = %d, want %d", len(got), len(tc.wantAuth))
			}
			for i := range got {
				if got[i].Auth != tc.wantAuth[i] {
					t.Fatalf("route %d auth = %q, want %q", i, got[i].Auth, tc.wantAuth[i])
				}
			}
		})
	}
}

func TestLoadPluginRejectsInvalidRouteAuth(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "bad-auth")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create plugin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.wasm"), minimalMultiCapabilityModule(), 0o600); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	manifest := `{
		"name": "bad-auth",
		"version": "1.0.0",
		"entryPoint": "plugin.wasm",
		"routes": [{"method": "GET", "path": "/x", "auth": "root"}]
	}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	m := NewManager(root)
	if err := m.LoadPlugin(dir); err == nil {
		t.Fatal("expected load to fail for an unknown route auth")
	}
}

func TestLoadPluginGatesPublicRoutes(t *testing.T) {
	write := func(t *testing.T) (string, string) {
		t.Helper()
		root := t.TempDir()
		dir := filepath.Join(root, "pub")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create plugin dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "plugin.wasm"), minimalMultiCapabilityModule(), 0o600); err != nil {
			t.Fatalf("write wasm: %v", err)
		}
		manifest := `{
			"name": "pub",
			"version": "1.0.0",
			"entryPoint": "plugin.wasm",
			"routes": [{"method": "GET", "path": "/x", "auth": "none"}]
		}`
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
		return root, dir
	}

	t.Run("disabled by default", func(t *testing.T) {
		root, dir := write(t)
		m := NewManager(root)
		if err := m.LoadPlugin(dir); err == nil {
			t.Fatal("expected load to fail with public routes disabled")
		}
	})

	t.Run("allowed with the option", func(t *testing.T) {
		root, dir := write(t)
		m := NewManager(root, WithAllowPublicRoutes(true))
		if err := m.LoadPlugin(dir); err != nil {
			t.Fatalf("expected load to succeed: %v", err)
		}
	})
}

func TestFindRouteSelectsMostSpecific(t *testing.T) {
	routes := []Route{
		{Method: "GET", Path: "/x*", Auth: RouteAuthNone},
		{Method: "GET", Path: "/x/y", Auth: RouteAuthSession},
	}
	route, ok := findRoute(routes, "GET", "/x/y")
	if !ok {
		t.Fatal("expected a match")
	}
	if route.Auth != RouteAuthSession {
		t.Fatalf("matched auth = %q, want session (exact beats wildcard)", route.Auth)
	}
}

type fakePluginSource struct {
	plugin   *LoadedPlugin
	last     *HTTPRequest
	response *HTTPResponse
}

func (f *fakePluginSource) GetPlugin(name string) (*LoadedPlugin, bool) {
	if f.plugin != nil && f.plugin.Manifest.Name == name {
		return f.plugin, true
	}
	return nil, false
}

func (f *fakePluginSource) HandleRequest(name string, req *HTTPRequest) (*HTTPResponse, error) {
	f.last = req
	if f.response != nil {
		return f.response, nil
	}
	return &HTTPResponse{StatusCode: http.StatusOK, Body: `{"ok":true}`}, nil
}

type fakePrincipalSource struct {
	admins map[int]bool
	groups map[int][]int
}

func (f fakePrincipalSource) IsSystemAdmin(userID int) (bool, error) { return f.admins[userID], nil }
func (f fakePrincipalSource) GetGroupMemberships(userID int) ([]int, error) {
	return f.groups[userID], nil
}

type fakeCSRF struct{ allow bool }

func (f fakeCSRF) Allows(*http.Request) bool { return f.allow }

func testRequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("X-Test-User")
		if raw == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		user := &models.User{}
		if raw == "admin" {
			user.ID = 7
			user.IsSystemAdmin = true
		} else {
			_, _ = fmt.Sscanf(raw, "%d", &user.ID)
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextkeys.User, user)))
	})
}

func testRequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _ := r.Context().Value(contextkeys.User).(*models.User)
		if user == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !user.IsSystemAdmin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func newTestRouter(t *testing.T, source PluginSource, csrfAllows bool) *http.ServeMux {
	t.Helper()
	router := NewRouter(source, RouterDeps{
		RequireAuth:  testRequireAuth,
		RequireAdmin: testRequireAdmin,
		CSRF:         fakeCSRF{allow: csrfAllows},
		Permissions: fakePrincipalSource{
			admins: map[int]bool{7: true},
			groups: map[int][]int{3: {1, 2}},
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	mux := http.NewServeMux()
	router.RegisterRoutes(mux)
	return mux
}

func routeAuthPlugin() *LoadedPlugin {
	return &LoadedPlugin{
		Manifest: PluginManifest{Name: "demo"},
		Enabled:  true,
		Routes: []Route{
			{Method: "GET", Path: "/private", Auth: RouteAuthSession},
			{Method: "POST", Path: "/private", Auth: RouteAuthSession},
			{Method: "GET", Path: "/admin-only", Auth: RouteAuthAdmin},
			{Method: "GET", Path: "/public", Auth: RouteAuthNone},
			{Method: "POST", Path: "/public", Auth: RouteAuthNone},
		},
	}
}

func doRequest(mux *http.ServeMux, method, path string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestPluginRouterSessionAuth(t *testing.T) {
	source := &fakePluginSource{plugin: routeAuthPlugin()}
	mux := newTestRouter(t, source, true)

	if rec := doRequest(mux, "GET", "/api/plugins/demo/private", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous session route = %d, want 401", rec.Code)
	}

	rec := doRequest(mux, "GET", "/api/plugins/demo/private", func(r *http.Request) {
		r.Header.Set("X-Test-User", "3")
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated session route = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if source.last == nil || source.last.Principal == nil {
		t.Fatal("plugin did not receive a principal")
	}
	principal := source.last.Principal
	if principal.UserID != 3 || principal.IsAdmin {
		t.Fatalf("principal = %+v, want user 3 non-admin", principal)
	}
	if len(principal.GroupIDs) != 2 || principal.GroupIDs[0] != 1 || principal.GroupIDs[1] != 2 {
		t.Fatalf("principal groups = %v, want [1 2]", principal.GroupIDs)
	}
}

func TestPluginRouterSessionCSRF(t *testing.T) {
	source := &fakePluginSource{plugin: routeAuthPlugin()}
	mux := newTestRouter(t, source, false)

	rec := doRequest(mux, "POST", "/api/plugins/demo/private", func(r *http.Request) {
		r.Header.Set("X-Test-User", "3")
		r.Header.Set("Origin", "https://evil.example")
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site session route = %d, want 403", rec.Code)
	}
}

func TestPluginRouterAdminAuth(t *testing.T) {
	source := &fakePluginSource{plugin: routeAuthPlugin()}
	mux := newTestRouter(t, source, true)

	rec := doRequest(mux, "GET", "/api/plugins/demo/admin-only", func(r *http.Request) {
		r.Header.Set("X-Test-User", "3")
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin admin route = %d, want 403", rec.Code)
	}

	rec = doRequest(mux, "GET", "/api/plugins/demo/admin-only", func(r *http.Request) {
		r.Header.Set("X-Test-User", "admin")
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin route = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if source.last.Principal == nil || !source.last.Principal.IsAdmin {
		t.Fatalf("principal = %+v, want admin", source.last.Principal)
	}
}

func TestPluginRouterPublicRouteStripsCredentials(t *testing.T) {
	source := &fakePluginSource{plugin: routeAuthPlugin()}
	mux := newTestRouter(t, source, false)

	rec := doRequest(mux, "GET", "/api/plugins/demo/public", func(r *http.Request) {
		r.Header.Set("Cookie", "windshift_session=secret")
		r.Header.Set("Authorization", "Bearer secret")
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("public route = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if source.last.Principal != nil {
		t.Fatalf("public route principal = %+v, want nil", source.last.Principal)
	}
	for key := range source.last.Headers {
		if strings.EqualFold(key, "Cookie") || strings.EqualFold(key, "Authorization") {
			t.Fatalf("public route forwarded credential header %q", key)
		}
	}
}

func TestPluginRouterRejectsOversizedBody(t *testing.T) {
	source := &fakePluginSource{plugin: routeAuthPlugin()}
	mux := newTestRouter(t, source, true)

	body := strings.Repeat("a", maxPluginRequestBodyBytes+1024)
	req := httptest.NewRequest("POST", "/api/plugins/demo/public", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body = %d, want 413", rec.Code)
	}
}
