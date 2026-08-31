package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
)

type screenReadCountingDB struct {
	database.Database
	reads int
}

func (db *screenReadCountingDB) Query(query string, args ...interface{}) (*sql.Rows, error) {
	db.reads++
	return db.Database.Query(query, args...)
}

func TestScreenListCanIncludeAllFieldsWithTwoReads(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "screens-with-fields.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	insertID := func(label, query string, args ...interface{}) int {
		t.Helper()
		result, err := db.ExecWrite(query, args...)
		if err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("LastInsertId for %s: %v", label, err)
		}
		return int(id)
	}
	fieldID := insertID(
		"custom field",
		`INSERT INTO custom_field_definitions (name, field_type, options) VALUES ('Bulk field', 'select', '{"items":[{"id":1,"label":"One"}]}')`,
	)
	screenOneID := insertID(
		"first screen",
		`INSERT INTO screens (name, description) VALUES ('Bulk Screen One', 'First')`,
	)
	screenTwoID := insertID(
		"second screen",
		`INSERT INTO screens (name, description) VALUES ('Bulk Screen Two', 'Second')`,
	)
	if _, err := db.ExecWrite(`
		INSERT INTO screen_fields
			(screen_id, field_type, field_identifier, display_order, is_required, field_width)
		VALUES (?, 'custom', ?, 3, true, 'half')
	`, screenOneID, strconv.Itoa(fieldID)); err != nil {
		t.Fatalf("insert custom screen field: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO screen_fields
			(screen_id, field_type, field_identifier, display_order, is_required, field_width)
		VALUES (?, 'custom', ?, 4, false, 'full')
	`, screenOneID, strconv.Itoa(fieldID+100000)); err != nil {
		t.Fatalf("insert orphaned custom screen field: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO screen_fields
			(screen_id, field_type, field_identifier, display_order, is_required, field_width)
		VALUES (?, 'system', 'title', 0, true, 'full')
	`, screenTwoID); err != nil {
		t.Fatalf("insert system screen field: %v", err)
	}

	countingDB := &screenReadCountingDB{Database: db}
	handler := NewScreenHandler(countingDB)
	recorder := httptest.NewRecorder()
	handler.GetAll(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/screens?include_fields=true", nil),
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if countingDB.reads != 2 {
		t.Fatalf("read queries = %d, want 2 independent of screen count", countingDB.reads)
	}
	var screens []models.Screen
	if err := json.NewDecoder(recorder.Body).Decode(&screens); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	findScreen := func(id int) *models.Screen {
		t.Helper()
		for i := range screens {
			if screens[i].ID == id {
				return &screens[i]
			}
		}
		return nil
	}
	first := findScreen(screenOneID)
	if first == nil || len(first.Fields) != 4 {
		t.Fatalf("first screen = %+v, want three required defaults plus custom field", first)
	}
	custom := first.Fields[3]
	if custom.FieldIdentifier != strconv.Itoa(fieldID) || custom.FieldName != "Bulk field" || custom.FieldConfig == nil {
		t.Fatalf("custom field = %+v, want joined custom definition %d", custom, fieldID)
	}
	second := findScreen(screenTwoID)
	if second == nil || len(second.Fields) != 3 {
		t.Fatalf("second screen = %+v, want normalized always-visible fields", second)
	}

	fieldsRecorder := httptest.NewRecorder()
	fieldsRequest := httptest.NewRequest(http.MethodGet, "/api/screens/1/fields", nil)
	fieldsRequest.SetPathValue("id", strconv.Itoa(screenOneID))
	handler.GetFields(fieldsRecorder, fieldsRequest)
	if fieldsRecorder.Code != http.StatusOK {
		t.Fatalf("get fields status = %d, want 200; body=%s", fieldsRecorder.Code, fieldsRecorder.Body.String())
	}
	var screenFields []models.ScreenField
	if err := json.NewDecoder(fieldsRecorder.Body).Decode(&screenFields); err != nil {
		t.Fatalf("decode screen fields response: %v", err)
	}
	if len(screenFields) != 4 || screenFields[3].FieldIdentifier != strconv.Itoa(fieldID) {
		t.Fatalf("screen fields = %+v, want required defaults plus valid custom field", screenFields)
	}
}
