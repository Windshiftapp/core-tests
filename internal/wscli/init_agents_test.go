//go:build test

package wscli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateAgentsFilesAddsClaudeImports(t *testing.T) {
	dir := t.TempDir()
	agentsPath := filepath.Join(dir, "AGENTS.md")
	claudePath := filepath.Join(dir, "CLAUDE.md")

	if err := os.WriteFile(agentsPath, []byte("# Agent rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	claude := "# Claude context\n\nSee [AGENTS.md](./AGENTS.md) and [WINDSHIFT.md](./WINDSHIFT.md).\n"
	if err := os.WriteFile(claudePath, []byte(claude), 0o600); err != nil {
		t.Fatal(err)
	}

	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	previousStdout := stdout
	stdout = &bytes.Buffer{}
	t.Cleanup(func() { stdout = previousStdout })

	updateAgentsFiles()
	updateAgentsFiles()

	agents := readAgentInstructionFile(t, agentsPath)
	if !strings.Contains(agents, "Read [WINDSHIFT.md](./WINDSHIFT.md)") {
		t.Fatalf("AGENTS.md missing Windshift guidance:\n%s", agents)
	}

	updatedClaude := readAgentInstructionFile(t, claudePath)
	for _, importLine := range []string{"@AGENTS.md", "@WINDSHIFT.md"} {
		if count := strings.Count(updatedClaude, importLine); count != 1 {
			t.Errorf("%s count = %d, want 1:\n%s", importLine, count, updatedClaude)
		}
	}
}

func readAgentInstructionFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
