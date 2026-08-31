//go:build test

package database_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

func TestDatabase_Initialize_FreshDatabase(t *testing.T) {
	// Create fresh database without initialization
	tdb := testutils.CreateFreshDB(t, true)
	defer tdb.Close()

	// Initialize the database
	if err := tdb.Initialize(); err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}

	// Verify all core tables exist
	coreTable := []string{
		"workspaces", "items", "users", "workflows", "system_settings",
		"status_categories", "statuses", "screens", "configuration_sets",
	}

	for _, table := range coreTable {
		tdb.AssertTableExists(t, table)
	}

	// Verify foreign key constraints are enabled
	tdb.AssertForeignKeyEnabled(t)

	// Verify core indexes exist
	coreIndexes := []string{
		"idx_items_workspace_id",
		"idx_users_email",
		"idx_system_settings_key",
		"idx_statuses_category_id",
	}

	for _, index := range coreIndexes {
		tdb.AssertIndexExists(t, index)
	}
}

func TestDatabase_Initialize_ExistingDatabase(t *testing.T) {
	// Create and initialize database
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// Get initial table count
	initialCount, err := tdb.GetTableCount()
	if err != nil {
		t.Fatalf("Failed to get initial table count: %v", err)
	}

	// Initialize again (should not recreate tables)
	if err = tdb.Initialize(); err != nil {
		t.Fatalf("Failed to reinitialize database: %v", err)
	}

	// Verify table count is unchanged
	finalCount, err := tdb.GetTableCount()
	if err != nil {
		t.Fatalf("Failed to get final table count: %v", err)
	}

	if finalCount != initialCount {
		t.Errorf("Table count changed from %d to %d after reinitialization", initialCount, finalCount)
	}
}

// TestSchema_CatalogIsUpgradeOnly asserts that schema/*.sql is canonical for
// fresh installs: every catalog migration must be a no-op on a brand-new DB.
// "No-op" means one of:
//
//  1. The migration's body is empty on this backend.
//  2. The migration's Check predicate returns >0, so the runner stamps
//     without executing the body (the effect is already in schema/).
//
// A migration whose body would actually run on a fresh install is drift:
// either schema/*.sql is missing what the migration creates, or the
// migration's Check predicate is wrong. Both are bugs and must be fixed
// before merge — fresh installs MUST come out of schema/*.sql alone, with
// the catalog reserved for upgrading existing DBs.
//
// Add a new table/column/index? Update the matching schema file in the same
// commit as the migration so this test stays green.
func TestSchema_CatalogIsUpgradeOnly(t *testing.T) {
	// Temporarily empty the catalog so the assertions inspect only the base
	// schema on both engines.
	catalog := database.Catalog
	database.Catalog = nil
	t.Cleanup(func() { database.Catalog = catalog })
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	database.Catalog = catalog

	var drift []string
	for _, m := range database.Catalog {
		body := m.SQLite
		check := m.CheckSQLite
		checkName := "CheckSQLite"
		if tdb.Engine == "postgres" {
			body = m.Postgres
			check = m.CheckPostgres
			checkName = "CheckPostgres"
		}
		if body == "" {
			continue // intentional no-op on this backend
		}
		if check == "" {
			drift = append(drift, fmt.Sprintf(
				"  %-50s  %s\n      no %s predicate; body always runs on a fresh %s DB. "+
					"Add a Check that returns >0 once the schema files cover the effect, "+
					"or convert the body to an empty no-op if the effect is now in schema/*.sql.",
				m.Version, m.Name, checkName, tdb.Engine))
			continue
		}
		var count int
		if err := tdb.QueryRow(check).Scan(&count); err != nil {
			t.Errorf("%s: failed to run %s predicate: %v", m.Version, checkName, err)
			continue
		}
		if count == 0 {
			drift = append(drift, fmt.Sprintf(
				"  %-50s  %s\n      %s returns 0 on a fresh %s schema-init DB. "+
					"Fold this migration's effect into schema/*.sql so fresh installs "+
					"see it without needing the catalog.",
				m.Version, m.Name, checkName, tdb.Engine))
		}
	}

	if len(drift) > 0 {
		sort.Strings(drift)
		t.Fatalf("schema drift detected: %d catalog migration(s) would run on a fresh install.\n"+
			"Fresh installs MUST be served entirely by schema/*.sql; the catalog is upgrade-only.\n\n"+
			"%s\n",
			len(drift), strings.Join(drift, "\n"))
	}
}

func TestDatabase_NewDB_ConnectionString(t *testing.T) {
	tests := []struct {
		name       string
		dsn        string
		shouldFail bool
	}{
		{
			name:       "Memory database",
			dsn:        ":memory:",
			shouldFail: false,
		},
		{
			name:       "File database with WAL mode",
			dsn:        "test.db?_journal=WAL",
			shouldFail: false,
		},
		{
			name:       "Invalid file path",
			dsn:        "/nonexistent/path/test.db",
			shouldFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := database.NewDB(tt.dsn, 120, 1)

			if tt.shouldFail {
				if err == nil {
					db.Close()
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			defer db.Close()

			// Verify foreign keys are enabled
			var fkEnabled int
			if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fkEnabled); err != nil {
				t.Fatalf("Failed to check foreign key status: %v", err)
			}
			if fkEnabled == 0 {
				t.Error("Foreign key constraints not enabled")
			}

			// If not memory database, verify WAL mode
			if tt.dsn != ":memory:" {
				var journalMode string
				if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
					t.Fatalf("Failed to check journal mode: %v", err)
				}
				if journalMode != "wal" {
					t.Errorf("Expected WAL mode, got %s", journalMode)
				}
			}
		})
	}
}

