//go:build test

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/authz"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
	"windshift/internal/validation"
)

// Regression tests for the 0.8.9 personal-workspace fixes (WI-1377..WI-1391).
// Each test pins the desired contract.

const dashedKeyWorkspaceID = 2

// seedDashedKeyWorkspace inserts a workspace whose key violates the documented
// format — exactly what the pre-0.8.9 personal-workspace generator produced
// and what existing installs still carry (no migration) — plus one item per
// given title. Direct SQL is the documented exception for legacy shapes the
// API can no longer create.
func seedDashedKeyWorkspace(t *testing.T, tdb *testutils.TestDB, titles map[string]string) {
	t.Helper()
	_, err := tdb.Exec(`
		INSERT INTO workspaces (id, name, key, description, active, is_personal, owner_id)
		VALUES (?, 'John''s Todo List', 'JOHN-DOE', 'personal', true, true, 1)
	`, dashedKeyWorkspaceID)
	if err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	var statusID int
	if err := tdb.QueryRow("SELECT id FROM statuses WHERE is_default = true LIMIT 1").Scan(&statusID); err != nil {
		t.Fatalf("status: %v", err)
	}
	var itemTypeID int
	if err := tdb.QueryRow("SELECT id FROM item_types LIMIT 1").Scan(&itemTypeID); err != nil {
		t.Fatalf("item type: %v", err)
	}
	number := 3
	for title := range titles {
		_, err := tdb.Exec(`
			INSERT INTO items (title, description, workspace_id, status_id, priority_id, item_type_id, workspace_item_number, creator_id, frac_index)
			VALUES (?, 'fixture body', ?, ?, 1, ?, ?, 1, ?)
		`, title, dashedKeyWorkspaceID, statusID, itemTypeID, number, fmt.Sprintf("a%d", number))
		if err != nil {
			t.Fatalf("seed item %q: %v", title, err)
		}
		number++
	}
}

// WI-1383: an item in a dash-containing workspace (legacy personal keys were
// not migrated) must be findable by its rendered KEY-NUMBER via a
// right-anchored split.
func TestItemKeyQueryResolvesKeysWithSeparators(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	seedDashedKeyWorkspace(t, tdb, map[string]string{
		"Findable by key": "key lookup fixture",
		"Other item":      "must not match",
	})

	repo := repository.NewItemRepository(tdb.GetDatabase())
	items, _, err := repo.FindAllWithDetails(repository.ItemListParams{
		WorkspaceIDs: []int{dashedKeyWorkspaceID},
		Filters:      repository.ItemFilters{ItemKeyQuery: "JOHN-DOE-3"},
	})
	if err != nil {
		t.Fatalf("list by item key: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected exactly item JOHN-DOE-3 to resolve via its rendered key, got %d items", len(items))
	}
	if items[0].WorkspaceItemNumber != 3 {
		t.Errorf("expected item number 3, got %d", items[0].WorkspaceItemNumber)
	}
}

// WI-1417: a text query that merely ends in "-<digits>" (a date, a version)
// must still match title/description text. Right-anchored key routing made
// such queries silently return nothing whenever the digit-prefix segment was
// not a real workspace key. Multi-dash queries now match the key OR the text;
// single-dash KEY-NUMBER lookups stay key-only.
func TestSearchMultiDashDateQueryStillMatchesText(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	seedDashedKeyWorkspace(t, tdb, map[string]string{
		"Fix release-2026-09-18 notes": "plain text body",
		"Unrelated item":               "must not match",
	})

	repo := repository.NewItemRepository(tdb.GetDatabase())
	items, _, err := repo.Search("release-2026-09-18", []int{dashedKeyWorkspaceID}, repository.PaginationParams{Limit: 50})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(items) != 1 || items[0].Title != "Fix release-2026-09-18 notes" {
		t.Fatalf("multi-dash text search returned %#v, want only the date-titled item", items)
	}
}

