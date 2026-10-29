//go:build test

package sla

import (
	"encoding/json"
	"testing"
	"time"

	"windshift/internal/models"
)

func ptrTime(t time.Time) *time.Time { return &t }
func ptrInt(v int) *int              { return &v }
func ptrInt64(v int64) *int64        { return &v }

func calendarSnapshot(t *testing.T) json.RawMessage {
	t.Helper()
	weekday := []map[string]string{{"start": "09:00", "end": "17:00"}}
	raw := map[string]any{
		"timezone": "UTC",
		"weekly_intervals": map[string]any{
			"monday": weekday, "tuesday": weekday, "wednesday": weekday,
			"thursday": weekday, "friday": weekday,
		},
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal calendar: %v", err)
	}
	return encoded
}

func TestDeriveRunningCycleExtendsElapsed(t *testing.T) {
	t.Parallel()
	// Wednesday 09:00 UTC start, goal 8h. At 11:00 two business hours elapsed.
	start := time.Date(2025, 1, 8, 9, 0, 0, 0, time.UTC)
	now := time.Date(2025, 1, 8, 11, 0, 0, 0, time.UTC)
	cycle := &models.ItemSLACycle{
		ID: 1, CycleNo: 1, Status: models.SLACycleOngoing,
		GoalID: ptrInt(5), GoalDurationMs: 8 * 3600 * 1000,
		StartedAt: start, LastCalculatedAt: start,
		CalendarSnapshot: calendarSnapshot(t),
	}
	derived, err := Derive(cycle, now)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if derived.ElapsedMs != 2*3600*1000 {
		t.Fatalf("elapsed = %d, want %d", derived.ElapsedMs, 2*3600*1000)
	}
	if derived.RemainingMs == nil || *derived.RemainingMs != 6*3600*1000 {
		t.Fatalf("remaining = %v, want %d", derived.RemainingMs, 6*3600*1000)
	}
	if derived.Breached {
		t.Fatal("cycle should not be breached")
	}
	if !derived.WithinCalendarHours {
		t.Fatal("11:00 should be within calendar hours")
	}
	if derived.CalendarTimezone != "UTC" {
		t.Fatalf("calendar timezone = %q, want UTC", derived.CalendarTimezone)
	}
}

func TestDeriveBreachedCycle(t *testing.T) {
	t.Parallel()
	start := time.Date(2025, 1, 8, 9, 0, 0, 0, time.UTC)
	now := time.Date(2025, 1, 8, 18, 0, 0, 0, time.UTC) // 8 business hours, goal 4h
	cycle := &models.ItemSLACycle{
		ID: 1, CycleNo: 1, Status: models.SLACycleOngoing,
		GoalID: ptrInt(5), GoalDurationMs: 4 * 3600 * 1000,
		StartedAt: start, LastCalculatedAt: start,
		CalendarSnapshot: calendarSnapshot(t),
	}
	derived, err := Derive(cycle, now)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if derived.ElapsedMs != 8*3600*1000 {
		t.Fatalf("elapsed = %d, want %d", derived.ElapsedMs, 8*3600*1000)
	}
	if derived.RemainingMs == nil || *derived.RemainingMs != -4*3600*1000 {
		t.Fatalf("remaining = %v, want %d", derived.RemainingMs, -4*3600*1000)
	}
	if !derived.Breached {
		t.Fatal("cycle should be breached")
	}
	if derived.WithinCalendarHours {
		t.Fatal("18:00 should be outside calendar hours")
	}
}

