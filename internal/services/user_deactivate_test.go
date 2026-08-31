package services

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"windshift/internal/database"
)

func TestUserDeactivationServiceOffboardsOwnerAndAllAgents(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "deactivation.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	insert := func(query string, args ...interface{}) int {
		t.Helper()
		result, err := db.ExecWrite(query, args...)
		if err != nil {
			t.Fatalf("insert fixture row: %v", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("LastInsertId: %v", err)
		}
		return int(id)
	}

	ownerID := insert(`INSERT INTO users (email, username, first_name, last_name, is_active) VALUES ('owner@example.com', 'owner', 'Owner', 'User', true)`)
	activeAgentID := insert(`INSERT INTO users (email, username, first_name, last_name, is_active, is_agent, agent_owner_user_id) VALUES ('active-agent@example.com', 'active-agent', 'Active', 'Agent', true, true, ?)`, ownerID)
	inactiveAgentID := insert(`INSERT INTO users (email, username, first_name, last_name, is_active, is_agent, agent_owner_user_id) VALUES ('inactive-agent@example.com', 'inactive-agent', 'Inactive', 'Agent', false, true, ?)`, ownerID)
	for index, userID := range []int{ownerID, activeAgentID, inactiveAgentID} {
		insert(`INSERT INTO api_tokens (user_id, name, token_hash, token_prefix) VALUES (?, ?, ?, ?)`, userID, "token", "hash-"+string(rune('a'+index)), "prefix")
	}

	var invalidatedTokens, invalidatedSessions []int
	var invalidatedPermissions []int
	service := NewUserDeactivationService(db, UserDeactivationInvalidators{
		Tokens: func(tokenIDs []int) {
			invalidatedTokens = append(invalidatedTokens, tokenIDs...)
		},
		Sessions: func(userID int) {
			invalidatedSessions = append(invalidatedSessions, userID)
		},
		Permissions: func(userID int) {
			invalidatedPermissions = append(invalidatedPermissions, userID)
		},
	})

	result, err := service.DeactivateUser(ownerID)
	if err != nil {
		t.Fatalf("DeactivateUser: %v", err)
	}
	if !reflect.DeepEqual(result.AgentIDs, []int{activeAgentID}) {
		t.Fatalf("newly deactivated agents = %v, want [%d]", result.AgentIDs, activeAgentID)
	}
	sort.Ints(result.OwnedAgentIDs)
	if !reflect.DeepEqual(result.OwnedAgentIDs, []int{activeAgentID, inactiveAgentID}) {
		t.Fatalf("owned agents = %v", result.OwnedAgentIDs)
	}
	if len(result.RevokedAPITokens) != 3 || len(invalidatedTokens) != 3 {
		t.Fatalf("revoked tokens result=%v invalidated=%v, want 3 each", result.RevokedAPITokens, invalidatedTokens)
	}

	sort.Ints(invalidatedSessions)
	if !reflect.DeepEqual(invalidatedSessions, []int{ownerID, activeAgentID, inactiveAgentID}) {
		t.Fatalf("invalidated sessions = %v", invalidatedSessions)
	}
	if !reflect.DeepEqual(invalidatedPermissions, []int{ownerID}) {
		t.Fatalf("invalidated permission owners = %v", invalidatedPermissions)
	}

	var activeUsers, tokenCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE id IN (?, ?, ?) AND is_active = true`, ownerID, activeAgentID, inactiveAgentID).Scan(&activeUsers); err != nil {
		t.Fatalf("count active users: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM api_tokens WHERE user_id IN (?, ?, ?)`, ownerID, activeAgentID, inactiveAgentID).Scan(&tokenCount); err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if activeUsers != 0 || tokenCount != 0 {
		t.Fatalf("after deactivation active_users=%d token_count=%d, want 0/0", activeUsers, tokenCount)
	}

	second, err := service.DeactivateUser(ownerID)
	if err != nil {
		t.Fatalf("idempotent DeactivateUser: %v", err)
	}
	if len(second.AgentIDs) != 0 || len(second.RevokedAPITokens) != 0 {
		t.Fatalf("idempotent result = %+v, want no new deactivations", second)
	}
}
