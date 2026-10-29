//go:build test

package services

import (
	"testing"

	"windshift/internal/testutils"
)

// TestGetConditionSetIDForItemFollowsEffectiveConfig pins the condition-set
// lookup onto the canonical configuration resolution: item-type override on
// the effective config set, then the config-set-level condition set.
// Personal and unknown workspaces resolve to nil.
func TestGetConditionSetIDForItemFollowsEffectiveConfig(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()
	service := NewConditionService(db, nil, nil)

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

	workflowID := insertID(`INSERT INTO workflows (name, description) VALUES ('Condition Resolution Workflow', '')`)
	conditionSet := func(name string) int {
		return insertID(`INSERT INTO condition_sets (name, workflow_id) VALUES (?, ?)`, name, workflowID)
	}
	defaultCondition := conditionSet("resolution default condition")
	assignedCondition := conditionSet("resolution assigned condition")
	overrideCondition := conditionSet("resolution override condition")

	// The seeded default config set ships without a condition set; attach one
	// so the default fallback has something to resolve.
	defaultSet := queryInt(`SELECT id FROM configuration_sets WHERE is_default = true ORDER BY id LIMIT 1`)
	if _, err := db.Exec(`UPDATE configuration_sets SET condition_set_id = ? WHERE id = ?`, defaultCondition, defaultSet); err != nil {
		t.Fatalf("attach condition set to default config set: %v", err)
	}

	assignedSet := insertID(
		`INSERT INTO configuration_sets (name, description, is_default, condition_set_id) VALUES ('condition assigned set', '', false, ?)`,
		assignedCondition)
	blankSet := insertID(
		`INSERT INTO configuration_sets (name, description, is_default) VALUES ('condition blank set', '', false)`)

	overrideTypeID := insertID(`INSERT INTO item_types (name, hierarchy_level) VALUES ('Condition Override Probe Type', 3)`)
	insertID(
		`INSERT INTO configuration_set_item_types (configuration_set_id, item_type_id, condition_set_id) VALUES (?, ?, ?)`,
		assignedSet, overrideTypeID, overrideCondition)

	insertWorkspace := func(key string, isPersonal bool) int {
		return insertID(`INSERT INTO workspaces (key, name, is_personal) VALUES (?, ?, ?)`, key, key, isPersonal)
	}
	assigned := insertWorkspace("CONDITIONASSIGN", false)
	insertID(`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, assigned, assignedSet)
	blank := insertWorkspace("CONDITIONBLANK", false)
	insertID(`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, blank, blankSet)
	personal := insertWorkspace("CONDITIONPERSONAL", true)

	resolve := func(workspaceID int, itemTypeID *int) *int {
		t.Helper()
		got, err := service.GetConditionSetIDForItem(workspaceID, itemTypeID)
		if err != nil {
			t.Fatalf("GetConditionSetIDForItem: %v", err)
		}
		return got
	}

	t.Run("assigned config set condition wins", func(t *testing.T) {
		if got := resolve(assigned, nil); got == nil || *got != assignedCondition {
			t.Fatalf("GetConditionSetIDForItem = %v, want %d", got, assignedCondition)
		}
	})

	t.Run("unassigned workspace falls back to default config set condition", func(t *testing.T) {
		unassigned := insertWorkspace("CONDITIONDEFAULT", false)
		if got := resolve(unassigned, nil); got == nil || *got != defaultCondition {
			t.Fatalf("GetConditionSetIDForItem = %v, want %d", got, defaultCondition)
		}
	})

	t.Run("item type override wins", func(t *testing.T) {
		if got := resolve(assigned, &overrideTypeID); got == nil || *got != overrideCondition {
			t.Fatalf("GetConditionSetIDForItem = %v, want %d", got, overrideCondition)
		}
	})

	t.Run("assigned config set without condition set returns nil", func(t *testing.T) {
		if got := resolve(blank, nil); got != nil {
			t.Fatalf("GetConditionSetIDForItem = %v, want nil", got)
		}
	})

	t.Run("personal workspace returns nil", func(t *testing.T) {
		if got := resolve(personal, nil); got != nil {
			t.Fatalf("GetConditionSetIDForItem = %v, want nil", got)
		}
	})

	t.Run("missing workspace returns nil", func(t *testing.T) {
		if got := resolve(999999999, nil); got != nil {
			t.Fatalf("GetConditionSetIDForItem = %v, want nil", got)
		}
	})
}
