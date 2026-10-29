//go:build test

package services_test

import (
	"fmt"
	"testing"

	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

// mapWorkspaceAccess is a stub CatalogAccess with explicit per-workspace
// grants, so tests exercise the visibility rule without a permission cache.
type mapWorkspaceAccess struct {
	view  map[int]bool
	admin map[int]bool
}

func (m *mapWorkspaceAccess) CanViewWorkspace(_, workspaceID int) (bool, error) {
	return m.view[workspaceID], nil
}

func (m *mapWorkspaceAccess) CanAdminWorkspace(_, workspaceID int) (bool, error) {
	return m.admin[workspaceID], nil
}

func (m *mapWorkspaceAccess) HasGlobalPermission(int, string) (bool, error) {
	return false, nil
}

func (m *mapWorkspaceAccess) WorkspaceVisibility(int) (services.WorkspaceVisibility, error) {
	return mapWorkspaceVisibility{access: m}, nil
}

// mapWorkspaceVisibility answers from the same explicit grants as the
// per-check methods so the request-scoped evaluator path and the
// single-workspace path stay behaviorally identical in tests.
type mapWorkspaceVisibility struct {
	access *mapWorkspaceAccess
}

func (v mapWorkspaceVisibility) CanView(workspaceID int) (bool, error) {
	return v.access.view[workspaceID], nil
}

func (v mapWorkspaceVisibility) CanAdmin(workspaceID int) (bool, error) {
	return v.access.admin[workspaceID], nil
}

type catalogListFixture struct {
	db     *testutils.TestDB
	userID int
	access *mapWorkspaceAccess
}

func newCatalogListFixture(t *testing.T) *catalogListFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	f := factory.NewTestFactory(tdb.GetDatabase())
	userID, err := f.CreateUser(nil)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return &catalogListFixture{
		db:     tdb,
		userID: userID,
		access: &mapWorkspaceAccess{view: map[int]bool{}, admin: map[int]bool{}},
	}
}

func (fx *catalogListFixture) service(t *testing.T) *services.CatalogReadService {
	t.Helper()
	return services.NewCatalogReadService(fx.db.GetDatabase(), nil, fx.access, nil)
}

// createWorkspace inserts a workspace row directly: inactive and personal
// shapes have no production creation path suitable for fixtures, and the
// list endpoint's visibility contract needs those shapes.
func (fx *catalogListFixture) createWorkspace(t *testing.T, name, key string, active, personal bool) int {
	t.Helper()
	var ownerID any
	if personal {
		ownerID = fx.userID
	}
	result, err := fx.db.Exec(
		`INSERT INTO workspaces (name, key, description, active, is_personal, owner_id, internal_comments_enabled)
		 VALUES (?, ?, ?, ?, ?, ?, false)`,
		name, key, "description of "+name, active, personal, ownerID,
	)
	if err != nil {
		t.Fatalf("create workspace %s: %v", name, err)
	}
	_ = result
	// Look up by the unique key: LastInsertId is not portable across engines.
	var id int
	if err := fx.db.QueryRow(`SELECT id FROM workspaces WHERE key = ?`, key).Scan(&id); err != nil {
		t.Fatalf("read back workspace %s: %v", key, err)
	}
	return id
}

func (fx *catalogListFixture) grantView(id int)  { fx.access.view[id] = true }
func (fx *catalogListFixture) grantAdmin(id int) { fx.access.admin[id] = true }

