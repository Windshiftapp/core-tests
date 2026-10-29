//go:build test

package sla

import (
	"testing"
	"time"

	"windshift/internal/models"
)

// TestItemsSLABadgesOmitCompletedCycles pins the badge-specific read contract:
// detail reads still return completed history, while the badge batch loads only
// ongoing cycles so response size tracks active metrics, not ticket history.
func TestItemsSLABadgesOmitCompletedCycles(t *testing.T) {
	fixture := newEngineFixture(t)
	now := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	fixture.engine.SetClock(fixedClock{now: now})
	fixture.createMetric(t,
		[]models.SLACondition{statusCondition(models.SLAPhaseStart, fixture.statusOpen), statusCondition(models.SLAPhaseStop, fixture.statusDone)},
		fixture.defaultGoal(),
	)
	fixture.observe(t, fixture.createdFact())
	fixture.observe(t, fixture.statusFact(fixture.statusDone))

	full, err := fixture.engine.ItemsSLA(t.Context(), fixture.workspaceID, []int{fixture.itemID})
	if err != nil {
		t.Fatalf("ItemsSLA: %v", err)
	}
	if got := len(full[fixture.itemID]); got != 1 {
		t.Fatalf("ItemsSLA states = %d, want 1 metric", got)
	}
	if got := len(full[fixture.itemID][0].Completed); got != 1 {
		t.Fatalf("ItemsSLA completed cycles = %d, want 1", got)
	}
	if full[fixture.itemID][0].Ongoing != nil {
		t.Fatalf("ItemsSLA ongoing = %+v, want nil", full[fixture.itemID][0].Ongoing)
	}

	badges, err := fixture.engine.ItemsSLABadges(t.Context(), fixture.workspaceID, []int{fixture.itemID})
	if err != nil {
		t.Fatalf("ItemsSLABadges: %v", err)
	}
	if got := len(badges[fixture.itemID]); got != 0 {
		t.Fatalf("ItemsSLABadges states = %d, want 0 completed-only states", got)
	}
}
