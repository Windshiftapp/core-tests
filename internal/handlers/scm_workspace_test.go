//go:build test

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/repository"
	"windshift/internal/restapi"
	"windshift/internal/testutils"
)

func newWorkspaceSCMTestHandler(t *testing.T, restrictionMode string) (*SCMWorkspaceHandler, *repository.SCMWorkspaceRepository, int, int) {
	t.Helper()

	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	workspaceID := seedWorkspace(t, db, "scm-test")
	var providerID int
	if restrictionMode == "restricted" {
		providerID = seedOAuthProvider(t, db, "gh-restricted", true, restrictionMode)
	} else {
		providerID = seedPATProvider(t, db, "gh-unrestricted")
	}

	repo := repository.NewSCMWorkspaceRepository(db)
	connectionID, err := repo.CreateConnection(workspaceID, providerID, "", "", nil)
	if err != nil {
		t.Fatalf("create workspace SCM connection: %v", err)
	}

	providerHandler := NewSCMProviderHandler(db, "test-session-secret", "http://test.local")
	handler := NewSCMWorkspaceHandler(
		repo,
		providerHandler.GetEncryption(),
		providerHandler,
		nil,
		nil,
		"http://test.local",
	)
	return handler, repo, workspaceID, connectionID
}

func workspaceSCMRequest(t *testing.T, method, path, body string, workspaceID, connectionID int) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.SetPathValue("id", testutils.IntToString(workspaceID))
	req.SetPathValue("connId", testutils.IntToString(connectionID))
	return req
}

func assertInsufficientPermission(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusForbidden, recorder.Body.String())
	}
	var response restapi.ErrorResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode forbidden response: %v", err)
	}
	if response.Code != restapi.ErrCodeInsufficientPermission {
		t.Fatalf("error code = %q, want %q", response.Code, restapi.ErrCodeInsufficientPermission)
	}
}

func TestSCMWorkspaceHandler_RestrictedProviderDeniesRepositoryOperations(t *testing.T) {
	handler, _, workspaceID, connectionID := newWorkspaceSCMTestHandler(t, "restricted")

	linkBody := `{"repository_external_id":"42","repository_name":"acme/project","repository_url":"https://github.com/acme/project"}`
	tests := []struct {
		name   string
		handle http.HandlerFunc
		method string
		path   string
		body   string
	}{
		{
			name:   "list available repositories",
			handle: handler.ListAvailableRepositories,
			method: http.MethodGet,
			path:   "/workspaces/1/scm-connections/1/repositories/available",
		},
		{
			name:   "link repository",
			handle: handler.LinkRepository,
			method: http.MethodPost,
			path:   "/workspaces/1/scm-connections/1/repositories",
			body:   linkBody,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			req := workspaceSCMRequest(t, tt.method, tt.path, tt.body, workspaceID, connectionID)

			tt.handle(recorder, req)

			assertInsufficientPermission(t, recorder)
		})
	}
}

func TestSCMWorkspaceHandler_LinkRepository_AllowsUnrestrictedProvider(t *testing.T) {
	handler, repo, workspaceID, connectionID := newWorkspaceSCMTestHandler(t, "unrestricted")
	recorder := httptest.NewRecorder()
	req := workspaceSCMRequest(
		t,
		http.MethodPost,
		"/workspaces/1/scm-connections/1/repositories",
		`{"repository_external_id":"42","repository_name":"acme/project","repository_url":"https://github.com/acme/project"}`,
		workspaceID,
		connectionID,
	)

	handler.LinkRepository(recorder, req)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	var response repository.SCMLinkedRepository
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode created repository: %v", err)
	}
	if response.RepositoryExternalID != "42" || response.RepositoryName != "acme/project" || response.DefaultBranch != "main" {
		t.Fatalf("created repository = %+v, want external id 42, acme/project, default branch main", response)
	}

	linked, err := repo.ListLinkedRepositories(connectionID)
	if err != nil {
		t.Fatalf("list linked repositories: %v", err)
	}
	if len(linked) != 1 || linked[0].RepositoryExternalID != "42" {
		t.Fatalf("persisted repositories = %+v, want one repository with external id 42", linked)
	}
}
