//go:build test && !noplugins

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"windshift/internal/plugins"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// TestListPluginsReportsRequestedAndEffectivePermissions guards the admin
// surface: an operator must be able to see what a plugin asked for and what it
// actually holds after license gating.
func TestListPluginsReportsRequestedAndEffectivePermissions(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	root := t.TempDir()
	pluginDir := filepath.Join(root, "permission-plugin")
	if err := os.Mkdir(pluginDir, 0o750); err != nil {
		t.Fatalf("create plugin directory: %v", err)
	}
	manifest := []byte(`{
  "name": "permission-plugin",
  "version": "1.0.0",
  "entryPoint": "plugin.wasm",
  "permissions": ["process.exec"]
}`)
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.json"), manifest, 0o600); err != nil {
		t.Fatalf("write plugin manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.wasm"), minimalPluginModule(), 0o600); err != nil {
		t.Fatalf("write plugin WASM: %v", err)
	}

	manager := plugins.NewManager(root)
	if err := manager.LoadPlugins(); err != nil {
		t.Fatalf("load plugins: %v", err)
	}
	defer func() { _ = manager.Close() }()

	handler := NewPluginHandler(manager, repository.NewPluginRegistryRepository(tdb.Database), nil, false)

	req := httptest.NewRequest(http.MethodGet, "/admin/plugins", nil)
	rec := httptest.NewRecorder()
	handler.ListPlugins(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var infos []PluginInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &infos); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("plugin count = %d, want 1", len(infos))
	}
	got := infos[0]
	if len(got.RequestedPermissions) != 1 || got.RequestedPermissions[0] != plugins.PermissionProcessExec {
		t.Fatalf("requested permissions = %v, want [%s]", got.RequestedPermissions, plugins.PermissionProcessExec)
	}
	if len(got.EffectivePermissions) != 1 || got.EffectivePermissions[0] != plugins.PermissionProcessExec {
		t.Fatalf("effective permissions = %v, want [%s]", got.EffectivePermissions, plugins.PermissionProcessExec)
	}
}
