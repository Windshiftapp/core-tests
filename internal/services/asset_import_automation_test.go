//go:build test

package services

import (
	"context"
	"testing"

	"windshift/internal/assetevents"
	"windshift/internal/events"
	"windshift/internal/itemevents"
	"windshift/internal/testutils"
)

// TestImportedAssetRowDoesNotCreateAutomationExecutionLog asserts asset imports
// are historical: the canonical created fact carries import provenance and the
// durable consumer refuses to run automation for it.
func TestImportedAssetRowDoesNotCreateAutomationExecutionLog(t *testing.T) {
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
	if !itemevents.IsImportOrigin(event.ActorKind, event.SourceKind) {
		t.Fatalf("imported asset event origin = (%q, %q), want import", event.ActorKind, event.SourceKind)
	}
	if err := NewDurableAssetActionConsumer(db, actions).Handle(context.Background(), *event); err != nil {
		t.Fatalf("consume event: %v", err)
	}

	var executions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_action_execution_logs WHERE action_id = ?`, actionID).Scan(&executions); err != nil {
		t.Fatalf("count executions: %v", err)
	}
	if executions != 0 {
		t.Fatalf("imported asset created %d automation executions, want 0", executions)
	}
}
