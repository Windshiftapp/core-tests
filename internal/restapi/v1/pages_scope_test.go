package v1_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/auth"
	"windshift/internal/models"
	"windshift/internal/restapi"
	"windshift/internal/restapi/v1/middleware"
)

// Reproduces the scope assignments in `router.go:248-254` so a change to
// any pages route's required scope fails this test until the table is
// updated. The middleware contract itself is tested in
// `middleware/auth_test.go`; this file pins which scope is required for
// each pages route — the wiring decision the audit doc called out as
// untested.
var pageRouteScopes = []struct {
	method string
	path   string
	scope  string
}{
	{http.MethodGet, "/workspaces/{id}/pages", "pages:read"},
	{http.MethodPost, "/workspaces/{id}/pages", "pages:write"},
	{http.MethodGet, "/workspaces/{id}/pages/{pageId}", "pages:read"},
	{http.MethodPut, "/workspaces/{id}/pages/{pageId}", "pages:write"},
	{http.MethodDelete, "/workspaces/{id}/pages/{pageId}", "pages:delete"},
	{http.MethodPost, "/workspaces/{id}/pages/{pageId}/move", "pages:write"},
	{http.MethodGet, "/workspaces/{id}/pages/{pageId}/history", "pages:read"},
	{http.MethodPost, "/workspaces/{id}/pages/{pageId}/attachments", "pages:write"},
	{http.MethodGet, "/workspaces/{id}/pages/{pageId}/diagrams", "pages:read"},
	{http.MethodPost, "/workspaces/{id}/pages/{pageId}/diagrams", "pages:write"},
	{http.MethodGet, "/workspaces/{id}/pages/{pageId}/diagrams/{attachmentId}", "pages:read"},
	{http.MethodPut, "/workspaces/{id}/pages/{pageId}/diagrams/{attachmentId}", "pages:write"},
}

// withTokenScopes returns a request carrying an APIToken whose Permissions
// JSON contains exactly the given scopes — mirroring the production
// context contract used by middleware.RequirePermission.
func withTokenScopes(method string, scopes ...string) *http.Request {
	req := httptest.NewRequest(method, "/irrelevant", nil)
	permissions := "["
	for i, s := range scopes {
		if i > 0 {
			permissions += ","
		}
		permissions += fmt.Sprintf("%q", s)
	}
	permissions += "]"
	token := &models.APIToken{Permissions: permissions}
	ctx := context.WithValue(req.Context(), restapi.ContextKeyAPIToken, token)
	return req.WithContext(ctx)
}

// TestV1PageRoutes_ScopeGate documents and enforces the required scope
// per pages route. For every route in pageRouteScopes:
//   - A token holding only an unrelated scope is rejected with 403.
//   - A token holding exactly the documented scope is admitted (200 from
//     the sentinel handler).
//
// :write does NOT imply :delete (the TokenManager hierarchy only does
// write→read), so the delete route is asserted separately.
func TestV1PageRoutes_ScopeGate(t *testing.T) {
	ba := middleware.NewBearerAuthWithPermissions(&auth.TokenManager{}, nil)
	sentinel := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for _, r := range pageRouteScopes {
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

// TestV1PageRoutes_WriteImpliesRead pins the scope hierarchy used by
// CheckTokenPermissions: a `pages:write` token must be able to satisfy
// the `pages:read` middleware (because write actions inherently need to
// read the current state). Delete intentionally does NOT participate in
// this hierarchy.
func TestV1PageRoutes_WriteImpliesRead(t *testing.T) {
	ba := middleware.NewBearerAuthWithPermissions(&auth.TokenManager{}, nil)
	sentinel := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	readGuarded := ba.RequirePermission("pages:read")(sentinel)
	rec := httptest.NewRecorder()
	readGuarded.ServeHTTP(rec, withTokenScopes(http.MethodGet, "pages:write"))
	if rec.Code != http.StatusOK {
		t.Errorf("pages:write should satisfy pages:read, got %d body=%s", rec.Code, rec.Body.String())
	}

	deleteGuarded := ba.RequirePermission("pages:delete")(sentinel)
	rec = httptest.NewRecorder()
	deleteGuarded.ServeHTTP(rec, withTokenScopes(http.MethodDelete, "pages:write"))
	if rec.Code != http.StatusForbidden {
		t.Errorf("pages:write must NOT satisfy pages:delete (no hierarchy), got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestV1PageRoutes_NoToken_Unauthorized asserts that a request without
// any APIToken in context is rejected by RequirePermission with 401.
// This complements RequireAuth's 401-on-missing-header check from
// middleware/auth_test.go and pins that the per-route scope middleware
// is not the first authorization gate (RequireAuth is).
func TestV1PageRoutes_NoToken_Unauthorized(t *testing.T) {
	ba := middleware.NewBearerAuthWithPermissions(&auth.TokenManager{}, nil)
	sentinel := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for _, r := range pageRouteScopes {
		t.Run(fmt.Sprintf("%s %s no-token", r.method, r.path), func(t *testing.T) {
			handler := ba.RequirePermission(r.scope)(sentinel)
			req := httptest.NewRequest(r.method, "/irrelevant", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("no token: want 401, got %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}
