package services

import "testing"

func TestMilestoneTestStatisticsDerivesRunOutcomeFromResults(t *testing.T) {
	tests := []struct {
		name       string
		createRun  bool
		ended      bool
		statuses   []string
		successful int
		failed     int
		inProgress int
	}{
		{
			name: "no runs",
		},
		{
			name:      "ended run with no results",
			createRun: true,
			ended:     true,
			failed:    1,
		},
		{
			name:       "active run remains in progress despite a failed result",
			createRun:  true,
			statuses:   []string{"failed"},
			inProgress: 1,
		},
		{
			name:       "all passed",
			createRun:  true,
			ended:      true,
			statuses:   []string{"passed", "passed"},
			successful: 1,
		},
		{
			name:      "failed result",
			createRun: true,
			ended:     true,
			statuses:  []string{"passed", "failed"},
			failed:    1,
		},
		{
			name:      "blocked result",
			createRun: true,
			ended:     true,
			statuses:  []string{"passed", "blocked"},
			failed:    1,
		},
		{
			name:       "passed and skipped results",
			createRun:  true,
			ended:      true,
			statuses:   []string{"passed", "skipped"},
			successful: 1,
		},
		{
			name:      "not-run result",
			createRun: true,
			ended:     true,
			statuses:  []string{"passed", "not_run"},
			failed:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := seedPlanningScopeFixture(t)
			var setID int
			if err := fixture.db.QueryRow(`
				SELECT id FROM test_sets
				WHERE workspace_id = ? AND milestone_id = ?
			`, fixture.workspaceA, fixture.milestoneID).Scan(&setID); err != nil {
				t.Fatalf("load test set: %v", err)
			}

			if tt.createRun {
				endedAt := interface{}(nil)
				if tt.ended {
					endedAt = "2026-07-27 12:00:00+00:00"
				}
				runID := planningScopeInsertID(t, fixture.db, `
					INSERT INTO test_runs (
						workspace_id, set_id, name, started_at, ended_at
					) VALUES (?, ?, 'Outcome run', '2026-07-27 10:00:00+00:00', ?)
				`, fixture.workspaceA, setID, endedAt)
				for i, status := range tt.statuses {
					caseID := planningScopeInsertID(t, fixture.db, `
						INSERT INTO test_cases (
							workspace_id, title, name, priority, status
						) VALUES (?, ?, ?, 'medium', 'active')
					`, fixture.workspaceA, "Case", "Case")
					if _, err := fixture.db.ExecWrite(`
						INSERT INTO test_results (
							run_id, test_case_id, status, executed_at
						) VALUES (?, ?, ?, '2026-07-27 11:00:00+00:00')
					`, runID, caseID, status); err != nil {
						t.Fatalf("insert result %d (%s): %v", i, status, err)
					}
				}
			}

			stats, err := NewPlanningService(fixture.db).GetMilestoneTestStatistics(
				fixture.milestoneID,
				[]int{fixture.workspaceA},
			)
			if err != nil {
				t.Fatalf("GetMilestoneTestStatistics: %v", err)
			}
			wantRuns := 0
			if tt.createRun {
				wantRuns = 1
			}
			if stats.TotalTestRuns != wantRuns ||
				stats.SuccessfulTestRuns != tt.successful ||
				stats.FailedTestRuns != tt.failed ||
				stats.InProgressTestRuns != tt.inProgress {
				t.Fatalf(
					"stats = total:%d success:%d failed:%d progress:%d, want %d/%d/%d/%d",
					stats.TotalTestRuns,
					stats.SuccessfulTestRuns,
					stats.FailedTestRuns,
					stats.InProgressTestRuns,
					wantRuns,
					tt.successful,
					tt.failed,
					tt.inProgress,
				)
			}
		})
	}
}
