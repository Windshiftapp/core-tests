//go:build test

package services

import (
	"errors"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// seedIncidentTeam creates an active team and returns its id.
func seedIncidentTeam(t *testing.T, tdb *testutils.TestDB, name string) int {
	t.Helper()
	var id int
	if err := tdb.QueryRow(
		`INSERT INTO teams (name, description, is_active) VALUES (?, '', true) RETURNING id`,
		name,
	).Scan(&id); err != nil {
		t.Fatalf("seed team %q: %v", name, err)
	}
	return id
}

func TestIncidentTriggerRequiresItemTeam(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	id, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "No team item",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	svc := NewIncidentService(db, repository.NewOnCallRepository(db), repository.NewItemRepository(db), nil, nil, nil)
	if _, err := svc.Trigger(int(id), nil, "", ""); !errors.Is(err, ErrIncidentItemTeamRequired) {
		t.Fatalf("Trigger without a team error = %v, want ErrIncidentItemTeamRequired", err)
	}
}

func TestIncidentTriggerUsesTeamActivePolicy(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	onCallRepo := repository.NewOnCallRepository(db)
	itemRepo := repository.NewItemRepository(db)

	teamID := seedIncidentTeam(t, tdb, "Platform")
	policyID, err := onCallRepo.CreatePolicy(teamID, "Primary", "", 1, data.UserID)
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	itemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Page the platform team",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
		TeamID:      &teamID,
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	svc := NewIncidentService(db, onCallRepo, itemRepo, nil, nil, nil)
	incident, err := svc.Trigger(int(itemID), nil, "", "")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if incident.Status != "triggered" {
		t.Fatalf("incident.Status = %q, want triggered", incident.Status)
	}
	if incident.Urgency != "high" || incident.Source != "manual" {
		t.Fatalf("incident urgency/source = %q/%q, want high/manual", incident.Urgency, incident.Source)
	}
	if incident.EscalationPolicyID == nil || *incident.EscalationPolicyID != policyID {
		t.Fatalf("incident policy = %v, want %d", incident.EscalationPolicyID, policyID)
	}

	item, err := itemRepo.FindByIDWithDetails(int(itemID))
	if err != nil {
		t.Fatalf("FindByIDWithDetails: %v", err)
	}
	if item.IncidentID == nil || *item.IncidentID != incident.ID {
		t.Fatalf("item.IncidentID = %v, want %d", item.IncidentID, incident.ID)
	}
}

func TestIncidentTriggerWithoutPolicyIsRejected(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	teamID := seedIncidentTeam(t, tdb, "No policy team")
	itemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Team without a policy",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
		TeamID:      &teamID,
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	svc := NewIncidentService(db, repository.NewOnCallRepository(db), repository.NewItemRepository(db), nil, nil, nil)
	if _, err := svc.Trigger(int(itemID), nil, "", ""); !errors.Is(err, ErrIncidentPolicyUnknown) {
		t.Fatalf("Trigger without policy error = %v, want ErrIncidentPolicyUnknown", err)
	}
}

func TestIncidentTriggerRejectsForeignPolicy(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	onCallRepo := repository.NewOnCallRepository(db)

	itemTeamID := seedIncidentTeam(t, tdb, "Item team")
	otherTeamID := seedIncidentTeam(t, tdb, "Other team")
	foreignPolicy, err := onCallRepo.CreatePolicy(otherTeamID, "Foreign", "", 1, data.UserID)
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	itemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Foreign policy item",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
		TeamID:      &itemTeamID,
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	svc := NewIncidentService(db, onCallRepo, repository.NewItemRepository(db), nil, nil, nil)
	if _, err := svc.Trigger(int(itemID), &foreignPolicy, "", ""); !errors.Is(err, ErrIncidentPolicyUnknown) {
		t.Fatalf("Trigger with foreign policy error = %v, want ErrIncidentPolicyUnknown", err)
	}
}

func TestIncidentLifecycleAckUnackResolveAndRetrigger(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	onCallRepo := repository.NewOnCallRepository(db)
	itemRepo := repository.NewItemRepository(db)

	teamID := seedIncidentTeam(t, tdb, "Lifecycle team")
	if _, err := onCallRepo.CreatePolicy(teamID, "Primary", "", 1, data.UserID); err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
	itemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Lifecycle item",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
		TeamID:      &teamID,
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	svc := NewIncidentService(db, onCallRepo, itemRepo, nil, nil, nil)
	first, err := svc.Trigger(int(itemID), nil, "low", "automation")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if first.Urgency != "low" || first.Source != "automation" {
		t.Fatalf("incident urgency/source = %q/%q, want low/automation", first.Urgency, first.Source)
	}

	if _, err := svc.Trigger(int(itemID), nil, "", ""); !errors.Is(err, ErrIncidentAlreadyOpen) {
		t.Fatalf("second Trigger error = %v, want ErrIncidentAlreadyOpen", err)
	}

	if err := svc.Acknowledge(first.ID, data.UserID); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	acked, err := onCallRepo.GetIncidentByID(first.ID)
	if err != nil {
		t.Fatalf("GetIncidentByID: %v", err)
	}
	if acked.Status != "acknowledged" || acked.AcknowledgedBy == nil || *acked.AcknowledgedBy != data.UserID {
		t.Fatalf("incident status/acker = %q/%v, want acknowledged/%d", acked.Status, acked.AcknowledgedBy, data.UserID)
	}

	if err := svc.Unacknowledge(first.ID); err != nil {
		t.Fatalf("Unacknowledge: %v", err)
	}
	unacked, err := onCallRepo.GetIncidentByID(first.ID)
	if err != nil {
		t.Fatalf("GetIncidentByID: %v", err)
	}
	if unacked.Status != "triggered" || unacked.AcknowledgedBy != nil {
		t.Fatalf("incident status/acker = %q/%v, want triggered/nil", unacked.Status, unacked.AcknowledgedBy)
	}

	if err := svc.Resolve(first.ID, data.UserID); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	resolved, err := onCallRepo.GetIncidentByID(first.ID)
	if err != nil {
		t.Fatalf("GetIncidentByID: %v", err)
	}
	if resolved.Status != "resolved" || resolved.ResolvedBy == nil || *resolved.ResolvedBy != data.UserID {
		t.Fatalf("incident status/resolver = %q/%v, want resolved/%d", resolved.Status, resolved.ResolvedBy, data.UserID)
	}

	// A resolved incident does not block a fresh page; the item repoints at it.
	second, err := svc.Trigger(int(itemID), nil, "", "")
	if err != nil {
		t.Fatalf("re-Trigger: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("re-trigger reused incident id %d, want a new row", first.ID)
	}
	item, err := itemRepo.FindByIDWithDetails(int(itemID))
	if err != nil {
		t.Fatalf("FindByIDWithDetails: %v", err)
	}
	if item.IncidentID == nil || *item.IncidentID != second.ID {
		t.Fatalf("item.IncidentID = %v, want %d", item.IncidentID, second.ID)
	}

	// Old incident is retained as history.
	history, err := onCallRepo.GetIncidentByID(first.ID)
	if err != nil {
		t.Fatalf("GetIncidentByID(first): %v", err)
	}
	if history.Status != "resolved" {
		t.Fatalf("history status = %q, want resolved", history.Status)
	}
}

func TestListIncidentsScopesToCallerTeams(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	onCallRepo := repository.NewOnCallRepository(db)
	itemRepo := repository.NewItemRepository(db)

	visibleTeam := seedIncidentTeam(t, tdb, "Visible team")
	hiddenTeam := seedIncidentTeam(t, tdb, "Hidden team")
	for _, teamID := range []int{visibleTeam, hiddenTeam} {
		if _, err := onCallRepo.CreatePolicy(teamID, "Primary", "", 1, data.UserID); err != nil {
			t.Fatalf("CreatePolicy: %v", err)
		}
	}

	svc := NewIncidentService(db, onCallRepo, itemRepo, nil, nil, nil)
	for i, teamID := range []int{visibleTeam, hiddenTeam} {
		itemID, err := CreateItem(db, ItemCreationParams{
			WorkspaceID: data.WorkspaceID,
			Title:       "Scoped incident",
			StatusID:    &data.StatusID,
			CreatorID:   &data.UserID,
			TeamID:      &teamID,
		})
		if err != nil {
			t.Fatalf("CreateItem %d: %v", i, err)
		}
		if _, err := svc.Trigger(int(itemID), nil, "", ""); err != nil {
			t.Fatalf("Trigger %d: %v", i, err)
		}
	}

	visible, err := onCallRepo.ListIncidents(repository.IncidentFilter{
		TeamIDs:      []int{visibleTeam},
		WorkspaceIDs: []int{data.WorkspaceID},
	})
	if err != nil {
		t.Fatalf("ListIncidents: %v", err)
	}
	if len(visible) != 1 || visible[0].TeamID == nil || *visible[0].TeamID != visibleTeam {
		t.Fatalf("ListIncidents = %#v, want one incident for team %d", visible, visibleTeam)
	}

	all, err := onCallRepo.ListIncidents(repository.IncidentFilter{
		AllTeams:     true,
		WorkspaceIDs: []int{data.WorkspaceID},
	})
	if err != nil {
		t.Fatalf("ListIncidents all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListIncidents all = %d, want 2", len(all))
	}
}

func TestIncidentEscalationAdvancesAndExhausts(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	onCallRepo := repository.NewOnCallRepository(db)
	itemRepo := repository.NewItemRepository(db)

	teamID := seedIncidentTeam(t, tdb, "Escalation team")
	policyID, err := onCallRepo.CreatePolicy(teamID, "Two step", "", 1, data.UserID)
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
	if err := onCallRepo.SetEscalationRules(policyID, []models.EscalationRuleInput{
		{StepOrder: 0, EscalationDelayMinutes: 5, TargetType: "user", TargetID: data.UserID},
		{StepOrder: 1, EscalationDelayMinutes: 5, TargetType: "user", TargetID: data.UserID},
	}); err != nil {
		t.Fatalf("SetEscalationRules: %v", err)
	}

	itemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Escalating item",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
		TeamID:      &teamID,
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	svc := NewIncidentService(db, onCallRepo, itemRepo, nil, nil, nil)
	incident, err := svc.Trigger(int(itemID), nil, "", "")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if incident.EscalationStep != 0 || incident.NextEscalationAt == nil {
		t.Fatalf("trigger cursor = step %d, next %v; want step 0 with a deadline", incident.EscalationStep, incident.NextEscalationAt)
	}

	// Not yet due: AdvanceDue must not move the cursor.
	if err := svc.AdvanceDue(incident.ID); err != nil {
		t.Fatalf("AdvanceDue before deadline: %v", err)
	}
	current, _ := onCallRepo.GetIncidentByID(incident.ID)
	if current.EscalationStep != 0 {
		t.Fatalf("step after early AdvanceDue = %d, want 0", current.EscalationStep)
	}

	// Force the deadline into the past, then advance to step 1.
	backdateIncident(t, tdb, incident.ID)
	if err := svc.AdvanceDue(incident.ID); err != nil {
		t.Fatalf("AdvanceDue: %v", err)
	}
	current, _ = onCallRepo.GetIncidentByID(incident.ID)
	if current.EscalationStep != 1 || current.NextEscalationAt == nil {
		t.Fatalf("step after advance = %d, next %v; want step 1 with a deadline", current.EscalationStep, current.NextEscalationAt)
	}

	// Last step with repeat_count=1: the next due pass exhausts the chain.
	backdateIncident(t, tdb, incident.ID)
	if err := svc.AdvanceDue(incident.ID); err != nil {
		t.Fatalf("AdvanceDue exhaust: %v", err)
	}
	current, _ = onCallRepo.GetIncidentByID(incident.ID)
	if current.EscalationStep != 1 || current.NextEscalationAt != nil {
		t.Fatalf("exhausted cursor = step %d, next %v; want step 1 with no deadline", current.EscalationStep, current.NextEscalationAt)
	}

	// An acknowledged incident never escalates even if a deadline is due.
	backdateIncident(t, tdb, incident.ID)
	if err := svc.Acknowledge(incident.ID, data.UserID); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	if err := svc.AdvanceDue(incident.ID); err != nil {
		t.Fatalf("AdvanceDue on acked incident: %v", err)
	}
	current, _ = onCallRepo.GetIncidentByID(incident.ID)
	if current.Status != "acknowledged" {
		t.Fatalf("acked incident status = %q, want acknowledged", current.Status)
	}
}

func TestFindDueIncidentIDsReturnsTriggeredPastDeadline(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	onCallRepo := repository.NewOnCallRepository(db)
	itemRepo := repository.NewItemRepository(db)

	teamID := seedIncidentTeam(t, tdb, "Due team")
	if _, err := onCallRepo.CreatePolicy(teamID, "Primary", "", 1, data.UserID); err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
	itemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Due item",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
		TeamID:      &teamID,
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	svc := NewIncidentService(db, onCallRepo, itemRepo, nil, nil, nil)
	incident, err := svc.Trigger(int(itemID), nil, "", "")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}

	before, err := onCallRepo.FindDueIncidentIDs(time.Now(), 10)
	if err != nil {
		t.Fatalf("FindDueIncidentIDs before: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("due incidents before deadline = %v, want none", before)
	}

	backdateIncident(t, tdb, incident.ID)
	due, err := onCallRepo.FindDueIncidentIDs(time.Now(), 10)
	if err != nil {
		t.Fatalf("FindDueIncidentIDs: %v", err)
	}
	if len(due) != 1 || due[0] != incident.ID {
		t.Fatalf("due incidents = %v, want [%d]", due, incident.ID)
	}
}

// backdateIncident moves the incident's escalation deadline into the past so a
// sweeper pass can advance it without waiting on wall-clock time.
func backdateIncident(t *testing.T, tdb *testutils.TestDB, incidentID int) {
	t.Helper()
	if _, err := tdb.Exec(
		`UPDATE incidents SET next_escalation_at = ? WHERE id = ?`,
		time.Now().Add(-time.Minute), incidentID,
	); err != nil {
		t.Fatalf("backdate incident %d: %v", incidentID, err)
	}
}

// recordingWebhookDispatcher captures incident webhook dispatches.
type recordingWebhookDispatcher struct {
	events []string
}

func (d *recordingWebhookDispatcher) DispatchEvent(eventType string, _ *models.Item) {
	d.events = append(d.events, eventType)
}

func TestIncidentSchedulesDelayedAndRepeatedNotifications(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	onCallRepo := repository.NewOnCallRepository(db)
	itemRepo := repository.NewItemRepository(db)

	teamID := seedIncidentTeam(t, tdb, "Scheduling team")
	policyID, err := onCallRepo.CreatePolicy(teamID, "Scheduler", "", 1, data.UserID)
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
	interval := 10
	if err := onCallRepo.SetEscalationRules(policyID, []models.EscalationRuleInput{{
		StepOrder:              0,
		EscalationDelayMinutes: 5,
		TargetType:             "user",
		TargetID:               data.UserID,
		NotificationRules: []models.NotificationRuleInput{{
			NotificationType:      "in_app",
			DelayMinutes:          60,
			RepeatIntervalMinutes: &interval,
			RepeatCount:           3,
		}},
	}}); err != nil {
		t.Fatalf("SetEscalationRules: %v", err)
	}

	itemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Scheduled pages",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
		TeamID:      &teamID,
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	svc := NewIncidentService(db, onCallRepo, itemRepo, nil, nil, nil)
	incident, err := svc.Trigger(int(itemID), nil, "", "")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}

	rows, err := tdb.Query(
		`SELECT repeat_index, next_notification_at FROM incident_notification_state WHERE incident_id = ? ORDER BY repeat_index`,
		incident.ID,
	)
	if err != nil {
		t.Fatalf("query notification state: %v", err)
	}
	var indices []int
	var dueAts []time.Time
	for rows.Next() {
		var index int
		var nextAt time.Time
		if err := rows.Scan(&index, &nextAt); err != nil {
			t.Fatalf("scan notification state: %v", err)
		}
		indices = append(indices, index)
		dueAts = append(dueAts, nextAt)
	}
	_ = rows.Close()
	if len(indices) != 1 || indices[0] != 0 {
		t.Fatalf("scheduled notification rows = %v, want a single row at index 0", indices)
	}
	if !dueAts[0].After(time.Now().Add(50 * time.Minute)) {
		t.Fatalf("first scheduled send at %v, want after the 60-minute delay", dueAts[0])
	}

	// Ack must cancel every scheduled repeat.
	if err := svc.Acknowledge(incident.ID, data.UserID); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	var remaining int
	if err := tdb.QueryRow(
		`SELECT COUNT(*) FROM incident_notification_state WHERE incident_id = ?`,
		incident.ID,
	).Scan(&remaining); err != nil {
		t.Fatalf("count notification state: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("scheduled notifications after ack = %d, want 0", remaining)
	}
}

func TestIncidentDispatchDueNotificationAdvancesRepeat(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	onCallRepo := repository.NewOnCallRepository(db)
	itemRepo := repository.NewItemRepository(db)

	teamID := seedIncidentTeam(t, tdb, "Repeat team")
	policyID, err := onCallRepo.CreatePolicy(teamID, "Repeats", "", 1, data.UserID)
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
	interval := 15
	if err := onCallRepo.SetEscalationRules(policyID, []models.EscalationRuleInput{{
		StepOrder:              0,
		EscalationDelayMinutes: 5,
		TargetType:             "user",
		TargetID:               data.UserID,
		NotificationRules: []models.NotificationRuleInput{{
			NotificationType:      "in_app",
			DelayMinutes:          30,
			RepeatIntervalMinutes: &interval,
			RepeatCount:           2,
		}},
	}}); err != nil {
		t.Fatalf("SetEscalationRules: %v", err)
	}

	itemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Repeat pages",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
		TeamID:      &teamID,
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	svc := NewIncidentService(db, onCallRepo, itemRepo, nil, nil, nil)
	incident, err := svc.Trigger(int(itemID), nil, "", "")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}

	var stateID int
	if err := tdb.QueryRow(
		`SELECT id FROM incident_notification_state WHERE incident_id = ? AND repeat_index = 0`,
		incident.ID,
	).Scan(&stateID); err != nil {
		t.Fatalf("find first state: %v", err)
	}
	if _, err := tdb.Exec(
		`UPDATE incident_notification_state SET next_notification_at = ? WHERE id = ?`,
		time.Now().Add(-time.Minute), stateID,
	); err != nil {
		t.Fatalf("backdate state: %v", err)
	}

	if err := svc.DispatchDueNotification(stateID); err != nil {
		t.Fatalf("DispatchDueNotification: %v", err)
	}

	var repeatIndex int
	var nextAt time.Time
	if err := tdb.QueryRow(
		`SELECT repeat_index, next_notification_at FROM incident_notification_state WHERE id = ?`,
		stateID,
	).Scan(&repeatIndex, &nextAt); err != nil {
		t.Fatalf("reload state: %v", err)
	}
	if repeatIndex != 1 || !nextAt.After(time.Now()) {
		t.Fatalf("state after dispatch = index %d next %v, want index 1 in the future", repeatIndex, nextAt)
	}
}

func TestIncidentWebhookStepDispatches(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	onCallRepo := repository.NewOnCallRepository(db)
	itemRepo := repository.NewItemRepository(db)

	teamID := seedIncidentTeam(t, tdb, "Webhook team")
	policyID, err := onCallRepo.CreatePolicy(teamID, "Webhooks", "", 1, data.UserID)
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
	if err := onCallRepo.SetEscalationRules(policyID, []models.EscalationRuleInput{{
		StepOrder:              0,
		EscalationDelayMinutes: 5,
		TargetType:             "user",
		TargetID:               data.UserID,
		NotificationRules: []models.NotificationRuleInput{{
			NotificationType: "webhook",
			DelayMinutes:     0,
			RepeatCount:      1,
		}},
	}}); err != nil {
		t.Fatalf("SetEscalationRules: %v", err)
	}

	itemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Webhook pages",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
		TeamID:      &teamID,
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	dispatcher := &recordingWebhookDispatcher{}
	svc := NewIncidentService(db, onCallRepo, itemRepo, nil, nil, nil)
	svc.SetWebhookDispatcher(dispatcher)

	if _, err := svc.Trigger(int(itemID), nil, "", ""); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if len(dispatcher.events) != 1 || dispatcher.events[0] != "incident.triggered" {
		t.Fatalf("webhook events = %v, want [incident.triggered]", dispatcher.events)
	}
}
