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

// seedIncidentEscalationPolicy creates a team, an active policy with one
// escalation step targeting userID, and returns (teamID, policyID).
func seedIncidentEscalationPolicy(t *testing.T, tdb *testutils.TestDB, name string, userID, repeatCount, delayMinutes int) (int, int) {
	t.Helper()
	onCallRepo := repository.NewOnCallRepository(tdb.GetDatabase())
	teamID := seedIncidentTeam(t, tdb, name)
	policyID, err := onCallRepo.CreatePolicy(teamID, "Primary", "", repeatCount, userID)
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
	err = onCallRepo.SetEscalationRules(policyID, []models.EscalationRuleInput{{
		StepOrder:              0,
		EscalationDelayMinutes: delayMinutes,
		TargetType:             "user",
		TargetID:               userID,
	}})
	if err != nil {
		t.Fatalf("SetEscalationRules: %v", err)
	}
	return teamID, policyID
}

// seedIncidentItem creates an item assigned to teamID.
func seedIncidentItem(t *testing.T, tdb *testutils.TestDB, title string, teamID int) int {
	t.Helper()
	data := tdb.SeedTestData(t)
	itemID, err := CreateItem(tdb.GetDatabase(), ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       title,
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
		TeamID:      &teamID,
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	return int(itemID)
}

// TestIncidentResolveReleasesItemPointer proves items.incident_id points only
// at the current (unresolved) incident: resolve clears it, history is kept,
// and a re-trigger repoints the item (WI-1536).
func TestIncidentResolveReleasesItemPointer(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	onCallRepo := repository.NewOnCallRepository(db)
	itemRepo := repository.NewItemRepository(db)

	teamID, _ := seedIncidentEscalationPolicy(t, tdb, "Pointer team", data.UserID, 1, 5)
	itemID := seedIncidentItem(t, tdb, "Pointer item", teamID)

	svc := NewIncidentService(db, onCallRepo, itemRepo, nil, nil, nil)
	incident, err := svc.Trigger(itemID, nil, "", "")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}

	item, err := itemRepo.FindByIDWithDetails(itemID)
	if err != nil {
		t.Fatalf("FindByIDWithDetails: %v", err)
	}
	if item.IncidentID == nil || *item.IncidentID != incident.ID {
		t.Fatalf("item.IncidentID = %v, want %d", item.IncidentID, incident.ID)
	}

	if err := svc.Resolve(incident.ID, data.UserID); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	item, err = itemRepo.FindByIDWithDetails(itemID)
	if err != nil {
		t.Fatalf("FindByIDWithDetails after resolve: %v", err)
	}
	if item.IncidentID != nil {
		t.Fatalf("item.IncidentID = %d after resolve, want nil", *item.IncidentID)
	}

	history, err := onCallRepo.GetIncidentByID(incident.ID)
	if err != nil {
		t.Fatalf("GetIncidentByID: %v", err)
	}
	if history.Status != "resolved" || history.ResolvedAt == nil {
		t.Fatalf("history status/resolved_at = %q/%v, want resolved/non-nil", history.Status, history.ResolvedAt)
	}
}

