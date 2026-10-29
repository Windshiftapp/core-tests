//go:build test

package database_test

import (
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// TestHelpdeskLookupIndexesExist verifies the access paths added for the
// 0.9.0 performance findings: ticket-import mappings must be seekable by job
// and item (WI-1596), and inbound email tracking by sender and by
// channel/normalized-sender/time (WI-1597). A fresh schema init must create
// them on both engines.
func TestHelpdeskLookupIndexesExist(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()

	indexes := []string{
		"idx_item_import_rows_job_id",
		"idx_item_import_rows_item_id",
		"idx_email_message_tracking_sender",
		"idx_email_message_tracking_channel_sender_time",
	}
	for _, name := range indexes {
		if !indexExists(t, db, name) {
			t.Fatalf("index %s is missing after schema init", name)
		}
	}
}

func indexExists(t *testing.T, db database.Database, name string) bool {
	t.Helper()
	query := `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`
	if db.GetDriverName() == "postgres" {
		query = `SELECT COUNT(*) FROM pg_indexes WHERE indexname = ?`
	}
	var count int
	if err := db.QueryRow(query, name).Scan(&count); err != nil {
		t.Fatalf("query index %s: %v", name, err)
	}
	return count > 0
}
