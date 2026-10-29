package database

import (
	"fmt"
	"testing"
)

func TestMigrationAgentSkillPageSnapshotsBackfillsExistingReferences(t *testing.T) {
	dsn := fmt.Sprintf("file:%s/skill-snapshots.db?mode=memory&cache=shared", t.TempDir())
	db, err := NewSQLiteDBWithPoolSizes(dsn, 2, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// This is intentionally a pre-migration schema fixture. Migration tests are
	// the one layer that must represent an older production table shape.
	for _, statement := range []string{
		`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, name TEXT, checksum TEXT)`,
		`CREATE TABLE pages (id INTEGER PRIMARY KEY, title TEXT NOT NULL, content TEXT NOT NULL, updated_at DATETIME NOT NULL)`,
		`CREATE TABLE workspace_agent_skill_pages (skill_id INTEGER NOT NULL, page_id INTEGER NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY(skill_id, page_id))`,
		`INSERT INTO pages(id, title, content, updated_at) VALUES (7, 'Runbook', 'saved instructions', '2026-08-24 10:00:00')`,
		`INSERT INTO workspace_agent_skill_pages(skill_id, page_id) VALUES (3, 7)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("legacy fixture: %v", err)
		}
	}
	var migration *Migration
	for i := range Catalog {
		if Catalog[i].Version == "20260824_agent_skill_page_snapshots" {
			migration = &Catalog[i]
			break
		}
	}
	if migration == nil {
		t.Fatal("snapshot migration missing from catalog")
	}
	if err := applyMigration(db, driverSQLite, *migration); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	var title, content string
	var updatedAt, snapshotAt any
	if err := db.QueryRow(`
		SELECT title_snapshot, content_snapshot, page_updated_at_snapshot, snapshot_at
		FROM workspace_agent_skill_pages WHERE skill_id = 3 AND page_id = 7
	`).Scan(&title, &content, &updatedAt, &snapshotAt); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if title != "Runbook" || content != "saved instructions" || updatedAt == nil || snapshotAt == nil {
		t.Fatalf("backfill mismatch: title=%q content=%q updated=%v snapshot=%v", title, content, updatedAt, snapshotAt)
	}
}
