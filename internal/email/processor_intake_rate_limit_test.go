//go:build test

package email_test

import (
	"context"
	"testing"

	"windshift/internal/email"
	"windshift/internal/models"
)

// The per-sender new-ticket cap is per intake, not per mailbox: one mailbox
// feeding two targets keeps independent quotas.
func TestProcessor_RateLimitIsPerIntake(t *testing.T) {
	env := setupProcessorTestEnv(t, false)
	ctx := context.Background()

	limit := 1
	config := &models.ChannelConfig{
		EmailWorkspaceID:      env.workspaceID,
		EmailItemTypeID:       &env.itemTypeID,
		EmailRateLimitPerHour: &limit,
	}
	intakeA := &models.Intake{ID: 1}
	intakeB := &models.Intake{ID: 2}

	first, err := env.processor.ProcessEmailWithIntake(
		ctx, newTestEmail("<intake-a-1@example.com>", "A one", "body"), env.channelID, 0, config, intakeA)
	if err != nil {
		t.Fatalf("intake A first: %v", err)
	}
	if first.Action != email.ActionItemCreated {
		t.Fatalf("intake A first action = %s, want %s", first.Action, email.ActionItemCreated)
	}

	other, err := env.processor.ProcessEmailWithIntake(
		ctx, newTestEmail("<intake-b-1@example.com>", "B one", "body"), env.channelID, 0, config, intakeB)
	if err != nil {
		t.Fatalf("intake B first: %v", err)
	}
	if other.Action != email.ActionItemCreated {
		t.Fatalf("intake B must have its own quota, got action %s", other.Action)
	}

	overCap, err := env.processor.ProcessEmailWithIntake(
		ctx, newTestEmail("<intake-a-2@example.com>", "A two", "body"), env.channelID, 0, config, intakeA)
	if err != nil {
		t.Fatalf("intake A second: %v", err)
	}
	if overCap.Action != email.ActionRateLimited {
		t.Fatalf("intake A second action = %s, want %s", overCap.Action, email.ActionRateLimited)
	}
}
