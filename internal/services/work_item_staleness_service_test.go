//go:build test

package services

import (
	"errors"
	"testing"

	"windshift/internal/testutils"
)

func TestWorkItemStalenessSettingsDefaultAndUpdate(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = db.Close() })
	service := NewWorkItemStalenessService(db)

	settings, err := service.Get()
	if err != nil {
		t.Fatalf("Get default settings: %v", err)
	}
	if settings.StaleAfterDays != 30 {
		t.Fatalf("default stale_after_days = %d, want 30", settings.StaleAfterDays)
	}

	updated, err := service.Update(45)
	if err != nil {
		t.Fatalf("Update settings: %v", err)
	}
	if updated.StaleAfterDays != 45 {
		t.Fatalf("updated stale_after_days = %d, want 45", updated.StaleAfterDays)
	}

	reloaded, err := NewWorkItemStalenessService(db).Get()
	if err != nil {
		t.Fatalf("reload settings: %v", err)
	}
	if reloaded.StaleAfterDays != 45 {
		t.Fatalf("reloaded stale_after_days = %d, want 45", reloaded.StaleAfterDays)
	}
}

func TestWorkItemStalenessSettingsRejectsOutOfRangeValues(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = db.Close() })
	service := NewWorkItemStalenessService(db)

	for _, days := range []int{0, 366} {
		_, err := service.Update(days)
		if !errors.Is(err, ErrInvalidWorkItemStalenessThreshold) {
			t.Fatalf("Update(%d) error = %v, want ErrInvalidWorkItemStalenessThreshold", days, err)
		}
	}
}
