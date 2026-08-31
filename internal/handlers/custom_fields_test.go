//go:build test

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

// materializeDeferredIndexes runs the same deferred-index build that fires
// at server startup. SQLite index creation is deferred by the handler so
// large CREATE INDEX statements don't block a live admin request; tests
// have to trigger materialization themselves before asserting on the
// physical sqlite_master row. Postgres index builds are handled by the
// cleanup scheduler and are not materialized here.
func materializeDeferredIndexes(t *testing.T, tdb *testutils.TestDB) {
	t.Helper()
	if tdb.GetDriverName() == "sqlite" {
		database.MaterializeDeferredSQLiteCustomFieldIndexes(tdb.GetDatabase())
	}
}

func createCustomFieldHandler(t *testing.T, tdb *testutils.TestDB) *CustomFieldHandler {
	t.Helper()
	return NewCustomFieldHandler(tdb.GetDatabase())
}

// createField is a test helper that creates a custom field and returns it
func createField(t *testing.T, handler *CustomFieldHandler, name, fieldType string) models.CustomFieldDefinition {
	t.Helper()
	body := map[string]interface{}{
		"name":       name,
		"field_type": fieldType,
	}
	req := testutils.CreateJSONRequest(t, "POST", "/api/custom-fields", body)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
	rr.AssertStatusCode(http.StatusCreated)

	var cf models.CustomFieldDefinition
	rr.AssertJSONResponse(&cf)
	return cf
}

// assertIndexExists checks that a database index exists, engine-aware
// (pg_indexes on Postgres, sqlite_master on SQLite).
func assertIndexExists(t *testing.T, tdb *testutils.TestDB, indexName string) {
	t.Helper()
	tdb.AssertIndexExists(t, indexName)
}

// assertIndexNotExists checks that a database index does not exist,
// engine-aware.
func assertIndexNotExists(t *testing.T, tdb *testutils.TestDB, indexName string) {
	t.Helper()
	var query string
	switch tdb.GetDriverName() {
	case "postgres":
		query = `SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE schemaname = current_schema() AND indexname = ?)`
	default:
		query = `SELECT EXISTS(SELECT name FROM sqlite_master WHERE type='index' AND name=?)`
	}
	var exists bool
	if err := tdb.QueryRow(query, indexName).Scan(&exists); err != nil {
		t.Fatalf("Failed to check index existence: %v", err)
	}
	if exists {
		t.Errorf("Expected index %q to NOT exist, but it does", indexName)
	}
}

// assertIndexRecordExists checks the junction table has a row
func assertIndexRecordExists(t *testing.T, tdb *testutils.TestDB, fieldID int, targetTable string) {
	t.Helper()
	var count int
	err := tdb.QueryRow(`SELECT COUNT(*) FROM custom_field_indexes WHERE custom_field_id = ? AND target_table = ?`, fieldID, targetTable).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to check index record: %v", err)
	}
	if count == 0 {
		t.Errorf("Expected index record for field %d on %s to exist", fieldID, targetTable)
	}
}

// assertIndexRecordNotExists checks the junction table has no row
func assertIndexRecordNotExists(t *testing.T, tdb *testutils.TestDB, fieldID int, targetTable string) {
	t.Helper()
	var count int
	err := tdb.QueryRow(`SELECT COUNT(*) FROM custom_field_indexes WHERE custom_field_id = ? AND target_table = ?`, fieldID, targetTable).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to check index record: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected index record for field %d on %s to NOT exist", fieldID, targetTable)
	}
}

// enableIndex sends an update request with indexing enabled for the given table
func enableIndex(t *testing.T, handler *CustomFieldHandler, fieldID int, fieldType string, items, assets bool) *testutils.ResponseRecorder {
	t.Helper()
	body := map[string]interface{}{
		"name":       "IndexTest",
		"field_type": fieldType,
		"indexed": map[string]bool{
			"items":  items,
			"assets": assets,
		},
	}
	req := testutils.CreateJSONRequest(t, "PUT", "/api/custom-fields/1", body)
	req.SetPathValue("id", testutils.IntToString(fieldID))
	return testutils.ExecuteAuthenticatedRequest(t, handler.Update, req, nil)
}

// --- Create ---

func TestCustomFieldHandler_Create_TextField(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)

	body := map[string]interface{}{
		"name":       "Priority Level",
		"field_type": "text",
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/custom-fields", body)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)

	rr.AssertStatusCode(http.StatusCreated).
		AssertContentType("application/json")

	var cf models.CustomFieldDefinition
	rr.AssertJSONResponse(&cf)

	if cf.ID == 0 {
		t.Error("Expected custom field to have an ID")
	}
	if cf.Name != "Priority Level" {
		t.Errorf("Expected name 'Priority Level', got %q", cf.Name)
	}
	if cf.FieldType != "text" {
		t.Errorf("Expected field_type 'text', got %q", cf.FieldType)
	}
}

