package repository

import (
	"path/filepath"
	"testing"

	"windshift/internal/database"
)

func TestStatusRepositoryListIncludesCategoryCompletion(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "statuses.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	statuses, err := NewStatusRepository(db).List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, status := range statuses {
		if status.Name == "Done" {
			if !status.IsCompleted {
				t.Fatal("Done status IsCompleted = false, want true")
			}
			return
		}
	}
	t.Fatal("Done status not found")
}
