//go:build test

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/auth"
	"windshift/internal/blobstore"
	"windshift/internal/database"
	"windshift/internal/middleware"
	"windshift/internal/testutils"
)

type portalAssetFixture struct {
	handler   *PortalHandler
	db        database.Database
	channelID int
	setID     int
	inCQLID   int
	outCQLID  int
}

// seedPortalAssetFixture builds a portal with one asset set, two assets, and an
// asset report whose CQL only includes the first asset. Portal access is
// granted separately by each test so denial cases stay explicit.
func seedPortalAssetFixture(t *testing.T, cql string) portalAssetFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	insert := func(label, query string, args ...any) int {
		t.Helper()
		var id int
		if err := db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		return id
	}

	userID := insert("user", "INSERT INTO users (email, username, first_name, last_name) VALUES ('portal-asset@example.test', 'portal-asset', 'Pat', 'Portal') RETURNING id")
	channelConfig := `{"portal_slug":"asset-portal","portal_workspace_ids":[]}`
	channelID := insert("channel", `
		INSERT INTO channels (name, type, direction, status, config, public_slug)
		VALUES ('Asset portal', 'portal', 'inbound', 'enabled', ?, 'asset-portal') RETURNING id
	`, channelConfig)
	setID := insert("asset set", "INSERT INTO asset_management_sets (name, created_by) VALUES ('Portal inventory', ?) RETURNING id", userID)
	assetTypeID := insert("asset type", "INSERT INTO asset_types (set_id, name) VALUES (?, 'Device') RETURNING id", setID)
	inCQLID := insert("matching asset", "INSERT INTO assets (set_id, asset_type_id, title, asset_tag) VALUES (?, ?, 'Matching device', 'MATCH-1') RETURNING id", setID, assetTypeID)
	outCQLID := insert("hidden asset", "INSERT INTO assets (set_id, asset_type_id, title, asset_tag) VALUES (?, ?, 'Hidden device', 'HIDDEN-1') RETURNING id", setID, assetTypeID)
	insert("asset report", `
		INSERT INTO asset_reports (channel_id, asset_set_id, name, cql_query, is_active, column_config, run_mode)
		VALUES (?, ?, 'Inventory', ?, true, '["title","asset_tag"]', 'direct') RETURNING id
	`, channelID, setID, cql)

	sessionManager := auth.NewSessionManagerWithValidationCacheTTL(db, false, false, nil, "portal-asset-test-secret", "strict", auth.DefaultSessionValidationCacheTTL)
	return portalAssetFixture{
		handler:   NewPortalHandler(db, sessionManager, nil, nil, blobstore.Disabled()),
		db:        db,
		channelID: channelID,
		setID:     setID,
		inCQLID:   inCQLID,
		outCQLID:  outCQLID,
	}
}

func (f portalAssetFixture) grantPortalAccess(t *testing.T) {
	t.Helper()
	if _, err := f.db.ExecWrite("INSERT INTO asset_set_portal_access (set_id) VALUES (?)", f.setID); err != nil {
		t.Fatalf("grant portal access: %v", err)
	}
}

func (f portalAssetFixture) request(t *testing.T, method, target, body string, assetID int) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("slug", "asset-portal")
	if assetID > 0 {
		req.SetPathValue("id", fmt.Sprintf("%d", assetID))
	}
	return req.WithContext(context.WithValue(req.Context(), middleware.ContextKeySession, &auth.Session{UserID: 1}))
}