func TestCustomFieldHandler_Create_SelectWithOptions(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)

	body := map[string]interface{}{
		"name":       "Environment",
		"field_type": "select",
		"options":    `["Production","Staging","Development"]`,
	}

	req := testutils.CreateJSONRequest(t, "POST", "/api/custom-fields", body)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)

	rr.AssertStatusCode(http.StatusCreated)

	var cf models.CustomFieldDefinition
	rr.AssertJSONResponse(&cf)

	if cf.ID == 0 {
		t.Error("Expected custom field to have an ID")
	}
	if cf.FieldType != "select" {
		t.Errorf("Expected field_type 'select', got %q", cf.FieldType)
	}
}

func TestCustomFieldHandler_Create_ValidationErrors(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)

	tests := []struct {
		name string
		body map[string]interface{}
	}{
		{
			name: "Empty name",
			body: map[string]interface{}{
				"name":       "",
				"field_type": "text",
			},
		},
		{
			name: "Invalid field type",
			body: map[string]interface{}{
				"name":       "Test",
				"field_type": "invalid_type",
			},
		},
		{
			name: "Select with empty options",
			body: map[string]interface{}{
				"name":       "Test",
				"field_type": "select",
				"options":    `[]`,
			},
		},
		{
			name: "Select with omitted options",
			body: map[string]interface{}{
				"name":       "Test",
				"field_type": "select",
			},
		},
		{
			name: "Multiselect with blank options",
			body: map[string]interface{}{
				"name":       "Test",
				"field_type": "multiselect",
				"options":    "",
			},
		},
		{
			name: "Asset missing asset_set_id",
			body: map[string]interface{}{
				"name":       "Test",
				"field_type": "asset",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := testutils.CreateJSONRequest(t, "POST", "/api/custom-fields", tt.body)
			rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
			rr.AssertStatusCode(http.StatusBadRequest)
		})
	}
}

func TestCustomFieldHandler_Create_InvalidBody(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)

	req, _ := testutils.MockHTTPRequest("POST", "/api/custom-fields", nil)
	req.Body = http.NoBody
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)

	rr.AssertStatusCode(http.StatusBadRequest)
}

// --- GetAll ---

func TestCustomFieldHandler_GetAll_Empty(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)

	// Delete any system defaults that may exist
	_, _ = tdb.Exec("DELETE FROM custom_field_definitions")

	req := testutils.CreateJSONRequest(t, "GET", "/api/custom-fields", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, req, nil)

	rr.AssertStatusCode(http.StatusOK).
		AssertContentType("application/json")

	var resp customFieldsResponse
	rr.AssertJSONResponse(&resp)

	if len(resp.Data) != 0 {
		t.Errorf("Expected 0 fields, got %d", len(resp.Data))
	}
}

func TestCustomFieldHandler_GetAll_WithFields(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)

	// Delete any system defaults
	_, _ = tdb.Exec("DELETE FROM custom_field_definitions")

	// Create 2 fields
	for _, name := range []string{"Field A", "Field B"} {
		createField(t, handler, name, "text")
	}

	req := testutils.CreateJSONRequest(t, "GET", "/api/custom-fields", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, req, nil)

	rr.AssertStatusCode(http.StatusOK)

	var resp customFieldsResponse
	rr.AssertJSONResponse(&resp)

	if len(resp.Data) != 2 {
		t.Errorf("Expected 2 fields, got %d", len(resp.Data))
	}
}

// --- Get ---

func TestCustomFieldHandler_Get_Success(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)
	created := createField(t, handler, "Get Test Field", "number")

	// Get it
	getReq := testutils.CreateJSONRequest(t, "GET", "/api/custom-fields/1", nil)
	getReq.SetPathValue("id", testutils.IntToString(created.ID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Get, getReq, nil)

	rr.AssertStatusCode(http.StatusOK).
		AssertContentType("application/json")

	var fetched models.CustomFieldDefinition
	rr.AssertJSONResponse(&fetched)

	if fetched.ID != created.ID {
		t.Errorf("Expected ID %d, got %d", created.ID, fetched.ID)
	}
	if fetched.Name != "Get Test Field" {
		t.Errorf("Expected name 'Get Test Field', got %q", fetched.Name)
	}
}

func TestCustomFieldHandler_Get_NotFound(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "GET", "/api/custom-fields/99999", nil)
	req.SetPathValue("id", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Get, req, nil)

	rr.AssertStatusCode(http.StatusNotFound)
}

// --- Update ---

func TestCustomFieldHandler_Update_Name(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)
	created := createField(t, handler, "Original Name", "text")

	// Update name
	updateBody := map[string]interface{}{
		"name":       "Updated Name",
		"field_type": "text",
	}
	updateReq := testutils.CreateJSONRequest(t, "PUT", "/api/custom-fields/1", updateBody)
	updateReq.SetPathValue("id", testutils.IntToString(created.ID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Update, updateReq, nil)

	rr.AssertStatusCode(http.StatusOK).
		AssertContentType("application/json")

	var updated models.CustomFieldDefinition
	rr.AssertJSONResponse(&updated)

	if updated.Name != "Updated Name" {
		t.Errorf("Expected name 'Updated Name', got %q", updated.Name)
	}
}

