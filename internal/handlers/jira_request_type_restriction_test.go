//go:build test

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/auth"
	"windshift/internal/blobstore"
	"windshift/internal/database"
	"windshift/internal/jira"
	"windshift/internal/jiraimport"
	"windshift/internal/middleware"
)

func TestClassifyJiraRequestTypeRestrictionFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		status          string
		wantStatus      string
		wantDisposition string
		wantFailClosed  bool
	}{
		{name: "open is active", status: "OPEN", wantStatus: "OPEN", wantDisposition: "open", wantFailClosed: false},
		{name: "open lower case", status: "open", wantStatus: "OPEN", wantDisposition: "open", wantFailClosed: false},
		{name: "restricted", status: "RESTRICTED", wantStatus: "RESTRICTED", wantDisposition: "restricted", wantFailClosed: true},
		{name: "undocumented closed", status: "CLOSED", wantStatus: "CLOSED", wantDisposition: "unknown", wantFailClosed: true},
		{name: "missing status", status: "", wantStatus: "", wantDisposition: "unknown", wantFailClosed: true},
		{name: "whitespace only", status: "   ", wantStatus: "", wantDisposition: "unknown", wantFailClosed: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := classifyJiraRequestTypeRestriction(test.status)
			if got.SourceStatus != test.wantStatus || got.Disposition != test.wantDisposition || got.failClosed() != test.wantFailClosed {
				t.Fatalf("classify(%q) = %#v failClosed=%t, want status %q disposition %q failClosed %t",
					test.status, got, got.failClosed(), test.wantStatus, test.wantDisposition, test.wantFailClosed)
			}
		})
	}
}

func requestTypeFixture(t *testing.T) (database.Database, *JiraImportHandler, int, int) {
	t.Helper()
	db := newJiraServiceManagementTestDB(t)
	h := NewJiraImportHandler(db, "jira-jsm-restriction-test-secret", "", blobstore.Disabled())

	var workspaceID int
	if err := db.QueryRow(`
		INSERT INTO workspaces (name, key, active) VALUES ('Help Desk', 'HELP', true) RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	var itemTypeID int
	if err := db.QueryRow(`SELECT id FROM item_types ORDER BY id LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("find item type: %v", err)
	}
	return db, h, workspaceID, itemTypeID
}

func importedRequestTypeState(t *testing.T, db database.Database, id int) (active bool, metadata map[string]any) {
	t.Helper()
	if err := db.QueryRow(`SELECT is_active FROM request_types WHERE id = ?`, id).Scan(&active); err != nil {
		t.Fatalf("load request type %d active flag: %v", id, err)
	}
	var metadataJSON string
	if err := db.QueryRow(`
		SELECT metadata_json FROM jira_import_id_mappings
		WHERE job_id = 'job-jsm' AND entity_type = 'request_type' AND windshift_id = ?
	`, id).Scan(&metadataJSON); err != nil {
		t.Fatalf("load request type %d mapping metadata: %v", id, err)
	}
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		t.Fatalf("decode request type %d mapping metadata: %v", id, err)
	}
	return active, metadata
}