func TestCatalogListWorkspaces_VisibilityContract(t *testing.T) {
	cases := []struct {
		name       string
		active     bool
		personal   bool
		view       bool
		admin      bool
		wantInList bool
	}{
		{name: "activeWithView", active: true, view: true, wantInList: true},
		{name: "activeWithoutView", active: true, view: false, admin: true, wantInList: false},
		{name: "inactiveWithAdmin", active: false, admin: true, wantInList: true},
		{name: "inactiveWithViewOnly", active: false, view: true, wantInList: false},
		{name: "activePersonalWithView", active: true, personal: true, view: true, wantInList: true},
		{name: "inactivePersonalWithAdmin", active: false, personal: true, admin: true, wantInList: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newCatalogListFixture(t)
			id := fx.createWorkspace(t, "WS "+tc.name, "K"+tc.name[:3], tc.active, tc.personal)
			if tc.view {
				fx.grantView(id)
			}
			if tc.admin {
				fx.grantAdmin(id)
			}

			workspaces, total, err := fx.service(t).ListWorkspaces(fx.userID, services.CatalogPageParams{Limit: 100})
			if err != nil {
				t.Fatalf("ListWorkspaces: %v", err)
			}

			found := false
			for _, ws := range workspaces {
				if ws.ID == id {
					found = true
				}
			}
			if found != tc.wantInList {
				t.Fatalf("workspace %d in list = %v, want %v (rows=%d total=%d)", id, found, tc.wantInList, len(workspaces), total)
			}
		})
	}

	t.Run("anotherUsersPersonalWorkspaceIsNeverACandidate", func(t *testing.T) {
		fx := newCatalogListFixture(t)
		// A second user in the same database owns the foreign personal workspace.
		f := factory.NewTestFactory(fx.db.GetDatabase())
		otherID, err := f.CreateUser(nil)
		if err != nil {
			t.Fatalf("create second user: %v", err)
		}
		if _, err := fx.db.Exec(
			`INSERT INTO workspaces (name, key, description, active, is_personal, owner_id, internal_comments_enabled)
			 VALUES ('Foreign personal', 'FORPN', 'd', true, true, ?, false)`,
			otherID,
		); err != nil {
			t.Fatalf("seed personal workspace: %v", err)
		}
		// Even a blanket view grant must not surface someone else's personal
		// workspace, because the candidate query only includes the caller's own.
		var foreignID int
		if err := fx.db.QueryRow(`SELECT id FROM workspaces WHERE key = 'FORPN'`).Scan(&foreignID); err != nil {
			t.Fatalf("read workspace: %v", err)
		}
		fx.access.view[foreignID] = true
		fx.access.admin[foreignID] = true

		workspaces, total, err := fx.service(t).ListWorkspaces(fx.userID, services.CatalogPageParams{Limit: 100})
		if err != nil {
			t.Fatalf("ListWorkspaces: %v", err)
		}
		for _, ws := range workspaces {
			if ws.ID == foreignID {
				t.Fatalf("foreign personal workspace leaked into list (total=%d)", total)
			}
		}
	})
}

func TestCatalogListWorkspaces_PaginationCoversDirectoryWithoutGaps(t *testing.T) {
	fx := newCatalogListFixture(t)
	var ids []int
	for i := 0; i < 5; i++ {
		id := fx.createWorkspace(t, fmt.Sprintf("Page workspace %d", i), fmt.Sprintf("PW%d", i), true, false)
		fx.grantView(id)
		ids = append(ids, id)
	}
	hidden := fx.createWorkspace(t, "Hidden workspace", "HID", true, false)
	_ = hidden // no grant: excluded from totals and pages

	svc := fx.service(t)
	var collected []int
	total := -1
	for page := 1; page <= 5; page++ {
		rows, pageTotal, err := svc.ListWorkspaces(fx.userID, services.CatalogPageParams{
			Limit: 1, Offset: (page - 1) * 1, Sort: "name",
		})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		total = pageTotal
		for _, ws := range rows {
			collected = append(collected, ws.ID)
		}
		if len(rows) == 0 {
			break
		}
	}

	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	if len(collected) != 5 {
		t.Fatalf("walked %d rows, want 5", len(collected))
	}
	seen := map[int]bool{}
	for _, id := range collected {
		if seen[id] {
			t.Fatalf("workspace %d appeared twice across pages: %v", id, collected)
		}
		seen[id] = true
	}
}

