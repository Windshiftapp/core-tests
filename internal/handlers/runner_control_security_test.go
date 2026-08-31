package handlers

import (
	"errors"
	"path/filepath"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
)

func TestPoolMaxConcurrentFailsClosedForRevokedOrInvalidPool(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "runner-control-pool-security.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	capability := &models.ActionCapability{
		Name:                   "Claim pool",
		CapabilityType:         models.CapabilityRunnerPool,
		Config:                 `{"max_concurrent_runs":3}`,
		IsEnabled:              true,
		AppliesToAllWorkspaces: true,
	}
	repo := repository.NewActionRepository(db)
	poolID, err := repo.CreateCapability(capability)
	if err != nil {
		t.Fatalf("CreateCapability: %v", err)
	}
	handler := &RunnerControlHandler{caps: repo}

	maxRuns, err := handler.poolMaxConcurrent(poolID)
	if err != nil || maxRuns != 3 {
		t.Fatalf("poolMaxConcurrent enabled = (%d, %v), want (3, nil)", maxRuns, err)
	}

	capability.IsEnabled = false
	if err := repo.UpdateCapability(capability); err != nil {
		t.Fatalf("disable pool: %v", err)
	}
	if _, err := handler.poolMaxConcurrent(poolID); !errors.Is(err, services.ErrRunnerPoolUnavailable) {
		t.Fatalf("poolMaxConcurrent disabled error = %v, want ErrRunnerPoolUnavailable", err)
	}

	capability.IsEnabled = true
	capability.Config = `{not-json}`
	if err := repo.UpdateCapability(capability); err != nil {
		t.Fatalf("set invalid pool config: %v", err)
	}
	if _, err := handler.poolMaxConcurrent(poolID); err == nil {
		t.Fatal("poolMaxConcurrent accepted invalid config as unlimited")
	}
}
