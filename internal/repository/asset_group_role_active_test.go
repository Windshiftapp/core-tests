//go:build test

package repository

import (
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// seedInactiveAssetGroupFixture creates an asset set, a user, and a group with
// a group-level Administrator role on the set. The group starts inactive so
// tests can prove inactive membership grants nothing before reactivating it.
func seedInactiveAssetGroupFixture(t *testing.T) (database.Database, *AssetRepository, int, int, int) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	var userID, setID, groupID int
	if err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('inactive-asset-group@example.com', 'inactive-asset-group', 'Inactive', 'Group')
		RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO asset_management_sets (name) VALUES ('Inactive group set') RETURNING id
	`).Scan(&setID); err != nil {
		t.Fatalf("insert asset set: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO groups (name, is_active) VALUES ('Asset administrators', FALSE) RETURNING id
	`).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if _, err := db.ExecWrite(`INSERT INTO group_members (group_id, user_id) VALUES (?, ?)`, groupID, userID); err != nil {
		t.Fatalf("insert group member: %v", err)
	}
	var adminRoleID int
	if err := db.QueryRow(`SELECT id FROM asset_roles WHERE name = 'Administrator'`).Scan(&adminRoleID); err != nil {
		t.Fatalf("lookup Administrator role: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO group_asset_set_roles (set_id, group_id, role_id)
		VALUES (?, ?, ?)
	`, setID, groupID, adminRoleID); err != nil {
		t.Fatalf("insert group asset role: %v", err)
	}
	return db, NewAssetRepository(db), userID, setID, groupID
}

func TestInactiveGroupDoesNotGrantAssetSetRoleOrVisibility(t *testing.T) {
	db, repo, userID, setID, groupID := seedInactiveAssetGroupFixture(t)

	// An inactive group must not resolve a role.
	role, err := repo.GetUserSetRole(userID, setID)
	if err != nil {
		t.Fatalf("GetUserSetRole (inactive): %v", err)
	}
	if role != nil {
		t.Fatalf("inactive group granted role %q, want nil", role.Name)
	}

	// ...nor expose the set through set listing.
	sets, err := repo.ListSetsForUser(userID, false)
	if err != nil {
		t.Fatalf("ListSetsForUser (inactive): %v", err)
	}
	if len(sets) != 0 {
		t.Fatalf("inactive group exposed sets: %#v", sets)
	}

	// Reactivating the group restores the inherited role and visibility.
	if _, err := db.ExecWrite(`UPDATE groups SET is_active = TRUE WHERE id = ?`, groupID); err != nil {
		t.Fatalf("reactivate group: %v", err)
	}
	role, err = repo.GetUserSetRole(userID, setID)
	if err != nil {
		t.Fatalf("GetUserSetRole (reactivated): %v", err)
	}
	if role == nil || role.Name != "Administrator" {
		t.Fatalf("reactivated group role = %#v, want Administrator", role)
	}
	sets, err = repo.ListSetsForUser(userID, false)
	if err != nil {
		t.Fatalf("ListSetsForUser (reactivated): %v", err)
	}
	if len(sets) != 1 || sets[0].ID != setID {
		t.Fatalf("reactivated group sets = %#v, want set %d", sets, setID)
	}

	// Deactivating again revokes both again.
	if _, err := db.ExecWrite(`UPDATE groups SET is_active = FALSE WHERE id = ?`, groupID); err != nil {
		t.Fatalf("deactivate group: %v", err)
	}
	role, err = repo.GetUserSetRole(userID, setID)
	if err != nil {
		t.Fatalf("GetUserSetRole (re-deactivated): %v", err)
	}
	if role != nil {
		t.Fatalf("re-deactivated group granted role %q, want nil", role.Name)
	}
	sets, err = repo.ListSetsForUser(userID, false)
	if err != nil {
		t.Fatalf("ListSetsForUser (re-deactivated): %v", err)
	}
	if len(sets) != 0 {
		t.Fatalf("re-deactivated group exposed sets: %#v", sets)
	}
}

func TestDirectAssetSetRoleSurvivesInactiveGroupMembership(t *testing.T) {
	db, repo, userID, setID, _ := seedInactiveAssetGroupFixture(t)

	var editorRoleID int
	if err := db.QueryRow(`SELECT id FROM asset_roles WHERE name = 'Editor'`).Scan(&editorRoleID); err != nil {
		t.Fatalf("lookup Editor role: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO user_asset_set_roles (user_id, set_id, role_id) VALUES (?, ?, ?)
	`, userID, setID, editorRoleID); err != nil {
		t.Fatalf("insert direct asset role: %v", err)
	}

	role, err := repo.GetUserSetRole(userID, setID)
	if err != nil {
		t.Fatalf("GetUserSetRole: %v", err)
	}
	if role == nil || role.Name != "Editor" {
		t.Fatalf("direct role = %#v, want Editor", role)
	}
	sets, err := repo.ListSetsForUser(userID, false)
	if err != nil {
		t.Fatalf("ListSetsForUser: %v", err)
	}
	if len(sets) != 1 || sets[0].ID != setID {
		t.Fatalf("direct grant sets = %#v, want set %d", sets, setID)
	}
}

// TestFindSetGroupRolesResolvesGroupNameNotTeamName guards the listing against
// the wrong join that once lived in GetSetGroupRoles: team_groups has no name
// column and its id is not groups.id, so resolving the role's group through
// team_groups errors instead of returning the group name.
func TestFindSetGroupRolesResolvesGroupNameNotTeamName(t *testing.T) {
	db, repo, _, setID, groupID := seedInactiveAssetGroupFixture(t)

	var teamID int
	if err := db.QueryRow(`INSERT INTO teams (name) VALUES ('Unrelated team name') RETURNING id`).Scan(&teamID); err != nil {
		t.Fatalf("insert team: %v", err)
	}
	// Force the team_groups row to share the group's id so the old
	// gasr.group_id = tg.id join would match and expose the team name.
	if _, err := db.ExecWrite(`
		INSERT INTO team_groups (id, team_id, group_id) VALUES (?, ?, ?)
	`, groupID, teamID, groupID); err != nil {
		t.Fatalf("insert team group: %v", err)
	}

	roles, err := repo.FindSetGroupRolesByGrantDate(setID)
	if err != nil {
		t.Fatalf("FindSetGroupRolesByGrantDate: %v", err)
	}
	if len(roles) != 1 {
		t.Fatalf("group roles = %#v, want exactly one", roles)
	}
	if roles[0].GroupName != "Asset administrators" {
		t.Fatalf("group name = %q, want %q", roles[0].GroupName, "Asset administrators")
	}
	if roles[0].RoleName != "Administrator" {
		t.Fatalf("role name = %q, want Administrator", roles[0].RoleName)
	}
}
