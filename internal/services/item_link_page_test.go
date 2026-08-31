package services

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
)

type oneHopWorkspacePermissions struct {
	workspaceIDs []int
}

func (p *oneHopWorkspacePermissions) HasWorkspacePermission(_ int, workspaceID int, _ string) (bool, error) {
	for _, allowedID := range p.workspaceIDs {
		if allowedID == workspaceID {
			return true, nil
		}
	}
	return false, nil
}

func (p *oneHopWorkspacePermissions) AccessibleWorkspaceIDs(int) ([]int, error) {
	return append([]int(nil), p.workspaceIDs...), nil
}

func (p *oneHopWorkspacePermissions) AccessibleWorkspaceIDKeys(int) ([]repository.IDKey, error) {
	return nil, nil
}

type countingOneHopReadDB struct {
	database.Database
	queries atomic.Int64
}

func (db *countingOneHopReadDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	db.queries.Add(1)
	return db.Database.QueryContext(ctx, query, args...)
}

func seedOneHopWorkspace(t *testing.T, db database.Database, name, key string) int {
	t.Helper()
	return insertItemLinkBatchRow(t, db,
		`INSERT INTO workspaces (name, key, description, active) VALUES (?, ?, '', true)`,
		name, key,
	)
}

func firstOneHopLinkTypeID(t *testing.T, db database.Database) int {
	t.Helper()
	var linkTypeID int
	if err := db.QueryRow(`SELECT id FROM link_types ORDER BY id LIMIT 1`).Scan(&linkTypeID); err != nil {
		t.Fatalf("load seeded link type: %v", err)
	}
	return linkTypeID
}

func seedOneHopItemLink(t *testing.T, db database.Database, linkTypeID, sourceID, targetID int, customFieldID *int) int {
	t.Helper()
	return insertItemLinkBatchRow(t, db, `
		INSERT INTO item_links (link_type_id, source_type, source_id, target_type, target_id, custom_field_id)
		VALUES (?, 'item', ?, 'item', ?, ?)
	`, linkTypeID, sourceID, targetID, customFieldID)
}

func TestListOneHopItemLinksPageGroupsAnchorsWithoutNPlusOne(t *testing.T) {
	db := newItemLinkBatchTestDB(t)
	workspaceID := seedOneHopWorkspace(t, db, "One-hop grouping", "OHG")
	itemA := insertBatchItem(t, db, workspaceID, "Item A")
	itemB := insertBatchItem(t, db, workspaceID, "Item B")
	itemC := insertBatchItem(t, db, workspaceID, "Item C")
	linkTypeID := firstOneHopLinkTypeID(t, db)
	linkAB := seedOneHopItemLink(t, db, linkTypeID, itemA, itemB, nil)
	linkCA := seedOneHopItemLink(t, db, linkTypeID, itemC, itemA, nil)

	countingDB := &countingOneHopReadDB{Database: db}
	service := NewItemLinkService(countingDB).WithPermissionService(&oneHopWorkspacePermissions{
		workspaceIDs: []int{workspaceID},
	})

	groups, err := service.ListOneHopItemLinksPageWithChecks(
		context.Background(), 1, []int{itemA, itemB, itemC}, 0, MaxOneHopLinksPerItem, false,
	)
	if err != nil {
		t.Fatalf("ListOneHopItemLinksPageWithChecks: %v", err)
	}
	if got := countingDB.queries.Load(); got != 2 {
		t.Fatalf("database queries = %d, want 2 regardless of anchor count", got)
	}
	if got := linkIDsForTest(groups[itemA].Outgoing); len(got) != 1 || got[0] != linkAB {
		t.Fatalf("item A outgoing ids = %v, want [%d]", got, linkAB)
	}
	if got := linkIDsForTest(groups[itemA].Incoming); len(got) != 1 || got[0] != linkCA {
		t.Fatalf("item A incoming ids = %v, want [%d]", got, linkCA)
	}
	if got := linkIDsForTest(groups[itemB].Incoming); len(got) != 1 || got[0] != linkAB {
		t.Fatalf("item B incoming ids = %v, want [%d]", got, linkAB)
	}
	if got := linkIDsForTest(groups[itemC].Outgoing); len(got) != 1 || got[0] != linkCA {
		t.Fatalf("item C outgoing ids = %v, want [%d]", got, linkCA)
	}
}

