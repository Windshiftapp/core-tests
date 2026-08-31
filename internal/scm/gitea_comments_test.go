package scm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/models"
)

// newGiteaCommentProvider builds a Gitea provider pointed at a test server and
// returns it already asserted to IssueCommentProvider — the narrow interface the
// "@agent" PR-comment trigger (WI-426) asserts in production. A failure here is
// the exact regression the trigger hit before Gitea implemented the comment
// methods: the assertion silently failed and the poller never fired.
func newGiteaCommentProvider(t *testing.T, baseURL string) IssueCommentProvider {
	t.Helper()
	provider, err := NewGiteaProvider(ProviderConfig{
		ProviderType:        models.SCMProviderTypeGitea,
		AuthMethod:          models.SCMAuthMethodPAT,
		BaseURL:             baseURL,
		PersonalAccessToken: "pat",
	})
	if err != nil {
		t.Fatalf("NewGiteaProvider: %v", err)
	}
	commenter, ok := interface{}(provider).(IssueCommentProvider)
	if !ok {
		t.Fatal("GiteaProvider does not satisfy IssueCommentProvider — @agent PR-comment trigger cannot fire")
	}
	return commenter
}

func TestGiteaListIssueComments_PaginatesAndMaps(t *testing.T) {
	// Two pages: the first full (50) so the client asks for a second, the second
	// short so it stops. Bodies carry the page so we can assert ordering.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/acme/widget/issues/7/comments", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		w.Header().Set("Content-Type", "application/json")
		switch page {
		case "1":
			var out []map[string]any
			for i := 0; i < 50; i++ {
				out = append(out, map[string]any{"id": i + 1, "body": "p1", "user": map[string]any{"id": 9, "login": "alice"}})
			}
			_ = json.NewEncoder(w).Encode(out)
		case "2":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 51, "body": "@agent please continue", "user": map[string]any{"id": 9, "login": "alice"}},
			})
		default:
			t.Errorf("unexpected page %q", page)
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	comments, err := newGiteaCommentProvider(t, srv.URL).ListIssueComments(context.Background(), "acme", "widget", 7)
	if err != nil {
		t.Fatalf("ListIssueComments: %v", err)
	}
	if len(comments) != 51 {
		t.Fatalf("expected 51 comments across two pages, got %d", len(comments))
	}
	last := comments[50]
	if last.ID != 51 || last.Body != "@agent please continue" {
		t.Fatalf("unexpected last comment: id=%d body=%q", last.ID, last.Body)
	}
	if last.User.Username != "alice" || last.User.ID != "9" {
		t.Fatalf("unexpected user mapping: %+v", last.User)
	}
}

func TestGiteaCreateIssueComment_PostsBodyAndReturnsID(t *testing.T) {
	var gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/acme/widget/issues/7/comments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		raw, _ := io.ReadAll(r.Body)
		var payload struct {
			Body string `json:"body"`
		}
		_ = json.Unmarshal(raw, &payload)
		gotBody = payload.Body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 4242, "body": payload.Body})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// A reply-back carries the marker so the poller never re-triggers on it.
	body := models.AgentCommentMarker + "\n\nOn it!"
	id, err := newGiteaCommentProvider(t, srv.URL).CreateIssueComment(context.Background(), "acme", "widget", 7, body)
	if err != nil {
		t.Fatalf("CreateIssueComment: %v", err)
	}
	if id != 4242 {
		t.Fatalf("expected returned id 4242, got %d", id)
	}
	if !strings.Contains(gotBody, models.AgentCommentMarker) {
		t.Fatalf("posted body lost the agent marker: %q", gotBody)
	}
}

func TestGiteaUpdateIssueComment_PatchesByCommentID(t *testing.T) {
	var hit bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/acme/widget/issues/comments/99", func(w http.ResponseWriter, r *http.Request) {
		hit = true
		if r.Method != http.MethodPatch {
			t.Errorf("expected PATCH, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 99, "body": "edited"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := newGiteaCommentProvider(t, srv.URL).UpdateIssueComment(context.Background(), "acme", "widget", 99, "edited"); err != nil {
		t.Fatalf("UpdateIssueComment: %v", err)
	}
	if !hit {
		t.Fatal("expected PATCH to /issues/comments/99")
	}
}
