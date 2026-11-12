//go:build test

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/auth"
	"windshift/internal/blobstore"
	"windshift/internal/middleware"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
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
	requestItemID         int
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

	// A portal request whose description carries a page link; the portal must
	// degrade it when the page is not published through this portal.
	var requestItemID int
	if err := db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, description, channel_id, creator_id, frac_index)
		VALUES (?, 1, 'Request with page link', 'See [secret](page:424242) and @{Agent Smith#777} for details', ?, ?, 'a0')
		RETURNING id
	`, workspaceID, channelID, userID).Scan(&requestItemID); err != nil {
		t.Fatalf("insert portal request: %v", err)
	}
	if _, err := db.ExecWrite(`
		INSERT INTO comments (item_id, author_id, content, is_private)
		VALUES (?, ?, 'Replying about @{Agent Smith#777} and [doc](page:424242)', false)
	`, requestItemID, userID); err != nil {
		t.Fatalf("insert portal comment: %v", err)
	}

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
		db: db, handler: NewPortalHandler(db, nil, nil, nil, blobstore.Disabled()),
		userID: userID, channelID: channelID,
		visibleRequestTypeID: visibleID, hiddenRequestTypeID: hiddenID, inactiveRequestTypeID: inactiveID,
		requestItemID: requestItemID,
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

// TestPortalMyRequestsRewritesPageLinks pins the list/detail parity contract:
// GetRequestDetail rewrites description page links and strips embedded mention
// ids, and GetMyRequests feeds the same detail view, so it must apply the same
// rewriting.
func TestPortalMyRequestsRewritesPageLinks(t *testing.T) {
	fixture := newPortalVisibilityDraftFixture(t)
	request := fixture.request(http.MethodGet, "/api/portal/visibility/my-requests", nil)
	recorder := httptest.NewRecorder()

	fixture.handler.GetMyRequests(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var requests []services.PortalRequestSummary
	if err := json.NewDecoder(recorder.Body).Decode(&requests); err != nil {
		t.Fatalf("decode requests: %v", err)
	}
	var found bool
	for _, summary := range requests {
		if summary.ID != fixture.requestItemID {
			continue
		}
		found = true
		if strings.Contains(summary.Description, "](page:") {
			t.Fatalf("description leaked a page anchor: %q", summary.Description)
		}
		if strings.Contains(summary.Description, "#777}") {
			t.Fatalf("description leaked a mention user id: %q", summary.Description)
		}
		if !strings.Contains(summary.Description, "secret") || !strings.Contains(summary.Description, "Agent Smith") {
			t.Fatalf("description = %q, want link and mention text preserved", summary.Description)
		}
	}
	if !found {
		t.Fatalf("request %d missing from %+v", fixture.requestItemID, requests)
	}
}

// TestPortalRequestAttachmentsUnavailableWithoutStorage pins the disabled
// attachment surface: when no storage backend is configured the portal
// attachment service is nil, and the list/download handlers must answer 503
// instead of dereferencing it.
func TestPortalRequestAttachmentsUnavailableWithoutStorage(t *testing.T) {
	fixture := newPortalVisibilityDraftFixture(t)

	listRequest := fixture.request(http.MethodGet, fmt.Sprintf("/api/portal/visibility/requests/%d/attachments", fixture.requestItemID), nil)
	listRequest.SetPathValue("itemId", fmt.Sprintf("%d", fixture.requestItemID))
	listRecorder := httptest.NewRecorder()
	fixture.handler.GetRequestAttachments(listRecorder, listRequest)
	if listRecorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("list status = %d, want 503; body=%s", listRecorder.Code, listRecorder.Body.String())
	}

	downloadRequest := fixture.request(http.MethodGet, fmt.Sprintf("/api/portal/visibility/requests/%d/attachments/1/download", fixture.requestItemID), nil)
	downloadRequest.SetPathValue("itemId", fmt.Sprintf("%d", fixture.requestItemID))
	downloadRequest.SetPathValue("attachmentId", "1")
	downloadRecorder := httptest.NewRecorder()
	fixture.handler.DownloadRequestAttachment(downloadRecorder, downloadRequest)
	if downloadRecorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("download status = %d, want 503; body=%s", downloadRecorder.Code, downloadRecorder.Body.String())
	}
}

// TestPortalRequestTypesDoNotExposeVisibilityPrincipals pins the public
// contract for the request-type catalog: a portal customer (or any portal
// caller) must not receive the internal group ids / customer-org ids that gate
// visibility. Those are internal routing data, matching PublicAssetReport.
func TestPortalRequestTypesDoNotExposeVisibilityPrincipals(t *testing.T) {
	fixture := newPortalVisibilityDraftFixture(t)

	var groupID int
	if err := fixture.db.QueryRow(`INSERT INTO groups (name, is_active) VALUES ('Portal visibility group', true) RETURNING id`).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if _, err := fixture.db.ExecWrite(`INSERT INTO group_members (group_id, user_id) VALUES (?, ?)`, groupID, fixture.userID); err != nil {
		t.Fatalf("insert group member: %v", err)
	}
	var itemTypeID, workspaceID int
	if err := fixture.db.QueryRow(`SELECT item_type_id, workspace_id FROM request_types WHERE id = ?`, fixture.visibleRequestTypeID).Scan(&itemTypeID, &workspaceID); err != nil {
		t.Fatalf("load visible request type: %v", err)
	}
	var requestTypeID int
	if err := fixture.db.QueryRow(`
		INSERT INTO request_types (channel_id, name, item_type_id, workspace_id, is_active, visibility_group_ids)
		VALUES (?, 'Group visible', ?, ?, true, ?) RETURNING id
	`, fixture.channelID, itemTypeID, workspaceID, fmt.Sprintf("[%d]", groupID)).Scan(&requestTypeID); err != nil {
		t.Fatalf("insert group-visible request type: %v", err)
	}

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
	var found bool
	for _, rt := range requestTypes {
		if rt.ID != requestTypeID {
			continue
		}
		found = true
		if len(rt.VisibilityGroupIDs) != 0 || len(rt.VisibilityOrgIDs) != 0 {
			t.Fatalf("request type exposed visibility principals: groups=%v orgs=%v", rt.VisibilityGroupIDs, rt.VisibilityOrgIDs)
		}
	}
	if !found {
		t.Fatalf("group-visible request type %d missing from %+v", requestTypeID, requestTypes)
	}
}

// TestPortalRequestCommentsStripMentionIDs pins the comment-side external
// divide: an agent's mention token carries the internal user id, which portal
// customers must never receive in the raw markdown they render.
func TestPortalRequestCommentsStripMentionIDs(t *testing.T) {
	fixture := newPortalVisibilityDraftFixture(t)
	request := fixture.request(http.MethodGet, fmt.Sprintf("/api/portal/visibility/requests/%d/comments", fixture.requestItemID), nil)
	request.SetPathValue("itemId", fmt.Sprintf("%d", fixture.requestItemID))
	recorder := httptest.NewRecorder()

	fixture.handler.GetRequestComments(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var comments []services.PortalComment
	if err := json.NewDecoder(recorder.Body).Decode(&comments); err != nil {
		t.Fatalf("decode comments: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("comments = %+v, want exactly one", comments)
	}
	if strings.Contains(comments[0].Content, "#777}") {
		t.Fatalf("comment leaked a mention user id: %q", comments[0].Content)
	}
	if strings.Contains(comments[0].Content, "](page:") {
		t.Fatalf("comment leaked a page anchor: %q", comments[0].Content)
	}
	if !strings.Contains(comments[0].Content, "Agent Smith") {
		t.Fatalf("comment = %q, want the mention text preserved", comments[0].Content)
	}
}
