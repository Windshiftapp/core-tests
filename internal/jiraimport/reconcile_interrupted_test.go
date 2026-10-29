//go:build test

package jiraimport

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"windshift/internal/blobstore"
	"windshift/internal/testutils"
)

// TestReconcileInterruptedFailsOnlyExpiredWorkers is the WI-1703 core
// contract: queued/running jobs whose worker lease expired (or was never set)
// are failed with provenance, while a job a live worker is still renewing is
// left untouched.
func TestReconcileInterruptedFailsOnlyExpiredWorkers(t *testing.T) {
	t.Parallel()

	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	insertConnection(t, tdb, "connection-reconcile")
	service := New(tdb.Database, blobstore.Disabled())

	now := time.Now().UTC()
	insertJobWithLease := func(id, status string, lease any) {
		t.Helper()
		if _, err := tdb.ExecWrite(`
			INSERT INTO jira_import_jobs (id, connection_id, status, scope, config_json, lease_expires_at)
			VALUES (?, 'connection-reconcile', ?, 'work_items', '{"project_keys":["WS"]}', ?)
		`, id, status, lease); err != nil {
			t.Fatalf("insert job %s: %v", id, err)
		}
	}
	insertJobWithLease("queued-stale", "queued", nil)
	insertJobWithLease("running-expired", "running", now.Add(-time.Minute).Unix())
	insertJobWithLease("running-live", "running", now.Add(time.Minute).Unix())
	insertJobWithLease("completed", "completed", nil)

	count, err := service.ReconcileInterrupted(now)
	if err != nil {
		t.Fatalf("ReconcileInterrupted() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("reconciled count = %d, want 2", count)
	}

	assertJob := func(id, wantStatus, wantError string, wantLeaseNull bool) {
		t.Helper()
		var status, errorMessage string
		var lease *int64
		if err := tdb.QueryRow(`
			SELECT status, COALESCE(error_message, ''), lease_expires_at
			FROM jira_import_jobs WHERE id = ?
		`, id).Scan(&status, &errorMessage, &lease); err != nil {
			t.Fatalf("load job %s: %v", id, err)
		}
		if status != wantStatus {
			t.Errorf("job %s status = %q, want %q", id, status, wantStatus)
		}
		if errorMessage != wantError {
			t.Errorf("job %s error = %q, want %q", id, errorMessage, wantError)
		}
		if wantLeaseNull && lease != nil {
			t.Errorf("job %s lease = %v, want NULL", id, *lease)
		}
		if !wantLeaseNull && lease == nil {
			t.Errorf("job %s lease = NULL, want a live lease", id)
		}
	}

	assertJob("queued-stale", "failed", "Import interrupted by server restart", true)
	assertJob("running-expired", "failed", "Import interrupted by server restart", true)
	assertJob("running-live", "running", "", false)
	assertJob("completed", "completed", "", true)
}

// TestDeleteImportedDataRecoversExpiredRunningJob asserts cleanup is not
// permanently refused after a crash: the expired worker is reconciled before
// the active-job guard runs.
func TestDeleteImportedDataRecoversExpiredRunningJob(t *testing.T) {
	t.Parallel()

	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	insertConnection(t, tdb, "connection-cleanup-reconcile")
	service := New(tdb.Database, blobstore.Disabled())

	if _, err := tdb.ExecWrite(`
		INSERT INTO jira_import_jobs (id, connection_id, status, scope, config_json, lease_expires_at)
		VALUES ('job-expired', 'connection-cleanup-reconcile', 'running', 'work_items', '{"project_keys":["WS"]}', ?)
	`, time.Now().UTC().Add(-time.Minute).Unix()); err != nil {
		t.Fatalf("insert expired running job: %v", err)
	}

	deleted, err := service.DeleteImportedData("job-expired", 0)
	if errors.Is(err, ErrJobActive) {
		t.Fatalf("DeleteImportedData() still refused an interrupted job: %v", err)
	}
	if err != nil {
		t.Fatalf("DeleteImportedData() error = %v", err)
	}
	if deleted == nil {
		t.Fatal("DeleteImportedData() returned nil result")
	}
}

// TestRecoveredJiraImportWorkerIsFencedFromJobState is the WI-1718 contract:
// once a job is recovered, its old worker token can no longer renew the lease,
// write progress, or set a terminal status, so a cleanup's data_deleted state
// is never overwritten and a live worker with the current token still writes.
func TestRecoveredJiraImportWorkerIsFencedFromJobState(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()
	insertConnection(t, tdb, "connection-fence")
	service := New(tdb.Database, blobstore.Disabled())

	insertJob(t, tdb, "job-fenced", "connection-fence", "queued")
	staleToken := "stale-worker"
	if err := service.ClaimJob("job-fenced", staleToken); err != nil {
		t.Fatalf("claim job: %v", err)
	}
	if _, err := tdb.ExecWrite(`
		UPDATE jira_import_jobs SET lease_expires_at = ? WHERE id = 'job-fenced'
	`, time.Now().UTC().Add(-time.Minute).Unix()); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	if _, err := service.ReconcileInterrupted(time.Now().UTC()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// The recovered worker cannot renew, progress, complete, or persist a result.
	if renewed, err := service.RenewLease("job-fenced", staleToken); err != nil || renewed {
		t.Fatalf("stale RenewLease = (%t, %v), want (false, nil)", renewed, err)
	}
	if err := service.CheckJobOwnership("job-fenced", staleToken); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale CheckJobOwnership = %v, want ErrLeaseLost", err)
	}
	if err := service.UpdateProgress("job-fenced", staleToken, &Progress{Phase: "issues"}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale UpdateProgress = %v, want ErrLeaseLost", err)
	}
	if err := service.UpdateStatus("job-fenced", staleToken, "completed", "completed", &Progress{}, ""); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale UpdateStatus = %v, want ErrLeaseLost", err)
	}
	if err := service.UpdateJobResult("job-fenced", staleToken, []byte(`{"summary":{}}`)); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale UpdateJobResult = %v, want ErrLeaseLost", err)
	}

	var status string
	var leaseToken sql.NullString
	if err := tdb.QueryRow(`SELECT status, lease_token FROM jira_import_jobs WHERE id = 'job-fenced'`).Scan(&status, &leaseToken); err != nil {
		t.Fatalf("load fenced job: %v", err)
	}
	if status != "failed" {
		t.Fatalf("fenced job status = %q, want failed", status)
	}
	if leaseToken.Valid {
		t.Fatalf("fenced job token = %q, want NULL", leaseToken.String)
	}

	// Cleanup marks the data deleted; the stale worker still cannot resurrect a
	// terminal status.
	if _, err := tdb.ExecWrite(`UPDATE jira_import_jobs SET status = 'data_deleted' WHERE id = 'job-fenced'`); err != nil {
		t.Fatalf("mark data deleted: %v", err)
	}
	if err := service.UpdateStatus("job-fenced", staleToken, "completed", "completed", &Progress{}, ""); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale UpdateStatus after cleanup = %v, want ErrLeaseLost", err)
	}
	if err := tdb.QueryRow(`SELECT status FROM jira_import_jobs WHERE id = 'job-fenced'`).Scan(&status); err != nil {
		t.Fatalf("reload fenced job: %v", err)
	}
	if status != "data_deleted" {
		t.Fatalf("job status after stale terminal write = %q, want data_deleted", status)
	}

	// A fresh queued job and its live worker still write normally.
	insertJob(t, tdb, "job-live", "connection-fence", "queued")
	liveToken := "live-worker"
	if err := service.ClaimJob("job-live", liveToken); err != nil {
		t.Fatalf("claim live job: %v", err)
	}
	if renewed, err := service.RenewLease("job-live", liveToken); err != nil || !renewed {
		t.Fatalf("live RenewLease = (%t, %v), want (true, nil)", renewed, err)
	}
	if err := service.CheckJobOwnership("job-live", liveToken); err != nil {
		t.Fatalf("live CheckJobOwnership = %v, want nil", err)
	}
	if err := service.UpdateProgress("job-live", liveToken, &Progress{Phase: "issues", ImportedIssues: 4}); err != nil {
		t.Fatalf("live UpdateProgress: %v", err)
	}
	if err := service.UpdateStatus("job-live", liveToken, "completed", "completed", &Progress{ImportedIssues: 4}, ""); err != nil {
		t.Fatalf("live UpdateStatus: %v", err)
	}
	if err := tdb.QueryRow(`SELECT status FROM jira_import_jobs WHERE id = 'job-live'`).Scan(&status); err != nil {
		t.Fatalf("load live job: %v", err)
	}
	if status != "completed" {
		t.Fatalf("live job status = %q, want completed", status)
	}
}
