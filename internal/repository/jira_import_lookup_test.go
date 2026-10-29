//go:build test

package repository

import (
	"errors"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

func TestJiraImportLookupRepositoriesOwnEntityQueries(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer func() { _ = tdb.Close() }()

	statuses := NewStatusRepository(tdb.Database)
	if _, err := tdb.ExecWrite(`
		INSERT INTO statuses (name, description, category_id) VALUES ('Jira lookup status', '', 1)
	`); err != nil {
		t.Fatalf("insert status: %v", err)
	}
	status, err := statuses.FindByName("Jira lookup status")
	if err != nil || status == nil || status.Name != "Jira lookup status" {
		t.Fatalf("find status by name = %#v, %v", status, err)
	}
	if _, err := statuses.FindByName("missing-jira-status"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing status error = %v, want ErrNotFound", err)
	}

	itemTypes := NewItemTypeRepository(tdb.Database)
	var itemTypeID int
	if err := tdb.QueryRow(`
		INSERT INTO item_types (name, description, icon, color)
		VALUES ('Jira lookup item type', '', 'Circle', '#000000') RETURNING id
	`).Scan(&itemTypeID); err != nil {
		t.Fatalf("insert item type: %v", err)
	}
	itemType, err := itemTypes.FindByName("Jira lookup item type")
	if err != nil || itemType == nil || itemType.ID != itemTypeID {
		t.Fatalf("find item type by name = %#v, %v", itemType, err)
	}
	if _, err := itemTypes.FindByName("missing-jira-item-type"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing item type error = %v, want ErrNotFound", err)
	}

	customFields := NewCustomFieldRepository(tdb.Database)
	id, err := customFields.Create(&models.CustomFieldDefinition{
		Name: "Jira lookup field", FieldType: "text", Options: "{}",
	}, time.Now())
	if err != nil {
		t.Fatalf("create custom field: %v", err)
	}
	field, err := customFields.FindByName("jira LOOKUP field")
	if err != nil || field == nil || field.ID != int(id) {
		t.Fatalf("find custom field by name = %#v, %v", field, err)
	}
	preferred, err := customFields.FindPreferredByName("Jira lookup field", "text")
	if err != nil || preferred == nil || preferred.ID != int(id) {
		t.Fatalf("find preferred custom field = %#v, %v", preferred, err)
	}
}
