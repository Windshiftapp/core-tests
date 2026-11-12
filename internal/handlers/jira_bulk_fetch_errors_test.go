//go:build test

package handlers

import (
	"testing"

	"windshift/internal/jira"
)

// TestRecordJiraBulkFetchResultsCorrelatesPublishedIssueError is the WI-2065
// regression: the published Cloud IssueError shape is {id, errorMessage} with
// the numeric issue id, not the key. The importer must retain the source id and
// message while counting the requested failed issue exactly once.
func TestRecordJiraBulkFetchResultsCorrelatesPublishedIssueError(t *testing.T) {
	t.Parallel()

	plan := &xrayImportPlan{
		keysByProject: map[string]map[string]struct{}{
			"APP": {"APP-2": {}},
		},
	}

	tests := []struct {
		name       string
		requested  []string
		result     *jira.BulkFetchResponse
		keysByID   map[string]string
		wantIssues int
		wantTests  int
	}{
		{
			name:      "numeric id resolves to the requested key",
			requested: []string{"APP-1", "APP-2"},
			result: &jira.BulkFetchResponse{
				Issues:      []jira.JiraIssue{{ID: "10001", Key: "APP-1"}},
				IssueErrors: []jira.BulkFetchError{{ID: "10002", ErrorMessage: "Issue does not exist or you do not have permission to see it."}},
			},
			keysByID:   map[string]string{"10001": "APP-1", "10002": "APP-2"},
			wantIssues: 0,
			wantTests:  1,
		},
		{
			name:      "unexplained omission still counts once",
			requested: []string{"APP-1", "APP-2", "APP-3"},
			result: &jira.BulkFetchResponse{
				Issues:      []jira.JiraIssue{{ID: "10001", Key: "APP-1"}},
				IssueErrors: []jira.BulkFetchError{{ID: "10002", ErrorMessage: "gone"}},
			},
			keysByID:   map[string]string{"10001": "APP-1", "10002": "APP-2"},
			wantIssues: 1,
			wantTests:  1,
		},
		{
			name:      "uncorrelated id is paired with the missing key once",
			requested: []string{"APP-1"},
			result: &jira.BulkFetchResponse{
				IssueErrors: []jira.BulkFetchError{{ID: "99999", ErrorMessage: "deleted"}},
			},
			keysByID:   map[string]string{"10001": "APP-1"},
			wantIssues: 1,
			wantTests:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			progress := &ImportProgress{}
			recordJiraBulkFetchResults("APP", tt.requested, tt.result, tt.keysByID, plan, progress)
			if progress.FailedIssues != tt.wantIssues {
				t.Errorf("failed issues = %d, want %d", progress.FailedIssues, tt.wantIssues)
			}
			if progress.FailedTests != tt.wantTests {
				t.Errorf("failed tests = %d, want %d", progress.FailedTests, tt.wantTests)
			}
		})
	}
}

func TestRecordJiraBulkFetchResultsAccountsForEveryRequestedKey(t *testing.T) {
	t.Parallel()

	plan := &xrayImportPlan{
		keysByProject: map[string]map[string]struct{}{
			"APP": {"APP-2": {}},
		},
	}

	tests := []struct {
		name       string
		requested  []string
		result     *jira.BulkFetchResponse
		wantIssues int
		wantTests  int
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
			name:       "nil response fails the whole batch",
			requested:  []string{"APP-1", "APP-2"},
			result:     nil,
			wantIssues: 1,
			wantTests:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			progress := &ImportProgress{}
			recordJiraBulkFetchResults("APP", tt.requested, tt.result, nil, plan, progress)
			if progress.FailedIssues != tt.wantIssues {
				t.Errorf("failed issues = %d, want %d", progress.FailedIssues, tt.wantIssues)
			}
			if progress.FailedTests != tt.wantTests {
				t.Errorf("failed tests = %d, want %d", progress.FailedTests, tt.wantTests)
			}
		})
	}
}