func TestCustomFieldHandler_Update_RejectsFieldTypeChangeWithoutMutation(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)
	created := createField(t, handler, "Original Text Field", "text")

	updateReq := testutils.CreateJSONRequest(t, "PUT", "/api/custom-fields/1", map[string]any{
		"name":       "Retyped Number Field",
		"field_type": "number",
	})
	updateReq.SetPathValue("id", testutils.IntToString(created.ID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Update, updateReq, nil)

	testutils.AssertValidationError(t, rr, "Custom field type cannot be changed after creation")

	var name, fieldType string
	if err := tdb.QueryRow(`SELECT name, field_type FROM custom_field_definitions WHERE id = ?`, created.ID).Scan(&name, &fieldType); err != nil {
		t.Fatalf("load custom field after rejected update: %v", err)
	}
	if name != "Original Text Field" || fieldType != "text" {
		t.Fatalf("rejected update mutated custom field: name=%q field_type=%q", name, fieldType)
	}
}

func TestCustomFieldHandler_Update_RejectsEmptySelectOptionsWithoutMutationOrCleanup(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)
	createReq := testutils.CreateJSONRequest(t, "POST", "/api/custom-fields", map[string]any{
		"name":       "Environment",
		"field_type": "select",
		"options":    `["Production","Staging"]`,
	})
	createResp := testutils.ExecuteAuthenticatedRequest(t, handler.Create, createReq, nil)
	createResp.AssertStatusCode(http.StatusCreated)

	var created models.CustomFieldDefinition
	createResp.AssertJSONResponse(&created)

	updateReq := testutils.CreateJSONRequest(t, "PUT", "/api/custom-fields/1", map[string]any{
		"name":       "Renamed Environment",
		"field_type": "select",
		"options":    "",
	})
	updateReq.SetPathValue("id", testutils.IntToString(created.ID))
	updateResp := testutils.ExecuteAuthenticatedRequest(t, handler.Update, updateReq, nil)

	testutils.AssertValidationError(t, updateResp, "Select fields must have at least one option")

	var name, options string
	if err := tdb.QueryRow(`SELECT name, options FROM custom_field_definitions WHERE id = ?`, created.ID).Scan(&name, &options); err != nil {
		t.Fatalf("load custom field after rejected update: %v", err)
	}
	if name != "Environment" || options != created.Options {
		t.Fatalf("rejected update mutated custom field: name=%q options=%q", name, options)
	}

	var cleanupJobs int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM pending_custom_field_cleanups WHERE field_id = ?`, created.ID).Scan(&cleanupJobs); err != nil {
		t.Fatalf("count cleanup jobs after rejected update: %v", err)
	}
	if cleanupJobs != 0 {
		t.Fatalf("rejected update enqueued %d cleanup jobs", cleanupJobs)
	}
}

func TestCustomFieldHandler_Update_NotFound(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)

	body := map[string]interface{}{
		"name":       "Test",
		"field_type": "text",
	}
	req := testutils.CreateJSONRequest(t, "PUT", "/api/custom-fields/99999", body)
	req.SetPathValue("id", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Update, req, nil)

	rr.AssertStatusCode(http.StatusNotFound)
}

// --- Delete ---

func TestCustomFieldHandler_Delete_AndVerifyGone(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)
	created := createField(t, handler, "Delete Me", "text")

	// Delete
	deleteReq := testutils.CreateJSONRequest(t, "DELETE", "/api/custom-fields/1", nil)
	deleteReq.SetPathValue("id", testutils.IntToString(created.ID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Delete, deleteReq, nil)

	rr.AssertStatusCode(http.StatusNoContent)

	// Verify it's gone
	getReq := testutils.CreateJSONRequest(t, "GET", "/api/custom-fields/1", nil)
	getReq.SetPathValue("id", testutils.IntToString(created.ID))
	getRR := testutils.ExecuteAuthenticatedRequest(t, handler.Get, getReq, nil)

	getRR.AssertStatusCode(http.StatusNotFound)
}

func TestCustomFieldHandler_DeleteLinkingFieldGuardsMirrorValues(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)
	primaryID, mirrorID := createLinkedFieldPair(t, tdb)

	var workspaceID int
	if err := tdb.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Mirror guard', 'MGD') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if _, err := tdb.ExecWrite(`
		INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index, custom_field_values)
		VALUES (?, 1, 'Mirror value', '', 'a', ?)
	`, workspaceID, fmt.Sprintf(`{"%d":"linked"}`, mirrorID)); err != nil {
		t.Fatalf("insert item with mirror value: %v", err)
	}

	deleteReq := testutils.CreateJSONRequest(t, "DELETE", "/api/custom-fields/1", nil)
	deleteReq.SetPathValue("id", testutils.IntToString(primaryID))
	deleteRR := testutils.ExecuteAuthenticatedRequest(t, handler.Delete, deleteReq, nil)
	deleteRR.AssertStatusCode(http.StatusConflict)

	var remaining int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM custom_field_definitions WHERE id IN (?, ?)`, primaryID, mirrorID).Scan(&remaining); err != nil {
		t.Fatalf("count linked fields: %v", err)
	}
	if remaining != 2 {
		t.Fatalf("remaining linked fields = %d, want both fields preserved", remaining)
	}
}

