//go:build test

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/auth"
	"windshift/internal/middleware"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

type portalVisibilityDraftFixture struct {
	db                    *testutils.TestDB
	handler               *PortalHandler
	userID                int
	channelID             int
	visibleRequestTypeID  int
	hiddenRequestTypeID   int
	inactiveRequestTypeID int
}

func newPortalVisibilityDraftFixture(t *testing.T) *portalVisibilityDraftFixture {
	t.Helper()
	db := testutils.CreateTestDB(t, true)
	if !testutils.IsPostgres() {
		t.Cleanup(func() { _ = db.Close() })
	}

	var workspaceID, itemTypeID, userID, channelID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Portal visibility', 'PVIS') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO item_types (name) VALUES ('Portal visibility request') RETURNING id`).Scan(&itemTypeID); err != nil {
		t.Fatalf("insert item type: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('manager@example.test', 'portal-manager', 'Portal', 'Manager', true) RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	config := fmt.Sprintf(`{"portal_slug":"visibility","portal_workspace_ids":[%d]}`, workspaceID)
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status, config, public_slug)
		VALUES ('Visibility portal', 'portal', 'inbound', 'enabled', ?, 'visibility') RETURNING id
	`, config).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO channel_managers (channel_id, manager_type, manager_id, added_by)
		VALUES (?, 'user', ?, ?)
	`, channelID, userID, userID); err != nil {
		t.Fatalf("insert channel manager: %v", err)
	}

	insertRequestType := func(name string, active bool, visibility interface{}) int {
		t.Helper()
		var id int
		if err := db.QueryRow(`
			INSERT INTO request_types
				(channel_id, name, item_type_id, workspace_id, is_active, visibility_group_ids)
			VALUES (?, ?, ?, ?, ?, ?) RETURNING id
		`, channelID, name, itemTypeID, workspaceID, active, visibility).Scan(&id); err != nil {
			t.Fatalf("insert request type %s: %v", name, err)
		}
		return id
	}
	visibleID := insertRequestType("Visible", true, nil)
	hiddenID := insertRequestType("Hidden", true, `[999999]`)
	inactiveID := insertRequestType("Inactive", false, nil)

	repo := repository.NewPortalDraftRepository(db)
	identity := repository.DraftIdentity{UserID: &userID}
	for _, requestTypeID := range []int{visibleID, hiddenID, inactiveID} {
		if _, err := repo.Upsert(context.Background(), channelID, requestTypeID, identity, repository.PortalRequestDraftPayload{
			Title: "Draft", CurrentStep: 1,
		}); err != nil {
			t.Fatalf("seed draft for request type %d: %v", requestTypeID, err)
		}
	}

	return &portalVisibilityDraftFixture{
		db: db, handler: NewPortalHandler(db, nil, nil, nil, ""),
		userID: userID, channelID: channelID,
		visibleRequestTypeID: visibleID, hiddenRequestTypeID: hiddenID, inactiveRequestTypeID: inactiveID,
	}
}

func (f *portalVisibilityDraftFixture) request(method, path string, body *bytes.Reader) *http.Request {
	var request *http.Request
	if body == nil {
		request = httptest.NewRequest(method, path, nil)
	} else {
		request = httptest.NewRequest(method, path, body)
	}
	request.SetPathValue("slug", "visibility")
	session := &auth.Session{UserID: f.userID}
	return request.WithContext(context.WithValue(request.Context(), middleware.ContextKeySession, session))
}

