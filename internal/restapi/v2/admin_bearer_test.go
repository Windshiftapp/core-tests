//go:build test

package v2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/models"
)

type deniedSystemAdmin struct{}

func (deniedSystemAdmin) IsSystemAdmin(int) (bool, error) { return false, nil }

func TestAdminBearerEndpointDenials(t *testing.T) {
	endpoints := []struct{ method, path, scope, body string }{
		{"GET", "/admin/audit-logs", "admin:audit-logs:read", ""},
		{"GET", "/admin/audit-logs/since", "admin:audit-logs:read", ""},
		{"GET", "/admin/api-tokens", "admin:api-tokens:read", ""},
		{"DELETE", "/admin/api-tokens/1", "admin:api-tokens:write", ""},
		{"GET", "/admin/object-translations/definitions", "admin:object-translations:read", ""},
		{"POST", "/admin/object-translations/resolve", "admin:object-translations:read", `{"locale":"de","targets":[]}`},
		{"GET", "/admin/object-translations/orphans", "admin:object-translations:read", ""},
		{"GET", "/admin/object-translations/canonical-differences", "admin:object-translations:read", ""},
		{"GET", "/admin/object-translations/priority/1", "admin:object-translations:read", ""},
		{"PUT", "/admin/object-translations/priority/1/name/de", "admin:object-translations:write", `{"value":"Hoch"}`},
		{"DELETE", "/admin/object-translations/priority/1/name/de", "admin:object-translations:write", ""},
	}
	for _, endpoint := range endpoints {
		for _, denial := range []struct {
			name, code, message         string
			status                      int
			authenticated, scope, admin bool
		}{
			{"anonymous", "authentication_required", "Bearer authentication is required", 401, false, false, false},
			{"admin without scope", "insufficient_permission", "Token lacks a required scope", 403, true, false, true},
			{"non-admin with scope", "insufficient_permission", "System administrator permission is required", 403, true, true, false},
		} {
			t.Run(endpoint.method+endpoint.path+"/"+denial.name, func(t *testing.T) {
				deps := Deps{Tokens: fakeTokens{user: &models.User{ID: 1}, token: &models.APIToken{ID: 1}, allow: denial.scope, wantScopes: []string{endpoint.scope}}, SystemAdmins: deniedSystemAdmin{}, Concurrency: fakeConcurrency{allow: true}, CORS: NewCORS(nil, false, true)}
				if denial.admin {
					deps.SystemAdmins = fakeSystemAdmins{}
				}
				mux := http.NewServeMux()
				registerMount(mux, restPrefix, buildRoutes(deps), deps, false)
				req := httptest.NewRequest(endpoint.method, restPrefix+endpoint.path, strings.NewReader(endpoint.body))
				if denial.authenticated {
					req.Header.Set("Authorization", "Bearer crw_valid")
				}
				req.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				mux.ServeHTTP(response, req)
				var body errorBody
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if response.Code != denial.status || body.Error.Code != denial.code || body.Error.Message != denial.message {
					t.Fatalf("response = %d %s, want %d %s: %s", response.Code, response.Body, denial.status, denial.code, denial.message)
				}
			})
		}
	}
}