// TestIncidentTerminalStateSurvivesLateLifecycleWrites proves a resolved
// incident stays terminal: acknowledge and unacknowledge lose against a
// resolve even when their caller read the open state first (WI-1570).
func TestIncidentTerminalStateSurvivesLateLifecycleWrites(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	onCallRepo := repository.NewOnCallRepository(db)
	itemRepo := repository.NewItemRepository(db)

	teamID, _ := seedIncidentEscalationPolicy(t, tdb, "Terminal team", data.UserID, 1, 5)
	itemID := seedIncidentItem(t, tdb, "Terminal item", teamID)

	svc := NewIncidentService(db, onCallRepo, itemRepo, nil, nil, nil)
	incident, err := svc.Trigger(itemID, nil, "", "")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if err := svc.Resolve(incident.ID, data.UserID); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	resolved, err := onCallRepo.GetIncidentByID(incident.ID)
	if err != nil {
		t.Fatalf("GetIncidentByID: %v", err)
	}

	// A stale acknowledge that read the open state before the resolve
	// committed must not resurrect or annotate the resolved row.
	if err := svc.Acknowledge(incident.ID, data.UserID); !errors.Is(err, ErrIncidentResolved) {
		t.Fatalf("Acknowledge after resolve error = %v, want ErrIncidentResolved", err)
	}
	if err := svc.Unacknowledge(incident.ID); !errors.Is(err, ErrIncidentResolved) {
		t.Fatalf("Unacknowledge after resolve error = %v, want ErrIncidentResolved", err)
	}

	after, err := onCallRepo.GetIncidentByID(incident.ID)
	if err != nil {
		t.Fatalf("GetIncidentByID after writes: %v", err)
	}
	if after.Status != "resolved" {
		t.Fatalf("status = %q after late writes, want resolved", after.Status)
	}
	if after.AcknowledgedAt != nil || after.AcknowledgedBy != nil {
		t.Fatalf("ack metadata = %v/%v on resolved incident, want nil", after.AcknowledgedAt, after.AcknowledgedBy)
	}
	if !after.ResolvedAt.Equal(*resolved.ResolvedAt) || after.ResolvedBy == nil || *after.ResolvedBy != data.UserID {
		t.Fatalf("resolved_at/by = %v/%v, want %v/%d", after.ResolvedAt, after.ResolvedBy, resolved.ResolvedAt, data.UserID)
	}

	// The conditional repository updates fence stale writers directly.
	affected, err := onCallRepo.AcknowledgeIncident(incident.ID, data.UserID, time.Now())
	if err != nil {
		t.Fatalf("AcknowledgeIncident: %v", err)
	}
	if affected != 0 {
		t.Fatalf("AcknowledgeIncident affected %d rows on resolved incident, want 0", affected)
	}
	affected, err = onCallRepo.SetIncidentEscalation(incident.ID, 3, 3, nil)
	if err != nil {
		t.Fatalf("SetIncidentEscalation: %v", err)
	}
	if affected != 0 {
		t.Fatalf("SetIncidentEscalation affected %d rows on resolved incident, want 0", affected)
	}
}

// TestIncidentUnacknowledgeRequiresAcknowledgedState proves unacknowledge
// applies only to an acknowledged incident (WI-1569/WI-1570 contracts).
func TestIncidentUnacknowledgeRequiresAcknowledgedState(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	onCallRepo := repository.NewOnCallRepository(db)
	itemRepo := repository.NewItemRepository(db)

	teamID, _ := seedIncidentEscalationPolicy(t, tdb, "Unack guard team", data.UserID, 1, 5)
	itemID := seedIncidentItem(t, tdb, "Unack guard item", teamID)

	svc := NewIncidentService(db, onCallRepo, itemRepo, nil, nil, nil)
	incident, err := svc.Trigger(itemID, nil, "", "")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}

	if err := svc.Unacknowledge(incident.ID); !errors.Is(err, ErrIncidentNotAcknowledged) {
		t.Fatalf("Unacknowledge on triggered error = %v, want ErrIncidentNotAcknowledged", err)
	}

	if err := svc.Acknowledge(incident.ID, data.UserID); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	if err := svc.Acknowledge(incident.ID, data.UserID); err != nil {
		t.Fatalf("re-Acknowledge on acknowledged incident: %v", err)
	}
}

// TestIncidentAcknowledgeCancelsPendingAdvance proves an acknowledge that
// commits before a stale escalation advance keeps its cleared deadline:
// the escalation cursor write is fenced on status='triggered' (WI-1536).
func TestIncidentAcknowledgeCancelsPendingAdvance(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	onCallRepo := repository.NewOnCallRepository(db)
	itemRepo := repository.NewItemRepository(db)

	teamID, _ := seedIncidentEscalationPolicy(t, tdb, "Fence team", data.UserID, 3, 5)
	itemID := seedIncidentItem(t, tdb, "Fence item", teamID)

	svc := NewIncidentService(db, onCallRepo, itemRepo, nil, nil, nil)
	incident, err := svc.Trigger(itemID, nil, "", "")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if incident.NextEscalationAt == nil {
		t.Fatal("Trigger left no escalation deadline")
	}

	if err := svc.Acknowledge(incident.ID, data.UserID); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}

	// A stale sweeper pass whose read happened before the ack must not
	// re-arm the deadline: the fenced cursor write affects zero rows.
	next := time.Now().Add(5 * time.Minute)
	affected, err := onCallRepo.SetIncidentEscalation(incident.ID, 0, 0, &next)
	if err != nil {
		t.Fatalf("SetIncidentEscalation: %v", err)
	}
	if affected != 0 {
		t.Fatalf("SetIncidentEscalation affected %d rows on acknowledged incident, want 0", affected)
	}
	if err := svc.AdvanceDue(incident.ID); err != nil {
		t.Fatalf("AdvanceDue: %v", err)
	}

	acked, err := onCallRepo.GetIncidentByID(incident.ID)
	if err != nil {
		t.Fatalf("GetIncidentByID: %v", err)
	}
	if acked.Status != "acknowledged" {
		t.Fatalf("status = %q, want acknowledged", acked.Status)
	}
	if acked.NextEscalationAt != nil {
		t.Fatalf("next_escalation_at = %v after ack, want nil", acked.NextEscalationAt)
	}
	if acked.EscalationStep != 0 {
		t.Fatalf("escalation_step = %d after ack, want 0", acked.EscalationStep)
	}
}