func TestCustomFieldHandler_DeleteLinkingFieldCleansMirrorMaintenance(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)
	primaryID, mirrorID := createLinkedFieldPair(t, tdb)
	indexName := fmt.Sprintf("idx_cf_items_%d", mirrorID)
	if _, err := tdb.ExecWrite(fmt.Sprintf(`CREATE INDEX %s ON items(id)`, indexName)); err != nil {
		t.Fatalf("create mirror index: %v", err)
	}
	if _, err := tdb.ExecWrite(`
		INSERT INTO custom_field_indexes (custom_field_id, target_table, index_name)
		VALUES (?, 'items', ?)
	`, mirrorID, indexName); err != nil {
		t.Fatalf("record mirror index: %v", err)
	}
	if _, err := tdb.ExecWrite(`
		INSERT INTO pending_custom_field_cleanups (field_id, job_type, status)
		VALUES (?, 'index_build', 'pending')
	`, mirrorID); err != nil {
		t.Fatalf("insert mirror index job: %v", err)
	}

	deleteReq := testutils.CreateJSONRequest(t, "DELETE", "/api/custom-fields/1", nil)
	deleteReq.SetPathValue("id", testutils.IntToString(primaryID))
	deleteRR := testutils.ExecuteAuthenticatedRequest(t, handler.Delete, deleteReq, nil)
	deleteRR.AssertStatusCode(http.StatusNoContent)

	assertIndexNotExists(t, tdb, indexName)
	var remaining, mirrorScrubs, primaryScrubs, completedIndexBuilds int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM custom_field_definitions WHERE id IN (?, ?)`, primaryID, mirrorID).Scan(&remaining); err != nil {
		t.Fatalf("count deleted fields: %v", err)
	}
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM pending_custom_field_cleanups WHERE field_id = ? AND job_type = 'field_scrub'`, mirrorID).Scan(&mirrorScrubs); err != nil {
		t.Fatalf("count mirror scrub jobs: %v", err)
	}
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM pending_custom_field_cleanups WHERE field_id = ? AND job_type = 'field_scrub'`, primaryID).Scan(&primaryScrubs); err != nil {
		t.Fatalf("count primary scrub jobs: %v", err)
	}
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM pending_custom_field_cleanups WHERE field_id = ? AND job_type = 'index_build' AND status = 'done'`, mirrorID).Scan(&completedIndexBuilds); err != nil {
		t.Fatalf("count cancelled mirror index jobs: %v", err)
	}
	if remaining != 0 || mirrorScrubs != 1 || primaryScrubs != 1 || completedIndexBuilds != 1 {
		t.Fatalf("remaining=%d mirror scrubs=%d primary scrubs=%d completed mirror index builds=%d; want 0,1,1,1", remaining, mirrorScrubs, primaryScrubs, completedIndexBuilds)
	}
}

func createLinkedFieldPair(t *testing.T, tdb *testutils.TestDB) (primaryID, mirrorID int) {
	t.Helper()
	if err := tdb.QueryRow(`
		INSERT INTO custom_field_definitions (name, field_type, options)
		VALUES ('Mirror field', 'linking', '{}') RETURNING id
	`).Scan(&mirrorID); err != nil {
		t.Fatalf("insert mirror field: %v", err)
	}
	if err := tdb.QueryRow(`
		INSERT INTO custom_field_definitions (name, field_type, options)
		VALUES ('Primary field', 'linking', ?) RETURNING id
	`, fmt.Sprintf(`{"mirror_field_id":%d}`, mirrorID)).Scan(&primaryID); err != nil {
		t.Fatalf("insert primary field: %v", err)
	}
	if _, err := tdb.ExecWrite(`UPDATE custom_field_definitions SET options = ? WHERE id = ?`, fmt.Sprintf(`{"mirror_of_field_id":%d}`, primaryID), mirrorID); err != nil {
		t.Fatalf("link mirror field: %v", err)
	}
	return primaryID, mirrorID
}

