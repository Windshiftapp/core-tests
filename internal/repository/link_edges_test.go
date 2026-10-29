//go:build test

package repository

import (
	"context"
	"fmt"
	"testing"

	"windshift/internal/testutils"
)

// edgeFixture builds two items, two assets, and links in both directions.
type edgeFixture struct {
	repo    *ItemLinkRepository
	itemOne int
	itemTwo int
	assetA  int
	assetB  int
	items   *ItemRepository
}

func newEdgeFixture(t *testing.T) edgeFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	var userID int
	if err := db.QueryRow(`INSERT INTO users (email, username, first_name, last_name) VALUES ('edges@example.test', 'edges', 'Test', 'User') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	var workspaceID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key, active) VALUES ('Edges', 'EDG', TRUE) RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	createItem := func(title string, number int) int {
		t.Helper()
		var id int
		if err := db.QueryRow(`
			INSERT INTO items (workspace_id, workspace_item_number, title, frac_index)
			VALUES (?, ?, ?, ?) RETURNING id
		`, workspaceID, number, title, fmt.Sprintf("e%d", number)).Scan(&id); err != nil {
			t.Fatalf("create item %s: %v", title, err)
		}
		return id
	}

	var linkTypeID int
	if err := db.QueryRow(`SELECT id FROM link_types ORDER BY id LIMIT 1`).Scan(&linkTypeID); err != nil {
		t.Fatalf("load link type: %v", err)
	}

	itemOne := createItem("Item One", 1)
	itemTwo := createItem("Item Two", 2)
	const assetA, assetB = 7001, 7002

	// One link with the asset as source, one with the asset as target.
	if _, err := db.ExecWrite(`INSERT INTO item_links (link_type_id, source_type, source_id, target_type, target_id) VALUES (?, 'asset', ?, 'item', ?)`, linkTypeID, assetA, itemOne); err != nil {
		t.Fatalf("insert link A: %v", err)
	}
	if _, err := db.ExecWrite(`INSERT INTO item_links (link_type_id, source_type, source_id, target_type, target_id) VALUES (?, 'item', ?, 'asset', ?)`, linkTypeID, itemTwo, assetB); err != nil {
		t.Fatalf("insert link B: %v", err)
	}

	return edgeFixture{
		repo:    NewItemLinkRepository(db),
		itemOne: itemOne,
		itemTwo: itemTwo,
		assetA:  assetA,
		assetB:  assetB,
		items:   NewItemRepository(db),
	}
}

func TestListAssetItemLinks(t *testing.T) {
	fixture := newEdgeFixture(t)
	ctx := context.Background()

	t.Run("resolves both directions", func(t *testing.T) {
		edges, truncated, err := fixture.repo.ListAssetItemLinks(ctx, []int{fixture.itemOne, fixture.itemTwo}, []int{fixture.assetA, fixture.assetB}, 100)
		if err != nil {
			t.Fatalf("ListAssetItemLinks: %v", err)
		}
		if truncated {
			t.Fatal("truncated = true, want false")
		}
		if len(edges) != 2 {
			t.Fatalf("edges = %+v, want two", edges)
		}
		pairs := map[string]bool{}
		for _, edge := range edges {
			if edge.Kind != "link" {
				t.Fatalf("kind = %q, want link", edge.Kind)
			}
			pairs[fmt.Sprintf("%d-%d", edge.ItemID, edge.AssetID)] = true
		}
		if !pairs[fmt.Sprintf("%d-%d", fixture.itemOne, fixture.assetA)] || !pairs[fmt.Sprintf("%d-%d", fixture.itemTwo, fixture.assetB)] {
			t.Fatalf("pairs = %v, want both endpoints in both directions", pairs)
		}
	})

	t.Run("only links between the two seed sets are returned", func(t *testing.T) {
		edges, _, err := fixture.repo.ListAssetItemLinks(ctx, []int{fixture.itemOne}, []int{fixture.assetA, fixture.assetB}, 100)
		if err != nil {
			t.Fatalf("ListAssetItemLinks: %v", err)
		}
		if len(edges) != 1 || edges[0].ItemID != fixture.itemOne || edges[0].AssetID != fixture.assetA {
			t.Fatalf("edges = %+v, want only itemOne-assetA", edges)
		}
	})

	t.Run("an empty seed set yields no edges", func(t *testing.T) {
		edges, _, err := fixture.repo.ListAssetItemLinks(ctx, nil, []int{fixture.assetA}, 100)
		if err != nil {
			t.Fatalf("ListAssetItemLinks: %v", err)
		}
		if len(edges) != 0 {
			t.Fatalf("edges = %+v, want none", edges)
		}
	})

	t.Run("the edge cap reports truncation", func(t *testing.T) {
		edges, truncated, err := fixture.repo.ListAssetItemLinks(ctx, []int{fixture.itemOne, fixture.itemTwo}, []int{fixture.assetA, fixture.assetB}, 1)
		if err != nil {
			t.Fatalf("ListAssetItemLinks: %v", err)
		}
		if !truncated {
			t.Fatal("truncated = false, want true")
		}
		if len(edges) != 1 {
			t.Fatalf("edges = %d, want the cap of 1", len(edges))
		}
	})
}

func TestListItemAssetReferences(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	ctx := context.Background()

	if _, err := db.ExecWrite(`INSERT INTO users (email, username, first_name, last_name) VALUES ('refs@example.test', 'refs', 'Test', 'User')`); err != nil {
		t.Fatalf("create user: %v", err)
	}
	var workspaceID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key, active) VALUES ('Refs', 'REF', TRUE) RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	var assetFieldID int
	if err := db.QueryRow(`INSERT INTO custom_field_definitions (name, field_type) VALUES ('Application', 'asset') RETURNING id`).Scan(&assetFieldID); err != nil {
		t.Fatalf("create asset custom field: %v", err)
	}
	var textFieldID int
	if err := db.QueryRow(`INSERT INTO custom_field_definitions (name, field_type) VALUES ('Notes', 'text') RETURNING id`).Scan(&textFieldID); err != nil {
		t.Fatalf("create text custom field: %v", err)
	}

	createItem := func(number int, values string) int {
		t.Helper()
		var id int
		if err := db.QueryRow(`
			INSERT INTO items (workspace_id, workspace_item_number, title, frac_index, custom_field_values)
			VALUES (?, ?, ?, ?, ?) RETURNING id
		`, workspaceID, number, fmt.Sprintf("Ref %d", number), fmt.Sprintf("r%d", number), values).Scan(&id); err != nil {
			t.Fatalf("create item: %v", err)
		}
		return id
	}

	objectItem := createItem(1, fmt.Sprintf(`{"%d":{"id":9001,"title":"Laptop"}}`, assetFieldID))
	arrayItem := createItem(2, fmt.Sprintf(`{"%d":[{"id":9002,"title":"Phone"},{"id":9003,"title":"Tablet"}]}`, assetFieldID))
	scalarItem := createItem(3, fmt.Sprintf(`{"%d":9004}`, assetFieldID))
	noneItem := createItem(4, fmt.Sprintf(`{"%d":"not a reference"}`, textFieldID))

	references, err := NewItemRepository(db).ListItemAssetReferences(ctx, []int{objectItem, arrayItem, scalarItem, noneItem})
	if err != nil {
		t.Fatalf("ListItemAssetReferences: %v", err)
	}

	if got := references[objectItem]; len(got) != 1 || got[0] != 9001 {
		t.Fatalf("object item references = %v, want [9001]", got)
	}
	if got := references[arrayItem]; len(got) != 2 || got[0] != 9002 || got[1] != 9003 {
		t.Fatalf("array item references = %v, want [9002 9003]", got)
	}
	if got := references[scalarItem]; len(got) != 1 || got[0] != 9004 {
		t.Fatalf("scalar item references = %v, want [9004]", got)
	}
	if _, present := references[noneItem]; present {
		t.Fatalf("non-reference custom fields must not produce references: %v", references[noneItem])
	}
}
