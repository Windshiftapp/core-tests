package services

import (
	"context"
	"path/filepath"
	"testing"

	"windshift/internal/database"
	"windshift/internal/services/actiontemplates"
)

func TestApplyActionTemplateRollsBackOnGraphWriteFailure(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "action-template.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	var workspaceID int
	if err := db.QueryRow(`
		INSERT INTO workspaces (name, key, description, active, is_personal)
		VALUES ('Templates', 'TPL', '', true, false) RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if _, err := db.ExecWrite(`
		CREATE TRIGGER reject_template_node
		BEFORE INSERT ON action_nodes
		BEGIN
			SELECT RAISE(ABORT, 'injected node failure');
		END
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	templates := actiontemplates.Registry()
	if len(templates) == 0 {
		t.Fatal("embedded action template registry is empty")
	}
	if _, err := NewActionTemplateService(db).ApplyToWorkspace(context.Background(), templates[0].Key, workspaceID, 0); err == nil {
		t.Fatal("template application unexpectedly succeeded")
	}

	for _, table := range []string{"actions", "action_nodes", "action_edges"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s count = %d after rollback, want 0", table, count)
		}
	}
}
