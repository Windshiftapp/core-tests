//go:build test

package database

import (
	"fmt"
	"testing"
)

func TestMigrationThemeBrandingAddsFullLogoAndLayoutColumns(t *testing.T) {
	dsn := fmt.Sprintf("file:%s/theme-branding.db?mode=memory&cache=shared", t.TempDir())
	db, err := NewSQLiteDBWithPoolSizes(dsn, 2, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Fixture represents a database that applied the light and dark logo
	// migrations but not the brand-layout/full-logo one.
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
			logo_url_dark TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`INSERT INTO themes (name, is_default, is_active, logo_url, logo_url_dark) VALUES ('Default', true, true, '/api/portal-assets/1', '/api/portal-assets/2')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("legacy fixture: %v", err)
		}
	}

	var migration *Migration
	for i := range Catalog {
		if Catalog[i].Version == "20261121_theme_branding" {
			migration = &Catalog[i]
			break
		}
	}
	if migration == nil {
		t.Fatal("theme branding migration missing from catalog")
	}
	if err := applyMigration(db, driverSQLite, *migration); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	// Existing rows keep their logos, get NULL full logos, and default to the
	// mark + text layout.
	var logoURL, logoURLDark string
	var logoFullURL, logoFullURLDark *string
	var brandLayout string
	if err := db.QueryRow(
		`SELECT COALESCE(logo_url, ''), COALESCE(logo_url_dark, ''), logo_full_url, logo_full_url_dark, brand_layout FROM themes WHERE name = 'Default'`,
	).Scan(&logoURL, &logoURLDark, &logoFullURL, &logoFullURLDark, &brandLayout); err != nil {
		t.Fatalf("read migrated row: %v", err)
	}
	if logoURL != "/api/portal-assets/1" || logoURLDark != "/api/portal-assets/2" {
		t.Fatalf("existing logos = %q / %q, want preserved", logoURL, logoURLDark)
	}
	if logoFullURL != nil || logoFullURLDark != nil {
		t.Fatalf("full logos = %v / %v, want NULL", logoFullURL, logoFullURLDark)
	}
	if brandLayout != "mark_text" {
		t.Fatalf("brand_layout = %q, want mark_text", brandLayout)
	}
	if _, err := db.Exec(`UPDATE themes SET logo_full_url = '/api/portal-assets/3', brand_layout = 'full_logo' WHERE name = 'Default'`); err != nil {
		t.Fatalf("write brand fields after migration: %v", err)
	}
}
