//go:build !noplugins

package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeAssets(t *testing.T) {
	cases := []struct {
		name        string
		assets      []Asset
		allowPublic bool
		wantAuth    []RouteAuth
		wantErr     bool
	}{
		{
			name:     "empty auth defaults to session",
			assets:   []Asset{{Path: "index.html"}},
			wantAuth: []RouteAuth{RouteAuthSession},
		},
		{
			name:        "none preserved when public assets are allowed",
			assets:      []Asset{{Path: "public/*", Auth: RouteAuthNone}},
			allowPublic: true,
			wantAuth:    []RouteAuth{RouteAuthNone},
		},
		{
			name:    "none rejected when public assets are disabled",
			assets:  []Asset{{Path: "public/*", Auth: RouteAuthNone}},
			wantErr: true,
		},
		{
			name:     "admin preserved",
			assets:   []Asset{{Path: "admin/*", Auth: RouteAuthAdmin}},
			wantAuth: []RouteAuth{RouteAuthAdmin},
		},
		{
			name:    "unknown auth fails",
			assets:  []Asset{{Path: "index.html", Auth: "root"}},
			wantErr: true,
		},
		{
			name:    "empty path fails",
			assets:  []Asset{{Path: ""}},
			wantErr: true,
		},
		{
			name:    "traversal path fails",
			assets:  []Asset{{Path: "../secret.txt"}},
			wantErr: true,
		},
		{
			name:    "leading slash fails",
			assets:  []Asset{{Path: "/index.html"}},
			wantErr: true,
		},
		{
			name:    "interior wildcard fails",
			assets:  []Asset{{Path: "pub/*.js"}},
			wantErr: true,
		},
		{
			name:    "two wildcards fail",
			assets:  []Asset{{Path: "pub/*/*"}},
			wantErr: true,
		},
		{
			name: "overlapping entries with different auth fail",
			assets: []Asset{
				{Path: "public/*", Auth: RouteAuthNone},
				{Path: "public/index.html", Auth: RouteAuthSession},
			},
			allowPublic: true,
			wantErr:     true,
		},
		{
			name: "overlapping entries with the same auth are allowed",
			assets: []Asset{
				{Path: "public/*", Auth: RouteAuthNone},
				{Path: "public/index.html", Auth: RouteAuthNone},
			},
			allowPublic: true,
			wantAuth:    []RouteAuth{RouteAuthNone, RouteAuthNone},
		},
		{
			name: "distinct exact paths do not overlap",
			assets: []Asset{
				{Path: "public/index.html", Auth: RouteAuthNone},
				{Path: "admin/index.html", Auth: RouteAuthAdmin},
			},
			allowPublic: true,
			wantAuth:    []RouteAuth{RouteAuthAdmin, RouteAuthNone}, // sorted by path
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeAssets(tc.assets, tc.allowPublic)
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
				t.Fatalf("assets = %d, want %d", len(got), len(tc.wantAuth))
			}
			for i := range got {
				if got[i].Auth != tc.wantAuth[i] {
					t.Fatalf("asset %d auth = %q, want %q", i, got[i].Auth, tc.wantAuth[i])
				}
			}
		})
	}
}

func TestAssetAuthResolvesMostSpecific(t *testing.T) {
	assets := []Asset{
		{Path: "*", Auth: RouteAuthSession},
		{Path: "public/*", Auth: RouteAuthNone},
		{Path: "public/index.html", Auth: RouteAuthAdmin},
	}

	cases := []struct {
		asset string
		want  RouteAuth
	}{
		{"public/app.js", RouteAuthNone},
		{"public/index.html", RouteAuthAdmin},
		{"admin/app.js", RouteAuthSession},
		{"index.html", RouteAuthSession},
	}
	for _, tc := range cases {
		if got := assetAuth(assets, tc.asset); got != tc.want {
			t.Errorf("assetAuth(%q) = %q, want %q", tc.asset, got, tc.want)
		}
	}

	if got := assetAuth(nil, "anything"); got != RouteAuthSession {
		t.Errorf("assetAuth with no policy = %q, want session", got)
	}
}

func writePluginWithManifest(t *testing.T, root, dirName, manifest string) string {
	t.Helper()
	pluginDir := filepath.Join(root, dirName)
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		t.Fatalf("create plugin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.wasm"), minimalCapabilityModule(), 0o600); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return pluginDir
}

func TestLoadPluginGatesPublicAssets(t *testing.T) {
	manifest := `{
		"name": "pub",
		"version": "1.0.0",
		"entryPoint": "plugin.wasm",
		"assets": [{"path": "public/*", "auth": "none"}]
	}`

	t.Run("disabled by default", func(t *testing.T) {
		root := t.TempDir()
		dir := writePluginWithManifest(t, root, "pub", manifest)
		m := NewManager(root)
		t.Cleanup(func() { _ = m.Close() })
		if err := m.LoadPlugin(dir); err == nil {
			t.Fatal("expected load to fail with public assets disabled")
		}
	})

	t.Run("allowed with the option", func(t *testing.T) {
		root := t.TempDir()
		dir := writePluginWithManifest(t, root, "pub", manifest)
		m := NewManager(root, WithAllowPublicRoutes(true))
		t.Cleanup(func() { _ = m.Close() })
		if err := m.LoadPlugin(dir); err != nil {
			t.Fatalf("expected load to succeed: %v", err)
		}
		auth, err := m.GetAssetAuth("pub", "public/app.js")
		if err != nil {
			t.Fatalf("GetAssetAuth: %v", err)
		}
		if auth != RouteAuthNone {
			t.Fatalf("public asset auth = %q, want none", auth)
		}
		auth, err = m.GetAssetAuth("pub", "other/app.js")
		if err != nil {
			t.Fatalf("GetAssetAuth: %v", err)
		}
		if auth != RouteAuthSession {
			t.Fatalf("undeclared asset auth = %q, want session", auth)
		}
	})
}

func TestLoadPluginRejectsAmbiguousAssetAuth(t *testing.T) {
	root := t.TempDir()
	manifest := `{
		"name": "ambiguous",
		"version": "1.0.0",
		"entryPoint": "plugin.wasm",
		"assets": [
			{"path": "public/*", "auth": "none"},
			{"path": "public/index.html", "auth": "session"}
		]
	}`
	dir := writePluginWithManifest(t, root, "ambiguous", manifest)
	m := NewManager(root, WithAllowPublicRoutes(true))
	t.Cleanup(func() { _ = m.Close() })
	if err := m.LoadPlugin(dir); err == nil {
		t.Fatal("expected load to fail for ambiguous asset auth")
	} else if !strings.Contains(err.Error(), "different auth") {
		t.Fatalf("error = %v, want a different-auth diagnostic", err)
	}
}

func TestGetAssetAuthRejectsUnknownAndDisabledPlugin(t *testing.T) {
	root := t.TempDir()
	dir := writePluginWithManifest(t, root, "demo", `{
		"name": "demo",
		"version": "1.0.0",
		"entryPoint": "plugin.wasm"
	}`)
	m := NewManager(root)
	t.Cleanup(func() { _ = m.Close() })
	if err := m.LoadPlugin(dir); err != nil {
		t.Fatalf("load plugin: %v", err)
	}

	if _, err := m.GetAssetAuth("missing", "index.html"); err == nil {
		t.Fatal("expected an error for an unknown plugin")
	}
	if err := m.DisablePlugin("demo"); err != nil {
		t.Fatalf("disable plugin: %v", err)
	}
	if _, err := m.GetAssetAuth("demo", "index.html"); err == nil {
		t.Fatal("expected an error for a disabled plugin")
	}
}
