// Fakeagent is a deterministic stand-in for windshift-agent in RPC mode that
// the AgentRunner unit tests shell out to. It reads JSONL commands on stdin
// and emits scripted NDJSON events on stdout. Behavior is selected by the
// FAKEAGENT_MODE env var so the same binary serves every scenario.
//
// Modes:
//
//	"happy"  (default) — echo two stdout events for the first prompt,
//	                     then emit one {"type":"session_idle"}, wait for
//	                     {"type":"abort"} on stdin, and exit 0.
//	"crash"            — emit one event and exit with code 7.
//	"hang"             — emit one event then ignore stdin entirely; the
//	                     orchestrator's grace timer is what unsticks it.
//	"badlines"         — emit a non-JSON line followed by a JSON idle so
//	                     the wrap-as-{"line":...} branch is exercised.
//
// Build via the standard `go test` toolchain — the test compiles this
// binary into t.TempDir() via `go build -o`.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func main() {
	mode := os.Getenv("FAKEAGENT_MODE")
	if mode == "" {
		mode = "happy"
	}
	switch mode {
	case "happy":
		runHappy()
	case "crash":
		runCrash()
	case "hang":
		runHang()
	case "badlines":
		runBadLines()
	case "toolfail":
		runToolFail()
	case "toolrecover":
		runToolRecover()
	default:
		fmt.Fprintf(os.Stderr, "fakeagent: unknown FAKEAGENT_MODE %q\n", mode)
		os.Exit(2)
	}
}

func emit(obj any) {
	b, _ := json.Marshal(obj)
	_, _ = os.Stdout.Write(append(b, '\n'))
}

func emitRaw(s string) {
	_, _ = os.Stdout.Write([]byte(s + "\n"))
}

func waitForAbort() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var cmd map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &cmd); err == nil {
			if t, _ := cmd["type"].(string); t == "abort" {
				return
			}
		}
	}
}

func runHappy() {
	// Wait for the first prompt command — drain stdin until we see one.
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var cmd map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &cmd); err != nil {
			continue
		}
		if t, _ := cmd["type"].(string); t == "prompt" {
			break
		}
	}
	emit(map[string]any{"type": "tool_call", "name": "bash", "input": "ls -la"})
	emit(map[string]any{"type": "tool_result", "ok": true})
	emit(map[string]any{"type": "session_idle"})
	waitForAbort()
	os.Exit(0)
}

func runCrash() {
	emit(map[string]any{"type": "tool_call", "name": "bash"})
	// Give the parent a beat to read the event before we exit non-zero.
	time.Sleep(10 * time.Millisecond)
	os.Exit(7)
}

func runHang() {
	emit(map[string]any{"type": "tool_call", "name": "bash"})
	// Sleep way longer than the test's ShutdownGrace; the orchestrator
	// is expected to time out and kill us.
	time.Sleep(60 * time.Second)
	os.Exit(0)
}

func runBadLines() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var cmd map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &cmd); err != nil {
			continue
		}
		if t, _ := cmd["type"].(string); t == "prompt" {
			break
		}
	}
	emitRaw("not-json")
	emit(map[string]any{"type": "session_idle"})
	waitForAbort()
	os.Exit(0)
}

func waitForPrompt() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var cmd map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &cmd); err != nil {
			continue
		}
		if t, _ := cmd["type"].(string); t == "prompt" {
			return
		}
	}
}

// runToolFail emits a single unrecovered unknown-tool failure — the model
// invoked a tool that does not exist and never recovered. The runner should
// synthesize one "review_flagged" event.
func runToolFail() {
	waitForPrompt()
	emit(map[string]any{"type": "tool_start", "id": 1, "tool": "bogus", "args": map[string]any{}})
	emit(map[string]any{"type": "tool_done", "id": 1, "tool": "bogus", "output": "(unknown tool: bogus)"})
	emit(map[string]any{"type": "finish", "outcome": "completed", "summary": "done"})
	emit(map[string]any{"type": "session_idle"})
	waitForAbort()
	os.Exit(0)
}

// runToolRecover emits an invalid-args failure on bash that is then recovered
// by a later successful bash call. The runner must NOT flag this run.
func runToolRecover() {
	waitForPrompt()
	emit(map[string]any{"type": "tool_done", "id": 1, "tool": "bash", "output": "(tool arguments were not valid JSON: unexpected end of input)"})
	emit(map[string]any{"type": "tool_done", "id": 2, "tool": "bash", "output": "total 0\n"})
	emit(map[string]any{"type": "finish", "outcome": "completed", "summary": "done"})
	emit(map[string]any{"type": "session_idle"})
	waitForAbort()
	os.Exit(0)
}
