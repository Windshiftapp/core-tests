package repository

import (
	"context"
	"fmt"
	"testing"

	"windshift/internal/database"
)

func openAgentSecurityTestDB(t *testing.T) (database.Database, int, int) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s/agent_security_repo.db?mode=memory&cache=shared", t.TempDir())
	db, err := database.NewSQLiteDBWithPoolSizes(dsn, 4, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize db: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces(id, name, key, active) VALUES (1, 'WS', 'WS', TRUE)`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users(email, username, first_name, last_name, is_agent) VALUES ('svc@example.com','svc','S','',TRUE)`); err != nil {
		t.Fatalf("seed service user: %v", err)
	}
	var svcID int
	if err := db.QueryRow(`SELECT id FROM users WHERE username='svc'`).Scan(&svcID); err != nil {
		t.Fatalf("read service user id: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users(email, username, first_name, last_name) VALUES ('admin@example.com','admin','A','')`); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	var adminID int
	if err := db.QueryRow(`SELECT id FROM users WHERE username='admin'`).Scan(&adminID); err != nil {
		t.Fatalf("read admin id: %v", err)
	}
	return db, svcID, adminID
}

func TestAgentSecurityRepository_FlagDefaultsFalseAndCanBeFlipped(t *testing.T) {
	ctx := context.Background()
	db, _, _ := openAgentSecurityTestDB(t)
	repo := NewAgentSecurityRepository(db)

	enabled, err := repo.GetAllowCentralizedServiceUsers(ctx)
	if err != nil {
		t.Fatalf("get flag: %v", err)
	}
	if enabled {
		t.Fatal("flag must default to false")
	}
	if err := repo.SetAllowCentralizedServiceUsers(ctx, true); err != nil {
		t.Fatalf("set flag true: %v", err)
	}
	enabled, _ = repo.GetAllowCentralizedServiceUsers(ctx)
	if !enabled {
		t.Fatal("flag should now be true")
	}
	if err := repo.SetAllowCentralizedServiceUsers(ctx, false); err != nil {
		t.Fatalf("set flag false: %v", err)
	}
	enabled, _ = repo.GetAllowCentralizedServiceUsers(ctx)
	if enabled {
		t.Fatal("flag should now be false")
	}
}

func TestAgentSecurityRepository_AllowlistRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, svcID, adminID := openAgentSecurityTestDB(t)
	repo := NewAgentSecurityRepository(db)
	ws := 1

	if err := repo.AddAllowlistEntry(ctx, svcID, nil, &adminID, "broad grant"); err != nil {
		t.Fatalf("add any-workspace grant: %v", err)
	}
	if err := repo.AddAllowlistEntry(ctx, svcID, &ws, &adminID, "ws-scoped"); err != nil {
		t.Fatalf("add workspace grant: %v", err)
	}

	entries, err := repo.ListAllowlist(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("list len: want 2, got %d (%+v)", len(entries), entries)
	}

	allowedForWS, err := repo.IsAllowed(ctx, svcID, 1)
	if err != nil {
		t.Fatalf("is allowed ws=1: %v", err)
	}
	if !allowedForWS {
		t.Fatal("svc must be allowed for ws=1")
	}
	allowedForOther, err := repo.IsAllowed(ctx, svcID, 99)
	if err != nil {
		t.Fatalf("is allowed ws=99: %v", err)
	}
	if !allowedForOther {
		t.Fatal("svc must be allowed for ws=99 via any-workspace grant")
	}

	// Remove the any-workspace grant; svc still allowed for ws=1 only.
	n, err := repo.RemoveAllowlistEntry(ctx, svcID, nil)
	if err != nil {
		t.Fatalf("remove any-grant: %v", err)
	}
	if n != 1 {
		t.Fatalf("remove rowcount: want 1, got %d", n)
	}
	allowedForOther, _ = repo.IsAllowed(ctx, svcID, 99)
	if allowedForOther {
		t.Fatal("svc should no longer be allowed for ws=99 after removing any-workspace grant")
	}
	allowedForWS, _ = repo.IsAllowed(ctx, svcID, 1)
	if !allowedForWS {
		t.Fatal("svc must still be allowed for ws=1 via the surviving workspace grant")
	}

	// Remove the missing pair returns 0 rows, no error.
	n, err = repo.RemoveAllowlistEntry(ctx, svcID, nil)
	if err != nil {
		t.Fatalf("idempotent remove: %v", err)
	}
	if n != 0 {
		t.Errorf("idempotent remove rowcount: want 0, got %d", n)
	}
}

func TestAgentSecurityRepository_AddRejectsDuplicate(t *testing.T) {
	ctx := context.Background()
	db, svcID, adminID := openAgentSecurityTestDB(t)
	repo := NewAgentSecurityRepository(db)
	ws := 1
	if err := repo.AddAllowlistEntry(ctx, svcID, &ws, &adminID, "first"); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if err := repo.AddAllowlistEntry(ctx, svcID, &ws, &adminID, "second"); err == nil {
		t.Fatal("expected unique constraint violation on duplicate (user_id, workspace_id), got nil")
	}
}

func TestAgentSecurityRepository_AddAllowlistEntries_Batch(t *testing.T) {
	ctx := context.Background()
	db, svcID, adminID := openAgentSecurityTestDB(t)
	repo := NewAgentSecurityRepository(db)

	// Seed a second workspace so we can fan out the batch.
	if _, err := db.Exec(`INSERT INTO workspaces(id, name, key, active) VALUES (2, 'WS2', 'WS2', TRUE)`); err != nil {
		t.Fatalf("seed ws2: %v", err)
	}

	// Empty slice → single any-workspace grant.
	if err := repo.AddAllowlistEntries(ctx, svcID, nil, &adminID, "broad"); err != nil {
		t.Fatalf("batch add (any): %v", err)
	}
	entries, _ := repo.ListAllowlist(ctx)
	if len(entries) != 1 || entries[0].WorkspaceID != nil {
		t.Fatalf("expected 1 NULL-workspace row, got %+v", entries)
	}

	// Non-empty slice → one grant per id, atomically.
	if err := repo.AddAllowlistEntries(ctx, svcID, []int{1, 2}, &adminID, "ws-scoped"); err != nil {
		t.Fatalf("batch add (multi): %v", err)
	}
	entries, _ = repo.ListAllowlist(ctx)
	if len(entries) != 3 {
		t.Fatalf("expected 3 rows total after batch, got %d", len(entries))
	}

	// Conflicting batch (second id duplicates an existing row) must
	// roll back the whole batch — neither id 1 (dup) nor id 2 (would-
	// be-second new row, but already exists from the previous call)
	// should leave new rows. Here ids 1 and 2 are both already taken,
	// so the unique index trips on the very first INSERT inside the tx.
	if err := repo.AddAllowlistEntries(ctx, svcID, []int{1, 2}, &adminID, "dup"); err == nil {
		t.Fatal("expected unique violation on duplicate (user_id, workspace_id), got nil")
	}
	entries, _ = repo.ListAllowlist(ctx)
	if len(entries) != 3 {
		t.Errorf("rollback failed: total rows want 3, got %d", len(entries))
	}

	// Bogus workspace id (<=0) is rejected before any insert.
	if err := repo.AddAllowlistEntries(ctx, svcID, []int{-1}, &adminID, "bad"); err == nil {
		t.Fatal("expected validation error on non-positive workspace id, got nil")
	}
}
