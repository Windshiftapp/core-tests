//go:build test

package handlers

import (
	"testing"

	"windshift/internal/jira"
)

func TestRecordJiraBulkFetchResultsAccountsForEveryRequestedKey(t *testing.T) {
	t.Parallel()

	plan := &xrayImportPlan{
		keysByProject: map[string]map[string]struct{}{
			"APP": {"APP-2": {}},
		},
	}

	tests := []struct {
		name          string
		requested     []string
		result        *jira.BulkFetchResponse
		wantIssues    int
		wantTests     int
	}{
		{
			name:      "issue errors count as failures",
			requested: []string{"APP-1", "APP-2", "APP-3"},
			result: &jira.BulkFetchResponse{
				IssueErrors: []jira.BulkFetchError{
					{IssueIDOrKey: "APP-1", ErrorMessages: []string{"issue is not visible"}},
					{IssueIDOrKey: "APP-2", ErrorMessages: []string{"issue was deleted"}},
					{IssueIDOrKey: "APP-3", ErrorMessages: []string{"issue is not visible"}},
				},
			},
			wantIssues: 2,
			wantTests:  1,
		},
		{
			name:      "keys missing from issues and issue errors fail",
			requested: []string{"APP-1", "APP-2", "APP-3"},
			result: &jira.BulkFetchResponse{
				Issues: []jira.JiraIssue{{Key: "APP-1"}},
				IssueErrors: []jira.BulkFetchError{
					{IssueIDOrKey: "APP-2", ErrorMessages: []string{"deleted"}},
				},
			},
			// APP-3 is absent from both lists and must still fail.
			wantIssues: 1,
			wantTests:  1,
		},
		{
			name:      "key returned and errored is not double counted",
			requested: []string{"APP-1"},
			result: &jira.BulkFetchResponse{
				Issues:      []jira.JiraIssue{{Key: "APP-1"}},
				IssueErrors: []jira.BulkFetchError{{IssueIDOrKey: "APP-1", ErrorMessages: []string{"stale"}}},
			},
			wantIssues: 0,
			wantTests:  0,
		},
		{
			name:      "nil response fails the whole batch",
			requested: []string{"APP-1", "APP-2"},
			result:    nil,
			wantIssues: 1,
			wantTests:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			progress := &ImportProgress{}
			recordJiraBulkFetchResults("APP", tt.requested, tt.result, plan, progress)
			if progress.FailedIssues != tt.wantIssues {
				t.Errorf("failed issues = %d, want %d", progress.FailedIssues, tt.wantIssues)
			}
			if progress.FailedTests != tt.wantTests {
				t.Errorf("failed tests = %d, want %d", progress.FailedTests, tt.wantTests)
			}
		})
	}
}
