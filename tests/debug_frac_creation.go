package tests

import (
	"fmt"
	"testing"

	"windshift/internal/database"
	"windshift/internal/repository"
)

func TestDebugFracCreation(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())

	dbWrapper, err := database.NewSQLiteDB(server.DBPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer dbWrapper.Close()
	db := dbWrapper.GetDB() // raw *sql.DB for direct queries

	// Create workspace
	result, _ := db.Exec("INSERT INTO workspaces (name, key) VALUES (?, ?)", "Test", "TEST")
	wsID, _ := result.LastInsertId()
	workspaceID := int(wsID)

	// Create 10 items and check for duplicates. Each iteration runs the
	// production-shape atomic tx: read MAX(frac_index), KeyBetween, INSERT, commit.
	for i := 0; i < 10; i++ {
		tx, err := dbWrapper.Begin()
		if err != nil {
			t.Fatalf("Failed to begin tx: %v", err)
		}
		fracIndex, err := repository.GenerateFracIndexForNewItem(tx)
		if err != nil {
			_ = tx.Rollback()
			t.Fatalf("Failed to generate frac_index: %v", err)
		}
		if _, err := tx.Exec("INSERT INTO items (workspace_id, workspace_item_number, title, status, priority, frac_index) VALUES (?, ?, ?, ?, ?, ?)",
			workspaceID, i+1, fmt.Sprintf("Item %d", i+1), "open", "medium", fracIndex); err != nil {
			_ = tx.Rollback()
			t.Fatalf("Failed to insert item: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Failed to commit: %v", err)
		}
		fmt.Printf("Generated: %s\n", fracIndex)

		// Verify it was inserted
		var count int
		_ = db.QueryRow("SELECT COUNT(*) FROM items WHERE frac_index = ?", fracIndex).Scan(&count)
		fmt.Printf("  Count in DB: %d\n", count)
	}

	// Check for duplicates
	rows, _ := db.Query("SELECT frac_index, COUNT(*) as cnt FROM items WHERE workspace_id = ? GROUP BY frac_index HAVING cnt > 1", workspaceID)
	defer rows.Close()

	hasDuplicates := false
	for rows.Next() {
		var fracIndex string
		var count int
		_ = rows.Scan(&fracIndex, &count)
		fmt.Printf("DUPLICATE: %s appears %d times\n", fracIndex, count)
		hasDuplicates = true
	}

	if hasDuplicates {
		t.Fatal("Found duplicate frac_index values!")
	}
}
