//go:build test

package handlers

import (
	"context"
	"strings"
	"testing"
	"time"
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

func TestJiraImportContextRespectsConfiguredLimit(t *testing.T) {
	t.Parallel()

	unlimited, cancelUnlimited := (&JiraImportHandler{}).jiraImportContext(context.Background())
	defer cancelUnlimited()
	if _, hasDeadline := unlimited.Deadline(); hasDeadline {
		t.Fatal("default import context has a deadline, want none")
	}

	limited, cancelLimited := (&JiraImportHandler{}).
		WithImportMaxDuration(2 * time.Hour).
		jiraImportContext(context.Background())
	defer cancelLimited()
	deadline, hasDeadline := limited.Deadline()
	if !hasDeadline {
		t.Fatal("configured import limit has no deadline")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > 2*time.Hour {
		t.Fatalf("deadline remaining = %v, want within (0, 2h]", remaining)
	}
}

func TestJiraImportTimedOutOutcome(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		contextKind  string
		progress     ImportProgress
		wantTimedOut bool
		wantStatus   string
		wantPhase    string
		wantDetails  []string
	}{
		{
			name:        "a live context is not a timeout",
			contextKind: "live",
			progress:    ImportProgress{ImportedIssues: 1, TotalIssues: 2},
		},
		{
			name:        "cancellation is not reported as a time limit",
			contextKind: "cancelled",
			progress:    ImportProgress{ImportedIssues: 1, TotalIssues: 2},
		},
		{
			name:         "deadline reports a partial stop with counts and resume guidance",
			contextKind:  "deadline",
			progress:     ImportProgress{ImportedIssues: 1800, FailedIssues: 40, TotalIssues: 2535, ImportedTests: 5, TotalTests: 5},
			wantTimedOut: true,
			wantStatus:   "timed_out",
			wantPhase:    "timed_out",
			wantDetails:  []string{"1805 of 2540", "same workspace", "Jira ID"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			switch test.contextKind {
			case "deadline":
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			status, phase, message, timedOut := jiraImportTimedOutOutcome(ctx, &test.progress)
			if timedOut != test.wantTimedOut {
				t.Fatalf("timedOut = %t, want %t", timedOut, test.wantTimedOut)
			}
			if !timedOut {
				if status != "" || phase != "" || message != "" {
					t.Fatalf("non-timeout outcome = (%q, %q, %q), want empty", status, phase, message)
				}
				return
			}
			if status != test.wantStatus || phase != test.wantPhase {
				t.Fatalf("outcome = (%q, %q), want (%q, %q)", status, phase, test.wantStatus, test.wantPhase)
			}
			for _, detail := range test.wantDetails {
				if !strings.Contains(message, detail) {
					t.Fatalf("message %q does not contain %q", message, detail)
				}
			}
		})
	}
}
