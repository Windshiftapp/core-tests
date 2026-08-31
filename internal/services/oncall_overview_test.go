package services

import (
	"testing"
	"time"

	"windshift/internal/models"
)

func TestCurrentOnCallForScheduleUsesHydratedMembersAndReplacesOverriddenUser(t *testing.T) {
	now := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	schedule := &models.OnCallSchedule{
		ID: 42,
		Layers: []models.OnCallScheduleLayer{
			{
				Name:         "Primary",
				Priority:     1,
				RotationType: "daily",
				HandoffTime:  "09:00",
				StartDate:    "2026-07-18",
				Members: []models.OnCallScheduleLayerMember{
					{UserID: 7, Position: 1, UserName: "Primary User", UserEmail: "primary@example.test"},
				},
			},
			{
				Name:         "Secondary",
				Priority:     2,
				RotationType: "daily",
				HandoffTime:  "09:00",
				StartDate:    "2026-07-18",
				Members: []models.OnCallScheduleLayerMember{
					{UserID: 9, Position: 1, UserName: "Secondary User", UserEmail: "secondary@example.test"},
				},
			},
		},
		Overrides: []models.OnCallScheduleOverride{
			{
				UserID:           7,
				OverrideUserID:   8,
				OverrideUserName: "Override User",
				StartTime:        now.Add(-time.Hour),
				EndTime:          now.Add(time.Hour),
			},
		},
	}

	result := (&OnCallService{}).CurrentOnCallForSchedule(schedule, now)

	if result.ScheduleID != schedule.ID || len(result.OnCall) != 2 {
		t.Fatalf("current on-call = %+v, want override plus secondary layer", result)
	}
	if entry := result.OnCall[0]; entry.UserID != 8 || entry.UserName != "Override User" || !entry.IsOverride {
		t.Fatalf("override entry = %+v, want override user 8", entry)
	}
	if entry := result.OnCall[1]; entry.UserID != 9 || entry.UserName != "Secondary User" || entry.UserEmail != "secondary@example.test" || entry.LayerName != "Secondary" {
		t.Fatalf("rotation entry = %+v, want hydrated secondary user", entry)
	}
	for _, entry := range result.OnCall {
		if entry.UserID == 7 {
			t.Fatalf("replaced user remained on call: %+v", result.OnCall)
		}
	}
}

func TestCurrentOnCallForScheduleUsesScheduleTimezone(t *testing.T) {
	schedule := rotationSchedule("Europe/Zurich", "2026-07-18", "09:00")
	service := &OnCallService{}
	before := service.CurrentOnCallForSchedule(schedule, time.Date(2026, time.July, 18, 6, 59, 0, 0, time.UTC))
	atBoundary := service.CurrentOnCallForSchedule(schedule, time.Date(2026, time.July, 18, 7, 0, 0, 0, time.UTC))
	// 08:30 UTC is 10:30 in Zurich. A UTC/server-clock calculation would still
	// be before handoff and select user 2.
	after := service.CurrentOnCallForSchedule(schedule, time.Date(2026, time.July, 18, 8, 30, 0, 0, time.UTC))
	if len(before.OnCall) != 1 || before.OnCall[0].UserID != 2 || len(atBoundary.OnCall) != 1 || atBoundary.OnCall[0].UserID != 1 || len(after.OnCall) != 1 || after.OnCall[0].UserID != 1 {
		t.Fatalf("Zurich handoff before=%+v boundary=%+v after=%+v", before.OnCall, atBoundary.OnCall, after.OnCall)
	}
}

func TestCurrentOnCallForScheduleDSTGapAdvancesHandoff(t *testing.T) {
	schedule := rotationSchedule("Europe/Zurich", "2026-03-28", "02:30")
	service := &OnCallService{}

	before := service.CurrentOnCallForSchedule(schedule, time.Date(2026, time.March, 29, 0, 59, 0, 0, time.UTC))
	after := service.CurrentOnCallForSchedule(schedule, time.Date(2026, time.March, 29, 1, 0, 0, 0, time.UTC))
	if before.OnCall[0].UserID != 1 || after.OnCall[0].UserID != 2 {
		t.Fatalf("spring handoff before=%+v after=%+v, want user 1 then user 2", before.OnCall, after.OnCall)
	}
}

func TestCurrentOnCallForScheduleDSTFoldHappensOnlyOnce(t *testing.T) {
	schedule := rotationSchedule("Europe/Zurich", "2026-10-24", "02:30")
	service := &OnCallService{}

	before := service.CurrentOnCallForSchedule(schedule, time.Date(2026, time.October, 25, 0, 20, 0, 0, time.UTC))
	firstAfter := service.CurrentOnCallForSchedule(schedule, time.Date(2026, time.October, 25, 0, 40, 0, 0, time.UTC))
	secondOccurrence := service.CurrentOnCallForSchedule(schedule, time.Date(2026, time.October, 25, 1, 10, 0, 0, time.UTC))
	if before.OnCall[0].UserID != 1 || firstAfter.OnCall[0].UserID != 2 || secondOccurrence.OnCall[0].UserID != 2 {
		t.Fatalf("fall handoff before=%+v first=%+v second=%+v", before.OnCall, firstAfter.OnCall, secondOccurrence.OnCall)
	}
}

func TestCurrentOnCallForScheduleEndDateIsInclusiveInScheduleTimezone(t *testing.T) {
	end := "2026-07-18"
	schedule := rotationSchedule("Pacific/Auckland", "2026-07-18", "09:00")
	schedule.Layers[0].EndDate = &end
	service := &OnCallService{}
	lastMinute := service.CurrentOnCallForSchedule(schedule, time.Date(2026, time.July, 18, 11, 59, 0, 0, time.UTC))
	nextDay := service.CurrentOnCallForSchedule(schedule, time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC))
	if len(lastMinute.OnCall) != 1 || len(nextDay.OnCall) != 0 {
		t.Fatalf("end-date boundary last=%+v next=%+v", lastMinute.OnCall, nextDay.OnCall)
	}
}

func rotationSchedule(timezone, startDate, handoff string) *models.OnCallSchedule {
	return &models.OnCallSchedule{
		ID:       99,
		Timezone: timezone,
		Layers: []models.OnCallScheduleLayer{{
			Name:         "Primary",
			Priority:     1,
			RotationType: "daily",
			HandoffTime:  handoff,
			StartDate:    startDate,
			Members: []models.OnCallScheduleLayerMember{
				{UserID: 1, Position: 1, UserName: "One"},
				{UserID: 2, Position: 2, UserName: "Two"},
			},
		}},
	}
}
