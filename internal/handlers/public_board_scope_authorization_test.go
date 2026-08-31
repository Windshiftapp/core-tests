//go:build test

package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

type publicBoardScopeFixture struct {
	tdb                *testutils.TestDB
	handler            *CollectionHandler
	userID             int
	workspaceID        int
	foreignWorkspaceID int
}

func newPublicBoardScopeFixture(t *testing.T) *publicBoardScopeFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)

	testFactory := factory.NewTestFactory(tdb.GetDatabase())
	foreignOwnerID, err := testFactory.CreateUser(nil)
	if err != nil {
		t.Fatalf("create foreign workspace owner: %v", err)
	}
	foreignWorkspaceID, err := testFactory.CreateWorkspace(factory.CreateWorkspaceOpts{
		Name:      "Foreign Workspace",
		Key:       "FOREIGN",
		CreatorID: foreignOwnerID,
	})
	if err != nil {
		t.Fatalf("create foreign workspace: %v", err)
	}

	newServiceSetup(t, tdb).GrantGlobal(data.UserID, models.PermissionPublicBoardManage)
	permService, _, _ := createTestServices(t, *tdb)
	return &publicBoardScopeFixture{
		tdb:                tdb,
		handler:            NewCollectionHandler(tdb.GetDatabase(), permService),
		userID:             data.UserID,
		workspaceID:        data.WorkspaceID,
		foreignWorkspaceID: foreignWorkspaceID,
	}
}

func (f *publicBoardScopeFixture) createCollection(t *testing.T, query string) models.Collection {
	t.Helper()
	req := testutils.CreateJSONRequest(t, http.MethodPost, "/api/collections", models.Collection{
		Name:    "Scoped collection",
		QLQuery: query,
	})
	rr := testutils.ExecuteAuthenticatedRequest(t, f.handler.Create, req, nil)
	rr.AssertStatusCode(http.StatusCreated)
	var collection models.Collection
	rr.AssertJSONResponse(&collection)
	return collection
}

func (f *publicBoardScopeFixture) enable(t *testing.T, collectionID int) *testutils.ResponseRecorder {
	t.Helper()
	req := testutils.CreateJSONRequest(t, http.MethodPut, "/api/collections/1/public", map[string]any{
		"is_public":   true,
		"public_slug": "scoped-board",
	})
	req.SetPathValue("id", testutils.IntToString(collectionID))
	return testutils.ExecuteAuthenticatedRequest(t, f.handler.UpdatePublicSharing, req, nil)
}

func (f *publicBoardScopeFixture) grantForeignWorkspaceAdmin(t *testing.T) {
	t.Helper()
	repo := repository.NewWorkspaceRoleRepository(f.tdb.GetDatabase())
	roles, err := repo.List()
	if err != nil {
		t.Fatalf("list workspace roles: %v", err)
	}
	for _, role := range roles {
		if role.Name != "Administrator" {
			continue
		}
		if err := repo.AssignToUser(f.userID, f.foreignWorkspaceID, role.ID, f.userID); err != nil {
			t.Fatalf("grant foreign workspace admin: %v", err)
		}
		return
	}
	t.Fatal("administrator role not found")
}

func TestCollectionHandler_EnablePublicBoardRequiresWorkspaceScope(t *testing.T) {
	fixture := newPublicBoardScopeFixture(t)
	collection := fixture.createCollection(t, `labels = "github"`)

	rr := fixture.enable(t, collection.ID)

	rr.AssertStatusCode(http.StatusBadRequest)
	if !strings.Contains(rr.Body.String(), "Public boards require a workspace scope in the collection query") {
		t.Fatalf("response body = %s", rr.Body.String())
	}
	stored, err := repository.NewCollectionRepository(fixture.tdb.GetDatabase()).GetModel(collection.ID)
	if err != nil {
		t.Fatalf("get collection after rejected enable: %v", err)
	}
	if stored.IsPublic {
		t.Fatal("collection became public after scope validation failed")
	}
}

func TestCollectionHandler_CreatePublicBoardRequiresWorkspaceScope(t *testing.T) {
	fixture := newPublicBoardScopeFixture(t)
	slug := "unscoped-create"
	req := testutils.CreateJSONRequest(t, http.MethodPost, "/api/collections", models.Collection{
		Name:       "Unscoped public collection",
		QLQuery:    `labels = "github"`,
		IsPublic:   true,
		PublicSlug: &slug,
	})

	rr := testutils.ExecuteAuthenticatedRequest(t, fixture.handler.Create, req, nil)

	rr.AssertStatusCode(http.StatusBadRequest)
	if !strings.Contains(rr.Body.String(), "Public boards require a workspace scope in the collection query") {
		t.Fatalf("response body = %s", rr.Body.String())
	}
}

