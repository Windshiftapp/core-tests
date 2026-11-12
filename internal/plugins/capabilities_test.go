//go:build !noplugins

package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	extism "github.com/extism/go-sdk"
	"github.com/tetratelabs/wabin/binary"
	"github.com/tetratelabs/wabin/wasm"
)

// minimalMultiCapabilityModule mirrors the minimal loadable plugin wasm: one
// exported init function with an empty body.
func minimalMultiCapabilityModule() []byte {
	module := &wasm.Module{
		TypeSection:     []*wasm.FunctionType{{}},
		FunctionSection: []wasm.Index{0},
		ExportSection:   []*wasm.Export{{Name: "init", Type: wasm.ExternTypeFunc, Index: 0}},
		CodeSection:     []*wasm.Code{{Body: []byte{wasm.OpcodeEnd}}},
	}
	return binary.EncodeModule(module)
}

// One Pro plugin must be able to carry several capabilities at once: the
// Windshift Pro bundle unlocks multiple features with a single upload.
func TestManager_MultipleCapabilitiesFromOnePlugin(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "windshift-pro")
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		t.Fatalf("create plugin dir: %v", err)
	}

	manifest := []byte(`{
		"name": "windshift-pro",
		"version": "1.0.0",
		"entryPoint": "plugin.wasm",
		"capabilities": ["sso.multi-provider", "map.swimlanes", "ldap.configuration"]
	}`)
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.json"), manifest, 0o600); err != nil {
		t.Fatalf("write plugin manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.wasm"), minimalMultiCapabilityModule(), 0o600); err != nil {
		t.Fatalf("write plugin wasm: %v", err)
	}

	manager := NewManager(root)
	t.Cleanup(func() { _ = manager.Close() })
	if err := manager.LoadPlugins(); err != nil {
		t.Fatalf("load plugins: %v", err)
	}

	for _, capability := range []string{"sso.multi-provider", "map.swimlanes", "ldap.configuration"} {
		if !manager.HasCapability(capability) {
			t.Errorf("HasCapability(%q) = false, want true", capability)
		}
	}
	if manager.HasCapability("scim.provisioning") {
		t.Error("HasCapability for undeclared capability = true, want false")
	}

	got := manager.GetCapabilities()
	want := map[string]bool{"sso.multi-provider": false, "map.swimlanes": false, "ldap.configuration": false}
	for _, capability := range got {
		if _, ok := want[capability]; !ok {
			t.Errorf("GetCapabilities contains unexpected %q", capability)
			continue
		}
		want[capability] = true
	}
	for capability, seen := range want {
		if !seen {
			t.Errorf("GetCapabilities missing %q", capability)
		}
	}
	if len(got) != len(want) {
		t.Errorf("GetCapabilities length = %d, want %d (capabilities: %v)", len(got), len(want), got)
	}
}

// TestValidateHostFunctionCatalogRejectsUnmapped pins the fail-closed guard: a
// host function without a permission mapping must fail construction instead of
// shipping callable without a declared permission.
func TestValidateHostFunctionCatalogRejectsUnmapped(t *testing.T) {
	if err := validateHostFunctionCatalog([]extism.HostFunction{{Name: "log"}, {Name: "http_fetch"}}); err != nil {
		t.Fatalf("mapped and ungated host functions rejected: %v", err)
	}
	err := validateHostFunctionCatalog([]extism.HostFunction{{Name: "unguarded_fn"}})
	if err == nil {
		t.Fatal("unmapped host function accepted, want error")
	}
	if !strings.Contains(err.Error(), "unguarded_fn") {
		t.Fatalf("error = %v, want it to name the offending host function", err)
	}
}

// TestNewManagerAcceptsBuiltCatalog is the smoke test for the panic guard: the
// shipped catalog is valid, so constructing a manager must not panic.
func TestNewManagerAcceptsBuiltCatalog(t *testing.T) {
	if manager := NewManager(t.TempDir()); manager == nil {
		t.Fatal("NewManager returned nil")
	}
}
