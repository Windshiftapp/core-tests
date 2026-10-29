//go:build test

package services

import (
	"testing"
	"time"

	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestInactiveGroupDeniesAssetSetPermissions(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	insertID := func(label, query string, args ...any) int {
		t.Helper()
		var id int
		if err := db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		return id
	}

	userID := insertID("user", `
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('inactive-asset-perm@example.com', 'inactive-asset-perm', 'Inactive', 'Perm')
		RETURNING id
	`)
	setID := insertID("asset set", `INSERT INTO asset_management_sets (name) VALUES ('Inactive group set') RETURNING id`)
	groupID := insertID("group", `INSERT INTO groups (name, is_active) VALUES ('Inactive asset admins', FALSE) RETURNING id`)
	if _, err := db.ExecWrite(`INSERT INTO group_members (group_id, user_id) VALUES (?, ?)`, groupID, userID); err != nil {
		t.Fatalf("insert group member: %v", err)
	}
	adminRoleID := insertID("admin role", `SELECT id FROM asset_roles WHERE name = 'Administrator'`)
	if _, err := db.ExecWrite(`
		INSERT INTO group_asset_set_roles (set_id, group_id, role_id) VALUES (?, ?, ?)
	`, setID, groupID, adminRoleID); err != nil {
		t.Fatalf("insert group asset role: %v", err)
	}

	permissions, err := NewPermissionService(db, PermissionCacheConfig{
		TTL:          time.Minute,
		MaxCacheSize: 8,
		BatchSize:    10,
	})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = permissions.cache.Close() })

	assetPerms := NewAssetPermissionService(repository.NewAssetRepository(db), permissions)

	assertPermission := func(want bool) {
		t.Helper()
		for _, key := range []string{AssetPermissionKeyView, AssetPermissionKeyAdmin} {
			allowed, err := assetPerms.HasAssetSetPermission(userID, setID, key)
			if err != nil {
				t.Fatalf("HasAssetSetPermission(%s): %v", key, err)
			}
			if allowed != want {
				t.Fatalf("HasAssetSetPermission(%s) = %v, want %v", key, allowed, want)
			}
		}
		role, err := assetPerms.GetUserSetRole(userID, setID)
		if err != nil {
			t.Fatalf("GetUserSetRole: %v", err)
		}
		if want && (role == nil || role.Name != "Administrator") {
			t.Fatalf("role = %#v, want Administrator", role)
		}
		if !want && role != nil {
			t.Fatalf("role = %#v, want nil", role)
		}
	}

	assertPermission(false)

	if _, err := db.ExecWrite(`UPDATE groups SET is_active = TRUE WHERE id = ?`, groupID); err != nil {
		t.Fatalf("reactivate group: %v", err)
	}
	assertPermission(true)

	if _, err := db.ExecWrite(`UPDATE groups SET is_active = FALSE WHERE id = ?`, groupID); err != nil {
		t.Fatalf("deactivate group: %v", err)
	}
	assertPermission(false)
}