func TestEnsureJiraRequestTypesFailsClosedForRestrictedAndUnknownStatuses(t *testing.T) {
	db, h, workspaceID, itemTypeID := requestTypeFixture(t)
	channelID, err := h.ensureJiraPortal(context.Background(), "job-jsm",
		jira.JiraProject{ID: "10000", Key: "HELP", Name: "Help Desk", ProjectType: "service_desk"},
		jira.JiraServiceDesk{ID: "1", ProjectID: "10000", ProjectKey: "HELP"},
		workspaceID, 0)
	if err != nil {
		t.Fatalf("ensure Jira portal: %v", err)
	}

	requestTypeMap, err := h.ensureJiraRequestTypes("job-jsm", channelID, workspaceID, []jira.JiraServiceDeskRequestType{
		{ID: "1", Name: "Open form", IssueTypeID: "10003", ServiceDeskID: "1", RestrictionStatus: "OPEN"},
		{ID: "2", Name: "Restricted form", IssueTypeID: "10003", ServiceDeskID: "1", RestrictionStatus: "RESTRICTED"},
		{ID: "3", Name: "Unknown form", IssueTypeID: "10003", ServiceDeskID: "1", RestrictionStatus: "CLOSED"},
	}, map[string]int{"10003": itemTypeID})
	if err != nil {
		t.Fatalf("ensure Jira request types: %v", err)
	}

	expectations := []struct {
		jiraID      string
		wantActive  bool
		wantStatus  string
		wantDispose string
	}{
		{jiraID: "1", wantActive: true, wantStatus: "OPEN", wantDispose: jiraimport.RequestTypeDispositionActive},
		{jiraID: "2", wantActive: false, wantStatus: "RESTRICTED", wantDispose: jiraimport.RequestTypeDispositionDisabledNeedsReview},
		{jiraID: "3", wantActive: false, wantStatus: "CLOSED", wantDispose: jiraimport.RequestTypeDispositionDisabledNeedsReview},
	}
	for _, expectation := range expectations {
		id := requestTypeMap[expectation.jiraID]
		if id == 0 {
			t.Fatalf("request type %s was not imported", expectation.jiraID)
		}
		active, metadata := importedRequestTypeState(t, db, id)
		if active != expectation.wantActive {
			t.Fatalf("request type %s active = %t, want %t", expectation.jiraID, active, expectation.wantActive)
		}
		if metadata["restriction_status"] != expectation.wantStatus ||
			metadata["restriction_disposition"] != expectation.wantDispose {
			t.Fatalf("request type %s restriction metadata = %#v, want status %q disposition %q",
				expectation.jiraID, metadata, expectation.wantStatus, expectation.wantDispose)
		}
	}
}

func TestReimportRestrictedJiraRequestTypeDisablesPreviouslyActiveType(t *testing.T) {
	db, h, workspaceID, itemTypeID := requestTypeFixture(t)
	channelID, err := h.ensureJiraPortal(context.Background(), "job-jsm",
		jira.JiraProject{ID: "10000", Key: "HELP", Name: "Help Desk", ProjectType: "service_desk"},
		jira.JiraServiceDesk{ID: "1", ProjectID: "10000", ProjectKey: "HELP"},
		workspaceID, 0)
	if err != nil {
		t.Fatalf("ensure Jira portal: %v", err)
	}

	firstMap, err := h.ensureJiraRequestTypes("job-jsm", channelID, workspaceID, []jira.JiraServiceDeskRequestType{{
		ID: "2", Name: "Restricted form", IssueTypeID: "10003", ServiceDeskID: "1", RestrictionStatus: "OPEN",
	}}, map[string]int{"10003": itemTypeID})
	if err != nil {
		t.Fatalf("initial Jira request type import: %v", err)
	}
	if active, _ := importedRequestTypeState(t, db, firstMap["2"]); !active {
		t.Fatal("initially open request type was not active")
	}

	reimportMap, err := h.ensureJiraRequestTypes("job-jsm", channelID, workspaceID, []jira.JiraServiceDeskRequestType{{
		ID: "2", Name: "Restricted form", IssueTypeID: "10003", ServiceDeskID: "1", RestrictionStatus: "RESTRICTED",
	}}, map[string]int{"10003": itemTypeID})
	if err != nil {
		t.Fatalf("reimport Jira request type: %v", err)
	}
	if reimportMap["2"] != firstMap["2"] {
		t.Fatalf("reimport created a new request type: %d != %d", reimportMap["2"], firstMap["2"])
	}
	active, metadata := importedRequestTypeState(t, db, reimportMap["2"])
	if active {
		t.Fatal("request type stayed active after Jira restricted it")
	}
	if metadata["restriction_status"] != "RESTRICTED" ||
		metadata["restriction_disposition"] != jiraimport.RequestTypeDispositionDisabledNeedsReview {
		t.Fatalf("reimport restriction metadata = %#v, want restricted/needs-review", metadata)
	}
}

