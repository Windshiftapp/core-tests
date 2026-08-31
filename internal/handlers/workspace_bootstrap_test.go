package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWorkspaceBootstrapRequiresAuthentication(t *testing.T) {
	handler := NewWorkspaceBootstrapHandler(&WorkspaceHandler{}, nil, nil, nil, nil)
	recorder := httptest.NewRecorder()

	handler.Get(recorder, httptest.NewRequest(http.MethodGet, "/api/workspaces/1/bootstrap", nil))

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}
