//go:build test

package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"windshift/internal/testutils"
)

// linkedProjectionFixture creates two workspaces, an item in each, one custom
// field, and one asset linked to both items (once as the link source and once
// as the link target, so both directions are covered).
type linkedProjectionFixture struct {
	repo       *ItemLinkRepository
	assetID    int
	itemInA    int
	itemInB    int
	workspaceA int
	workspaceB int
}

func newLinkedProjectionFixture(t *testing.T) linkedProjectionFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	createUser := func(email string) int {
		t.Helper()
		var id int
		if err := db.QueryRow(`INSERT INTO users (email, username, first_name, last_name) VALUES (?, ?, 'Test', 'User') RETURNING id`,
			email, strings.Split(email, "@")[0]).Scan(&id); err != nil {
			t.Fatalf("create user %s: %v", email, err)
		}
		return id
	}
	createWorkspace := func(name, key string) int {
		t.Helper()
		var id int
		if err := db.QueryRow(`INSERT INTO workspaces (name, key, active) VALUES (?, ?, TRUE) RETURNING id`, name, key).Scan(&id); err != nil {
			t.Fatalf("create workspace %s: %v", key, err)
		}
		return id
	}
	createItem := func(workspaceID int, title string) int {
		t.Helper()
		var id int
		if err := db.QueryRow(`
			INSERT INTO items (workspace_id, workspace_item_number, title, frac_index)
			VALUES (?, ?, ?, ?) RETURNING id
		`, workspaceID, 1, title, fmt.Sprintf("a%d", workspaceID)).Scan(&id); err != nil {
			t.Fatalf("create item %s: %v", title, err)
		}
		return id
	}

	createUser("projection@example.test")
	workspaceA := createWorkspace("Projection A", "PRA")
	workspaceB := createWorkspace("Projection B", "PRB")
	itemInA := createItem(workspaceA, "Alpha")
	itemInB := createItem(workspaceB, "Beta")

	var linkTypeID int
	if err := db.QueryRow(`SELECT id FROM link_types ORDER BY id LIMIT 1`).Scan(&linkTypeID); err != nil {
		t.Fatalf("load link type: %v", err)
	}

	const assetID = 9001
	insertLink := func(sourceType string, sourceID int, targetType string, targetID int) {
		t.Helper()
		if _, err := db.ExecWrite(`
			INSERT INTO item_links (link_type_id, source_type, source_id, target_type, target_id)
			VALUES (?, ?, ?, ?, ?)
		`, linkTypeID, sourceType, sourceID, targetType, targetID); err != nil {
			t.Fatalf("insert link %s/%d -> %s/%d: %v", sourceType, sourceID, targetType, targetID, err)
		}
	}
	insertLink("asset", assetID, "item", itemInA)
	insertLink("item", itemInB, "asset", assetID)

	// A custom field on the item in workspace A, keyed by field ID the way the
	// application stores it.
	var fieldID int
	if err := db.QueryRow(`INSERT INTO custom_field_definitions (name, field_type) VALUES ('Application', 'text') RETURNING id`).Scan(&fieldID); err != nil {
		t.Fatalf("create custom field: %v", err)
	}
	if _, err := db.ExecWrite(`UPDATE items SET custom_field_values = ? WHERE id = ?`,
		fmt.Sprintf(`{"%d":"Windows"}`, fieldID), itemInA); err != nil {
		t.Fatalf("set custom field value: %v", err)
	}

	return linkedProjectionFixture{
		repo:       NewItemLinkRepository(db),
		assetID:    assetID,
		itemInA:    itemInA,
		itemInB:    itemInB,
		workspaceA: workspaceA,
		workspaceB: workspaceB,
	}
}

