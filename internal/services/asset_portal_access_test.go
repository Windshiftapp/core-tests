//go:build test

package services

import (
	"errors"
	"testing"

	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// assetPortalAccessFixture creates a set with an Administrator, a Viewer, and a
// user who only holds the global asset.manage permission with no role on the
// set. The last one must not be able to publish the set to portals.
type assetPortalAccessFixture struct {
	app        *AssetApplicationService
	repo       *repository.AssetRepository
	setID      int
	adminID    int
	viewerID   int
	manageOnly int
}

func newAssetPortalAccessFixture(t *testing.T) assetPortalAccessFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	insert := func(label, query string, args ...any) int {
		t.Helper()
		var id int
		if err := db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		return id
	}

	adminID := insert("admin user", "INSERT INTO users (email, username, first_name, last_name) VALUES ('portal-access-admin@example.test', 'portal-access-admin', 'Ada', 'Admin') RETURNING id")
	viewerID := insert("viewer user", "INSERT INTO users (email, username, first_name, last_name) VALUES ('portal-access-viewer@example.test', 'portal-access-viewer', 'Vic', 'Viewer') RETURNING id")
	manageOnlyID := insert("manager user", "INSERT INTO users (email, username, first_name, last_name) VALUES ('portal-access-manager@example.test', 'portal-access-manager', 'Mia', 'Manager') RETURNING id")
	setID := insert("asset set", "INSERT INTO asset_management_sets (name, created_by) VALUES ('Portal access set', ?) RETURNING id", adminID)

	if _, err := db.ExecWrite("INSERT INTO user_global_permissions (user_id, permission_id) SELECT ?, id FROM permissions WHERE permission_key = 'asset.manage'", manageOnlyID); err != nil {
		t.Fatalf("grant global asset.manage: %v", err)
	}

	repo := repository.NewAssetRepository(db)
	adminRoleID, err := repo.GetAssetRoleIDByName("Administrator")
	if err != nil {
		t.Fatalf("administrator role: %v", err)
	}
	viewerRoleID, err := repo.GetAssetRoleIDByName("Viewer")
	if err != nil {
		t.Fatalf("viewer role: %v", err)
	}
	if err := repo.AssignUserRole(setID, adminID, adminRoleID, adminID); err != nil {
		t.Fatalf("assign admin role: %v", err)
	}
	if err := repo.AssignUserRole(setID, viewerID, viewerRoleID, adminID); err != nil {
		t.Fatalf("assign viewer role: %v", err)
	}

	config := DefaultPermissionCacheConfig()
	config.WarmupOnStartup = false
	perms, err := NewPermissionService(db, config)
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = perms.Close() })

	return assetPortalAccessFixture{
		app:        NewAssetApplicationService(db, perms, nil, nil),
		repo:       repo,
		setID:      setID,
		adminID:    adminID,
		viewerID:   viewerID,
		manageOnly: manageOnlyID,
	}
}

func TestSetPortalAccessRequiresSetAdmin(t *testing.T) {
	f := newAssetPortalAccessFixture(t)

	tests := []struct {
		name     string
		userID   int
		wantDeny bool
	}{
		{name: "set administrator can grant", userID: f.adminID},
		{name: "set viewer cannot grant", userID: f.viewerID, wantDeny: true},
		{name: "global asset.manage without set role cannot grant", userID: f.manageOnly, wantDeny: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.app.SetPortalAccess(tt.userID, f.setID, true, AuditActor{UserID: tt.userID})
			if tt.wantDeny {
				if !errors.Is(err, ErrAssetForbidden) {
					t.Fatalf("SetPortalAccess error = %v, want ErrAssetForbidden", err)
				}
			} else if err != nil {
				t.Fatalf("SetPortalAccess: %v", err)
			}
			enabled, checkErr := f.repo.IsSetPortalEnabled(f.setID)
			if checkErr != nil {
				t.Fatalf("IsSetPortalEnabled: %v", checkErr)
			}
			if enabled != !tt.wantDeny {
				t.Fatalf("portal enabled = %v, want %v", enabled, !tt.wantDeny)
			}
			if !tt.wantDeny {
				if err := f.repo.RevokeSetPortalAccess(f.setID); err != nil {
					t.Fatalf("reset grant: %v", err)
				}
			}
		})
	}
}

func TestSetPortalAccessGrantPersistsAndRevokes(t *testing.T) {
	f := newAssetPortalAccessFixture(t)

	granted, err := f.app.SetPortalAccess(f.adminID, f.setID, true, AuditActor{UserID: f.adminID})
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if granted == nil || granted.SetID != f.setID {
		t.Fatalf("granted = %+v, want set %d", granted, f.setID)
	}
	if granted.GrantedBy == nil || *granted.GrantedBy != f.adminID {
		t.Fatalf("granted_by = %v, want %d", granted.GrantedBy, f.adminID)
	}

	set, err := f.app.GetSet(f.adminID, f.setID)
	if err != nil {
		t.Fatalf("GetSet: %v", err)
	}
	if set.PortalAccess == nil || set.PortalAccess.SetID != f.setID {
		t.Fatalf("set.PortalAccess = %+v, want grant on set %d", set.PortalAccess, f.setID)
	}

	revoked, err := f.app.SetPortalAccess(f.adminID, f.setID, false, AuditActor{UserID: f.adminID})
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if revoked != nil {
		t.Fatalf("revoked = %+v, want nil", revoked)
	}
	enabled, err := f.repo.IsSetPortalEnabled(f.setID)
	if err != nil {
		t.Fatalf("IsSetPortalEnabled: %v", err)
	}
	if enabled {
		t.Fatal("portal access still enabled after revoke")
	}
}

func TestPortalAccessReadRequiresSetView(t *testing.T) {
	f := newAssetPortalAccessFixture(t)

	if _, err := f.app.SetPortalAccess(f.adminID, f.setID, true, AuditActor{UserID: f.adminID}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	if _, err := f.app.PortalAccess(f.manageOnly, f.setID); !errors.Is(err, ErrAssetForbidden) {
		t.Fatalf("PortalAccess for role-less manager = %v, want ErrAssetForbidden", err)
	}
	access, err := f.app.PortalAccess(f.viewerID, f.setID)
	if err != nil {
		t.Fatalf("PortalAccess for viewer: %v", err)
	}
	if access == nil {
		t.Fatal("PortalAccess for viewer returned nil, want the grant")
	}
}
