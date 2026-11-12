package scm

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"windshift/internal/models"
)

// GitHub's list endpoint (GET /repos/{owner}/{repo}/pulls) omits the `merged`
// field and only returns `merged_at`, so a listed merged PR must still convert
// to IsMerged. Otherwise sync stores it as "closed" and never emits
// scm_pr_merged or runs smart commits (Windshiftapp/core#304).
func TestGitHubListPullRequestsDetectsMergedFromMergedAt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/app/pulls" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `[
			{"id": 1, "number": 11, "state": "closed", "merged_at": "2026-10-01T12:00:00Z", "closed_at": "2026-10-01T12:00:00Z"},
			{"id": 2, "number": 12, "state": "closed", "merged_at": null, "closed_at": "2026-10-01T12:00:00Z"},
			{"id": 3, "number": 13, "state": "open", "merged_at": null}
		]`)
	}))
	defer server.Close()

	provider, err := NewGitHubProvider(ProviderConfig{
		ProviderType:        models.SCMProviderTypeGitHub,
		AuthMethod:          models.SCMAuthMethodPAT,
		BaseURL:             server.URL,
		PersonalAccessToken: "pat-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	provider.httpClient = server.Client()

	prs, err := provider.ListPullRequests(t.Context(), "acme", "app", ListPROptions{State: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 3 {
		t.Fatalf("got %d pull requests, want 3", len(prs))
	}

	merged, closed, open := prs[0], prs[1], prs[2]
	if !merged.IsMerged {
		t.Errorf("PR #%d with merged_at set: IsMerged = false, want true", merged.Number)
	}
	if want := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC); merged.MergedAt == nil || !merged.MergedAt.Equal(want) {
		t.Errorf("PR #%d MergedAt = %v, want %v", merged.Number, merged.MergedAt, want)
	}
	if closed.IsMerged {
		t.Errorf("PR #%d closed without merge: IsMerged = true, want false", closed.Number)
	}
	if open.IsMerged {
		t.Errorf("PR #%d open: IsMerged = true, want false", open.Number)
	}
}

// The single-PR endpoint sends `merged`; it keeps working on its own.
func TestGitHubPullRequestMergedFlagStillHonoured(t *testing.T) {
	pr := githubPullRequest{Number: 21, State: "closed", Merged: true}
	if !pr.toPullRequest().IsMerged {
		t.Fatal("merged=true without merged_at: IsMerged = false, want true")
	}
}
