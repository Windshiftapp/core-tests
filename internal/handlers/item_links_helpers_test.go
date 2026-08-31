//go:build test

package handlers

import (
	"fmt"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

// These tests cover the deterministic, pure-data helpers added to lock down
// the item-linking permission cluster: resolveEntityScope,
// accessibleAssetSetIDSet, endpointVisible, filterLinksByAccess, and
// canUserViewEntity. Full HTTP-level coverage of CreateLink / DeleteLink /
// GetLinksForItem / SearchLinkableItems lives alongside in item_links_test.go.

// newTestDB returns an initialized test database using the shared core-tests
// utility so helper coverage runs against the production schema.
func newTestDB(t *testing.T) database.Database {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	if tdb.Engine == "sqlite" {
		t.Cleanup(func() { _ = tdb.Close() })
	}
	return tdb.DB
}

// fakeAssetPerm returns pre-declared (userID, setID, permKey) allow-list
// results so tests don't need to seed the asset role tables.
type fakeAssetPerm struct {
	allow map[[3]int]bool // {userID, setID, permKeyHash}
}

func newFakeAssetPerm(entries ...fakeAssetPermEntry) *fakeAssetPerm {
	m := make(map[[3]int]bool, len(entries))
	for _, e := range entries {
		m[[3]int{e.userID, e.setID, permKeyHash(e.key)}] = true
	}
	return &fakeAssetPerm{allow: m}
}

type fakeAssetPermEntry struct {
	userID int
	setID  int
	key    string
}

func (f *fakeAssetPerm) HasAssetSetPermission(userID, setID int, key string) (bool, error) {
	return f.allow[[3]int{userID, setID, permKeyHash(key)}], nil
}

// permKeyHash keeps the fake's lookup map integer-keyed; any stable mapping
// from the handful of permission key strings the handler uses is fine.
func permKeyHash(key string) int {
	switch key {
	case services.AssetPermissionKeyView:
		return 1
	case services.AssetPermissionKeyEdit:
		return 2
	case services.AssetPermissionKeyDelete:
		return 3
	}
	return 0
}

// --- Tests -----------------------------------------------------------------

func TestResolveEntityScope(t *testing.T) {
	db := newTestDB(t)
	insertItemScope(t, db, 10, 7)
	insertTestCaseScope(t, db, 20, 8)
	insertAssetScope(t, db, 30, 9)

	h := &ItemLinkHandler{db: db}

	wsID, setID, found, err := h.resolveEntityScope("item", 10)
	if err != nil || !found || wsID != 7 || setID != 0 {
		t.Fatalf("item: got (%d,%d,%v,%v), want (7,0,true,nil)", wsID, setID, found, err)
	}

	wsID, setID, found, err = h.resolveEntityScope("test_case", 20)
	if err != nil || !found || wsID != 8 || setID != 0 {
		t.Fatalf("test_case: got (%d,%d,%v,%v), want (8,0,true,nil)", wsID, setID, found, err)
	}

	wsID, setID, found, err = h.resolveEntityScope("asset", 30)
	if err != nil || !found || wsID != 0 || setID != 9 {
		t.Fatalf("asset: got (%d,%d,%v,%v), want (0,9,true,nil)", wsID, setID, found, err)
	}

	// Missing rows return found=false without error — callers turn that into 404.
	if _, _, found, err := h.resolveEntityScope("item", 999); err != nil || found {
		t.Fatalf("missing item: got (_,_,%v,%v), want (false, nil)", found, err)
	}

	// Unknown entity type is a real error (caller fails closed).
	if _, _, _, err := h.resolveEntityScope("bogus", 1); err == nil {
		t.Fatal("unknown entity type: want error, got nil")
	}
}

func TestEndpointVisible_ItemUsesWorkspaceKey(t *testing.T) {
	h := &ItemLinkHandler{db: newTestDB(t)}
	accessibleKeys := map[string]bool{"ok-ws": true}

	if !h.endpointVisible("item", 0, "ok-ws", accessibleKeys, nil, nil) {
		t.Fatal("item in accessible workspace should be visible")
	}
	if h.endpointVisible("item", 0, "hidden-ws", accessibleKeys, nil, nil) {
		t.Fatal("item in inaccessible workspace should be hidden")
	}
	// A missing workspace key cannot prove access and must fail closed.
	if h.endpointVisible("item", 0, "", accessibleKeys, nil, nil) {
		t.Fatal("item with empty workspace key should be hidden")
	}
}

func TestEndpointVisible_TestCaseUsesWorkspaceID(t *testing.T) {
	db := newTestDB(t)
	insertTestCaseScope(t, db, 1, 100)
	insertTestCaseScope(t, db, 2, 200)
	h := &ItemLinkHandler{db: db}
	accessibleWs := map[int]bool{100: true}

	if !h.endpointVisible("test_case", 1, "", nil, accessibleWs, nil) {
		t.Fatal("test_case in accessible workspace should be visible")
	}
	if h.endpointVisible("test_case", 2, "", nil, accessibleWs, nil) {
		t.Fatal("test_case in inaccessible workspace should be hidden")
	}
	if h.endpointVisible("test_case", 999, "", nil, accessibleWs, nil) {
		t.Fatal("missing test_case should be hidden (fail-closed)")
	}
}

func TestEndpointVisible_AssetUsesSetID(t *testing.T) {
	db := newTestDB(t)
	insertAssetScope(t, db, 1, 500)
	insertAssetScope(t, db, 2, 600)
	h := &ItemLinkHandler{db: db}
	accessibleSets := map[int]bool{500: true}

	if !h.endpointVisible("asset", 1, "", nil, nil, accessibleSets) {
		t.Fatal("asset in accessible set should be visible")
	}
	if h.endpointVisible("asset", 2, "", nil, nil, accessibleSets) {
		t.Fatal("asset in inaccessible set should be hidden")
	}
	if h.endpointVisible("asset", 999, "", nil, nil, accessibleSets) {
		t.Fatal("missing asset should be hidden (fail-closed)")
	}
}

func TestFilterLinksByAccess_DropsMixedInaccessibleEndpoints(t *testing.T) {
	db := newTestDB(t)
	// test_case 1 is visible (ws 100), test_case 2 is not (ws 200).
	insertTestCaseScope(t, db, 1, 100)
	insertTestCaseScope(t, db, 2, 200)
	// asset 10 is visible (set 500), asset 20 is not (set 600).
	insertAssetScope(t, db, 10, 500)
	insertAssetScope(t, db, 20, 600)
	h := &ItemLinkHandler{db: db}

	accessibleKeys := map[string]bool{"ok": true}
	accessibleWs := map[int]bool{100: true}
	accessibleSets := map[int]bool{500: true}

	links := []models.ItemLink{
		{ID: 1, SourceType: "item", SourceID: 1, SourceWorkspaceKey: "ok",
			TargetType: "test_case", TargetID: 1},
		{ID: 2, SourceType: "item", SourceID: 1, SourceWorkspaceKey: "ok",
			TargetType: "test_case", TargetID: 2}, // dropped: tc in hidden ws
		{ID: 3, SourceType: "item", SourceID: 1, SourceWorkspaceKey: "ok",
			TargetType: "asset", TargetID: 10},
		{ID: 4, SourceType: "asset", SourceID: 20,
			TargetType: "item", TargetID: 1, TargetWorkspaceKey: "ok"}, // dropped: asset in hidden set
		{ID: 5, SourceType: "item", SourceID: 1, SourceWorkspaceKey: "hidden",
			TargetType: "item", TargetID: 1, TargetWorkspaceKey: "ok"}, // dropped: source item ws hidden
	}

	got := h.filterLinksByAccess(links, accessibleKeys, accessibleWs, accessibleSets)
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 3 {
		t.Fatalf("want IDs [1,3], got %v", linkIDs(got))
	}
}

func TestAccessibleAssetSetIDSet_UsesChecker(t *testing.T) {
	db := newTestDB(t)
	mustExec(t, db, "INSERT INTO asset_management_sets (id, name) VALUES (1, 'ok')")
	mustExec(t, db, "INSERT INTO asset_management_sets (id, name) VALUES (2, 'hidden')")
	mustExec(t, db, "INSERT INTO asset_management_sets (id, name) VALUES (3, 'other')")

	checker := newFakeAssetPerm(
		fakeAssetPermEntry{userID: 42, setID: 1, key: services.AssetPermissionKeyView},
		fakeAssetPermEntry{userID: 42, setID: 3, key: services.AssetPermissionKeyView},
	)
	h := &ItemLinkHandler{db: db, assetPerm: checker}
	got := h.accessibleAssetSetIDSet(&models.User{ID: 42})

	if len(got) != 2 || !got[1] || got[2] || !got[3] {
		t.Fatalf("want {1:true,3:true}, got %v", got)
	}

	// Nil checker → fail-closed: empty set.
	h2 := &ItemLinkHandler{db: db}
	if len(h2.accessibleAssetSetIDSet(&models.User{ID: 42})) != 0 {
		t.Fatal("nil assetPerm must produce empty set (fail-closed)")
	}

	// Nil user → empty set.
	if len(h.accessibleAssetSetIDSet(nil)) != 0 {
		t.Fatal("nil user must produce empty set")
	}
}

func TestCanUserViewEntity(t *testing.T) {
	db := newTestDB(t)
	insertItemScope(t, db, 1, 100)
	insertTestCaseScope(t, db, 1, 200)
	insertAssetScope(t, db, 1, 300)
	h := &ItemLinkHandler{db: db}
	accessibleWs := map[int]bool{100: true}
	accessibleSets := map[int]bool{}

	if !h.canUserViewEntity(1, "item", 1, accessibleWs, accessibleSets) {
		t.Fatal("item in accessible ws should be viewable")
	}
	if h.canUserViewEntity(1, "test_case", 1, accessibleWs, accessibleSets) {
		t.Fatal("test_case in inaccessible ws must not be viewable")
	}
	if h.canUserViewEntity(1, "asset", 1, accessibleWs, accessibleSets) {
		t.Fatal("asset in inaccessible set must not be viewable")
	}
}

// --- helpers ---------------------------------------------------------------

func insertItemScope(t *testing.T, db database.Database, id, workspaceID int) {
	t.Helper()
	mustExec(t, db, "INSERT INTO workspaces (id, name, key) VALUES (?, ?, ?) ON CONFLICT (id) DO NOTHING", workspaceID, fmt.Sprintf("Workspace %d", workspaceID), fmt.Sprintf("W%d", workspaceID))
	// Create through the production path, then pin the fixture's item id so
	// the entity-scope lookups below resolve this exact row.
	f := factory.NewTestFactory(db)
	createdID, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: workspaceID,
		Title:       fmt.Sprintf("Item %d", id),
	})
	if err != nil {
		t.Fatalf("insert item scope fixture: %v", err)
	}
	if _, err := db.Exec(`UPDATE items SET id = ? WHERE id = ?`, id, createdID); err != nil {
		t.Fatalf("pin item id %d: %v", id, err)
	}
}

