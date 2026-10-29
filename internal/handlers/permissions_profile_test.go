//go:build test

package handlers

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// The permission profile is the SPA boot payload (WI-1444): it must stay
// compact — permission keys only, grouped per workspace — because the
// Everyone fallback materializes entries for every workspace the user can
// reach.
func TestPermissionHandler_GetUserPermissions_CompactProfile(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	setup := newServiceSetup(t, tdb)
	handler := createPermissionHandler(t, tdb)

	wsA := setup.CreateWorkspace("Profile Workspace A", "PWA")
	wsB := setup.CreateWorkspace("Profile Workspace B", "PWB")
	setup.GrantGlobal(data.UserID, models.PermissionWorkspaceCreate)

	// Two editor-role assignments grant overlapping keys; the profile must
	// dedupe them into one key list.
	for _, wsID := range []int{wsA, wsB} {
		if _, err := tdb.Exec(`
			INSERT INTO user_workspace_roles (user_id, workspace_id, role_id)
			SELECT ?, ?, id FROM workspace_roles WHERE builtin_key = ?
		`, data.UserID, wsID, models.RoleBuiltinEditor); err != nil {
			t.Fatalf("assign editor role on workspace %d: %v", wsID, err)
		}
	}

	req := testutils.CreateJSONRequest(t, "GET", "/api/permissions/users/1", nil)
	req.SetPathValue("userId", testutils.IntToString(data.UserID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetUserPermissions, req, nil)
	rr.AssertStatusCode(http.StatusOK)

	var summary models.UserPermissionSummary
	rr.AssertJSONResponse(&summary)

	if summary.UserID != data.UserID {
		t.Fatalf("user_id = %d, want %d", summary.UserID, data.UserID)
	}
	if summary.HasSystemAdmin {
		t.Fatal("has_system_admin = true, want false")
	}
	if !slices.Equal(summary.GlobalPermissions, []string{models.PermissionWorkspaceCreate}) {
		t.Fatalf("global_permissions = %v, want [%s]", summary.GlobalPermissions, models.PermissionWorkspaceCreate)
	}

	// Every reachable workspace appears with its Everyone/role keys.
	for _, wsID := range []int{wsA, wsB} {
		keys, ok := summary.WorkspacePermissions[wsID]
		if !ok {
			t.Fatalf("workspace %d missing from profile: %+v", wsID, summary.WorkspacePermissions)
		}
		for _, want := range []string{"item.view", "item.edit"} {
			if !slices.Contains(keys, want) {
				t.Fatalf("workspace %d keys = %v, want to contain %q", wsID, keys, want)
			}
		}
		if !slices.IsSorted(keys) {
			t.Fatalf("workspace %d keys not sorted: %v", wsID, keys)
		}
	}

	// Compactness contract: no joined display objects may leak into the body.
	body := rr.Body.String()
	for _, banned := range []string{`"permission_name"`, `"permission":`, `"workspace":`, `"granted_by_user"`} {
		if strings.Contains(body, banned) {
			t.Fatalf("profile body contains verbose field %s:\n%s", banned, body)
		}
	}
}

func TestPermissionHandler_GetUserPermissions_SystemAdminFlag(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	setup := newServiceSetup(t, tdb)
	handler := createPermissionHandler(t, tdb)

	setup.GrantGlobal(data.UserID, models.PermissionSystemAdmin)

	req := testutils.CreateJSONRequest(t, "GET", "/api/permissions/users/1", nil)
	req.SetPathValue("userId", testutils.IntToString(data.UserID))
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetUserPermissions, req, nil)
	rr.AssertStatusCode(http.StatusOK)

	var summary models.UserPermissionSummary
	rr.AssertJSONResponse(&summary)

	if !summary.HasSystemAdmin {
		t.Fatal("has_system_admin = false, want true")
	}
	if !slices.Contains(summary.GlobalPermissions, models.PermissionSystemAdmin) {
		t.Fatalf("global_permissions = %v, want to contain system.admin", summary.GlobalPermissions)
	}
}