func TestProjectLinkedItems(t *testing.T) {
	fixture := newLinkedProjectionFixture(t)
	ctx := context.Background()
	fields := []string{"id", "key", "title", "status", "workspace", "creator"}

	t.Run("both link directions resolve", func(t *testing.T) {
		links, truncated, err := fixture.repo.ProjectLinkedItems(ctx, []int{fixture.assetID}, []int{fixture.workspaceA, fixture.workspaceB}, fields)
		if err != nil {
			t.Fatalf("ProjectLinkedItems: %v", err)
		}
		if truncated {
			t.Fatal("truncated = true, want false")
		}
		rows := links[fixture.assetID]
		if len(rows) != 2 {
			t.Fatalf("links = %+v, want the item from each direction", rows)
		}
		titles := map[string]bool{}
		for _, row := range rows {
			titles[fmt.Sprint(row["title"])] = true
			if row["key"] == "" || row["key"] == nil {
				t.Fatalf("row %+v has no display key", row)
			}
		}
		if !titles["Alpha"] || !titles["Beta"] {
			t.Fatalf("titles = %v, want both Alpha and Beta", titles)
		}
	})

	t.Run("items outside the accessible workspaces are invisible", func(t *testing.T) {
		links, _, err := fixture.repo.ProjectLinkedItems(ctx, []int{fixture.assetID}, []int{fixture.workspaceA}, fields)
		if err != nil {
			t.Fatalf("ProjectLinkedItems: %v", err)
		}
		rows := links[fixture.assetID]
		if len(rows) != 1 || rows[0]["title"] != "Alpha" {
			t.Fatalf("links = %+v, want only the item in the accessible workspace", rows)
		}
	})

	t.Run("an empty workspace set yields no links", func(t *testing.T) {
		links, _, err := fixture.repo.ProjectLinkedItems(ctx, []int{fixture.assetID}, nil, fields)
		if err != nil {
			t.Fatalf("ProjectLinkedItems: %v", err)
		}
		if len(links[fixture.assetID]) != 0 {
			t.Fatalf("links = %+v, want none", links[fixture.assetID])
		}
	})

	t.Run("a custom field projects by name", func(t *testing.T) {
		links, _, err := fixture.repo.ProjectLinkedItems(ctx, []int{fixture.assetID}, []int{fixture.workspaceA}, []string{"id", "cf_application"})
		if err != nil {
			t.Fatalf("ProjectLinkedItems: %v", err)
		}
		rows := links[fixture.assetID]
		if len(rows) != 1 {
			t.Fatalf("links = %+v, want one", rows)
		}
		if rows[0]["cf_application"] != "Windows" {
			t.Fatalf("cf_application = %v, want Windows", rows[0]["cf_application"])
		}
		// Only the requested fields are projected.
		if _, present := rows[0]["title"]; present {
			t.Fatalf("row %+v projected a field that was not requested", rows[0])
		}
	})

	t.Run("caps links per asset and reports truncation", func(t *testing.T) {
		tdb := testutils.CreateTestDB(t, true)
		t.Cleanup(func() { _ = tdb.Close() })
		db := tdb.GetDatabase()
		var workspaceID int
		if err := db.QueryRow(`INSERT INTO workspaces (name, key, active) VALUES ('Projection Many', 'PRM', TRUE) RETURNING id`).Scan(&workspaceID); err != nil {
			t.Fatalf("create workspace: %v", err)
		}

		var linkTypeID int
		if err := db.QueryRow(`SELECT id FROM link_types ORDER BY id LIMIT 1`).Scan(&linkTypeID); err != nil {
			t.Fatalf("load link type: %v", err)
		}
		const assetID = 9100
		for i := 0; i < MaxProjectedLinksPerAsset+3; i++ {
			var itemID int
			if err := db.QueryRow(`
				INSERT INTO items (workspace_id, workspace_item_number, title, frac_index)
				VALUES (?, ?, ?, ?) RETURNING id
			`, workspaceID, i+1, fmt.Sprintf("Many %d", i), fmt.Sprintf("m%d", i)).Scan(&itemID); err != nil {
				t.Fatalf("create item: %v", err)
			}
			if _, err := db.ExecWrite(`INSERT INTO item_links (link_type_id, source_type, source_id, target_type, target_id) VALUES (?, 'asset', ?, 'item', ?)`,
				linkTypeID, assetID, itemID); err != nil {
				t.Fatalf("insert link: %v", err)
			}
		}

		links, truncated, err := NewItemLinkRepository(db).ProjectLinkedItems(ctx, []int{assetID}, []int{workspaceID}, []string{"id"})
		if err != nil {
			t.Fatalf("ProjectLinkedItems: %v", err)
		}
		if !truncated {
			t.Fatal("truncated = false, want true once the per-asset cap is exceeded")
		}
		if len(links[assetID]) != MaxProjectedLinksPerAsset {
			t.Fatalf("links = %d, want the cap %d", len(links[assetID]), MaxProjectedLinksPerAsset)
		}
	})
}