func TestCustomFieldHandler_Delete_RemovesOnlyDeletedFieldFromLayouts(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)
	deleted := createField(t, handler, "Delete from layouts", "date")
	retained := createField(t, handler, "Keep in layouts", "date")

	var screenID int
	if err := tdb.QueryRow(`
		INSERT INTO screens (name, description)
		VALUES ('Custom field deletion screen', '')
		RETURNING id
	`).Scan(&screenID); err != nil {
		t.Fatalf("insert screen: %v", err)
	}
	for _, field := range []struct {
		fieldType  string
		identifier string
		order      int
	}{
		{fieldType: "system", identifier: "title", order: 0},
		{fieldType: "custom", identifier: testutils.IntToString(deleted.ID), order: 1},
		{fieldType: "custom", identifier: testutils.IntToString(retained.ID), order: 2},
	} {
		if _, err := tdb.ExecWrite(`
			INSERT INTO screen_fields
				(screen_id, field_type, field_identifier, display_order, is_required, field_width)
			VALUES (?, ?, ?, ?, false, 'full')
		`, screenID, field.fieldType, field.identifier, field.order); err != nil {
			t.Fatalf("insert screen field %q: %v", field.identifier, err)
		}
	}

	listColumns := []models.ListColumn{
		{FieldIdentifier: "key", FieldType: "system", DisplayOrder: 0, Width: 1},
		{FieldIdentifier: testutils.IntToString(deleted.ID), FieldType: "custom", DisplayOrder: 1, Width: 2},
		{FieldIdentifier: testutils.IntToString(retained.ID), FieldType: "custom", DisplayOrder: 2, Width: 3},
	}
	cardFields := []models.ListColumn{
		{FieldIdentifier: "title", FieldType: "system", DisplayOrder: 0},
		{FieldIdentifier: "custom_field_" + testutils.IntToString(deleted.ID), FieldType: "custom", DisplayOrder: 1},
		{FieldIdentifier: "custom_field_" + testutils.IntToString(retained.ID), FieldType: "custom", DisplayOrder: 2},
	}
	roadmap := models.RoadmapConfig{
		StartFieldID:         "cf_" + testutils.IntToString(deleted.ID),
		EndFieldID:           "cf_" + testutils.IntToString(retained.ID),
		DependencyLinkTypeID: intPointer(17),
	}
	listJSON, err := json.Marshal(listColumns)
	if err != nil {
		t.Fatalf("encode list columns: %v", err)
	}
	cardJSON, err := json.Marshal(cardFields)
	if err != nil {
		t.Fatalf("encode card fields: %v", err)
	}
	roadmapJSON, err := json.Marshal(roadmap)
	if err != nil {
		t.Fatalf("encode roadmap config: %v", err)
	}
	var boardConfigID int
	if err := tdb.QueryRow(`
		INSERT INTO board_configurations (list_columns, card_fields, roadmap_config)
		VALUES (?, ?, ?)
		RETURNING id
	`, string(listJSON), string(cardJSON), string(roadmapJSON)).Scan(&boardConfigID); err != nil {
		t.Fatalf("insert board configuration: %v", err)
	}

	deleteReq := testutils.CreateJSONRequest(t, "DELETE", "/api/custom-fields/1", nil)
	deleteReq.SetPathValue("id", testutils.IntToString(deleted.ID))
	deleteRR := testutils.ExecuteAuthenticatedRequest(t, handler.Delete, deleteReq, nil)
	deleteRR.AssertStatusCode(http.StatusNoContent)

	var deletedScreenRefs int
	if err := tdb.QueryRow(`
		SELECT COUNT(*) FROM screen_fields
		WHERE screen_id = ? AND field_type = 'custom' AND field_identifier = ?
	`, screenID, testutils.IntToString(deleted.ID)).Scan(&deletedScreenRefs); err != nil {
		t.Fatalf("count deleted screen references: %v", err)
	}
	if deletedScreenRefs != 0 {
		t.Fatalf("deleted field screen references = %d, want 0", deletedScreenRefs)
	}

	var retainedScreenRefs, titleScreenRefs int
	if err := tdb.QueryRow(`
		SELECT COUNT(*) FROM screen_fields
		WHERE screen_id = ? AND field_type = 'custom' AND field_identifier = ?
	`, screenID, testutils.IntToString(retained.ID)).Scan(&retainedScreenRefs); err != nil {
		t.Fatalf("count retained screen references: %v", err)
	}
	if err := tdb.QueryRow(`
		SELECT COUNT(*) FROM screen_fields
		WHERE screen_id = ? AND field_type = 'system' AND field_identifier = 'title'
	`, screenID).Scan(&titleScreenRefs); err != nil {
		t.Fatalf("count title screen references: %v", err)
	}
	if retainedScreenRefs != 1 || titleScreenRefs != 1 {
		t.Fatalf("retained screen references = custom %d, title %d; want 1 each", retainedScreenRefs, titleScreenRefs)
	}

	var gotListJSON, gotCardJSON, gotRoadmapJSON string
	if err := tdb.QueryRow(`
		SELECT list_columns, card_fields, roadmap_config
		FROM board_configurations WHERE id = ?
	`, boardConfigID).Scan(&gotListJSON, &gotCardJSON, &gotRoadmapJSON); err != nil {
		t.Fatalf("load cleaned board configuration: %v", err)
	}
	var gotList, gotCards []models.ListColumn
	var gotRoadmap models.RoadmapConfig
	if err := json.Unmarshal([]byte(gotListJSON), &gotList); err != nil {
		t.Fatalf("decode cleaned list columns: %v", err)
	}
	if err := json.Unmarshal([]byte(gotCardJSON), &gotCards); err != nil {
		t.Fatalf("decode cleaned card fields: %v", err)
	}
	if err := json.Unmarshal([]byte(gotRoadmapJSON), &gotRoadmap); err != nil {
		t.Fatalf("decode cleaned roadmap config: %v", err)
	}
	if got := listColumnIdentifiers(gotList); !equalStrings(got, []string{"key", testutils.IntToString(retained.ID)}) {
		t.Fatalf("list column identifiers = %#v, want system and retained custom field", got)
	}
	if got := listColumnIdentifiers(gotCards); !equalStrings(got, []string{"title", "custom_field_" + testutils.IntToString(retained.ID)}) {
		t.Fatalf("card field identifiers = %#v, want system and retained custom field", got)
	}
	if gotRoadmap.StartFieldID != "" || gotRoadmap.EndFieldID != "cf_"+testutils.IntToString(retained.ID) {
		t.Fatalf("roadmap fields = start %q, end %q; want empty start and retained end", gotRoadmap.StartFieldID, gotRoadmap.EndFieldID)
	}
	if gotRoadmap.DependencyLinkTypeID == nil || *gotRoadmap.DependencyLinkTypeID != 17 {
		t.Fatalf("roadmap dependency link type = %v, want 17", gotRoadmap.DependencyLinkTypeID)
	}
}

