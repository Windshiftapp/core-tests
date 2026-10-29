//go:build test

package services

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

type slaSideEffectFixture struct {
	db          database.Database
	emitter     *SLASideEffectEmitter
	workspaceID int
	itemID      int
	metricID    int
}

func newSLASideEffectFixture(t *testing.T) *slaSideEffectFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	var workspaceID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('SLA FX', 'SLAFX') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	var itemID int
	if err := db.QueryRow(`INSERT INTO items (workspace_id, workspace_item_number, title, frac_index) VALUES (?, 7, 'Overdue ticket', ?) RETURNING id`,
		workspaceID, testutils.NextTestFracIndex()).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	var metricID int
	if err := db.QueryRow(`INSERT INTO sla_metrics (workspace_id, name, is_active) VALUES (?, 'First response', true) RETURNING id`, workspaceID).Scan(&metricID); err != nil {
		t.Fatalf("insert metric: %v", err)
	}

	return &slaSideEffectFixture{
		db: db, emitter: NewSLASideEffectEmitter(db),
		workspaceID: workspaceID, itemID: itemID, metricID: metricID,
	}
}

func (f *slaSideEffectFixture) enableNotification(t *testing.T, eventType string) {
	t.Helper()
	var settingID int
	if err := f.db.QueryRow(`INSERT INTO notification_settings (name) VALUES ('SLA setting') RETURNING id`).Scan(&settingID); err != nil {
		t.Fatalf("insert notification setting: %v", err)
	}
	if _, err := f.db.Exec(`INSERT INTO notification_event_rules (notification_setting_id, event_type, is_enabled, notify_workspace_admins) VALUES (?, ?, true, true)`, settingID, eventType); err != nil {
		t.Fatalf("insert notification rule: %v", err)
	}
}

func (f *slaSideEffectFixture) enableAction(t *testing.T, trigger string) {
	t.Helper()
	if _, err := f.db.Exec(`INSERT INTO actions (workspace_id, name, trigger_type, is_enabled) VALUES (?, 'Page on-call', ?, true)`, f.workspaceID, trigger); err != nil {
		t.Fatalf("insert action: %v", err)
	}
}

func (f *slaSideEffectFixture) cycle(breachedAt time.Time) *models.ItemSLACycle {
	return &models.ItemSLACycle{
		ID: 42, ItemID: f.itemID, MetricID: f.metricID, CycleNo: 1,
		Status: models.SLACycleOngoing, GoalDurationMs: 3_600_000, ElapsedMs: 4_000_000,
		BreachedAt: &breachedAt,
	}
}

func (f *slaSideEffectFixture) emitBreach(t *testing.T, cycle *models.ItemSLACycle) {
	t.Helper()
	if err := database.WithTx(f.db, func(tx database.Tx) error {
		return f.emitter.EmitBreach(context.Background(), tx, cycle)
	}); err != nil {
		t.Fatalf("EmitBreach: %v", err)
	}
}