// A multi-dash query that IS a rendered item key still resolves through search.
func TestSearchMultiDashRenderedKeyStillResolves(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	seedDashedKeyWorkspace(t, tdb, map[string]string{
		"Findable by key": "key lookup fixture",
		"Other item":      "must not match",
	})

	repo := repository.NewItemRepository(tdb.GetDatabase())
	items, _, err := repo.Search("JOHN-DOE-3", []int{dashedKeyWorkspaceID}, repository.PaginationParams{Limit: 50})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(items) != 1 || items[0].WorkspaceItemNumber != 3 {
		t.Fatalf("search by rendered key returned %#v, want exactly item number 3", items)
	}
}

// WI-1382: an inactive personal workspace is reactivated on access — the
// handler and the permission cache (active = true) must agree again.
func TestGetOrCreatePersonalWorkspaceReactivatesInactiveWorkspace(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	req := testutils.CreateJSONRequest(t, "POST", "/api/workspaces/personal", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetOrCreatePersonalWorkspace, req, nil)
	rr.AssertStatusCode(http.StatusCreated)
	var created models.Workspace
	rr.AssertJSONResponse(&created)

	// Deprovisioning-style deactivation (SCIM cascade).
	if _, err := tdb.Exec(`UPDATE workspaces SET active = false WHERE is_personal = true AND owner_id = 1`); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	// Access after reactivation: same workspace, active again, 200 (not a new one).
	req2 := testutils.CreateJSONRequest(t, "POST", "/api/workspaces/personal", nil)
	rr2 := testutils.ExecuteAuthenticatedRequest(t, handler.GetOrCreatePersonalWorkspace, req2, nil)
	rr2.AssertStatusCode(http.StatusOK)
	var ws models.Workspace
	rr2.AssertJSONResponse(&ws)
	if ws.ID != created.ID {
		t.Errorf("expected reactivation of workspace %d, got %d", created.ID, ws.ID)
	}
	if !ws.Active {
		t.Errorf("GetOrCreatePersonalWorkspace returned a deactivated workspace (active=%v)", ws.Active)
	}

	var count int
	if err := tdb.QueryRow(`SELECT COUNT(*) FROM workspaces WHERE is_personal = TRUE AND owner_id = 1`).Scan(&count); err != nil {
		t.Fatalf("count personal ws: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected exactly 1 personal workspace row, got %d", count)
	}
}

// WI-1383: new personal workspace keys derive from the owner ID and satisfy
// the key contract.
func TestGetOrCreatePersonalWorkspaceUsesOwnerDerivedKey(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	handler := newWorkspaceHandlerForSettings(t, tdb)

	req := testutils.CreateJSONRequest(t, "POST", "/api/workspaces/personal", nil)
	rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetOrCreatePersonalWorkspace, req, nil)
	rr.AssertStatusCode(http.StatusCreated)

	var ws models.Workspace
	rr.AssertJSONResponse(&ws)
	if ws.Key != "P1" {
		t.Errorf("expected owner-derived key P1, got %q", ws.Key)
	}
}

// WI-1381: the schema guarantees at most one personal workspace per owner.
func TestSchemaRejectsDuplicatePersonalWorkspacesPerOwner(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)

	_, err := tdb.Exec(`
		INSERT INTO workspaces (name, key, active, is_personal, owner_id)
		VALUES ('First Todo List', 'TODO1', true, true, 1)
	`)
	if err != nil {
		t.Fatalf("first personal workspace: %v", err)
	}
	_, err = tdb.Exec(`
		INSERT INTO workspaces (name, key, active, is_personal, owner_id)
		VALUES ('Second Todo List', 'TODO2', true, true, 1)
	`)
	if err == nil {
		t.Fatal("schema accepted a second personal workspace for the same owner; expected a uniqueness violation on (owner_id) where is_personal")
	}
}

