//go:build test

package handlers

import (
	"database/sql"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/testutils"
)

func TestJiraLinkParents_ValidatesGenericSubtaskSentinel(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)

	insertJiraImportConnection(t, tdb.DB, "generic-subtask-connection")
	insertJiraImportJob(t, tdb.DB, "generic-subtask-job", "generic-subtask-connection", "running", []string{"APP"})

	var genericTypeID, regularTypeID int
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE hierarchy_level = -1 LIMIT 1`).Scan(&genericTypeID); err != nil {
		t.Fatalf("load generic subtask type: %v", err)
	}
	if err := tdb.QueryRow(`SELECT id FROM item_types WHERE hierarchy_level = 2 LIMIT 1`).Scan(&regularTypeID); err != nil {
		t.Fatalf("load regular item type: %v", err)
	}

	// Deliberate legacy fixture: the Jira two-phase import briefly stages
	// items (including parentless generic subtasks) that the create path's
	// validators would reject; linkParents() then reconciles parent edges from
	// the Jira metadata. These rows bypass the production create path on
	// purpose, so each one gets an explicit unique canonical rank.
	insertItem := func(number, itemTypeID int, title string) int {
		t.Helper()
		var id int
		if err := tdb.QueryRow(`
			INSERT INTO items (workspace_id, workspace_item_number, item_type_id, title, description, frac_index)
			VALUES (?, ?, ?, ?, '', ?)
			RETURNING id
		`, data.WorkspaceID, number, itemTypeID, title, testutils.NextTestFracIndex()).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", title, err)
		}
		return id
	}

	regularParentID := insertItem(400, regularTypeID, "Regular Jira parent")
	validChildID := insertItem(401, genericTypeID, "Valid Jira generic subtask")
	genericParentID := insertItem(402, genericTypeID, "Invalid generic parent")
	invalidChildID := insertItem(403, genericTypeID, "Invalid nested generic subtask")

	for _, mapping := range []struct {
		jiraID      string
		jiraKey     string
		windshiftID int
		metadata    string
	}{
		{jiraID: "1", jiraKey: "APP-1", windshiftID: regularParentID, metadata: `{}`},
		{jiraID: "2", jiraKey: "APP-2", windshiftID: validChildID, metadata: `{"parent_key":"APP-1"}`},
		{jiraID: "3", jiraKey: "APP-3", windshiftID: genericParentID, metadata: `{}`},
		{jiraID: "4", jiraKey: "APP-4", windshiftID: invalidChildID, metadata: `{"parent_key":"APP-3"}`},
	} {
		if _, err := tdb.Exec(`
			INSERT INTO jira_import_id_mappings (
				job_id, entity_type, jira_id, jira_key, windshift_id, metadata_json
			)
			VALUES ('generic-subtask-job', 'item', ?, ?, ?, ?)
		`, mapping.jiraID, mapping.jiraKey, mapping.windshiftID, mapping.metadata); err != nil {
			t.Fatalf("insert Jira mapping %s: %v", mapping.jiraKey, err)
		}
	}

	handler := NewJiraImportHandler(tdb.DB, "jira-generic-subtask-test-secret", "", blobstore.Disabled())
	handler.linkParents("generic-subtask-job")

	var validParent sql.NullInt64
	if err := tdb.QueryRow(`SELECT parent_id FROM items WHERE id = ?`, validChildID).Scan(&validParent); err != nil {
		t.Fatalf("load valid child parent: %v", err)
	}
	if !validParent.Valid || int(validParent.Int64) != regularParentID {
		t.Fatalf("valid generic subtask parent = %v, want %d", validParent, regularParentID)
	}

	var invalidParent sql.NullInt64
	if err := tdb.QueryRow(`SELECT parent_id FROM items WHERE id = ?`, invalidChildID).Scan(&invalidParent); err != nil {
		t.Fatalf("load invalid child parent: %v", err)
	}
	if invalidParent.Valid {
		t.Fatalf("nested generic subtask parent = %d, want NULL", invalidParent.Int64)
	}
}
