package scm

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/models"
)

func TestGitHubReviewEventsAndPermission(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls/7/reviews"):
			_, _ = fmt.Fprint(w, `[{"id":11,"body":"@agent address the review","user":{"id":1,"login":"maintainer"},"author_association":"MEMBER","submitted_at":"2026-07-11T10:00:00Z"}]`)
		case strings.HasSuffix(r.URL.Path, "/pulls/7/comments"):
			_, _ = fmt.Fprint(w, `[{"id":12,"body":"@agent fix this line","user":{"id":1,"login":"maintainer"},"author_association":"MEMBER","path":"main.go","line":42,"side":"RIGHT","created_at":"2026-07-11T10:01:00Z","updated_at":"2026-07-11T10:01:00Z"}]`)
		case strings.Contains(r.URL.Path, "/collaborators/maintainer/permission"):
			_, _ = fmt.Fprint(w, `{"permission":"write"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	provider, err := NewGitHubProvider(ProviderConfig{ProviderType: models.SCMProviderTypeGitHub, AuthMethod: models.SCMAuthMethodPAT, BaseURL: server.URL, PersonalAccessToken: "token"})
	if err != nil {
		t.Fatal(err)
	}
	provider.httpClient = server.Client()
	events, err := provider.ListPullRequestReviewEvents(t.Context(), "acme", "repo", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Kind != "review" || events[1].Kind != "review_comment" || events[1].Path != "main.go" || events[1].Line != 42 {
		t.Fatalf("events=%+v", events)
	}
	allowed, err := provider.CanUserWriteRepository(t.Context(), "acme", "repo", "maintainer")
	if err != nil || !allowed {
		t.Fatalf("allowed=%v err=%v", allowed, err)
	}
}

func TestGiteaReviewEventsAndPermission(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls/7/reviews"):
			_, _ = fmt.Fprint(w, `[{"id":21,"body":"@agent please update","user":{"id":1,"login":"maintainer"},"submitted_at":"2026-07-11T10:00:00Z"}]`)
		case strings.HasSuffix(r.URL.Path, "/pulls/7/reviews/21/comments"):
			_, _ = fmt.Fprint(w, `[{"id":22,"body":"@agent inline fix","user":{"id":1,"login":"maintainer"},"path":"main.go","new_position":9,"created_at":"2026-07-11T10:01:00Z","updated_at":"2026-07-11T10:01:00Z"}]`)
		case strings.Contains(r.URL.Path, "/collaborators/maintainer/permission"):
			_, _ = fmt.Fprint(w, `{"permission":"write"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	provider, err := NewGiteaProvider(ProviderConfig{ProviderType: models.SCMProviderTypeGitea, AuthMethod: models.SCMAuthMethodPAT, BaseURL: server.URL, PersonalAccessToken: "token"})
	if err != nil {
		t.Fatal(err)
	}
	provider.httpClient = server.Client()
	events, err := provider.ListPullRequestReviewEvents(t.Context(), "acme", "repo", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Kind != "review" || events[1].Kind != "review_comment" || events[1].Line != 9 {
		t.Fatalf("events=%+v", events)
	}
	allowed, err := provider.CanUserWriteRepository(t.Context(), "acme", "repo", "maintainer")
	if err != nil || !allowed {
		t.Fatalf("allowed=%v err=%v", allowed, err)
	}
}
