package repository

import (
	"encoding/json"
	"reflect"
	"testing"

	"windshift/internal/testutils"
)

func TestCompareAndSwapRowCustomFieldsPreservesConcurrentEdit(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()

	var workspaceID, itemID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('CFV CAS', 'CAS') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index, custom_field_values)
		VALUES (?, 1, 'Concurrent cleanup', '', 'a', '{"91":"remove","7":"old"}') RETURNING id
	`, workspaceID).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}

	repo := NewCustomFieldRepository(db)
	rows, err := repo.ListRowsWithCustomFieldsPageByKey("items", 0, "91", 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("load cleanup row: rows=%v err=%v", rows, err)
	}
	stale := rows[0].Value
	concurrent := `{"91":"remove","7":"new","8":"added"}`
	if _, err := db.ExecWrite(`UPDATE items SET custom_field_values = ? WHERE id = ?`, concurrent, itemID); err != nil {
		t.Fatalf("apply concurrent edit: %v", err)
	}

	swapped, err := repo.CompareAndSwapRowCustomFields("items", itemID, stale, `{"7":"old"}`)
	if err != nil {
		t.Fatalf("stale compare and swap: %v", err)
	}
	if swapped {
		t.Fatal("stale compare and swap succeeded, want concurrent edit preserved")
	}
	assertRowCustomFields(t, repo, itemID, map[string]any{"91": "remove", "7": "new", "8": "added"})

	current, found, err := repo.FindRowCustomFields("items", itemID)
	if err != nil || !found {
		t.Fatalf("reload current custom fields: found=%v err=%v", found, err)
	}
	swapped, err = repo.CompareAndSwapRowCustomFields("items", itemID, current, `{"7":"new","8":"added"}`)
	if err != nil || !swapped {
		t.Fatalf("current compare and swap: swapped=%v err=%v", swapped, err)
	}
	assertRowCustomFields(t, repo, itemID, map[string]any{"7": "new", "8": "added"})
}

func assertRowCustomFields(t *testing.T, repo *CustomFieldRepository, itemID int, want map[string]any) {
	t.Helper()
	raw, found, err := repo.FindRowCustomFields("items", itemID)
	if err != nil || !found {
		t.Fatalf("find item custom fields: found=%v err=%v", found, err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode item custom fields: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("custom fields = %v, want %v", got, want)
	}
}
