package plugins

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/tetratelabs/wabin/binary"
	"github.com/tetratelabs/wabin/wasm"
)

// minimalCapabilityModule is the smallest wasm the host can compile: one
// exported init function with an empty body.
func minimalCapabilityModule() []byte {
	module := &wasm.Module{
		TypeSection:     []*wasm.FunctionType{{}},
		FunctionSection: []wasm.Index{0},
		ExportSection:   []*wasm.Export{{Name: "init", Type: wasm.ExternTypeFunc, Index: 0}},
		CodeSection:     []*wasm.Code{{Body: []byte{wasm.OpcodeEnd}}},
	}
	return binary.EncodeModule(module)
}

func writeCapabilityPlugin(t *testing.T, root, name string) string {
	t.Helper()
	pluginDir := filepath.Join(root, name)
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		t.Fatalf("create plugin dir: %v", err)
	}
	manifest := []byte(`{
		"name": "` + name + `",
		"version": "1.0.0",
		"entryPoint": "plugin.wasm",
		"capabilities": ["sso.multi-provider"]
	}`)
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.json"), manifest, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.wasm"), minimalCapabilityModule(), 0o600); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	return pluginDir
}

func writeLicense(t *testing.T, pluginDir, token string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(pluginDir, "license.token"), []byte(token), 0o600); err != nil {
		t.Fatalf("write license: %v", err)
	}
}

func loadManager(t *testing.T, root string, verifier LicenseVerifier) *Manager {
	t.Helper()
	manager := NewManager(root, WithLicenseVerifier(verifier))
	t.Cleanup(func() { _ = manager.Close() })
	if err := manager.LoadPlugins(); err != nil {
		t.Fatalf("load plugins: %v", err)
	}
	return manager
}

const verifierInstanceID = "1A2B-3C4D-5E6F-7081"
const validToken = "valid-token"

// verifierForTest accepts exactly validToken — the manager test only cares
// about pass/fail wiring, not the token format (covered by the licensing
// package tests).
func verifierForTest(pluginName string, license []byte) error {
	if string(license) == validToken {
		return nil
	}
	return os.ErrPermission
}

func TestLicenseEnforcement_GatedPluginWithoutLicenseGrantsNoCapability(t *testing.T) {
	root := t.TempDir()
	writeCapabilityPlugin(t, root, "windshift-pro")

	manager := loadManager(t, root, verifierForTest)

	if manager.HasCapability("sso.multi-provider") {
		t.Error("unlicensed plugin granted capability with enforcement on")
	}
	if caps := manager.GetCapabilities(); len(caps) != 0 {
		t.Errorf("GetCapabilities = %v, want empty", caps)
	}
}

func TestLicenseEnforcement_ValidLicenseGrantsCapability(t *testing.T) {
	root := t.TempDir()
	pluginDir := writeCapabilityPlugin(t, root, "windshift-pro")
	writeLicense(t, pluginDir, validToken)

	manager := loadManager(t, root, verifierForTest)

	if !manager.HasCapability("sso.multi-provider") {
		t.Error("licensed plugin did not grant capability")
	}
	caps := manager.GetCapabilities()
	if len(caps) != 1 || caps[0] != "sso.multi-provider" {
		t.Errorf("GetCapabilities = %v, want [sso.multi-provider]", caps)
	}
}

func TestLicenseEnforcement_UnlicensedPluginGrantsCapabilityWhenEnforcementOff(t *testing.T) {
	root := t.TempDir()
	writeCapabilityPlugin(t, root, "windshift-pro")

	manager := loadManager(t, root, nil)

	if !manager.HasCapability("sso.multi-provider") {
		t.Error("enforcement-off manager did not grant capability from unlicensed plugin")
	}
}

func TestUploadWithLicense_StoresTokenAndRejectsInvalid(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root, WithLicenseVerifier(verifierForTest))
	t.Cleanup(func() { _ = manager.Close() })

	// A minimal zip: manifest + wasm. Built in-process to avoid fixture files.
	zipBytes := buildCapabilityPluginZip(t, "windshift-pro", minimalCapabilityModule())

	if err := manager.UploadPluginWithLicense("windshift-pro", zipBytes, []byte("invalid-token")); err == nil {
		t.Error("upload with invalid license succeeded, want rejection")
	}

	if err := manager.UploadPluginWithLicense("windshift-pro", zipBytes, []byte(validToken)); err != nil {
		t.Fatalf("upload with valid license: %v", err)
	}

	stored, err := os.ReadFile(filepath.Join(root, "windshift-pro", "license.token"))
	if err != nil {
		t.Fatalf("license token not stored: %v", err)
	}
	if string(stored) != validToken {
		t.Errorf("stored license = %q, want %q", stored, validToken)
	}
}

func buildCapabilityPluginZip(t *testing.T, name string, wasm []byte) []byte {
	t.Helper()

	files := []struct {
		name string
		data []byte
	}{
		{"manifest.json", []byte(`{
			"name": "` + name + `",
			"version": "1.0.0",
			"entryPoint": "plugin.wasm",
			"capabilities": ["sso.multi-provider"]
		}`)},
		{"plugin.wasm", wasm},
	}

	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, f := range files {
		entry, err := writer.Create(f.name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", f.name, err)
		}
		if _, err := entry.Write(f.data); err != nil {
			t.Fatalf("write zip entry %s: %v", f.name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}
