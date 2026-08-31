//go:build test

package repository

import (
	"errors"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// twoItemTypeIDs returns two distinct item_type ids seeded by database init.
func twoItemTypeIDs(t *testing.T, tdb *testutils.TestDB) (int, int) {
	t.Helper()
	rows, err := tdb.Query("SELECT id FROM item_types ORDER BY id LIMIT 2")
	if err != nil {
		t.Fatalf("query item_types: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan item_type: %v", err)
		}
		ids = append(ids, id)
	}
	if len(ids) < 2 {
		t.Fatalf("need >=2 seeded item_types, got %d", len(ids))
	}
	return ids[0], ids[1]
}

// TestTemplateRepository_CRUDAndTypes exercises the migration (the tables must
// exist) plus create/get/list/update/delete with the item-type join.
func TestTemplateRepository_CRUDAndTypes(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	data := tdb.SeedTestData(t)
	typeA, typeB := twoItemTypeIDs(t, tdb)
	repo := NewTemplateRepository(tdb.GetDatabase())
	uid := data.UserID

	// Global selectable (no target types).
	global, err := repo.Create(&models.ItemTemplate{
		WorkspaceID:     data.WorkspaceID,
		Name:            "global",
		DescriptionBody: "## Global body",
		Mode:            models.TemplateModeSelectable,
		IsActive:        true,
		CreatedBy:       &uid,
	})
	if err != nil {
		t.Fatalf("create global: %v", err)
	}
	if len(global.ItemTypeIDs) != 0 {
		t.Fatalf("expected no target types, got %v", global.ItemTypeIDs)
	}

	// Typed selectable targeting typeA.
	typed, err := repo.Create(&models.ItemTemplate{
		WorkspaceID:     data.WorkspaceID,
		Name:            "typed-a",
		DescriptionBody: "typed body",
		Mode:            models.TemplateModeSelectable,
		IsActive:        true,
		ItemTypeIDs:     []int{typeA},
	})
	if err != nil {
		t.Fatalf("create typed: %v", err)
	}

	// ListForType(typeA) -> global + typed-a; ListForType(typeB) -> global only.
	forA, err := repo.ListForType(data.WorkspaceID, typeA)
	if err != nil {
		t.Fatalf("list for typeA: %v", err)
	}
	if len(forA) != 2 {
		t.Fatalf("expected 2 templates for typeA, got %d", len(forA))
	}
	forB, err := repo.ListForType(data.WorkspaceID, typeB)
	if err != nil {
		t.Fatalf("list for typeB: %v", err)
	}
	if len(forB) != 1 || forB[0].ID != global.ID {
		t.Fatalf("expected only global for typeB, got %+v", forB)
	}

	// GetByID round-trips the body and types.
	got, err := repo.GetByID(typed.ID)
	if err != nil {
		t.Fatalf("get typed: %v", err)
	}
	if got.DescriptionBody != "typed body" || len(got.ItemTypeIDs) != 1 || got.ItemTypeIDs[0] != typeA {
		t.Fatalf("get typed mismatch: %+v", got)
	}

	// Update: rename + retarget to typeB.
	got.Name = "typed-renamed"
	got.ItemTypeIDs = []int{typeB}
	got.UpdatedBy = &uid
	if err := repo.Update(got); err != nil {
		t.Fatalf("update: %v", err)
	}
	reloaded, _ := repo.GetByID(typed.ID)
	if reloaded.Name != "typed-renamed" || reloaded.ItemTypeIDs[0] != typeB {
		t.Fatalf("update not persisted: %+v", reloaded)
	}

	// Name uniqueness per workspace.
	exists, err := repo.NameExistsInWorkspace(data.WorkspaceID, "global", 0)
	if err != nil || !exists {
		t.Fatalf("expected global name to exist (err=%v exists=%v)", err, exists)
	}

	// Delete cascades the join rows.
	if err := repo.Delete(typed.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.GetByID(typed.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

// TestTemplateRepository_MandatoryInvariants covers the exactly-one-type and
// at-most-one-active-mandatory-per-type rules plus GetMandatoryForType.
func TestTemplateRepository_MandatoryInvariants(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	data := tdb.SeedTestData(t)
	typeA, typeB := twoItemTypeIDs(t, tdb)
	repo := NewTemplateRepository(tdb.GetDatabase())

	// Mandatory with zero types is rejected.
	if _, err := repo.Create(&models.ItemTemplate{
		WorkspaceID: data.WorkspaceID, Name: "bad-mandatory", Mode: models.TemplateModeMandatory, IsActive: true,
	}); !errors.Is(err, ErrMandatoryRequiresOneType) {
		t.Fatalf("expected ErrMandatoryRequiresOneType, got %v", err)
	}

	// Mandatory with two types is rejected.
	if _, err := repo.Create(&models.ItemTemplate{
		WorkspaceID: data.WorkspaceID, Name: "bad-mandatory2", Mode: models.TemplateModeMandatory, IsActive: true,
		ItemTypeIDs: []int{typeA, typeB},
	}); !errors.Is(err, ErrMandatoryRequiresOneType) {
		t.Fatalf("expected ErrMandatoryRequiresOneType for two types, got %v", err)
	}

	// Valid mandatory for typeA.
	mand, err := repo.Create(&models.ItemTemplate{
		WorkspaceID: data.WorkspaceID, Name: "mandatory-a", DescriptionBody: "must fill", Mode: models.TemplateModeMandatory, IsActive: true,
		ItemTypeIDs: []int{typeA},
	})
	if err != nil {
		t.Fatalf("create mandatory: %v", err)
	}

	// Lookup returns it.
	found, err := repo.GetMandatoryForType(data.WorkspaceID, typeA)
	if err != nil {
		t.Fatalf("GetMandatoryForType: %v", err)
	}
	if found.ID != mand.ID || found.DescriptionBody != "must fill" {
		t.Fatalf("mandatory lookup mismatch: %+v", found)
	}

	// No mandatory for typeB.
	if _, err := repo.GetMandatoryForType(data.WorkspaceID, typeB); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for typeB mandatory, got %v", err)
	}

	// A second active mandatory for typeA conflicts.
	if _, err := repo.Create(&models.ItemTemplate{
		WorkspaceID: data.WorkspaceID, Name: "mandatory-a2", Mode: models.TemplateModeMandatory, IsActive: true,
		ItemTypeIDs: []int{typeA},
	}); !errors.Is(err, ErrMandatoryConflict) {
		t.Fatalf("expected ErrMandatoryConflict, got %v", err)
	}

	// An inactive mandatory draft for typeA is allowed (no conflict).
	if _, err := repo.Create(&models.ItemTemplate{
		WorkspaceID: data.WorkspaceID, Name: "mandatory-a-draft", Mode: models.TemplateModeMandatory, IsActive: false,
		ItemTypeIDs: []int{typeA},
	}); err != nil {
		t.Fatalf("inactive mandatory draft should be allowed: %v", err)
	}
}