func TestDatabase_DefaultData_SystemSettings(t *testing.T) {
	// Create and initialize database
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// Verify system settings were created
	expectedSettings := map[string]struct {
		value     string
		valueType string
		category  string
	}{
		"setup_completed":         {"false", "boolean", "setup"},
		"admin_user_created":      {"false", "boolean", "setup"},
		"time_tracking_enabled":   {"true", "boolean", "modules"},
		"test_management_enabled": {"true", "boolean", "modules"},
		"calendar_feed_enabled":   {"true", "boolean", "security"},
	}

	for key, expected := range expectedSettings {
		var value, valueType, category string
		err := tdb.QueryRow(`
			SELECT value, value_type, category
			FROM system_settings
			WHERE key = ?
		`, key).Scan(&value, &valueType, &category)

		if err != nil {
			t.Fatalf("Failed to query system setting %s: %v", key, err)
		}

		if value != expected.value {
			t.Errorf("Setting %s: expected value %s, got %s", key, expected.value, value)
		}
		if valueType != expected.valueType {
			t.Errorf("Setting %s: expected type %s, got %s", key, expected.valueType, valueType)
		}
		if category != expected.category {
			t.Errorf("Setting %s: expected category %s, got %s", key, expected.category, category)
		}
	}
}

func TestDatabase_DefaultData_StatusSystem(t *testing.T) {
	// Create and initialize database
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// Verify status categories were created
	var categoryCount int
	err := tdb.QueryRow("SELECT COUNT(*) FROM status_categories").Scan(&categoryCount)
	if err != nil {
		t.Fatalf("Failed to count status categories: %v", err)
	}
	if categoryCount < 3 {
		t.Errorf("Expected at least 3 status categories, got %d", categoryCount)
	}

	// Verify statuses were created
	var statusCount int
	err = tdb.QueryRow("SELECT COUNT(*) FROM statuses").Scan(&statusCount)
	if err != nil {
		t.Fatalf("Failed to count statuses: %v", err)
	}
	if statusCount < 3 {
		t.Errorf("Expected at least 3 statuses, got %d", statusCount)
	}

	// Verify default workflow exists
	var workflowCount int
	err = tdb.QueryRow("SELECT COUNT(*) FROM workflows WHERE is_default = true").Scan(&workflowCount)
	if err != nil {
		t.Fatalf("Failed to count default workflows: %v", err)
	}
	if workflowCount != 1 {
		t.Errorf("Expected 1 default workflow, got %d", workflowCount)
	}

	// Verify workflow transitions exist
	var transitionCount int
	err = tdb.QueryRow("SELECT COUNT(*) FROM workflow_transitions").Scan(&transitionCount)
	if err != nil {
		t.Fatalf("Failed to count workflow transitions: %v", err)
	}
	if transitionCount < 4 {
		t.Errorf("Expected at least 4 workflow transitions, got %d", transitionCount)
	}
}

func TestDatabase_DefaultData_ScreenSystem(t *testing.T) {
	// Create and initialize database
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// Verify default screen exists
	var screenCount int
	err := tdb.QueryRow("SELECT COUNT(*) FROM screens").Scan(&screenCount)
	if err != nil {
		t.Fatalf("Failed to count screens: %v", err)
	}
	if screenCount < 1 {
		t.Error("Expected at least 1 default screen")
	}

	// Verify screen has fields
	var fieldCount int
	err = tdb.QueryRow("SELECT COUNT(*) FROM screen_fields").Scan(&fieldCount)
	if err != nil {
		t.Fatalf("Failed to count screen fields: %v", err)
	}
	if fieldCount < 3 {
		t.Errorf("Expected at least 3 screen fields, got %d", fieldCount)
	}

	// Verify configuration set exists
	var configSetCount int
	err = tdb.QueryRow("SELECT COUNT(*) FROM configuration_sets WHERE is_default = true").Scan(&configSetCount)
	if err != nil {
		t.Fatalf("Failed to count configuration sets: %v", err)
	}
	if configSetCount != 1 {
		t.Errorf("Expected 1 default configuration set, got %d", configSetCount)
	}
}

func TestDatabase_DefaultData_LinkTypes(t *testing.T) {
	// Create and initialize database
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// Verify link types were created
	var linkTypeCount int
	err := tdb.QueryRow("SELECT COUNT(*) FROM link_types WHERE is_system = true").Scan(&linkTypeCount)
	if err != nil {
		t.Fatalf("Failed to count system link types: %v", err)
	}
	if linkTypeCount < 5 {
		t.Errorf("Expected at least 5 system link types, got %d", linkTypeCount)
	}

	// Verify specific link types exist
	expectedLinkTypes := []string{"Tests", "Implements", "Depends On", "Relates To", "Links To"}
	for _, linkTypeName := range expectedLinkTypes {
		var exists bool
		err := tdb.QueryRow("SELECT EXISTS(SELECT 1 FROM link_types WHERE name = ?)", linkTypeName).Scan(&exists)
		if err != nil {
			t.Fatalf("Failed to check link type %s: %v", linkTypeName, err)
		}
		if !exists {
			t.Errorf("Expected link type '%s' to exist", linkTypeName)
		}
	}
}

func TestDatabase_SchemaColumns(t *testing.T) {
	// Create fresh database
	tdb := testutils.CreateFreshDB(t, true)
	defer tdb.Close()

	// Initialize database
	if err := tdb.Initialize(); err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}

	// Verify expected columns exist in the schema
	tdb.AssertColumnExists(t, "items", "rank")
	tdb.AssertColumnExists(t, "items", "assignee_id")
	tdb.AssertColumnExists(t, "items", "creator_id")
	tdb.AssertColumnExists(t, "workspaces", "time_project_id")
	tdb.AssertColumnExists(t, "time_worklogs", "item_id")
}
