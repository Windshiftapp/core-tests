package repository

import (
	"errors"
	"path/filepath"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
)

func TestTimeProjectRepositoryCookieAuthLifecycleAndFilters(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "time-projects.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	workspaceID := insertTestRow(t, db, `INSERT INTO workspaces (name, key) VALUES ('Time workspace', 'TIME')`)
	customerID := insertTestRow(t, db, `INSERT INTO customer_organisations (name) VALUES ('Time customer')`)
	categoryID := insertTestRow(t, db, `INSERT INTO time_project_categories (name, color) VALUES ('Delivery', '#123456')`)
	insertTestRow(t, db, `
		INSERT INTO workspace_time_project_categories (workspace_id, time_project_category_id)
		VALUES (?, ?)
	`, workspaceID, categoryID)

	project := models.TimeProject{
		CustomerID:  &customerID,
		CategoryID:  &categoryID,
		Name:        "Repository migration",
		Description: "Move handler SQL",
		Status:      "Active",
		Color:       "#abcdef",
		HourlyRate:  175,
		Settings:    map[string]interface{}{"max_hours": float64(12)},
	}
	repo := NewTimeProjectRepository(db)
	if err := repo.Create(&project); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if project.ID == 0 || project.CreatedAt.IsZero() || project.UpdatedAt.IsZero() {
		t.Fatalf("created project not populated: %+v", project)
	}

	insertTestRow(t, db, `
		INSERT INTO time_worklogs (project_id, customer_id, description, date, start_time, end_time, duration_minutes, created_at, updated_at)
		VALUES (?, ?, 'Migration work', 100, 100, 3700, 60, 100, 100)
	`, project.ID, customerID)

	detail, err := repo.GetDetail(project.ID)
	if err != nil {
		t.Fatalf("GetDetail: %v", err)
	}
	if detail.CustomerName != "Time customer" || detail.CategoryName != "Delivery" || detail.TotalHours == nil || *detail.TotalHours != 1 {
		t.Fatalf("joined detail = %+v", detail)
	}
	if detail.Settings["max_hours"] != float64(12) {
		t.Fatalf("settings = %#v", detail.Settings)
	}

	listed, err := repo.ListDetailsFiltered(TimeProjectListFilter{
		AccessibleIDs: []int{project.ID},
		CategoryIDs:   []int{categoryID},
		CustomerID:    &customerID,
		Status:        "Active",
	})
	if err != nil {
		t.Fatalf("ListDetailsFiltered: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != project.ID {
		t.Fatalf("filtered projects = %+v", listed)
	}
	listed, err = repo.ListDetailsFiltered(TimeProjectListFilter{AccessibleIDs: []int{}})
	if err != nil || len(listed) != 0 {
		t.Fatalf("empty access list = %+v, %v", listed, err)
	}

	categoryIDs, err := NewTimeProjectCategoryRepository(db).ListIDsForWorkspace(workspaceID)
	if err != nil {
		t.Fatalf("ListIDsForWorkspace: %v", err)
	}
	if len(categoryIDs) != 1 || categoryIDs[0] != categoryID {
		t.Fatalf("workspace category IDs = %v", categoryIDs)
	}

	project.Name = "Repository migration updated"
	project.Settings = nil
	if err := repo.Update(project.ID, &project); err != nil {
		t.Fatalf("Update: %v", err)
	}
	updated, err := repo.GetDetail(project.ID)
	if err != nil {
		t.Fatalf("GetDetail after update: %v", err)
	}
	if updated.Name != project.Name || updated.Settings != nil {
		t.Fatalf("updated project = %+v", updated)
	}

	if err := repo.Delete(project.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetDetail(project.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetDetail after delete error = %v, want ErrNotFound", err)
	}
}
