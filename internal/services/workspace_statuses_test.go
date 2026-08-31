package services

import (
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

func insertWorkspaceStatusID(t *testing.T, db database.Database, query string, args ...any) int {
	t.Helper()
	var id int
	if err := db.QueryRow(query+" RETURNING id", args...).Scan(&id); err != nil {
		t.Fatalf("insert fixture: %v\nquery: %s", err, query)
	}
	return id
}

func statusNames(statuses []models.Status) map[string]bool {
	names := make(map[string]bool, len(statuses))
	for _, status := range statuses {
		names[status.Name] = true
	}
	return names
}

func TestWorkspaceServiceStatusesFollowEffectiveWorkflows(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.DB
	service := NewWorkspaceService(db)

	var categoryID int
	if err := db.QueryRow(`SELECT id FROM status_categories ORDER BY id LIMIT 1`).Scan(&categoryID); err != nil {
		t.Fatalf("load status category: %v", err)
	}
	alphaStart := insertWorkspaceStatusID(t, db,
		`INSERT INTO statuses (name, category_id) VALUES ('Alpha Start', ?)`, categoryID)
	alphaReview := insertWorkspaceStatusID(t, db,
		`INSERT INTO statuses (name, category_id) VALUES ('Alpha Review', ?)`, categoryID)
	betaStart := insertWorkspaceStatusID(t, db,
		`INSERT INTO statuses (name, category_id) VALUES ('Beta Start', ?)`, categoryID)
	betaDone := insertWorkspaceStatusID(t, db,
		`INSERT INTO statuses (name, category_id) VALUES ('Beta Done', ?)`, categoryID)
	rogueStatus := insertWorkspaceStatusID(t, db,
		`INSERT INTO statuses (name, category_id) VALUES ('System Only', ?)`, categoryID)

	alphaWorkflow := insertWorkspaceStatusID(t, db,
		`INSERT INTO workflows (name) VALUES ('Alpha Workflow')`)
	betaWorkflow := insertWorkspaceStatusID(t, db,
		`INSERT INTO workflows (name) VALUES ('Beta Workflow')`)
	for _, transition := range []struct {
		workflowID int
		fromID     any
		toID       int
	}{
		{alphaWorkflow, nil, alphaStart},
		{alphaWorkflow, alphaStart, alphaReview},
		{betaWorkflow, nil, betaStart},
		{betaWorkflow, betaStart, betaDone},
	} {
		if _, err := db.Exec(`
			INSERT INTO workflow_transitions (workflow_id, from_status_id, to_status_id)
			VALUES (?, ?, ?)
		`, transition.workflowID, transition.fromID, transition.toID); err != nil {
			t.Fatalf("insert transition: %v", err)
		}
	}

	alphaConfig := insertWorkspaceStatusID(t, db,
		`INSERT INTO configuration_sets (name, workflow_id) VALUES ('Alpha Config', ?)`, alphaWorkflow)
	betaConfig := insertWorkspaceStatusID(t, db,
		`INSERT INTO configuration_sets (name, workflow_id) VALUES ('Beta Config', ?)`, betaWorkflow)
	alphaWorkspace := insertWorkspaceStatusID(t, db,
		`INSERT INTO workspaces (name, key, active) VALUES ('Alpha', 'ALPHA', true)`)
	betaWorkspace := insertWorkspaceStatusID(t, db,
		`INSERT INTO workspaces (name, key, active) VALUES ('Beta', 'BETA', true)`)
	fallbackWorkspace := insertWorkspaceStatusID(t, db,
		`INSERT INTO workspaces (name, key, active) VALUES ('Fallback', 'FALLBACK', true)`)
	for _, assignment := range []struct{ workspaceID, configID int }{
		{alphaWorkspace, alphaConfig},
		{betaWorkspace, betaConfig},
	} {
		if _, err := db.Exec(`
			INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id)
			VALUES (?, ?)
		`, assignment.workspaceID, assignment.configID); err != nil {
			t.Fatalf("assign configuration set: %v", err)
		}
	}

	alphaStatuses, err := service.GetStatuses(alphaWorkspace)
	if err != nil {
		t.Fatalf("GetStatuses(alpha): %v", err)
	}
	alphaNames := statusNames(alphaStatuses)
	if len(alphaNames) != 2 || !alphaNames["Alpha Start"] || !alphaNames["Alpha Review"] {
		t.Fatalf("alpha statuses = %#v, want only Alpha Start/Review", alphaNames)
	}
	if alphaNames["Beta Done"] || alphaNames["System Only"] {
		t.Fatalf("alpha leaked unavailable statuses: %#v", alphaNames)
	}

	betaStatuses, err := service.GetStatuses(betaWorkspace)
	if err != nil {
		t.Fatalf("GetStatuses(beta): %v", err)
	}
	betaNames := statusNames(betaStatuses)
	if len(betaNames) != 2 || !betaNames["Beta Start"] || !betaNames["Beta Done"] {
		t.Fatalf("beta statuses = %#v, want only Beta Start/Done", betaNames)
	}
	if betaNames["Alpha Review"] || betaNames["System Only"] {
		t.Fatalf("beta leaked unavailable statuses: %#v", betaNames)
	}

	union, err := service.GetStatusesForWorkspaces([]int{alphaWorkspace, betaWorkspace})
	if err != nil {
		t.Fatalf("GetStatusesForWorkspaces: %v", err)
	}
	unionNames := statusNames(union)
	if len(unionNames) != 4 || unionNames["System Only"] {
		t.Fatalf("workspace union = %#v, want four workflow statuses and no system-only status", unionNames)
	}

	// No explicit workspace configuration must follow the global default
	// workflow rather than falling back to every system status.
	fallbackStatuses, err := service.GetStatuses(fallbackWorkspace)
	if err != nil {
		t.Fatalf("GetStatuses(fallback): %v", err)
	}
	if statusNames(fallbackStatuses)["System Only"] {
		t.Fatalf("fallback workspace exposed unrelated system status %d", rogueStatus)
	}
}
