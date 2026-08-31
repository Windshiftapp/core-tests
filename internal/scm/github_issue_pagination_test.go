package scm

import "testing"

func TestFilterGitHubIssuePageUsesRawPageLength(t *testing.T) {
	raw := make([]githubIssue, 100)
	for i := range raw {
		raw[i].Number = i + 1
	}
	raw[0].PullRequest = &struct {
		URL string `json:"url"`
	}{URL: "https://api.github.test/pulls/1"}

	issues, hasNext := filterGitHubIssuePage(raw, 100)
	if got, want := len(issues), 99; got != want {
		t.Fatalf("filtered issue count = %d, want %d", got, want)
	}
	if !hasNext {
		t.Fatal("hasNext = false for a full raw page containing a pull request")
	}
}

func TestFilterGitHubIssuePageStopsAfterShortRawPage(t *testing.T) {
	raw := make([]githubIssue, 99)

	issues, hasNext := filterGitHubIssuePage(raw, 100)
	if got, want := len(issues), 99; got != want {
		t.Fatalf("filtered issue count = %d, want %d", got, want)
	}
	if hasNext {
		t.Fatal("hasNext = true for a short raw page")
	}
}
