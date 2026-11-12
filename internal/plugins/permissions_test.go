//go:build test && !noplugins

package plugins

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"windshift/internal/repository"
	"windshift/internal/testutils"

	"github.com/tetratelabs/wabin/binary"
	"github.com/tetratelabs/wabin/wasm"
)

// hostCallTestModule builds the minimal Extism guest that forwards its input to
// the named host function and exposes the host response as plugin output.
func hostCallTestModule(hostFunction string) []byte {
	i32, i64 := wasm.ValueTypeI32, wasm.ValueTypeI64
	module := &wasm.Module{
		TypeSection: []*wasm.FunctionType{
			{Results: []wasm.ValueType{i64}},
			{Params: []wasm.ValueType{i64}, Results: []wasm.ValueType{i64}},
			{Params: []wasm.ValueType{i64, i64}},
			{Results: []wasm.ValueType{i32}},
		},
		ImportSection: []*wasm.Import{
			{Module: "extism:host/env", Name: "input_offset", Type: wasm.ExternTypeFunc, DescFunc: 0},
			{Module: "extism:host/env", Name: "length", Type: wasm.ExternTypeFunc, DescFunc: 1},
			{Module: "extism:host/env", Name: "output_set", Type: wasm.ExternTypeFunc, DescFunc: 2},
			{Module: "extism:host/user", Name: hostFunction, Type: wasm.ExternTypeFunc, DescFunc: 1},
		},
		FunctionSection: []wasm.Index{3},
		ExportSection: []*wasm.Export{
			{Name: "run", Type: wasm.ExternTypeFunc, Index: 4},
		},
		CodeSection: []*wasm.Code{{
			LocalTypes: []wasm.ValueType{i64},
			Body: []byte{
				wasm.OpcodeCall, 0,
				wasm.OpcodeCall, 3,
				wasm.OpcodeLocalSet, 0,
				wasm.OpcodeLocalGet, 0,
				wasm.OpcodeLocalGet, 0,
				wasm.OpcodeCall, 1,
				wasm.OpcodeCall, 2,
				wasm.OpcodeI32Const, 0,
				wasm.OpcodeEnd,
			},
		}},
	}
	return binary.EncodeModule(module)
}

// logTestModule builds a guest that calls the always-available log host function
// and returns success without setting output.
func logTestModule() []byte {
	i32, i64 := wasm.ValueTypeI32, wasm.ValueTypeI64
	module := &wasm.Module{
		TypeSection: []*wasm.FunctionType{
			{Results: []wasm.ValueType{i64}},
			{Params: []wasm.ValueType{i64}},
			{Results: []wasm.ValueType{i32}},
		},
		ImportSection: []*wasm.Import{
			{Module: "extism:host/env", Name: "input_offset", Type: wasm.ExternTypeFunc, DescFunc: 0},
			{Module: "extism:host/user", Name: "log", Type: wasm.ExternTypeFunc, DescFunc: 1},
		},
		FunctionSection: []wasm.Index{2},
		ExportSection: []*wasm.Export{
			{Name: "run", Type: wasm.ExternTypeFunc, Index: 2},
		},
		CodeSection: []*wasm.Code{{
			LocalTypes: []wasm.ValueType{i64},
			Body: []byte{
				wasm.OpcodeCall, 0,
				wasm.OpcodeLocalSet, 0,
				wasm.OpcodeLocalGet, 0,
				wasm.OpcodeCall, 1,
				wasm.OpcodeI32Const, 0,
				wasm.OpcodeEnd,
			},
		}},
	}
	return binary.EncodeModule(module)
}

// writePluginWithPermissions writes a plugin directory with a manifest that
// declares permissions and the given WASM module.
func writePluginWithPermissions(t *testing.T, root, name string, permissions []string, module []byte) string {
	t.Helper()

	pluginDir := filepath.Join(root, name)
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		t.Fatalf("create plugin dir: %v", err)
	}
	if permissions == nil {
		permissions = []string{}
	}
	permissionsJSON, err := json.Marshal(permissions)
	if err != nil {
		t.Fatalf("marshal permissions: %v", err)
	}
	manifest := []byte(`{"name":"` + name + `","version":"1.0.0","entryPoint":"plugin.wasm","permissions":` + string(permissionsJSON) + `}`)
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.json"), manifest, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.wasm"), module, 0o600); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	return pluginDir
}

