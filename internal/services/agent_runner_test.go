package services

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"windshift/internal/models"
)

// buildFakeAgent compiles internal/services/testdata/fakeagent into a per-test
// temp directory and returns the absolute path. The tests then point
// AgentRunner.Command at the resulting binary. Per-test compilation keeps
// the tests hermetic; the binary itself takes ~200ms to build and is
// cached by the Go build cache between runs.
func buildFakeAgent(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "fakeagent")
	cmd := exec.Command("go", "build", "-o", out, "./testdata/fakeagent/")
	cmd.Dir = "."
	combined, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build fakeagent: %v\n%s", err, combined)
	}
	return out
}

func collectEvents(emit *[]string) EventSink {
	var mu sync.Mutex
	return func(eventType, payloadJSON string) error {
		mu.Lock()
		defer mu.Unlock()
		*emit = append(*emit, eventType+"|"+payloadJSON)
		return nil
	}
}

func TestAgentRunner_HappyPath(t *testing.T) {
	bin := buildFakeAgent(t)
	r := &AgentRunner{
		Command:       bin,
		Env:           map[string]string{"FAKEAGENT_MODE": "happy"},
		InitialPrompt: "do the thing",
		ShutdownGrace: 2 * time.Second,
	}
	var events []string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := r.Run(ctx, RunInput{RunID: 1}, collectEvents(&events))
	if result.Status != models.AgentRunStatusSucceeded {
		t.Fatalf("status: want succeeded, got %q (err=%q)", result.Status, result.Error)
	}
	// Expect tool_call, tool_result, session_idle on stdout.
	wantTypes := []string{"tool_call", "tool_result", "session_idle"}
	idx := 0
	for _, ev := range events {
		if !strings.HasPrefix(ev, "stdout|") {
			continue
		}
		for _, t := range wantTypes[idx:] {
			if strings.Contains(ev, `"type":"`+t+`"`) {
				idx++
				break
			}
		}
	}
	if idx != len(wantTypes) {
		t.Errorf("expected to observe %v in order; matched %d. events=%v", wantTypes, idx, events)
	}
}

