package services

import (
	"errors"
	"testing"
)

// TestDueDateReminderPreferencesRoundTrip pins the per-user opt-in and lead
// time: each field can be patched independently and a fresh user defaults to
// reminders off.
func TestDueDateReminderPreferencesRoundTrip(t *testing.T) {
	_, svc := openTUIPrefsTestDB(t)

	fresh, err := svc.GetSnapshot(1)
	if err != nil {
		t.Fatalf("fresh snapshot: %v", err)
	}
	if fresh.DueDateRemindersEnabled || fresh.DueDateReminderDays != 3 {
		t.Fatalf("fresh defaults = enabled:%v days:%d", fresh.DueDateRemindersEnabled, fresh.DueDateReminderDays)
	}

	enabled, err := svc.UpdateSnapshot(1, UserPreferencesPatch{
		DueDateRemindersEnabledSet: true,
		DueDateRemindersEnabled:    true,
		DueDateReminderDaysSet:     true,
		DueDateReminderDays:        7,
	})
	if err != nil {
		t.Fatalf("enable reminders: %v", err)
	}
	if !enabled.DueDateRemindersEnabled || enabled.DueDateReminderDays != 7 {
		t.Fatalf("enabled snapshot = enabled:%v days:%d", enabled.DueDateRemindersEnabled, enabled.DueDateReminderDays)
	}

	// Disabling must not reset the saved lead time.
	disabled, err := svc.UpdateSnapshot(1, UserPreferencesPatch{
		DueDateRemindersEnabledSet: true,
		DueDateRemindersEnabled:    false,
	})
	if err != nil {
		t.Fatalf("disable reminders: %v", err)
	}
	if disabled.DueDateRemindersEnabled || disabled.DueDateReminderDays != 7 {
		t.Fatalf("disabled snapshot = enabled:%v days:%d", disabled.DueDateRemindersEnabled, disabled.DueDateReminderDays)
	}
}

func TestDueDateReminderPreferencesRejectOutOfRangeDays(t *testing.T) {
	_, svc := openTUIPrefsTestDB(t)

	for _, days := range []int{-1, 0, 31} {
		_, err := svc.UpdateSnapshot(1, UserPreferencesPatch{
			DueDateReminderDaysSet: true,
			DueDateReminderDays:    days,
		})
		if !errors.Is(err, ErrInvalidReminderDays) {
			t.Fatalf("days=%d error = %v, want ErrInvalidReminderDays", days, err)
		}
	}
}
