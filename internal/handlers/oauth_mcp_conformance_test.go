//go:build test

package handlers

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"windshift/internal/testutils"
)

const oauthMCPTestIssuer = "https://windshift.example/windshift"

func newOAuthMCPConformanceHandler(t *testing.T) *OAuthHandler {
	t.Helper()
	db := testutils.CreateTestDB(t, true)
	if !testutils.IsPostgres() {
		t.Cleanup(func() { _ = db.Close() })
	}
	return NewOAuthHandler(
		db,
		nil,
		nil,
		nil,
		nil,
		OAuthServerConfig{
			IssuerURL:  oauthMCPTestIssuer,
			MCPEnabled: true,
		},
	)
}

func decodeOAuthMCPJSON(t *testing.T, recorder *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v; body=%q", err, recorder.Body.String())
	}
	return body
}

func TestOAuthMCPMetadataUsesExternalIssuerAndContextPath(t *testing.T) {
	handler := newOAuthMCPConformanceHandler(t)

	protectedRecorder := httptest.NewRecorder()
	handler.ProtectedResourceMetadata(
		protectedRecorder,
		httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource/mcp", nil),
	)
	if protectedRecorder.Code != http.StatusOK {
		t.Fatalf("protected metadata status = %d, want 200", protectedRecorder.Code)
	}
	if got := protectedRecorder.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("protected metadata CORS = %q, want *", got)
	}
	if got := protectedRecorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("protected metadata cache-control = %q, want no-store", got)
	}
	protected := decodeOAuthMCPJSON(t, protectedRecorder)
	if got := protected["resource"]; got != oauthMCPTestIssuer+"/mcp" {
		t.Fatalf("protected resource = %v", got)
	}
	servers, ok := protected["authorization_servers"].([]interface{})
	if !ok || len(servers) != 1 || servers[0] != oauthMCPTestIssuer {
		t.Fatalf("authorization_servers = %#v", protected["authorization_servers"])
	}

	authorizationRecorder := httptest.NewRecorder()
	handler.AuthorizationServerMetadata(
		authorizationRecorder,
		httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil),
	)
	if authorizationRecorder.Code != http.StatusOK {
		t.Fatalf("authorization metadata status = %d, want 200", authorizationRecorder.Code)
	}
	authorization := decodeOAuthMCPJSON(t, authorizationRecorder)
	wantEndpoints := map[string]string{
		"issuer":                 oauthMCPTestIssuer,
		"authorization_endpoint": oauthMCPTestIssuer + "/oauth/authorize",
		"token_endpoint":         oauthMCPTestIssuer + "/api/oauth/token",
		"registration_endpoint":  oauthMCPTestIssuer + "/api/oauth/register",
	}
	for field, want := range wantEndpoints {
		if got := authorization[field]; got != want {
			t.Fatalf("%s = %v, want %q", field, got, want)
		}
	}
	if got := authorization["code_challenge_methods_supported"]; !containsJSONValue(got, "S256") {
		t.Fatalf("code_challenge_methods_supported = %#v, want S256", got)
	}
}

