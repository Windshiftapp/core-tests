//go:build test

package services_test

import (
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

// seedMandatoryTemplate creates an active mandatory template for itemTypeID and
// returns it.
func seedMandatoryTemplate(t *testing.T, db database.Database, workspaceID, itemTypeID int, body string) *models.ItemTemplate {
	t.Helper()
	tmpl, err := repository.NewTemplateRepository(db).Create(&models.ItemTemplate{
		WorkspaceID:     workspaceID,
		Name:            "mandatory-tmpl",
		DescriptionBody: body,
		Mode:            models.TemplateModeMandatory,
		IsActive:        true,
		ItemTypeIDs:     []int{itemTypeID},
	})
	if err != nil {
		t.Fatalf("seed mandatory template: %v", err)
	}
	return tmpl
}

func descriptionOf(t *testing.T, db database.Database, itemID int64) string {
	t.Helper()
	var desc string
	if err := db.QueryRow("SELECT description FROM items WHERE id = ?", itemID).Scan(&desc); err != nil {
		t.Fatalf("read description: %v", err)
	}
	return desc
}

// TestCreateItem_MandatoryTemplate_AppliedWhenEmpty verifies the seam fills an
// empty description with the mandatory template body and reports Applied=true.
func TestCreateItem_MandatoryTemplate_AppliedWhenEmpty(t *testing.T) {
	tdb := testutils.CreateTestDB(t, false)
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	var itemTypeID int
	if err := db.QueryRow("SELECT id FROM item_types ORDER BY id LIMIT 1").Scan(&itemTypeID); err != nil {
		t.Fatalf("get item type: %v", err)
	}
	tmpl := seedMandatoryTemplate(t, db, data.WorkspaceID, itemTypeID, "## Steps to reproduce\n")

	var out services.MandatoryTemplateInfo
	itemID, err := services.CreateItem(db, services.ItemCreationParams{
		WorkspaceID:          data.WorkspaceID,
		Title:                "empty desc",
		Description:          "",
		ItemTypeID:           &itemTypeID,
		MandatoryTemplateOut: &out,
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if got := descriptionOf(t, db, itemID); got != "## Steps to reproduce\n" {
		t.Fatalf("description not filled from template, got %q", got)
	}
	if !out.Applied || out.TemplateID != tmpl.ID {
		t.Fatalf("expected Applied=true id=%d, got %+v", tmpl.ID, out)
	}
}

// TestCreateItem_MandatoryTemplate_NotAppliedWhenProvided verifies a supplied
// description is respected (Applied=false) but the enforced template is still
// reported.
func TestCreateItem_MandatoryTemplate_NotAppliedWhenProvided(t *testing.T) {
	tdb := testutils.CreateTestDB(t, false)
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	var itemTypeID int
	if err := db.QueryRow("SELECT id FROM item_types ORDER BY id LIMIT 1").Scan(&itemTypeID); err != nil {
		t.Fatalf("get item type: %v", err)
	}
	tmpl := seedMandatoryTemplate(t, db, data.WorkspaceID, itemTypeID, "## Steps\n")

	var out services.MandatoryTemplateInfo
	itemID, err := services.CreateItem(db, services.ItemCreationParams{
		WorkspaceID:          data.WorkspaceID,
		Title:                "supplied desc",
		Description:          "my own description",
		ItemTypeID:           &itemTypeID,
		MandatoryTemplateOut: &out,
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if got := descriptionOf(t, db, itemID); got != "my own description" {
		t.Fatalf("supplied description was overwritten, got %q", got)
	}
	if out.Applied || out.TemplateID != tmpl.ID {
		t.Fatalf("expected Applied=false id=%d reported, got %+v", tmpl.ID, out)
	}
}

// TestCreateItem_NoMandatoryTemplate_NoReport verifies the out param stays zero
// when the type enforces no mandatory template.
func TestCreateItem_NoMandatoryTemplate_NoReport(t *testing.T) {
	tdb := testutils.CreateTestDB(t, false)
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	var itemTypeID int
	if err := db.QueryRow("SELECT id FROM item_types ORDER BY id LIMIT 1").Scan(&itemTypeID); err != nil {
		t.Fatalf("get item type: %v", err)
	}

	var out services.MandatoryTemplateInfo
	itemID, err := services.CreateItem(db, services.ItemCreationParams{
		WorkspaceID:          data.WorkspaceID,
		Title:                "no template",
		Description:          "",
		ItemTypeID:           &itemTypeID,
		MandatoryTemplateOut: &out,
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if got := descriptionOf(t, db, itemID); got != "" {
		t.Fatalf("description should be empty, got %q", got)
	}
	if out.TemplateID != 0 {
		t.Fatalf("expected no enforced template, got %+v", out)
	}
}
