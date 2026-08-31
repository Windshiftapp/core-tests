package services

import (
	"testing"

	"windshift/internal/testutils"
)

// TestWorkspaceService_GetPriorities verifies that workspace priority listing
// is scoped to the workspace's configuration set, and falls back to all
// priorities when the workspace has no configuration set — mirroring the
// behaviour of GetItemTypes/GetStatuses.
func TestWorkspaceService_GetPriorities(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	if tdb.Engine == "sqlite" {
		t.Cleanup(func() { _ = tdb.Close() })
	}
	db := tdb.DB
	svc := NewWorkspaceService(db)

	// Baseline: total priorities seeded by schema initialization.
	var totalPriorities int
	if err := db.QueryRow(`SELECT COUNT(*) FROM priorities`).Scan(&totalPriorities); err != nil {
		t.Fatalf("count priorities: %v", err)
	}
	if totalPriorities < 3 {
		t.Fatalf("expected >=3 seeded priorities, got %d", totalPriorities)
	}

	t.Run("NoConfigurationSet_ReturnsAll", func(t *testing.T) {
		const wsID = 9101
		if _, err := db.Exec(`INSERT INTO workspaces (id, name, key, active) VALUES (?, 'No Config', 'NOCFG', true)`, wsID); err != nil {
			t.Fatalf("insert workspace: %v", err)
		}

		got, err := svc.GetPriorities(wsID)
		if err != nil {
			t.Fatalf("GetPriorities: %v", err)
		}
		if len(got) != totalPriorities {
			t.Errorf("workspace with no config set: expected all %d priorities, got %d", totalPriorities, len(got))
		}
	})

	t.Run("WithConfigurationSet_ReturnsScopedSubset", func(t *testing.T) {
		const wsID = 9102
		if _, err := db.Exec(`INSERT INTO workspaces (id, name, key, active) VALUES (?, 'Scoped', 'SCOPED', true)`, wsID); err != nil {
			t.Fatalf("insert workspace: %v", err)
		}

		csID := testutils.InsertID(t, db, `INSERT INTO configuration_sets (name) VALUES ('Scoped Set')`)

		if _, err := db.Exec(`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, wsID, csID); err != nil {
			t.Fatalf("link workspace to config set: %v", err)
		}

		// Enable only the two lowest-sort priorities for this config set.
		rows, err := db.Query(`SELECT id, name FROM priorities ORDER BY sort_order, name LIMIT 2`)
		if err != nil {
			t.Fatalf("query priorities: %v", err)
		}
		enabled := map[int]string{}
		for rows.Next() {
			var id int
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				rows.Close()
				t.Fatalf("scan priority: %v", err)
			}
			enabled[id] = name
		}
		rows.Close()
		if len(enabled) != 2 {
			t.Fatalf("expected to select 2 priorities to enable, got %d", len(enabled))
		}
		for id := range enabled {
			if _, err := db.Exec(`INSERT INTO configuration_set_priorities (configuration_set_id, priority_id) VALUES (?, ?)`, csID, id); err != nil {
				t.Fatalf("map priority %d to config set: %v", id, err)
			}
		}

		got, err := svc.GetPriorities(wsID)
		if err != nil {
			t.Fatalf("GetPriorities: %v", err)
		}
		if len(got) != len(enabled) {
			t.Fatalf("expected %d scoped priorities, got %d", len(enabled), len(got))
		}
		for _, p := range got {
			if _, ok := enabled[p.ID]; !ok {
				t.Errorf("priority %d (%s) is not enabled for the workspace's config set but was returned", p.ID, p.Name)
			}
		}
		if len(got) >= totalPriorities {
			t.Errorf("scoped priorities (%d) should be fewer than the global catalog (%d)", len(got), totalPriorities)
		}
	})
}
