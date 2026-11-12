//go:build test

package handlers

import (
	"testing"
	"time"

	"windshift/internal/blobstore"
	"windshift/internal/jira"
	"windshift/internal/repository"
)

// A group that already exists (for example, provisioned by LDAP or SCIM) must be
// reused as-is; the import must never rewrite its membership.
func TestEnsureJiraGroupsReusesExistingWithoutTouchingMembers(t *testing.T) {
	t.Parallel()

	db := newJiraServiceManagementTestDB(t)
	handler := NewJiraImportHandler(db, "jira-groups-test-secret", "", blobstore.Disabled())
	repo := repository.NewGroupRepository(db)

	existingID, err := repo.Create("jira-administrators", "provisioned by LDAP", nil, time.Now())
	if err != nil {
		t.Fatalf("seed existing group: %v", err)
	}

	groups := []jira.JiraGroup{
		{Name: "jira-administrators"},
		{Name: "developers"},
	}
	result := handler.ensureJiraGroups(groups, nil)

	if result["jira-administrators"] != int(existingID) {
		t.Fatalf("existing group mapped to %d, want %d", result["jira-administrators"], existingID)
	}
	if result["developers"] == 0 {
		t.Fatalf("new group was not created: %#v", result)
	}
	members, err := repo.ListMembers(int(existingID))
	if err != nil {
		t.Fatalf("list existing group members: %v", err)
	}
	if len(members) != 0 {
		t.Fatalf("existing group membership was modified: %#v", members)
	}
}

func TestEnsureJiraProjectRolesResolvesAliasAndCreatesCustom(t *testing.T) {
	t.Parallel()

	db := newJiraServiceManagementTestDB(t)
	handler := NewJiraImportHandler(db, "jira-roles-test-secret", "", blobstore.Disabled())

	roleRepo := repository.NewWorkspaceRoleRepository(db)
	existingRoles, err := roleRepo.List()
	if err != nil {
		t.Fatalf("list workspace roles: %v", err)
	}
	builtinAdministrator := 0
	for _, role := range existingRoles {
		if role.Name == "Administrator" {
			builtinAdministrator = role.ID
		}
	}
	if builtinAdministrator == 0 {
		t.Fatal("built-in Administrator role missing from the test schema")
	}

	roles := []jira.JiraProjectRole{
		{Name: "Administrators"},
		{Name: "QA Engineers"},
	}
	result := handler.ensureJiraProjectRoles("WSCL", 1, roles, nil, nil)

	if result["Administrators"] != builtinAdministrator {
		t.Fatalf("Administrators resolved to %d, want built-in %d", result["Administrators"], builtinAdministrator)
	}
	if result["QA Engineers"] == 0 {
		t.Fatalf("custom role was not created: %#v", result)
	}

	rolesAfter, err := roleRepo.List()
	if err != nil {
		t.Fatalf("list workspace roles after import: %v", err)
	}
	found := false
	for _, role := range rolesAfter {
		if role.Name == "QA Engineers" {
			found = true
		}
	}
	if !found {
		t.Fatal("custom role QA Engineers was not persisted")
	}
}
