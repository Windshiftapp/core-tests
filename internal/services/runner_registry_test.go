package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/repository"
)

func newRunnerRegistryTestService(t *testing.T) (*RunnerRegistryService, int) {
	t.Helper()
	db := newRunServiceTestDB(t)
	poolID, err := repository.NewActionRepository(db).CreateCapability(&models.ActionCapability{
		Name:                   "Test runner pool",
		CapabilityType:         models.CapabilityRunnerPool,
		Config:                 `{}`,
		IsEnabled:              true,
		AppliesToAllWorkspaces: true,
	})
	if err != nil {
		t.Fatalf("seed runner pool: %v", err)
	}
	return NewRunnerRegistryService(repository.NewRunnerRepository(db), nil), poolID
}

// TestRunnerRegistry_RegistrationTokenIsSingleUse pins the WI-238 security
// Phase 6 contract: a registration token bootstraps exactly one runner. The
// first Register succeeds and consumes the token; a second Register with the
// same token is rejected, so a leaked/shared token cannot be replayed to
// register additional instances.
func TestRunnerRegistry_RegistrationTokenIsSingleUse(t *testing.T) {
	reg, poolID := newRunnerRegistryTestService(t)
	ctx := context.Background()

	token, _, err := reg.MintRegistrationToken(ctx, poolID, nil, "test", time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	cred, inst, err := reg.Register(ctx, token, "runner-1")
	if err != nil {
		t.Fatalf("first register: %v", err)
	}
	if cred == "" || inst == nil {
		t.Fatalf("first register returned empty credential/instance: cred=%q inst=%v", cred, inst)
	}

	if _, _, err := reg.Register(ctx, token, "runner-2"); !errors.Is(err, ErrInvalidRegistrationToken) {
		t.Fatalf("second register: want ErrInvalidRegistrationToken, got %v", err)
	}
}

// TestRunnerRegistry_ConsumedCredentialStillAuthenticates confirms the runner
// the token registered keeps working after the token is consumed — single-use
// burns the token, not the per-instance credential.
func TestRunnerRegistry_ConsumedCredentialStillAuthenticates(t *testing.T) {
	reg, poolID := newRunnerRegistryTestService(t)
	ctx := context.Background()

	token, _, err := reg.MintRegistrationToken(ctx, poolID, nil, "test", time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	cred, _, err := reg.Register(ctx, token, "runner-1")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	inst, err := reg.Authenticate(ctx, cred)
	if err != nil || inst == nil {
		t.Fatalf("authenticate consumed-token credential: inst=%v err=%v", inst, err)
	}
}
