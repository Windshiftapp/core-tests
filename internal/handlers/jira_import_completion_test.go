//go:build test

package handlers

import (
	"strings"
	"testing"
)

func TestJiraImportTerminalOutcome(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		progress    ImportProgress
		wantStatus  string
		wantPhase   string
		wantError   bool
		errorDetail string
	}{
		{
			name:       "empty import completes",
			progress:   ImportProgress{},
			wantStatus: "completed",
			wantPhase:  "completed",
		},
		{
			name:       "successful import completes",
			progress:   ImportProgress{ImportedIssues: 3, ImportedTests: 1},
			wantStatus: "completed",
			wantPhase:  "completed",
		},
		{
			name:       "mixed issue results complete with errors",
			progress:   ImportProgress{ImportedIssues: 2, FailedIssues: 1},
			wantStatus: "completed_with_errors",
			wantPhase:  "completed_with_errors",
		},
		{
			name:       "successful test with project and link failures completes with errors",
			progress:   ImportProgress{ImportedTests: 1, FailedProjects: 1, FailedLinks: 1},
			wantStatus: "completed_with_errors",
			wantPhase:  "completed_with_errors",
		},
		{
			name:        "fully failed issue import fails",
			progress:    ImportProgress{FailedIssues: 4},
			wantStatus:  "failed",
			wantPhase:   "failed",
			wantError:   true,
			errorDetail: "4 issue",
		},
		{
			name:        "project failure without imported rows fails",
			progress:    ImportProgress{FailedProjects: 1},
			wantStatus:  "failed",
			wantPhase:   "failed",
			wantError:   true,
			errorDetail: "1 project",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			status, phase, errorMessage := jiraImportTerminalOutcome(&test.progress)
			if status != test.wantStatus || phase != test.wantPhase {
				t.Fatalf("outcome = (%q, %q), want (%q, %q)", status, phase, test.wantStatus, test.wantPhase)
			}
			if (errorMessage != "") != test.wantError {
				t.Fatalf("error message = %q, want present = %t", errorMessage, test.wantError)
			}
			if test.errorDetail != "" && !strings.Contains(errorMessage, test.errorDetail) {
				t.Fatalf("error message %q does not contain %q", errorMessage, test.errorDetail)
			}
		})
	}
}
