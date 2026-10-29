//go:build test

package database_test

import (
	"strings"
	"testing"

	"windshift/internal/testutils"
)

// TestNotificationDigestIndex verifies the partial index that supports the
// digest recipient scan (WI-1768) is created by a fresh schema init on both
// engines and keeps the shape the recipient query needs: grouped by user over
// the active delivery states.
func TestNotificationDigestIndex(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()

	if !indexExists(t, db, "idx_notifications_email_digest_pending") {
		t.Fatal("idx_notifications_email_digest_pending is missing after schema init")
	}

	var definition string
	if db.GetDriverName() == "postgres" {
		if err := db.QueryRow(
			`SELECT indexdef FROM pg_indexes WHERE indexname = 'idx_notifications_email_digest_pending'`,
		).Scan(&definition); err != nil {
			t.Fatalf("read index definition: %v", err)
		}
	} else {
		if err := db.QueryRow(
			`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'idx_notifications_email_digest_pending'`,
		).Scan(&definition); err != nil {
			t.Fatalf("read index definition: %v", err)
		}
	}
	normalized := strings.ToLower(definition)
	for _, want := range []string{"user_id", "timestamp", "sent_at is null", "read = false", "email_delivery_state"} {
		if !strings.Contains(normalized, want) {
			t.Fatalf("index definition missing %q: %s", want, definition)
		}
	}
}
