//go:build test

package handlers

import (
	"context"
	"fmt"
	"testing"

	"windshift/internal/jira"
)

type fakeWorklogClient struct {
	jira.Client
	page     *jira.JiraWorklogContainer
	requests []int
}

func (c *fakeWorklogClient) GetIssueWorklogs(_ context.Context, _ string, startAt, _ int) (*jira.JiraWorklogContainer, error) {
	c.requests = append(c.requests, startAt)
	return c.page, nil
}

func worklogRange(first, last int) []jira.JiraWorklog {
	worklogs := make([]jira.JiraWorklog, 0, last-first+1)
	for id := first; id <= last; id++ {
		worklogs = append(worklogs, jira.JiraWorklog{ID: fmt.Sprintf("%d", id)})
	}
	return worklogs
}

// TestCompleteIssueWorklogsDeduplicatesCompleteDataCenterList is the WI-2072
// regression: Data Center returns the complete worklog list regardless of
// startAt, so merging must not duplicate the rows already embedded on the
// issue.
func TestCompleteIssueWorklogsDeduplicatesCompleteDataCenterList(t *testing.T) {
	embedded := worklogRange(1, 20)
	complete := worklogRange(1, 30)
	client := &fakeWorklogClient{page: &jira.JiraWorklogContainer{
		Worklogs: complete, StartAt: 0, MaxResults: len(complete), Total: len(complete),
	}}
	issue := &jira.JiraIssue{
		Key: "DC-1",
		Fields: jira.JiraIssueFields{
			Worklog: &jira.JiraWorklogContainer{Worklogs: embedded, MaxResults: 20, Total: 30},
		},
	}

	if err := (&JiraImportHandler{}).completeIssueWorklogs(context.Background(), issue, client); err != nil {
		t.Fatalf("completeIssueWorklogs: %v", err)
	}

	got := issue.Fields.Worklog.Worklogs
	if len(got) != 30 {
		t.Fatalf("worklog count = %d, want 30 unique rows", len(got))
	}
	seen := make(map[string]bool, len(got))
	for _, worklog := range got {
		if seen[worklog.ID] {
			t.Fatalf("duplicate worklog id %q in %#v", worklog.ID, got)
		}
		seen[worklog.ID] = true
	}
	for id := 1; id <= 30; id++ {
		if !seen[fmt.Sprintf("%d", id)] {
			t.Fatalf("worklog id %d missing from merged result", id)
		}
	}
}

// TestCompleteIssueWorklogsKeepsCloudPagination proves the Cloud paged path is
// unchanged: a page starting at the requested offset appends its rows once.
func TestCompleteIssueWorklogsKeepsCloudPagination(t *testing.T) {
	embedded := worklogRange(1, 20)
	page := worklogRange(21, 50)
	client := &fakeWorklogClient{page: &jira.JiraWorklogContainer{
		Worklogs: page, StartAt: 20, MaxResults: 30, Total: 50,
	}}
	issue := &jira.JiraIssue{
		Key: "CLOUD-1",
		Fields: jira.JiraIssueFields{
			Worklog: &jira.JiraWorklogContainer{Worklogs: embedded, MaxResults: 20, Total: 50},
		},
	}

	if err := (&JiraImportHandler{}).completeIssueWorklogs(context.Background(), issue, client); err != nil {
		t.Fatalf("completeIssueWorklogs: %v", err)
	}
	if got := len(issue.Fields.Worklog.Worklogs); got != 50 {
		t.Fatalf("worklog count = %d, want 50", got)
	}
	if len(client.requests) != 1 || client.requests[0] != 20 {
		t.Fatalf("worklog requests = %#v, want a single request at startAt 20", client.requests)
	}
}

func TestMergeJiraWorklogsPreservesFirstOccurrence(t *testing.T) {
	t.Parallel()

	existing := []jira.JiraWorklog{{ID: "1", TimeSpentSeconds: 60}, {ID: "2", TimeSpentSeconds: 120}}
	incoming := []jira.JiraWorklog{{ID: "2", TimeSpentSeconds: 999}, {ID: "3", TimeSpentSeconds: 30}}
	merged := mergeJiraWorklogs(existing, incoming)

	if len(merged) != 3 {
		t.Fatalf("merged count = %d, want 3", len(merged))
	}
	if merged[1].TimeSpentSeconds != 120 {
		t.Fatalf("duplicate worklog replaced the first occurrence: %#v", merged)
	}
	if merged[2].ID != "3" {
		t.Fatalf("merged = %#v, want id 3 appended", merged)
	}
}
