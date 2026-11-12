//go:build test

package wscli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The body shape that started WI-1796's correction loop: backticked code
// spans plus a literal two-char \n. A double-quoted -m loses both to the
// shell, so --file must deliver these bytes verbatim.
const backtickBody = "the current `internal/llm/manager.go` still falls back to `FallbackMaxOutputTokens = 65_536`\nliteral \\n stays two chars"

func TestResolveCommentBody_FileWinsOverMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(path, []byte(backtickBody), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	body, err := resolveCommentBody("ignored -m value", path)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if body != backtickBody {
		t.Errorf("body = %q, want file bytes %q", body, backtickBody)
	}
}

func TestResolveCommentBody_StdinUsedVerbatim(t *testing.T) {
	prev := stdin
	stdin = strings.NewReader(backtickBody)
	t.Cleanup(func() { stdin = prev })

	body, err := resolveCommentBody("", "-")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if body != backtickBody {
		t.Errorf("body = %q, want stdin bytes %q", body, backtickBody)
	}
}

func TestResolveCommentBody_MessageAppliesCLIEscapes(t *testing.T) {
	body, err := resolveCommentBody("line one\\nline two", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if want := "line one\nline two"; body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

func TestResolveCommentBody_RequiresInput(t *testing.T) {
	_, err := resolveCommentBody("", "")
	if err == nil {
		t.Fatal("expected error for missing input")
	}
	if !strings.Contains(err.Error(), "--file") {
		t.Errorf("error = %v, want it to mention --file", err)
	}
}

func TestResolveCommentBody_MissingFile(t *testing.T) {
	_, err := resolveCommentBody("", filepath.Join(t.TempDir(), "nope.md"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "failed to read") {
		t.Errorf("error = %v, want read failure context", err)
	}
}

// Comment add and edit must post --file bytes verbatim: before the flag
// existed the command failed with "unknown flag", and the -m path could
// never carry a body containing backticks safely.
func TestCommentCommands_FilePostsVerbatimBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"add via stdin", []string{"comment", "add", "WI-42", "--file", "-"}},
		{"add via file path", nil}, // path filled in below
		{"edit via stdin", []string{"comment", "edit", "9", "--file", "-"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotMethod string
			var gotBody map[string]string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/rest/api/v2/workspaces/WI/items/42":
					_, _ = w.Write([]byte(`{"data":{"id":77}}`))
				case r.Method == http.MethodPost && r.URL.Path == "/rest/api/v2/items/77/comments",
					r.Method == http.MethodPatch && r.URL.Path == "/rest/api/v2/comments/9":
					_ = json.NewDecoder(r.Body).Decode(&gotBody)
					_, _ = w.Write([]byte(`{"data":{"id":9,"item_id":77,"content":"echo"}}`))
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)

			args := tc.args
			if args == nil {
				path := filepath.Join(t.TempDir(), "body.md")
				if err := os.WriteFile(path, []byte(backtickBody), 0o600); err != nil {
					t.Fatalf("write: %v", err)
				}
				args = []string{"comment", "add", "WI-42", "--file", path}
			}

			var out, errOut strings.Builder
			var stdinReader io.Reader
			if args[len(args)-1] == "-" {
				stdinReader = strings.NewReader(backtickBody)
			}
			code := Run(context.Background(), args, stdinReader, &out, &errOut, map[string]string{
				"WS_URL":   server.URL,
				"WS_TOKEN": "test-token",
			})

			if code != 0 {
				t.Fatalf("exit code = %d, stderr = %q", code, errOut.String())
			}
			wantVerb := "POST"
			if args[1] == "edit" {
				wantVerb = "PATCH"
			}
			if gotMethod != wantVerb {
				t.Errorf("request = %s, want %s", gotMethod, wantVerb)
			}
			if gotBody["content"] != backtickBody {
				t.Errorf("posted content = %q, want verbatim %q", gotBody["content"], backtickBody)
			}
		})
	}
}
