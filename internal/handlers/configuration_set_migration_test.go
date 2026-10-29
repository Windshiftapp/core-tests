//go:build test

package handlers

import (
	"net/http"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
	"windshift/internal/testutils/mocks"
)

// --- helpers used only by migration tests ---------------------------------

// createTestStatus inserts a status row and returns its ID. The test schema
// has a default status_category, so we look it up rather than creating one.
func createTestStatus(t *testing.T, tdb *testutils.TestDB, name string) int {
	t.Helper()
	var categoryID int
	if err := tdb.QueryRow(`SELECT id FROM status_categories WHERE is_default = true LIMIT 1`).Scan(&categoryID); err != nil {
		t.Fatalf("Failed to find default status category: %v", err)
	}
	var id int64
	if err := tdb.QueryRow(`
		INSERT INTO statuses (name, description, category_id, is_default, created_at, updated_at)
		VALUES (?, '', ?, false, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) RETURNING id
	`, name, categoryID).Scan(&id); err != nil {
		t.Fatalf("Failed to create status %q: %v", name, err)
	}
	return int(id)
}

// linkStatusToWorkflow inserts a workflow_transitions row so the status
// becomes reachable from the analyzer's workflow-status query.
func linkStatusToWorkflow(t *testing.T, tdb *testutils.TestDB, workflowID, statusID int) {
	t.Helper()
	if _, err := tdb.Exec(`
		INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id, display_order, created_at)
		VALUES (?, NULL, ?, 0, CURRENT_TIMESTAMP)
	`, workflowID, statusID); err != nil {
		t.Fatalf("Failed to link status %d to workflow %d: %v", statusID, workflowID, err)
	}
}

