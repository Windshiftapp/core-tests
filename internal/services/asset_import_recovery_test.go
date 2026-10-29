//go:build test

package services

import (
	"errors"
	"testing"
	"time"

	"windshift/internal/csvimport"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestAssetImportReconciliationPreservesLiveJobs(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	userID, setID, typeID := assetEventFixture(t, db)
	repo := repository.NewAssetRepository(db)
	store := seedRunningAssetImportJob(t, db, setID, userID, "live-import")
	createdAt := time.Now().UTC().Add(-time.Minute)
	worker := NewAssetService(db, repo)
	assetID, err := worker.InsertImportedAsset(repository.ImportAssetRowInput{
		SetID: setID, AssetTypeID: typeID, Title: "Live imported asset", ImportJobID: "live-import", CreatedBy: userID, CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	otherServer := NewAssetApplicationService(db, nil, nil, nil)
	liveJob, err := otherServer.getImportJob(setID, "live-import")
	if err != nil {
		t.Fatalf("live getImportJob: %v", err)
	}
	if liveJob.Status != "running" {
		t.Errorf("live job status = %q, want running", liveJob.Status)
	}
	job, err := store.GetJob(csvimport.KindAsset, setID, "live-import")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status.String != "running" {
		t.Errorf("live job status = %q, want running", job.Status.String)
	}
	asset, err := repo.FindAssetFullByID(assetID)
	if err != nil {
		t.Fatalf("live asset removed: %v", err)
	}
	if asset.Title != "Live imported asset" {
		t.Fatalf("asset title = %q", asset.Title)
	}

	if _, err := db.ExecWrite(`UPDATE import_jobs SET lease_expires_at = ? WHERE id = ?`, time.Now().UTC().Add(-time.Minute).Unix(), "live-import"); err != nil {
		t.Fatalf("expire import lease: %v", err)
	}
	expiredJob, err := otherServer.getImportJob(setID, "live-import")
	if err != nil {
		t.Fatalf("expired getImportJob: %v", err)
	}
	if expiredJob.Status != "failed" {
		t.Fatalf("expired job status = %q, want failed", expiredJob.Status)
	}
	if _, err := repo.FindAssetFullByID(assetID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("recovered asset lookup = %v", err)
	}
	_, err = worker.InsertImportedAsset(repository.ImportAssetRowInput{
		SetID: setID, AssetTypeID: typeID, Title: "Late worker write", ImportJobID: "live-import", CreatedBy: userID, CreatedAt: createdAt,
	})
	if !errors.Is(err, repository.ErrAssetImportLeaseLost) {
		t.Fatalf("late insertion error = %v", err)
	}
	for name, update := range map[string]func() error{
		"start":      func() error { return store.StartRunning("live-import", "importing", "{}") },
		"progress":   func() error { return store.UpdateProgress("live-import", "importing", "{}") },
		"status":     func() error { return store.UpdateStatus("live-import", "running", "importing", "{}") },
		"completion": func() error { return store.Finish("live-import", "completed", "completed", "{}", "") },
	} {
		t.Run("recovered job rejects "+name, func(t *testing.T) {
			if err := update(); !errors.Is(err, csvimport.ErrLeaseLost) {
				t.Fatalf("late update error = %v", err)
			}
		})
	}
	var assets int
	if err := db.QueryRow("SELECT COUNT(*) FROM assets WHERE import_job_id = 'live-import'").Scan(&assets); err != nil {
		t.Fatal(err)
	}
	if assets != 0 {
		t.Fatalf("late writes left %d assets", assets)
	}
	job, err = store.GetJob(csvimport.KindAsset, setID, "live-import")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status.String != "failed" || job.ErrorMessage.String != "Import worker lease expired" {
		t.Fatalf("recovered job = %+v", job)
	}
}
