package aitools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
)

func TestItemSummaryIncludesRealIDAndVerbatimKey(t *testing.T) {
	model := &models.Item{
		ID:                  123,
		WorkspaceID:         7,
		WorkspaceKey:        "WI",
		WorkspaceItemNumber: 348,
		Title:               "Use the item key",
	}

	item := itemToSummary(model)
	body, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal item summary: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("decode item summary: %v", err)
	}
	if got := fields["key"]; got != "WI-348" {
		t.Fatalf("key = %v, want WI-348", got)
	}
	if got := fields["id"]; got != float64(model.ID) {
		t.Fatalf("id = %v, want %d: %s", got, model.ID, body)
	}
}

func TestResolveStatusNameUsesWorkspaceStatuses(t *testing.T) {
	dsn := fmt.Sprintf("file:aitools-resolve-status-%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := database.NewSQLiteDB(dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize sqlite: %v", err)
	}

	var want, unavailable int
	if err := db.QueryRow("SELECT id FROM statuses WHERE name = ?", "Done").Scan(&want); err != nil {
		t.Fatalf("lookup fixture status: %v", err)
	}
	if err := db.QueryRow("SELECT id FROM statuses WHERE name = ?", "Open").Scan(&unavailable); err != nil {
		t.Fatalf("lookup unavailable fixture status: %v", err)
	}
	var workflowID, configSetID, workspaceID int
	if err := db.QueryRow(`INSERT INTO workflows (name) VALUES ('AI tools status workflow') RETURNING id`).Scan(&workflowID); err != nil {
		t.Fatalf("insert workflow: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id) VALUES (?, NULL, ?)`, workflowID, want); err != nil {
		t.Fatalf("insert workflow status: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO configuration_sets (name, workflow_id) VALUES ('AI tools status config', ?) RETURNING id`, workflowID).Scan(&configSetID); err != nil {
		t.Fatalf("insert configuration set: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO workspaces (name, key, active) VALUES ('AI tools status workspace', 'AIT', true) RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, workspaceID, configSetID); err != nil {
		t.Fatalf("assign workspace configuration: %v", err)
	}

	got, err := resolveStatusName(db, "done", workspaceID)
	if err != nil {
		t.Fatalf("resolve status name: %v", err)
	}
	if got != want {
		t.Fatalf("resolve status name returned %d, want %d", got, want)
	}
	if _, err := resolveStatusName(db, "open", workspaceID); err == nil {
		t.Fatalf("globally existing but unavailable status %d unexpectedly resolved", unavailable)
	} else if !strings.Contains(err.Error(), "valid statuses: Done") {
		t.Fatalf("unavailable status error = %q, want workspace candidates", err)
	}
}