func TestDerivePausedCycleDoesNotAdvance(t *testing.T) {
	t.Parallel()
	pausedAt := time.Date(2025, 1, 8, 10, 0, 0, 0, time.UTC)
	now := time.Date(2025, 1, 9, 15, 0, 0, 0, time.UTC)
	cycle := &models.ItemSLACycle{
		ID: 1, CycleNo: 1, Status: models.SLACycleOngoing,
		GoalID: ptrInt(5), GoalDurationMs: 8 * 3600 * 1000,
		StartedAt:        time.Date(2025, 1, 8, 9, 0, 0, 0, time.UTC),
		LastCalculatedAt: pausedAt, PauseStartedAt: &pausedAt,
		ElapsedMs: 3600 * 1000, CalendarSnapshot: calendarSnapshot(t),
	}
	derived, err := Derive(cycle, now)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if derived.ElapsedMs != 3600*1000 {
		t.Fatalf("elapsed = %d, want %d", derived.ElapsedMs, 3600*1000)
	}
	if !derived.Paused {
		t.Fatal("cycle should report paused")
	}
}

func TestDeriveCompletedCycleIsImmutable(t *testing.T) {
	t.Parallel()
	start := time.Date(2025, 1, 8, 9, 0, 0, 0, time.UTC)
	stopped := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	cycle := &models.ItemSLACycle{
		ID: 1, CycleNo: 1, Status: models.SLACycleCompleted,
		GoalID: ptrInt(5), GoalDurationMs: 8 * 3600 * 1000,
		StartedAt: start, StoppedAt: &stopped,
		ElapsedMs: 3 * 3600 * 1000, CalendarSnapshot: calendarSnapshot(t),
	}
	// Even a much later now must not change a completed cycle.
	derived, err := Derive(cycle, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if derived.ElapsedMs != 3*3600*1000 {
		t.Fatalf("elapsed = %d, want %d", derived.ElapsedMs, 3*3600*1000)
	}
	if derived.Breached {
		t.Fatal("cycle should not be breached")
	}
}

func TestDeriveNoGoalHasNoRemaining(t *testing.T) {
	t.Parallel()
	cycle := &models.ItemSLACycle{
		ID: 1, CycleNo: 1, Status: models.SLACycleOngoing,
		GoalID: nil, GoalDurationMs: 0,
		StartedAt:        time.Date(2025, 1, 8, 9, 0, 0, 0, time.UTC),
		LastCalculatedAt: time.Date(2025, 1, 8, 9, 0, 0, 0, time.UTC),
		CalendarSnapshot: calendarSnapshot(t),
	}
	derived, err := Derive(cycle, time.Date(2025, 1, 8, 20, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if derived.ElapsedMs != 0 {
		t.Fatalf("elapsed = %d for a cycle without a goal, want 0", derived.ElapsedMs)
	}
	if derived.RemainingMs != nil {
		t.Fatalf("remaining = %v, want nil", derived.RemainingMs)
	}
	if derived.Breached {
		t.Fatal("a cycle without a goal must never breach")
	}
}

func TestDeriveUnbreachWhenGoalLifts(t *testing.T) {
	t.Parallel()
	// A cycle that was breached at 4h but whose goal is now 16h: with 5h
	// elapsed the derived state is no longer breached.
	cycle := &models.ItemSLACycle{
		ID: 1, CycleNo: 1, Status: models.SLACycleOngoing,
		GoalID: ptrInt(5), GoalDurationMs: 16 * 3600 * 1000,
		StartedAt:        time.Date(2025, 1, 8, 9, 0, 0, 0, time.UTC),
		LastCalculatedAt: time.Date(2025, 1, 8, 9, 0, 0, 0, time.UTC),
		ElapsedMs:        5 * 3600 * 1000, BreachedAt: ptrTime(time.Date(2025, 1, 8, 13, 0, 0, 0, time.UTC)),
		CalendarSnapshot: calendarSnapshot(t),
	}
	derived, err := Derive(cycle, time.Date(2025, 1, 8, 14, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if derived.Breached {
		t.Fatal("derived breach should clear after the goal is lifted")
	}
	if derived.RemainingMs == nil || *derived.RemainingMs <= 0 {
		t.Fatalf("remaining = %v, want a positive value", derived.RemainingMs)
	}
}
