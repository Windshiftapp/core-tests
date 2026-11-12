//go:build test && !noplugins

package plugins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	extism "github.com/extism/go-sdk"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

const (
	cliExecHelperProcessEnv = "WINDSHIFT_CLI_EXEC_HELPER_PROCESS"
	cliExecDisabledMessage  = "plugin CLI execution is disabled"
)

func TestCLIExecHostFunctionHonorsRuntimeSecuritySetting(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	manager := NewManager(t.TempDir(), WithDatabase(tdb.Database))
	plugin := newCLIExecTestPlugin(t, manager)
	defer plugin.Close(context.Background())

	disabledMarker := filepath.Join(t.TempDir(), "disabled")
	response := callCLIExecTestPlugin(t, plugin, helperProcessRequest(disabledMarker))
	if response.Status != "error" {
		t.Fatalf("disabled cli_exec status: got %q, want error", response.Status)
	}
	if response.Error != cliExecDisabledMessage {
		t.Fatalf("disabled cli_exec error: got %q, want %q", response.Error, cliExecDisabledMessage)
	}
	if _, err := os.Stat(disabledMarker); !os.IsNotExist(err) {
		t.Fatalf("disabled cli_exec ran the command: marker stat error = %v", err)
	}

	settings := repository.NewSystemSettingRepository(tdb.Database)
	if err := settings.Upsert(
		"plugin_cli_exec_enabled", "true", "boolean",
		"Allow plugins to execute CLI commands", "security",
	); err != nil {
		t.Fatalf("enable plugin CLI execution: %v", err)
	}

	enabledMarker := filepath.Join(t.TempDir(), "enabled")
	response = callCLIExecTestPlugin(t, plugin, helperProcessRequest(enabledMarker))
	if response.Status != "ok" || response.ExitCode != 0 {
		t.Fatalf("enabled cli_exec response: %+v", response)
	}
	if _, err := os.Stat(enabledMarker); err != nil {
		t.Fatalf("enabled cli_exec did not run the command: %v", err)
	}

	if err := settings.Upsert(
		"plugin_cli_exec_enabled", "false", "boolean",
		"Allow plugins to execute CLI commands", "security",
	); err != nil {
		t.Fatalf("disable plugin CLI execution: %v", err)
	}

	disabledAgainMarker := filepath.Join(t.TempDir(), "disabled-again")
	response = callCLIExecTestPlugin(t, plugin, helperProcessRequest(disabledAgainMarker))
	if response.Status != "error" || response.Error != cliExecDisabledMessage {
		t.Fatalf("runtime-disabled cli_exec response: %+v", response)
	}
	if _, err := os.Stat(disabledAgainMarker); !os.IsNotExist(err) {
		t.Fatalf("runtime-disabled cli_exec ran the command: marker stat error = %v", err)
	}
}

func TestCLIExecHostFunctionFailsClosedWithoutSettingsDatabase(t *testing.T) {
	manager := NewManager(t.TempDir())
	plugin := newCLIExecTestPlugin(t, manager)
	defer plugin.Close(context.Background())

	marker := filepath.Join(t.TempDir(), "missing-database")
	response := callCLIExecTestPlugin(t, plugin, helperProcessRequest(marker))
	if response.Status != "error" || response.Error != cliExecDisabledMessage {
		t.Fatalf("cli_exec response without settings database: %+v", response)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("cli_exec without settings database ran the command: marker stat error = %v", err)
	}
}

func TestCLIExecHelperProcess(t *testing.T) {
	if os.Getenv(cliExecHelperProcessEnv) != "1" {
		return
	}

	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) != 2 {
		os.Exit(2)
	}
	if err := os.WriteFile(args[1], []byte("executed"), 0o600); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

func helperProcessRequest(marker string) CLIExecRequest {
	return CLIExecRequest{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestCLIExecHelperProcess", "--", marker},
		Env:     map[string]string{cliExecHelperProcessEnv: "1"},
	}
}

func newCLIExecTestPlugin(t *testing.T, manager *Manager) *extism.Plugin {
	t.Helper()

	manifest := extism.Manifest{Wasm: []extism.Wasm{extism.WasmData{Data: cliExecTestModule()}}}
	plugin, err := extism.NewPlugin(context.Background(), manifest, manager.pluginConfig(), manager.hostFuncs)
	if err != nil {
		t.Fatalf("create cli_exec test plugin: %v", err)
	}
	return plugin
}

func callCLIExecTestPlugin(t *testing.T, plugin *extism.Plugin, request CLIExecRequest) CLIExecResponse {
	t.Helper()

	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal cli_exec request: %v", err)
	}
	// The host function enforces the calling plugin's effective permissions, so
	// the directly-constructed test plugin must carry a plugin context.
	ctx := withPluginPermissions(withPluginName(context.Background(), "cli-exec-test"), []string{PermissionProcessExec})
	exitCode, output, err := plugin.CallWithContext(ctx, "run", payload)
	if err != nil {
		t.Fatalf("call cli_exec test plugin: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("cli_exec test plugin exit code: got %d, want 0", exitCode)
	}

	var response CLIExecResponse
	if err := json.Unmarshal(output, &response); err != nil {
		t.Fatalf("unmarshal cli_exec response %q: %v", output, err)
	}
	return response
}

// cliExecTestModule is retained as a named reference to the shared host-call
// guest so existing cli_exec tests read clearly.
func cliExecTestModule() []byte { return hostCallTestModule("cli_exec") }
