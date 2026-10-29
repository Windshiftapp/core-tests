//go:build test

package services

import (
	"testing"
	"time"

	"windshift/internal/csvimport"
	"windshift/internal/database"
)

// seedRunningAssetImportJob seeds a live (running) import job through the
// shared csvimport store so tests can exercise lease-fenced row writes.
func seedRunningAssetImportJob(t *testing.T, db database.Database, scopeID, userID int, jobID string) *csvimport.Store {
	t.Helper()
	store := csvimport.NewStore(db)
	now := time.Now().UTC()
	if err := store.CreateUpload(csvimport.KindAsset, scopeID, userID, jobID, now); err != nil {
		t.Fatalf("seed import upload: %v", err)
	}
	claimed, err := store.ClaimUpload(csvimport.KindAsset, scopeID, userID, jobID, "unused.csv", "{}", now)
	if err != nil || !claimed {
		t.Fatalf("seed import claim = (%v, %v), want (true, nil)", claimed, err)
	}
	if err := store.StartRunning(jobID, "importing", "{}"); err != nil {
		t.Fatalf("seed import running: %v", err)
	}
	return store
}