// WI-1387: a NULL is_personal row must be treated as non-personal, not error.
func TestNullIsPersonalTreatedAsShared(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)

	// Legacy/corruption fixture: the column is nullable in the schema and old
	// rows may predate it. The API cannot create this state.
	_, err := tdb.Exec(`
		INSERT INTO workspaces (id, name, key, active, is_personal)
		VALUES (3, 'Legacy', 'LEG', true, NULL)
	`)
	if err != nil {
		t.Fatalf("seed null-is-personal workspace: %v", err)
	}

	if _, err := repository.NewWorkspaceRepository(tdb.GetDatabase()).FindMissingOrPersonal([]int{3}); err != nil {
		t.Errorf("FindMissingOrPersonal errored on NULL is_personal: %v", err)
	}
	if _, err := validation.NewItemFieldValidator(tdb.GetDatabase()).IsPersonalWorkspace(3); err != nil {
		t.Errorf("IsPersonalWorkspace errored on NULL is_personal: %v", err)
	}
}

// WI-1391: the fallback edit check must return (false, nil) for a user with
// no role — not a sql.ErrNoRows error.
func TestCanEditWorkspaceFallbackReturnsFalseWithoutRole(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)

	if _, err := tdb.Exec(`
		INSERT INTO users (id, email, username, first_name, last_name, password_hash, is_active)
		VALUES (2, 'norole@example.com', 'norole', 'No', 'Role', '$2a$10$hash', true)
		ON CONFLICT DO NOTHING
	`); err != nil {
		t.Fatalf("seed role-less user: %v", err)
	}

	a := authz.New(tdb.GetDatabase(), nil)
	allowed, err := a.CanEditWorkspace(2, 1)
	if err != nil {
		t.Fatalf("CanEditWorkspace for role-less user returned error: %v", err)
	}
	if allowed {
		t.Error("CanEditWorkspace for role-less user returned true; want false")
	}
}

// WI-1384: SCIM deprovision must not leave an active personal workspace or a
// running Todoist sync behind.
func TestSCIMDeprovisionStopsPersonalWorkspaceAndTodoistSync(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)

	if _, err := tdb.Exec(`
		INSERT INTO workspaces (name, key, active, is_personal, owner_id)
		VALUES ('My Todo List', 'MINE1', true, true, 1)
	`); err != nil {
		t.Fatalf("seed personal workspace: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO integration_providers (id, slug, name, provider_type, enabled)
		VALUES ('prov-todoist', 'todoist', 'Todoist', 'todoist', true)
	`); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO todoist_sync_config (id, user_id, integration_provider_id, personal_workspace_id, enabled)
		VALUES ('cfg-1', '1', 'prov-todoist', 1, true)
	`); err != nil {
		t.Fatalf("seed todoist config: %v", err)
	}

	scim := repository.NewSCIMRepository(tdb.GetDatabase())
	if _, err := scim.TombstoneUser(1); err != nil {
		t.Fatalf("tombstone user: %v", err)
	}

	var wsActive, syncEnabled bool
	if err := tdb.QueryRow(`SELECT active FROM workspaces WHERE is_personal = true AND owner_id = 1`).Scan(&wsActive); err == nil && wsActive {
		t.Error("personal workspace is still active after SCIM tombstone")
	}
	if err := tdb.QueryRow(`SELECT enabled FROM todoist_sync_config WHERE user_id = '1'`).Scan(&syncEnabled); err == nil && syncEnabled {
		t.Error("todoist sync config is still enabled after SCIM tombstone")
	}
}

