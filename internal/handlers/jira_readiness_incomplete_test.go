//go:build test

package handlers

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"windshift/internal/jira"
)

// readinessPageFailingClient serves full pages and then fails the requested
// page, so readiness sampling cannot silently treat a rejected page as a
// complete sample.
type readinessPageFailingClient struct {
	jira.Client
	pageSize  int
	successes int
	calls     int
}

func (c *readinessPageFailingClient) GetProject(context.Context, string) (*jira.JiraProject, error) {
	return &jira.JiraProject{ID: "P1", Key: "CT", Name: "CT", Style: "classic"}, nil
}

func (c *readinessPageFailingClient) GetProjectFields(context.Context, []string) ([]jira.JiraCustomField, error) {
	return nil, nil
}

func (c *readinessPageFailingClient) GetIssueCount(context.Context, string, bool) (int, error) {
	return 250, nil
}

func (c *readinessPageFailingClient) ListBoards(context.Context, string) (*jira.BoardListResult, error) {
	return &jira.BoardListResult{}, nil
}

func (c *readinessPageFailingClient) GetProjectIssueTypes(context.Context, string) ([]jira.JiraIssueType, error) {
	return nil, errors.New("issue types unavailable")
}

func (c *readinessPageFailingClient) SearchIssuesJQL(_ context.Context, _ jira.JQLSearchRequest) (*jira.JQLSearchResponse, error) {
	c.calls++
	if c.calls > c.successes {
		return nil, errors.New("jira API error: status 400 - invalid JQL")
	}
	issues := make([]jira.JiraIssue, c.pageSize)
	for i := range issues {
		issues[i].Key = fmt.Sprintf("CT-%d", (c.calls-1)*c.pageSize+i+1)
	}
	return &jira.JQLSearchResponse{Issues: issues, NextPageToken: "next-page"}, nil
}

// TestReadinessMarksRejectedSamplePagesIncomplete is the WI-2063 regression:
// a rejected first or later sampling page must produce an explicit incomplete
// assessment with exact sampled counts instead of an apparently complete score.
func TestReadinessMarksRejectedSamplePagesIncomplete(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		successes   int
		wantSampled int
	}{
		{name: "rejected first page", successes: 0, wantSampled: 0},
		{name: "rejected later page", successes: 1, wantSampled: 100},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			client := &readinessPageFailingClient{pageSize: 100, successes: test.successes}
			report := (&JiraImportHandler{}).analyzeReadiness(context.Background(), client, JiraReadinessRequest{
				ProjectKeys: []string{"CT"},
				SampleSize:  200,
			})

			if report.AssessmentComplete {
				t.Fatal("assessment marked complete after a rejected sampling page")
			}
			if len(report.Projects) != 1 {
				t.Fatalf("projects = %d, want 1", len(report.Projects))
			}
			project := report.Projects[0]
			if project.AssessmentComplete {
				t.Fatal("project assessment marked complete after a rejected sampling page")
			}
			if project.SampledIssues != test.wantSampled || report.SampledIssues != test.wantSampled {
				t.Fatalf("sampled issues = project %d report %d, want %d",
					project.SampledIssues, report.SampledIssues, test.wantSampled)
			}
			if project.AssessmentError == "" {
				t.Fatal("assessment error is empty, want the upstream failure")
			}
			found := false
			for _, finding := range project.Findings {
				if finding.Category == "assessment" {
					found = true
					if finding.Severity != jira.SeverityBlocked {
						t.Fatalf("assessment finding severity = %q, want blocked", finding.Severity)
					}
				}
			}
			if !found {
				t.Fatalf("assessment finding missing from %#v", project.Findings)
			}
		})
	}
}

// TestReadinessCompleteSampleIsMarkedComplete guards the success path.
func TestReadinessCompleteSampleIsMarkedComplete(t *testing.T) {
	t.Parallel()

	client := &readinessPageFailingClient{pageSize: 100, successes: 5}
	report := (&JiraImportHandler{}).analyzeReadiness(context.Background(), client, JiraReadinessRequest{
		ProjectKeys: []string{"CT"},
		SampleSize:  200,
	})

	if !report.AssessmentComplete || !report.Projects[0].AssessmentComplete {
		t.Fatalf("assessment completeness = report %t project %t, want true/true",
			report.AssessmentComplete, report.Projects[0].AssessmentComplete)
	}
	if report.SampledIssues != 200 {
		t.Fatalf("sampled issues = %d, want 200", report.SampledIssues)
	}
}