func TestGetPortalAssetRequiresPortalEnabledSet(t *testing.T) {
	f := seedPortalAssetFixture(t, "")

	recorder := httptest.NewRecorder()
	f.handler.GetPortalAsset(recorder, f.request(t, http.MethodGet, "/api/portal/asset-portal/assets/1", "", f.inCQLID))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 without a portal grant; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGetPortalAssetRequiresVisibleReport(t *testing.T) {
	f := seedPortalAssetFixture(t, "")
	f.grantPortalAccess(t)
	if _, err := f.db.ExecWrite("UPDATE asset_reports SET is_active = false"); err != nil {
		t.Fatalf("deactivate report: %v", err)
	}

	recorder := httptest.NewRecorder()
	f.handler.GetPortalAsset(recorder, f.request(t, http.MethodGet, "/api/portal/asset-portal/assets/1", "", f.inCQLID))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 without a visible report; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGetPortalAssetHonorsReportCQLAndHidesUnauthorizedIDs(t *testing.T) {
	f := seedPortalAssetFixture(t, `asset_tag = "MATCH-1"`)
	f.grantPortalAccess(t)

	allowed := httptest.NewRecorder()
	f.handler.GetPortalAsset(allowed, f.request(t, http.MethodGet, "/api/portal/asset-portal/assets/1", "", f.inCQLID))
	if allowed.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, want 200; body=%s", allowed.Code, allowed.Body.String())
	}
	var asset struct {
		ID       int    `json:"id"`
		Title    string `json:"title"`
		AssetTag string `json:"asset_tag"`
	}
	if err := json.NewDecoder(allowed.Body).Decode(&asset); err != nil {
		t.Fatalf("decode authorized asset: %v", err)
	}
	if asset.ID != f.inCQLID || asset.AssetTag != "MATCH-1" {
		t.Fatalf("authorized asset = %+v, want id %d", asset, f.inCQLID)
	}

	hidden := httptest.NewRecorder()
	f.handler.GetPortalAsset(hidden, f.request(t, http.MethodGet, "/api/portal/asset-portal/assets/1", "", f.outCQLID))
	if hidden.Code != http.StatusNotFound {
		t.Fatalf("asset outside CQL status = %d, want 404; body=%s", hidden.Code, hidden.Body.String())
	}

	missing := httptest.NewRecorder()
	f.handler.GetPortalAsset(missing, f.request(t, http.MethodGet, "/api/portal/asset-portal/assets/1", "", 999999))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing asset status = %d, want 404; body=%s", missing.Code, missing.Body.String())
	}
}

func TestPortalAssetSummariesOmitsUnauthorizedIDs(t *testing.T) {
	f := seedPortalAssetFixture(t, `asset_tag = "MATCH-1"`)
	f.grantPortalAccess(t)

	body := fmt.Sprintf(`{"ids":[%d,%d,999999]}`, f.inCQLID, f.outCQLID)
	recorder := httptest.NewRecorder()
	f.handler.PortalAssetSummaries(recorder, f.request(t, http.MethodPost, "/api/portal/asset-portal/assets/summaries", body, 0))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Assets []struct {
			ID int `json:"id"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode summaries: %v", err)
	}
	if len(response.Assets) != 1 || response.Assets[0].ID != f.inCQLID {
		t.Fatalf("summaries = %+v, want only asset %d", response.Assets, f.inCQLID)
	}
}

func TestSearchPortalAssetsScopesToVisibleReportCQL(t *testing.T) {
	f := seedPortalAssetFixture(t, `asset_tag = "MATCH-1"`)
	f.grantPortalAccess(t)

	recorder := httptest.NewRecorder()
	f.handler.SearchPortalAssets(recorder, f.request(t, http.MethodGet, "/api/portal/asset-portal/assets/search?q=device", "", 0))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Assets []struct {
			ID       int    `json:"id"`
			AssetTag string `json:"asset_tag"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	if len(response.Assets) != 1 || response.Assets[0].ID != f.inCQLID {
		t.Fatalf("search = %+v, want only asset %d in the report", response.Assets, f.inCQLID)
	}
}

func TestSearchPortalAssetsEmptyQueryReturnsNothing(t *testing.T) {
	f := seedPortalAssetFixture(t, "")
	f.grantPortalAccess(t)

	recorder := httptest.NewRecorder()
	f.handler.SearchPortalAssets(recorder, f.request(t, http.MethodGet, "/api/portal/asset-portal/assets/search?q=", "", 0))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"assets":[]`) {
		t.Fatalf("body = %s, want empty asset list", recorder.Body.String())
	}
}
