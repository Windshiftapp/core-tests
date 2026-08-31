//go:build test

package scheduler

import (
	"fmt"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

func recurrenceSchedulerFixture(t *testing.T) (*RecurrenceScheduler, *repository.RecurrenceRepository, int, int, int) {
	t.Helper()

	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	itemRepo := repository.NewItemRepository(tdb.GetDatabase())
	tx, err := tdb.Begin()
	if err != nil {
		t.Fatalf("begin template item transaction: %v", err)
	}
	statusID, priorityID, creatorID := data.StatusID, data.PriorityID, data.UserID
	templateID, err := itemRepo.Create(tx, &models.Item{
		WorkspaceID:         data.WorkspaceID,
		WorkspaceItemNumber: 1,
		Title:               "Recurring boundary template",
		StatusID:            &statusID,
		PriorityID:          &priorityID,
		CreatorID:           &creatorID,
	})
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("create template item: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit template item: %v", err)
	}

	recurrenceRepo := repository.NewRecurrenceRepository(tdb.GetDatabase())
	scheduler := NewRecurrenceScheduler(
		tdb.GetDatabase(),
		services.NewWorkflowService(tdb.GetDatabase()),
	)
	return scheduler, recurrenceRepo, templateID, statusID, data.UserID
}

func TestRecurrenceSchedulerGenerationHonorsRuleEndConditions(t *testing.T) {
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -2)
	end := start.AddDate(0, 0, 2)

	tests := []struct {
		name  string
		rrule string
	}{
		{
			name:  "after occurrences stops at COUNT",
			rrule: "FREQ=DAILY;COUNT=3",
		},
		{
			name:  "on date includes and stops at UNTIL",
			rrule: fmt.Sprintf("FREQ=DAILY;UNTIL=%s", end.Format("20060102")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheduler, recurrenceRepo, templateID, statusID, creatorID := recurrenceSchedulerFixture(t)
			ruleID, err := recurrenceRepo.Create(&models.RecurrenceRule{
				TemplateItemID:   templateID,
				WorkspaceID:      1,
				RRule:            tt.rrule,
				DtStart:          start,
				Timezone:         "UTC",
				LeadTimeDays:     30,
				CopyAssignee:     true,
				CopyPriority:     true,
				CopyCustomFields: true,
				CopyDescription:  true,
				StatusOnCreate:   &statusID,
				IsActive:         true,
				CreatedBy:        &creatorID,
			})
			if err != nil {
				t.Fatalf("create recurrence rule: %v", err)
			}

			generated, err := scheduler.ForceGenerate(ruleID)
			if err != nil {
				t.Fatalf("generate recurrence instances: %v", err)
			}
			if generated != 3 {
				t.Fatalf("generated count = %d, want 3", generated)
			}

			instances, err := recurrenceRepo.GetInstancesByRuleID(ruleID, 20, 0)
			if err != nil {
				t.Fatalf("list generated instances: %v", err)
			}
			if len(instances) != 3 {
				t.Fatalf("persisted instance count = %d, want 3", len(instances))
			}

			gotDates := make(map[string]bool, len(instances))
			for _, instance := range instances {
				gotDates[instance.ScheduledDate.UTC().Format("2006-01-02")] = true
			}
			for offset := 0; offset < 3; offset++ {
				wantDate := start.AddDate(0, 0, offset).Format("2006-01-02")
				if !gotDates[wantDate] {
					t.Errorf("generated dates = %v, missing %s", gotDates, wantDate)
				}
			}

			generatedAgain, err := scheduler.ForceGenerate(ruleID)
			if err != nil {
				t.Fatalf("generate recurrence instances again: %v", err)
			}
			if generatedAgain != 0 {
				t.Fatalf("second generation count = %d, want 0", generatedAgain)
			}
		})
	}
}

func TestRecurrenceSchedulerBoundsHighFrequencyRulePerPass(t *testing.T) {
	scheduler, recurrenceRepo, templateID, statusID, creatorID := recurrenceSchedulerFixture(t)
	start := time.Now().UTC().AddDate(0, 0, -1).Truncate(24 * time.Hour)
	end := start.AddDate(0, 0, MaxRecurrenceInstancesPerRulePass+20)
	ruleID, err := recurrenceRepo.Create(&models.RecurrenceRule{
		TemplateItemID:   templateID,
		WorkspaceID:      1,
		RRule:            "FREQ=DAILY",
		DtStart:          start,
		DtEnd:            &end,
		Timezone:         "UTC",
		LeadTimeDays:     MaxRecurrenceInstancesPerRulePass + 20,
		CopyAssignee:     true,
		CopyPriority:     true,
		CopyCustomFields: true,
		CopyDescription:  true,
		StatusOnCreate:   &statusID,
		IsActive:         true,
		CreatedBy:        &creatorID,
	})
	if err != nil {
		t.Fatalf("create high-frequency recurrence: %v", err)
	}

	generated, err := scheduler.ForceGenerate(ruleID)
	if err != nil {
		t.Fatalf("generate high-frequency recurrence: %v", err)
	}
	if generated != MaxRecurrenceInstancesPerRulePass {
		t.Fatalf("generated count = %d, want bounded batch %d", generated, MaxRecurrenceInstancesPerRulePass)
	}
	persisted, err := recurrenceRepo.GetByID(ruleID)
	if err != nil {
		t.Fatalf("get recurrence progress: %v", err)
	}
	if persisted.LastGeneratedUntil == nil || !persisted.LastGeneratedUntil.Before(end) {
		t.Fatalf("last_generated_until = %v, want progress before horizon %s", persisted.LastGeneratedUntil, end)
	}
}
