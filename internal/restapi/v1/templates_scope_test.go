package v1_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/auth"
	"windshift/internal/restapi/v1/middleware"
)

// Pins the required scope per work-item-template route (router.go, WI-438) so a
// change to any template route's scope fails this test until updated. Mirrors
// pages_scope_test.go.
var templateRouteScopes = []struct {
	method string
	path   string
	scope  string
}{
	{http.MethodGet, "/workspaces/{id}/templates", "item-templates:read"},
	{http.MethodPost, "/workspaces/{id}/templates", "item-templates:write"},
	{http.MethodGet, "/workspaces/{id}/templates/{templateId}", "item-templates:read"},
	{http.MethodPut, "/workspaces/{id}/templates/{templateId}", "item-templates:write"},
	{http.MethodDelete, "/workspaces/{id}/templates/{templateId}", "item-templates:write"},
}

// TestV1TemplateRoutes_ScopeGate asserts each template route requires its
// documented item-templates scope: an unrelated scope is rejected (403), the
// documented scope is admitted (200).
func TestV1TemplateRoutes_ScopeGate(t *testing.T) {
	ba := middleware.NewBearerAuthWithPermissions(&auth.TokenManager{}, nil)
	sentinel := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for _, r := range templateRouteScopes {
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

// TestV1TemplateRoutes_WriteImpliesRead pins that item-templates:write
// satisfies item-templates:read (write actions read current state first).
func TestV1TemplateRoutes_WriteImpliesRead(t *testing.T) {
	ba := middleware.NewBearerAuthWithPermissions(&auth.TokenManager{}, nil)
	sentinel := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	readGuarded := ba.RequirePermission("item-templates:read")(sentinel)
	rec := httptest.NewRecorder()
	readGuarded.ServeHTTP(rec, withTokenScopes(http.MethodGet, "item-templates:write"))
	if rec.Code != http.StatusOK {
		t.Errorf("item-templates:write should satisfy item-templates:read, got %d body=%s", rec.Code, rec.Body.String())
	}
}
