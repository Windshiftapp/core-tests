package repository

import (
	"errors"
	"path/filepath"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
)

func TestCollectionRepositoryCookieAuthLifecycleAndVisibility(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "collections.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	ownerID := insertTestRow(t, db, `
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('owner@example.test', 'collection-owner', 'Collection', 'Owner')
	`)
	otherID := insertTestRow(t, db, `
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('other@example.test', 'collection-other', 'Collection', 'Other')
	`)
	workspaceID := insertTestRow(t, db, `INSERT INTO workspaces (name, key) VALUES ('Collection workspace', 'COL')`)
	categoryID := insertTestRow(t, db, `INSERT INTO collection_categories (name, color) VALUES ('Reports', '#654321')`)
	filterState := `{"mode":"all"}`
	privateCollection := models.Collection{
		Name:        "Private collection",
		Description: "Owner only",
		QLQuery:     "status = Open",
		FilterState: &filterState,
		WorkspaceID: &workspaceID,
	}

	repo := NewCollectionRepository(db)
	if err := repo.Create(&privateCollection, ownerID); err != nil {
		t.Fatalf("Create private: %v", err)
	}
	publicSlug := "public-collection"
	publicCollection := models.Collection{
		Name:       "Public collection",
		IsPublic:   true,
		CategoryID: &categoryID,
		PublicSlug: &publicSlug,
	}
	if err := repo.Create(&publicCollection, otherID); err != nil {
		t.Fatalf("Create public: %v", err)
	}

	visible, err := repo.ListVisibleModels(CollectionListFilter{UserID: ownerID})
	if err != nil {
		t.Fatalf("ListVisibleModels: %v", err)
	}
	if len(visible) != 2 {
		t.Fatalf("owner-visible collections = %+v", visible)
	}
	visible, err = repo.ListVisibleModels(CollectionListFilter{UserID: otherID, CategoryID: &categoryID})
	if err != nil {
		t.Fatalf("ListVisibleModels category: %v", err)
	}
	if len(visible) != 1 || visible[0].ID != publicCollection.ID || visible[0].CategoryName != "Reports" {
		t.Fatalf("category-filtered collections = %+v", visible)
	}
	if _, err := repo.GetVisibleModel(privateCollection.ID, otherID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("private visibility error = %v, want ErrNotFound", err)
	}

	owner, err := repo.GetOwnerID(privateCollection.ID)
	if err != nil || owner == nil || *owner != ownerID {
		t.Fatalf("GetOwnerID = %v, %v", owner, err)
	}
	exists, err := repo.CategoryExists(categoryID)
	if err != nil || !exists {
		t.Fatalf("CategoryExists = %v, %v", exists, err)
	}

	privateCollection.Name = "Updated collection"
	privateCollection.IsPublic = true
	privateCollection.PublicSlug = &publicSlug
	if err := repo.Update(privateCollection.ID, &privateCollection); err == nil {
		t.Fatal("Update unexpectedly accepted duplicate public slug")
	}
	secondSlug := "updated-collection"
	privateCollection.PublicSlug = &secondSlug
	if err := repo.Update(privateCollection.ID, &privateCollection); err != nil {
		t.Fatalf("Update: %v", err)
	}
	updated, err := repo.GetModel(privateCollection.ID)
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if updated.Name != privateCollection.Name || updated.PublicSlug == nil || *updated.PublicSlug != secondSlug {
		t.Fatalf("updated collection = %+v", updated)
	}

	if err := repo.UpdatePublicSharing(privateCollection.ID, false, nil); err != nil {
		t.Fatalf("UpdatePublicSharing: %v", err)
	}
	if err := repo.Delete(privateCollection.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetModel(privateCollection.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetModel after delete error = %v, want ErrNotFound", err)
	}
}
