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

func TestDynamicRegistrationDoesNotImplicitlyBindMCPResource(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	handler := NewOAuthHandler(
		tdb.GetDatabase(),
		nil,
		nil,
		nil,
		nil,
		OAuthServerConfig{
			IssuerURL:  "https://windshift.example",
			MCPEnabled: true,
		},
	)

	registrationBody := `{
		"client_name": "Docmost Windshift",
		"redirect_uris": ["https://docmost.example/api/integrations/oauth/windshift/callback"],
		"grant_types": ["authorization_code", "refresh_token"],
		"response_types": ["code"],
		"token_endpoint_auth_method": "none",
		"scope": "items:read workspaces:read collections:read"
	}`
	registrationRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/register",
		strings.NewReader(registrationBody),
	)
	registrationRequest.Header.Set("Content-Type", "application/json")
	registrationResponse := httptest.NewRecorder()

	handler.RegisterDynamicClient(registrationResponse, registrationRequest)

	if registrationResponse.Code != http.StatusCreated {
		t.Fatalf(
			"registration status = %d, want %d (body %q)",
			registrationResponse.Code,
			http.StatusCreated,
			registrationResponse.Body.String(),
		)
	}

	var registered dynamicClientRegistrationResponse
	if err := json.NewDecoder(registrationResponse.Body).Decode(&registered); err != nil {
		t.Fatalf("decode registration response: %v", err)
	}
	if registered.ClientID == "" {
		t.Fatal("registration response omitted client_id")
	}
	if registered.TokenEndpointAuthMethod != "none" {
		t.Fatalf(
			"token_endpoint_auth_method = %q, want none",
			registered.TokenEndpointAuthMethod,
		)
	}

	var clientType, slug string
	var resourceURI *string
	if err := tdb.QueryRow(
		"SELECT client_type, slug, resource_uri FROM oauth_clients WHERE client_id = ?",
		registered.ClientID,
	).Scan(&clientType, &slug, &resourceURI); err != nil {
		t.Fatalf("query registered client: %v", err)
	}
	if clientType != "public" {
		t.Fatalf("client_type = %q, want public", clientType)
	}
	if !strings.HasPrefix(slug, "oauth-") {
		t.Fatalf("slug = %q, want oauth- prefix", slug)
	}
	if resourceURI != nil && *resourceURI != "" {
		t.Fatalf(
			"resource_uri = %q, want empty so the authorization request selects the audience",
			*resourceURI,
		)
	}

	verifier := "docmost-public-client-verifier-0123456789"
	challengeBytes := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeBytes[:])
	redirectURI := registered.RedirectURIs[0]
	authorizeQuery := url.Values{
		"client_id":             {registered.ClientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {"items:read workspaces:read collections:read"},
		"state":                 {"test-state"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	authorizeRequest := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/oauth/authorize/info?%s", authorizeQuery.Encode()),
		nil,
	)
	authorizeResponse := httptest.NewRecorder()

	handler.AuthorizeInfo(authorizeResponse, authorizeRequest)

	if authorizeResponse.Code != http.StatusOK {
		t.Fatalf(
			"resource-less authorize status = %d, want %d (body %q)",
			authorizeResponse.Code,
			http.StatusOK,
			authorizeResponse.Body.String(),
		)
	}
	var authorizeInfo AuthorizeInfoResponse
	if err := json.NewDecoder(authorizeResponse.Body).Decode(&authorizeInfo); err != nil {
		t.Fatalf("decode authorize response: %v", err)
	}
	if authorizeInfo.Resource != "" {
		t.Fatalf("authorize resource = %q, want empty", authorizeInfo.Resource)
	}
}
