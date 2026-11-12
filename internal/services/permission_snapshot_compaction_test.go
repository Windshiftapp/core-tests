//go:build test

package services

import (
	"fmt"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// A user snapshot must not embed an Everyone permission map per workspace: that
// made snapshot size scale with the total workspace count. The fallback is
// served by one shared, user-independent table instead.
func TestPermissionSnapshotDoesNotEmbedEveryonePerWorkspace(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()

	config := DefaultPermissionCacheConfig()
	config.WarmupOnStartup = false
	permService, err := NewPermissionService(db, config)
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	defer permService.Close()

	const workspaceCount = 40
	workspaceIDs := make([]int, 0, workspaceCount)
	for i := range workspaceCount {
		key := fmt.Sprintf("CMP%03d", i)
		if _, err := db.Exec(`
			INSERT INTO workspaces (name, key, description, active, is_personal, created_at, updated_at)
			VALUES (?, ?, '', true, false, ?, ?)
		`, fmt.Sprintf("Compaction %d", i), key, time.Now(), time.Now()); err != nil {
			t.Fatalf("insert workspace %d: %v", i, err)
		}
		workspaceIDs = append(workspaceIDs, int(fixtureWorkspaceID(t, db, key)))
	}
	if _, err := db.Exec(`
		INSERT INTO users (username, email, first_name, last_name, password_hash, created_at, updated_at)
		VALUES ('compact', 'compact@test.com', 'Com', 'Pact', 'hash', ?, ?)
	`, time.Now(), time.Now()); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID := int(fixtureUserID(t, db, "compact@test.com"))

	snapshot, err := permService.GetUserEffectivePermissions(userID)
	if err != nil {
		t.Fatalf("GetUserEffectivePermissions: %v", err)
	}
	if !snapshot.UsesSharedEveryone {
		t.Fatal("snapshot did not opt into the shared Everyone table")
	}
	if len(snapshot.WorkspaceEveryone) != 0 {
		t.Fatalf("snapshot embedded %d per-workspace Everyone entries, want 0", len(snapshot.WorkspaceEveryone))
	}

	// Open workspaces still resolve through the shared table.
	for _, wsID := range []int{workspaceIDs[0], workspaceIDs[workspaceCount-1]} {
		hasView, err := permService.HasWorkspacePermission(userID, wsID, models.PermissionItemView)
		if err != nil {
			t.Fatalf("HasWorkspacePermission(view) ws=%d: %v", wsID, err)
		}
		if !hasView {
			t.Fatalf("open workspace %d did not grant item.view via shared Everyone table", wsID)
		}
		hasAdmin, err := permService.HasWorkspacePermission(userID, wsID, models.PermissionWorkspaceAdmin)
		if err != nil {
			t.Fatalf("HasWorkspacePermission(admin) ws=%d: %v", wsID, err)
		}
		if hasAdmin {
			t.Fatalf("open workspace %d granted workspace.admin implicitly", wsID)
		}
	}

	// Locking one workspace down with an explicit Viewer assignment must remove
	// the fallback for every other user after the global invalidation hook runs.
	if _, err := db.Exec(`
		INSERT INTO users (username, email, first_name, last_name, password_hash, created_at, updated_at)
		VALUES ('locker', 'locker@test.com', 'Lock', 'Er', 'hash', ?, ?)
	`, time.Now(), time.Now()); err != nil {
		t.Fatalf("insert locker user: %v", err)
	}
	lockerID := int(fixtureUserID(t, db, "locker@test.com"))
	if _, err := db.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_by, granted_at)
		VALUES (?, ?, (SELECT id FROM workspace_roles WHERE builtin_key = ?), ?, ?)
	`, lockerID, workspaceIDs[0], models.RoleBuiltinViewer, lockerID, time.Now()); err != nil {
		t.Fatalf("assign Viewer: %v", err)
	}
	permService.OnEveryoneAccessChanged()

	hasView, err := permService.HasWorkspacePermission(userID, workspaceIDs[0], models.PermissionItemView)
	if err != nil {
		t.Fatalf("HasWorkspacePermission after lock-down: %v", err)
	}
	if hasView {
		t.Fatalf("locked-down workspace %d still granted item.view", workspaceIDs[0])
	}
	hasView, err = permService.HasWorkspacePermission(userID, workspaceIDs[1], models.PermissionItemView)
	if err != nil {
		t.Fatalf("HasWorkspacePermission on open workspace: %v", err)
	}
	if !hasView {
		t.Fatalf("open workspace %d lost item.view after unrelated lock-down", workspaceIDs[1])
	}

	// The materialized profile view reflects the same state.
	everyone, err := permService.EveryonePermissions()
	if err != nil {
		t.Fatalf("EveryonePermissions: %v", err)
	}
	if _, ok := everyone[workspaceIDs[0]]; ok {
		t.Fatalf("materialized Everyone fallback included locked-down workspace %d", workspaceIDs[0])
	}
	if _, ok := everyone[workspaceIDs[1]]; !ok {
		t.Fatalf("materialized Everyone fallback missing open workspace %d", workspaceIDs[1])
	}
}
