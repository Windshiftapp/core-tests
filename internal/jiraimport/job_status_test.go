//go:build test

package jiraimport

import (
	"database/sql"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/testutils"
)

func TestCompletedWithErrorsIsTerminalAndAvailableForReimport(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	insertConnection(t, tdb, "connection-partial-import")
	insertJob(t, tdb, "job-partial-import", "connection-partial-import", "running")
	service := New(tdb.Database, blobstore.Disabled())
	token := "partial-worker"
	if _, err := tdb.ExecWrite(`UPDATE jira_import_jobs SET lease_token = ? WHERE id = ?`, token, "job-partial-import"); err != nil {
		t.Fatalf("set job token: %v", err)
	}

	progress := &Progress{ImportedIssues: 2, FailedIssues: 1}
	if err := service.UpdateStatus(
		"job-partial-import",
		token,
		"completed_with_errors",
		"completed_with_errors",
		progress,
		"",
	); err != nil {
		t.Fatalf("complete import with errors: %v", err)
	}

	status, err := service.GetJobStatus("job-partial-import")
	if err != nil {
		t.Fatalf("get partial import status: %v", err)
	}
	if status.Status != "completed_with_errors" || status.CompletedAt == nil {
		t.Fatalf("partial import status = %#v, want terminal completed_with_errors", status)
	}

	previous, err := service.PreviousImports([]string{"WS"})
	if err != nil {
		t.Fatalf("list previous partial imports: %v", err)
	}
	if len(previous) != 1 || previous[0].JobID != "job-partial-import" {
		t.Fatalf("previous imports = %#v, want partial import job", previous)
	}
}

// TestTimedOutIsTerminalAndAvailableForReimport is the WI-1981 contract: an
// import stopped by the operator's configured time limit must be a terminal
// status with a clear message, release its worker lease, and remain available
// for a re-import that continues from the partial data.
func TestTimedOutIsTerminalAndAvailableForReimport(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	insertConnection(t, tdb, "connection-timeout-import")
	insertJob(t, tdb, "job-timeout-import", "connection-timeout-import", "running")
	service := New(tdb.Database, blobstore.Disabled())
	token := "timeout-worker"
	if _, err := tdb.ExecWrite(`UPDATE jira_import_jobs SET lease_token = ? WHERE id = ?`, token, "job-timeout-import"); err != nil {
		t.Fatalf("set job token: %v", err)
	}

	progress := &Progress{ImportedIssues: 1800, TotalIssues: 2535}
	message := "Import stopped at the configured time limit after importing 1800 of 2535 issues."
	if err := service.UpdateStatus(
		"job-timeout-import",
		token,
		"timed_out",
		"timed_out",
		progress,
		message,
	); err != nil {
		t.Fatalf("record timed out import: %v", err)
	}

	status, err := service.GetJobStatus("job-timeout-import")
	if err != nil {
		t.Fatalf("get timed out status: %v", err)
	}
	if status.Status != "timed_out" || status.Phase != "timed_out" {
		t.Fatalf("status = %q/%q, want timed_out/timed_out", status.Status, status.Phase)
	}
	if status.CompletedAt == nil {
		t.Fatal("timed_out CompletedAt = nil, want terminal timestamp")
	}
	if status.ErrorMessage != message {
		t.Fatalf("error message = %q, want %q", status.ErrorMessage, message)
	}
	if status.Progress == nil || status.Progress["imported_issues"] != float64(1800) || status.Progress["total_issues"] != float64(2535) {
		t.Fatalf("progress = %#v, want the partial counts", status.Progress)
	}

	var leaseToken sql.NullString
	var leaseExpires sql.NullInt64
	if err := tdb.QueryRow(`SELECT lease_token, lease_expires_at FROM jira_import_jobs WHERE id = ?`, "job-timeout-import").Scan(&leaseToken, &leaseExpires); err != nil {
		t.Fatalf("load lease: %v", err)
	}
	if leaseToken.Valid || leaseExpires.Valid {
		t.Fatalf("lease token/expiry = %v/%v, want cleared for retry", leaseToken, leaseExpires)
	}

	previous, err := service.PreviousImports([]string{"WS"})
	if err != nil {
		t.Fatalf("list previous imports: %v", err)
	}
	if len(previous) != 1 || previous[0].JobID != "job-timeout-import" || previous[0].Status != "timed_out" {
		t.Fatalf("previous imports = %#v, want the timed out job", previous)
	}
}
