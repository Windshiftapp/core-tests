//go:build test

package handlers

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"windshift/internal/jira"
)

// removedLegacySearchClient simulates Atlassian's removal of the legacy
// GET /rest/api/3/search endpoint (issue #301): every SearchIssues call fails
// with the 410 the cloud returns, while SearchIssuesJQL serves scripted pages.
type removedLegacySearchClient struct {
	jira.Client
	legacyCalls int
	requests    []jira.JQLSearchRequest
	pages       [][]jira.JiraIssue
	searchErr   error
}

func (c *removedLegacySearchClient) SearchIssues(context.Context, jira.SearchOptions) (*jira.SearchResult, error) {
	c.legacyCalls++
	return nil, fmt.Errorf(
		"jira API error: status 410 - {\"errorMessages\":[\"The requested API has been removed. Please migrate to the /rest/api/3/search/jql API.\"],\"errors\":{}}",
	)
}

func (c *removedLegacySearchClient) SearchIssuesJQL(_ context.Context, req jira.JQLSearchRequest) (*jira.JQLSearchResponse, error) {
	if c.searchErr != nil {
		return nil, c.searchErr
	}
	c.requests = append(c.requests, req)
	page := c.pages[len(c.requests)-1]
	nextPageToken := ""
	if len(c.requests) < len(c.pages) {
		nextPageToken = fmt.Sprintf("token-%d", len(c.requests))
	}
	return &jira.JQLSearchResponse{Issues: page, NextPageToken: nextPageToken}, nil
}

func sampleSearchIssues(count, offset int) []jira.JiraIssue {
	issues := make([]jira.JiraIssue, count)
	for i := range issues {
		issues[i].Key = fmt.Sprintf("APP-%d", offset+i+1)
	}
	return issues
}

func TestSampleIssuesFetchesAllPagesThroughEnhancedJQLSearch(t *testing.T) {
	t.Parallel()

	client := &removedLegacySearchClient{
		pages: [][]jira.JiraIssue{
			sampleSearchIssues(100, 0),
			sampleSearchIssues(50, 100),
		},
	}

	got := (&JiraImportHandler{}).sampleIssues(context.Background(), client, "CT", false, 200)

	if client.legacyCalls != 0 {
		t.Fatalf("sampleIssues called the removed legacy search %d times", client.legacyCalls)
	}
	if len(got) != 150 {
		t.Fatalf("sampleIssues returned %d issues, want 150", len(got))
	}
	for i, issue := range got {
		wantKey := fmt.Sprintf("APP-%d", i+1)
		if issue.Key != wantKey {
			t.Fatalf("issue[%d].Key = %q, want %q", i, issue.Key, wantKey)
		}
	}
	wantRequests := []jira.JQLSearchRequest{
		{
			JQL:        `project = "CT" ORDER BY created DESC`,
			MaxResults: 100,
			Fields:     []string{"*all"},
			Expand:     []string{"changelog"},
		},
		{
			JQL:           `project = "CT" ORDER BY created DESC`,
			MaxResults:    100,
			Fields:        []string{"*all"},
			Expand:        []string{"changelog"},
			NextPageToken: "token-1",
		},
	}
	if !reflect.DeepEqual(client.requests, wantRequests) {
		t.Fatalf("sampleIssues sent %+v, want %+v", client.requests, wantRequests)
	}
}

func TestSampleIssuesStopsAtSampleLimitAcrossPages(t *testing.T) {
	t.Parallel()

	client := &removedLegacySearchClient{
		pages: [][]jira.JiraIssue{
			sampleSearchIssues(100, 0),
			sampleSearchIssues(100, 100),
		},
	}

	got := (&JiraImportHandler{}).sampleIssues(context.Background(), client, "CT", false, 150)

	if client.legacyCalls != 0 {
		t.Fatalf("sampleIssues called the removed legacy search %d times", client.legacyCalls)
	}
	if len(got) != 150 {
		t.Fatalf("sampleIssues returned %d issues, want exactly 150", len(got))
	}
	if got[149].Key != "APP-150" {
		t.Fatalf("last sampled issue key = %q, want APP-150", got[149].Key)
	}
	if len(client.requests) != 2 {
		t.Fatalf("sampleIssues sent %d requests, want 2", len(client.requests))
	}
}

