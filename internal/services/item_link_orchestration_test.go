package services

import (
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
)

// These tests cover the pure-data helpers lifted from the cookie-auth
// handler into ItemLinkService: resolveEntityScope, EndpointVisible,
// FilterLinksByAccess, AccessibleAssetSetIDs. The HTTP-grade orchestration
// (CreateLinkWithChecks, DeleteLinkWithChecks,
// ListLinksForEntityWithChecks) is exercised end-to-end against a real
// server in core-tests/.

// newLinkTestDB returns a real database.Database (shared in-memory SQLite)
// with just the four tables the helpers read from. Mirrors the previous
// handler-level newTestDB so the test bodies stay verbatim.
func newLinkTestDB(t *testing.T) database.Database {
	t.Helper()
	db, err := database.NewSQLiteDB("file::memory:?cache=shared&mode=memory")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, stmt := range []string{
		`CREATE TABLE items (id INTEGER PRIMARY KEY, workspace_id INTEGER NOT NULL)`,
		`CREATE TABLE test_cases (id INTEGER PRIMARY KEY, workspace_id INTEGER NOT NULL, title TEXT)`,
		`CREATE TABLE asset_management_sets (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE assets (id INTEGER PRIMARY KEY, set_id INTEGER NOT NULL, title TEXT)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	return db
}

func mustLinkExec(t *testing.T, db database.Database, q string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
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

func permKeyHash(key string) int {
	switch key {
	case AssetPermissionKeyView:
		return 1
	case AssetPermissionKeyEdit:
		return 2
	}
	return 0
}

// --- Tests ----------------------------------------------------------------

func TestItemLinkService_ResolveEntityScope(t *testing.T) {
	db := newLinkTestDB(t)
	mustLinkExec(t, db, "INSERT INTO items (id, workspace_id) VALUES (10, 100)")
	mustLinkExec(t, db, "INSERT INTO test_cases (id, workspace_id) VALUES (20, 200)")
	mustLinkExec(t, db, "INSERT INTO assets (id, set_id) VALUES (30, 300)")
	s := NewItemLinkService(db)

	wsID, setID, found, err := s.ResolveEntityScope("item", 10)
	if err != nil || !found || wsID != 100 || setID != 0 {
		t.Fatalf("item: got (%d,%d,%v,%v)", wsID, setID, found, err)
	}
	wsID, setID, found, err = s.ResolveEntityScope("test_case", 20)
	if err != nil || !found || wsID != 200 || setID != 0 {
		t.Fatalf("test_case: got (%d,%d,%v,%v)", wsID, setID, found, err)
	}
	wsID, setID, found, err = s.ResolveEntityScope("asset", 30)
	if err != nil || !found || wsID != 0 || setID != 300 {
		t.Fatalf("asset: got (%d,%d,%v,%v)", wsID, setID, found, err)
	}
	if _, _, found, err := s.ResolveEntityScope("item", 999); err != nil || found {
		t.Fatalf("missing item: want (_,_,false,nil), got (_,_,%v,%v)", found, err)
	}
	if _, _, _, err := s.ResolveEntityScope("bogus", 1); err == nil {
		t.Fatal("unknown type must error")
	}
}

func TestItemLinkService_EndpointVisible_ItemUsesWorkspaceKey(t *testing.T) {
	s := NewItemLinkService(newLinkTestDB(t))
	accessibleKeys := map[string]bool{"ok-ws": true}

	if !s.EndpointVisible("item", 0, "ok-ws", accessibleKeys, nil, nil) {
		t.Fatal("accessible key should be visible")
	}
	if s.EndpointVisible("item", 0, "hidden-ws", accessibleKeys, nil, nil) {
		t.Fatal("inaccessible key must be hidden")
	}
	// Missing scope metadata must fail closed instead of exposing an item from
	// an unknown workspace.
	if s.EndpointVisible("item", 0, "", accessibleKeys, nil, nil) {
		t.Fatal("empty workspace key on item endpoint should be hidden")
	}
}

func TestItemLinkService_EndpointVisible_TestCaseUsesWorkspaceID(t *testing.T) {
	db := newLinkTestDB(t)
	mustLinkExec(t, db, "INSERT INTO test_cases (id, workspace_id) VALUES (1, 100), (2, 200)")
	s := NewItemLinkService(db)
	accessibleWs := map[int]bool{100: true}

	if !s.EndpointVisible("test_case", 1, "", nil, accessibleWs, nil) {
		t.Fatal("test_case in accessible workspace should be visible")
	}
	if s.EndpointVisible("test_case", 2, "", nil, accessibleWs, nil) {
		t.Fatal("test_case in inaccessible workspace must be hidden")
	}
	if s.EndpointVisible("test_case", 999, "", nil, accessibleWs, nil) {
		t.Fatal("missing test_case must be hidden")
	}
}

func TestItemLinkService_EndpointVisible_AssetUsesSetID(t *testing.T) {
	db := newLinkTestDB(t)
	mustLinkExec(t, db, "INSERT INTO assets (id, set_id) VALUES (1, 100), (2, 200)")
	s := NewItemLinkService(db)
	accessibleSets := map[int]bool{100: true}

	if !s.EndpointVisible("asset", 1, "", nil, nil, accessibleSets) {
		t.Fatal("asset in accessible set should be visible")
	}
	if s.EndpointVisible("asset", 2, "", nil, nil, accessibleSets) {
		t.Fatal("asset in inaccessible set must be hidden")
	}
	if s.EndpointVisible("asset", 999, "", nil, nil, accessibleSets) {
		t.Fatal("missing asset must be hidden")
	}
}

func TestItemLinkService_FilterLinksByAccess_DropsMixedInaccessibleEndpoints(t *testing.T) {
	db := newLinkTestDB(t)
	mustLinkExec(t, db, "INSERT INTO test_cases (id, workspace_id) VALUES (1, 100), (2, 200)")
	mustLinkExec(t, db, "INSERT INTO assets (id, set_id) VALUES (10, 1000), (20, 2000)")
	s := NewItemLinkService(db)

	accessibleKeys := map[string]bool{"OK": true}
	accessibleWs := map[int]bool{100: true}
	accessibleSets := map[int]bool{1000: true}

	links := []models.ItemLink{
		// Both endpoints visible — kept.
		{ID: 1, SourceType: "item", SourceWorkspaceKey: "OK", TargetType: "test_case", TargetID: 1},
		// Target test_case hidden — dropped.
		{ID: 2, SourceType: "item", SourceWorkspaceKey: "OK", TargetType: "test_case", TargetID: 2},
		// Source asset hidden — dropped.
		{ID: 3, SourceType: "asset", SourceID: 20, TargetType: "item", TargetWorkspaceKey: "OK"},
		// Both ends visible (asset 10 in set 1000, item OK) — kept.
		{ID: 4, SourceType: "asset", SourceID: 10, TargetType: "item", TargetWorkspaceKey: "OK"},
	}

	got := s.FilterLinksByAccess(links, accessibleKeys, accessibleWs, accessibleSets)
	gotIDs := make([]int, len(got))
	for i, l := range got {
		gotIDs[i] = l.ID
	}
	want := []int{1, 4}
	if len(gotIDs) != len(want) || gotIDs[0] != want[0] || gotIDs[1] != want[1] {
		t.Fatalf("ids: got %v, want %v", gotIDs, want)
	}
}

func TestItemLinkService_AccessibleAssetSetIDs_UsesChecker(t *testing.T) {
	db := newLinkTestDB(t)
	mustLinkExec(t, db, "INSERT INTO asset_management_sets (id, name) VALUES (1, 'a'), (2, 'b'), (3, 'c')")
	checker := newFakeAssetPerm(
		fakeAssetPermEntry{userID: 42, setID: 1, key: AssetPermissionKeyView},
		fakeAssetPermEntry{userID: 42, setID: 3, key: AssetPermissionKeyView},
	)
	s := NewItemLinkService(db).WithAssetPermissionChecker(checker)

	got := s.AccessibleAssetSetIDs(42)
	if len(got) != 2 || !got[1] || !got[3] {
		t.Fatalf("want {1:true, 3:true}, got %v", got)
	}

	// No checker → fail closed.
	if s2 := NewItemLinkService(db); len(s2.AccessibleAssetSetIDs(42)) != 0 {
		t.Fatal("missing checker must yield empty set")
	}
}
