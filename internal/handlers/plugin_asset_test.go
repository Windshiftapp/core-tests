//go:build test && !noplugins

package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"windshift/internal/plugins"
)

// TestGetAssetServesFrontendInline is a regression test for the plugin admin
// iframe: the entry document must render in a same-origin frame. A previous
// hardening change set Content-Disposition: attachment, X-Frame-Options: DENY,
// and a default-src 'none'; sandbox CSP, which made the browser download the
// plugin HTML and blocked its scripts and styles.
func TestGetAssetServesFrontendInline(t *testing.T) {
	html := []byte("<!doctype html><html><body>ldap</body></html>")
	js := []byte("export const x = 1;\n")

	manager := newPluginManagerWithAssets(t, map[string][]byte{
		"ldap-config.html": html,
		"index-abc.js":     js,
	})
	defer func() {
		if err := manager.Close(); err != nil {
			t.Errorf("close plugin manager: %v", err)
		}
	}()

	handler := NewPluginHandler(manager, nil, nil, false)

	tests := []struct {
		name        string
		asset       string
		wantType    string
		wantContent []byte
	}{
		{name: "html entry document", asset: "ldap-config.html", wantType: "text/html", wantContent: html},
		{name: "module script", asset: "index-abc.js", wantType: "text/javascript", wantContent: js},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/plugins/test-plugin/assets/"+tt.asset, nil)
			req.SetPathValue("name", "test-plugin")
			req.SetPathValue("asset", tt.asset)
			recorder := httptest.NewRecorder()

			handler.GetAsset(recorder, req)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body: %s", recorder.Code, http.StatusOK, recorder.Body.String())
			}
			if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, tt.wantType) {
				t.Errorf("Content-Type = %q, want prefix %q", got, tt.wantType)
			}
			if got := recorder.Header().Get("Content-Disposition"); got != "" {
				t.Errorf("Content-Disposition = %q, want empty so the asset renders inline", got)
			}
			if got := recorder.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
				t.Errorf("X-Frame-Options = %q, want SAMEORIGIN", got)
			}
			if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
			if got := recorder.Body.Bytes(); string(got) != string(tt.wantContent) {
				t.Errorf("body = %q, want %q", got, tt.wantContent)
			}
		})
	}
}

// newPluginManagerWithAssets builds a loadable plugin containing the given
// files under its assets/ directory.
func newPluginManagerWithAssets(t *testing.T, assets map[string][]byte) *plugins.Manager {
	t.Helper()

	root := t.TempDir()
	pluginDir := filepath.Join(root, "test-plugin")
	assetsDir := filepath.Join(pluginDir, "assets")
	if err := os.MkdirAll(assetsDir, 0o750); err != nil {
		t.Fatalf("create plugin assets directory: %v", err)
	}

	manifest := []byte(`{
  "name": "test-plugin",
  "version": "1.0.0",
  "entryPoint": "plugin.wasm"
}`)
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.json"), manifest, 0o600); err != nil {
		t.Fatalf("write plugin manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.wasm"), minimalPluginModule(), 0o600); err != nil {
		t.Fatalf("write plugin WASM: %v", err)
	}
	for name, content := range assets {
		if err := os.WriteFile(filepath.Join(assetsDir, name), content, 0o600); err != nil {
			t.Fatalf("write asset %s: %v", name, err)
		}
	}

	manager := plugins.NewManager(root)
	if err := manager.LoadPlugins(); err != nil {
		t.Fatalf("load plugins: %v", err)
	}
	return manager
}
