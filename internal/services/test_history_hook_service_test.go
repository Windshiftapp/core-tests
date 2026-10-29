//go:build test

package services

import (
	"context"
	"testing"
	"time"

	"windshift/internal/testutils"
)

type historyHookFixture struct {
	db      *testutils.TestDB
	service *TestHistoryHookService
	itemID  int
	userID  int
}

func newHistoryHookFixture(t *testing.T) *historyHookFixture {
	t.Helper()
	db := testutils.CreateTestDB(t, false)
	workspaceID := planningScopeInsertID(t, db, `
		INSERT INTO workspaces (name, key, description, active, is_personal)
		VALUES ('Hook', 'HOOK', '', true, false)
	`)
	userID := planningScopeInsertID(t, db, `
		INSERT INTO users (email, username, first_name, last_name, password_hash, is_active)
		VALUES ('hook@example.test', 'hook', 'Hook', 'Test', '', true)
	`)
	created := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
	itemID64, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: workspaceID,
		Title:       "Hook seed item",
		CreatedAt:   &created,
		UpdatedAt:   &created,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	itemID := int(itemID64)
	// This fixture owns the exact history rows it backdates; drop the creation
	// history CreateItem records so the seeded set stays deterministic.
	if _, err := db.ExecWrite("DELETE FROM item_history WHERE item_id = ?", itemID); err != nil {
		t.Fatalf("clear creation history: %v", err)
	}
	seedHistory := func(field, oldValue, newValue string) {
		t.Helper()
		if _, err := db.ExecWrite(`
			INSERT INTO item_history (item_id, user_id, changed_at, field_name, old_value, new_value)
			VALUES (?, ?, ?, ?, ?, ?)
		`, itemID, userID, created, field, oldValue, newValue); err != nil {
			t.Fatalf("insert %s history: %v", field, err)
		}
	}
	seedHistory("title", "", "Hook seed item")
	seedHistory("status_id", "", "1")
	seedHistory("status_id", "1", "2")
	return &historyHookFixture{db: db, service: NewTestHistoryHookService(db), itemID: itemID, userID: userID}
}

func (f *historyHookFixture) changedAt(t *testing.T, field, oldValue, newValue string) time.Time {
	t.Helper()
	var stored string
	err := f.db.QueryRow(`
		SELECT changed_at FROM item_history
		WHERE item_id = ? AND field_name = ? AND old_value = ? AND new_value = ?
	`, f.itemID, field, oldValue, newValue).Scan(&stored)
	if err != nil {
		t.Fatalf("read %s history: %v", field, err)
	}
	parsed, ok := analyticsDBTime(stored)
	if !ok {
		t.Fatalf("parse stored changed_at %q", stored)
	}
	return parsed
}

func (f *historyHookFixture) itemCreatedAt(t *testing.T) time.Time {
	t.Helper()
	var stored string
	err := f.db.QueryRow(`SELECT created_at FROM items WHERE id = ?`, f.itemID).Scan(&stored)
	if err != nil {
		t.Fatalf("read created_at: %v", err)
	}
	parsed, ok := analyticsDBTime(stored)
	if !ok {
		t.Fatalf("parse stored created_at %q", stored)
	}
	return parsed
}

func ts(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02 15:04:05", value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return parsed
}

func hookStr(value string) *string { return &value }

func TestHistoryHookBackdateShiftsMatchedRowsOnly(t *testing.T) {
	f := newHistoryHookFixture(t)

	updated, err := f.service.Backdate(context.Background(), HistoryBackdateRequest{
		Items: []HistoryBackdateItem{
			{
				ItemID: f.itemID,
				Set: []HistoryBackdateEntry{
					// Wildcard first: every row lands at the base time...
					{FieldName: "*", ChangedAt: ts(t, "2026-09-07 09:30:00")},
					// ...then the transition row overrides to its own date.
					{
						FieldName: "status_id",
						OldValue:  hookStr("1"),
						NewValue:  hookStr("2"),
						ChangedAt: ts(t, "2026-09-12 16:00:00"),
					},
					// A non-matching row must not blow up or change anything.
					{
						FieldName: "story_points",
						OldValue:  hookStr("3"),
						NewValue:  hookStr("8"),
						ChangedAt: ts(t, "2026-09-10 10:00:00"),
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Backdate: %v", err)
	}
	// 3 rows by wildcard + 1 override = 4 row updates.
	if updated != 4 {
		t.Fatalf("updated rows = %d, want 4", updated)
	}

	wantBase := ts(t, "2026-09-07 09:30:00")
	if got := f.changedAt(t, "title", "", "Hook seed item"); !got.Equal(wantBase) {
		t.Fatalf("title changed_at = %v, want %v", got, wantBase)
	}
	if got := f.changedAt(t, "status_id", "", "1"); !got.Equal(wantBase) {
		t.Fatalf("creation status changed_at = %v, want %v", got, wantBase)
	}
	wantDone := ts(t, "2026-09-12 16:00:00")
	if got := f.changedAt(t, "status_id", "1", "2"); !got.Equal(wantDone) {
		t.Fatalf("transition changed_at = %v, want %v", got, wantDone)
	}
}

func TestHistoryHookBackdateShiftsItemCreatedAt(t *testing.T) {
	f := newHistoryHookFixture(t)

	wantCreated := ts(t, "2026-09-05 07:15:00")
	updated, err := f.service.Backdate(context.Background(), HistoryBackdateRequest{
		Items: []HistoryBackdateItem{
			{ItemID: f.itemID, CreatedAt: &wantCreated, Set: []HistoryBackdateEntry{
				{FieldName: "*", ChangedAt: ts(t, "2026-09-07 09:30:00")},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Backdate: %v", err)
	}
	if updated != 3 {
		t.Fatalf("updated rows = %d, want 3", updated)
	}
	if got := f.itemCreatedAt(t); !got.Equal(wantCreated) {
		t.Fatalf("created_at = %v, want %v", got, wantCreated)
	}
}

func TestHistoryHookBackdateRejectsUnknownItem(t *testing.T) {
	f := newHistoryHookFixture(t)
	wantCreated := ts(t, "2026-09-05 07:15:00")

	_, err := f.service.Backdate(context.Background(), HistoryBackdateRequest{
		Items: []HistoryBackdateItem{
			// created_at set: the zero-rows update must surface as an error.
			{ItemID: f.itemID + 424242, CreatedAt: &wantCreated},
		},
	})
	if err == nil {
		t.Fatalf("Backdate for unknown item: want error, got nil")
	}
}