// insertItem inserts a plain work item with the given fields. Pass statusID=0
// for a NULL status_id — the migration analyzer specifically addresses
// legacy "no status" rows the production create path cannot produce, so these
// fixtures bypass the create path on purpose; each carries an explicit unique
// canonical rank.
func insertItem(t *testing.T, tdb *testutils.TestDB, workspaceID, statusID, workspaceItemNumber int) int {
	t.Helper()
	var statusArg interface{}
	if statusID == 0 {
		statusArg = nil
	} else {
		statusArg = statusID
	}
	var id int64
	if err := tdb.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, status_id, frac_index, created_at, updated_at)
		VALUES (?, ?, 'Test Item', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) RETURNING id
	`, workspaceID, workspaceItemNumber, statusArg, testutils.NextTestFracIndex()).Scan(&id); err != nil {
		t.Fatalf("Failed to insert item: %v", err)
	}
	return int(id)
}

func createMigrationItemType(t *testing.T, tdb *testutils.TestDB, name string, hierarchyLevel int) int {
	t.Helper()
	var id int64
	if err := tdb.QueryRow(`
		INSERT INTO item_types (
			name, description, is_default, icon, color, hierarchy_level, sort_order,
			created_at, updated_at
		)
		VALUES (?, '', false, 'Circle', '#2563eb', ?, 100, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) RETURNING id
	`, name, hierarchyLevel).Scan(&id); err != nil {
		t.Fatalf("Failed to create item type %q: %v", name, err)
	}
	return int(id)
}

func insertTypedItem(t *testing.T, tdb *testutils.TestDB, workspaceID, itemTypeID, workspaceItemNumber int) int {
	t.Helper()
	var id int64
	// The typed rows are also migration-analysis fixtures (custom item types
	// staged before any config-set link exists); rank them explicitly.
	if err := tdb.QueryRow(`
		INSERT INTO items (
			workspace_id, workspace_item_number, item_type_id, title, frac_index, created_at, updated_at
		)
		VALUES (?, ?, ?, 'Typed migration item', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) RETURNING id
	`, workspaceID, workspaceItemNumber, itemTypeID, testutils.NextTestFracIndex()).Scan(&id); err != nil {
		t.Fatalf("Failed to insert typed item: %v", err)
	}
	return int(id)
}

// attachWorkspaceToConfigSet inserts a workspace_configuration_sets row and
// fails the test if it cannot.
func attachWorkspaceToConfigSet(t *testing.T, tdb *testutils.TestDB, workspaceID, configSetID int) {
	t.Helper()
	if _, err := tdb.Exec(`
		INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id, created_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
	`, workspaceID, configSetID); err != nil {
		t.Fatalf("Failed to attach workspace %d to config set %d: %v", workspaceID, configSetID, err)
	}
}

func newConfigSetTestHandler(tdb *testutils.TestDB) *ConfigurationSetHandler {
	mockNotificationService := mocks.CreateMockNotificationService()
	return NewConfigurationSetHandler(tdb.GetDatabase(), mockNotificationService, nil)
}

// --- C1: ListWorkspaceIDsForConfigSet -------------------------------------

func TestConfigurationSetRepository_ListWorkspaceIDsForConfigSet(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	wsA := createTestWorkspace(t, tdb, "WS A", "WSA")
	wsB := createTestWorkspace(t, tdb, "WS B", "WSB")
	wsC := createTestWorkspace(t, tdb, "WS C", "WSC")

	var setID int
	if err := tdb.QueryRow(`
		INSERT INTO configuration_sets (name, description, is_default, created_at, updated_at)
		VALUES ('Set X', '', false, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) RETURNING id
	`).Scan(&setID); err != nil {
		t.Fatalf("Failed to create config set: %v", err)
	}
	configSetID := setID

	attachWorkspaceToConfigSet(t, tdb, wsA, configSetID)
	attachWorkspaceToConfigSet(t, tdb, wsB, configSetID)
	attachWorkspaceToConfigSet(t, tdb, wsC, configSetID)

	repo := repository.NewConfigurationSetRepository(tdb.GetDatabase())
	got, err := repo.ListWorkspaceIDsForConfigSet(configSetID)
	if err != nil {
		t.Fatalf("ListWorkspaceIDsForConfigSet returned error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("Expected 3 workspaces, got %d (%v)", len(got), got)
	}
	want := map[int]bool{wsA: true, wsB: true, wsC: true}
	for _, id := range got {
		if !want[id] {
			t.Errorf("Unexpected workspace ID in result: %d", id)
		}
	}
}

// --- C2: workflow change inside the same config set requires migration ----

func TestConfigurationSetHandler_Update_WorkflowChangeRequiresMigration(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	workspaceID := createTestWorkspace(t, tdb, "Test Workspace", "WS")
	workflowOldID := createTestWorkflow(t, tdb, "Workflow Old")
	workflowNewID := createTestWorkflow(t, tdb, "Workflow New")

	// Old workflow contains "Open Old"; new workflow contains a different
	// status whose name doesn't match — items on "Open Old" must be flagged
	// for migration.
	openOld := createTestStatus(t, tdb, "Open Old")
	doneNew := createTestStatus(t, tdb, "Done New")
	linkStatusToWorkflow(t, tdb, workflowOldID, openOld)
	linkStatusToWorkflow(t, tdb, workflowNewID, doneNew)

	var configSetID int64
	if err := tdb.QueryRow(`
		INSERT INTO configuration_sets (name, description, is_default, workflow_id, created_at, updated_at)
		VALUES ('Set X', '', false, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) RETURNING id
	`, workflowOldID).Scan(&configSetID); err != nil {
		t.Fatalf("Failed to create config set: %v", err)
	}
	attachWorkspaceToConfigSet(t, tdb, workspaceID, int(configSetID))

	// Plant 3 items in this workspace using the old-workflow status.
	for i := 1; i <= 3; i++ {
		insertItem(t, tdb, workspaceID, openOld, i)
	}

	// PUT the same config set, changing only workflow_id to a different
	// workflow that does NOT contain the items' current status.
	handler := newConfigSetTestHandler(tdb)
	updated := models.ConfigurationSet{
		Name:         "Set X",
		WorkflowID:   &workflowNewID,
		WorkspaceIDs: []int{workspaceID},
	}
	req := testutils.CreateJSONRequest(t, "PUT", "/api/configuration-sets/"+testutils.IntToString(int(configSetID)), updated)
	req.SetPathValue("id", testutils.IntToString(int(configSetID)))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Update, req, nil)

	rr.AssertStatusCode(http.StatusConflict)

	var body map[string]interface{}
	rr.AssertJSONResponse(&body)
	if body["error"] != "migration_required" {
		t.Errorf("Expected error=migration_required, got %v", body["error"])
	}
	analysis, ok := body["analysis"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected analysis object in 409 body; got %v", body)
	}
	if analysis["requires_status_migration"] != true {
		t.Errorf("Expected requires_status_migration=true, got %v", analysis["requires_status_migration"])
	}
	statusMigrations, _ := analysis["status_migrations"].([]interface{})
	if len(statusMigrations) == 0 {
		t.Errorf("Expected at least one status migration entry, got none")
	}
}

func TestConfigurationSetHandler_Update_ItemTypeRemovalRequiresSameLevelMigration(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	workspaceID := createTestWorkspace(t, tdb, "Item Type Migration", "ITM")
	sourceTypeID := createMigrationItemType(t, tdb, "Removal Source", 3)
	sameLevelTargetID := createMigrationItemType(t, tdb, "Removal Target", 3)
	otherLevelTargetID := createMigrationItemType(t, tdb, "Wrong Level Target", 2)

	var configSetID int
	if err := tdb.QueryRow(`
		INSERT INTO configuration_sets (name, description, is_default, created_at, updated_at)
		VALUES ('Item Type Set', '', false, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) RETURNING id
	`).Scan(&configSetID); err != nil {
		t.Fatalf("Failed to create config set: %v", err)
	}
	attachWorkspaceToConfigSet(t, tdb, workspaceID, configSetID)
	for _, itemTypeID := range []int{sourceTypeID, sameLevelTargetID, otherLevelTargetID} {
		if _, err := tdb.Exec(`
			INSERT INTO configuration_set_item_types (configuration_set_id, item_type_id, created_at)
			VALUES (?, ?, CURRENT_TIMESTAMP)
		`, configSetID, itemTypeID); err != nil {
			t.Fatalf("Failed to configure item type %d: %v", itemTypeID, err)
		}
	}
	insertTypedItem(t, tdb, workspaceID, sourceTypeID, 1)

	handler := newConfigSetTestHandler(tdb)
	updated := models.ConfigurationSet{
		Name:         "Item Type Set",
		WorkspaceIDs: []int{workspaceID},
		ItemTypeConfigs: []models.ItemTypeConfig{
			{ItemTypeID: sameLevelTargetID},
			{ItemTypeID: otherLevelTargetID},
		},
	}
	req := testutils.CreateJSONRequest(t, "PUT", "/api/configuration-sets/"+testutils.IntToString(configSetID), updated)
	req.SetPathValue("id", testutils.IntToString(configSetID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Update, req, nil)
	rr.AssertStatusCode(http.StatusConflict)

	var body struct {
		Error    string                                `json:"error"`
		Analysis models.ComprehensiveMigrationAnalysis `json:"analysis"`
	}
	rr.AssertJSONResponse(&body)
	if body.Error != "migration_required" {
		t.Fatalf("Expected migration_required, got %q", body.Error)
	}
	if !body.Analysis.RequiresItemTypeMigration || len(body.Analysis.ItemTypeMigrations) != 1 {
		t.Fatalf("Expected one required item type migration, got %+v", body.Analysis)
	}
	migration := body.Analysis.ItemTypeMigrations[0]
	if migration.CurrentItemTypeID == nil || *migration.CurrentItemTypeID != sourceTypeID {
		t.Fatalf("Expected source item type %d, got %+v", sourceTypeID, migration.CurrentItemTypeID)
	}
	if len(migration.AvailableTargets) != 1 || migration.AvailableTargets[0].ID != sameLevelTargetID {
		t.Fatalf("Expected only same-level target %d, got %+v", sameLevelTargetID, migration.AvailableTargets)
	}
}

func TestConfigurationSetHandler_Update_ItemTypeRemovalReportsNoTargetAtHierarchyLevel(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	workspaceID := createTestWorkspace(t, tdb, "No Item Type Target", "NITT")
	sourceTypeID := createMigrationItemType(t, tdb, "Only Level Three Type", 3)
	otherLevelTargetID := createMigrationItemType(t, tdb, "Level Two Survivor", 2)

	var configSetID int
	if err := tdb.QueryRow(`
		INSERT INTO configuration_sets (name, description, is_default, created_at, updated_at)
		VALUES ('No Target Set', '', false, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) RETURNING id
	`).Scan(&configSetID); err != nil {
		t.Fatalf("Failed to create config set: %v", err)
	}
	attachWorkspaceToConfigSet(t, tdb, workspaceID, configSetID)
	for _, itemTypeID := range []int{sourceTypeID, otherLevelTargetID} {
		if _, err := tdb.Exec(`
			INSERT INTO configuration_set_item_types (configuration_set_id, item_type_id, created_at)
			VALUES (?, ?, CURRENT_TIMESTAMP)
		`, configSetID, itemTypeID); err != nil {
			t.Fatalf("Failed to configure item type %d: %v", itemTypeID, err)
		}
	}
	insertTypedItem(t, tdb, workspaceID, sourceTypeID, 1)

	handler := newConfigSetTestHandler(tdb)
	updated := models.ConfigurationSet{
		Name:         "No Target Set",
		WorkspaceIDs: []int{workspaceID},
		ItemTypeConfigs: []models.ItemTypeConfig{
			{ItemTypeID: otherLevelTargetID},
		},
	}
	req := testutils.CreateJSONRequest(t, "PUT", "/api/configuration-sets/"+testutils.IntToString(configSetID), updated)
	req.SetPathValue("id", testutils.IntToString(configSetID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Update, req, nil)
	rr.AssertStatusCode(http.StatusConflict)

	var body struct {
		Analysis models.ComprehensiveMigrationAnalysis `json:"analysis"`
	}
	rr.AssertJSONResponse(&body)
	if len(body.Analysis.ItemTypeMigrations) != 1 {
		t.Fatalf("Expected one migration, got %+v", body.Analysis.ItemTypeMigrations)
	}
	if got := body.Analysis.ItemTypeMigrations[0].AvailableTargets; len(got) != 0 {
		t.Fatalf("Expected no same-level targets, got %+v", got)
	}
}

func TestExecuteComprehensiveMigration_AppliesItemTypeRemovalAtomically(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	workspaceID := createTestWorkspace(t, tdb, "Atomic Item Type Migration", "AITM")
	sourceTypeID := createMigrationItemType(t, tdb, "Atomic Source", 3)
	targetTypeID := createMigrationItemType(t, tdb, "Atomic Target", 3)
	var configSetID int
	if err := tdb.QueryRow(`
		INSERT INTO configuration_sets (name, description, is_default, created_at, updated_at)
		VALUES ('Atomic Item Type Set', '', false, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) RETURNING id
	`).Scan(&configSetID); err != nil {
		t.Fatalf("Failed to create config set: %v", err)
	}
	attachWorkspaceToConfigSet(t, tdb, workspaceID, configSetID)
	for _, itemTypeID := range []int{sourceTypeID, targetTypeID} {
		if _, err := tdb.Exec(`
			INSERT INTO configuration_set_item_types (configuration_set_id, item_type_id, created_at)
			VALUES (?, ?, CURRENT_TIMESTAMP)
		`, configSetID, itemTypeID); err != nil {
			t.Fatalf("Failed to configure item type %d: %v", itemTypeID, err)
		}
	}
	itemID := insertTypedItem(t, tdb, workspaceID, sourceTypeID, 1)

	proposed := []models.ItemTypeConfig{{ItemTypeID: targetTypeID}}
	from := sourceTypeID
	migrationReq := models.ComprehensiveMigrationRequest{
		OldConfigurationSetID:           configSetID,
		NewConfigurationSetID:           configSetID,
		WorkspaceIDs:                    []int{workspaceID},
		ItemTypeMappings:                []models.ItemTypeMigrationMapping{{FromItemTypeID: &from, ToItemTypeID: targetTypeID}},
		ApplyItemTypeConfigsToConfigSet: &proposed,
	}
	handler := newConfigSetTestHandler(tdb)
	req := testutils.CreateJSONRequest(t, "POST", "/api/configuration-sets/execute-comprehensive-migration", migrationReq)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.ExecuteComprehensiveMigration, req, nil)
	rr.AssertStatusCode(http.StatusOK)

	var actualTypeID int
	if err := tdb.QueryRow(`SELECT item_type_id FROM items WHERE id = ?`, itemID).Scan(&actualTypeID); err != nil {
		t.Fatalf("Read migrated item: %v", err)
	}
	if actualTypeID != targetTypeID {
		t.Fatalf("Expected item type %d, got %d", targetTypeID, actualTypeID)
	}
	var sourceMemberships int
	if err := tdb.QueryRow(`
		SELECT COUNT(*) FROM configuration_set_item_types
		WHERE configuration_set_id = ? AND item_type_id = ?
	`, configSetID, sourceTypeID).Scan(&sourceMemberships); err != nil {
		t.Fatalf("Read item type memberships: %v", err)
	}
	if sourceMemberships != 0 {
		t.Fatalf("Expected source item type to be removed, found %d memberships", sourceMemberships)
	}
}

func TestExecuteComprehensiveMigration_RejectsCrossHierarchyItemTypeTarget(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	workspaceID := createTestWorkspace(t, tdb, "Cross Level Migration", "CLM")
	sourceTypeID := createMigrationItemType(t, tdb, "Cross Level Source", 3)
	targetTypeID := createMigrationItemType(t, tdb, "Cross Level Target", 2)
	var configSetID int
	if err := tdb.QueryRow(`
		INSERT INTO configuration_sets (name, description, is_default, created_at, updated_at)
		VALUES ('Cross Level Set', '', false, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) RETURNING id
	`).Scan(&configSetID); err != nil {
		t.Fatalf("Failed to create config set: %v", err)
	}
	attachWorkspaceToConfigSet(t, tdb, workspaceID, configSetID)
	for _, itemTypeID := range []int{sourceTypeID, targetTypeID} {
		if _, err := tdb.Exec(`
			INSERT INTO configuration_set_item_types (configuration_set_id, item_type_id, created_at)
			VALUES (?, ?, CURRENT_TIMESTAMP)
		`, configSetID, itemTypeID); err != nil {
			t.Fatalf("Failed to configure item type %d: %v", itemTypeID, err)
		}
	}
	insertTypedItem(t, tdb, workspaceID, sourceTypeID, 1)

	from := sourceTypeID
	migrationReq := models.ComprehensiveMigrationRequest{
		OldConfigurationSetID: configSetID,
		NewConfigurationSetID: configSetID,
		WorkspaceIDs:          []int{workspaceID},
		ItemTypeMappings: []models.ItemTypeMigrationMapping{
			{FromItemTypeID: &from, ToItemTypeID: targetTypeID},
		},
	}
	handler := newConfigSetTestHandler(tdb)
	req := testutils.CreateJSONRequest(t, "POST", "/api/configuration-sets/execute-comprehensive-migration", migrationReq)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.ExecuteComprehensiveMigration, req, nil)
	rr.AssertStatusCode(http.StatusBadRequest)
}

// --- C3: cross-workflow target status is rejected -------------------------

func TestExecuteComprehensiveMigration_RejectsCrossWorkflowStatus(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	workspaceID := createTestWorkspace(t, tdb, "Test Workspace", "WS")
	wfA := createTestWorkflow(t, tdb, "Workflow A")
	wfB := createTestWorkflow(t, tdb, "Workflow B")
	statusInA := createTestStatus(t, tdb, "Open A")
	statusInB := createTestStatus(t, tdb, "Open B")
	linkStatusToWorkflow(t, tdb, wfA, statusInA)
	linkStatusToWorkflow(t, tdb, wfB, statusInB)

	// Two configuration sets: source uses workflow A, target uses workflow B.
	srcID := insertConfigSet(t, tdb, "Source", wfA)
	tgtID := insertConfigSet(t, tdb, "Target", wfB)
	attachWorkspaceToConfigSet(t, tdb, workspaceID, srcID)

	handler := newConfigSetTestHandler(tdb)

	// Caller tries to map an old status to statusInA — which belongs to
	// workflow A, NOT the target's workflow B. Must be rejected.
	from := statusInA
	migrationReq := models.ComprehensiveMigrationRequest{
		OldConfigurationSetID: int(srcID),
		NewConfigurationSetID: int(tgtID),
		WorkspaceIDs:          []int{workspaceID},
		StatusMappings: []models.StatusMigrationMapping{
			{FromStatusID: &from, ToStatusID: statusInA},
		},
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/configuration-sets/execute-comprehensive-migration", migrationReq)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.ExecuteComprehensiveMigration, req, nil)

	rr.AssertStatusCode(http.StatusBadRequest)
}

// --- C4: items with NULL status_id are migrated when from_status_id is nil

func TestExecuteComprehensiveMigration_MigratesNullStatusItems(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	workspaceID := createTestWorkspace(t, tdb, "Test Workspace", "WS")
	wf := createTestWorkflow(t, tdb, "Workflow C4")
	openStatus := createTestStatus(t, tdb, "Open C4")
	linkStatusToWorkflow(t, tdb, wf, openStatus)

	configSetID := insertConfigSet(t, tdb, "Target", wf)
	attachWorkspaceToConfigSet(t, tdb, workspaceID, configSetID)

	// Two items with NULL status_id; one with a real status that should not be touched.
	itemA := insertItem(t, tdb, workspaceID, 0, 1)
	itemB := insertItem(t, tdb, workspaceID, 0, 2)
	itemC := insertItem(t, tdb, workspaceID, openStatus, 3)

	handler := newConfigSetTestHandler(tdb)

	migrationReq := models.ComprehensiveMigrationRequest{
		OldConfigurationSetID: int(configSetID),
		NewConfigurationSetID: int(configSetID),
		WorkspaceIDs:          []int{workspaceID},
		StatusMappings: []models.StatusMigrationMapping{
			// FromStatusID == nil signals "items with status_id IS NULL".
			{FromStatusID: nil, ToStatusID: openStatus},
		},
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/configuration-sets/execute-comprehensive-migration", migrationReq)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.ExecuteComprehensiveMigration, req, nil)

	rr.AssertStatusCode(http.StatusOK)

	var resp map[string]interface{}
	rr.AssertJSONResponse(&resp)
	if got, _ := resp["migrated_items"].(float64); int(got) < 2 {
		t.Errorf("Expected at least 2 migrated items (NULL-status), got %v", resp["migrated_items"])
	}

	// Verify itemA & itemB now have openStatus, itemC unchanged.
	for _, id := range []int{itemA, itemB} {
		var sid *int
		if err := tdb.QueryRow(`SELECT status_id FROM items WHERE id = ?`, id).Scan(&sid); err != nil {
			t.Fatalf("Read item %d: %v", id, err)
		}
		if sid == nil || *sid != openStatus {
			t.Errorf("Item %d: expected status_id=%d, got %v", id, openStatus, sid)
		}
	}
	var sidC *int
	_ = tdb.QueryRow(`SELECT status_id FROM items WHERE id = ?`, itemC).Scan(&sidC)
	if sidC == nil || *sidC != openStatus {
		t.Errorf("Item %d should still be at openStatus", itemC)
	}
}

// --- C5: AttachAfterMigration is atomic; conflict on source mismatch ------

func TestExecuteComprehensiveMigration_AttachIsAtomic_HappyPath(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	workspaceID := createTestWorkspace(t, tdb, "WS", "WS")
	wfA := createTestWorkflow(t, tdb, "WF A")
	wfB := createTestWorkflow(t, tdb, "WF B")
	openStatus := createTestStatus(t, tdb, "Open Shared")
	// Same status name in both workflows so analyzer says "no migration needed";
	// but the test only exercises the swap, so just link the status to the target.
	linkStatusToWorkflow(t, tdb, wfA, openStatus)
	linkStatusToWorkflow(t, tdb, wfB, openStatus)

	srcID := insertConfigSet(t, tdb, "Source", wfA)
	tgtID := insertConfigSet(t, tdb, "Target", wfB)
	attachWorkspaceToConfigSet(t, tdb, workspaceID, srcID)

	handler := newConfigSetTestHandler(tdb)
	migrationReq := models.ComprehensiveMigrationRequest{
		OldConfigurationSetID: srcID,
		NewConfigurationSetID: tgtID,
		WorkspaceIDs:          []int{workspaceID},
		AttachAfterMigration:  true,
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/configuration-sets/execute-comprehensive-migration", migrationReq)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.ExecuteComprehensiveMigration, req, nil)
	rr.AssertStatusCode(http.StatusOK)

	// Assignment row now references the target.
	var attachedID int
	err := tdb.QueryRow(`SELECT configuration_set_id FROM workspace_configuration_sets WHERE workspace_id = ?`, workspaceID).Scan(&attachedID)
	if err != nil {
		t.Fatalf("Read workspace_configuration_sets: %v", err)
	}
	if attachedID != tgtID {
		t.Errorf("Expected workspace attached to target %d, got %d", tgtID, attachedID)
	}
}

func TestExecuteComprehensiveMigration_AttachReturnsConflictOnSourceMismatch(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	workspaceID := createTestWorkspace(t, tdb, "WS", "WS")
	wfA := createTestWorkflow(t, tdb, "WF A")
	wfB := createTestWorkflow(t, tdb, "WF B")
	wfC := createTestWorkflow(t, tdb, "WF C")
	_ = wfC

	srcID := insertConfigSet(t, tdb, "A", wfA)
	tgtID := insertConfigSet(t, tdb, "B", wfB)
	otherID := insertConfigSet(t, tdb, "Other", wfA)

	// Workspace is currently attached to "Other", NOT to "A". Caller claims
	// the source is A → swap must return 409.
	attachWorkspaceToConfigSet(t, tdb, workspaceID, otherID)

	handler := newConfigSetTestHandler(tdb)
	migrationReq := models.ComprehensiveMigrationRequest{
		OldConfigurationSetID: srcID,
		NewConfigurationSetID: tgtID,
		WorkspaceIDs:          []int{workspaceID},
		AttachAfterMigration:  true,
	}
	req := testutils.CreateJSONRequest(t, "POST", "/api/configuration-sets/execute-comprehensive-migration", migrationReq)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.ExecuteComprehensiveMigration, req, nil)
	rr.AssertStatusCode(http.StatusConflict)

	// Assignment row should be unchanged.
	var attachedID int
	if err := tdb.QueryRow(`SELECT configuration_set_id FROM workspace_configuration_sets WHERE workspace_id = ?`, workspaceID).Scan(&attachedID); err != nil {
		t.Fatalf("Read workspace_configuration_sets: %v", err)
	}
	if attachedID != otherID {
		t.Errorf("Expected workspace assignment unchanged at %d, got %d", otherID, attachedID)
	}
}

// insertConfigSet inserts a configuration set row (with optional workflow)
// through a RETURNING query, which both SQLite and Postgres support.
func insertConfigSet(t *testing.T, tdb *testutils.TestDB, name string, workflowID int) int {
	t.Helper()
	var id int
	if err := tdb.QueryRow(`
		INSERT INTO configuration_sets (name, description, is_default, workflow_id, created_at, updated_at)
		VALUES (?, '', false, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) RETURNING id
	`, name, workflowID).Scan(&id); err != nil {
		t.Fatalf("Failed to create config set %q: %v", name, err)
	}
	return id
}

// --- R1: setting workflow_id to nil resolves to global default ------------

func TestConfigurationSetHandler_Update_NilWorkflowResolvesToGlobalDefault(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// Use the seed's default workflow as the global fallback. Create a fresh
	// workflow that does NOT contain the items' current status — items must
	// then be flagged for migration when the explicit workflow is removed.
	workspaceID := createTestWorkspace(t, tdb, "WS R1", "WSR1")
	customWf := createTestWorkflow(t, tdb, "Custom WF R1")
	openR1 := createTestStatus(t, tdb, "Open R1")
	linkStatusToWorkflow(t, tdb, customWf, openR1)

	// Mark a workflow as the global default (the seed may already have one).
	// This row is what resolveEffectiveWorkflowID should find.
	if _, err := tdb.Exec(`UPDATE workflows SET is_default = false WHERE is_default = true`); err != nil {
		t.Fatalf("clear default workflows: %v", err)
	}
	defaultWf := createTestWorkflow(t, tdb, "Global Default R1")
	if _, err := tdb.Exec(`UPDATE workflows SET is_default = true WHERE id = ?`, defaultWf); err != nil {
		t.Fatalf("set default workflow: %v", err)
	}
	// Default workflow has only "Done R1" — items on Open R1 won't match.
	doneR1 := createTestStatus(t, tdb, "Done R1")
	linkStatusToWorkflow(t, tdb, defaultWf, doneR1)

	configSetID := insertConfigSet(t, tdb, "Set R1", customWf)
	attachWorkspaceToConfigSet(t, tdb, workspaceID, configSetID)
	insertItem(t, tdb, workspaceID, openR1, 1)

	handler := newConfigSetTestHandler(tdb)
	updated := map[string]any{
		"name":          "Set R1",
		"workflow_id":   nil,
		"workspace_ids": []int{workspaceID},
	}
	req := testutils.CreateJSONRequest(t, "PUT", "/api/configuration-sets/"+testutils.IntToString(configSetID), updated)
	req.SetPathValue("id", testutils.IntToString(configSetID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Update, req, nil)

	rr.AssertStatusCode(http.StatusConflict)
	var body map[string]interface{}
	rr.AssertJSONResponse(&body)
	if body["error"] != "migration_required" {
		t.Errorf("Expected migration_required, got %v", body["error"])
	}
}

// --- R3: ApplyWorkflowToConfigSet updates workflow atomically -------------

func TestExecuteComprehensiveMigration_ApplyWorkflowAtomic(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	workspaceID := createTestWorkspace(t, tdb, "WS R3", "WSR3")
	wfOld := createTestWorkflow(t, tdb, "WF Old R3")
	wfNew := createTestWorkflow(t, tdb, "WF New R3")
	openOld := createTestStatus(t, tdb, "Open Old R3")
	openNew := createTestStatus(t, tdb, "Open New R3")
	linkStatusToWorkflow(t, tdb, wfOld, openOld)
	linkStatusToWorkflow(t, tdb, wfNew, openNew)

	configSetID := insertConfigSet(t, tdb, "Set R3", wfOld)
	attachWorkspaceToConfigSet(t, tdb, workspaceID, configSetID)

	itemID := insertItem(t, tdb, workspaceID, openOld, 1)

	handler := newConfigSetTestHandler(tdb)
	from := openOld
	migrationReq := models.ComprehensiveMigrationRequest{
		OldConfigurationSetID:    configSetID,
		NewConfigurationSetID:    configSetID,
		WorkspaceIDs:             []int{workspaceID},
		ApplyWorkflowToConfigSet: &wfNew, // intra-set: validate against wfNew, write it after migration
		StatusMappings: []models.StatusMigrationMapping{
			{FromStatusID: &from, ToStatusID: openNew},
		},
	}
	req := testutils.CreateJSONRequest(t, "POST", "/api/configuration-sets/execute-comprehensive-migration", migrationReq)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.ExecuteComprehensiveMigration, req, nil)
	rr.AssertStatusCode(http.StatusOK)

	// Item migrated to openNew.
	var sid *int
	if err := tdb.QueryRow(`SELECT status_id FROM items WHERE id = ?`, itemID).Scan(&sid); err != nil {
		t.Fatalf("read item: %v", err)
	}
	if sid == nil || *sid != openNew {
		t.Errorf("Expected item to have status_id=%d, got %v", openNew, sid)
	}

	// Config set's workflow_id swapped to wfNew.
	var attachedWf int
	if err := tdb.QueryRow(`SELECT workflow_id FROM configuration_sets WHERE id = ?`, configSetID).Scan(&attachedWf); err != nil {
		t.Fatalf("read config set workflow: %v", err)
	}
	if attachedWf != wfNew {
		t.Errorf("Expected workflow_id=%d after migration, got %d", wfNew, attachedWf)
	}
}

func TestExecuteComprehensiveMigration_ApplyWorkflow_RejectsCrossWorkflowStatus(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	workspaceID := createTestWorkspace(t, tdb, "WS R3b", "WSR3B")
	wfOld := createTestWorkflow(t, tdb, "WF Old R3b")
	wfNew := createTestWorkflow(t, tdb, "WF New R3b")
	wfElsewhere := createTestWorkflow(t, tdb, "WF Else R3b")
	openOld := createTestStatus(t, tdb, "Open Old R3b")
	openNew := createTestStatus(t, tdb, "Open New R3b")
	stranger := createTestStatus(t, tdb, "Stranger R3b")
	linkStatusToWorkflow(t, tdb, wfOld, openOld)
	linkStatusToWorkflow(t, tdb, wfNew, openNew)
	linkStatusToWorkflow(t, tdb, wfElsewhere, stranger)

	configSetID := insertConfigSet(t, tdb, "Set R3b", wfOld)
	attachWorkspaceToConfigSet(t, tdb, workspaceID, configSetID)

	handler := newConfigSetTestHandler(tdb)
	from := openOld
	// Caller asks for ApplyWorkflowToConfigSet=wfNew but maps to a status from
	// wfElsewhere — must be rejected against the supplied workflow, not the DB.
	migrationReq := models.ComprehensiveMigrationRequest{
		OldConfigurationSetID:    configSetID,
		NewConfigurationSetID:    configSetID,
		WorkspaceIDs:             []int{workspaceID},
		ApplyWorkflowToConfigSet: &wfNew,
		StatusMappings: []models.StatusMigrationMapping{
			{FromStatusID: &from, ToStatusID: stranger},
		},
	}
	req := testutils.CreateJSONRequest(t, "POST", "/api/configuration-sets/execute-comprehensive-migration", migrationReq)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.ExecuteComprehensiveMigration, req, nil)
	rr.AssertStatusCode(http.StatusBadRequest)
}

// --- R4: cross-set + workflow change validates against new workflow -------

func TestConfigurationSetHandler_Update_CrossSetMoveWithWorkflowChangeChecksAgainstNew(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	wsMoving := createTestWorkspace(t, tdb, "WS Moving R4", "WSMOVR4")

	// Source config set has workflow A with status "OnA". Items in the moving
	// workspace are on OnA.
	wfA := createTestWorkflow(t, tdb, "WF A R4")
	onA := createTestStatus(t, tdb, "OnA R4")
	linkStatusToWorkflow(t, tdb, wfA, onA)
	srcID := insertConfigSet(t, tdb, "Source R4", wfA)
	attachWorkspaceToConfigSet(t, tdb, wsMoving, srcID)
	insertItem(t, tdb, wsMoving, onA, 1)

	// Target config set has workflow B (currently in DB) with the same name
	// "OnA", which the analyzer would match → no migration needed if reading
	// the DB. But we're also changing the target's workflow_id to wfC in the
	// same PUT — wfC has only "OnC" — so the moving workspace's items should
	// be flagged.
	wfB := createTestWorkflow(t, tdb, "WF B R4")
	wfC := createTestWorkflow(t, tdb, "WF C R4")
	onAInB := createTestStatus(t, tdb, "OnA Twin R4") // matching name, in wfB
	onC := createTestStatus(t, tdb, "OnC R4")
	linkStatusToWorkflow(t, tdb, wfB, onAInB)
	linkStatusToWorkflow(t, tdb, wfC, onC)
	tgtID := insertConfigSet(t, tdb, "Target R4", wfB)

	handler := newConfigSetTestHandler(tdb)
	updated := models.ConfigurationSet{
		Name:         "Target R4",
		WorkflowID:   &wfC, // changing from wfB → wfC in the same PUT
		WorkspaceIDs: []int{wsMoving},
	}
	req := testutils.CreateJSONRequest(t, "PUT", "/api/configuration-sets/"+testutils.IntToString(tgtID), updated)
	req.SetPathValue("id", testutils.IntToString(tgtID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Update, req, nil)

	rr.AssertStatusCode(http.StatusConflict)
	var body map[string]interface{}
	rr.AssertJSONResponse(&body)
	if body["error"] != "migration_required" {
		t.Fatalf("Expected migration_required, got %v", body["error"])
	}
	analysis, _ := body["analysis"].(map[string]interface{})
	if analysis["requires_status_migration"] != true {
		t.Errorf("Expected requires_status_migration=true (against new workflow), got %v", analysis["requires_status_migration"])
	}
}