func intPointer(value int) *int {
	return &value
}

func listColumnIdentifiers(fields []models.ListColumn) []string {
	identifiers := make([]string, 0, len(fields))
	for _, field := range fields {
		identifiers = append(identifiers, field.FieldIdentifier)
	}
	return identifiers
}

func TestCustomFieldHandler_Delete_NotFound(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)

	req := testutils.CreateJSONRequest(t, "DELETE", "/api/custom-fields/99999", nil)
	req.SetPathValue("id", "99999")
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Delete, req, nil)

	rr.AssertStatusCode(http.StatusNotFound)
}

func TestCustomFieldHandler_Delete_SystemDefault(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)

	// Insert a system_default field directly via SQL
	var id int
	err := tdb.QueryRow(`
		INSERT INTO custom_field_definitions (name, field_type, required, display_order, system_default, created_at, updated_at)
		VALUES ('System Field', 'text', false, 1, true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) RETURNING id
	`).Scan(&id)
	if err != nil {
		t.Fatalf("Failed to insert system default field: %v", err)
	}

	req := testutils.CreateJSONRequest(t, "DELETE", "/api/custom-fields/1", nil)
	req.SetPathValue("id", testutils.IntToString(id))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.Delete, req, nil)

	rr.AssertStatusCode(http.StatusForbidden)
}

// --- Indexing ---

func TestCustomFieldHandler_EnableIndex_Number(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// On Postgres the physical index is built asynchronously by the
	// CFVCleanupScheduler (covered by tests/custom_field_async_maintenance_test.go);
	// the white-box materialization path exercised here is SQLite-only.
	if testutils.IsPostgres() {
		t.Skip("physical custom-field index build is async on Postgres")
	}

	handler := createCustomFieldHandler(t, tdb)
	cf := createField(t, handler, "Cost", "number")

	rr := enableIndex(t, handler, cf.ID, "number", true, false)
	rr.AssertStatusCode(http.StatusOK)

	indexName := fmt.Sprintf("idx_cf_items_%d", cf.ID)
	materializeDeferredIndexes(t, tdb)
	assertIndexExists(t, tdb, indexName)
	assertIndexRecordExists(t, tdb, cf.ID, "items")
}

func TestCustomFieldHandler_EnableIndex_Text(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// On Postgres the physical index is built asynchronously by the
	// CFVCleanupScheduler (covered by tests/custom_field_async_maintenance_test.go);
	// the white-box materialization path exercised here is SQLite-only.
	if testutils.IsPostgres() {
		t.Skip("physical custom-field index build is async on Postgres")
	}

	handler := createCustomFieldHandler(t, tdb)
	cf := createField(t, handler, "Serial", "text")

	rr := enableIndex(t, handler, cf.ID, "text", true, false)
	rr.AssertStatusCode(http.StatusOK)

	indexName := fmt.Sprintf("idx_cf_items_%d", cf.ID)
	materializeDeferredIndexes(t, tdb)
	assertIndexExists(t, tdb, indexName)
	assertIndexRecordExists(t, tdb, cf.ID, "items")
}

func TestCustomFieldHandler_EnableIndex_Date(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// On Postgres the physical index is built asynchronously by the
	// CFVCleanupScheduler (covered by tests/custom_field_async_maintenance_test.go);
	// the white-box materialization path exercised here is SQLite-only.
	if testutils.IsPostgres() {
		t.Skip("physical custom-field index build is async on Postgres")
	}

	handler := createCustomFieldHandler(t, tdb)
	cf := createField(t, handler, "Deadline", "date")

	rr := enableIndex(t, handler, cf.ID, "date", true, false)
	rr.AssertStatusCode(http.StatusOK)

	indexName := fmt.Sprintf("idx_cf_items_%d", cf.ID)
	materializeDeferredIndexes(t, tdb)
	assertIndexExists(t, tdb, indexName)
	assertIndexRecordExists(t, tdb, cf.ID, "items")
}

