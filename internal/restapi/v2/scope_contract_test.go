//go:build test

package v2

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestV2RouteScopesAreGrantable(t *testing.T) {
	if err := validateRouteScopes(buildRoutes(Deps{})); err != nil {
		t.Fatalf("v2 route scope contract is invalid: %v", err)
	}
}

func TestV2RouteScopeValidationRejectsUnknownScope(t *testing.T) {
	routes := []route{{Route: Route{
		Method:   http.MethodGet,
		Path:     "/example",
		Auth:     AuthAuthenticated,
		Exposure: ExposureBoth,
		Scopes:   []string{"unknown:read"},
	}}}

	err := validateRouteScopes(routes)
	if err == nil || !strings.Contains(err.Error(), "invalid scopes: unknown:read") {
		t.Fatalf("validateRouteScopes() error = %v, want unknown-scope error", err)
	}
}

func TestV2RouteScopeValidationRejectsUnknownAlternativeScope(t *testing.T) {
	routes := []route{{Route: Route{
		Method:    http.MethodGet,
		Path:      "/example",
		Auth:      AuthAuthenticated,
		Exposure:  ExposureBoth,
		ScopesAny: []string{"unknown:read"},
	}}}

	err := validateRouteScopes(routes)
	if err == nil || !strings.Contains(err.Error(), "invalid scopes: unknown:read") {
		t.Fatalf("validateRouteScopes() error = %v, want unknown-scope error", err)
	}
}

func TestV2RouteScopeValidationRejectsInvalidExposureMetadata(t *testing.T) {
	tests := []struct {
		name     string
		route    Route
		wantText string
	}{
		{
			name:     "unknown exposure",
			route:    Route{Method: http.MethodGet, Path: "/example", Auth: AuthAuthenticated, Exposure: Exposure("unknown")},
			wantText: "unknown exposure",
		},
		{
			name:     "session route with token scope",
			route:    Route{Method: http.MethodGet, Path: "/example", Auth: AuthAuthenticated, Exposure: ExposureSession, Scopes: []string{"items:read"}},
			wantText: "session-only route",
		},
		{
			name:     "session route with alternative token scope",
			route:    Route{Method: http.MethodGet, Path: "/example", Auth: AuthAuthenticated, Exposure: ExposureSession, ScopesAny: []string{"items:read"}},
			wantText: "session-only route",
		},
		{
			name:     "authenticated route without any token scope",
			route:    Route{Method: http.MethodGet, Path: "/example", Auth: AuthAuthenticated, Exposure: ExposureBoth},
			wantText: "declares no token scope",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateRouteScopes([]route{{Route: test.route}})
			if err == nil || !strings.Contains(err.Error(), test.wantText) {
				t.Fatalf("validateRouteScopes() error = %v, want %q", err, test.wantText)
			}
		})
	}
}

func TestV2RoutesDeclareDeliberateExposure(t *testing.T) {
	inventory := Inventory()
	for _, item := range inventory {
		switch item.Exposure {
		case ExposureBoth, ExposureSession, ExposureBearer:
		default:
			t.Errorf("%s %s exposure = %q", item.Method, item.Path, item.Exposure)
		}
	}

	tests := []struct {
		method       string
		path         string
		wantExposure Exposure
		wantScopes   []string
	}{
		{method: http.MethodGet, path: "/users/me", wantExposure: ExposureBoth, wantScopes: []string{"users:read"}},
		{method: http.MethodGet, path: "/admin/groups", wantExposure: ExposureSession},
		{method: http.MethodPatch, path: "/admin/users/{user_id}", wantExposure: ExposureSession},
		{method: http.MethodPost, path: "/asset-sets", wantExposure: ExposureSession},
		{method: http.MethodPut, path: "/asset-types/{asset_type_id}/fields", wantExposure: ExposureSession},
		{method: http.MethodPost, path: "/asset-sets/{asset_set_id}/assets", wantExposure: ExposureBoth, wantScopes: []string{"assets:write"}},
		{method: http.MethodPost, path: "/asset-sets/{asset_set_id}/import/start", wantExposure: ExposureBoth, wantScopes: []string{"assets:write"}},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			for _, item := range inventory {
				if item.Method == test.method && item.Path == test.path {
					if item.Exposure != test.wantExposure || !slices.Equal(item.Scopes, test.wantScopes) {
						t.Fatalf("exposure/scopes = %q/%v, want %q/%v", item.Exposure, item.Scopes, test.wantExposure, test.wantScopes)
					}
					return
				}
			}
			t.Fatal("route is missing")
		})
	}
}