func loadPluginWithPermissions(t *testing.T, manager *Manager, root, name string, permissions []string, module []byte) {
	t.Helper()
	if err := manager.LoadPlugin(writePluginWithPermissions(t, root, name, permissions, module)); err != nil {
		t.Fatalf("load plugin %s: %v", name, err)
	}
}

func callHost[T any](t *testing.T, manager *Manager, pluginName string, payload any) T {
	t.Helper()

	output, err := manager.CallPluginFunction(pluginName, "run", payload)
	if err != nil {
		t.Fatalf("call %s: %v", pluginName, err)
	}
	var response T
	if err := json.Unmarshal(output, &response); err != nil {
		t.Fatalf("unmarshal %s response %q: %v", pluginName, output, err)
	}
	return response
}

// TestHostFunctionPermissionMapping guards the catalog: every host function the
// manager registers must either be explicitly ungated or carry a permission, so
// a new host function cannot ship without enforcement.
func TestHostFunctionPermissionMapping(t *testing.T) {
	manager := NewManager(t.TempDir())
	t.Cleanup(func() { _ = manager.Close() })

	for _, hostFunction := range manager.buildHostFunctions() {
		if _, ungated := ungatedHostFunctions[hostFunction.Name]; ungated {
			continue
		}
		if _, mapped := hostFunctionPermissions[hostFunction.Name]; !mapped {
			t.Errorf("host function %q is registered but has no permission mapping", hostFunction.Name)
		}
	}
	for hostFunction, permission := range hostFunctionPermissions {
		if _, known := knownPermissions[permission]; !known {
			t.Errorf("host function %q maps to unknown permission %q", hostFunction, permission)
		}
	}
	for hostFunction := range ungatedHostFunctions {
		if hostFunction != "log" {
			t.Errorf("unexpected ungated host function %q", hostFunction)
		}
	}
}

