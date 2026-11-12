//go:build test && !noplugins

package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"windshift/internal/plugins"
)

// allowAllAssets lets the existing inline-serving test exercise the asset
// writer without an auth policy.
func allowAllAssets(next http.Handler) http.Handler { return next }

func requireUserAssets(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-User") == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requireAdminAssets(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-User") != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func limitPublicAssets(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Public-Limit", "applied")
		next.ServeHTTP(w, r)
	})
}

// newPluginManagerWithAssetPolicy loads a plugin whose manifest declares a
// distinct auth policy per asset prefix, plus one undeclared asset.
func newPluginManagerWithAssetPolicy(t *testing.T) *plugins.Manager {
	t.Helper()

	root := t.TempDir()
	pluginDir := filepath.Join(root, "test-plugin")
	assetsDir := filepath.Join(pluginDir, "assets")
	for _, dir := range []string{
		filepath.Join(assetsDir, "public"),
		filepath.Join(assetsDir, "admin"),
		filepath.Join(assetsDir, "internal"),
	} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("create asset dir %s: %v", dir, err)
		}
	}

	manifest := []byte(`{
		"name": "test-plugin",
		"version": "1.0.0",
		"entryPoint": "plugin.wasm",
		"assets": [
			{"path": "public/*", "auth": "none"},
			{"path": "admin/*", "auth": "admin"},
			{"path": "internal/*", "auth": "session"}
		]
	}`)
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.json"), manifest, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.wasm"), minimalPluginModule(), 0o600); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	for _, rel := range []string{"public/app.js", "admin/config.html", "internal/secret.js", "top.html"} {
		if err := os.WriteFile(filepath.Join(assetsDir, rel), []byte("body:"+rel), 0o600); err != nil {
			t.Fatalf("write asset %s: %v", rel, err)
		}
	}

	manager := plugins.NewManager(root, plugins.WithAllowPublicRoutes(true))
	if err := manager.LoadPlugins(); err != nil {
		t.Fatalf("load plugins: %v", err)
	}
	return manager
}

func TestGetAssetEnforcesDeclaredAuth(t *testing.T) {
	manager := newPluginManagerWithAssetPolicy(t)
	t.Cleanup(func() { _ = manager.Close() })

	handler := NewPluginHandler(manager, nil, nil, false)
	handler.SetAssetAuth(requireUserAssets, requireAdminAssets, limitPublicAssets)

	cases := []struct {
		name            string
		asset           string
		user            string
		want            int
		wantPublicLimit bool
	}{
		{name: "public asset allows anonymous", asset: "public/app.js", want: http.StatusOK, wantPublicLimit: true},
		{name: "session asset rejects anonymous", asset: "internal/secret.js", want: http.StatusUnauthorized},
		{name: "session asset allows a user", asset: "internal/secret.js", user: "3", want: http.StatusOK},
		{name: "admin asset rejects a non-admin", asset: "admin/config.html", user: "3", want: http.StatusForbidden},
		{name: "admin asset allows an admin", asset: "admin/config.html", user: "admin", want: http.StatusOK},
		{name: "undeclared asset defaults to session", asset: "top.html", want: http.StatusUnauthorized},
		{name: "undeclared asset allows a user", asset: "top.html", user: "3", want: http.StatusOK},
		{name: "missing asset is not found for an authorized user", asset: "internal/missing.js", user: "3", want: http.StatusNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/plugins/test-plugin/assets/"+tc.asset, nil)
			req.SetPathValue("name", "test-plugin")
			req.SetPathValue("asset", tc.asset)
			if tc.user != "" {
				req.Header.Set("X-Test-User", tc.user)
			}
			rec := httptest.NewRecorder()

			handler.GetAsset(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tc.want, rec.Body.String())
			}
			gotLimit := rec.Header().Get("X-Public-Limit")
			if tc.wantPublicLimit && gotLimit != "applied" {
				t.Errorf("public limiter not applied: X-Public-Limit = %q", gotLimit)
			}
			if !tc.wantPublicLimit && gotLimit != "" {
				t.Errorf("public limiter applied to a non-public asset: %q", gotLimit)
			}
			if tc.want == http.StatusOK && rec.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
				t.Errorf("X-Frame-Options = %q, want SAMEORIGIN", rec.Header().Get("X-Frame-Options"))
			}
		})
	}
}

func TestGetAssetFailsClosedWithoutAuthWiring(t *testing.T) {
	manager := newPluginManagerWithAssetPolicy(t)
	t.Cleanup(func() { _ = manager.Close() })

	handler := NewPluginHandler(manager, nil, nil, false)

	req := httptest.NewRequest(http.MethodGet, "/api/plugins/test-plugin/assets/internal/secret.js", nil)
	req.SetPathValue("name", "test-plugin")
	req.SetPathValue("asset", "internal/secret.js")
	rec := httptest.NewRecorder()

	handler.GetAsset(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 when auth middleware is not configured", rec.Code)
	}
}
