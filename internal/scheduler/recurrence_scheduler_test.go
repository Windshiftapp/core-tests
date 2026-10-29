//go:build test

package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"windshift/internal/constants"
	"windshift/internal/events"
	"windshift/internal/itemevents"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/validation"
)

func createSingleOccurrenceRule(t *testing.T, scheduler *RecurrenceScheduler, recurrenceRepo *repository.RecurrenceRepository, templateID, statusID, creatorID int) int {
	t.Helper()
	start := time.Now().UTC().AddDate(0, 0, -1).Truncate(24 * time.Hour)
	ruleID, err := recurrenceRepo.Create(&models.RecurrenceRule{
		TemplateItemID: templateID,
		WorkspaceID:    1,
		RRule:          "FREQ=DAILY;COUNT=1",
		DtStart:        start,
		Timezone:       "UTC",
		LeadTimeDays:   2,
		StatusOnCreate: &statusID,
		IsActive:       true,
		CreatedBy:      &creatorID,
	})
	if err != nil {
		t.Fatalf("create recurrence rule: %v", err)
	}
	return ruleID
}

func generatedItemEvent(t *testing.T, scheduler *RecurrenceScheduler, recurrenceRepo *repository.RecurrenceRepository, ruleID int) events.Event {
	t.Helper()
	instances, err := recurrenceRepo.GetInstancesByRuleID(ruleID, 10, 0)
	if err != nil {
		t.Fatalf("list recurrence instances: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("recurrence instances = %d, want 1", len(instances))
	}
	itemID := instances[0].InstanceItemID
	var eventID int64
	if err := scheduler.db.QueryRow(`
		SELECT id FROM domain_events
		WHERE event_type = ? AND aggregate_type = 'item' AND aggregate_id = ?
	`, itemevents.Created, strconv.Itoa(itemID)).Scan(&eventID); err != nil {
		t.Fatalf("load generated item event: %v", err)
	}
	event, err := events.NewStore(scheduler.db).Event(context.Background(), eventID)
	if err != nil {
		t.Fatalf("load event %d: %v", eventID, err)
	}
	return *event
}

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

func TestRecurrenceSchedulerRejectsInvalidTaskTemplateState(t *testing.T) {
	scheduler, recurrenceRepo, templateID, statusID, creatorID := recurrenceSchedulerFixture(t)
	if _, err := scheduler.db.Exec(`UPDATE items SET is_task = true WHERE id = ?`, templateID); err != nil {
		t.Fatalf("install invalid legacy task template: %v", err)
	}
	start := time.Now().UTC().AddDate(0, 0, -1).Truncate(24 * time.Hour)
	ruleID, err := recurrenceRepo.Create(&models.RecurrenceRule{
		TemplateItemID: templateID,
		WorkspaceID:    1,
		RRule:          "FREQ=DAILY;COUNT=1",
		DtStart:        start,
		Timezone:       "UTC",
		LeadTimeDays:   2,
		StatusOnCreate: &statusID,
		IsActive:       true,
		CreatedBy:      &creatorID,
	})
	if err != nil {
		t.Fatalf("create recurrence rule: %v", err)
	}

	generated, err := scheduler.ForceGenerate(ruleID)
	var validationErr *validation.ValidationError
	if generated != 0 || !errors.As(err, &validationErr) || validationErr.Field != "is_task" {
		t.Fatalf("generation = %d, %v; want zero and is_task ValidationError", generated, err)
	}
	instances, err := recurrenceRepo.GetInstancesByRuleID(ruleID, 10, 0)
	if err != nil {
		t.Fatalf("list generated instances: %v", err)
	}
	if len(instances) != 0 {
		t.Fatalf("persisted recurrence instances = %d, want 0", len(instances))
	}
}

func TestRecurrenceSchedulerRecordsOneCanonicalCreatedEvent(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(*RecurrenceScheduler, int) error
	}{
		{
			name: "forced generation",
			run: func(scheduler *RecurrenceScheduler, ruleID int) error {
				generated, err := scheduler.ForceGenerate(ruleID)
				if err == nil && generated != 1 {
					return fmt.Errorf("generated count = %d, want 1", generated)
				}
				return err
			},
		},
		{
			name: "scheduled generation",
			run: func(scheduler *RecurrenceScheduler, _ int) error {
				scheduler.processRecurrenceRules()
				return nil
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			scheduler, recurrenceRepo, templateID, statusID, creatorID := recurrenceSchedulerFixture(t)
			ruleID := createSingleOccurrenceRule(t, scheduler, recurrenceRepo, templateID, statusID, creatorID)
			if err := test.run(scheduler, ruleID); err != nil {
				t.Fatalf("generate recurrence: %v", err)
			}

			event := generatedItemEvent(t, scheduler, recurrenceRepo, ruleID)
			if event.Type != itemevents.Created || event.WorkspaceID == nil || *event.WorkspaceID != 1 || event.ActorKind != "user" || event.ActorRef != strconv.Itoa(creatorID) || event.SourceKind != "recurrence" {
				t.Fatalf("created event metadata = type:%q workspace:%v actor:%s/%s source:%q", event.Type, event.WorkspaceID, event.ActorKind, event.ActorRef, event.SourceKind)
			}
			var payload itemevents.CreatedV1
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("decode created event: %v", err)
			}
			if payload.Item.ID == 0 || payload.Item.WorkspaceID != 1 || payload.Item.CreatorID == nil || *payload.Item.CreatorID != creatorID {
				t.Fatalf("created event item = %+v", payload.Item)
			}
			var count int
			if err := scheduler.db.QueryRow(`
				SELECT COUNT(*) FROM domain_events
				WHERE event_type = ? AND aggregate_type = 'item' AND aggregate_id = ?
			`, itemevents.Created, strconv.Itoa(payload.Item.ID)).Scan(&count); err != nil {
				t.Fatalf("count created events: %v", err)
			}
			if count != 1 {
				t.Fatalf("created event count = %d, want 1", count)
			}
		})
	}
}