func TestCustomFieldHandler_EnableIndex_NonIndexableType(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)

	// Create a select field (not indexable)
	body := map[string]interface{}{
		"name":       "Category",
		"field_type": "select",
		"options":    `["A","B","C"]`,
	}
	req := testutils.CreateJSONRequest(t, "POST", "/api/custom-fields", body)
	createRR := testutils.ExecuteAuthenticatedRequest(t, handler.Create, req, nil)
	createRR.AssertStatusCode(http.StatusCreated)

	var cf models.CustomFieldDefinition
	createRR.AssertJSONResponse(&cf)

	// Try to enable indexing - should fail with 400
	rr := enableIndex(t, handler, cf.ID, "select", true, false)
	rr.AssertStatusCode(http.StatusBadRequest)
}

func TestCustomFieldHandler_DisableIndex(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// On Postgres the physical index is built asynchronously by the
	// CFVCleanupScheduler (covered by tests/custom_field_async_maintenance_test.go);
	// the white-box materialization path exercised here is SQLite-only.
	if testutils.IsPostgres() {
		t.Skip("physical custom-field index build is async on Postgres")
	}

	handler := createCustomFieldHandler(t, tdb)
	cf := createField(t, handler, "Cost", "number")

	// Enable
	rr := enableIndex(t, handler, cf.ID, "number", true, false)
	rr.AssertStatusCode(http.StatusOK)

	indexName := fmt.Sprintf("idx_cf_items_%d", cf.ID)
	materializeDeferredIndexes(t, tdb)
	assertIndexExists(t, tdb, indexName)

	// Disable
	rr = enableIndex(t, handler, cf.ID, "number", false, false)
	rr.AssertStatusCode(http.StatusOK)

	assertIndexNotExists(t, tdb, indexName)
	assertIndexRecordNotExists(t, tdb, cf.ID, "items")
}

func TestCustomFieldHandler_IndexLimit(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)

	// Set limit to 2 for testing
	_, err := tdb.Exec(`UPDATE system_settings SET value = '2' WHERE key = 'max_custom_field_indexes_per_table'`)
	if err != nil {
		t.Fatalf("Failed to update setting: %v", err)
	}

	// Create 3 number fields
	fields := make([]models.CustomFieldDefinition, 3)
	for i := 0; i < 3; i++ {
		fields[i] = createField(t, handler, fmt.Sprintf("Field %d", i), "number")
	}

	// Enable index on first two - should succeed
	for i := 0; i < 2; i++ {
		rr := enableIndex(t, handler, fields[i].ID, "number", true, false)
		rr.AssertStatusCode(http.StatusOK)
	}

	// Third should fail with 400
	rr := enableIndex(t, handler, fields[2].ID, "number", true, false)
	rr.AssertStatusCode(http.StatusBadRequest)

	// Verify error message contains count info
	var errResp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &errResp); err == nil {
		msg, _ := errResp["error"].(string)
		if msg == "" {
			t.Error("Expected error message about index limit")
		}
	}
}

func TestCustomFieldHandler_DeleteIndexedField(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// On Postgres the physical index is built asynchronously by the
	// CFVCleanupScheduler (covered by tests/custom_field_async_maintenance_test.go);
	// the white-box materialization path exercised here is SQLite-only.
	if testutils.IsPostgres() {
		t.Skip("physical custom-field index build is async on Postgres")
	}

	handler := createCustomFieldHandler(t, tdb)
	cf := createField(t, handler, "Indexed Cost", "number")

	// Enable index
	rr := enableIndex(t, handler, cf.ID, "number", true, false)
	rr.AssertStatusCode(http.StatusOK)

	indexName := fmt.Sprintf("idx_cf_items_%d", cf.ID)
	materializeDeferredIndexes(t, tdb)
	assertIndexExists(t, tdb, indexName)

	// Delete the field
	deleteReq := testutils.CreateJSONRequest(t, "DELETE", "/api/custom-fields/1", nil)
	deleteReq.SetPathValue("id", testutils.IntToString(cf.ID))
	deleteRR := testutils.ExecuteAuthenticatedRequest(t, handler.Delete, deleteReq, nil)
	deleteRR.AssertStatusCode(http.StatusNoContent)

	// Verify DB index is gone
	assertIndexNotExists(t, tdb, indexName)
}

func TestCustomFieldHandler_IndexOnMultipleTables(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// On Postgres the physical index is built asynchronously by the
	// CFVCleanupScheduler (covered by tests/custom_field_async_maintenance_test.go);
	// the white-box materialization path exercised here is SQLite-only.
	if testutils.IsPostgres() {
		t.Skip("physical custom-field index build is async on Postgres")
	}

	handler := createCustomFieldHandler(t, tdb)
	cf := createField(t, handler, "Multi Index", "number")

	// Enable on both tables
	rr := enableIndex(t, handler, cf.ID, "number", true, true)
	rr.AssertStatusCode(http.StatusOK)

	itemsIndex := fmt.Sprintf("idx_cf_items_%d", cf.ID)
	assetsIndex := fmt.Sprintf("idx_cf_assets_%d", cf.ID)

	materializeDeferredIndexes(t, tdb)
	assertIndexExists(t, tdb, itemsIndex)
	assertIndexExists(t, tdb, assetsIndex)
	assertIndexRecordExists(t, tdb, cf.ID, "items")
	assertIndexRecordExists(t, tdb, cf.ID, "assets")

	// Disable items only
	rr = enableIndex(t, handler, cf.ID, "number", false, true)
	rr.AssertStatusCode(http.StatusOK)

	assertIndexNotExists(t, tdb, itemsIndex)
	assertIndexExists(t, tdb, assetsIndex)
	assertIndexRecordNotExists(t, tdb, cf.ID, "items")
	assertIndexRecordExists(t, tdb, cf.ID, "assets")
}

