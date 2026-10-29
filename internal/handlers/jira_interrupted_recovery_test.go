package handlers

import (
	"testing"
	"time"

	"windshift/internal/blobstore"
	"windshift/internal/testutils"
)

// TestEnqueueJiraImportRecoversInterruptedJobForRetry is the WI-1703 retry
// contract: a queued/running job whose worker lease expired is failed before
// the conflict check, so a force re-import is admitted instead of being
// permanently blocked. A job a live worker still holds keeps blocking.
func TestEnqueueJiraImportRecoversInterruptedJobForRetry(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	insertJiraImportConnection(t, tdb.Database, "conn-interrupted")
	insertJiraImportJob(t, tdb.Database, "job-interrupted", "conn-interrupted", "running", []string{"APP"})
	if _, err := tdb.ExecWrite(`
		UPDATE jira_import_jobs SET lease_expires_at = ? WHERE id = 'job-interrupted'
	`, time.Now().UTC().Add(-time.Minute).Unix()); err != nil {
		t.Fatalf("expire job lease: %v", err)
	}

	handler := NewJiraImportHandler(tdb.Database, "jira-interrupted-recovery-secret", "", blobstore.Disabled())
	req := StartImportRequest{
		ConnectionID:  "conn-interrupted",
		ProjectKeys:   []string{"APP"},
		ForceReimport: true,
	}
	fingerprint, err := jiraImportPlanFingerprint(req)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}

	result, err := handler.enqueueJiraImport(req, fingerprint, nil)
	if err != nil {
		t.Fatalf("retry after interruption was blocked: %v", err)
	}
	if result == nil || result.JobID == "" {
		t.Fatalf("enqueue result = %#v, want a new job", result)
	}

	var status string
	if err := tdb.QueryRow(`SELECT status FROM jira_import_jobs WHERE id = 'job-interrupted'`).Scan(&status); err != nil {
		t.Fatalf("load interrupted job: %v", err)
	}
	if status != "failed" {
		t.Fatalf("interrupted job status = %q, want failed", status)
	}

	// A live worker's fresh lease still blocks a concurrent import.
	insertJiraImportConnection(t, tdb.Database, "conn-live")
	insertJiraImportJob(t, tdb.Database, "job-live", "conn-live", "running", []string{"APP"})
	liveReq := StartImportRequest{
		ConnectionID:  "conn-live",
		ProjectKeys:   []string{"APP"},
		ForceReimport: true,
	}
	liveFingerprint, err := jiraImportPlanFingerprint(liveReq)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if _, err := handler.enqueueJiraImport(liveReq, liveFingerprint, nil); err == nil {
		t.Fatal("enqueueJiraImport() admitted an import while a live worker held the job")
	}

	var liveStatus string
	if err := tdb.QueryRow(`SELECT status FROM jira_import_jobs WHERE id = 'job-live'`).Scan(&liveStatus); err != nil {
		t.Fatalf("load live job: %v", err)
	}
	if liveStatus != "running" {
		t.Fatalf("live job status = %q, want running (untouched)", liveStatus)
	}
}

// TestEnqueueJiraImportReservesNewlyQueuedJob is the WI-1717 regression: a job
// admitted to the queue must hold an active lease immediately, so a reconcile
// between the insert commit and worker startup does not fail it and an
// overlapping force re-import stays rejected. A genuinely abandoned queued job
// is still recovered once its startup lease expires.
func TestEnqueueJiraImportReservesNewlyQueuedJob(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	insertJiraImportConnection(t, tdb.Database, "conn-reserve")
	handler := NewJiraImportHandler(tdb.Database, "jira-reserve-secret", "", blobstore.Disabled())

	req := StartImportRequest{
		ConnectionID: "conn-reserve",
		ProjectKeys:  []string{"APP"},
	}
	fingerprint, err := jiraImportPlanFingerprint(req)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	result, err := handler.enqueueJiraImport(req, fingerprint, nil)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// The worker has not started yet; reconcile inside that window.
	reconciled, err := handler.imports.ReconcileInterrupted(time.Now().UTC())
	if err != nil {
		t.Fatalf("ReconcileInterrupted: %v", err)
	}
	if reconciled != 0 {
		t.Fatalf("reconciled %d freshly queued jobs, want 0", reconciled)
	}

	var status string
	var lease int64
	if err := tdb.QueryRow(`SELECT status, lease_expires_at FROM jira_import_jobs WHERE id = ?`, result.JobID).Scan(&status, &lease); err != nil {
		t.Fatalf("load queued job: %v", err)
	}
	if status != "queued" {
		t.Fatalf("freshly queued job status = %q, want queued", status)
	}
	if lease <= time.Now().UTC().Unix() {
		t.Fatalf("freshly queued job lease = %d, want an active future lease", lease)
	}

	// An overlapping force re-import stays rejected while the queued job holds
	// its reservation.
	forceReq := StartImportRequest{
		ConnectionID:  "conn-reserve",
		ProjectKeys:   []string{"APP"},
		ForceReimport: true,
	}
	forceFingerprint, err := jiraImportPlanFingerprint(forceReq)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if _, err := handler.enqueueJiraImport(forceReq, forceFingerprint, nil); err == nil {
		t.Fatal("overlapping force re-import was admitted while a fresh queued job held its reservation")
	}

	// A genuinely abandoned queued job is recovered once its startup lease
	// expires.
	if _, err := tdb.ExecWrite(`
		UPDATE jira_import_jobs SET lease_expires_at = ? WHERE id = ?
	`, time.Now().UTC().Add(-time.Minute).Unix(), result.JobID); err != nil {
		t.Fatalf("expire queued job lease: %v", err)
	}
	if _, err := handler.imports.ReconcileInterrupted(time.Now().UTC()); err != nil {
		t.Fatalf("reconcile abandoned queued job: %v", err)
	}
	if err := tdb.QueryRow(`SELECT status FROM jira_import_jobs WHERE id = ?`, result.JobID).Scan(&status); err != nil {
		t.Fatalf("load abandoned job: %v", err)
	}
	if status != "failed" {
		t.Fatalf("abandoned queued job status = %q, want failed", status)
	}
}