func TestReimportRestrictedJiraRequestTypePreservesConfiguredAudience(t *testing.T) {
	db, h, workspaceID, itemTypeID := requestTypeFixture(t)
	channelID, err := h.ensureJiraPortal(context.Background(), "job-jsm",
		jira.JiraProject{ID: "10000", Key: "HELP", Name: "Help Desk", ProjectType: "service_desk"},
		jira.JiraServiceDesk{ID: "1", ProjectID: "10000", ProjectKey: "HELP"},
		workspaceID, 0)
	if err != nil {
		t.Fatalf("ensure Jira portal: %v", err)
	}

	firstMap, err := h.ensureJiraRequestTypes("job-jsm", channelID, workspaceID, []jira.JiraServiceDeskRequestType{{
		ID: "2", Name: "Restricted form", IssueTypeID: "10003", ServiceDeskID: "1", RestrictionStatus: "RESTRICTED",
	}}, map[string]int{"10003": itemTypeID})
	if err != nil {
		t.Fatalf("initial Jira request type import: %v", err)
	}
	requestTypeID := firstMap["2"]

	// An administrator maps the restricted source type to a specific portal
	// audience and enables it.
	if _, err := db.ExecWrite(`
		UPDATE request_types SET visibility_org_ids = '[42]', is_active = true WHERE id = ?
	`, requestTypeID); err != nil {
		t.Fatalf("configure request type audience: %v", err)
	}

	if _, err := h.ensureJiraRequestTypes("job-jsm", channelID, workspaceID, []jira.JiraServiceDeskRequestType{{
		ID: "2", Name: "Restricted form", IssueTypeID: "10003", ServiceDeskID: "1", RestrictionStatus: "RESTRICTED",
	}}, map[string]int{"10003": itemTypeID}); err != nil {
		t.Fatalf("reimport Jira request type: %v", err)
	}
	active, metadata := importedRequestTypeState(t, db, requestTypeID)
	if !active {
		t.Fatal("administrator-configured audience was deactivated by reimport")
	}
	if metadata["restriction_disposition"] != jiraimport.RequestTypeDispositionAudienceConfigured {
		t.Fatalf("restriction disposition = %v, want %q", metadata["restriction_disposition"], jiraimport.RequestTypeDispositionAudienceConfigured)
	}
}

func TestImportedRestrictedJiraRequestTypeDeniedToUnrelatedPortalUser(t *testing.T) {
	db, h, workspaceID, itemTypeID := requestTypeFixture(t)
	channelID, err := h.ensureJiraPortal(context.Background(), "job-jsm",
		jira.JiraProject{ID: "10000", Key: "HELP", Name: "Help Desk", ProjectType: "service_desk"},
		jira.JiraServiceDesk{ID: "1", ProjectID: "10000", ProjectKey: "HELP"},
		workspaceID, 0)
	if err != nil {
		t.Fatalf("ensure Jira portal: %v", err)
	}
	requestTypeMap, err := h.ensureJiraRequestTypes("job-jsm", channelID, workspaceID, []jira.JiraServiceDeskRequestType{
		{ID: "1", Name: "Open form", IssueTypeID: "10003", ServiceDeskID: "1", RestrictionStatus: "OPEN"},
		{ID: "2", Name: "Restricted form", IssueTypeID: "10003", ServiceDeskID: "1", RestrictionStatus: "RESTRICTED"},
	}, map[string]int{"10003": itemTypeID})
	if err != nil {
		t.Fatalf("ensure Jira request types: %v", err)
	}

	var unrelatedUserID int
	if err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('unrelated@example.test', 'jira-unrelated', 'Unrelated', 'User', true) RETURNING id
	`).Scan(&unrelatedUserID); err != nil {
		t.Fatalf("create unrelated portal user: %v", err)
	}

	portalHandler := NewPortalHandler(db, nil, nil, nil, blobstore.Disabled())
	resolve := func(requestTypeID int) (*httptest.ResponseRecorder, bool) {
		req := httptest.NewRequest(http.MethodPost, "/api/portal/jira-help/submit", nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeySession, &auth.Session{UserID: unrelatedUserID}))
		rec := httptest.NewRecorder()
		_, ok := portalHandler.resolveVisibleRequestType(req.Context(), rec, req, channelID, requestTypeID)
		return rec, ok
	}

	openRec, openOK := resolve(requestTypeMap["1"])
	if !openOK || openRec.Code != http.StatusOK {
		t.Fatalf("open request type resolution = ok %t code %d, want ok/200", openOK, openRec.Code)
	}

	restrictedRec, restrictedOK := resolve(requestTypeMap["2"])
	if restrictedOK {
		t.Fatal("restricted request type resolved for an unrelated portal user")
	}
	if restrictedRec.Code != http.StatusNotFound {
		t.Fatalf("restricted request type status = %d, want 404", restrictedRec.Code)
	}
	var denial struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(restrictedRec.Body.Bytes(), &denial); err != nil {
		t.Fatalf("decode denial: %v; body=%s", err, restrictedRec.Body.String())
	}
	if denial.Error != "Request type not found" {
		t.Fatalf("denial error = %q, want %q", denial.Error, "Request type not found")
	}
}
