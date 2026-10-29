//go:build test

package repository

import (
	"slices"
	"testing"

	"windshift/internal/testutils"
)

// ListForWorkspace must follow the same effective-configuration resolution as
// ResolveEffective: an assigned set wins, an unassigned workspace falls back to
// the global default set, and personal workspaces stay unrestricted. The board
// quick-add reads this catalog, so a regression here shows every item type.
func TestListForWorkspaceFollowsEffectiveConfiguration(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()
	repo := NewItemTypeRepository(db)

	insertID := func(query string, args ...any) int {
		t.Helper()
		return testutils.InsertID(t, db, query, args...)
	}
	queryInt := func(query string, args ...any) int {
		t.Helper()
		var id int
		if err := db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("query %q: %v", query, err)
		}
		return id
	}
	namesFor := func(workspaceID int) []string {
		t.Helper()
		rows, err := repo.ListForWorkspace(workspaceID)
		if err != nil {
			t.Fatalf("ListForWorkspace(%d): %v", workspaceID, err)
		}
		names := make([]string, 0, len(rows))
		for _, row := range rows {
			names = append(names, row.Name)
		}
		return names
	}
	insertWorkspace := func(key string, isPersonal bool) int {
		t.Helper()
		return insertID(`INSERT INTO workspaces (key, name, is_personal) VALUES (?, ?, ?)`, key, key, isPersonal)
	}

	defaultSet := queryInt(`SELECT id FROM configuration_sets WHERE is_default = true ORDER BY id LIMIT 1`)

	t.Run("unassigned workspace falls back to the default config set", func(t *testing.T) {
		workspaceID := insertWorkspace("CATALOG-UNASSIGNED", false)

		want := []string{"Epic", "Story", "Task", "Bug", "Sub-task"}
		got := namesFor(workspaceID)
		if len(got) != len(want) {
			t.Fatalf("item types = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("item types = %v, want %v", got, want)
			}
		}
		if slices.Contains(got, "Initiative") {
			t.Fatalf("unassigned workspace exposed %q outside the default set: %v", "Initiative", got)
		}
	})

	t.Run("assigned config set restricts the catalog", func(t *testing.T) {
		workspaceID := insertWorkspace("CATALOG-ASSIGNED", false)
		customType := insertID(`INSERT INTO item_types (name, hierarchy_level) VALUES ('Catalog Probe Type', 3)`)
		assignedSet := insertID(`INSERT INTO configuration_sets (name, is_default) VALUES ('Catalog Probe Set', false)`)
		insertID(`INSERT INTO configuration_set_item_types (configuration_set_id, item_type_id) VALUES (?, ?)`, assignedSet, customType)
		insertID(`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, workspaceID, assignedSet)

		got := namesFor(workspaceID)
		want := []string{"Catalog Probe Type"}
		if len(got) != len(want) || got[0] != want[0] {
			t.Fatalf("item types = %v, want %v", got, want)
		}
	})

	t.Run("effective set with no item types exposes the full catalog", func(t *testing.T) {
		workspaceID := insertWorkspace("CATALOG-EMPTY", false)
		emptySet := insertID(`INSERT INTO configuration_sets (name, is_default) VALUES ('Catalog Empty Set', false)`)
		insertID(`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, workspaceID, emptySet)

		got := namesFor(workspaceID)
		if !slices.Contains(got, "Initiative") || !slices.Contains(got, "Task") {
			t.Fatalf("empty effective set should expose the full catalog, got %v", got)
		}
	})

	t.Run("personal workspace exposes the full catalog", func(t *testing.T) {
		workspaceID := insertWorkspace("CATALOG-PERSONAL", true)
		insertID(`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, workspaceID, defaultSet)

		got := namesFor(workspaceID)
		if !slices.Contains(got, "Initiative") || !slices.Contains(got, "Task") {
			t.Fatalf("personal workspace should expose the full catalog, got %v", got)
		}
	})

	t.Run("unknown workspace exposes the full catalog", func(t *testing.T) {
		got := namesFor(999999999)
		if !slices.Contains(got, "Initiative") || !slices.Contains(got, "Task") {
			t.Fatalf("unknown workspace should expose the full catalog, got %v", got)
		}
	})
}
