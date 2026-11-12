//go:build test

package handlers

import (
	"testing"

	"windshift/internal/jira"
)

// TestJiraTestCasePriorityMapsLowestToLow guards the Jira-to-test-case priority
// mapping so Jira's `Lowest` does not silently fall through to `medium`.
func TestJiraTestCasePriorityMapsLowestToLow(t *testing.T) {
	cases := []struct {
		jiraPriority string
		want         string
	}{
		{"Highest", "critical"},
		{"Blocker", "critical"},
		{"Critical", "critical"},
		{"High", "high"},
		{"Major", "high"},
		{"Medium", "medium"},
		{"Low", "low"},
		{"Lowest", "low"},
		{"Minor", "low"},
		{"Trivial", "low"},
	}

	for _, tc := range cases {
		t.Run(tc.jiraPriority, func(t *testing.T) {
			got := jiraTestCasePriority(&jira.JiraPriority{Name: tc.jiraPriority})
			if got != tc.want {
				t.Fatalf("jiraTestCasePriority(%q) = %q, want %q", tc.jiraPriority, got, tc.want)
			}
		})
	}
}
