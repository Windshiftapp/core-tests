//go:build test

package database

import (
	"fmt"
	"testing"
)

func TestMigrationThemeLogoURLAddsNullableColumn(t *testing.T) {
	dsn := fmt.Sprintf("file:%s/theme-logo.db?mode=memory&cache=shared", t.TempDir())
	db, err := NewSQLiteDBWithPoolSizes(dsn, 2, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// This fixture represents the themes table before the logo column.
	for _, statement := range []string{
		`CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			checksum TEXT NOT NULL,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE themes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			builtin_key TEXT,
			name TEXT NOT NULL,
			description TEXT,
			is_default BOOLEAN DEFAULT false,
			is_active BOOLEAN DEFAULT false,
			nav_background_color_light TEXT NOT NULL DEFAULT '#ffffff',
			nav_text_color_light TEXT NOT NULL DEFAULT '#374151',
			nav_background_color_dark TEXT NOT NULL DEFAULT '#1f2937',
			nav_text_color_dark TEXT NOT NULL DEFAULT '#f3f4f6',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`INSERT INTO themes (name, is_default, is_active) VALUES ('Default', true, true)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("legacy fixture: %v", err)
		}
	}

	var migration *Migration
	for i := range Catalog {
		if Catalog[i].Version == "20260911_theme_logo_url" {
			migration = &Catalog[i]
			break
		}
	}
	if migration == nil {
		t.Fatal("theme logo migration missing from catalog")
	}
	if err := applyMigration(db, driverSQLite, *migration); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	// Existing rows must survive with a NULL logo, and the new column must be
	// writable for later branding updates.
	var logoURL *string
	if err := db.QueryRow(`SELECT logo_url FROM themes WHERE name = 'Default'`).Scan(&logoURL); err != nil {
		t.Fatalf("read migrated row: %v", err)
	}
	if logoURL != nil {
		t.Fatalf("existing logo_url = %v, want NULL", *logoURL)
	}
	if _, err := db.Exec(`UPDATE themes SET logo_url = '/api/portal-assets/1' WHERE name = 'Default'`); err != nil {
		t.Fatalf("write logo_url after migration: %v", err)
	}
}

func TestMigrationThemeDarkLogoURLAddsNullableColumn(t *testing.T) {
	dsn := fmt.Sprintf("file:%s/theme-dark-logo.db?mode=memory&cache=shared", t.TempDir())
	db, err := NewSQLiteDBWithPoolSizes(dsn, 2, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Fixture represents a database that already applied the light logo
	// migration but not the dark one.
	for _, statement := range []string{
		`CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			checksum TEXT NOT NULL,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE themes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			builtin_key TEXT,
			name TEXT NOT NULL,
			description TEXT,
			is_default BOOLEAN DEFAULT false,
			is_active BOOLEAN DEFAULT false,
			nav_background_color_light TEXT NOT NULL DEFAULT '#ffffff',
			nav_text_color_light TEXT NOT NULL DEFAULT '#374151',
			nav_background_color_dark TEXT NOT NULL DEFAULT '#1f2937',
			nav_text_color_dark TEXT NOT NULL DEFAULT '#f3f4f6',
			logo_url TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`INSERT INTO themes (name, is_default, is_active, logo_url) VALUES ('Default', true, true, '/api/portal-assets/1')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("legacy fixture: %v", err)
		}
	}

	var migration *Migration
	for i := range Catalog {
		if Catalog[i].Version == "20260924_theme_dark_logo_url" {
			migration = &Catalog[i]
			break
		}
	}
	if migration == nil {
		t.Fatal("theme dark logo migration missing from catalog")
	}
	if err := applyMigration(db, driverSQLite, *migration); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	// The light logo must survive untouched and the new column must start NULL
	// and accept a later branding update.
	var logoURL string
	var logoURLDark *string
	if err := db.QueryRow(`SELECT COALESCE(logo_url, ''), logo_url_dark FROM themes WHERE name = 'Default'`).Scan(&logoURL, &logoURLDark); err != nil {
		t.Fatalf("read migrated row: %v", err)
	}
	if logoURL != "/api/portal-assets/1" {
		t.Fatalf("existing logo_url = %q, want preserved light URL", logoURL)
	}
	if logoURLDark != nil {
		t.Fatalf("existing logo_url_dark = %v, want NULL", *logoURLDark)
	}
	if _, err := db.Exec(`UPDATE themes SET logo_url_dark = '/api/portal-assets/2' WHERE name = 'Default'`); err != nil {
		t.Fatalf("write logo_url_dark after migration: %v", err)
	}
}
