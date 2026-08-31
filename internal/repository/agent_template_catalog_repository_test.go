package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

func openAgentTemplateCatalogTestDB(t *testing.T) *testutils.TestDB {
	t.Helper()
	return testutils.CreateTestDB(t, true)
}

func TestAgentTemplateCatalogRepositoryCRUD(t *testing.T) {
	ctx := context.Background()
	db := openAgentTemplateCatalogTestDB(t)
	repo := NewAgentTemplateCatalogRepository(db.GetDatabase())

	created, err := repo.Create(ctx, &models.AgentTemplateCatalogEntry{
		TemplateKey:  "software_engineer",
		Name:         "Software Engineer v2",
		DefaultType:  models.AgentProfileCoding,
		Instructions: "overridden instructions",
		Enabled:      true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("expected non-zero id")
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("create returned zero timestamps: %+v", created)
	}

	got, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Software Engineer v2" || got.TemplateKey != "software_engineer" {
		t.Fatalf("got %+v, want persisted override", got)
	}

	byKey, err := repo.GetByKey(ctx, "software_engineer")
	if err != nil {
		t.Fatalf("get by key: %v", err)
	}
	if byKey.ID != created.ID {
		t.Fatalf("get by key returned id %d, want %d", byKey.ID, created.ID)
	}

	// Force a historical timestamp so the update assertion does not depend on
	// sleeps or database timestamp precision.
	historical := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	if _, err := db.ExecWrite("UPDATE agent_template_catalog SET updated_at = ? WHERE id = ?", historical, created.ID); err != nil {
		t.Fatalf("set historical updated_at: %v", err)
	}
	got, err = repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get after timestamp setup: %v", err)
	}

	// Update flips name/type/instructions and disables the row.
	got.Name = "Disabled Review"
	got.Enabled = false
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	if !got.UpdatedAt.After(historical) {
		t.Fatalf("updated_at = %v, want after %v", got.UpdatedAt, historical)
	}
	updated, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if updated.Enabled || updated.Name != "Disabled Review" {
		t.Fatalf("update did not persist: %+v", updated)
	}
	if !updated.UpdatedAt.Equal(got.UpdatedAt) {
		t.Fatalf("persisted updated_at = %v, returned %v", updated.UpdatedAt, got.UpdatedAt)
	}

	// A duplicate template key is rejected.
	if _, err := repo.Create(ctx, &models.AgentTemplateCatalogEntry{
		TemplateKey: "software_engineer",
		Name:        "dup",
		Enabled:     true,
	}); !errors.Is(err, ErrDuplicateEntry) {
		t.Fatalf("duplicate create err = %v, want ErrDuplicateEntry", err)
	}

	if err := repo.Delete(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.Get(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete err = %v, want ErrNotFound", err)
	}
}

func TestAgentTemplateCatalogRepositoryListEnabledExcludesDisabled(t *testing.T) {
	ctx := context.Background()
	db := openAgentTemplateCatalogTestDB(t)
	repo := NewAgentTemplateCatalogRepository(db.GetDatabase())

	for _, e := range []*models.AgentTemplateCatalogEntry{
		{TemplateKey: "workspace_guide", Name: "Guide", Enabled: true},
		{TemplateKey: "blank", Name: "Blank", Enabled: false},
	} {
		if _, err := repo.Create(ctx, e); err != nil {
			t.Fatalf("create %s: %v", e.TemplateKey, err)
		}
	}

	enabled, err := repo.ListEnabled(ctx)
	if err != nil {
		t.Fatalf("list enabled: %v", err)
	}
	if len(enabled) != 1 || enabled[0].TemplateKey != "workspace_guide" {
		t.Fatalf("ListEnabled = %+v, want only workspace_guide", enabled)
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List = %d rows, want 2", len(all))
	}
}
