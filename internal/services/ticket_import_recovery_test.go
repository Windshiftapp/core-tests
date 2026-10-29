//go:build test

package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"windshift/internal/csvimport"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestTicketImportGetJobReconcilesExpiredImport(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	store := csvimport.NewStore(db)
	jobID := "interrupted-ticket-import"
	now := time.Now().UTC()
	if err := store.CreateUpload(csvimport.KindTicket, data.WorkspaceID, data.UserID, jobID, now); err != nil {
		t.Fatalf("CreateUpload: %v", err)
	}
	claimed, err := store.ClaimUpload(csvimport.KindTicket, data.WorkspaceID, data.UserID, jobID, "unused.csv", "{}", now)
	if err != nil || !claimed {
		t.Fatalf("ClaimUpload = (%v, %v), want (true, nil)", claimed, err)
	}
	if err := store.StartRunning(jobID, "importing", "{}"); err != nil {
		t.Fatalf("StartRunning: %v", err)
	}

	itemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Partially imported ticket",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if _, err := db.ExecWrite(`INSERT INTO item_import_rows (workspace_id, external_ref, item_id, job_id) VALUES (?, ?, ?, ?)`, data.WorkspaceID, "external-1", itemID, jobID); err != nil {
		t.Fatalf("seed import row: %v", err)
	}
	if _, err := db.ExecWrite(`UPDATE import_jobs SET lease_expires_at = ? WHERE id = ?`, now.Add(-time.Minute).Unix(), jobID); err != nil {
		t.Fatalf("expire import lease: %v", err)
	}
	liveJobID := "live-ticket-import"
	if err := store.CreateUpload(csvimport.KindTicket, data.WorkspaceID, data.UserID, liveJobID, now); err != nil {
		t.Fatalf("CreateUpload live job: %v", err)
	}
	claimed, err = store.ClaimUpload(csvimport.KindTicket, data.WorkspaceID, data.UserID, liveJobID, "unused.csv", "{}", now)
	if err != nil || !claimed {
		t.Fatalf("ClaimUpload live job = (%v, %v), want (true, nil)", claimed, err)
	}
	if err := store.StartRunning(liveJobID, "importing", "{}"); err != nil {
		t.Fatalf("StartRunning live job: %v", err)
	}

	tickets := NewTicketImportService(db, nil, "")
	liveJob, err := tickets.getJob(data.WorkspaceID, liveJobID)
	if err != nil {
		t.Fatalf("get live job: %v", err)
	}
	if liveJob.Status != "running" {
		t.Fatalf("live job status = %q, want running", liveJob.Status)
	}
	staleJob, err := store.GetJob(csvimport.KindTicket, data.WorkspaceID, jobID)
	if err != nil || staleJob.Status.String != "running" {
		t.Fatalf("unrelated stale job after live lookup = (%v, %v), want running", staleJob, err)
	}

	job, err := tickets.getJob(data.WorkspaceID, jobID)
	if err != nil {
		t.Fatalf("getJob: %v", err)
	}
	if job.Status != "failed" {
		t.Fatalf("job status = %q, want failed", job.Status)
	}
	row, err := store.GetJob(csvimport.KindTicket, data.WorkspaceID, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if row.ErrorMessage.String != "Import worker lease expired" {
		t.Fatalf("job error = %q, want expired lease", row.ErrorMessage.String)
	}
	if _, err := repository.NewItemRepository(db).FindByIDWithDetails(int(itemID)); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("partially imported ticket lookup = %v, want not found", err)
	}
	var mappings int
	if err := db.QueryRow(`SELECT COUNT(*) FROM item_import_rows WHERE job_id = ?`, jobID).Scan(&mappings); err != nil {
		t.Fatalf("count import row mappings: %v", err)
	}
	if mappings != 0 {
		t.Fatalf("import row mappings = %d, want 0", mappings)
	}
}

// TestTicketImportRowRemovesOrphanWhenMappingFails verifies WI-1562: when the
// ticket is created but the mapping transaction cannot commit (here because
// the worker lease expired), the just-created ticket is deleted so recovery
// cannot leave an untracked orphan or a duplicate on re-import.
func TestTicketImportRowRemovesOrphanWhenMappingFails(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	store := csvimport.NewStore(db)
	jobID := "orphan-ticket-import"
	now := time.Now().UTC()
	if err := store.CreateUpload(csvimport.KindTicket, data.WorkspaceID, data.UserID, jobID, now); err != nil {
		t.Fatalf("CreateUpload: %v", err)
	}
	claimed, err := store.ClaimUpload(csvimport.KindTicket, data.WorkspaceID, data.UserID, jobID, "unused.csv", "{}", now)
	if err != nil || !claimed {
		t.Fatalf("ClaimUpload = (%v, %v), want (true, nil)", claimed, err)
	}
	if err := store.StartRunning(jobID, "importing", "{}"); err != nil {
		t.Fatalf("StartRunning: %v", err)
	}
	// Expire the lease so the mapping transaction's renewal fails after the
	// ticket insert has already committed.
	if _, err := db.ExecWrite(`UPDATE import_jobs SET lease_expires_at = ? WHERE id = ?`, now.Add(-time.Minute).Unix(), jobID); err != nil {
		t.Fatalf("expire import lease: %v", err)
	}

	tickets := NewTicketImportService(db, nil, "")
	columns := map[string]int{"external_ref": 0, "title": 1}
	if err := tickets.importRow(context.Background(), data.WorkspaceID, jobID, columns, []string{"ORPHAN-1", "Orphan ticket"}, data.UserID); err == nil {
		t.Fatal("importRow succeeded with an expired lease, want an error")
	}

	var orphans int
	if err := db.QueryRow(`SELECT COUNT(*) FROM items WHERE workspace_id = ? AND title = ?`, data.WorkspaceID, "Orphan ticket").Scan(&orphans); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if orphans != 0 {
		t.Fatalf("orphan tickets = %d, want 0 (the failed mapping must compensate)", orphans)
	}
	var mappings int
	if err := db.QueryRow(`SELECT COUNT(*) FROM item_import_rows WHERE job_id = ?`, jobID).Scan(&mappings); err != nil {
		t.Fatalf("count mappings: %v", err)
	}
	if mappings != 0 {
		t.Fatalf("import mappings = %d, want 0", mappings)
	}
}