func TestNormalizePermissions(t *testing.T) {
	tests := []struct {
		name    string
		declared []string
		want    []string
		wantErr string
	}{
		{name: "empty", declared: nil, want: nil},
		{name: "dedupes and sorts", declared: []string{PermissionNetworkEgress, PermissionProcessExec, PermissionNetworkEgress}, want: []string{PermissionNetworkEgress, PermissionProcessExec}},
		{name: "trims whitespace", declared: []string{" process.exec "}, want: []string{PermissionProcessExec}},
		{name: "ignores blank entries", declared: []string{"", "  "}, want: nil},
		{name: "unknown name fails", declared: []string{"process.exe"}, wantErr: `unknown permission "process.exe"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizePermissions(tt.declared)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("normalizePermissions(%v) error = %v, want %q", tt.declared, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizePermissions(%v) unexpected error: %v", tt.declared, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("normalizePermissions(%v) = %v, want %v", tt.declared, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("normalizePermissions(%v) = %v, want %v", tt.declared, got, tt.want)
				}
			}
		})
	}
}

func TestPluginWithoutPermissionsDeniesGatedHostFunctions(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root)
	t.Cleanup(func() { _ = manager.Close() })

	loadPluginWithPermissions(t, manager, root, "denied-cli", nil, hostCallTestModule("cli_exec"))
	loadPluginWithPermissions(t, manager, root, "denied-http", nil, hostCallTestModule("http_fetch"))

	cliResponse := callHost[CLIExecResponse](t, manager, "denied-cli", CLIExecRequest{Command: "echo"})
	if cliResponse.Status != "error" || cliResponse.Error != permissionDeniedMessage(PermissionProcessExec) {
		t.Fatalf("cli_exec = %+v, want permission denied", cliResponse)
	}

	httpResponse := callHost[HTTPFetchResponse](t, manager, "denied-http", HTTPFetchRequest{URL: "http://example.com"})
	if httpResponse.Status != http.StatusForbidden {
		t.Fatalf("http_fetch status = %d, want %d", httpResponse.Status, http.StatusForbidden)
	}
	if string(httpResponse.Body) != permissionDeniedMessage(PermissionNetworkEgress) {
		t.Fatalf("http_fetch body = %q, want permission denied", httpResponse.Body)
	}
}

func TestPluginWithoutPermissionsStillLogs(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root)
	t.Cleanup(func() { _ = manager.Close() })

	loadPluginWithPermissions(t, manager, root, "logger", nil, logTestModule())

	if _, err := manager.CallPluginFunction("logger", "run", LogRequest{Level: "info", Message: "hello"}); err != nil {
		t.Fatalf("log host function failed: %v", err)
	}
}

func TestProcessExecPermissionRunsCLIAndDeniesNetwork(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	settings := repository.NewSystemSettingRepository(tdb.Database)
	if err := settings.Upsert(
		"plugin_cli_exec_enabled", "true", "boolean",
		"Allow plugins to execute CLI commands", "security",
	); err != nil {
		t.Fatalf("enable plugin CLI execution: %v", err)
	}

	root := t.TempDir()
	manager := NewManager(root, WithDatabase(tdb.Database))
	t.Cleanup(func() { _ = manager.Close() })

	loadPluginWithPermissions(t, manager, root, "cli-allowed", []string{PermissionProcessExec}, hostCallTestModule("cli_exec"))
	loadPluginWithPermissions(t, manager, root, "http-denied", []string{PermissionProcessExec}, hostCallTestModule("http_fetch"))

	marker := filepath.Join(t.TempDir(), "marker")
	cliResponse := callHost[CLIExecResponse](t, manager, "cli-allowed", helperProcessRequest(marker))
	if cliResponse.Status != "ok" || cliResponse.ExitCode != 0 {
		t.Fatalf("cli_exec = %+v, want ok", cliResponse)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("cli_exec did not run the command: %v", err)
	}

	httpResponse := callHost[HTTPFetchResponse](t, manager, "http-denied", HTTPFetchRequest{URL: "http://example.com"})
	if httpResponse.Status != http.StatusForbidden || string(httpResponse.Body) != permissionDeniedMessage(PermissionNetworkEgress) {
		t.Fatalf("http_fetch = %+v, want permission denied", httpResponse)
	}
}

func TestUnknownPermissionFailsLoad(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root)
	t.Cleanup(func() { _ = manager.Close() })

	pluginDir := writePluginWithPermissions(t, root, "bad-permission", []string{"process.exe"}, hostCallTestModule("cli_exec"))

	err := manager.LoadPlugin(pluginDir)
	if err == nil {
		t.Fatal("LoadPlugin with unknown permission succeeded, want error")
	}
	if !strings.Contains(err.Error(), `unknown permission "process.exe"`) {
		t.Fatalf("LoadPlugin error = %v, want unknown permission naming process.exe", err)
	}
	if _, exists := manager.GetPlugin("bad-permission"); exists {
		t.Error("plugin with unknown permission was registered")
	}
}

func TestUnlicensedPluginGetsNoPermissions(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root, WithLicenseVerifier(verifierForTest))
	t.Cleanup(func() { _ = manager.Close() })

	loadPluginWithPermissions(t, manager, root, "unlicensed", []string{PermissionProcessExec}, hostCallTestModule("cli_exec"))

	plugin, ok := manager.GetPlugin("unlicensed")
	if !ok {
		t.Fatal("plugin not loaded")
	}
	if len(plugin.Manifest.Permissions) != 1 || plugin.Manifest.Permissions[0] != PermissionProcessExec {
		t.Fatalf("requested permissions = %v, want [%s]", plugin.Manifest.Permissions, PermissionProcessExec)
	}
	if len(plugin.Permissions) != 0 {
		t.Fatalf("effective permissions = %v, want none for an unlicensed plugin", plugin.Permissions)
	}

	response := callHost[CLIExecResponse](t, manager, "unlicensed", CLIExecRequest{Command: "echo"})
	if response.Status != "error" || response.Error != permissionDeniedMessage(PermissionProcessExec) {
		t.Fatalf("cli_exec = %+v, want permission denied", response)
	}
}

func TestLicensedPluginKeepsDeclaredPermissions(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root, WithLicenseVerifier(verifierForTest))
	t.Cleanup(func() { _ = manager.Close() })

	pluginDir := writePluginWithPermissions(t, root, "licensed", []string{PermissionProcessExec}, hostCallTestModule("cli_exec"))
	writeLicense(t, pluginDir, validToken)

	if err := manager.LoadPlugin(pluginDir); err != nil {
		t.Fatalf("load licensed plugin: %v", err)
	}

	plugin, ok := manager.GetPlugin("licensed")
	if !ok {
		t.Fatal("plugin not loaded")
	}
	if len(plugin.Permissions) != 1 || plugin.Permissions[0] != PermissionProcessExec {
		t.Fatalf("effective permissions = %v, want [%s]", plugin.Permissions, PermissionProcessExec)
	}
}
