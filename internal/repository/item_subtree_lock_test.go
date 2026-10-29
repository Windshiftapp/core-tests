//go:build test

package repository

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/lib/pq"
	"windshift/internal/testutils"
)

func TestItemSubtreeLocksEveryGeneration(t *testing.T) {
	tdb := testutils.CreateTestDB(t, false)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	// Repository fixtures deliberately bypass hierarchy validation to cover trees
	// deeper than the read API's traversal cap.
	ids := make([]int, 0, 36)
	var parent *int
	for i := 0; i < 36; i++ {
		id := testutils.InsertID(t, db, `INSERT INTO items (workspace_id,workspace_item_number,title,description,parent_id,frac_index) VALUES (?, ?, ?, '', ?, ?)`, data.WorkspaceID, 1000+i, "Subtree lock", parent, testutils.NextTestFracIndex())
		ids = append(ids, id)
		parent = &id
	}
	outsider := testutils.InsertID(t, db, `INSERT INTO items (workspace_id,workspace_item_number,title,description,frac_index) VALUES (?, ?, ?, '', ?)`, data.WorkspaceID, 2000, "Unrelated", testutils.NextTestFracIndex())
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	items, err := NewItemRepository(db).FindSubtreeForUpdateContext(context.Background(), tx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	got := make([]int, 0, len(items))
	for _, item := range items {
		got = append(got, item.ID)
	}
	if !reflect.DeepEqual(got, ids) {
		t.Fatalf("subtree=%v want=%v", got, ids)
	}
	if db.GetDriverName() != "postgres" {
		return
	}
	// FOR UPDATE prevents moves and workspace changes; FOR KEY SHARE is the
	// lock used by the parent foreign key when attaching a new descendant.
	for _, mode := range []string{"UPDATE", "KEY SHARE"} {
		for _, id := range ids {
			probe, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			var found int
			err = probe.QueryRow("SELECT id FROM items WHERE id = ? FOR "+mode+" NOWAIT", id).Scan(&found)
			_ = probe.Rollback()
			var locked *pq.Error
			if !errors.As(err, &locked) || locked.Code != "55P03" {
				t.Fatalf("item %d %s lock error=%v, want lock_not_available", id, mode, err)
			}
		}
	}
	probe, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Rollback()
	var found int
	if err := probe.QueryRow("SELECT id FROM items WHERE id = ? FOR UPDATE NOWAIT", outsider).Scan(&found); err != nil {
		t.Fatal(err)
	}
	if found != outsider {
		t.Fatalf("unrelated row=%d, want %d", found, outsider)
	}
}
