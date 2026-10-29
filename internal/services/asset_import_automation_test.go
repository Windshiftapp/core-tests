//go:build test

package services

import (
	"context"
	"strings"
	"testing"

	"windshift/internal/assetevents"
	"windshift/internal/events"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

func TestImportedAssetRowCreatesAutomationExecutionLog(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	db := tdb.GetDatabase()
	insertID := func(label, query string, args ...any) int {
		t.Helper()
		var id int
		if err := db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		return id
	}
	userID := insertID("user", `INSERT INTO users (email, username, first_name, last_name) VALUES ('async-import@example.test', 'async-import', 'Async', 'Importer') RETURNING id`)
	setID := insertID("asset set", `INSERT INTO asset_management_sets (name, created_by) VALUES ('Async import assets', ?) RETURNING id`, userID)
	typeID := insertID("asset type", `INSERT INTO asset_types (set_id, name) VALUES (?, 'Server') RETURNING id`, setID)
	actionID := insertID("asset action", `INSERT INTO asset_actions (set_id, name, is_enabled, trigger_type, created_by) VALUES (?, 'Imported title condition', true, 'asset_created', ?) RETURNING id`, setID, userID)
	insertID("condition node", `INSERT INTO asset_action_nodes (action_id, node_type, node_config) VALUES (?, 'condition', '{"field_name":"asset_title","operator":"eq","value":"Imported async"}') RETURNING id`, actionID)

	actions := NewAssetActionService(db, DefaultActionServiceConfig(), nil)
	t.Cleanup(actions.Stop)
	app := NewAssetApplicationService(db, nil, nil, nil)
	seedRunningAssetImportJob(t, db, setID, userID, "async-job")
	input := StartAssetImport{AssetTypeID: typeID, Mappings: AssetImportMappings{Title: 0, Description: 1, AssetTag: 2, CategoryID: -1, StatusID: -1}}
	if err := app.importAssetCSVRow([]string{"Imported async", "Resolved async description", "ASYNC-1"}, setID, input, userID, nil, "async-job"); err != nil {
		t.Fatalf("importAssetCSVRow: %v", err)
	}
	actions.InvalidateSetCache(setID)
	var eventID int64
	if err := db.QueryRow("SELECT id FROM domain_events WHERE event_type = ? ORDER BY id DESC LIMIT 1", assetevents.Created).Scan(&eventID); err != nil {
		t.Fatalf("load event: %v", err)
	}
	event, err := events.NewStore(db).Event(context.Background(), eventID)
	if err != nil {
		t.Fatalf("load event: %v", err)
	}
	if err := NewDurableAssetActionConsumer(db, actions).Handle(context.Background(), *event); err != nil {
		t.Fatalf("consume event: %v", err)
	}
	var status models.ActionExecutionStatus
	var triggerEvent, trace string
	var actorID int
	if err := db.QueryRow(`SELECT l.status, l.trigger_event, l.execution_trace, a.created_by FROM asset_action_execution_logs l JOIN assets a ON a.id = l.asset_id WHERE l.action_id = ?`, actionID).Scan(&status, &triggerEvent, &trace, &actorID); err != nil {
		t.Fatalf("load execution: %v", err)
	}
	if status != models.ActionStatusCompleted || triggerEvent != string(models.AssetTriggerAssetCreated) || actorID != userID {
		t.Fatalf("execution = status %q, event %q, actor %d", status, triggerEvent, actorID)
	}
	if !strings.Contains(trace, `"condition_result":true`) || !strings.Contains(trace, `"field_value":"Imported async"`) {
		t.Fatalf("execution trace = %s", trace)
	}
}
