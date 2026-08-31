package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"windshift/internal/models"
)

// TestAgentRunner_WindshiftAgent_EditsAndCommits drives the REAL windshift-agent
// binary (the codehamr fork, WI-204) under the production AgentRunner against a
// fake provider-neutral broker, and asserts it completes a real repo-edit task
// using only its four tools (edit_file + bash), commits locally, and emits
// session_idle — i.e. the JSONL contract and Fantasy's streaming adapter work
// end to end under the unchanged AgentRunner. WI-209/WI-918.
//
// The agent binary lives in the sibling windshift-agent repo, so the path is
// passed in via WINDSHIFT_AGENT_BIN (the test runner builds it first). When
// unset the test skips, keeping the core-tests overlay self-contained.
func TestAgentRunner_WindshiftAgent_EditsAndCommits(t *testing.T) {
	agentBin := os.Getenv("WINDSHIFT_AGENT_BIN")
	if agentBin == "" {
		t.Skip("set WINDSHIFT_AGENT_BIN to the built windshift-agent binary to run this integration test")
	}
	if _, err := os.Stat(agentBin); err != nil {
		t.Fatalf("WINDSHIFT_AGENT_BIN=%q not usable: %v", agentBin, err)
	}

	// A throwaway git repo standing in for the prepared /workspace checkout.
	repo := t.TempDir()
	target := filepath.Join(repo, "hello.txt")
	if err := os.WriteFile(target, []byte("original line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, repo,
		[]string{"init", "-q"},
		[]string{"config", "user.email", "agent@windshift.local"},
		[]string{"config", "user.name", "windshift-agent"},
		[]string{"add", "-A"},
		[]string{"commit", "-q", "-m", "seed"},
	)

	const newContent = "edited by windshift-agent"
	srv := fakeOpenAI(t, repo, target, newContent)
	defer srv.Close()

	r := &AgentRunner{
		Command: agentBin,
		Env: map[string]string{
			"LLM_BASE_URL":              srv.URL,
			"LLM_CONTEXT_WINDOW":        "128000",
			"LLM_MAX_TOKENS":            "16384",
			"WINDSHIFT_AGENT_WORKSPACE": repo,
		},
		InitialPrompt: "Edit hello.txt to say the agent was here, then commit.",
		ShutdownGrace: 5 * time.Second,
	}

	var events []string
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result := r.Run(ctx, RunInput{RunID: 209}, collectEvents(&events))

	if result.Status != models.AgentRunStatusSucceeded {
		t.Fatalf("status: want succeeded, got %q (err=%q)\nevents=%v", result.Status, result.Error, events)
	}

	// The edit actually happened.
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), newContent) {
		t.Errorf("hello.txt was not edited; content=%q", string(got))
	}

	// The agent committed locally (bash tool ran git commit).
	out := gitOut(t, repo, "log", "--oneline")
	if !strings.Contains(out, "agent edit") {
		t.Errorf("expected an 'agent edit' commit; git log=%q", out)
	}

	// The contract's terminal event was emitted.
	if !anyContains(events, `"type":"session_idle"`) {
		t.Errorf("expected a session_idle event; events=%v", events)
	}
}

// fakeOpenAI is a minimal provider-neutral inference server. It walks a
// fixed three-step plan keyed on how many tool results the agent has already
// sent back: edit the file, then commit via bash, then answer without tools.
func fakeOpenAI(t *testing.T, repo, target, newContent string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var body struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		toolResults := 0
		for _, m := range body.Messages {
			if m.Role == "tool" {
				toolResults++
			}
		}

		message := map[string]any{"role": "assistant", "content": ""}
		finishReason := "tool_calls"
		switch toolResults {
		case 0:
			message["tool_calls"] = []any{neutralToolCall("call_edit", "edit_file", map[string]any{
				"path":       target,
				"old_string": "original line",
				"new_string": newContent,
			})}
		case 1:
			message["tool_calls"] = []any{neutralToolCall("call_commit", "bash", map[string]any{
				"cmd": "git -C " + repo + " add -A && git -C " + repo + " commit -q -m 'agent edit'",
			})}
		default:
			message["content"] = "Edited hello.txt and committed."
			finishReason = "stop"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": message, "finish_reason": finishReason}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		})
	}))
}

func neutralToolCall(id, name string, args map[string]any) map[string]any {
	raw, _ := json.Marshal(args)
	return map[string]any{
		"id":       id,
		"type":     "function",
		"function": map[string]any{"name": name, "arguments": string(raw)},
	}
}

func gitRepo(t *testing.T, dir string, cmds ...[]string) {
	t.Helper()
	for _, args := range cmds {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func anyContains(events []string, sub string) bool {
	for _, e := range events {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}

// collectEvents and EventSink are defined in agent_runner_test.go / the
// services package (same package); reused here.