func TestSampleIssuesReturnsEmptyWhenEnhancedSearchFails(t *testing.T) {
	t.Parallel()

	client := &removedLegacySearchClient{
		searchErr: errors.New("jira API error: status 400 - invalid JQL"),
	}

	got := (&JiraImportHandler{}).sampleIssues(context.Background(), client, "CT", false, 200)

	if len(got) != 0 {
		t.Fatalf("sampleIssues returned %d issues on search failure, want 0", len(got))
	}
	if client.legacyCalls != 0 {
		t.Fatalf("sampleIssues called the removed legacy search %d times", client.legacyCalls)
	}
}

func TestCollectSampleUsersBuildsSummariesFromEnhancedJQLSearch(t *testing.T) {
	t.Parallel()

	assignee := &jira.JiraUser{
		AccountID:    "acc-1",
		AccountType:  "atlassian",
		EmailAddress: "ada@example.com",
		DisplayName:  "Ada Lovelace",
		AvatarURLs:   map[string]string{"48x48": "https://jira.example.com/avatar/ada.png"},
	}
	reporter := &jira.JiraUser{
		AccountID:    "acc-2",
		AccountType:  "atlassian",
		EmailAddress: "grace@example.com",
		DisplayName:  "Grace Hopper",
	}
	client := &removedLegacySearchClient{
		pages: [][]jira.JiraIssue{{
			{Key: "CT-1", Fields: jira.JiraIssueFields{Assignee: assignee, Reporter: reporter}},
			// Repeat participants across roles and issues must dedupe.
			{Key: "CT-2", Fields: jira.JiraIssueFields{Assignee: reporter, Reporter: assignee}},
			// Missing or blank account IDs are ignored.
			{Key: "CT-3", Fields: jira.JiraIssueFields{Creator: &jira.JiraUser{DisplayName: "No Account"}}},
		}},
	}

	got := (&JiraImportHandler{}).collectSampleUsers(context.Background(), client, []string{"CT"}, false)

	if client.legacyCalls != 0 {
		t.Fatalf("collectSampleUsers called the removed legacy search %d times", client.legacyCalls)
	}
	want := map[string]JiraUserSummary{
		"acc-1": {
			AccountID:   "acc-1",
			AccountType: "atlassian",
			Email:       "ada@example.com",
			DisplayName: "Ada Lovelace",
			AvatarURL:   "https://jira.example.com/avatar/ada.png",
		},
		"acc-2": {
			AccountID:   "acc-2",
			AccountType: "atlassian",
			Email:       "grace@example.com",
			DisplayName: "Grace Hopper",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("collectSampleUsers = %+v, want %+v", got, want)
	}
	wantRequests := []jira.JQLSearchRequest{{
		JQL:        `project = "CT" ORDER BY created DESC`,
		MaxResults: 100,
		Fields:     []string{"assignee", "reporter", "creator"},
	}}
	if !reflect.DeepEqual(client.requests, wantRequests) {
		t.Fatalf("collectSampleUsers sent %+v, want %+v", client.requests, wantRequests)
	}
}

func TestCollectSampleUsersFiltersOpenIssuesWhenRequested(t *testing.T) {
	t.Parallel()

	client := &removedLegacySearchClient{
		pages: [][]jira.JiraIssue{{
			{Key: "CT-1", Fields: jira.JiraIssueFields{
				Assignee: &jira.JiraUser{AccountID: "acc-1", DisplayName: "Ada Lovelace"},
			}},
		}},
	}

	got := (&JiraImportHandler{}).collectSampleUsers(context.Background(), client, []string{"CT"}, true)

	want := map[string]JiraUserSummary{
		"acc-1": {AccountID: "acc-1", DisplayName: "Ada Lovelace"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("collectSampleUsers = %+v, want %+v", got, want)
	}
	wantRequests := []jira.JQLSearchRequest{{
		JQL:        `project = "CT" AND statusCategory != Done ORDER BY created DESC`,
		MaxResults: 100,
		Fields:     []string{"assignee", "reporter", "creator"},
	}}
	if !reflect.DeepEqual(client.requests, wantRequests) {
		t.Fatalf("collectSampleUsers sent %+v, want %+v", client.requests, wantRequests)
	}
}

func TestCollectSampleUsersSkipsProjectWhenSearchFails(t *testing.T) {
	t.Parallel()

	client := &removedLegacySearchClient{
		searchErr: fmt.Errorf(
			"jira API error: status 410 - The requested API has been removed",
		),
	}

	got := (&JiraImportHandler{}).collectSampleUsers(context.Background(), client, []string{"CT"}, false)

	if len(got) != 0 {
		t.Fatalf("collectSampleUsers returned %+v on search failure, want empty", got)
	}
	if client.legacyCalls != 0 {
		t.Fatalf("collectSampleUsers called the removed legacy search %d times", client.legacyCalls)
	}
}
