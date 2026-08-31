package wscli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePageInput_TitleFlagWins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Heading\n\nbody"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	title, content, err := resolvePageInput("Explicit", "", path)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if title != "Explicit" {
		t.Errorf("title: want Explicit, got %q", title)
	}
	if content != "# Heading\n\nbody" {
		t.Errorf("content not passed through: %q", content)
	}
}

func TestResolvePageInput_FallsBackToFirstH1(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Heading\n\nbody"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	title, _, err := resolvePageInput("", "", path)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if title != "Heading" {
		t.Errorf("title: want Heading, got %q", title)
	}
}

func TestResolvePageInput_FallsBackToFilename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "onboarding-guide.md")
	if err := os.WriteFile(path, []byte("no heading here"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	title, _, err := resolvePageInput("", "", path)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if title != "onboarding-guide" {
		t.Errorf("title: want onboarding-guide, got %q", title)
	}
}

func TestResolvePageInput_InlineContent(t *testing.T) {
	title, content, err := resolvePageInput("Notes", "Initial body", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if title != "Notes" {
		t.Errorf("title: want Notes, got %q", title)
	}
	if content != "Initial body" {
		t.Errorf("content: want Initial body, got %q", content)
	}
}

func TestResolvePageInput_NoInput(t *testing.T) {
	title, content, err := resolvePageInput("", "", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if title != "" || content != "" {
		t.Errorf("expected empty (title=%q, content=%q)", title, content)
	}
}

func TestResolvePageInput_H1RegexSkipsLowerHeadings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	// First heading is H2 — must NOT be picked up as the title.
	body := "## Subsection\n\n## Another\n\n# Real Title\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	title, _, err := resolvePageInput("", "", path)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if title != "Real Title" {
		t.Errorf("title: want 'Real Title', got %q", title)
	}
}

// Regression for bug-hunt finding #5: `ws page edit 42 --content ""` must
// be able to intentionally blank a page body. Before the fix, the command
// detected "was --content set?" by checking pageEditContent != "", so an
// explicit empty string short-circuited as "no content supplied" and the
// PUT body never carried Content=&"".
func TestPageEditCommand_EmptyContentClearsBody(t *testing.T) {
	var gotBody PageUpdateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/rest/api/v1/workspaces/42/pages/7"):
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			_ = json.NewEncoder(w).Encode(Page{ID: 7, Title: "Onboarding"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	env := map[string]string{
		"WS_URL":       srv.URL,
		"WS_TOKEN":     "ws_test_token",
		"WS_WORKSPACE": "42",
	}
	var out, errBuf bytes.Buffer
	code := Run(context.Background(), []string{"page", "edit", "7", "--content", ""}, nil, &out, &errBuf, env)
	if code != 0 {
		t.Fatalf("Run exited with code %d; stderr=%s", code, errBuf.String())
	}
	if gotBody.Content == nil {
		t.Fatalf("PUT body did not include Content; got %+v", gotBody)
	}
	if *gotBody.Content != "" {
		t.Errorf("PUT body Content: want \"\", got %q", *gotBody.Content)
	}
}

func TestPageMoveCommand_ResolvesDestinationWorkspace(t *testing.T) {
	var gotBody PageMoveRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/v1/workspaces":
			_ = json.NewEncoder(w).Encode(PaginatedResponse[Workspace]{Data: []Workspace{{ID: 77, Key: "DEST"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/v1/workspaces/42/pages/7/move":
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			_ = json.NewEncoder(w).Encode(Page{ID: 7, WorkspaceID: 77, Path: "/7/"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	env := map[string]string{
		"WS_URL":       srv.URL,
		"WS_TOKEN":     "ws_test_token",
		"WS_WORKSPACE": "42",
	}
	var out, errBuf bytes.Buffer
	code := Run(context.Background(), []string{"page", "move", "7", "--root", "--to-workspace", "DEST"}, nil, &out, &errBuf, env)
	if code != 0 {
		t.Fatalf("Run exited with code %d; stderr=%s", code, errBuf.String())
	}
	if gotBody.ParentID != nil {
		t.Fatalf("parent_id: got %+v, want nil", gotBody.ParentID)
	}
	if gotBody.DestinationWorkspaceID == nil || *gotBody.DestinationWorkspaceID != 77 {
		t.Fatalf("destination_workspace_id: got %+v, want 77", gotBody.DestinationWorkspaceID)
	}
}
