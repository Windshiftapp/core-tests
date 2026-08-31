//go:build test

package services_test

import (
	"testing"

	"windshift/internal/database"
	"windshift/internal/services"
)

// TestCommentService_Create_BumpsLastActiveAt verifies that posting a comment
// floats the item to the top of the board's Bubble Mode sort by advancing
// last_active_at, while leaving updated_at ("last edited") untouched.
func TestCommentService_Create_BumpsLastActiveAt(t *testing.T) {
	db := createCommentTestDB(t)
	service := services.NewCommentService(db)
	env := setupCommentTestEnv(t, db)

	// Stamp a fixed past time on both columns, then read it back as the baseline
	// (the SQLite driver re-serializes datetimes on read, so we compare against
	// the stored value rather than the literal we wrote).
	const sentinel = "2000-01-01 00:00:00"
	if _, err := db.Exec(
		"UPDATE items SET last_active_at = ?, updated_at = ? WHERE id = ?",
		sentinel, sentinel, env.ItemID,
	); err != nil {
		t.Fatalf("failed to stamp sentinel: %v", err)
	}
	baseActive, baseUpdated := readActivityTimes(t, db, env.ItemID)

	_, err := service.Create(services.CreateCommentParams{
		ItemID:      env.ItemID,
		AuthorID:    env.UserID,
		Content:     "Bumping this item",
		ActorUserID: env.UserID,
	})
	if err != nil {
		t.Fatalf("comment Create failed: %v", err)
	}

	lastActive, updated := readActivityTimes(t, db, env.ItemID)
	if lastActive == baseActive {
		t.Error("expected last_active_at to be bumped on comment, but it was unchanged")
	}
	if updated != baseUpdated {
		t.Errorf("expected updated_at to be unchanged on comment (%q), got %q", baseUpdated, updated)
	}
}

func readActivityTimes(t *testing.T, db database.Database, itemID int) (string, string) {
	t.Helper()
	var lastActive, updated string
	if err := db.QueryRow(
		"SELECT last_active_at, updated_at FROM items WHERE id = ?", itemID,
	).Scan(&lastActive, &updated); err != nil {
		t.Fatalf("failed to read activity times: %v", err)
	}
	return lastActive, updated
}