func TestRecurrenceSchedulerRecordsCreationHistory(t *testing.T) {
	scheduler, recurrenceRepo, templateID, statusID, creatorID := recurrenceSchedulerFixture(t)
	ruleID := createSingleOccurrenceRule(t, scheduler, recurrenceRepo, templateID, statusID, creatorID)
	if _, err := scheduler.ForceGenerate(ruleID); err != nil {
		t.Fatalf("generate recurrence: %v", err)
	}

	instances, err := recurrenceRepo.GetInstancesByRuleID(ruleID, 10, 0)
	if err != nil {
		t.Fatalf("list recurrence instances: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("recurrence instances = %d, want 1", len(instances))
	}

	var actorKind string
	var userID sql.NullInt64
	if err := scheduler.db.QueryRow(`
		SELECT actor_kind, user_id FROM item_history
		WHERE item_id = ? AND field_name = 'title'
	`, instances[0].InstanceItemID).Scan(&actorKind, &userID); err != nil {
		t.Fatalf("load generated item creation history: %v", err)
	}
	if actorKind != "user" || !userID.Valid || int(userID.Int64) != creatorID {
		t.Fatalf("generated creation history actor = %q/%v, want user/%d", actorKind, userID, creatorID)
	}
}

func TestRecurrenceSchedulerRollsBackItemAndInstanceWhenEventAppendFails(t *testing.T) {
	scheduler, recurrenceRepo, templateID, statusID, creatorID := recurrenceSchedulerFixture(t)
	ruleID := createSingleOccurrenceRule(t, scheduler, recurrenceRepo, templateID, statusID, creatorID)
	var itemCountBefore int
	if err := scheduler.db.QueryRow("SELECT COUNT(*) FROM items").Scan(&itemCountBefore); err != nil {
		t.Fatalf("count items before generation: %v", err)
	}
	if err := installRecurrenceEventFailure(scheduler); err != nil {
		t.Fatalf("install event failure: %v", err)
	}

	generated, err := scheduler.ForceGenerate(ruleID)
	if err == nil || generated != 0 {
		t.Fatalf("generation = %d, %v; want zero and event append failure", generated, err)
	}
	var itemCountAfter, eventCount int
	if err := scheduler.db.QueryRow("SELECT COUNT(*) FROM items").Scan(&itemCountAfter); err != nil {
		t.Fatalf("count items after generation: %v", err)
	}
	if err := scheduler.db.QueryRow("SELECT COUNT(*) FROM domain_events WHERE event_type = ?", itemevents.Created).Scan(&eventCount); err != nil {
		t.Fatalf("count created events: %v", err)
	}
	instances, listErr := recurrenceRepo.GetInstancesByRuleID(ruleID, 10, 0)
	if listErr != nil {
		t.Fatalf("list recurrence instances: %v", listErr)
	}
	if itemCountAfter != itemCountBefore || eventCount != 0 || len(instances) != 0 {
		t.Fatalf("rolled-back rows = items:%d->%d events:%d instances:%d", itemCountBefore, itemCountAfter, eventCount, len(instances))
	}
}

func TestRecurrenceSchedulerCreatedEventReachesMatchingAutomation(t *testing.T) {
	scheduler, recurrenceRepo, templateID, statusID, creatorID := recurrenceSchedulerFixture(t)
	repo := repository.NewActionRepository(scheduler.db)
	actionID, err := repo.Create(&models.Action{
		WorkspaceID: 1,
		Name:        "Recurring item created",
		IsEnabled:   true,
		TriggerType: models.ActionTriggerItemCreated,
	})
	if err != nil {
		t.Fatalf("create action: %v", err)
	}
	actionService := services.NewActionService(scheduler.db, services.DefaultActionServiceConfig(), nil)
	t.Cleanup(actionService.Stop)
	actionService.InvalidateWorkspaceCache(1)
	ruleID := createSingleOccurrenceRule(t, scheduler, recurrenceRepo, templateID, statusID, creatorID)
	if generated, err := scheduler.ForceGenerate(ruleID); err != nil || generated != 1 {
		t.Fatalf("generation = %d, %v; want 1", generated, err)
	}
	event := generatedItemEvent(t, scheduler, recurrenceRepo, ruleID)
	if err := services.NewDurableActionConsumer(scheduler.db, actionService).Handle(context.Background(), event); err != nil {
		t.Fatalf("consume generated item event: %v", err)
	}
	var loggedActionID int
	if err := scheduler.db.QueryRow("SELECT action_id FROM action_execution_logs WHERE durable_event_key = ?", event.Key).Scan(&loggedActionID); err != nil {
		t.Fatalf("load action execution: %v", err)
	}
	if loggedActionID != actionID {
		t.Fatalf("executed action = %d, want %d", loggedActionID, actionID)
	}
}

func installRecurrenceEventFailure(scheduler *RecurrenceScheduler) error {
	if scheduler.db.GetDriverName() == "postgres" {
		if _, err := scheduler.db.Exec(`
			CREATE FUNCTION wi1261_fail_event() RETURNS trigger
			LANGUAGE plpgsql AS $function$
			BEGIN
				RAISE EXCEPTION 'forced recurrence event failure';
			END;
			$function$
		`); err != nil {
			return err
		}
		_, err := scheduler.db.Exec(`
			CREATE TRIGGER wi1261_fail_event
			BEFORE INSERT ON domain_events
			FOR EACH ROW EXECUTE FUNCTION wi1261_fail_event()
		`)
		return err
	}
	_, err := scheduler.db.Exec(`
		CREATE TRIGGER wi1261_fail_event
		BEFORE INSERT ON domain_events
		BEGIN
			SELECT RAISE(ABORT, 'forced recurrence event failure');
		END
	`)
	return err
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

// TestRecurrenceSchedulerGeneratesValidPersonalTask is the WI-1719 regression:
// generating an instance of an owner's personal-task recurrence rule must
// succeed. The scheduler used to revalidate with a zero user id, so every
// is_task rule failed to generate.
func TestRecurrenceSchedulerGeneratesValidPersonalTask(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	var personalWorkspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key, active, is_personal, owner_id)
		VALUES ('Personal tasks', 'PERSONAL-TASKS', true, true, ?)
		RETURNING id
	`, data.UserID).Scan(&personalWorkspaceID); err != nil {
		t.Fatalf("create personal workspace: %v", err)
	}

	openStatusID := constants.StatusIDOpen
	templateID, err := services.CreateItem(tdb.GetDatabase(), services.ItemCreationParams{
		WorkspaceID:      personalWorkspaceID,
		Title:            "Personal recurring template",
		StatusID:         &openStatusID,
		IsTask:           true,
		CreatorID:        &data.UserID,
		ValidatingUserID: data.UserID,
	})
	if err != nil {
		t.Fatalf("create personal task template: %v", err)
	}

	recurrenceRepo := repository.NewRecurrenceRepository(tdb.GetDatabase())
	scheduler := NewRecurrenceScheduler(tdb.GetDatabase(), services.NewWorkflowService(tdb.GetDatabase()))

	start := time.Now().UTC().AddDate(0, 0, -1).Truncate(24 * time.Hour)
	ruleID, err := recurrenceRepo.Create(&models.RecurrenceRule{
		TemplateItemID: int(templateID),
		WorkspaceID:    personalWorkspaceID,
		RRule:          "FREQ=DAILY;COUNT=1",
		DtStart:        start,
		Timezone:       "UTC",
		LeadTimeDays:   2,
		StatusOnCreate: &openStatusID,
		IsActive:       true,
		CreatedBy:      &data.UserID,
	})
	if err != nil {
		t.Fatalf("create recurrence rule: %v", err)
	}

	generated, err := scheduler.ForceGenerate(ruleID)
	if err != nil || generated != 1 {
		t.Fatalf("generate personal task = %d, %v; want 1, nil", generated, err)
	}

	instances, err := recurrenceRepo.GetInstancesByRuleID(ruleID, 10, 0)
	if err != nil {
		t.Fatalf("list recurrence instances: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("recurrence instances = %d, want 1", len(instances))
	}
	instanceItemID := instances[0].InstanceItemID

	var workspaceID, statusID, creatorID int
	var isTask bool
	if err := tdb.QueryRow(`
		SELECT workspace_id, status_id, creator_id, is_task FROM items WHERE id = ?
	`, instanceItemID).Scan(&workspaceID, &statusID, &creatorID, &isTask); err != nil {
		t.Fatalf("load generated task: %v", err)
	}
	if workspaceID != personalWorkspaceID || statusID != constants.StatusIDOpen || creatorID != data.UserID || !isTask {
		t.Fatalf("generated task = ws:%d status:%d creator:%d is_task:%t",
			workspaceID, statusID, creatorID, isTask)
	}

	var actorKind string
	var historyUserID sql.NullInt64
	if err := tdb.QueryRow(`
		SELECT actor_kind, user_id FROM item_history
		WHERE item_id = ? AND field_name = 'title'
	`, instanceItemID).Scan(&actorKind, &historyUserID); err != nil {
		t.Fatalf("load generated creation history: %v", err)
	}
	if actorKind != "user" || !historyUserID.Valid || int(historyUserID.Int64) != data.UserID {
		t.Fatalf("generated creation history actor = %q/%v, want user/%d", actorKind, historyUserID, data.UserID)
	}
}