func TestCatalogListWorkspaces_Search(t *testing.T) {
	fx := newCatalogListFixture(t)
	alpha := fx.createWorkspace(t, "Alpha Platform", "ALPHA", true, false)
	fx.grantView(alpha)
	wild := fx.createWorkspace(t, "Alpha_100% zone", "AWILD", true, false)
	fx.grantView(wild)
	byKey := fx.createWorkspace(t, "Unrelated name", "betakey", true, false)
	fx.grantView(byKey)
	byDescription := fx.createWorkspace(t, "Unrelated too", "GAMMA", true, false)
	// description is seeded as "description of <name>" — search text unique to it
	if _, err := fx.db.Exec(`UPDATE workspaces SET description = 'special glossary content' WHERE id = ?`, byDescription); err != nil {
		t.Fatalf("set description: %v", err)
	}
	fx.grantView(byDescription)
	none := fx.createWorkspace(t, "Zebra zone", "ZEBRA", true, false)
	fx.grantView(none)

	svc := fx.service(t)
	cases := []struct {
		name      string
		search    string
		wantIDs   []int
		wantTotal int
	}{
		{name: "nameSubstringCaseInsensitive", search: "alpha PLAT", wantIDs: []int{alpha}, wantTotal: 1},
		{name: "keySubstring", search: "BETAKEY", wantIDs: []int{byKey}, wantTotal: 1},
		{name: "descriptionSubstring", search: "GLOSSARY", wantIDs: []int{byDescription}, wantTotal: 1},
		// Wildcards must match literally: "a_1" matches "Alpha_100% zone"
		// only, not every name with "a", one char, "1".
		{name: "underscoreIsLiteral", search: "a_1", wantIDs: []int{wild}, wantTotal: 1},
		// If % were a wildcard, "100%z" would match "100% zone"; literally it
		// must not match anything.
		{name: "percentIsNotAWildcard", search: "100%z", wantIDs: nil, wantTotal: 0},
		{name: "percentMatchedLiterally", search: "00% zo", wantIDs: []int{wild}, wantTotal: 1},
		{name: "noMatch", search: "nosuchworkspace", wantIDs: nil, wantTotal: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, total, err := svc.ListWorkspaces(fx.userID, services.CatalogPageParams{
				Limit: 100, Sort: "name", Search: tc.search,
			})
			if err != nil {
				t.Fatalf("ListWorkspaces search=%q: %v", tc.search, err)
			}
			if total != tc.wantTotal {
				t.Fatalf("total = %d, want %d", total, tc.wantTotal)
			}
			got := map[int]bool{}
			for _, ws := range rows {
				got[ws.ID] = true
			}
			for _, want := range tc.wantIDs {
				if !got[want] {
					t.Fatalf("search %q: missing workspace %d in %+v", tc.search, want, rows)
				}
			}
			if len(rows) != len(tc.wantIDs) {
				t.Fatalf("search %q: got %d rows, want %d (%+v)", tc.search, len(rows), len(tc.wantIDs), rows)
			}
		})
	}
}

func TestCatalogListWorkspaces_Sorting(t *testing.T) {
	fx := newCatalogListFixture(t)
	ids := map[string]int{}
	for _, name := range []string{"Mango", "apple", "Banana"} {
		id := fx.createWorkspace(t, name, "K"+name[:2], true, false)
		fx.grantView(id)
		ids[name] = id
	}
	svc := fx.service(t)

	t.Run("nameSortIsCaseInsensitiveAscending", func(t *testing.T) {
		rows, _, err := svc.ListWorkspaces(fx.userID, services.CatalogPageParams{Limit: 10, Sort: "name"})
		if err != nil {
			t.Fatalf("ListWorkspaces: %v", err)
		}
		var got []string
		for _, ws := range rows {
			got = append(got, ws.Name)
		}
		want := []string{"apple", "Banana", "Mango"}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("names = %v, want %v", got, want)
			}
		}
	})

	t.Run("keySort", func(t *testing.T) {
		rows, _, err := svc.ListWorkspaces(fx.userID, services.CatalogPageParams{Limit: 10, Sort: "key"})
		if err != nil {
			t.Fatalf("ListWorkspaces: %v", err)
		}
		// Keys: Kap (apple), KBa (Banana), KMa (Mango) — lowercase ordering:
		// kap < kba < kma.
		if len(rows) != 3 || rows[0].Name != "apple" || rows[1].Name != "Banana" || rows[2].Name != "Mango" {
			t.Fatalf("key order unexpected: %+v", rows)
		}
	})

	t.Run("descFlipsNamesButKeepsIdTiebreakAscending", func(t *testing.T) {
		tieA := fx.createWorkspace(t, "Twin", "TWIA", true, false)
		fx.grantView(tieA)
		tieB := fx.createWorkspace(t, "Twin", "TWIB", true, false)
		fx.grantView(tieB)

		rows, _, err := svc.ListWorkspaces(fx.userID, services.CatalogPageParams{Limit: 10, Sort: "name", Desc: true})
		if err != nil {
			t.Fatalf("ListWorkspaces: %v", err)
		}
		if rows[0].Name != "Twin" || rows[1].Name != "Twin" {
			t.Fatalf("twins should sort last (desc on 'Twin'), got %+v", rows)
		}
		if rows[0].ID > rows[1].ID {
			t.Fatalf("id tiebreak must stay ascending in desc mode: %d then %d", rows[0].ID, rows[1].ID)
		}
	})
}

