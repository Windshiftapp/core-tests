//go:build test

package database_test

import (
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

func TestValidateCanonicalSchemaCheckpointRefusesMalformedRank(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	var workspaceID int
	if err := tdb.QueryRow(`
		INSERT INTO workspaces (name, key, active, is_personal)
		VALUES ('Malformed checkpoint rank', 'MCR', true, false)
		RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	// Direct SQL creates corruption that the production API intentionally rejects.
	if _, err := tdb.Exec(`
		INSERT INTO items (workspace_id, workspace_item_number, title, frac_index)
		VALUES (?, 1, 'malformed rank', 'legacy-rank')`, workspaceID); err != nil {
		t.Fatalf("insert malformed rank fixture: %v", err)
	}

	err := database.ValidateCanonicalSchemaCheckpoint(tdb.DB)
	want := "database schema checkpoint 0.8.5 is incomplete: canonical item ranks are invalid or duplicated"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("validate malformed rank error = %v, want %q", err, want)
	}
}
