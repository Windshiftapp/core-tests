//go:build test

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/restapi"
	"windshift/internal/restapi/v1/dto"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

// WI-1909: ListTypes used to load each asset type's fields with its own query.
// It must now resolve every type's fields in one batched query.

func grantSystemAdminForListTypesTest(t *testing.T, db database.Database, userID int) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO user_global_permissions (user_id, permission_id, granted_by, granted_at)
		VALUES (?, (SELECT id FROM permissions WHERE permission_key = 'system.admin'), ?, ?)
	`, userID, userID, time.Now().UTC()); err != nil {
		t.Fatalf("grant system admin: %v", err)
	}
}

func TestListTypesLoadsFieldsInOneQuery(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	seed := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	grantSystemAdminForListTypesTest(t, db, seed.UserID)

	setID := testutils.InsertID(t, db,
		`INSERT INTO asset_management_sets (name) VALUES (?)`, "n+1 asset set")

	const typeCount = 4
	for i := 0; i < typeCount; i++ {
		typeID := testutils.InsertID(t, db,
			`INSERT INTO asset_types (set_id, name) VALUES (?, ?)`, setID, "Type "+strconv.Itoa(i))
		fieldID := testutils.InsertID(t, db,
			`INSERT INTO custom_field_definitions (name, field_type) VALUES (?, 'text')`, "Field "+strconv.Itoa(i))
		testutils.InsertID(t, db, `
			INSERT INTO asset_type_fields (asset_type_id, custom_field_id, is_required, display_order)
			VALUES (?, ?, false, 0)
		`, typeID, fieldID)
	}

	counting := testutils.CountQueries(db)
	config := services.DefaultPermissionCacheConfig()
	config.WarmupOnStartup = false
	permService, err := services.NewPermissionService(counting, config)
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = permService.Close() })

	assetRepo := repository.NewAssetRepository(counting)
	handler := &AssetHandler{
		BaseHandler: NewBaseHandler(counting, permService),
		repo:        assetRepo,
		assetPerm:   services.NewAssetPermissionService(assetRepo, permService),
	}

	req := httptest.NewRequest(http.MethodGet, "/rest/api/v1/asset-sets/"+strconv.Itoa(setID)+"/types", nil)
	req.SetPathValue("setId", strconv.Itoa(setID))
	req = req.WithContext(context.WithValue(req.Context(), restapi.ContextKeyUser, &models.User{ID: seed.UserID}))
	recorder := httptest.NewRecorder()

	counting.ResetQueries()
	handler.ListTypes(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var types []dto.AssetTypeResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &types); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, recorder.Body.String())
	}
	if len(types) != typeCount {
		t.Fatalf("returned %d types, want %d", len(types), typeCount)
	}
	for _, assetType := range types {
		if len(assetType.Fields) != 1 {
			t.Fatalf("type %d has %d fields, want 1", assetType.ID, len(assetType.Fields))
		}
	}

	if got := counting.CountQueriesContaining("WHERE atf.asset_type_id = ?"); got != 0 {
		t.Fatalf("handler still issues %d per-type field lookups", got)
	}
	if got := counting.CountQueriesContaining("WHERE atf.asset_type_id IN ("); got != 1 {
		t.Fatalf("batched field lookup ran %d times, want 1", got)
	}
}
