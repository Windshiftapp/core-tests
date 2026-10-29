//go:build test

package repository

import "testing"

func TestAggregateStatusesPrecedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		statuses []string
		want     string
	}{
		{name: "empty is not_run", statuses: nil, want: "not_run"},
		{name: "any failure fails", statuses: []string{"passed", "failed", "passed"}, want: "failed"},
		{name: "blocked beats skipped", statuses: []string{"skipped", "blocked"}, want: "blocked"},
		{name: "skipped when not failed or blocked", statuses: []string{"skipped", "passed"}, want: "skipped"},
		{name: "pending example keeps aggregate not_run", statuses: []string{"passed", "not_run"}, want: "not_run"},
		{name: "all passed passes", statuses: []string{"passed", "passed"}, want: "passed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := aggregateStatuses(tt.statuses); got != tt.want {
				t.Fatalf("aggregateStatuses(%v) = %q, want %q", tt.statuses, got, tt.want)
			}
		})
	}
}
