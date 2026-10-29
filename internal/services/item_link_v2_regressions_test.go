//go:build test

package services

import (
	"reflect"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

func TestBatchLinksFiltersTestPermissionsBeforePagination(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	visible := batchRegressionWorkspace(t, db, "Visible", "BLV")
	hidden := batchRegressionWorkspace(t, db, "Hidden", "BLH")
	anchor := insertBatchItem(t, db, visible, "Anchor")
	linkType := firstOneHopLinkTypeID(t, db)
	var wanted []int
	for i, workspace := range []int{hidden, hidden, hidden, visible, visible} {
		caseID := batchRegressionInsert(t, db, `INSERT INTO test_cases (workspace_id, title, priority, status) VALUES (?, 'Case', 'medium', 'active')`, workspace)
		id := batchRegressionInsert(t, db, `INSERT INTO item_links (link_type_id, source_type, source_id, target_type, target_id) VALUES (?, 'item', ?, 'test_case', ?)`, linkType, anchor, caseID)
		if i >= 3 {
			wanted = append(wanted, id)
		}
	}
	service := NewItemLinkService(db).WithPermissionService(&oneHopWorkspacePermissions{workspaceIDs: []int{visible}})
	first, err := service.ListOneHopItemLinksPageWithChecks(t.Context(), 1, []int{anchor}, 0, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := linkIDsForTest(first[anchor].Outgoing); !reflect.DeepEqual(got, wanted[:1]) {
		t.Fatalf("first visible links = %v, want %v", got, wanted[:1])
	}
	if !first[anchor].HasMore || first[anchor].NextAfterID != wanted[0] {
		t.Fatalf("first page = %+v", first[anchor])
	}
	second, err := service.ListOneHopItemLinksPageWithChecks(t.Context(), 1, []int{anchor}, first[anchor].NextAfterID, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := linkIDsForTest(second[anchor].Outgoing); !reflect.DeepEqual(got, wanted[1:]) || second[anchor].HasMore {
		t.Fatalf("second page = %+v, want only %v and no continuation", second[anchor], wanted[1:])
	}
}

func TestBatchLinksHidesDeniedNonItemEndpoints(t *testing.T) {
	for _, entity := range []string{"test_case", "asset", "page"} {
		t.Run(entity, func(t *testing.T) {
			db := testutils.CreateTestDB(t, true)
			workspace := batchRegressionWorkspace(t, db, "Visible", "BND")
			anchor := insertBatchItem(t, db, workspace, "Anchor")
			linkType := firstOneHopLinkTypeID(t, db)
			var endpoint int
			switch entity {
			case "test_case":
				hidden := batchRegressionWorkspace(t, db, "Hidden", "BHD")
				endpoint = batchRegressionInsert(t, db, `INSERT INTO test_cases (workspace_id, title, priority, status) VALUES (?, 'Restricted case', 'medium', 'active')`, hidden)
			case "asset":
				setID := batchRegressionInsert(t, db, `INSERT INTO asset_management_sets (name) VALUES ('Restricted set')`)
				typeID := batchRegressionInsert(t, db, `INSERT INTO asset_types (set_id, name) VALUES (?, 'Type')`, setID)
				endpoint = batchRegressionInsert(t, db, `INSERT INTO assets (set_id, asset_type_id, title) VALUES (?, ?, 'Restricted asset')`, setID, typeID)
			case "page":
				userID := batchRegressionInsert(t, db, `INSERT INTO users (email, username, first_name, last_name) VALUES ('author@example.com', 'author', 'Page', 'Author')`)
				endpoint = batchRegressionInsert(t, db, `INSERT INTO pages (workspace_id, title, slug, created_by) VALUES (?, 'Restricted page', 'restricted', ?)`, workspace, userID)
			}
			for _, incoming := range []bool{false, true} {
				sourceType, targetType, sourceID, targetID := "item", entity, anchor, endpoint
				if incoming {
					sourceType, targetType, sourceID, targetID = entity, "item", endpoint, anchor
				}
				batchRegressionInsert(t, db, `INSERT INTO item_links (link_type_id, source_type, source_id, target_type, target_id) VALUES (?, ?, ?, ?, ?)`, linkType, sourceType, sourceID, targetType, targetID)
			}
			service := NewItemLinkService(db).WithPermissionService(&oneHopWorkspacePermissions{workspaceIDs: []int{workspace}})
			groups, err := service.ListOneHopItemLinksPageWithChecks(t.Context(), 1, []int{anchor}, 0, 1, false)
			if err != nil {
				t.Fatal(err)
			}
			want := OneHopItemLinksPage{Outgoing: []models.ItemLink{}, Incoming: []models.ItemLink{}}
			if !reflect.DeepEqual(groups[anchor], want) {
				t.Fatalf("denied page = %+v, want %+v", groups[anchor], want)
			}
		})
	}
}

func TestBatchLinksEmptyQueryPageSucceeds(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	workspace := batchRegressionWorkspace(t, db, "Query", "BEQ")
	insertBatchItem(t, db, workspace, "Match")
	service := NewItemLinkService(db).WithPermissionService(&oneHopWorkspacePermissions{workspaceIDs: []int{workspace}})
	for _, test := range []struct {
		name, query   string
		offset, total int
	}{
		{"no matches", `title = "Absent"`, 0, 0},
		{"past end", `title = "Match"`, 10, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			groups, total, err := service.ListBatch(t.Context(), 1, BatchLinkParams{QLQuery: test.query, Page: PaginationParams{Limit: 10, Offset: test.offset}})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(groups, []BatchItemLinks{}) || total != test.total {
				t.Fatalf("groups=%+v total=%d, want [] and %d", groups, total, test.total)
			}
		})
	}
}

func batchRegressionWorkspace(t *testing.T, db database.Database, name, key string) int {
	t.Helper()
	return batchRegressionInsert(t, db, "INSERT INTO workspaces (name, key, active) VALUES (?, ?, true)", name, key)
}

func batchRegressionInsert(t *testing.T, db database.Database, query string, args ...any) int {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var id int
	if err := tx.QueryRow(query+" RETURNING id", args...).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return id
}