func TestAgentRunner_CancelMidStreamMapsToCanceled(t *testing.T) {
	bin := buildFakeAgent(t)
	r := &AgentRunner{
		Command:       bin,
		Env:           map[string]string{"FAKEAGENT_MODE": "hang"},
		InitialPrompt: "spin",
		ShutdownGrace: 500 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	var events []string
	result := r.Run(ctx, RunInput{RunID: 2}, collectEvents(&events))
	if result.Status != models.AgentRunStatusCanceled {
		t.Fatalf("status: want canceled, got %q (err=%q)", result.Status, result.Error)
	}
}

func TestAgentRunner_NonZeroExitMapsToFailed(t *testing.T) {
	bin := buildFakeAgent(t)
	r := &AgentRunner{
		Command:       bin,
		Env:           map[string]string{"FAKEAGENT_MODE": "crash"},
		InitialPrompt: "kaboom",
		ShutdownGrace: 2 * time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var events []string
	result := r.Run(ctx, RunInput{RunID: 3}, collectEvents(&events))
	if result.Status != models.AgentRunStatusFailed {
		t.Fatalf("status: want failed, got %q (err=%q)", result.Status, result.Error)
	}
	if !strings.Contains(result.Error, "code 7") {
		t.Errorf("expected exit code 7 in error message, got %q", result.Error)
	}
}

func TestAgentRunner_NonJSONLineGetsWrapped(t *testing.T) {
	bin := buildFakeAgent(t)
	r := &AgentRunner{
		Command:       bin,
		Env:           map[string]string{"FAKEAGENT_MODE": "badlines"},
		InitialPrompt: "with garbage",
		ShutdownGrace: 2 * time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var events []string
	result := r.Run(ctx, RunInput{RunID: 4}, collectEvents(&events))
	if result.Status != models.AgentRunStatusSucceeded {
		t.Fatalf("status: want succeeded, got %q", result.Status)
	}
	wrapped := false
	for _, ev := range events {
		if strings.Contains(ev, `"line":"not-json"`) {
			wrapped = true
			break
		}
	}
	if !wrapped {
		t.Errorf("expected non-JSON line to be wrapped as {line:...}; events=%v", events)
	}
}

func TestAgentRunner_MissingCommandFailsFast(t *testing.T) {
	r := &AgentRunner{InitialPrompt: "anything"}
	result := r.Run(context.Background(), RunInput{}, func(string, string) error { return nil })
	if result.Status != models.AgentRunStatusFailed {
		t.Errorf("want failed, got %q", result.Status)
	}
}

func TestAgentRunner_MissingPromptFailsFast(t *testing.T) {
	r := &AgentRunner{Command: "/bin/true"}
	result := r.Run(context.Background(), RunInput{}, func(string, string) error { return nil })
	if result.Status != models.AgentRunStatusFailed {
		t.Errorf("want failed, got %q", result.Status)
	}
}

// --- DockerAgentRunner sandbox argv tests (WI-135) ---
//
// Pure-function tests over buildDockerArgs: no live docker daemon
// required. These pin the security-critical contract from finding 3 of
// the 2026-05-29 coding-agent security review — every spawn must carry
// cap-drop, no-new-privileges, an unprivileged user, read-only root,
// pids/memory/cpu limits, and a non-default network.

func TestDockerAgentRunner_BuildDockerArgs_HardeningDefaults(t *testing.T) {
	r := &DockerAgentRunner{Image: "wi/coding-agent:test"}
	args := r.buildDockerArgs(RunInput{RunID: 42}, "/tmp/envfile.env")

	mustContainArg(t, args,
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--user=1000:1000",
		"--read-only",
		"--network=coding-agent-egress",
		"--pids-limit=512",
		"--memory=4g",
		"--memory-swap=4g",
		"--cpus=2",
	)
	if !containsArgPrefix(args, "--tmpfs=/tmp:") {
		t.Errorf("missing /tmp tmpfs mount: %v", args)
	}
	if !containsArgPrefix(args, "--tmpfs=/home/agent:") {
		t.Errorf("missing /home/agent tmpfs mount: %v", args)
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--tmpfs=") && !strings.Contains(a, "nosuid") {
			t.Errorf("tmpfs mount missing nosuid: %q", a)
		}
	}

	if args[len(args)-1] != "wi/coding-agent:test" {
		t.Errorf("image must be last positional; got %v", args)
	}
}

func TestDockerAgentRunner_BuildDockerArgs_TunableOverrides(t *testing.T) {
	r := &DockerAgentRunner{
		Image:     "wi/coding-agent:test",
		Network:   "bridge",
		PidsLimit: 999,
		Memory:    "8g",
		CPUs:      "4",
	}
	args := r.buildDockerArgs(RunInput{RunID: 7}, "/tmp/envfile.env")

	mustContainArg(t, args,
		"--cap-drop=ALL", // hardening still applied
		"--security-opt=no-new-privileges",
		"--network=bridge",
		"--pids-limit=999",
		"--memory=8g",
		"--memory-swap=8g",
		"--cpus=4",
	)
}

func TestDockerAgentRunner_BuildDockerArgs_WorkspaceMount(t *testing.T) {
	r := &DockerAgentRunner{Image: "wi/coding-agent:test"}
	args := r.buildDockerArgs(RunInput{
		RunID:         3,
		WorkspacePath: "/var/lib/windshift/worktrees/run-3",
	}, "")
	// :Z privately relabels the per-run checkout on SELinux-enforcing hosts;
	// without it the agent container gets EACCES on every read (WI-388).
	want := "/var/lib/windshift/worktrees/run-3:/workspace:Z"
	if !containsArg(args, want) {
		t.Errorf("expected -v %s in args: %v", want, args)
	}
}

// TestDockerAgentRunner_BuildDockerArgs_EnvFileFlag pins the WI-137
// invariant: env values reach the container via --env-file <path>,
// never via -e KEY=VAL (which would leak through /proc/<pid>/cmdline
// and `docker inspect`).
func TestDockerAgentRunner_BuildDockerArgs_EnvFileFlag(t *testing.T) {
	r := &DockerAgentRunner{
		Image: "wi/coding-agent:test",
		Env:   map[string]string{"LLM_MODEL": "claude-opus"},
	}
	args := r.buildDockerArgs(RunInput{
		RunID: 1,
		Env:   map[string]string{"WS_TOKEN": "crw_redacted"},
	}, "/tmp/agent-env.env")

	// --env-file must be present and pointed at the supplied path.
	idx := -1
	for i, a := range args {
		if a == "--env-file" {
			idx = i
			break
		}
	}
	if idx < 0 || idx+1 >= len(args) || args[idx+1] != "/tmp/agent-env.env" {
		t.Fatalf("expected --env-file /tmp/agent-env.env in args: %v", args)
	}

	// And no `-e KEY=VAL` style env entries — that's the regression
	// shape from finding 2.
	for i, a := range args {
		if a == "-e" {
			t.Errorf("unexpected -e in args[%d] (must use --env-file instead): %v", i, args)
		}
	}
}

// --- DockerRunner (action_container / ci_task) sandbox argv tests (WI-238) ---
//
// Security Phase 2: the plain-container path must carry the SAME baseline
// hardening as the coding agent — a job kind cannot opt out of the sandbox.

func TestDockerRunner_BuildDockerArgs_BaselineHardening(t *testing.T) {
	r := &DockerRunner{Image: "admin/ci:test"}
	args := r.buildDockerArgs(RunInput{RunID: 5}, "/tmp/envfile.env")

	mustContainArg(t, args,
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--user=1000:1000",
		"--read-only",
		"--network=coding-agent-egress",
		"--pids-limit=512",
		"--memory=4g",
		"--memory-swap=4g",
		"--cpus=2",
	)
	if !containsArgPrefix(args, "--tmpfs=/tmp:") {
		t.Errorf("missing /tmp tmpfs mount: %v", args)
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--tmpfs=") && !strings.Contains(a, "nosuid") {
			t.Errorf("tmpfs mount missing nosuid: %q", a)
		}
	}
	if args[len(args)-1] != "admin/ci:test" {
		t.Errorf("image must be last positional; got %v", args)
	}
}

// TestDockerRunner_BuildDockerArgs_EnvFileNotArgv pins that container-job
// secrets travel via --env-file, never -e KEY=VAL argv.
func TestDockerRunner_BuildDockerArgs_EnvFileNotArgv(t *testing.T) {
	r := &DockerRunner{Image: "admin/ci:test", Env: map[string]string{"FOO": "bar"}}
	args := r.buildDockerArgs(RunInput{RunID: 1, Env: map[string]string{"WS_TOKEN": "crw_redacted"}}, "/tmp/ci-env.env")

	idx := -1
	for i, a := range args {
		if a == "--env-file" {
			idx = i
			break
		}
	}
	if idx < 0 || idx+1 >= len(args) || args[idx+1] != "/tmp/ci-env.env" {
		t.Fatalf("expected --env-file /tmp/ci-env.env in args: %v", args)
	}
	for i, a := range args {
		if a == "-e" {
			t.Errorf("unexpected -e in args[%d] (must use --env-file): %v", i, args)
		}
	}
}

func TestDockerRunner_BuildDockerArgs_TunableOverrides(t *testing.T) {
	r := &DockerRunner{Image: "admin/ci:test", Network: "bridge", PidsLimit: 64, Memory: "1g", CPUs: "1"}
	args := r.buildDockerArgs(RunInput{RunID: 2}, "/tmp/e.env")
	mustContainArg(t, args,
		"--cap-drop=ALL", // baseline still applied
		"--network=bridge",
		"--pids-limit=64",
		"--memory=1g",
		"--memory-swap=1g",
		"--cpus=1",
	)
}

// TestWriteDockerEnvFile asserts the env file is 0600 and contains
// every merged value, including AGENT_RUN_ID. The on-disk file is
// what carries WS_TOKEN — confirming permissions prevents the
// "world-readable temp file" leak path.
func TestWriteDockerEnvFile(t *testing.T) {
	path, cleanup, err := writeDockerEnvFile(
		map[string]string{"LLM_MODEL": "claude-opus"},
		map[string]string{"WS_TOKEN": "crw_redacted"},
		42,
	)
	if err != nil {
		t.Fatalf("write env file: %v", err)
	}
	t.Cleanup(cleanup)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat env file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("env file perms: want 0600, got %#o", perm)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	bs := string(body)
	for _, want := range []string{"LLM_MODEL=claude-opus", "WS_TOKEN=crw_redacted", "AGENT_RUN_ID=42"} {
		if !strings.Contains(bs, want) {
			t.Errorf("env file missing %q; body=%q", want, bs)
		}
	}
}

// --- arg-matching helpers, namespaced so they don't collide with
// other tests in the package.

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func containsArgPrefix(args []string, prefix string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return false
}

func mustContainArg(t *testing.T, args []string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !containsArg(args, w) {
			t.Errorf("missing flag %q in args: %v", w, args)
		}
	}
}
