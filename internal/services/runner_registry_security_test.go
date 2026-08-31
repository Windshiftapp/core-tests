package services

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
)

func TestRunnerRegistrationHonorsPoolDisableAndPreservesUnconsumedToken(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "runner-registration-security.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	capability := &models.ActionCapability{
		Name:                   "Registration pool",
		CapabilityType:         models.CapabilityRunnerPool,
		Config:                 `{}`,
		IsEnabled:              true,
		AppliesToAllWorkspaces: true,
	}
	actionRepo := repository.NewActionRepository(db)
	poolID, err := actionRepo.CreateCapability(capability)
	if err != nil {
		t.Fatalf("CreateCapability: %v", err)
	}

	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	runnerRepo := repository.NewRunnerRepository(db)
	registry := NewRunnerRegistryService(runnerRepo, func() time.Time { return now })
	token, _, err := registry.MintRegistrationToken(context.Background(), poolID, nil, "one runner", time.Hour)
	if err != nil {
		t.Fatalf("MintRegistrationToken: %v", err)
	}

	capability.IsEnabled = false
	if err := actionRepo.UpdateCapability(capability); err != nil {
		t.Fatalf("disable pool: %v", err)
	}
	if _, _, err := registry.Register(context.Background(), token, "runner-1"); !errors.Is(err, ErrRunnerPoolUnavailable) {
		t.Fatalf("Register disabled pool error = %v, want ErrRunnerPoolUnavailable", err)
	}
	if _, _, err := registry.MintRegistrationToken(context.Background(), poolID, nil, "blocked", time.Hour); !errors.Is(err, ErrRunnerPoolUnavailable) {
		t.Fatalf("MintRegistrationToken disabled pool error = %v, want ErrRunnerPoolUnavailable", err)
	}

	// A disable check must happen before the single-use token is consumed. If
	// the pool is deliberately re-enabled, the same token can still bootstrap
	// its one runner.
	capability.IsEnabled = true
	if err := actionRepo.UpdateCapability(capability); err != nil {
		t.Fatalf("re-enable pool: %v", err)
	}
	credential, instance, err := registry.Register(context.Background(), token, "runner-1")
	if err != nil {
		t.Fatalf("Register after re-enable: %v", err)
	}
	if credential == "" || instance == nil || instance.PoolCapabilityID != poolID {
		t.Fatalf("registration result credential=%q instance=%+v", credential, instance)
	}
}
