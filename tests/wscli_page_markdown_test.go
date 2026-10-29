package tests

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestWSCLI_PageCreateAndEdit_AcceptUTF8AtMarkdownChunkBoundary(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	_, workspaceKey := CreateTestWorkspace(t, ts, "Page Markdown", shortKey("PMD"))
	content := "*" + strings.Repeat("a", 2046) + "é\n**bold text**\nlast italic line*"

	t.Run("create", func(t *testing.T) {
		out, stderr, code := runWS(t, ts,
			"page", "create",
			"-w", workspaceKey,
			"--title", "Nested emphasis on create",
			"--content", content,
			"-o", "json",
		)
		requireZero(t, code, stderr)
		var created struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(out, &created); err != nil {
			t.Fatalf("decode create output: %v\nraw=%s", err, out)
		}
		if created.Content != content {
			t.Fatalf("created content = %q, want %q", created.Content, content)
		}
	})

	t.Run("edit", func(t *testing.T) {
		out, stderr, code := runWS(t, ts,
			"page", "create",
			"-w", workspaceKey,
			"--title", "Nested emphasis on edit",
			"--content", "Initial body",
			"-o", "json",
		)
		requireZero(t, code, stderr)
		var created struct {
			ID int `json:"id"`
		}
		if err := json.Unmarshal(out, &created); err != nil {
			t.Fatalf("decode seed page output: %v\nraw=%s", err, out)
		}

		out, stderr, code = runWS(t, ts,
			"page", "edit", strconv.Itoa(created.ID),
			"-w", workspaceKey,
			"--content", content,
			"-o", "json",
		)
		requireZero(t, code, stderr)
		var updated struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(out, &updated); err != nil {
			t.Fatalf("decode edit output: %v\nraw=%s", err, out)
		}
		if updated.Content != content {
			t.Fatalf("updated content = %q, want %q", updated.Content, content)
		}
	})
}
