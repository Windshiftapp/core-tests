//go:build test

package services

import (
	"context"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

func TestWorkspaceUserResolverFiltersToActionableUsers(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	workspaceID := testutils.InsertID(t, db, `
		INSERT INTO workspaces (name, key, active) VALUES ('Resolver', 'RSLV', true)
	`)
	insertUser := func(username string, active, agent bool) int {
		t.Helper()
		return testutils.InsertID(t, db, `
			INSERT INTO users (email, username, first_name, last_name, is_active, is_agent, timezone, language)
			VALUES (?, ?, ?, 'User', ?, ?, 'Europe/Zurich', 'de')
		`, username+"@example.com", username, username, active, agent)
	}

	directUserID := insertUser("direct", true, false)
	groupUserID := insertUser("group", true, false)
	deniedUserID := insertUser("denied", true, false)
	inactiveUserID := insertUser("inactive", false, false)
	readyAgentID := insertUser("ready-agent", true, true)
	unboundAgentID := insertUser("unbound-agent", true, true)
	pausedAgentID := insertUser("paused-agent", true, true)
	deniedAgentID := insertUser("denied-agent", true, true)

	var viewerRoleID int
	if err := db.QueryRow(`SELECT id FROM workspace_roles WHERE name = 'Viewer'`).Scan(&viewerRoleID); err != nil {
		t.Fatalf("load Viewer role: %v", err)
	}
	grantViewer := func(userID int) {
		t.Helper()
		if _, err := db.Exec(`
			INSERT INTO user_workspace_roles (user_id, workspace_id, role_id)
			VALUES (?, ?, ?)
		`, userID, workspaceID, viewerRoleID); err != nil {
			t.Fatalf("grant Viewer to user %d: %v", userID, err)
		}
	}
	for _, userID := range []int{directUserID, inactiveUserID, readyAgentID, unboundAgentID, pausedAgentID} {
		grantViewer(userID)
	}

	groupID := testutils.InsertID(t, db, `INSERT INTO groups (name, is_active) VALUES ('Resolver group', true)`)
	if _, err := db.Exec(`INSERT INTO group_members (group_id, user_id) VALUES (?, ?)`, groupID, groupUserID); err != nil {
		t.Fatalf("add group member: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO group_workspace_roles (group_id, workspace_id, role_id) VALUES (?, ?, ?)
	`, groupID, workspaceID, viewerRoleID); err != nil {
		t.Fatalf("grant group Viewer role: %v", err)
	}

	insertBinding := func(userID int, lifecycle models.AgentLifecycle) {
		t.Helper()
		if _, err := db.Exec(`
			INSERT INTO workspace_agent_bindings
				(workspace_id, acting_user_id, acting_user_kind, lifecycle, created_by_user_id)
			VALUES (?, ?, 'agent', ?, ?)
		`, workspaceID, userID, lifecycle, directUserID); err != nil {
			t.Fatalf("bind agent %d: %v", userID, err)
		}
	}
	insertBinding(readyAgentID, models.AgentLifecycleReady)
	insertBinding(pausedAgentID, models.AgentLifecyclePaused)
	insertBinding(deniedAgentID, models.AgentLifecycleReady)

	config := DefaultPermissionCacheConfig()
	config.WarmupOnStartup = false
	config.PreWarmActive = false
	permissions, err := NewPermissionService(db, config)
	if err != nil {
		t.Fatalf("permission service: %v", err)
	}
	t.Cleanup(func() { _ = permissions.Close() })
	resolver := NewWorkspaceUserResolver(db, permissions)

	users, err := resolver.List(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byID := make(map[int]models.User, len(users))
	for _, user := range users {
		byID[user.ID] = user
	}
	for _, userID := range []int{directUserID, groupUserID, readyAgentID} {
		if _, ok := byID[userID]; !ok {
			t.Errorf("actionable user %d missing from roster", userID)
		}
	}
	for _, userID := range []int{deniedUserID, inactiveUserID, unboundAgentID, pausedAgentID, deniedAgentID} {
		if _, ok := byID[userID]; ok {
			t.Errorf("non-actionable user %d present in roster", userID)
		}
	}
	if got := byID[readyAgentID].AgentPresence; got != AgentPresenceLocal {
		t.Errorf("ready agent presence = %q, want %q", got, AgentPresenceLocal)
	}
	if user := byID[directUserID]; user.Email != "" || user.Timezone != "" || user.Language != "" {
		t.Errorf("limited roster leaked sensitive fields: %+v", user)
	}

	checks := []struct {
		name   string
		userID int
		want   bool
	}{
		{name: "direct human", userID: directUserID, want: true},
		{name: "group human", userID: groupUserID, want: true},
		{name: "human without access", userID: deniedUserID, want: false},
		{name: "ready agent", userID: readyAgentID, want: true},
		{name: "unbound agent", userID: unboundAgentID, want: false},
		{name: "paused agent", userID: pausedAgentID, want: false},
		{name: "bound agent without access", userID: deniedAgentID, want: false},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			got, err := resolver.CanAct(context.Background(), check.userID, workspaceID)
			if err != nil {
				t.Fatalf("CanAct: %v", err)
			}
			if got != check.want {
				t.Errorf("CanAct = %v, want %v", got, check.want)
			}
		})
	}

	mentions := NewMentionService(db, nil, permissions)
	mentions.SetWorkspaceUserResolver(resolver)
	mentionedIDs, err := mentions.ResolveActionableMentionedUserIDs(
		"@direct @ready-agent @unbound-agent @denied", workspaceID,
	)
	if err != nil {
		t.Fatalf("ResolveActionableMentionedUserIDs: %v", err)
	}
	if len(mentionedIDs) != 2 || mentionedIDs[0] != directUserID || mentionedIDs[1] != readyAgentID {
		t.Fatalf("actionable mentioned IDs = %v, want [%d %d]", mentionedIDs, directUserID, readyAgentID)
	}
}