func containsJSONValue(raw interface{}, want string) bool {
	values, ok := raw.([]interface{})
	if !ok {
		return false
	}
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestDynamicRegistrationDefersResourceBindingToAuthorization(t *testing.T) {
	handler := newOAuthMCPConformanceHandler(t)
	registrationBody := `{
		"client_name": "Odysseus Windshift",
		"redirect_uris": ["https://odysseus.example/api/integrations/oauth/windshift/callback"],
		"grant_types": ["authorization_code", "refresh_token"],
		"response_types": ["code"],
		"token_endpoint_auth_method": "none",
		"scope": "mcp:access items:read items:write workspaces:read"
	}`
	registrationRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/register",
		strings.NewReader(registrationBody),
	)
	registrationRequest.Header.Set("Content-Type", "application/json")
	registrationRecorder := httptest.NewRecorder()

	handler.RegisterDynamicClient(registrationRecorder, registrationRequest)

	if registrationRecorder.Code != http.StatusCreated {
		t.Fatalf(
			"registration status = %d, want 201; body=%q",
			registrationRecorder.Code,
			registrationRecorder.Body.String(),
		)
	}
	var registered dynamicClientRegistrationResponse
	if err := json.NewDecoder(registrationRecorder.Body).Decode(&registered); err != nil {
		t.Fatalf("decode registration: %v", err)
	}
	if registered.ClientID == "" || registered.TokenEndpointAuthMethod != "none" {
		t.Fatalf("registration response = %+v", registered)
	}

	var clientType string
	var resourceURI *string
	if err := handler.db.QueryRow(
		"SELECT client_type, resource_uri FROM oauth_clients WHERE client_id = ?",
		registered.ClientID,
	).Scan(&clientType, &resourceURI); err != nil {
		t.Fatalf("query registered client: %v", err)
	}
	if clientType != "public" {
		t.Fatalf("client_type = %q, want public", clientType)
	}
	if resourceURI != nil && *resourceURI != "" {
		t.Fatalf("resource_uri = %q, want unbound dynamic client", *resourceURI)
	}

	verifier := "odysseus-public-client-verifier-0123456789"
	challengeBytes := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeBytes[:])
	authorizeQuery := url.Values{
		"client_id":             {registered.ClientID},
		"redirect_uri":          {registered.RedirectURIs[0]},
		"response_type":         {"code"},
		"scope":                 {"mcp:access items:read items:write workspaces:read"},
		"state":                 {"conformance-state"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"resource":              {oauthMCPTestIssuer + "/mcp"},
	}
	authorizeRequest := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/oauth/authorize/info?%s", authorizeQuery.Encode()),
		nil,
	)
	authorizeRecorder := httptest.NewRecorder()

	handler.AuthorizeInfo(authorizeRecorder, authorizeRequest)

	if authorizeRecorder.Code != http.StatusOK {
		t.Fatalf(
			"authorize status = %d, want 200; body=%q",
			authorizeRecorder.Code,
			authorizeRecorder.Body.String(),
		)
	}
	var authorizeInfo AuthorizeInfoResponse
	if err := json.NewDecoder(authorizeRecorder.Body).Decode(&authorizeInfo); err != nil {
		t.Fatalf("decode authorize response: %v", err)
	}
	if authorizeInfo.Resource != oauthMCPTestIssuer+"/mcp" {
		t.Fatalf("authorize resource = %q", authorizeInfo.Resource)
	}

	authorizeQuery.Set("resource", "https://other.example/mcp")
	rejectedRequest := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/oauth/authorize/info?%s", authorizeQuery.Encode()),
		nil,
	)
	rejectedRecorder := httptest.NewRecorder()
	handler.AuthorizeInfo(rejectedRecorder, rejectedRequest)
	if rejectedRecorder.Code != http.StatusBadRequest {
		t.Fatalf("foreign resource status = %d, want 400", rejectedRecorder.Code)
	}
	if !strings.Contains(rejectedRecorder.Body.String(), "resource is not registered") {
		t.Fatalf("foreign resource body = %q", rejectedRecorder.Body.String())
	}
}

func TestDynamicRegistrationRejectsUnsafeMetadata(t *testing.T) {
	handler := newOAuthMCPConformanceHandler(t)
	tests := []struct {
		name string
		body string
	}{
		{
			name: "non-loopback HTTP redirect",
			body: `{
				"redirect_uris": ["http://odysseus.example/callback"],
				"grant_types": ["authorization_code"],
				"response_types": ["code"],
				"token_endpoint_auth_method": "none"
			}`,
		},
		{
			name: "admin scope",
			body: `{
				"redirect_uris": ["https://odysseus.example/callback"],
				"grant_types": ["authorization_code"],
				"response_types": ["code"],
				"token_endpoint_auth_method": "none",
				"scope": "mcp:access admin:users"
			}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/oauth/register",
				strings.NewReader(test.body),
			)
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()

			handler.RegisterDynamicClient(recorder, request)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%q", recorder.Code, recorder.Body.String())
			}
			body := decodeOAuthMCPJSON(t, recorder)
			if body["error"] != "invalid_client_metadata" {
				t.Fatalf("error = %v, want invalid_client_metadata", body["error"])
			}
		})
	}
}

func TestOAuthResourceGrantBinding(t *testing.T) {
	resource := oauthMCPTestIssuer + "/mcp"
	tests := []struct {
		name      string
		stored    string
		requested string
		want      string
		wantError bool
	}{
		{name: "token exchange inherits authorization resource", stored: resource, want: resource},
		{name: "matching token resource", stored: resource, requested: resource, want: resource},
		{name: "resource-less authorization stays unbound"},
		{name: "cannot add resource during token exchange", requested: resource, wantError: true},
		{name: "cannot switch resource during token exchange", stored: resource, requested: "https://other.example/mcp", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := matchStoredOAuthResource(test.stored, test.requested)
			if test.wantError {
				if err == nil {
					t.Fatalf("matchStoredOAuthResource(%q, %q) unexpectedly succeeded", test.stored, test.requested)
				}
				return
			}
			if err != nil {
				t.Fatalf("matchStoredOAuthResource: %v", err)
			}
			if got != test.want {
				t.Fatalf("resource = %q, want %q", got, test.want)
			}
		})
	}
}