func TestCustomFieldHandler_GetAll_IncludesIndexInfo(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)

	// Delete any system defaults
	_, _ = tdb.Exec("DELETE FROM custom_field_definitions")

	// Create a number field and index it
	cf := createField(t, handler, "Indexed Number", "number")
	rr := enableIndex(t, handler, cf.ID, "number", true, false)
	rr.AssertStatusCode(http.StatusOK)

	// GetAll and verify index info
	getAllReq := testutils.CreateJSONRequest(t, "GET", "/api/custom-fields", nil)
	getAllRR := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, getAllReq, nil)
	getAllRR.AssertStatusCode(http.StatusOK)

	var resp customFieldsResponse
	getAllRR.AssertJSONResponse(&resp)

	if len(resp.Data) != 1 {
		t.Fatalf("Expected 1 field, got %d", len(resp.Data))
	}

	field := resp.Data[0]
	if field.Indexed == nil {
		t.Fatal("Expected indexed info to be present")
	}
	if !field.Indexed.Items {
		t.Error("Expected items index to be true")
	}
	if field.Indexed.Assets {
		t.Error("Expected assets index to be false")
	}

	// Verify index counts
	if resp.IndexCounts["items"].Current != 1 {
		t.Errorf("Expected items index count 1, got %d", resp.IndexCounts["items"].Current)
	}
	if resp.IndexCounts["assets"].Current != 0 {
		t.Errorf("Expected assets index count 0, got %d", resp.IndexCounts["assets"].Current)
	}
	if resp.IndexCounts["items"].Max != 20 {
		t.Errorf("Expected items max 20, got %d", resp.IndexCounts["items"].Max)
	}
}

// --- UpdateSettings ---

func TestCustomFieldHandler_UpdateSettings(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)

	// Update limit to 10
	body := map[string]interface{}{
		"max_indexes_per_table": 10,
	}
	req := testutils.CreateJSONRequest(t, "PUT", "/api/admin/custom-fields/settings", body)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.UpdateSettings, req, nil)
	rr.AssertStatusCode(http.StatusOK)

	var settings customFieldSettings
	rr.AssertJSONResponse(&settings)
	if settings.MaxIndexesPerTable != 10 {
		t.Errorf("Expected max_indexes_per_table 10, got %d", settings.MaxIndexesPerTable)
	}

	// Verify GetAll returns new max
	getAllReq := testutils.CreateJSONRequest(t, "GET", "/api/custom-fields", nil)
	getAllRR := testutils.ExecuteAuthenticatedRequest(t, handler.GetAll, getAllReq, nil)
	getAllRR.AssertStatusCode(http.StatusOK)

	var resp customFieldsResponse
	getAllRR.AssertJSONResponse(&resp)

	if resp.IndexCounts["items"].Max != 10 {
		t.Errorf("Expected items max 10, got %d", resp.IndexCounts["items"].Max)
	}
	if resp.IndexCounts["assets"].Max != 10 {
		t.Errorf("Expected assets max 10, got %d", resp.IndexCounts["assets"].Max)
	}
}

func TestCustomFieldHandler_UpdateSettings_BelowUsage(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)

	// Create 3 number fields and index them
	for i := 0; i < 3; i++ {
		cf := createField(t, handler, fmt.Sprintf("Field %d", i), "number")
		rr := enableIndex(t, handler, cf.ID, "number", true, false)
		rr.AssertStatusCode(http.StatusOK)
	}

	// Try to set limit to 2 - should fail
	body := map[string]interface{}{
		"max_indexes_per_table": 2,
	}
	req := testutils.CreateJSONRequest(t, "PUT", "/api/admin/custom-fields/settings", body)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.UpdateSettings, req, nil)
	rr.AssertStatusCode(http.StatusBadRequest)
}

func TestCustomFieldHandler_UpdateSettings_InvalidValue(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := createCustomFieldHandler(t, tdb)

	tests := []struct {
		name  string
		value int
	}{
		{"Zero", 0},
		{"Negative", -5},
		{"Over max", 101},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := map[string]interface{}{
				"max_indexes_per_table": tt.value,
			}
			req := testutils.CreateJSONRequest(t, "PUT", "/api/admin/custom-fields/settings", body)
			rr := testutils.ExecuteAuthenticatedRequest(t, handler.UpdateSettings, req, nil)
			rr.AssertStatusCode(http.StatusBadRequest)
		})
	}
}
