package services

import (
	"path/filepath"
	"testing"

	"windshift/internal/database"
)

func TestCreateItemPersistsCustomAndVirtualFieldsInInsert(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "item-fields.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	var workspaceID, itemTypeID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Atomic fields', 'AFI') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO item_types (name) VALUES ('Atomic Field Type') RETURNING id`).Scan(&itemTypeID); err != nil {
		t.Fatalf("insert item type: %v", err)
	}

	itemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID:           workspaceID,
		ItemTypeID:            &itemTypeID,
		Title:                 "Field values are atomic",
		CustomFieldValuesJSON: `{"7":"selected"}`,
		VirtualFieldDataJSON:  `{"summary":"customer supplied"}`,
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	var customFields, virtualFields string
	if err := db.QueryRow(`
		SELECT COALESCE(custom_field_values, ''), COALESCE(virtual_field_data, '')
		FROM items WHERE id = ?
	`, itemID).Scan(&customFields, &virtualFields); err != nil {
		t.Fatalf("load item fields: %v", err)
	}
	if customFields != `{"7":"selected"}` {
		t.Fatalf("custom_field_values = %q", customFields)
	}
	if virtualFields != `{"summary":"customer supplied"}` {
		t.Fatalf("virtual_field_data = %q", virtualFields)
	}
}
