//go:build test

package services

import (
	"context"
	"testing"

	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// TestAttachConfigurationSetIsAtomic verifies WI-1534: the attach pair
// (delete the old assignment, insert the new one) runs in one transaction, so
// a failed insert leaves the workspace's previous configuration-set
// assignment intact instead of falling back to the default set.
func TestAttachConfigurationSetIsAtomic(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	insertSet := func(name string) int {
		var id int
		if err := db.QueryRow(`INSERT INTO configuration_sets (name) VALUES (?) RETURNING id`, name).Scan(&id); err != nil {
			t.Fatalf("insert configuration set %q: %v", name, err)
		}
		return id
	}
	firstSet := insertSet("Atomic Set A")
	secondSet := insertSet("Atomic Set B")

	repo := repository.NewConfigurationSetRepository(db)
	if err := repo.AssignWorkspace(data.WorkspaceID, &firstSet); err != nil {
		t.Fatalf("assign first set: %v", err)
	}

	// A non-existent configuration set violates the foreign key, failing the
	// insert after the delete.
	if err := attachConfigurationSet(context.Background(), db, data.WorkspaceID, 999_999); err == nil {
		t.Fatal("attach with an unknown configuration set succeeded, want an error")
	}

	var assigned int
	if err := db.QueryRow(`
		SELECT configuration_set_id FROM workspace_configuration_sets WHERE workspace_id = ?
	`, data.WorkspaceID).Scan(&assigned); err != nil {
		t.Fatalf("read assignment: %v", err)
	}
	if assigned != firstSet {
		t.Fatalf("assignment = %d, want the previous set %d to survive the failed attach", assigned, firstSet)
	}

	// A valid attach still replaces the assignment.
	if err := attachConfigurationSet(context.Background(), db, data.WorkspaceID, secondSet); err != nil {
		t.Fatalf("attach second set: %v", err)
	}
	if err := db.QueryRow(`
		SELECT configuration_set_id FROM workspace_configuration_sets WHERE workspace_id = ?
	`, data.WorkspaceID).Scan(&assigned); err != nil {
		t.Fatalf("read assignment after valid attach: %v", err)
	}
	if assigned != secondSet {
		t.Fatalf("assignment = %d, want %d", assigned, secondSet)
	}
}
