//go:build test

package database_test

import (
	"testing"
	"time"

	"windshift/internal/testutils"
)

// TestMigration_EmailTrackingCompletedBackfill_StampsLegacyRows covers the
// production upgrade path: databases that already had email_message_tracking
// rows when completed_at was introduced have no tombstone on those rows, so a
// deleted ticket would be recreated by a mailbox refetch. The backfill must
// stamp every completed row (item/comment reference or long-lived orphan) while
// leaving live claims and rate-limited rows requeueable.
func TestMigration_EmailTrackingCompletedBackfill_StampsLegacyRows(t *testing.T) {
	tdb := testutils.CreateFreshDB(t, false)
	seedCanonicalSchemaCheckpoint(t, tdb)
	seedMinimalWorkspaces(t, tdb)

	createSchemaMigrationsTable(t, tdb)
	stampAllExcept(t, tdb, "20261020_email_tracking_completed_backfill", "")

	timestampType := "DATETIME"
	if testutils.IsPostgres() {
		timestampType = "TIMESTAMPTZ"
	}
	if _, err := tdb.Exec(`CREATE TABLE email_message_tracking (
		id INTEGER PRIMARY KEY,
		item_id INTEGER,
		comment_id INTEGER,
		rate_limited_at ` + timestampType + `,
		completed_at ` + timestampType + `,
		processed_at ` + timestampType + ` NOT NULL
	)`); err != nil {
		t.Fatalf("seed email_message_tracking: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	rows := []struct {
		id           int
		itemID       any
		commentID    any
		rateLimited  any
		completedAt  any
		processedAt  time.Time
		wantComplete bool
	}{
		// Still references a live ticket: definitely completed.
		{id: 1, itemID: 10, processedAt: now.Add(-2 * time.Hour), wantComplete: true},
		// Reply comment still exists: definitely completed.
		{id: 2, commentID: 20, processedAt: now.Add(-2 * time.Hour), wantComplete: true},
		// Old orphan: a deleted-ticket tombstone, not a live claim.
		{id: 3, processedAt: now.Add(-2 * time.Hour), wantComplete: true},
		// Fresh orphan: could still be an in-flight claim, leave it alone.
		{id: 4, processedAt: now, wantComplete: false},
		// Rate-limited mail stays requeueable.
		{id: 5, rateLimited: now.Add(-2 * time.Hour), processedAt: now.Add(-2 * time.Hour), wantComplete: false},
		// Already stamped rows are never rewritten.
		{id: 6, completedAt: now.Add(-30 * time.Minute), processedAt: now.Add(-4 * time.Hour), wantComplete: true},
	}
	for _, row := range rows {
		if _, err := tdb.Exec(`
			INSERT INTO email_message_tracking
				(id, item_id, comment_id, rate_limited_at, completed_at, processed_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, row.id, row.itemID, row.commentID, row.rateLimited, row.completedAt, row.processedAt); err != nil {
			t.Fatalf("seed tracking row %d: %v", row.id, err)
		}
	}

	if err := tdb.Initialize(); err != nil {
		t.Fatalf("run completed_at backfill migration: %v", err)
	}

	for _, row := range rows {
		var completedAt *time.Time
		if err := tdb.QueryRow(
			`SELECT completed_at FROM email_message_tracking WHERE id = ?`, row.id,
		).Scan(&completedAt); err != nil {
			t.Fatalf("load tracking row %d: %v", row.id, err)
		}
		if row.wantComplete && completedAt == nil {
			t.Errorf("row %d completed_at = NULL, want a tombstone", row.id)
		}
		if !row.wantComplete && completedAt != nil {
			t.Errorf("row %d completed_at = %v, want NULL", row.id, completedAt)
		}
	}

	// The already-stamped row keeps its original marker instead of being
	// rewritten to processed_at.
	var completedAt time.Time
	if err := tdb.QueryRow(`SELECT completed_at FROM email_message_tracking WHERE id = 6`).Scan(&completedAt); err != nil {
		t.Fatalf("load already-stamped row: %v", err)
	}
	if !completedAt.After(now.Add(-time.Hour)) {
		t.Fatalf("row 6 completed_at = %v, want the original marker near %v", completedAt, now.Add(-30*time.Minute))
	}

	// Re-running Initialize must not touch the now-stamped rows again.
	if err := tdb.Initialize(); err != nil {
		t.Fatalf("repeat initialize: %v", err)
	}
}
