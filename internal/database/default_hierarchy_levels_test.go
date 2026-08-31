//go:build test

package database_test

import (
	"testing"

	"windshift/internal/testutils"
)

func TestDefaultHierarchyLevelFourIsActivity(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })

	var name, description string
	if err := tdb.QueryRow(`
		SELECT name, description
		FROM hierarchy_levels
		WHERE level = 4
	`).Scan(&name, &description); err != nil {
		t.Fatalf("load default level-4 hierarchy: %v", err)
	}
	if name != "Activity" {
		t.Fatalf("default level-4 hierarchy name = %q, want Activity", name)
	}
	if description != "Discrete activity within a task" {
		t.Fatalf("default level-4 hierarchy description = %q", description)
	}
}