func TestV2RoutesUseDomainScopes(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		want   []string
	}{
		{name: "automation read", method: http.MethodGet, path: "/workspaces/{workspace_id}/actions", want: []string{"actions:read"}},
		{name: "automation write", method: http.MethodPatch, path: "/workspaces/{workspace_id}/actions/{action_id}", want: []string{"actions:write"}},
		{name: "test read", method: http.MethodGet, path: "/workspaces/{workspace_id}/test-cases", want: []string{"tests:read"}},
		{name: "test write", method: http.MethodPost, path: "/workspaces/{workspace_id}/test-runs/{run_id}/end", want: []string{"tests:write"}},
		{name: "milestone delete", method: http.MethodDelete, path: "/milestones/{milestone_id}", want: []string{"milestones:delete"}},
		{name: "iteration write", method: http.MethodPost, path: "/iterations/{iteration_id}/complete", want: []string{"iterations:write"}},
		{name: "item delete", method: http.MethodDelete, path: "/items/{item_id}", want: []string{"items:delete"}},
		{name: "cross-resource link", method: http.MethodPost, path: "/links", want: []string{"links:write"}},
		{name: "test-case links", method: http.MethodGet, path: "/test-cases/{test_case_id}/links", want: []string{"tests:read"}},
	}

	inventory := Inventory()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, item := range inventory {
				if item.Method == test.method && item.Path == test.path {
					if !slices.Equal(item.Scopes, test.want) {
						t.Fatalf("%s %s scopes = %v, want %v", test.method, test.path, item.Scopes, test.want)
					}
					return
				}
			}
			t.Fatalf("route %s %s is missing", test.method, test.path)
		})
	}
}

func TestV2TimerRoutesUseTimeNamespace(t *testing.T) {
	inventory := Inventory()
	want := map[string]bool{
		http.MethodPost + " /time/timers":             false,
		http.MethodGet + " /time/timers/active":       false,
		http.MethodPost + " /time/timers/active/stop": false,
	}
	for _, item := range inventory {
		key := item.Method + " " + item.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
		if strings.HasPrefix(item.Path, "/timer/") {
			t.Errorf("legacy timer route remains: %s", key)
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("route is missing: %s", key)
		}
	}
}

func TestV2ItemWatchAndCascadeRoutesUseExplicitSemantics(t *testing.T) {
	inventory := Inventory()
	want := map[string]bool{
		http.MethodGet + " /items/{item_id}/watch":    false,
		http.MethodPut + " /items/{item_id}/watch":    false,
		http.MethodDelete + " /items/{item_id}/watch": false,
		http.MethodDelete + " /items/{item_id}":       false,
	}
	for _, item := range inventory {
		if strings.Contains(item.Path, "cascade-deletion") {
			t.Errorf("removed cascade alias remains: %s", item.Path)
		}
		key := item.Method + " " + item.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
		if item.Method == http.MethodPost && item.Path == "/items/{item_id}/watch" {
			t.Errorf("non-idempotent watch route remains: %s", key)
		}
		if item.Path == "/items/{item_id}/cascade" {
			t.Errorf("ambiguous cascade route remains: %s", key)
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("route is missing: %s", key)
		}
	}
}

func TestV2PlanningCollectionsEncodeOwnershipInPaths(t *testing.T) {
	inventory := Inventory()
	want := map[string]bool{
		http.MethodGet + " /milestones":                                    false,
		http.MethodPost + " /milestones":                                   false,
		http.MethodGet + " /workspaces/{workspace_id}/milestones":          false,
		http.MethodPost + " /workspaces/{workspace_id}/milestones":         false,
		http.MethodPost + " /milestones/reorder":                           false,
		http.MethodPost + " /workspaces/{workspace_id}/milestones/reorder": false,
		http.MethodGet + " /iterations":                                    false,
		http.MethodPost + " /iterations":                                   false,
		http.MethodGet + " /workspaces/{workspace_id}/iterations":          false,
		http.MethodPost + " /workspaces/{workspace_id}/iterations":         false,
	}
	for _, item := range inventory {
		key := item.Method + " " + item.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("route is missing: %s", key)
		}
	}
}
