//go:build test

package handlers

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

func TestPrivateVerificationRunReadsRequireWorkspaceAdmin(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	if _, err := tdb.Exec(`
		INSERT INTO users
			(id, email, username, first_name, last_name, password_hash, is_active)
		VALUES
			(2, 'run-viewer@example.test', 'run-viewer', 'Run', 'Viewer', '', true)
	`); err != nil {
		t.Fatalf("insert viewer: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO user_workspace_roles(user_id, workspace_id, role_id, granted_by)
		SELECT 2, ?, id, 1 FROM workspace_roles WHERE name = 'Viewer'
	`, data.WorkspaceID); err != nil {
		t.Fatalf("grant viewer role: %v", err)
	}

	permissions, err := services.NewPermissionService(db, services.PermissionCacheConfig{
		TTL:             time.Minute,
		MaxCacheSize:    32,
		WarmupOnStartup: false,
		PreWarmActive:   false,
		BatchSize:       10,
	})
	if err != nil {
		t.Fatalf("permission service: %v", err)
	}
	t.Cleanup(func() { _ = permissions.Close() })
	runs := repository.NewAgentRunRepository(db)
	runID, err := runs.Insert(context.Background(), &models.AgentRun{
		WorkspaceID: data.WorkspaceID,
		IsEphemeral: true,
	})
	if err != nil {
		t.Fatalf("insert private run: %v", err)
	}
	if err := runs.AppendEvent(context.Background(), runID, "result", `{"answer":"private"}`); err != nil {
		t.Fatalf("append private event: %v", err)
	}
	handler := NewAgentRunHandler(
		runs,
		nil,
		permissions,
		repository.NewItemRepository(db),
		nil,
	)
	viewer := testutils.TestUserWithID(2)
	admin := testutils.DefaultTestUser()

	get := func(user *models.User) *testutils.ResponseRecorder {
		t.Helper()
		req := testutils.CreateJSONRequest(t, http.MethodGet, "/api/agent-runs/1", nil)
		req.SetPathValue("id", strconv.Itoa(runID))
		return testutils.ExecuteAuthenticatedRequest(t, handler.Get, req, user)
	}
	events := func(user *models.User) *testutils.ResponseRecorder {
		t.Helper()
		req := testutils.CreateJSONRequest(t, http.MethodGet, "/api/agent-runs/1/events", nil)
		req.SetPathValue("id", strconv.Itoa(runID))
		return testutils.ExecuteAuthenticatedRequest(t, handler.Events, req, user)
	}

	get(viewer).AssertStatusCode(http.StatusForbidden)
	events(viewer).AssertStatusCode(http.StatusForbidden)
	get(admin).AssertStatusCode(http.StatusOK)
	events(admin).AssertStatusCode(http.StatusOK)
}