func TestListOneHopItemLinksPageCapsAndContinuesPerAnchor(t *testing.T) {
	db := newItemLinkBatchTestDB(t)
	workspaceID := seedOneHopWorkspace(t, db, "One-hop pagination", "OHP")
	anchorID := insertBatchItem(t, db, workspaceID, "Anchor")
	linkTypeID := firstOneHopLinkTypeID(t, db)
	linkIDs := make([]int, 0, MaxOneHopLinksPerItem+1)
	for i := 0; i < MaxOneHopLinksPerItem+1; i++ {
		targetID := insertBatchItem(t, db, workspaceID, fmt.Sprintf("Target %02d", i+1))
		linkIDs = append(linkIDs, seedOneHopItemLink(t, db, linkTypeID, anchorID, targetID, nil))
	}

	service := NewItemLinkService(db).WithPermissionService(&oneHopWorkspacePermissions{
		workspaceIDs: []int{workspaceID},
	})
	first, err := service.ListOneHopItemLinksPageWithChecks(
		context.Background(), 1, []int{anchorID}, 0, MaxOneHopLinksPerItem, false,
	)
	if err != nil {
		t.Fatalf("first link page: %v", err)
	}
	firstPage := first[anchorID]
	if len(firstPage.Outgoing) != MaxOneHopLinksPerItem {
		t.Fatalf("first page links = %d, want %d", len(firstPage.Outgoing), MaxOneHopLinksPerItem)
	}
	if !firstPage.HasMore {
		t.Fatal("first page has_more = false, want true")
	}
	if firstPage.NextAfterID != linkIDs[MaxOneHopLinksPerItem-1] {
		t.Fatalf("first page cursor = %d, want %d", firstPage.NextAfterID, linkIDs[MaxOneHopLinksPerItem-1])
	}

	second, err := service.ListOneHopItemLinksPageWithChecks(
		context.Background(), 1, []int{anchorID}, firstPage.NextAfterID, MaxOneHopLinksPerItem, false,
	)
	if err != nil {
		t.Fatalf("second link page: %v", err)
	}
	secondPage := second[anchorID]
	if got := linkIDsForTest(secondPage.Outgoing); len(got) != 1 || got[0] != linkIDs[MaxOneHopLinksPerItem] {
		t.Fatalf("second page link ids = %v, want [%d]", got, linkIDs[MaxOneHopLinksPerItem])
	}
	if secondPage.HasMore {
		t.Fatal("second page has_more = true, want false")
	}
}

func TestListOneHopItemLinksPageFiltersDeniedEndpointsBeforeLimit(t *testing.T) {
	db := newItemLinkBatchTestDB(t)
	visibleWorkspaceID := seedOneHopWorkspace(t, db, "Visible one-hop", "OHV")
	hiddenWorkspaceID := seedOneHopWorkspace(t, db, "Hidden one-hop", "OHH")
	anchorID := insertBatchItem(t, db, visibleWorkspaceID, "Visible anchor")
	linkTypeID := firstOneHopLinkTypeID(t, db)
	for i := 0; i < MaxOneHopLinksPerItem; i++ {
		hiddenTargetID := insertBatchItem(t, db, hiddenWorkspaceID, fmt.Sprintf("Hidden target %02d", i+1))
		seedOneHopItemLink(t, db, linkTypeID, anchorID, hiddenTargetID, nil)
	}
	visibleTargetID := insertBatchItem(t, db, visibleWorkspaceID, "Visible target")
	visibleLinkID := seedOneHopItemLink(t, db, linkTypeID, anchorID, visibleTargetID, nil)

	service := NewItemLinkService(db).WithPermissionService(&oneHopWorkspacePermissions{
		workspaceIDs: []int{visibleWorkspaceID},
	})
	groups, err := service.ListOneHopItemLinksPageWithChecks(
		context.Background(), 1, []int{anchorID}, 0, MaxOneHopLinksPerItem, false,
	)
	if err != nil {
		t.Fatalf("ListOneHopItemLinksPageWithChecks: %v", err)
	}
	page := groups[anchorID]
	if got := linkIDsForTest(page.Outgoing); len(got) != 1 || got[0] != visibleLinkID {
		t.Fatalf("visible outgoing ids = %v, want [%d]", got, visibleLinkID)
	}
	if page.HasMore {
		t.Fatal("has_more = true after inaccessible links were filtered")
	}
}

func TestListOneHopItemLinksPageIncludesCustomFieldLinksOnlyWhenRequested(t *testing.T) {
	db := newItemLinkBatchTestDB(t)
	workspaceID := seedOneHopWorkspace(t, db, "One-hop custom fields", "OHC")
	anchorID := insertBatchItem(t, db, workspaceID, "Anchor")
	standardTargetID := insertBatchItem(t, db, workspaceID, "Standard target")
	customTargetID := insertBatchItem(t, db, workspaceID, "Custom target")
	linkTypeID := firstOneHopLinkTypeID(t, db)
	standardLinkID := seedOneHopItemLink(t, db, linkTypeID, anchorID, standardTargetID, nil)
	customFieldID := insertItemLinkBatchRow(t, db,
		`INSERT INTO custom_field_definitions (name, field_type) VALUES ('Linked item', 'linking')`,
	)
	customLinkID := seedOneHopItemLink(t, db, linkTypeID, anchorID, customTargetID, &customFieldID)

	service := NewItemLinkService(db).WithPermissionService(&oneHopWorkspacePermissions{
		workspaceIDs: []int{workspaceID},
	})
	withoutCustomFields, err := service.ListOneHopItemLinksPageWithChecks(
		context.Background(), 1, []int{anchorID}, 0, MaxOneHopLinksPerItem, false,
	)
	if err != nil {
		t.Fatalf("link page without custom fields: %v", err)
	}
	if got := linkIDsForTest(withoutCustomFields[anchorID].Outgoing); len(got) != 1 || got[0] != standardLinkID {
		t.Fatalf("default link ids = %v, want [%d]", got, standardLinkID)
	}

	withCustomFields, err := service.ListOneHopItemLinksPageWithChecks(
		context.Background(), 1, []int{anchorID}, 0, MaxOneHopLinksPerItem, true,
	)
	if err != nil {
		t.Fatalf("link page with custom fields: %v", err)
	}
	got := linkIDsForTest(withCustomFields[anchorID].Outgoing)
	if len(got) != 2 || got[0] != standardLinkID || got[1] != customLinkID {
		t.Fatalf("all link ids = %v, want [%d %d]", got, standardLinkID, customLinkID)
	}
}

func linkIDsForTest(links []models.ItemLink) []int {
	ids := make([]int, len(links))
	for i := range links {
		ids[i] = links[i].ID
	}
	return ids
}
