package repository

import (
	"database/sql"
	"path/filepath"
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

type activityAttributionReadCountingDB struct {
	database.Database
	reads int
}

func (db *activityAttributionReadCountingDB) Query(query string, args ...interface{}) (*sql.Rows, error) {
	db.reads++
	return db.Database.Query(query, args...)
}

func TestActivityAttributionUsesOneJoinedRead(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "activity-attribution.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	insertID := func(label, query string, args ...interface{}) int {
		t.Helper()
		result, err := db.ExecWrite(query, args...)
		if err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("LastInsertId for %s: %v", label, err)
		}
		return int(id)
	}

	ownerID := insertID("owner", `INSERT INTO users (email, username, first_name, last_name) VALUES ('owner@example.test', 'owner', 'Agent', 'Owner')`)
	agentID := insertID("agent", `INSERT INTO users (email, username, first_name, last_name, is_agent, agent_owner_user_id) VALUES ('agent@example.test', 'agent', 'Task', 'Agent', true, ?)`, ownerID)
	workspaceID := insertID("workspace", `INSERT INTO workspaces (name, key) VALUES ('Attribution', 'ATTR')`)
	itemID := insertID("item", `INSERT INTO items (workspace_id, workspace_item_number, title, frac_index) VALUES (?, 1, 'Attributed item', ?)`, workspaceID, testutils.NextTestFracIndex())
	insertID("history", `INSERT INTO item_history (item_id, user_id, field_name, new_value) VALUES (?, ?, 'title', 'Updated')`, itemID, agentID)
	pageID := insertID("page", `INSERT INTO pages (workspace_id, title, slug, created_by) VALUES (?, 'Attributed page', 'attributed-page', ?)`, workspaceID, ownerID)
	insertID("revision", `INSERT INTO page_revisions (page_id, revision_number, title, slug, created_by) VALUES (?, 1, 'Attributed page', 'attributed-page', ?)`, pageID, ownerID)

	countingDB := &activityAttributionReadCountingDB{Database: db}
	history, err := NewItemRepository(countingDB).GetHistoryWithApprovals(itemID, true)
	if err != nil {
		t.Fatalf("GetHistoryWithApprovals: %v", err)
	}
	if countingDB.reads != 1 || len(history) != 1 || history[0].AgentOwnerName != "Agent Owner" {
		t.Fatalf("history reads/results = %d/%+v, want one joined read with owner", countingDB.reads, history)
	}

	countingDB.reads = 0
	history, err = NewItemRepository(countingDB).GetHistoryWithApprovals(itemID, false)
	if err != nil {
		t.Fatalf("GetHistoryWithApprovals filtered: %v", err)
	}
	if countingDB.reads != 1 || history[0].AgentOwnerName != "" {
		t.Fatalf("filtered history reads/results = %d/%+v, want one read without owner label", countingDB.reads, history)
	}

	countingDB.reads = 0
	revisions, err := NewPageRepository(countingDB).ListRevisions(pageID, 50, 0)
	if err != nil {
		t.Fatalf("ListRevisions: %v", err)
	}
	if countingDB.reads != 1 || len(revisions) != 1 || revisions[0].Author == nil || revisions[0].Author.Name != "Agent Owner" {
		t.Fatalf("revision reads/results = %d/%+v, want one joined read with author", countingDB.reads, revisions)
	}
}
