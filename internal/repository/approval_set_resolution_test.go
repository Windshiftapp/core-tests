//go:build test

package repository

import (
	"context"
	"testing"

	"windshift/internal/testutils"
)

// TestResolveForWorkspaceDelegatesToEffectiveConfig pins the approval-set
// delegation onto the canonical configuration resolution: item-type override
// on the effective config set, then the config-set-level approval. Personal
// and unknown workspaces resolve to nil.
func TestResolveForWorkspaceDelegatesToEffectiveConfig(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	db := tdb.GetDatabase()
	repo := NewApprovalSetRepository(db)

	insertID := func(query string, args ...interface{}) int {
		t.Helper()
		return testutils.InsertID(t, db, query, args...)
	}
	queryInt := func(query string, args ...interface{}) int {
		t.Helper()
		var id int
		if err := db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("query %q: %v", query, err)
		}
		return id
	}

	workflowID := insertID(`INSERT INTO workflows (name, description) VALUES ('Approval Resolution Workflow', '')`)
	approvalSet := func(name string) int {
		return insertID(`INSERT INTO approval_sets (name, workflow_id) VALUES (?, ?)`, name, workflowID)
	}
	defaultApproval := approvalSet("resolution default approval")
	assignedApproval := approvalSet("resolution assigned approval")
	overrideApproval := approvalSet("resolution override approval")

	// The seeded default config set ships without an approval set; attach one
	// so the default fallback has something to resolve.
	defaultSet := queryInt(`SELECT id FROM configuration_sets WHERE is_default = true ORDER BY id LIMIT 1`)
	if _, err := db.Exec(`UPDATE configuration_sets SET approval_set_id = ? WHERE id = ?`, defaultApproval, defaultSet); err != nil {
		t.Fatalf("attach approval to default config set: %v", err)
	}

	assignedSet := insertID(
		`INSERT INTO configuration_sets (name, description, is_default, approval_set_id) VALUES ('resolution assigned set', '', false, ?)`,
		assignedApproval)
	blankSet := insertID(
		`INSERT INTO configuration_sets (name, description, is_default) VALUES ('resolution blank set', '', false)`)

	overrideTypeID := insertID(`INSERT INTO item_types (name, hierarchy_level) VALUES ('Approval Override Probe Type', 3)`)
	insertID(
		`INSERT INTO configuration_set_item_types (configuration_set_id, item_type_id, approval_set_id) VALUES (?, ?, ?)`,
		assignedSet, overrideTypeID, overrideApproval)
	deferTypeID := insertID(`INSERT INTO item_types (name, hierarchy_level) VALUES ('Approval Deferral Probe Type', 3)`)
	insertID(
		`INSERT INTO configuration_set_item_types (configuration_set_id, item_type_id) VALUES (?, ?)`,
		assignedSet, deferTypeID)

	insertWorkspace := func(key string, isPersonal bool) int {
		return insertID(`INSERT INTO workspaces (key, name, is_personal) VALUES (?, ?, ?)`, key, key, isPersonal)
	}
	assigned := insertWorkspace("APPROVALASSIGN", false)
	insertID(`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, assigned, assignedSet)
	blank := insertWorkspace("APPROVALBLANK", false)
	insertID(`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, blank, blankSet)
	personal := insertWorkspace("APPROVALPERSONAL", true)

	resolve := func(workspaceID int, itemTypeID *int) *int {
		t.Helper()
		got, err := repo.ResolveForWorkspace(ctx, workspaceID, itemTypeID)
		if err != nil {
			t.Fatalf("ResolveForWorkspace: %v", err)
		}
		return got
	}

	t.Run("assigned config set approval wins", func(t *testing.T) {
		if got := resolve(assigned, nil); got == nil || *got != assignedApproval {
			t.Fatalf("ResolveForWorkspace = %v, want %d", got, assignedApproval)
		}
	})

	t.Run("unassigned workspace falls back to default config set approval", func(t *testing.T) {
		unassigned := insertWorkspace("APPROVALDEFAULT", false)
		if got := resolve(unassigned, nil); got == nil || *got != defaultApproval {
			t.Fatalf("ResolveForWorkspace = %v, want %d", got, defaultApproval)
		}
	})

	t.Run("item type override wins", func(t *testing.T) {
		if got := resolve(assigned, &overrideTypeID); got == nil || *got != overrideApproval {
			t.Fatalf("ResolveForWorkspace = %v, want %d", got, overrideApproval)
		}
	})

	t.Run("override without approval defers to config set level", func(t *testing.T) {
		if got := resolve(assigned, &deferTypeID); got == nil || *got != assignedApproval {
			t.Fatalf("ResolveForWorkspace = %v, want %d", got, assignedApproval)
		}
	})

	t.Run("assigned config set without approval returns nil", func(t *testing.T) {
		if got := resolve(blank, nil); got != nil {
			t.Fatalf("ResolveForWorkspace = %v, want nil", got)
		}
	})

	t.Run("personal workspace returns nil", func(t *testing.T) {
		if got := resolve(personal, nil); got != nil {
			t.Fatalf("ResolveForWorkspace = %v, want nil", got)
		}
	})

	t.Run("missing workspace returns nil", func(t *testing.T) {
		if got := resolve(999999999, nil); got != nil {
			t.Fatalf("ResolveForWorkspace = %v, want nil", got)
		}
	})
}
