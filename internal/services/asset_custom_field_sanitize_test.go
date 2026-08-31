//go:build test

package services

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// assetCFSanitizeFixture creates an asset set + type with one text and one
// textarea custom field attached, returning the service and the ids needed
// to exercise the custom-field write path.
type assetCFSanitizeFixture struct {
	svc         *AssetService
	setID       int
	assetTypeID int
	textFieldID string
	areaFieldID string
}

func newAssetCFSanitizeFixture(t *testing.T, tdb *testutils.TestDB) assetCFSanitizeFixture {
	t.Helper()
	var setID int
	if err := tdb.QueryRow(`
		INSERT INTO asset_management_sets (name, created_at, updated_at)
		VALUES ('WI-319 set', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) RETURNING id
	`).Scan(&setID); err != nil {
		t.Fatalf("insert asset set: %v", err)
	}
	var typeID int
	if err := tdb.QueryRow(`
		INSERT INTO asset_types (set_id, name, created_at, updated_at)
		VALUES (?, 'Laptop', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) RETURNING id
	`, setID).Scan(&typeID); err != nil {
		t.Fatalf("insert asset type: %v", err)
	}

	insertField := func(name, fieldType string) string {
		var cfID int
		if err := tdb.QueryRow(`
			INSERT INTO custom_field_definitions (name, field_type, required, display_order, system_default, created_at, updated_at)
			VALUES (?, ?, FALSE, 1, FALSE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) RETURNING id
		`, name, fieldType).Scan(&cfID); err != nil {
			t.Fatalf("insert custom field %q: %v", name, err)
		}
		if _, err := tdb.Exec(`
			INSERT INTO asset_type_fields (asset_type_id, custom_field_id, is_required, display_order)
			VALUES (?, ?, FALSE, 1)
		`, typeID, cfID); err != nil {
			t.Fatalf("attach custom field %q: %v", name, err)
		}
		return strconv.Itoa(cfID)
	}

	db := tdb.GetDatabase()
	return assetCFSanitizeFixture{
		svc:         NewAssetService(db, repository.NewAssetRepository(db)),
		setID:       setID,
		assetTypeID: typeID,
		textFieldID: insertField("Notes", "text"),
		areaFieldID: insertField("Details", "textarea"),
	}
}

// WI-319: ValidateCustomFieldsSchema must sanitize text/textarea values
// in place so the values map comes out write-safe.
func TestValidateCustomFieldsSchema_SanitizesTextValues(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	fx := newAssetCFSanitizeFixture(t, tdb)

	long := strings.Repeat("x", 400)
	values := map[string]interface{}{
		fx.textFieldID: "<script>alert(1)</script>" + long,
		fx.areaFieldID: "line one\nline two\n[click](javascript:alert(1))",
	}
	if err := fx.svc.ValidateCustomFieldsSchema(fx.assetTypeID, values, CustomFieldsValidationOpts{}); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	text := values[fx.textFieldID].(string)
	if strings.Contains(text, "<script>") {
		t.Errorf("HTML tag survived text sanitization: %q", text)
	}
	if len([]rune(text)) > 256 {
		t.Errorf("text value not capped at 256 runes, got %d", len([]rune(text)))
	}

	area := values[fx.areaFieldID].(string)
	if strings.Contains(area, "javascript:") {
		t.Errorf("dangerous markdown URL survived textarea sanitization: %q", area)
	}
	if !strings.Contains(area, "line one\nline two") {
		t.Errorf("newlines should be preserved in textarea, got: %q", area)
	}
}

// WI-319: the handler pre-encodes custom_field_values to JSON before the
// service sanitizes the map — CreateAsset must re-encode so the sanitized
// values are what lands in the database.
func TestCreateAsset_PersistsSanitizedCustomFields(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	fx := newAssetCFSanitizeFixture(t, tdb)

	values := map[string]interface{}{
		fx.textFieldID: "<b>bold</b> note",
	}
	// Simulate the handler's pre-sanitization encode.
	preEncoded := `{"` + fx.textFieldID + `":"<b>bold</b> note"}`

	asset, err := fx.svc.CreateAsset(AuditActor{UserID: 1}, repository.CreateAssetInput{
		SetID:                 fx.setID,
		AssetTypeID:           fx.assetTypeID,
		Title:                 "Test asset",
		CustomFieldValuesJSON: &preEncoded,
		CreatedBy:             1,
		CreatedAt:             time.Now(),
	}, values)
	if err != nil {
		t.Fatalf("CreateAsset: %v", err)
	}

	var storedJSON string
	if err := tdb.QueryRow(`SELECT custom_field_values FROM assets WHERE id = ?`, asset.ID).Scan(&storedJSON); err != nil {
		t.Fatalf("read stored custom_field_values: %v", err)
	}
	if strings.Contains(storedJSON, "<b>") {
		t.Errorf("pre-encoded unsanitized JSON reached persistence: %s", storedJSON)
	}
	if !strings.Contains(storedJSON, "bold note") {
		t.Errorf("expected sanitized value in stored JSON, got: %s", storedJSON)
	}
}

func TestUpdateAsset_PersistsSanitizedCustomFields(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	fx := newAssetCFSanitizeFixture(t, tdb)

	clean := `{}`
	asset, err := fx.svc.CreateAsset(AuditActor{UserID: 1}, repository.CreateAssetInput{
		SetID:                 fx.setID,
		AssetTypeID:           fx.assetTypeID,
		Title:                 "Update target",
		CustomFieldValuesJSON: &clean,
		CreatedBy:             1,
		CreatedAt:             time.Now(),
	}, map[string]interface{}{})
	if err != nil {
		t.Fatalf("CreateAsset: %v", err)
	}

	values := map[string]interface{}{
		fx.areaFieldID: "safe\n[x](javascript:alert(1))",
	}
	preEncoded := `{"` + fx.areaFieldID + `":"safe\n[x](javascript:alert(1))"}`
	snap, err := fx.svc.repo.GetAssetUpdateSnapshot(asset.ID)
	if err != nil {
		t.Fatalf("GetAssetUpdateSnapshot: %v", err)
	}
	if _, err := fx.svc.UpdateAsset(AuditActor{UserID: 1}, asset.ID, *snap, repository.UpdateAssetInput{
		AssetTypeID:           fx.assetTypeID,
		Title:                 "Update target",
		CustomFieldValuesJSON: &preEncoded,
	}, values); err != nil {
		t.Fatalf("UpdateAsset: %v", err)
	}

	var storedJSON string
	if err := tdb.QueryRow(`SELECT custom_field_values FROM assets WHERE id = ?`, asset.ID).Scan(&storedJSON); err != nil {
		t.Fatalf("read stored custom_field_values: %v", err)
	}
	if strings.Contains(storedJSON, "javascript:") {
		t.Errorf("unsanitized markdown URL reached persistence: %s", storedJSON)
	}
}
