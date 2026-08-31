package tests

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	"windshift/internal/database"
	"windshift/internal/wscli"
)

func insertWSCLIStatusID(t *testing.T, db database.Database, query string, args ...any) int {
	t.Helper()
	var id int
	if err := db.QueryRow(query+" RETURNING id", args...).Scan(&id); err != nil {
		t.Fatalf("insert CLI status fixture: %v\nquery: %s", err, query)
	}
	return id
}

func TestWSCLI_StatusListUsesWorkspaceWorkflows(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	world := SeedWorld(t, ts)
	db := ts.DB()

	var categoryID int
	if err := db.QueryRow(`SELECT category_id FROM statuses WHERE id = ?`, world.Statuses.InProgress).Scan(&categoryID); err != nil {
		t.Fatalf("load in-progress category: %v", err)
	}
	reviewName := fmt.Sprintf("Alpha Review %d", world.Alpha.ID)
	reviewID := insertWSCLIStatusID(t, db,
		`INSERT INTO statuses (name, category_id) VALUES (?, ?)`, reviewName, categoryID)

	alphaWorkflow := insertWSCLIStatusID(t, db,
		`INSERT INTO workflows (name) VALUES (?)`, fmt.Sprintf("Alpha CLI Workflow %d", world.Alpha.ID))
	betaWorkflow := insertWSCLIStatusID(t, db,
		`INSERT INTO workflows (name) VALUES (?)`, fmt.Sprintf("Beta CLI Workflow %d", world.Beta.ID))
	for _, transition := range []struct {
		workflowID int
		fromID     any
		toID       int
	}{
		{alphaWorkflow, nil, world.Statuses.Open},
		{alphaWorkflow, world.Statuses.Open, reviewID},
		{betaWorkflow, nil, world.Statuses.Open},
		{betaWorkflow, world.Statuses.Open, world.Statuses.Done},
	} {
		if _, err := db.Exec(`
			INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id)
			VALUES (?, ?, ?)
		`, transition.workflowID, transition.fromID, transition.toID); err != nil {
			t.Fatalf("insert workflow transition: %v", err)
		}
	}

	alphaConfig := insertWSCLIStatusID(t, db,
		`INSERT INTO configuration_sets (name, workflow_id) VALUES (?, ?)`,
		fmt.Sprintf("Alpha CLI Config %d", world.Alpha.ID), alphaWorkflow)
	betaConfig := insertWSCLIStatusID(t, db,
		`INSERT INTO configuration_sets (name, workflow_id) VALUES (?, ?)`,
		fmt.Sprintf("Beta CLI Config %d", world.Beta.ID), betaWorkflow)
	if _, err := db.Exec(`DELETE FROM workspace_configuration_sets WHERE workspace_id IN (?, ?)`, world.Alpha.ID, world.Beta.ID); err != nil {
		t.Fatalf("clear workspace configuration assignments: %v", err)
	}
	for _, assignment := range []struct{ workspaceID, configID int }{
		{world.Alpha.ID, alphaConfig},
		{world.Beta.ID, betaConfig},
	} {
		if _, err := db.Exec(`
			INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id)
			VALUES (?, ?)
		`, assignment.workspaceID, assignment.configID); err != nil {
			t.Fatalf("assign workspace configuration: %v", err)
		}
	}

	list := func(t *testing.T, workspaceKey string) wscli.StatusListResult {
		t.Helper()
		out, stderr, code := runWS(t, ts, "status", "ls", "-w", workspaceKey, "-o", "json")
		requireZero(t, code, stderr)
		var result wscli.StatusListResult
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatalf("decode status list: %v\nraw=%s", err, string(out))
		}
		return result
	}

	alpha := list(t, world.Alpha.Key)
	if alpha.Scope != "workspace" || alpha.Workspace == nil || alpha.Workspace.Key != world.Alpha.Key {
		t.Fatalf("alpha scope metadata = %+v", alpha)
	}
	alphaIDs := map[int]bool{}
	for _, status := range alpha.Statuses {
		alphaIDs[status.ID] = true
	}
	if len(alphaIDs) != 2 || !alphaIDs[world.Statuses.Open] || !alphaIDs[reviewID] || alphaIDs[world.Statuses.Done] {
		t.Fatalf("alpha statuses = %+v, want Open + Review only", alpha.Statuses)
	}

	beta := list(t, world.Beta.Key)
	if beta.Scope != "workspace" || beta.Workspace == nil || beta.Workspace.Key != world.Beta.Key {
		t.Fatalf("beta scope metadata = %+v", beta)
	}
	betaIDs := map[int]bool{}
	for _, status := range beta.Statuses {
		betaIDs[status.ID] = true
	}
	if len(betaIDs) != 2 || !betaIDs[world.Statuses.Open] || !betaIDs[world.Statuses.Done] || betaIDs[reviewID] {
		t.Fatalf("beta statuses = %+v, want Open + Done only", beta.Statuses)
	}

	// Use the status exactly as discovered by the workspace-scoped command.
	// This proves a listed status is a real move target for an applicable item.
	target := world.Items[0]
	if target.WorkspaceID != world.Alpha.ID || target.StatusID != world.Statuses.Open {
		t.Fatalf("unexpected move fixture: %+v", target)
	}
	_, stderr, code := runWS(t, ts,
		"task", "move", strconv.Itoa(target.ID), reviewName,
		"-w", world.Alpha.Key, "-o", "json",
	)
	requireZero(t, code, stderr)

	itemOut, stderr, code := runWS(t, ts, "task", "get", strconv.Itoa(target.ID), "-o", "json")
	requireZero(t, code, stderr)
	var moved struct {
		Status struct {
			ID int `json:"id"`
		} `json:"status"`
	}
	if err := json.Unmarshal(itemOut, &moved); err != nil {
		t.Fatalf("decode moved item: %v\nraw=%s", err, string(itemOut))
	}
	if moved.Status.ID != reviewID {
		t.Fatalf("item status = %d, want listed Review status %d", moved.Status.ID, reviewID)
	}
}
