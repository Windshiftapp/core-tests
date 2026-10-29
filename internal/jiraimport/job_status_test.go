//go:build test

package jiraimport

import (
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
