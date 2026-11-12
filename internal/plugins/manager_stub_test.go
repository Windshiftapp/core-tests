//go:build noplugins

package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStubGetAssetConfinement covers the noplugins build variant: asset paths
// must stay inside the plugin's assets directory and plugin names must be
// validated even though no plugin is ever loaded.
func TestStubGetAssetConfinement(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "test-plugin")
	assetsDir := filepath.Join(pluginDir, "assets")
	if err := os.MkdirAll(assetsDir, 0o750); err != nil {
		t.Fatalf("create assets dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "ok.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	m := NewManager(root)

	data, _, err := m.GetAsset("test-plugin", "ok.txt")
	if err != nil {
		t.Fatalf("read valid asset: %v", err)
	}
	if string(data) != "ok" {
		t.Fatalf("asset = %q, want ok", data)
	}

	if _, _, err := m.GetAsset("test-plugin", "../secret.txt"); err == nil {
		t.Fatal("expected traversal asset path to be rejected")
	}
	if _, _, err := m.GetAsset("../test-plugin", "ok.txt"); err == nil {
		t.Fatal("expected invalid plugin name to be rejected")
	}
	if _, _, err := m.GetAsset("test-plugin", "/etc/passwd"); err == nil {
		t.Fatal("expected absolute asset path to be rejected")
	}
}
