//go:build test

package aitools

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"windshift/internal/auth"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

func newPlanningToolTestEnv(t *testing.T) (*Env, int) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	var workspaceID int
	if err := db.QueryRow(`
		INSERT INTO workspaces (name, key, active)
		VALUES ('Planning tools', 'PLAN', true)
		RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}

	config := services.DefaultPermissionCacheConfig()
	config.WarmupOnStartup = false
	permService, err := services.NewPermissionService(db, config)
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	t.Cleanup(func() { _ = permService.Close() })

	return &Env{
		DB:                     db,
		UserID:                 1,
		Username:               "testuser",
		Source:                 SourceMCP,
		AccessibleWorkspaceIDs: []int{workspaceID},
		PermService:            permService,
	}, workspaceID
}

func runPlanningTool(t *testing.T, env *Env, name string, input any) any {
	t.Helper()
	entry, ok := Default.Lookup(name)
	if !ok {
		t.Fatalf("tool %q is not registered", name)
	}
	if !reflect.DeepEqual(entry.Scopes, []string{auth.ScopeItemsWrite}) {
		t.Fatalf("%s scopes = %v, want [%s]", name, entry.Scopes, auth.ScopeItemsWrite)
	}

	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal %s args: %v", name, err)
	}
	args := entry.NewArgs()
	if err := json.Unmarshal(raw, args); err != nil {
		t.Fatalf("unmarshal %s args: %v", name, err)
	}
	result, err := entry.Run(context.Background(), env, args)
	if err != nil {
		t.Fatalf("run %s: %v", name, err)
	}
	return result
}

func TestCreateMilestoneTool(t *testing.T) {
	env, workspaceID := newPlanningToolTestEnv(t)

	result := runPlanningTool(t, env, "create_milestone", map[string]any{
		"workspace_id": workspaceID,
		"name":         "0.8.4",
		"description":  "Next release",
		"target_date":  "2026-08-15",
	})
	milestone, ok := result.(milestoneDTO)
	if !ok {
		t.Fatalf("result type = %T, want milestoneDTO", result)
	}
	if milestone.Name != "0.8.4" || milestone.Status != "planning" || milestone.WorkspaceID != workspaceID {
		t.Fatalf("unexpected milestone: %+v", milestone)
	}

	var isGlobal bool
	var persistedWorkspaceID int
	if err := env.DB.QueryRow(
		"SELECT is_global, workspace_id FROM milestones WHERE id = ?",
		milestone.ID,
	).Scan(&isGlobal, &persistedWorkspaceID); err != nil {
		t.Fatalf("query milestone: %v", err)
	}
	if isGlobal || persistedWorkspaceID != workspaceID {
		t.Fatalf("milestone scope = global:%v workspace:%d", isGlobal, persistedWorkspaceID)
	}
}

func TestCreateIterationTool(t *testing.T) {
	env, workspaceID := newPlanningToolTestEnv(t)

	result := runPlanningTool(t, env, "create_iteration", map[string]any{
		"workspace_id": workspaceID,
		"name":         "Sprint 12",
		"description":  "0.8.4 delivery",
		"start_date":   "2026-08-03",
		"end_date":     "2026-08-14",
	})
	iteration, ok := result.(iterationDTO)
	if !ok {
		t.Fatalf("result type = %T, want iterationDTO", result)
	}
	if iteration.Name != "Sprint 12" || iteration.Status != "planned" || iteration.WorkspaceID != workspaceID {
		t.Fatalf("unexpected iteration: %+v", iteration)
	}

	var isGlobal bool
	var persistedWorkspaceID int
	if err := env.DB.QueryRow(
		"SELECT is_global, workspace_id FROM iterations WHERE id = ?",
		iteration.ID,
	).Scan(&isGlobal, &persistedWorkspaceID); err != nil {
		t.Fatalf("query iteration: %v", err)
	}
	if isGlobal || persistedWorkspaceID != workspaceID {
		t.Fatalf("iteration scope = global:%v workspace:%d", isGlobal, persistedWorkspaceID)
	}
}

func TestPlanningCreateToolsRejectInvalidScopeAndDates(t *testing.T) {
	env, workspaceID := newPlanningToolTestEnv(t)

	result := runPlanningTool(t, env, "create_milestone", map[string]any{
		"workspace_id": workspaceID + 1,
		"name":         "Invisible",
	})
	if got := result.(map[string]string)["error"]; got != "workspace not found" {
		t.Fatalf("inaccessible workspace error = %q", got)
	}

	result = runPlanningTool(t, env, "create_iteration", map[string]any{
		"workspace_id": workspaceID,
		"name":         "Backwards sprint",
		"start_date":   "2026-08-14",
		"end_date":     "2026-08-03",
	})
	if got := result.(map[string]string)["error"]; got != "end_date must be on or after start_date" {
		t.Fatalf("invalid date range error = %q", got)
	}
}