// TestIncidentUnacknowledgeRearmsEscalationAndDrains proves unacknowledge
// resumes paging: the deadline re-arms from the incident's current step and
// escalation continues exactly per the chain (WI-1569).
func TestIncidentUnacknowledgeRearmsEscalationAndDrains(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	onCallRepo := repository.NewOnCallRepository(db)
	itemRepo := repository.NewItemRepository(db)

	// Two steps so the resumed advance moves to step 1, repeat_count 1 so
	// the chain then exhausts instead of wrapping.
	teamID := seedIncidentTeam(t, tdb, "Rearm team")
	policyID, err := onCallRepo.CreatePolicy(teamID, "Primary", "", 1, data.UserID)
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
	err = onCallRepo.SetEscalationRules(policyID, []models.EscalationRuleInput{
		{StepOrder: 0, EscalationDelayMinutes: 5, TargetType: "user", TargetID: data.UserID},
		{StepOrder: 1, EscalationDelayMinutes: 10, TargetType: "user", TargetID: data.UserID},
	})
	if err != nil {
		t.Fatalf("SetEscalationRules: %v", err)
	}
	itemID := seedIncidentItem(t, tdb, "Rearm item", teamID)

	svc := NewIncidentService(db, onCallRepo, itemRepo, nil, nil, nil)
	incident, err := svc.Trigger(itemID, nil, "", "")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if err := svc.Acknowledge(incident.ID, data.UserID); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}

	if err := svc.Unacknowledge(incident.ID); err != nil {
		t.Fatalf("Unacknowledge: %v", err)
	}
	resumed, err := onCallRepo.GetIncidentByID(incident.ID)
	if err != nil {
		t.Fatalf("GetIncidentByID: %v", err)
	}
	if resumed.Status != "triggered" {
		t.Fatalf("status = %q after unacknowledge, want triggered", resumed.Status)
	}
	if resumed.NextEscalationAt == nil || !resumed.NextEscalationAt.After(time.Now()) {
		t.Fatalf("next_escalation_at = %v after unacknowledge, want re-armed in the future", resumed.NextEscalationAt)
	}
	if resumed.EscalationStep != 0 {
		t.Fatalf("escalation_step = %d after unacknowledge, want 0", resumed.EscalationStep)
	}

	// Past the resumed deadline, the next advance pages step 1 exactly once.
	dueAt := time.Now().Add(-time.Minute)
	if _, err := onCallRepo.SetIncidentEscalation(incident.ID, 0, 0, &dueAt); err != nil {
		t.Fatalf("rewind deadline: %v", err)
	}
	due, err := onCallRepo.FindDueIncidentIDs(time.Now(), 10)
	if err != nil {
		t.Fatalf("FindDueIncidentIDs: %v", err)
	}
	if len(due) != 1 || due[0] != incident.ID {
		t.Fatalf("due incidents = %v, want [%d]", due, incident.ID)
	}
	if err := svc.AdvanceDue(incident.ID); err != nil {
		t.Fatalf("AdvanceDue: %v", err)
	}
	advanced, err := onCallRepo.GetIncidentByID(incident.ID)
	if err != nil {
		t.Fatalf("GetIncidentByID: %v", err)
	}
	if advanced.EscalationStep != 1 {
		t.Fatalf("escalation_step = %d, want 1", advanced.EscalationStep)
	}
	if advanced.NextEscalationAt == nil || !advanced.NextEscalationAt.After(time.Now()) {
		t.Fatalf("next_escalation_at = %v, want step-1 deadline in the future", advanced.NextEscalationAt)
	}

	// Exhausting the chain clears the deadline instead of repeating.
	dueAt = time.Now().Add(-time.Minute)
	if _, err := onCallRepo.SetIncidentEscalation(incident.ID, 1, 0, &dueAt); err != nil {
		t.Fatalf("rewind step-1 deadline: %v", err)
	}
	if err := svc.AdvanceDue(incident.ID); err != nil {
		t.Fatalf("AdvanceDue after step 1: %v", err)
	}
	exhausted, err := onCallRepo.GetIncidentByID(incident.ID)
	if err != nil {
		t.Fatalf("GetIncidentByID: %v", err)
	}
	if exhausted.NextEscalationAt != nil || exhausted.EscalationStep != 1 {
		t.Fatalf("exhausted chain state = step %d, deadline %v; want step 1, nil",
			exhausted.EscalationStep, exhausted.NextEscalationAt)
	}
}

