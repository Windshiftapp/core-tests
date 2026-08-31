//go:build test

package services

import (
	"errors"
	"strings"
	"testing"
	"time"

	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

type recurrenceGeneratorSpy struct {
	ruleID int
	count  int
	err    error
}

func TestRecurrenceService_AuditsOnceInsideSharedService(t *testing.T) {
	_, tdb, data, itemID, generator := recurrenceServiceFixture(t)
	service := NewRecurrenceService(
		repository.NewRecurrenceRepository(tdb.GetDatabase()),
		generator,
		logger.NewAuditor(tdb.GetDatabase()),
	)
	actor := AuditActor{UserID: data.UserID, Username: "testuser", Source: "rest_v1", AuthMethod: "bearer"}

	if _, err := service.Create(itemID, data.WorkspaceID, data.UserID, models.CreateRecurrenceRequest{
		RRule: "FREQ=DAILY;COUNT=2", DtStart: "2026-08-01",
	}, actor); err != nil {
		t.Fatalf("create recurrence: %v", err)
	}
	timezone := "Europe/Zurich"
	if _, err := service.Update(itemID, models.UpdateRecurrenceRequest{Timezone: &timezone}, actor); err != nil {
		t.Fatalf("update recurrence: %v", err)
	}
	if _, err := service.ForceGenerate(itemID, actor); err != nil {
		t.Fatalf("force recurrence generation: %v", err)
	}
	if err := service.Delete(itemID, actor); err != nil {
		t.Fatalf("delete recurrence: %v", err)
	}

	for _, action := range []string{
		logger.ActionRecurrenceCreate,
		logger.ActionRecurrenceUpdate,
		logger.ActionRecurrenceForceGenerate,
		logger.ActionRecurrenceDelete,
	} {
		var count int
		if err := tdb.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action_type = ?`, action).Scan(&count); err != nil {
			t.Fatalf("count %s audits: %v", action, err)
		}
		if count != 1 {
			t.Fatalf("%s audit count = %d, want 1", action, count)
		}
	}
}

func (s *recurrenceGeneratorSpy) ForceGenerate(ruleID int) (int, error) {
	s.ruleID = ruleID
	return s.count, s.err
}

func recurrenceServiceFixture(t *testing.T) (*RecurrenceService, *testutils.TestDB, testutils.TestDataSet, int, *recurrenceGeneratorSpy) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	tx, err := tdb.Begin()
	if err != nil {
		t.Fatalf("begin item transaction: %v", err)
	}
	statusID, priorityID, creatorID := data.StatusID, data.PriorityID, data.UserID
	itemID, err := repository.NewItemRepository(tdb.GetDatabase()).Create(tx, &models.Item{
		WorkspaceID:         data.WorkspaceID,
		WorkspaceItemNumber: 1,
		Title:               "Recurring template",
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

	generator := &recurrenceGeneratorSpy{count: 3}
	service := NewRecurrenceService(repository.NewRecurrenceRepository(tdb.GetDatabase()), generator)
	return service, tdb, data, itemID, generator
}

func TestRecurrenceService_CreateAppliesSharedDefaultsAndSanitization(t *testing.T) {
	service, _, data, itemID, _ := recurrenceServiceFixture(t)

	rule, err := service.Create(itemID, data.WorkspaceID, data.UserID, models.CreateRecurrenceRequest{
		RRule:    "FREQ=WEEKLY;COUNT=3",
		DtStart:  "2026-08-01",
		Timezone: "<b>Europe/Zurich</b>",
	})
	if err != nil {
		t.Fatalf("create recurrence: %v", err)
	}

	if rule.TemplateItemID != itemID || rule.WorkspaceID != data.WorkspaceID {
		t.Fatalf("rule scope = item %d workspace %d, want item %d workspace %d", rule.TemplateItemID, rule.WorkspaceID, itemID, data.WorkspaceID)
	}
	if rule.Timezone != "Europe/Zurich" {
		t.Fatalf("timezone = %q, want sanitized Europe/Zurich", rule.Timezone)
	}
	if rule.LeadTimeDays != 14 || !rule.CopyAssignee || !rule.CopyPriority || !rule.CopyCustomFields || !rule.CopyDescription || !rule.IsActive {
		t.Fatalf("shared defaults not applied: %+v", rule)
	}
	if rule.CreatedBy == nil || *rule.CreatedBy != data.UserID {
		t.Fatalf("created_by = %v, want %d", rule.CreatedBy, data.UserID)
	}

	persisted, err := service.Get(itemID)
	if err != nil {
		t.Fatalf("get persisted recurrence: %v", err)
	}
	if persisted.ID != rule.ID || persisted.Timezone != rule.Timezone {
		t.Fatalf("persisted rule = %+v, want id=%d timezone=%q", persisted, rule.ID, rule.Timezone)
	}
}

func TestRecurrenceService_RejectsInvalidTimezoneOnCreateAndUpdate(t *testing.T) {
	service, _, data, itemID, _ := recurrenceServiceFixture(t)

	_, err := service.Create(itemID, data.WorkspaceID, data.UserID, models.CreateRecurrenceRequest{
		RRule: "FREQ=DAILY;COUNT=2", DtStart: "2026-08-01", Timezone: "Not/AZone",
	})
	if validationErr, ok := AsRecurrenceValidationError(err); !ok || validationErr.Kind != RecurrenceInvalidInput {
		t.Fatalf("create error = %v, want invalid-input recurrence error", err)
	}

	rule, err := service.Create(itemID, data.WorkspaceID, data.UserID, models.CreateRecurrenceRequest{
		RRule: "FREQ=DAILY;COUNT=2", DtStart: "2026-08-01", Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("create valid recurrence: %v", err)
	}
	invalid := "Local"
	if _, err := service.Update(itemID, models.UpdateRecurrenceRequest{Timezone: &invalid}); err == nil {
		t.Fatal("update accepted Local timezone")
	}
	persisted, err := service.Get(itemID)
	if err != nil {
		t.Fatalf("get recurrence: %v", err)
	}
	if persisted.Timezone != rule.Timezone {
		t.Fatalf("timezone changed to %q after rejected update, want %q", persisted.Timezone, rule.Timezone)
	}
}

func TestRecurrenceService_RejectsLeadTimeOutsideBoundedRange(t *testing.T) {
	service, _, data, itemID, _ := recurrenceServiceFixture(t)

	for _, leadTime := range []int{-1, 366} {
		_, err := service.Create(itemID, data.WorkspaceID, data.UserID, models.CreateRecurrenceRequest{
			RRule: "FREQ=DAILY;COUNT=2", DtStart: "2026-08-01", LeadTimeDays: &leadTime,
		})
		validationErr, ok := AsRecurrenceValidationError(err)
		if !ok || validationErr.Kind != RecurrenceInvalidInput {
			t.Fatalf("create lead_time_days=%d error = %v, want invalid-input recurrence error", leadTime, err)
		}
	}

	rule, err := service.Create(itemID, data.WorkspaceID, data.UserID, models.CreateRecurrenceRequest{
		RRule: "FREQ=DAILY;COUNT=2", DtStart: "2026-08-01",
	})
	if err != nil {
		t.Fatalf("create valid recurrence: %v", err)
	}
	tooLarge := 366
	if _, err := service.Update(itemID, models.UpdateRecurrenceRequest{LeadTimeDays: &tooLarge}); err == nil {
		t.Fatal("update accepted lead_time_days above the maximum")
	}
	persisted, err := service.Get(itemID)
	if err != nil {
		t.Fatalf("get recurrence after rejected update: %v", err)
	}
	if persisted.LeadTimeDays != rule.LeadTimeDays {
		t.Fatalf("lead_time_days changed to %d after rejected update, want %d", persisted.LeadTimeDays, rule.LeadTimeDays)
	}
}

func TestRecurrenceService_RejectsOverlengthRRuleWithoutSemanticTruncation(t *testing.T) {
	service, _, data, itemID, _ := recurrenceServiceFixture(t)

	_, err := service.Create(itemID, data.WorkspaceID, data.UserID, models.CreateRecurrenceRequest{
		RRule:   "FREQ=DAILY;" + strings.Repeat("X", 100),
		DtStart: "2026-08-01",
	})
	validationErr, ok := AsRecurrenceValidationError(err)
	if !ok {
		t.Fatalf("error = %v, want RecurrenceValidationError", err)
	}
	if validationErr.Kind != RecurrenceInvalidInput || validationErr.Message != "rrule exceeds the maximum length" {
		t.Fatalf("validation error = %+v", validationErr)
	}
	if _, getErr := service.Get(itemID); !errors.Is(getErr, repository.ErrNotFound) {
		t.Fatalf("overlength request persisted a rule: %v", getErr)
	}
}

func TestRecurrenceService_UpdateConflictAndForceGeneration(t *testing.T) {
	service, _, data, itemID, generator := recurrenceServiceFixture(t)
	rule, err := service.Create(itemID, data.WorkspaceID, data.UserID, models.CreateRecurrenceRequest{
		RRule:   "FREQ=DAILY;COUNT=2",
		DtStart: "2026-08-01",
	})
	if err != nil {
		t.Fatalf("create recurrence: %v", err)
	}

	if _, err := service.Create(itemID, data.WorkspaceID, data.UserID, models.CreateRecurrenceRequest{
		RRule: "FREQ=WEEKLY", DtStart: "2026-08-01",
	}); !errors.Is(err, ErrRecurrenceConflict) {
		t.Fatalf("duplicate create error = %v, want ErrRecurrenceConflict", err)
	}

	timezone, active := "America/New_York", false
	updated, err := service.Update(itemID, models.UpdateRecurrenceRequest{Timezone: &timezone, IsActive: &active})
	if err != nil {
		t.Fatalf("update recurrence: %v", err)
	}
	if updated.Timezone != timezone || updated.IsActive {
		t.Fatalf("updated rule = %+v", updated)
	}

	count, err := service.ForceGenerate(itemID)
	if err != nil {
		t.Fatalf("force generate: %v", err)
	}
	if count != generator.count || generator.ruleID != rule.ID {
		t.Fatalf("generator called with rule %d count %d, want rule %d count %d", generator.ruleID, count, rule.ID, generator.count)
	}
}

func TestRecurrenceService_PreviewBoundsUnendingRules(t *testing.T) {
	service := NewRecurrenceService(nil, nil)
	preview, err := service.Preview(models.RRulePreviewRequest{
		RRule:   "FREQ=MINUTELY",
		DtStart: "2026-08-01",
		Count:   3,
	})
	if err != nil {
		t.Fatalf("preview recurrence: %v", err)
	}
	if len(preview.Occurrences) != 3 {
		t.Fatalf("occurrence count = %d, want 3", len(preview.Occurrences))
	}
	for i := 1; i < len(preview.Occurrences); i++ {
		if preview.Occurrences[i].Sub(preview.Occurrences[i-1]) != time.Minute {
			t.Fatalf("occurrence delta %d = %s, want 1m", i, preview.Occurrences[i].Sub(preview.Occurrences[i-1]))
		}
	}
}

func TestRecurrenceService_PreviewHonorsRuleEndConditions(t *testing.T) {
	service := NewRecurrenceService(nil, nil)
	tests := []struct {
		name      string
		rrule     string
		wantDates []string
	}{
		{
			name:      "after occurrences stops at COUNT",
			rrule:     "FREQ=DAILY;COUNT=3",
			wantDates: []string{"2026-08-01", "2026-08-02", "2026-08-03"},
		},
		{
			name:      "on date includes and stops at UNTIL",
			rrule:     "FREQ=DAILY;UNTIL=20260803",
			wantDates: []string{"2026-08-01", "2026-08-02", "2026-08-03"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preview, err := service.Preview(models.RRulePreviewRequest{
				RRule:   tt.rrule,
				DtStart: "2026-08-01",
				Count:   10,
			})
			if err != nil {
				t.Fatalf("preview recurrence: %v", err)
			}
			if len(preview.Occurrences) != len(tt.wantDates) {
				t.Fatalf("occurrence count = %d, want %d", len(preview.Occurrences), len(tt.wantDates))
			}
			for i, occurrence := range preview.Occurrences {
				if got := occurrence.UTC().Format("2006-01-02"); got != tt.wantDates[i] {
					t.Errorf("occurrence %d = %s, want %s", i, got, tt.wantDates[i])
				}
			}
		})
	}
}

func TestRecurrenceService_EnforcesWorkspaceRuleLimitAndReleasesCapacity(t *testing.T) {
	service, tdb, data, firstItemID, _ := recurrenceServiceFixture(t)
	itemRepo := repository.NewItemRepository(tdb.GetDatabase())
	recurrenceRepo := repository.NewRecurrenceRepository(tdb.GetDatabase())
	itemIDs := make([]int, 0, MaxRecurrenceRulesPerWorkspace+1)
	itemIDs = append(itemIDs, firstItemID)

	tx, err := tdb.Begin()
	if err != nil {
		t.Fatalf("begin item transaction: %v", err)
	}
	for number := 2; number <= MaxRecurrenceRulesPerWorkspace+1; number++ {
		statusID, priorityID, creatorID := data.StatusID, data.PriorityID, data.UserID
		itemID, createErr := itemRepo.Create(tx, &models.Item{
			WorkspaceID:         data.WorkspaceID,
			WorkspaceItemNumber: number,
			Title:               "Recurring quota template",
			StatusID:            &statusID,
			PriorityID:          &priorityID,
			CreatorID:           &creatorID,
		})
		if createErr != nil {
			_ = tx.Rollback()
			t.Fatalf("create template item %d: %v", number, createErr)
		}
		itemIDs = append(itemIDs, itemID)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit template items: %v", err)
	}

	var firstRuleID int
	for i := 0; i < MaxRecurrenceRulesPerWorkspace; i++ {
		createdBy := data.UserID
		ruleID, createErr := recurrenceRepo.Create(&models.RecurrenceRule{
			TemplateItemID:   itemIDs[i],
			WorkspaceID:      data.WorkspaceID,
			RRule:            "FREQ=DAILY;COUNT=2",
			DtStart:          time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC),
			Timezone:         "UTC",
			LeadTimeDays:     14,
			CopyAssignee:     true,
			CopyPriority:     true,
			CopyCustomFields: true,
			CopyDescription:  true,
			IsActive:         true,
			CreatedBy:        &createdBy,
		})
		if createErr != nil {
			t.Fatalf("seed recurrence rule %d: %v", i+1, createErr)
		}
		if i == 0 {
			firstRuleID = ruleID
		}
	}

	_, err = service.Create(itemIDs[MaxRecurrenceRulesPerWorkspace], data.WorkspaceID, data.UserID, models.CreateRecurrenceRequest{
		RRule:   "FREQ=DAILY;COUNT=2",
		DtStart: "2026-08-01",
	})
	if !errors.Is(err, ErrRecurrenceWorkspaceLimit) {
		t.Fatalf("101st recurrence create error = %v, want ErrRecurrenceWorkspaceLimit", err)
	}
	if count, countErr := recurrenceRepo.CountByWorkspace(data.WorkspaceID); countErr != nil || count != MaxRecurrenceRulesPerWorkspace {
		t.Fatalf("workspace rule count = %d, error = %v, want %d", count, countErr, MaxRecurrenceRulesPerWorkspace)
	}

	if err := recurrenceRepo.Delete(firstRuleID); err != nil {
		t.Fatalf("delete recurrence rule to release capacity: %v", err)
	}
	created, err := service.Create(itemIDs[MaxRecurrenceRulesPerWorkspace], data.WorkspaceID, data.UserID, models.CreateRecurrenceRequest{
		RRule:   "FREQ=DAILY;COUNT=2",
		DtStart: "2026-08-01",
	})
	if err != nil {
		t.Fatalf("create recurrence after capacity released: %v", err)
	}
	if created.TemplateItemID != itemIDs[MaxRecurrenceRulesPerWorkspace] {
		t.Fatalf("created recurrence template = %d, want %d", created.TemplateItemID, itemIDs[MaxRecurrenceRulesPerWorkspace])
	}
}
