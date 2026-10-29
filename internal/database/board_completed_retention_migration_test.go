//go:build test

package database

import (
	"fmt"
	"testing"
)

func TestMigrationBoardCompletedItemRetentionAddsNullableColumn(t *testing.T) {
	dsn := fmt.Sprintf("file:%s/board-retention.db?mode=memory&cache=shared", t.TempDir())
	db, err := NewSQLiteDBWithPoolSizes(dsn, 2, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// This fixture represents the production table before the retention option.
	for _, statement := range []string{
		`CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			checksum TEXT NOT NULL,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE board_configurations (
			id INTEGER PRIMARY KEY,
			show_rightmost_column_last_50 BOOLEAN DEFAULT false
		)`,
		`INSERT INTO board_configurations (id, show_rightmost_column_last_50) VALUES (7, false)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("legacy fixture: %v", err)
		}
	}

	var migration *Migration
	for i := range Catalog {
		if Catalog[i].Version == "20260826_board_completed_item_retention" {
			migration = &Catalog[i]
			break
		}
	}
	if migration == nil {
		t.Fatal("board completed item retention migration missing from catalog")
	}
	if err := applyMigration(db, driverSQLite, *migration); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	var retentionDays *int
	if err := db.QueryRow(`SELECT completed_item_retention_days FROM board_configurations WHERE id = 7`).Scan(&retentionDays); err != nil {
		t.Fatalf("read migrated row: %v", err)
	}
	if retentionDays != nil {
		t.Fatalf("existing retention = %v, want NULL", *retentionDays)
	}
	if _, err := db.Exec(`UPDATE board_configurations SET completed_item_retention_days = 45 WHERE id = 7`); err != nil {
		t.Fatalf("write retention after migration: %v", err)
	}
	var days int
	if err := db.QueryRow(`SELECT completed_item_retention_days FROM board_configurations WHERE id = 7`).Scan(&days); err != nil || days != 45 {
		t.Fatalf("retention after update = %d, %v; want 45", days, err)
	}
}
