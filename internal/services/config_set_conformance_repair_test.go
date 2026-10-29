//go:build test

package services

import (
	"context"
	"testing"

	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// TestReferencedWorkflowIDsByNameScopesToAddressedSet verifies WI-1561's other
// half: repair resolves workflows through the addressed configuration set's
// references, not a global name lookup, so a same-named workflow owned by
// another set is never modified.
func TestReferencedWorkflowIDsByNameScopesToAddressedSet(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()

	insertWorkflow := func(name string) int {
		var id int
		if err := db.QueryRow(`INSERT INTO workflows (name) VALUES (?) RETURNING id`, name).Scan(&id); err != nil {
			t.Fatalf("insert workflow %q: %v", name, err)
		}
		return id
	}
	insertSet := func(name string, workflowID *int) int {
		var id int
		if err := db.QueryRow(`INSERT INTO configuration_sets (name, workflow_id) VALUES (?, ?) RETURNING id`, name, workflowID).Scan(&id); err != nil {
			t.Fatalf("insert configuration set %q: %v", name, err)
		}
		return id
	}

	workflowA := insertWorkflow("Shared Workflow")
	workflowB := insertWorkflow("Shared Workflow")
	setA := insertSet("Scoped Set A", &workflowA)
	setB := insertSet("Scoped Set B", &workflowB)

	// An item-type overlay workflow is also a reference of the set.
	overlay := insertWorkflow("Overlay Workflow")
	var itemTypeID int
	if err := db.QueryRow(`SELECT id FROM item_types ORDER BY id LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("load item type: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO configuration_set_item_types (configuration_set_id, item_type_id, workflow_id) VALUES (?, ?, ?)
	`, setA, itemTypeID, overlay); err != nil {
		t.Fatalf("insert item-type config: %v", err)
	}

	svc := NewConfigSetConformanceService(db, repository.NewConfigurationSetRepository(db))
	got, err := svc.referencedWorkflowIDsByName(context.Background(), setA)
	if err != nil {
		t.Fatalf("referencedWorkflowIDsByName: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("referenced workflows = %v, want the set's primary and overlay", got)
	}
	if got[lowerStr("Shared Workflow")] != workflowA {
		t.Fatalf("shared workflow resolved to %d, want set A's %d", got[lowerStr("Shared Workflow")], workflowA)
	}
	if got[lowerStr("Overlay Workflow")] != overlay {
		t.Fatalf("overlay workflow = %d, want %d", got[lowerStr("Overlay Workflow")], overlay)
	}
	if _, ok := got[lowerStr("Scoped Set B")]; ok {
		t.Fatal("set A resolution leaked set B's workflow")
	}

	// The other set resolves to its own same-named workflow.
	other, err := svc.referencedWorkflowIDsByName(context.Background(), setB)
	if err != nil {
		t.Fatalf("referencedWorkflowIDsByName(set B): %v", err)
	}
	if other[lowerStr("Shared Workflow")] != workflowB {
		t.Fatalf("set B shared workflow = %d, want %d", other[lowerStr("Shared Workflow")], workflowB)
	}
}
