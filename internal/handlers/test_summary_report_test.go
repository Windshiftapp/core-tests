//go:build test

package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestTestSummaryHandler_GetReportsSummary_ReturnsCompleteShape(t *testing.T) {
	const userID = 1
	db := newNegativeTestDB(t)
	seedNegativeTestUser(t, db, userID)
	d := seedTwoTestWorkspaces(t, db)

	if _, err := db.Exec(`DELETE FROM test_results WHERE run_id = ?`, d.run2); err != nil {
		t.Fatalf("delete unrelated result: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM test_runs WHERE id = ?`, d.run2); err != nil {
		t.Fatalf("delete unrelated run: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE test_runs SET started_at = ?, ended_at = ? WHERE id = ?
	`, time.Now().Add(-time.Minute), time.Now(), d.run1); err != nil {
		t.Fatalf("complete report run: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE test_results
		SET status = 'passed', executed_at = ?, actual_result = 'checkout completed'
		WHERE id = ?
	`, time.Now(), d.result1); err != nil {
		t.Fatalf("seed passed result: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO test_cases (id, workspace_id, title, priority)
		VALUES (103, ?, 'Checkout failure', 'high')
	`, d.ws1); err != nil {
		t.Fatalf("seed failed case: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO test_results (id, run_id, test_case_id, status, notes, executed_at)
		VALUES (503, ?, 103, 'failed', 'upstream returned 502', ?)
	`, d.run1, time.Now()); err != nil {
		t.Fatalf("seed failed result: %v", err)
	}

	handler := newTestSummaryHandler(t, db)
	req := authedRequest(
		http.MethodGet,
		fmt.Sprintf("/workspaces/%d/test-reports/summary?days=30", d.ws1),
		userID,
		nil,
	)
	req.SetPathValue("workspaceId", strconv.Itoa(d.ws1))
	rr := httptest.NewRecorder()
	handler.GetReportsSummary(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GetReportsSummary status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var payload struct {
		Overall struct {
			TotalRuns  int     `json:"total_runs"`
			TotalTests int     `json:"total_tests"`
			Passed     int     `json:"passed"`
			Failed     int     `json:"failed"`
			Blocked    int     `json:"blocked"`
			Skipped    int     `json:"skipped"`
			NotRun     int     `json:"not_run"`
			PassRate   float64 `json:"pass_rate"`
		} `json:"overall"`
		Trend []struct {
			Date     string  `json:"date"`
			PassRate float64 `json:"pass_rate"`
			Total    int     `json:"total"`
		} `json:"trend"`
		RecentFailures []struct {
			TestCaseID    int    `json:"test_case_id"`
			TestCaseTitle string `json:"test_case_title"`
			RunID         int    `json:"run_id"`
			RunName       string `json:"run_name"`
		} `json:"recent_failures"`
		RecentBlocked []any `json:"recent_blocked"`
	}
	decodeJSONBody(t, rr, &payload)

	if payload.Overall.TotalRuns != 1 || payload.Overall.TotalTests != 2 {
		t.Fatalf("overall totals = runs:%d tests:%d, want runs:1 tests:2", payload.Overall.TotalRuns, payload.Overall.TotalTests)
	}
	if payload.Overall.Passed != 1 || payload.Overall.Failed != 1 || payload.Overall.Blocked != 0 || payload.Overall.Skipped != 0 || payload.Overall.NotRun != 0 {
		t.Fatalf("overall result counts = %+v, want one passed and one failed", payload.Overall)
	}
	if payload.Overall.PassRate != 50 {
		t.Fatalf("pass_rate = %v, want 50", payload.Overall.PassRate)
	}
	if len(payload.Trend) != 1 || payload.Trend[0].Total != 2 || payload.Trend[0].PassRate != 50 || payload.Trend[0].Date == "" {
		t.Fatalf("trend = %+v, want one 50%% point with two tests", payload.Trend)
	}
	if len(payload.RecentFailures) != 1 {
		t.Fatalf("recent_failures length = %d, want 1", len(payload.RecentFailures))
	}
	failure := payload.RecentFailures[0]
	if failure.TestCaseID != 103 || failure.TestCaseTitle != "Checkout failure" || failure.RunID != d.run1 || failure.RunName != "Run A in W1" {
		t.Fatalf("recent failure = %+v, want failed checkout case and run", failure)
	}
	if len(payload.RecentBlocked) != 0 {
		t.Fatalf("recent_blocked = %+v, want empty", payload.RecentBlocked)
	}
}
