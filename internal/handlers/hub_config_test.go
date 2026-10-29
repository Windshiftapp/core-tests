//go:build test

package handlers

import (
	"net/http"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// newHubHandlerForTest wires a HubHandler with a real permission service and
// an in-memory database. The test user's admin grant is established by the
// caller before the permission service is constructed.
func newHubHandlerForTest(t *testing.T, tdb *testutils.TestDB) *HubHandler {
	t.Helper()
	permissionService, _, _ := createTestServices(t, *tdb)
	return NewHubHandler(tdb.GetDatabase(), permissionService, nil)
}

// seedPortalChannel inserts an enabled portal channel and returns its ID.
func seedPortalChannel(t *testing.T, tdb *testutils.TestDB, slug string) int {
	t.Helper()
	var channelID int
	if err := tdb.QueryRow(`
		INSERT INTO channels (name, type, direction, status, config, public_slug)
		VALUES (?, 'portal', 'inbound', 'enabled', ?, ?) RETURNING id
	`, "Portal "+slug, `{"portal_slug":"`+slug+`"}`, slug).Scan(&channelID); err != nil {
		t.Fatalf("insert portal channel %s: %v", slug, err)
	}
	return channelID
}

// seedRequestType inserts an active request type. An empty visibilityGroupIDs
// leaves the column NULL (visible to everyone).
func seedRequestType(t *testing.T, tdb *testutils.TestDB, channelID, workspaceID int, name, visibilityGroupIDs string) int {
	t.Helper()
	var itemTypeID int
	if err := tdb.QueryRow(`INSERT INTO item_types (name) VALUES (?) RETURNING id`, name+" type").Scan(&itemTypeID); err != nil {
		t.Fatalf("insert item type: %v", err)
	}
	var visibility any
	if visibilityGroupIDs != "" {
		visibility = visibilityGroupIDs
	}
	var requestTypeID int
	if err := tdb.QueryRow(`
		INSERT INTO request_types (channel_id, name, item_type_id, workspace_id, is_active, visibility_group_ids)
		VALUES (?, ?, ?, ?, true, ?) RETURNING id
	`, channelID, name, itemTypeID, workspaceID, visibility).Scan(&requestTypeID); err != nil {
		t.Fatalf("insert request type %s: %v", name, err)
	}
	return requestTypeID
}

// TestHubConfigRoundTripPreservesSectionAssignments is the regression test for
// hub sections losing their subtitle, ordering, and portal assignments on
// save. The backend DTO previously dropped those fields, so a reload rendered
// sections with no portals (and crashed on `portal_ids.map`).
func TestHubConfigRoundTripPreservesSectionAssignments(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	tdb.SeedTestData(t)
	grantSystemAdmin(t, tdb, 1)

	channelID := seedPortalChannel(t, tdb, "support")
	handler := newHubHandlerForTest(t, tdb)

	config := models.PortalHubConfig{
		Title:  "Hub",
		Theme:  "light",
		Sections: []models.HubSection{
			{
				ID:           "section-devices",
				Title:        "Devices",
				Subtitle:     "Managed hardware",
				Content:      "<p>Devices we support</p>",
				Visible:      true,
				DisplayOrder: 2,
				PortalIDs:    []int{channelID},
			},
			{
				ID:           "section-empty",
				Title:        "Empty",
				Visible:      true,
				DisplayOrder: 3,
			},
		},
	}

	updateReq := testutils.CreateJSONRequest(t, http.MethodPut, "/api/hub/config", config)
	updateResp := testutils.ExecuteAuthenticatedRequest(t, handler.UpdateHubConfig, updateReq, nil)
	updateResp.AssertStatusCode(http.StatusOK)

	getReq := testutils.CreateJSONRequest(t, http.MethodGet, "/api/hub", nil)
	getResp := testutils.ExecuteAuthenticatedRequest(t, handler.GetHub, getReq, nil)
	getResp.AssertStatusCode(http.StatusOK)

	var got models.HubResponse
	getResp.AssertJSONResponse(&got)
	if len(got.Config.Sections) != 2 {
		t.Fatalf("sections = %d, want 2: %+v", len(got.Config.Sections), got.Config.Sections)
	}

	devices := got.Config.Sections[0]
	if devices.Subtitle != "Managed hardware" {
		t.Errorf("subtitle = %q, want %q", devices.Subtitle, "Managed hardware")
	}
	// Content is sanitized on write; assert it survives the round trip rather
	// than the exact markup the sanitizer emits.
	if devices.Content != "Devices we support" {
		t.Errorf("content = %q, want sanitized text to survive", devices.Content)
	}
	if devices.DisplayOrder != 2 {
		t.Errorf("display_order = %d, want 2", devices.DisplayOrder)
	}
	if len(devices.PortalIDs) != 1 || devices.PortalIDs[0] != channelID {
		t.Errorf("portal_ids = %v, want [%d]", devices.PortalIDs, channelID)
	}

	// An absent assignment list must serialize as [] so the frontend never
	// has to handle null.
	empty := got.Config.Sections[1]
	if empty.PortalIDs == nil {
		t.Errorf("empty section portal_ids = nil, want empty slice")
	}
	if len(empty.PortalIDs) != 0 {
		t.Errorf("empty section portal_ids = %v, want []", empty.PortalIDs)
	}
}

// TestHubPortalRequestTypeCountMatchesVisibleTypes is the regression test for
// hub portal cards reporting a raw active request-type count while returning a
// visibility-filtered list. A non-admin without the gating group must see a
// count that matches the list.
func TestHubPortalRequestTypeCountMatchesVisibleTypes(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	data := tdb.SeedTestData(t)

	channelID := seedPortalChannel(t, tdb, "support")
	seedRequestType(t, tdb, channelID, data.WorkspaceID, "Public request", "")
	seedRequestType(t, tdb, channelID, data.WorkspaceID, "Hidden request", "[42]")

	nonAdminID := newServiceSetup(t, tdb).CreateUser("member@example.test", "member", "Member", "User")

	permissionService, _, _ := createTestServices(t, *tdb)
	handler := NewHubHandler(tdb.GetDatabase(), permissionService, nil)

	getReq := testutils.CreateJSONRequest(t, http.MethodGet, "/api/hub", nil)
	getResp := testutils.ExecuteAuthenticatedRequest(t, handler.GetHub, getReq, testutils.TestUserWithID(nonAdminID))
	getResp.AssertStatusCode(http.StatusOK)

	var got models.HubResponse
	getResp.AssertJSONResponse(&got)
	if len(got.Portals) != 1 {
		t.Fatalf("portals = %d, want 1: %+v", len(got.Portals), got.Portals)
	}
	portal := got.Portals[0]
	if portal.RequestTypeCount != 1 {
		t.Errorf("request_type_count = %d, want 1 (only visible types)", portal.RequestTypeCount)
	}
	if len(portal.RequestTypes) != 1 || portal.RequestTypes[0].Name != "Public request" {
		t.Errorf("request_types = %+v, want only Public request", portal.RequestTypes)
	}
	if portal.RequestTypeCount != len(portal.RequestTypes) {
		t.Errorf("count %d does not match returned request types %d", portal.RequestTypeCount, len(portal.RequestTypes))
	}
}

// TestHubPortalRequestTypeCountShowsAllForAdmin guards the other half of the
// contract: an admin still sees the true total.
func TestHubPortalRequestTypeCountShowsAllForAdmin(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	data := tdb.SeedTestData(t)
	grantSystemAdmin(t, tdb, data.UserID)

	channelID := seedPortalChannel(t, tdb, "support")
	seedRequestType(t, tdb, channelID, data.WorkspaceID, "Public request", "")
	seedRequestType(t, tdb, channelID, data.WorkspaceID, "Hidden request", "[42]")

	handler := newHubHandlerForTest(t, tdb)

	getReq := testutils.CreateJSONRequest(t, http.MethodGet, "/api/hub", nil)
	getResp := testutils.ExecuteAuthenticatedRequest(t, handler.GetHub, getReq, nil)
	getResp.AssertStatusCode(http.StatusOK)

	var got models.HubResponse
	getResp.AssertJSONResponse(&got)
	if len(got.Portals) != 1 {
		t.Fatalf("portals = %d, want 1", len(got.Portals))
	}
	if got.Portals[0].RequestTypeCount != 2 {
		t.Errorf("admin request_type_count = %d, want 2", got.Portals[0].RequestTypeCount)
	}
}
