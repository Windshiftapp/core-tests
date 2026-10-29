//go:build test

package services

import (
	"strconv"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// A linking custom field needs an existing item to attach links to, so a
// create form can never satisfy is_required. The screen editor disables the
// toggle; the service must not persist the impossible state either.
func TestReplaceScreenFieldsClearsRequiredOnLinkingCustomFields(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	screenID := testutils.InsertID(t, db, `
		INSERT INTO screens (name) VALUES ('Linking required screen')
	`)
	linkingID := testutils.InsertID(t, db, `
		INSERT INTO custom_field_definitions (name, field_type, description)
		VALUES ('Blocks', 'linking', 'needs an existing item')
	`)
	textID := testutils.InsertID(t, db, `
		INSERT INTO custom_field_definitions (name, field_type, description)
		VALUES ('Risk', 'text', 'plain text')
	`)

	svc := NewScreenProvisioningService(db)
	fields, err := svc.ReplaceScreenFields(AuditActor{UserID: 1}, screenID, []models.ScreenField{
		{
			FieldType:       "custom",
			FieldIdentifier: strconv.Itoa(linkingID),
			DisplayOrder:    0,
			IsRequired:      true,
			FieldWidth:      "full",
		},
		{
			FieldType:       "custom",
			FieldIdentifier: strconv.Itoa(textID),
			DisplayOrder:    1,
			IsRequired:      true,
			FieldWidth:      "full",
		},
	})
	if err != nil {
		t.Fatalf("ReplaceScreenFields: %v", err)
	}

	required := map[string]bool{}
	for _, field := range fields {
		if field.FieldType == "custom" {
			required[field.FieldIdentifier] = field.IsRequired
		}
	}
	if required[strconv.Itoa(linkingID)] {
		t.Errorf("linking field kept is_required=true: %#v", fields)
	}
	if !required[strconv.Itoa(textID)] {
		t.Errorf("text field lost is_required=true: %#v", fields)
	}
}
