package services

import (
	"context"
	"fmt"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/repository"
)

// TestAgentPresenceService_ForWorkspace pins the presence derivation for
// assignment pickers (WI-272): local binding → local, pool with a fresh
// heartbeat → online, pool with only stale runners → offline; agents without
// a ready binding are absent from the map.
func TestAgentPresenceService_ForWorkspace(t *testing.T) {
	ctx := context.Background()
	dsn := fmt.Sprintf("file:%s/presence.db?mode=memory&cache=shared", t.TempDir())
	db, err := database.NewSQLiteDBWithPoolSizes(dsn, 4, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize db: %v", err)
	}

	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	mustExec(`INSERT INTO workspaces(id, name, key, active) VALUES (1, 'ws', 'WS', true)`)
	for i, name := range []string{"local-agent", "online-agent", "offline-agent", "paused-agent"} {
		mustExec(`INSERT INTO users(id, email, username, first_name, last_name, is_active, is_agent)
		          VALUES (?, ?, ?, ?, 'agent', TRUE, TRUE)`, 10+i, name+"@agents.local", name, name)
	}

	// Bindings: user 10 local, user 11 on pool 1 (live runner), user 12 on
	// pool 2 (stale runner only), and user 13 paused.
	mustExec(`INSERT INTO workspace_agent_bindings(workspace_id, acting_user_id, acting_user_kind, target_pool_id, lifecycle, created_by_user_id)
	          VALUES (1, 10, 'agent', NULL, 'ready', 10), (1, 11, 'agent', 1, 'ready', 10),
	                 (1, 12, 'agent', 2, 'ready', 10), (1, 13, 'agent', NULL, 'paused', 10)`)

	now := time.Now().UTC()
	mustExec(`INSERT INTO runner_instances(pool_capability_id, name, credential_hash, status, registered_at, last_heartbeat_at)
	          VALUES (1, 'fresh', 'hash-fresh', 'active', ?, ?)`, now.Add(-time.Hour), now.Add(-10*time.Second))
	mustExec(`INSERT INTO runner_instances(pool_capability_id, name, credential_hash, status, registered_at, last_heartbeat_at)
	          VALUES (2, 'stale', 'hash-stale', 'active', ?, ?)`, now.Add(-time.Hour), now.Add(-30*time.Minute))

	svc := NewAgentPresenceService(
		repository.NewWorkspaceAgentBindingRepository(db),
		repository.NewRunnerRepository(db),
	)
	presence, err := svc.ForWorkspace(ctx, 1)
	if err != nil {
		t.Fatalf("ForWorkspace: %v", err)
	}

	want := map[int]string{10: AgentPresenceLocal, 11: AgentPresenceOnline, 12: AgentPresenceOffline}
	for userID, expected := range want {
		if got := presence[userID]; got != expected {
			t.Errorf("user %d: presence=%q want %q", userID, got, expected)
		}
	}
	if len(presence) != len(want) {
		t.Errorf("presence map has %d entries, want %d: %v", len(presence), len(want), presence)
	}
}