func countDomainEvents(t *testing.T, db database.Database, eventType string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM domain_events WHERE event_type = ?`, eventType).Scan(&count); err != nil {
		t.Fatalf("count %s events: %v", eventType, err)
	}
	return count
}

func TestSLASideEffectEmitterEmitsConfiguredNotification(t *testing.T) {
	fixture := newSLASideEffectFixture(t)
	fixture.enableNotification(t, models.EventSLABreached)
	deadline := time.Date(2025, 1, 8, 13, 0, 0, 0, time.UTC)

	fixture.emitBreach(t, fixture.cycle(deadline))

	if got := countDomainEvents(t, fixture.db, DurableNotificationCompatibilityEvent); got != 1 {
		t.Fatalf("notification compatibility events = %d, want 1", got)
	}
	if got := countDomainEvents(t, fixture.db, DurableActionCompatibilityEvent); got != 0 {
		t.Fatalf("action compatibility events = %d, want 0 without a rule", got)
	}

	var payload []byte
	var occurredAt time.Time
	if err := fixture.db.QueryRow(`SELECT payload, occurred_at FROM domain_events WHERE event_type = ?`, DurableNotificationCompatibilityEvent).Scan(&payload, &occurredAt); err != nil {
		t.Fatalf("load notification event: %v", err)
	}
	var event NotificationEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("decode notification event: %v", err)
	}
	if event.EventType != models.EventSLABreached || event.ItemID != fixture.itemID {
		t.Fatalf("notification event = %#v", event)
	}
	if event.TemplateData["item.key"] != "SLAFX-7" {
		t.Fatalf("item.key = %v, want SLAFX-7", event.TemplateData["item.key"])
	}
	if !occurredAt.Equal(deadline) {
		t.Fatalf("occurred_at = %s, want the deadline %s", occurredAt, deadline)
	}
}

func TestSLASideEffectEmitterGatedWithoutConfiguration(t *testing.T) {
	fixture := newSLASideEffectFixture(t)
	fixture.emitBreach(t, fixture.cycle(time.Now().UTC()))

	if got := countDomainEvents(t, fixture.db, DurableNotificationCompatibilityEvent); got != 0 {
		t.Fatalf("notification events = %d, want 0", got)
	}
	if got := countDomainEvents(t, fixture.db, DurableActionCompatibilityEvent); got != 0 {
		t.Fatalf("action events = %d, want 0", got)
	}
}

func TestSLASideEffectEmitterEmitsConfiguredAction(t *testing.T) {
	fixture := newSLASideEffectFixture(t)
	fixture.enableAction(t, string(models.ActionTriggerSLABreached))

	fixture.emitBreach(t, fixture.cycle(time.Now().UTC()))

	if got := countDomainEvents(t, fixture.db, DurableActionCompatibilityEvent); got != 1 {
		t.Fatalf("action compatibility events = %d, want 1", got)
	}
	var payload []byte
	if err := fixture.db.QueryRow(`SELECT payload FROM domain_events WHERE event_type = ?`, DurableActionCompatibilityEvent).Scan(&payload); err != nil {
		t.Fatalf("load action event: %v", err)
	}
	var event models.ActionEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("decode action event: %v", err)
	}
	if event.EventType != models.ActionTriggerSLABreached || event.ItemID != fixture.itemID {
		t.Fatalf("action event = %#v", event)
	}
	if event.NewValues["metric_id"] != float64(fixture.metricID) {
		t.Fatalf("metric_id = %v, want %d", event.NewValues["metric_id"], fixture.metricID)
	}
}

func TestSLASideEffectEmitterWarningUsesWarningTriggers(t *testing.T) {
	fixture := newSLASideEffectFixture(t)
	fixture.enableNotification(t, models.EventSLAWarning)
	fixture.enableAction(t, string(models.ActionTriggerSLAWarning))

	if err := database.WithTx(fixture.db, func(tx database.Tx) error {
		return fixture.emitter.EmitWarning(context.Background(), tx, fixture.cycle(time.Now().UTC()), "75pct")
	}); err != nil {
		t.Fatalf("EmitWarning: %v", err)
	}
	if got := countDomainEvents(t, fixture.db, DurableNotificationCompatibilityEvent); got != 1 {
		t.Fatalf("warning notification events = %d, want 1", got)
	}
	var payload []byte
	if err := fixture.db.QueryRow(`SELECT payload FROM domain_events WHERE event_type = ?`, DurableActionCompatibilityEvent).Scan(&payload); err != nil {
		t.Fatalf("load warning action event: %v", err)
	}
	var event models.ActionEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("decode warning action event: %v", err)
	}
	if event.EventType != models.ActionTriggerSLAWarning {
		t.Fatalf("warning action trigger = %q", event.EventType)
	}
	if event.NewValues["threshold_key"] != "75pct" {
		t.Fatalf("threshold_key = %v, want 75pct", event.NewValues["threshold_key"])
	}
}