func TestCollectionHandler_GenericUpdateCannotEnableUnscopedPublicBoard(t *testing.T) {
	fixture := newPublicBoardScopeFixture(t)
	collection := fixture.createCollection(t, `labels = "github"`)
	req := testutils.CreateJSONRequest(t, http.MethodPut, "/api/collections/1", map[string]any{
		"name":        collection.Name,
		"ql_query":    collection.QLQuery,
		"is_public":   true,
		"public_slug": "generic-unscoped",
	})
	req.SetPathValue("id", testutils.IntToString(collection.ID))

	rr := testutils.ExecuteAuthenticatedRequest(t, fixture.handler.Update, req, nil)

	rr.AssertStatusCode(http.StatusBadRequest)
	stored, err := repository.NewCollectionRepository(fixture.tdb.GetDatabase()).GetModel(collection.ID)
	if err != nil {
		t.Fatalf("get collection after rejected update: %v", err)
	}
	if stored.IsPublic {
		t.Fatal("generic update enabled an unscoped public board")
	}
}

func TestCollectionHandler_EnablePublicBoardRequiresAdminInEveryScopedWorkspace(t *testing.T) {
	fixture := newPublicBoardScopeFixture(t)
	collection := fixture.createCollection(t, `workspace IN ("Test Workspace", "Foreign Workspace")`)

	rr := fixture.enable(t, collection.ID)

	rr.AssertStatusCode(http.StatusForbidden)
	stored, err := repository.NewCollectionRepository(fixture.tdb.GetDatabase()).GetModel(collection.ID)
	if err != nil {
		t.Fatalf("get collection after rejected enable: %v", err)
	}
	if stored.IsPublic {
		t.Fatal("collection became public without admin access to every workspace")
	}
}

func TestCollectionHandler_EnablePublicBoardAcceptsAdminInEveryScopedWorkspace(t *testing.T) {
	fixture := newPublicBoardScopeFixture(t)
	fixture.grantForeignWorkspaceAdmin(t)
	collection := fixture.createCollection(t, `workspaceKey IN ("TEST", "FOREIGN")`)

	rr := fixture.enable(t, collection.ID)

	rr.AssertStatusCode(http.StatusOK)
	stored, err := repository.NewCollectionRepository(fixture.tdb.GetDatabase()).GetModel(collection.ID)
	if err != nil {
		t.Fatalf("get enabled collection: %v", err)
	}
	if !stored.IsPublic {
		t.Fatal("collection did not become public after all workspace checks passed")
	}
}

func TestCollectionHandler_EnablePublicBoardRejectsUnknownWorkspace(t *testing.T) {
	fixture := newPublicBoardScopeFixture(t)
	collection := fixture.createCollection(t, `workspaceKey = "MISSING"`)

	rr := fixture.enable(t, collection.ID)

	rr.AssertStatusCode(http.StatusBadRequest)
	if !strings.Contains(rr.Body.String(), "Public board query references an unknown workspace") {
		t.Fatalf("response body = %s", rr.Body.String())
	}
}

func TestCollectionHandler_PublicCollectionRejectsUnscopedQueryUpdate(t *testing.T) {
	fixture := newPublicBoardScopeFixture(t)
	collection := fixture.createCollection(t, `workspace_id = 1 AND labels = "github"`)
	fixture.enable(t, collection.ID).AssertStatusCode(http.StatusOK)

	req := testutils.CreateJSONRequest(t, http.MethodPut, "/api/collections/1", map[string]any{
		"name":     collection.Name,
		"ql_query": `labels = "github"`,
	})
	req.SetPathValue("id", testutils.IntToString(collection.ID))
	rr := testutils.ExecuteAuthenticatedRequest(t, fixture.handler.Update, req, nil)

	rr.AssertStatusCode(http.StatusBadRequest)
	stored, err := repository.NewCollectionRepository(fixture.tdb.GetDatabase()).GetModel(collection.ID)
	if err != nil {
		t.Fatalf("get collection after rejected update: %v", err)
	}
	if stored.QLQuery != `workspace_id = 1 AND labels = "github"` {
		t.Fatalf("stored query = %q, want original scoped query", stored.QLQuery)
	}
}

func TestPublicBoardFailsClosedForLegacyUnscopedCollection(t *testing.T) {
	fixture := newPublicBoardScopeFixture(t)
	collection := fixture.createCollection(t, `id > 0`)

	// Simulate a legacy row created before public-board scope validation existed.
	slug := "legacy-unscoped"
	if err := repository.NewCollectionRepository(fixture.tdb.GetDatabase()).UpdatePublicSharing(collection.ID, true, &slug); err != nil {
		t.Fatalf("enable legacy public collection: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/public/board/"+slug, nil)
	req.SetPathValue("slug", slug)
	rr := httptest.NewRecorder()
	NewPublicBoardHandler(fixture.tdb.GetDatabase(), nil, t.TempDir()).GetPublicBoard(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}
