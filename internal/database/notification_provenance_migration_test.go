//go:build test

package database

import (
	"fmt"
	"testing"
)

func TestMigrationNotificationProvenanceMarksExistingRowsLegacy(t *testing.T) {
	dsn := fmt.Sprintf("file:%s/notification-provenance.db?mode=memory&cache=shared", t.TempDir())
	db, err := NewSQLiteDBWithPoolSizes(dsn, 2, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// This fixture is the production notification table before provenance.
	for _, statement := range []string{
		`CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			checksum TEXT NOT NULL,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE notifications (
			id INTEGER PRIMARY KEY,
			user_id INTEGER NOT NULL,
			title TEXT NOT NULL,
			message TEXT NOT NULL,
			type TEXT NOT NULL,
			timestamp DATETIME,
			read BOOLEAN,
			created_at DATETIME,
			updated_at DATETIME
		)`,
		`INSERT INTO notifications (
			id, user_id, title, message, type, timestamp, read, created_at, updated_at
		) VALUES (1, 7, 'retained', 'unknown source', 'info', CURRENT_TIMESTAMP, false, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("legacy fixture: %v", err)
		}
	}

	var migration *Migration
	for i := range Catalog {
		if Catalog[i].Version == "20260827_notification_provenance" {
			migration = &Catalog[i]
			break
		}
	}
	if migration == nil {
		t.Fatal("notification provenance migration missing from catalog")
	}
	if err := applyMigration(db, driverSQLite, *migration); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	var scope string
	var workspaceID, itemID, sourceType, sourceID any
	if err := db.QueryRow(`
		SELECT authorization_scope, workspace_id, item_id, source_type, source_id
		FROM notifications WHERE id = 1
	`).Scan(&scope, &workspaceID, &itemID, &sourceType, &sourceID); err != nil {
		t.Fatalf("read migrated row: %v", err)
	}
	if scope != "legacy" || workspaceID != nil || itemID != nil || sourceType != nil || sourceID != nil {
		t.Fatalf("migrated provenance = scope:%q workspace:%v item:%v source:%v/%v", scope, workspaceID, itemID, sourceType, sourceID)
	}

	var referenceMigration *Migration
	for i := range Catalog {
		if Catalog[i].Version == "20260905_notification_reference_provenance" {
			referenceMigration = &Catalog[i]
			break
		}
	}
	if referenceMigration == nil {
		t.Fatal("notification reference provenance migration missing from catalog")
	}
	if err := applyMigration(db, driverSQLite, *referenceMigration); err != nil {
		t.Fatalf("apply reference migration: %v", err)
	}
	var entityType, entityID, referencedWorkspaceID, permission any
	if err := db.QueryRow(`
		SELECT referenced_entity_type, referenced_entity_id, referenced_workspace_id, referenced_workspace_permission
		FROM notifications WHERE id = 1
	`).Scan(&entityType, &entityID, &referencedWorkspaceID, &permission); err != nil {
		t.Fatalf("read reference-migrated row: %v", err)
	}
	if entityType != nil || entityID != nil || referencedWorkspaceID != nil || permission != nil {
		t.Fatalf("migrated reference provenance = type:%v id:%v workspace:%v permission:%v", entityType, entityID, referencedWorkspaceID, permission)
	}

	var claimMigration *Migration
	for i := range Catalog {
		if Catalog[i].Version == "20260905_notification_email_claims" {
			claimMigration = &Catalog[i]
			break
		}
	}
	if claimMigration == nil {
		t.Fatal("notification email claim migration missing from catalog")
	}
	if err := applyMigration(db, driverSQLite, *claimMigration); err != nil {
		t.Fatalf("apply email claim migration: %v", err)
	}
	var state string
	var owner, token, expires, lastError any
	var attempts int
	if err := db.QueryRow(`
		SELECT email_delivery_state, email_claim_owner, email_claim_token,
		       email_claim_expires_at, email_delivery_attempts, email_last_error
		FROM notifications WHERE id = 1
	`).Scan(&state, &owner, &token, &expires, &attempts, &lastError); err != nil {
		t.Fatalf("read email-claim migrated row: %v", err)
	}
	if state != "pending" || owner != nil || token != nil || expires != nil || attempts != 0 || lastError != nil {
		t.Fatalf("migrated email claim = state:%q owner:%v token:%v expires:%v attempts:%d error:%v", state, owner, token, expires, attempts, lastError)
	}

	var deliveryMigration *Migration
	for i := range Catalog {
		if Catalog[i].Version == "20260905_notification_delivery_keys" {
			deliveryMigration = &Catalog[i]
			break
		}
	}
	if deliveryMigration == nil {
		t.Fatal("notification delivery-key migration missing from catalog")
	}
	if err := applyMigration(db, driverSQLite, *deliveryMigration); err != nil {
		t.Fatalf("apply notification delivery-key migration: %v", err)
	}
	var deliveryKey any
	if err := db.QueryRow(`SELECT delivery_key FROM notifications WHERE id = 1`).Scan(&deliveryKey); err != nil {
		t.Fatalf("read migrated delivery key: %v", err)
	}
	if deliveryKey != nil {
		t.Fatalf("legacy delivery key = %v, want NULL", deliveryKey)
	}
}