func insertTestCaseScope(t *testing.T, db database.Database, id, workspaceID int) {
	t.Helper()
	mustExec(t, db, "INSERT INTO workspaces (id, name, key) VALUES (?, ?, ?) ON CONFLICT (id) DO NOTHING", workspaceID, fmt.Sprintf("Workspace %d", workspaceID), fmt.Sprintf("W%d", workspaceID))
	mustExec(t, db, "INSERT INTO test_cases (id, workspace_id, title) VALUES (?, ?, ?)", id, workspaceID, fmt.Sprintf("Test case %d", id))
}

func insertAssetScope(t *testing.T, db database.Database, id, setID int) {
	t.Helper()
	assetTypeID := setID
	mustExec(t, db, "INSERT INTO asset_management_sets (id, name) VALUES (?, ?) ON CONFLICT (id) DO NOTHING", setID, fmt.Sprintf("Asset set %d", setID))
	mustExec(t, db, "INSERT INTO asset_types (id, set_id, name) VALUES (?, ?, ?) ON CONFLICT (id) DO NOTHING", assetTypeID, setID, fmt.Sprintf("Asset type %d", setID))
	mustExec(t, db, "INSERT INTO assets (id, set_id, asset_type_id, title) VALUES (?, ?, ?, ?)", id, setID, assetTypeID, fmt.Sprintf("Asset %d", id))
}

func mustExec(t *testing.T, db database.Database, q string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func linkIDs(links []models.ItemLink) []int {
	ids := make([]int, len(links))
	for i, l := range links {
		ids[i] = l.ID
	}
	return ids
}