func TestPortalManagerNormalViewUsesAudienceVisibility(t *testing.T) {
	fixture := newPortalVisibilityDraftFixture(t)
	request := fixture.request(http.MethodGet, "/api/portal/visibility/request-types", nil)
	recorder := httptest.NewRecorder()

	fixture.handler.GetRequestTypes(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var requestTypes []models.RequestType
	if err := json.NewDecoder(recorder.Body).Decode(&requestTypes); err != nil {
		t.Fatalf("decode request types: %v", err)
	}
	if len(requestTypes) != 1 || requestTypes[0].ID != fixture.visibleRequestTypeID {
		t.Fatalf("request types = %+v, want only visible request type %d", requestTypes, fixture.visibleRequestTypeID)
	}
}

func TestPortalDraftListOmitsRevokedAndInactiveForms(t *testing.T) {
	fixture := newPortalVisibilityDraftFixture(t)
	request := fixture.request(http.MethodGet, "/api/portal/visibility/drafts", nil)
	recorder := httptest.NewRecorder()

	fixture.handler.GetMyDrafts(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var drafts []repository.PortalRequestDraftSummary
	if err := json.NewDecoder(recorder.Body).Decode(&drafts); err != nil {
		t.Fatalf("decode drafts: %v", err)
	}
	if len(drafts) != 1 || drafts[0].RequestTypeID != fixture.visibleRequestTypeID {
		t.Fatalf("drafts = %+v, want only visible request type %d", drafts, fixture.visibleRequestTypeID)
	}
}

func TestPortalDraftSaveResumeAndDeactivateLifecycle(t *testing.T) {
	fixture := newPortalVisibilityDraftFixture(t)
	body := bytes.NewReader([]byte(fmt.Sprintf(`{
		"request_type_id": %d,
		"title": "Saved through the portal",
		"description": "Resume me",
		"custom_fields": {"virtual_note": "draft"},
		"current_step": 2
	}`, fixture.visibleRequestTypeID)))
	saveRequest := fixture.request(http.MethodPost, "/api/portal/visibility/drafts", body)
	saveRecorder := httptest.NewRecorder()

	fixture.handler.SaveDraft(saveRecorder, saveRequest)

	if saveRecorder.Code != http.StatusOK {
		t.Fatalf("save status = %d, want 200; body=%s", saveRecorder.Code, saveRecorder.Body.String())
	}

	resumeRequest := fixture.request(http.MethodGet, fmt.Sprintf("/api/portal/visibility/drafts/%d", fixture.visibleRequestTypeID), nil)
	resumeRequest.SetPathValue("requestTypeId", fmt.Sprintf("%d", fixture.visibleRequestTypeID))
	resumeRecorder := httptest.NewRecorder()
	fixture.handler.GetDraftByRequestType(resumeRecorder, resumeRequest)
	if resumeRecorder.Code != http.StatusOK {
		t.Fatalf("resume status = %d, want 200; body=%s", resumeRecorder.Code, resumeRecorder.Body.String())
	}
	var resumed map[string]interface{}
	if err := json.NewDecoder(resumeRecorder.Body).Decode(&resumed); err != nil {
		t.Fatalf("decode resumed draft: %v", err)
	}
	if resumed["title"] != "Saved through the portal" || resumed["current_step"] != float64(2) {
		t.Fatalf("resumed draft = %+v", resumed)
	}

	if _, err := fixture.db.ExecWrite(`UPDATE request_types SET is_active = false WHERE id = ?`, fixture.visibleRequestTypeID); err != nil {
		t.Fatalf("deactivate request type: %v", err)
	}

	listRequest := fixture.request(http.MethodGet, "/api/portal/visibility/drafts", nil)
	listRecorder := httptest.NewRecorder()
	fixture.handler.GetMyDrafts(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list after deactivation status = %d, want 200; body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	var drafts []repository.PortalRequestDraftSummary
	if err := json.NewDecoder(listRecorder.Body).Decode(&drafts); err != nil {
		t.Fatalf("decode drafts after deactivation: %v", err)
	}
	if len(drafts) != 0 {
		t.Fatalf("drafts after deactivation = %+v, want none", drafts)
	}

	resumeRecorder = httptest.NewRecorder()
	fixture.handler.GetDraftByRequestType(resumeRecorder, resumeRequest)
	if resumeRecorder.Code != http.StatusNotFound {
		t.Fatalf("resume after deactivation status = %d, want 404; body=%s", resumeRecorder.Code, resumeRecorder.Body.String())
	}

	deleteRequest := fixture.request(http.MethodDelete, fmt.Sprintf("/api/portal/visibility/drafts/%d", fixture.visibleRequestTypeID), nil)
	deleteRequest.SetPathValue("requestTypeId", fmt.Sprintf("%d", fixture.visibleRequestTypeID))
	deleteRecorder := httptest.NewRecorder()
	fixture.handler.DeleteDraft(deleteRecorder, deleteRequest)
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("delete after deactivation status = %d, want 204; body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}
}

func TestPortalDraftOwnerCanDeleteRevokedDraftsWithinChannel(t *testing.T) {
	fixture := newPortalVisibilityDraftFixture(t)

	for _, requestTypeID := range []int{fixture.hiddenRequestTypeID, fixture.inactiveRequestTypeID} {
		request := fixture.request(http.MethodDelete, fmt.Sprintf("/api/portal/visibility/drafts/%d", requestTypeID), nil)
		request.SetPathValue("requestTypeId", fmt.Sprintf("%d", requestTypeID))
		recorder := httptest.NewRecorder()

		fixture.handler.DeleteDraft(recorder, request)

		if recorder.Code != http.StatusNoContent {
			t.Fatalf("request type %d status = %d, want 204; body=%s", requestTypeID, recorder.Code, recorder.Body.String())
		}
		var count int
		if err := fixture.db.QueryRow(`
			SELECT COUNT(*) FROM portal_request_drafts
			WHERE channel_id = ? AND request_type_id = ? AND user_id = ?
		`, fixture.channelID, requestTypeID, fixture.userID).Scan(&count); err != nil {
			t.Fatalf("count draft %d: %v", requestTypeID, err)
		}
		if count != 0 {
			t.Fatalf("draft for request type %d still exists", requestTypeID)
		}
	}
}
