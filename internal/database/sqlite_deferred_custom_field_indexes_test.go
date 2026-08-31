//go:build test

package database_test

import (
	"fmt"
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

func TestDeferredSQLiteTextAndDateIndexesMatchQueryExpressions(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	if tdb.GetDriverName() != "sqlite" {
		t.Skip("SQLite query planner regression")
	}
	db := tdb.GetDatabase()

	for _, fieldType := range []string{"text", "date"} {
		var fieldID int
		if err := db.QueryRow(`
			INSERT INTO custom_field_definitions (name, field_type)
			VALUES (?, ?) RETURNING id
		`, "Indexed "+fieldType, fieldType).Scan(&fieldID); err != nil {
			t.Fatalf("insert %s field: %v", fieldType, err)
		}
		indexName := fmt.Sprintf("idx_cf_items_%d", fieldID)
		if _, err := db.ExecWrite(`
			INSERT INTO custom_field_indexes (custom_field_id, target_table, index_name)
			VALUES (?, 'items', ?)
		`, fieldID, indexName); err != nil {
			t.Fatalf("record %s index: %v", fieldType, err)
		}

		database.MaterializeDeferredSQLiteCustomFieldIndexes(db)
		rows, err := db.Query(fmt.Sprintf(`
			EXPLAIN QUERY PLAN
			SELECT id FROM items
			WHERE NULLIF(custom_field_values, '') ->> '$."%d"' = ?
		`, fieldID), "value")
		if err != nil {
			t.Fatalf("explain %s lookup: %v", fieldType, err)
		}
		usedIndex := false
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				_ = rows.Close()
				t.Fatalf("scan %s query plan: %v", fieldType, err)
			}
			if strings.Contains(detail, indexName) {
				usedIndex = true
			}
		}
		_ = rows.Close()
		if !usedIndex {
			t.Errorf("%s lookup did not use %s", fieldType, indexName)
		}
	}
}
