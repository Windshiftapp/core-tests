package v1_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/auth"
	"windshift/internal/restapi/v1/middleware"
)

func TestV1PageExtendedRoutes_ScopeGate(t *testing.T) {
	routes := []struct {
		method string
		path   string
		scope  string
	}{
		{http.MethodGet, "/workspaces/{id}/pages/{pageId}/history/{revisionId}", "pages:read"},
		{http.MethodPost, "/workspaces/{id}/pages/{pageId}/history/{revisionId}/restore", "pages:write"},
		{http.MethodGet, "/workspaces/{id}/pages/{pageId}/permissions", "pages:read"},
		{http.MethodPost, "/workspaces/{id}/pages/{pageId}/permissions", "pages:write"},
		{http.MethodDelete, "/workspaces/{id}/pages/{pageId}/permissions/{permissionId}", "pages:write"},
		{http.MethodPatch, "/workspaces/{id}/pages/{pageId}/inheritance", "pages:write"},
	}

	ba := middleware.NewBearerAuthWithPermissions(&auth.TokenManager{}, nil)
	sentinel := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for _, r := range routes {
		t.Run(fmt.Sprintf("%s %s requires %s", r.method, r.path, r.scope), func(t *testing.T) {
			handler := ba.RequirePermission(r.scope)(sentinel)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, withTokenScopes(r.method, "items:read"))
			if rec.Code != http.StatusForbidden {
				t.Errorf("token without %s: want 403, got %d body=%s", r.scope, rec.Code, rec.Body.String())
			}

			rec = httptest.NewRecorder()
			handler.ServeHTTP(rec, withTokenScopes(r.method, r.scope))
			if rec.Code != http.StatusOK {
				t.Errorf("token with %s: want 200, got %d body=%s", r.scope, rec.Code, rec.Body.String())
			}
		})
	}
}
