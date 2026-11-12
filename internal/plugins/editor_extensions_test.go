//go:build !noplugins

package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeEditorExtensions(t *testing.T) {
	cases := []struct {
		name     string
		ext      Extension
		wantErr  string
		wantMode string
	}{
		{
			name:     "defaultsModeToToggle",
			ext:      Extension{Point: ExtensionPointEditorMarkdown, ID: "plaintext", Component: "editor.html", EntityTypes: []string{"item", "page"}},
			wantMode: EditorModeToggle,
		},
		{
			name:     "acceptsReplace",
			ext:      Extension{Point: ExtensionPointEditorMarkdown, ID: "plaintext", Component: "editor.html", Mode: EditorModeReplace},
			wantMode: EditorModeReplace,
		},
		{
			name:    "requiresComponent",
			ext:     Extension{Point: ExtensionPointEditorMarkdown, ID: "plaintext"},
			wantErr: "component is required",
		},
		{
			name:    "rejectsUnknownMode",
			ext:     Extension{Point: ExtensionPointEditorMarkdown, ID: "plaintext", Component: "editor.html", Mode: "inline"},
			wantErr: "mode must be",
		},
		{
			name:    "rejectsUnknownEntityType",
			ext:     Extension{Point: ExtensionPointEditorMarkdown, ID: "plaintext", Component: "editor.html", EntityTypes: []string{"asset"}},
			wantErr: "unknown entity type",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			extensions := []Extension{tc.ext}
			err := normalizeEditorExtensions(extensions)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeEditorExtensions() error = %v", err)
			}
			if extensions[0].Mode != tc.wantMode {
				t.Fatalf("mode = %q, want %q", extensions[0].Mode, tc.wantMode)
			}
		})
	}

	t.Run("leavesOtherPointsAlone", func(t *testing.T) {
		extensions := []Extension{{Point: "admin.tab", ID: "react-demo", Component: "index.html"}}
		if err := normalizeEditorExtensions(extensions); err != nil {
			t.Fatalf("normalizeEditorExtensions() error = %v", err)
		}
		if extensions[0].Mode != "" {
			t.Fatalf("non-editor mode = %q, want empty", extensions[0].Mode)
		}
	})
}

func writeEditorPlugin(t *testing.T, root, name, extensionJSON string) string {
	t.Helper()
	pluginDir := filepath.Join(root, name)
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		t.Fatalf("create plugin dir: %v", err)
	}
	manifest := []byte(`{
		"name": "` + name + `",
		"version": "1.0.0",
		"entryPoint": "plugin.wasm",
		"extensions": [` + extensionJSON + `]
	}`)
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.json"), manifest, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.wasm"), minimalCapabilityModule(), 0o600); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	return pluginDir
}

func TestGetExtensionsLicenseGating(t *testing.T) {
	root := t.TempDir()
	writeEditorPlugin(t, root, "windshift-plaintext", `{
		"point": "editor.markdown",
		"id": "plaintext",
		"label": "Plain Markdown",
		"component": "editor.html",
		"entityTypes": ["item", "page"]
	}`)

	// Unlicensed plugins must not advertise an editor provider.
	unlicensed := loadManager(t, root, verifierForTest)
	if got := unlicensed.GetExtensions()[ExtensionPointEditorMarkdown]; len(got) != 0 {
		t.Fatalf("unlicensed provider advertised: %+v", got)
	}

	writeLicense(t, filepath.Join(root, "windshift-plaintext"), validToken)
	licensed := loadManager(t, root, verifierForTest)
	providers := licensed.GetExtensions()[ExtensionPointEditorMarkdown]
	if len(providers) != 1 {
		t.Fatalf("licensed providers = %+v, want one", providers)
	}
	if providers[0].Mode != EditorModeToggle {
		t.Fatalf("mode = %q, want default %q", providers[0].Mode, EditorModeToggle)
	}
	if providers[0].PluginName != "windshift-plaintext" {
		t.Fatalf("pluginName = %q, want windshift-plaintext", providers[0].PluginName)
	}
	if providers[0].EntityTypes[0] != "item" || providers[0].EntityTypes[1] != "page" {
		t.Fatalf("entityTypes = %v, want [item page]", providers[0].EntityTypes)
	}
}

func TestLoadPluginRejectsInvalidEditorExtension(t *testing.T) {
	root := t.TempDir()
	writeEditorPlugin(t, root, "bad-editor", `{
		"point": "editor.markdown",
		"id": "bad",
		"component": "editor.html",
		"mode": "sideways"
	}`)

	manager := NewManager(root)
	t.Cleanup(func() { _ = manager.Close() })
	if err := manager.LoadPlugins(); err != nil {
		t.Fatalf("LoadPlugins() error = %v", err)
	}
	if _, ok := manager.GetExtensions()[ExtensionPointEditorMarkdown]; ok {
		t.Fatal("invalid editor provider was loaded")
	}
}
