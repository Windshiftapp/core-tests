//go:build test

package services

import (
	"testing"

	"windshift/internal/testutils"
)

// WI-1682: the delegated asset surface must be reachable by set-level
// administrators, not only by holders of the global asset.manage permission.
func TestManagesAssetSetsIncludesSetAdministrators(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	data := tdb.SeedTestData(t)

	var userID int
	if err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('set-admin@example.com', 'set-admin', 'Set', 'Admin') RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}

	var setID int
	if err := db.QueryRow(`INSERT INTO asset_management_sets (name) VALUES ('Managed set') RETURNING id`).Scan(&setID); err != nil {
		t.Fatalf("create asset set: %v", err)
	}
	var adminRoleID int
	if err := db.QueryRow(`SELECT id FROM asset_roles WHERE name = 'Administrator'`).Scan(&adminRoleID); err != nil {
		t.Fatalf("load Administrator role: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO user_asset_set_roles (user_id, set_id, role_id, granted_by)
		VALUES (?, ?, ?, ?)
	`, userID, setID, adminRoleID, data.UserID); err != nil {
		t.Fatalf("grant set Administrator: %v", err)
	}

	permissions, err := NewPermissionService(db, DefaultPermissionCacheConfig())
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	t.Cleanup(func() { _ = permissions.cache.Close() })
	app := NewAssetApplicationService(db, permissions, nil, nil)

	manages, err := app.ManagesAssetSets(userID)
	if err != nil {
		t.Fatalf("ManagesAssetSets(set admin): %v", err)
	}
	if !manages {
		t.Fatal("set Administrator not recognized as asset-set manager")
	}

	// Demote to Viewer: the set stays accessible but is no longer manageable.
	var viewerRoleID int
	if err := db.QueryRow(`SELECT id FROM asset_roles WHERE name = 'Viewer'`).Scan(&viewerRoleID); err != nil {
		t.Fatalf("load Viewer role: %v", err)
	}
	if _, err := db.ExecWrite(`
		UPDATE user_asset_set_roles SET role_id = ? WHERE user_id = ? AND set_id = ?
	`, viewerRoleID, userID, setID); err != nil {
		t.Fatalf("demote set role: %v", err)
	}

	manages, err = app.ManagesAssetSets(userID)
	if err != nil {
		t.Fatalf("ManagesAssetSets(viewer): %v", err)
	}
	if manages {
		t.Fatal("set Viewer reported as asset-set manager")
	}
}