func TestCatalogListWorkspaces_NoVisibleWorkspaces(t *testing.T) {
	fx := newCatalogListFixture(t)
	id := fx.createWorkspace(t, "Invisible", "INVS", true, false)
	_ = id

	workspaces, total, err := fx.service(t).ListWorkspaces(fx.userID, services.CatalogPageParams{Limit: 100})
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(workspaces) != 0 || total != 0 {
		t.Fatalf("got %d rows total %d, want empty/0", len(workspaces), total)
	}
}

func TestWorkspaceServiceList_SearchContract(t *testing.T) {
	fx := newCatalogListFixture(t)
	alpha := fx.createWorkspace(t, "Alpha Platform", "ALPHA", true, false)
	wild := fx.createWorkspace(t, "Alpha_100 zone", "AWILD", true, false)
	fx.grantView(alpha)
	fx.grantView(wild)
	service := services.NewWorkspaceService(fx.db.GetDatabase())
	scope := []int{alpha, wild}

	cases := []struct {
		name      string
		search    string
		wantTotal int
		wantID    int
	}{
		{name: "matchesNameCaseInsensitive", search: "alpha PLAT", wantTotal: 1, wantID: alpha},
		{name: "underscoreIsLiteral", search: "a_1", wantTotal: 1, wantID: wild},
		{name: "noMatchExcludesAll", search: "missing", wantTotal: 0, wantID: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, total, err := service.List(services.WorkspaceListParams{
				WorkspaceIDs: scope,
				Search:       tc.search,
				Limit:        100,
			})
			if err != nil {
				t.Fatalf("List search=%q: %v", tc.search, err)
			}
			if total != tc.wantTotal {
				t.Fatalf("total = %d, want %d", total, tc.wantTotal)
			}
			if len(rows) != tc.wantTotal {
				t.Fatalf("rows = %d, want %d", len(rows), tc.wantTotal)
			}
			if tc.wantID != 0 && rows[0].ID != tc.wantID {
				t.Fatalf("row id = %d, want %d", rows[0].ID, tc.wantID)
			}
		})
	}

	t.Run("walksPagesWithConsistentTotals", func(t *testing.T) {
		third := fx.createWorkspace(t, "Alpha three", "ATHREE", true, false)
		fx.grantView(third)
		scope := []int{alpha, wild, third}
		seen := map[int]bool{}
		for offset := 0; offset < 3; offset++ {
			rows, total, err := service.List(services.WorkspaceListParams{
				WorkspaceIDs: scope,
				Search:       "alpha",
				Limit:        1,
				Offset:       offset,
			})
			if err != nil {
				t.Fatalf("page at offset %d: %v", offset, err)
			}
			if total != 3 {
				t.Fatalf("total at offset %d = %d, want 3", offset, total)
			}
			for _, ws := range rows {
				if seen[ws.ID] {
					t.Fatalf("workspace %d repeated across pages", ws.ID)
				}
				seen[ws.ID] = true
			}
		}
		if len(seen) != 3 {
			t.Fatalf("walk covered %d workspaces, want 3", len(seen))
		}
	})

	t.Run("findByIDsPageEmptyScopeReturnsEmptyWithoutQuery", func(t *testing.T) {
		repo := repository.NewWorkspaceRepository(fx.db.GetDatabase())
		rows, total, err := repo.FindByIDsPage(repository.WorkspaceIDPageParams{IDs: nil, Limit: 10})
		if err != nil {
			t.Fatalf("FindByIDsPage empty scope: %v", err)
		}
		if len(rows) != 0 || total != 0 {
			t.Fatalf("empty scope returned %d rows total %d", len(rows), total)
		}
	})
}
