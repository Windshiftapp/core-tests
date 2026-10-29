//go:build test

package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestAgentRunReadServiceListForItemEnforcesVisibility(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	itemID64, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Agent item",
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	itemID := int(itemID64)
	runs := repository.NewAgentRunRepository(db)
	runID, err := runs.Insert(context.Background(), &models.AgentRun{WorkspaceID: data.WorkspaceID, ItemID: &itemID})
	if err != nil {
		t.Fatalf("insert run: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO users (id, email, username, first_name, last_name, password_hash, is_active)
		VALUES (2, 'outsider@example.test', 'outsider', 'Out', 'Sider', '', TRUE)
	`); err != nil {
		t.Fatalf("insert outsider: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_by)
		SELECT ?, ?, id, ? FROM workspace_roles WHERE name = 'Viewer'
	`, data.UserID, data.WorkspaceID, data.UserID); err != nil {
		t.Fatalf("restrict workspace viewer access: %v", err)
	}

	permissions, err := NewPermissionService(db, PermissionCacheConfig{TTL: time.Minute, MaxCacheSize: 32})
	if err != nil {
		t.Fatalf("permission service: %v", err)
	}
	t.Cleanup(func() { _ = permissions.Close() })
	service := NewAgentRunReadService(repository.NewItemRepository(db), runs, permissions)

	got, err := service.ListForItem(context.Background(), data.UserID, itemID, 50, 0)
	if err != nil {
		t.Fatalf("visible list: %v", err)
	}
	if len(got) != 1 || got[0].ID != runID {
		t.Fatalf("visible runs = %+v, want run %d", got, runID)
	}

	_, err = service.ListForItem(context.Background(), 2, itemID, 50, 0)
	if !errors.Is(err, ErrAgentRunItemNotVisible) {
		t.Fatalf("outsider error = %v, want ErrAgentRunItemNotVisible", err)
	}
	_, err = service.ListForItem(context.Background(), data.UserID, 999999, 50, 0)
	if !errors.Is(err, ErrAgentRunItemNotVisible) {
		t.Fatalf("missing item error = %v, want ErrAgentRunItemNotVisible", err)
	}
}
