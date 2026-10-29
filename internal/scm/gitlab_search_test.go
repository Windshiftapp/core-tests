//go:build test

package scm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"windshift/internal/models"
)

// newGitLabSearchStub starts a GitLab API stub that records the query of the
// projects list request and replies with a single minimal project.
func newGitLabSearchStub(t *testing.T) (*httptest.Server, func() *url.Values) {
	t.Helper()

	var lastQuery *url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects" {
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		query := r.URL.Query()
		lastQuery = &query
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"id":                 42,
			"name":               "project",
			"path_with_namespace": "group/project",
			"web_url":            serverURL(r),
			"http_url_to_repo":   serverURL(r) + "/group/project.git",
			"default_branch":     "main",
			"visibility":         "private",
			"namespace":          map[string]string{"full_path": "group"},
		}})
	}))
	t.Cleanup(server.Close)
	return server, func() *url.Values { return lastQuery }
}

func serverURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func TestGitLabListRepositoriesSearch(t *testing.T) {
	tests := []struct {
		name            string
		search          string
		wantSearch      string
		wantNamespaces  bool
	}{
		{name: "no search omits search params", search: "", wantSearch: "", wantNamespaces: false},
		{name: "search is sent server-side", search: "windshift", wantSearch: "windshift", wantNamespaces: true},
		{name: "search is not truncated by the provider", search: strings.Repeat("a", 250), wantSearch: strings.Repeat("a", 250), wantNamespaces: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server, lastQuery := newGitLabSearchStub(t)

			provider, err := NewGitLabProvider(ProviderConfig{
				ProviderType:       models.SCMProviderTypeGitLab,
				BaseURL:            server.URL,
				AuthMethod:         models.SCMAuthMethodPAT,
				PersonalAccessToken: "pat-token",
			})
			if err != nil {
				t.Fatalf("create provider: %v", err)
			}

			repos, err := provider.ListRepositories(context.Background(), ListRepositoriesOptions{
				Page:    1,
				PerPage: 30,
				Search:  tt.search,
			})
			if err != nil {
				t.Fatalf("ListRepositories: %v", err)
			}
			if len(repos) != 1 || repos[0].FullName != "group/project" {
				t.Fatalf("unexpected repositories: %+v", repos)
			}

			query := lastQuery()
			if query == nil {
				t.Fatal("projects list request was not recorded")
			}
			if got := query.Get("search"); got != tt.wantSearch {
				t.Errorf("search param = %q, want %q", got, tt.wantSearch)
			}
			if got := query.Get("search_namespaces"); (got == "true") != tt.wantNamespaces {
				t.Errorf("search_namespaces param = %q, want present: %t", got, tt.wantNamespaces)
			}
			if got := query.Get("membership"); got != "true" {
				t.Errorf("membership param = %q, want %q", got, "true")
			}
		})
	}
}