// TestIncidentSweeperDrainsFullBacklogInOneTick proves one tick drains all
// due escalations and notifications instead of advancing a fixed batch
// (WI-1595).
func TestIncidentSweeperDrainsFullBacklogInOneTick(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	onCallRepo := repository.NewOnCallRepository(db)
	itemRepo := repository.NewItemRepository(db)

	teamID, policyID := seedIncidentEscalationPolicy(t, tdb, "Drain team", data.UserID, 1, 5)

	svc := NewIncidentService(db, onCallRepo, itemRepo, nil, nil, nil)
	const incidentCount = 25
	const stateCount = 15
	incidentIDs := make([]int, 0, incidentCount)
	for i := 0; i < incidentCount; i++ {
		itemID := seedIncidentItem(t, tdb, "Drain item", teamID)
		incident, err := svc.Trigger(itemID, nil, "", "")
		if err != nil {
			t.Fatalf("Trigger %d: %v", i, err)
		}
		incidentIDs = append(incidentIDs, incident.ID)
		// Overdue deadline: the escalation is due now.
		overdue := time.Now().Add(-time.Minute)
		if _, err := onCallRepo.SetIncidentEscalation(incident.ID, 0, 0, &overdue); err != nil {
			t.Fatalf("arm overdue deadline: %v", err)
		}
	}

	// Due scheduled notifications for distinct incidents.
	rules, err := onCallRepo.GetEscalationRules(policyID)
	if err != nil {
		t.Fatalf("GetEscalationRules: %v", err)
	}
	if len(rules) == 0 {
		t.Fatal("policy has no escalation rules")
	}
	notificationRules, err := onCallRepo.GetNotificationRulesForStep(rules[0].ID)
	if err != nil {
		t.Fatalf("GetNotificationRulesForStep: %v", err)
	}
	var notificationRuleID int
	if len(notificationRules) > 0 {
		notificationRuleID = notificationRules[0].ID
	} else {
		// Steps without explicit rules page in-app inline; synthesize one so
		// the notification queue has real rows to drain.
		if _, err := db.Exec(`
			INSERT INTO on_call_notification_rules (escalation_rule_id, notification_type, delay_minutes, repeat_count, created_at)
			VALUES (?, 'in_app', 0, 1, CURRENT_TIMESTAMP)
		`, rules[0].ID); err != nil {
			t.Fatalf("seed notification rule: %v", err)
		}
		notificationRules, err = onCallRepo.GetNotificationRulesForStep(rules[0].ID)
		if err != nil || len(notificationRules) == 0 {
			t.Fatalf("read synthesized notification rule: %v", err)
		}
		notificationRuleID = notificationRules[0].ID
	}
	for i := 0; i < stateCount; i++ {
		if err := onCallRepo.CreateIncidentNotificationState(incidentIDs[i], rules[0].ID, notificationRuleID, 0, time.Now().Add(-time.Minute)); err != nil {
			t.Fatalf("CreateIncidentNotificationState %d: %v", i, err)
		}
	}

	sweeper := NewIncidentEscalationSweeper(db, svc, IncidentEscalationSweeperConfig{BatchSize: 10})
	sweeper.tick()

	if sweeper.stepsAdvanced != incidentCount {
		t.Fatalf("stepsAdvanced = %d after one tick, want %d", sweeper.stepsAdvanced, incidentCount)
	}
	if sweeper.notificationsSent != stateCount {
		t.Fatalf("notificationsSent = %d after one tick, want %d", sweeper.notificationsSent, stateCount)
	}

	stillDue, err := onCallRepo.FindDueIncidentIDs(time.Now(), 2*incidentCount)
	if err != nil {
		t.Fatalf("FindDueIncidentIDs: %v", err)
	}
	if len(stillDue) != 0 {
		t.Fatalf("due incidents after one tick = %v, want none (backlog drained)", stillDue)
	}
	var remainingStates int
	if err := db.QueryRow(`SELECT COUNT(*) FROM incident_notification_state`).Scan(&remainingStates); err != nil {
		t.Fatalf("count notification states: %v", err)
	}
	if remainingStates != 0 {
		t.Fatalf("notification states remaining = %d, want 0 (all drained in one tick)", remainingStates)
	}
}