// WI-1384 (defense in depth): the poller skips enabled configs whose personal
// workspace is inactive, so a missed cascade cannot resurrect a sync.
func TestTodoistPollerSkipsInactivePersonalWorkspace(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)

	if _, err := tdb.Exec(`
		INSERT INTO workspaces (id, name, key, active, is_personal, owner_id)
		VALUES (2, 'My Todo List', 'MINE1', false, true, 1)
	`); err != nil {
		t.Fatalf("seed personal workspace: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO integration_providers (id, slug, name, provider_type, enabled)
		VALUES ('prov-todoist', 'todoist', 'Todoist', 'todoist', true)
	`); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO todoist_sync_config (id, user_id, integration_provider_id, personal_workspace_id, enabled)
		VALUES ('cfg-1', '1', 'prov-todoist', 2, true)
	`); err != nil {
		t.Fatalf("seed todoist config: %v", err)
	}

	configs, err := repository.NewTodoistSyncRepository(tdb.GetDatabase()).ListEnabledConfigs()
	if err != nil {
		t.Fatalf("list enabled configs: %v", err)
	}
	if len(configs) != 0 {
		t.Errorf("poller returned %d configs for an inactive personal workspace; want 0", len(configs))
	}
}

// WI-1380: the scheduled-items calendar surface honors exclude_personal.
func TestGetScheduledItemsExcludesPersonal(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	permService, actTracker, notifService := createTestServices(t, *tdb)
	handler := NewItemHandler(tdb.GetDatabase(), permService, actTracker, notifService)

	// Personal workspace owned by the seeded user, with one scheduled item.
	if _, err := tdb.Exec(`
		INSERT INTO workspaces (id, name, key, active, is_personal, owner_id)
		VALUES (2, 'My Todo List', 'MINE1', true, true, 1)
	`); err != nil {
		t.Fatalf("seed personal workspace: %v", err)
	}
	entries := []map[string]any{{
		"user_id":        1,
		"workspace_id":   2,
		"scheduled_date": "2026-04-20",
	}}
	blob, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal calendar: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO items (title, description, workspace_id, status_id, priority_id, item_type_id, workspace_item_number, creator_id, frac_index, calendar_data)
		VALUES ('Personal scheduled', 'x', 2, ?, 1, 1, 1, 1, 'a0', ?)
	`, data.StatusID, string(blob)); err != nil {
		t.Fatalf("seed personal item: %v", err)
	}

	// Shared workspace item scheduled for the same day.
	sharedEntries := []map[string]any{{
		"user_id":        1,
		"workspace_id":   data.WorkspaceID,
		"scheduled_date": "2026-04-20",
	}}
	sharedBlob, err := json.Marshal(sharedEntries)
	if err != nil {
		t.Fatalf("marshal calendar: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO items (title, description, workspace_id, status_id, priority_id, item_type_id, workspace_item_number, creator_id, frac_index, calendar_data)
		VALUES ('Shared scheduled', 'x', ?, ?, 1, 1, 1, 1, 'a1', ?)
	`, data.WorkspaceID, data.StatusID, string(sharedBlob)); err != nil {
		t.Fatalf("seed shared item: %v", err)
	}

	run := func(t *testing.T, query string) map[string][]map[string]any {
		t.Helper()
		req := testutils.CreateJSONRequest(t, "GET", "/api/calendar/scheduled-items"+query, nil)
		rr := testutils.ExecuteAuthenticatedRequest(t, handler.GetScheduledItems, req, nil)
		rr.AssertStatusCode(http.StatusOK)
		var document map[string][]map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &document); err != nil {
			t.Fatalf("decode scheduled items: %v", err)
		}
		return document
	}

	excluded := run(t, "?exclude_personal=true")
	for _, entries := range excluded {
		for _, entry := range entries {
			if int(entry["id"].(float64)) == 1 {
				t.Errorf("scheduled items returned personal item 1 despite exclude_personal=true: %v", entries)
			}
		}
	}

	included := run(t, "")
	found := false
	for _, entries := range included {
		for _, entry := range entries {
			if int(entry["id"].(float64)) == 1 {
				found = true
			}
		}
	}
	if !found {
		t.Error("scheduled items baseline missing personal item 1 without exclusion")
	}
}
