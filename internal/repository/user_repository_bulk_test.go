//go:build test

package repository

import (
	"context"
	"database/sql"
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

type userBulkReadCountingDB struct {
	database.Database
	reads int
}

func (db *userBulkReadCountingDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	db.reads++
	return db.Database.QueryContext(ctx, query, args...)
}

func TestUserListByIDsUsesOneQuery(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	insert := func(name string, active bool) int {
		t.Helper()
		var id int
		if err := db.QueryRow(`
			INSERT INTO users (email, username, first_name, last_name, is_active)
			VALUES (?, ?, ?, 'User', ?) RETURNING id
		`, name+"@example.test", name, name, active).Scan(&id); err != nil {
			t.Fatalf("insert user %s: %v", name, err)
		}
		return id
	}
	first := insert("bulk-first", true)
	second := insert("bulk-second", true)
	inactive := insert("bulk-inactive", false)

	counting := &userBulkReadCountingDB{Database: db}
	users, err := NewUserRepository(counting).ListByIDs(context.Background(), []int{first, second, inactive, 999999})
	if err != nil {
		t.Fatalf("ListByIDs: %v", err)
	}
	if counting.reads != 1 {
		t.Fatalf("read queries = %d, want 1", counting.reads)
	}
	if len(users) != 3 {
		t.Fatalf("users = %d, want 3 matching rows (including inactive)", len(users))
	}
	seen := map[int]bool{}
	for _, user := range users {
		seen[user.ID] = true
	}
	for _, id := range []int{first, second, inactive} {
		if !seen[id] {
			t.Fatalf("user %d missing from %+v", id, users)
		}
	}
}
