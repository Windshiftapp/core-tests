package services

import (
	"fmt"
	"path/filepath"
	"testing"

	"windshift/internal/database"
	"windshift/internal/repository"
)

func TestValidateCustomFieldsSchema(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "asset-cf.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	defer db.Close()

	repo := repository.NewAssetRepository(db)
	svc := NewAssetService(db, repo)

	setID := mustAssetExecInsertID(t, db, "INSERT INTO asset_management_sets (name) VALUES (?)", "Test Set")
	typeID := mustAssetExecInsertID(t, db, "INSERT INTO asset_types (set_id, name) VALUES (?, ?)", setID, "Laptop")

	selectFieldID := mustAssetExecInsertID(t, db,
		"INSERT INTO custom_field_definitions (name, field_type, options) VALUES (?, ?, ?)",
		"Color", "select", `{"next_id":3,"items":[{"id":1,"label":"Red"},{"id":2,"label":"Blue"}]}`)
	multiFieldID := mustAssetExecInsertID(t, db,
		"INSERT INTO custom_field_definitions (name, field_type, options) VALUES (?, ?, ?)",
		"Tags", "multiselect", `{"next_id":3,"items":[{"id":1,"label":"Tag A"},{"id":2,"label":"Tag B"}]}`)
	reqFieldID := mustAssetExecInsertID(t, db,
		"INSERT INTO custom_field_definitions (name, field_type, options) VALUES (?, ?, ?)",
		"RequiredSelect", "select", `{"next_id":2,"items":[{"id":1,"label":"Yes"}]}`)

	for _, f := range []struct {
		id       int
		required bool
	}{{
		id:       selectFieldID,
		required: false,
	}, {
		id:       multiFieldID,
		required: false,
	}, {
		id:       reqFieldID,
		required: true,
	}} {
		mustAssetExec(t, db,
			"INSERT INTO asset_type_fields (asset_type_id, custom_field_id, is_required) VALUES (?, ?, ?)",
			typeID, f.id, f.required)
	}

	tests := []struct {
		name    string
		values  map[string]interface{}
		opts    CustomFieldsValidationOpts
		wantErr bool
	}{
		{
			name:   "numeric select ID accepted",
			values: map[string]interface{}{fmt.Sprintf("%d", selectFieldID): 1},
		},
		{
			name:   "legacy select label accepted",
			values: map[string]interface{}{fmt.Sprintf("%d", selectFieldID): "Red"},
		},
		{
			name:    "invalid select value rejected",
			values:  map[string]interface{}{fmt.Sprintf("%d", selectFieldID): 99},
			wantErr: true,
		},
		{
			name:   "multiselect numeric IDs accepted",
			values: map[string]interface{}{fmt.Sprintf("%d", multiFieldID): []interface{}{1, 2}},
		},
		{
			name:   "multiselect mixed label and ID accepted",
			values: map[string]interface{}{fmt.Sprintf("%d", multiFieldID): []interface{}{"Tag A", 2}},
		},
		{
			name:    "invalid multiselect element rejected",
			values:  map[string]interface{}{fmt.Sprintf("%d", multiFieldID): []interface{}{1, 99}},
			wantErr: true,
		},
		{
			name:   "field name key accepted",
			values: map[string]interface{}{"Color": "Blue"},
		},
		{
			name:    "unknown key rejected",
			values:  map[string]interface{}{"Unknown": "x"},
			wantErr: true,
		},
		{
			name:    "required field missing rejected",
			values:  map[string]interface{}{fmt.Sprintf("%d", selectFieldID): 1},
			opts:    CustomFieldsValidationOpts{EnforceRequired: true},
			wantErr: true,
		},
		{
			name:   "required field present accepted",
			values: map[string]interface{}{fmt.Sprintf("%d", selectFieldID): 1, fmt.Sprintf("%d", reqFieldID): 1},
			opts:   CustomFieldsValidationOpts{EnforceRequired: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.ValidateCustomFieldsSchema(typeID, tt.values, tt.opts)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateCustomFieldsSchema() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func mustAssetExecInsertID(t *testing.T, db database.Database, query string, args ...interface{}) int {
	t.Helper()
	result, err := db.ExecWrite(query, args...)
	if err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return int(id)
}

func mustAssetExec(t *testing.T, db database.Database, query string, args ...interface{}) {
	t.Helper()
	if _, err := db.ExecWrite(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
