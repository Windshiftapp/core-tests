//go:build test

package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/auth"
	"windshift/internal/models"
)

const (
	testMCPResource = "https://windshift.example/windshift/mcp"
	testMCPMetadata = "https://windshift.example/windshift/.well-known/oauth-protected-resource/mcp"
)

func TestOAuthMCPChallengeAdvertisesDiscoveryMetadata(t *testing.T) {
	tokenManager, _, _ := newMCPTestEnv(t)
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("unauthenticated request reached MCP handler")
	})
	middleware := bearerAuthMiddlewareWithConfig(
		tokenManager,
		AuthConfig{
			ResourceURI:         testMCPResource,
			ResourceMetadataURI: testMCPMetadata,
		},
		next,
	)
	recorder := httptest.NewRecorder()

	middleware.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"method":"tools/list"}`)),
	)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
	// The challenge advertises the full default agent scope set, not just
	// mcp:access (WI-960): a client that authorizes on what the challenge
	// names used to end up with a token that could open the transport and
	// call nothing.
	want := `Bearer resource_metadata="` + testMCPMetadata + `", scope="` + strings.Join(auth.DefaultAgentScopes, " ") + `"`
	if got := recorder.Header().Get("WWW-Authenticate"); got != want {
		t.Fatalf("WWW-Authenticate = %q, want %q", got, want)
	}
}

func TestOAuthMCPRejectsTokenForAnotherResource(t *testing.T) {
	tokenManager, _, userID := newMCPTestEnv(t)
	token, err := tokenManager.CreateToken(userID, models.APITokenCreate{
		Name:          "wrong audience",
		Permissions:   []string{auth.ScopeMCPAccess, auth.ScopeItemsRead},
		OAuthClientID: "odysseus-client",
		OAuthResource: "https://other.example/mcp",
	})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("wrong-audience request reached MCP handler")
	})
	middleware := bearerAuthMiddlewareWithConfig(
		tokenManager,
		AuthConfig{
			ResourceURI:         testMCPResource,
			ResourceMetadataURI: testMCPMetadata,
		},
		next,
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/mcp",
		strings.NewReader(`{"method":"tools/list"}`),
	)
	request.Header.Set("Authorization", "Bearer "+token.Token)
	recorder := httptest.NewRecorder()

	middleware.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%q", recorder.Code, recorder.Body.String())
	}
	// The challenge advertises the full default agent scope set, not just
	// mcp:access (WI-960): a client that authorizes on what the challenge
	// names used to end up with a token that could open the transport and
	// call nothing.
	want := `Bearer resource_metadata="` + testMCPMetadata + `", scope="` + strings.Join(auth.DefaultAgentScopes, " ") + `"`
	if got := recorder.Header().Get("WWW-Authenticate"); got != want {
		t.Fatalf("WWW-Authenticate = %q, want %q", got, want)
	}
}

func TestOAuthMCPToolChallengesRequestIncrementalScopes(t *testing.T) {
	tests := []struct {
		name      string
		tool      string
		current   []string
		wantScope string
	}{
		{
			name:      "read action",
			tool:      "get_item",
			current:   []string{auth.ScopeMCPAccess},
			wantScope: "items:read mcp:access",
		},
		{
			name:      "write action",
			tool:      "create_item",
			current:   []string{auth.ScopeMCPAccess, auth.ScopeItemsRead},
			wantScope: "items:read items:write mcp:access",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tokenManager, _, userID := newMCPTestEnv(t)
			token, err := tokenManager.CreateToken(userID, models.APITokenCreate{
				Name:          test.name,
				Permissions:   test.current,
				OAuthClientID: "odysseus-client",
				OAuthResource: testMCPResource,
			})
			if err != nil {
				t.Fatalf("CreateToken: %v", err)
			}
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("insufficient-scope request reached MCP handler")
			})
			middleware := bearerAuthMiddlewareWithConfig(
				tokenManager,
				AuthConfig{
					ResourceURI:         testMCPResource,
					ResourceMetadataURI: testMCPMetadata,
				},
				next,
			)
			request := httptest.NewRequest(
				http.MethodPost,
				"/mcp",
				strings.NewReader(`{"method":"tools/call","params":{"name":"`+test.tool+`"}}`),
			)
			request.Header.Set("Authorization", "Bearer "+token.Token)
			recorder := httptest.NewRecorder()

			middleware.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body=%q", recorder.Code, recorder.Body.String())
			}
			want := `Bearer resource_metadata="` + testMCPMetadata +
				`", error="insufficient_scope", scope="` + test.wantScope + `"`
			if got := recorder.Header().Get("WWW-Authenticate"); got != want {
				t.Fatalf("WWW-Authenticate = %q, want %q", got, want)
			}
		})
	}
}

func TestOAuthMCPAcceptsBoundReadAndWriteActions(t *testing.T) {
	tokenManager, _, userID := newMCPTestEnv(t)
	token, err := tokenManager.CreateToken(userID, models.APITokenCreate{
		Name: "Odysseus MCP",
		Permissions: []string{
			auth.ScopeMCPAccess,
			auth.ScopeItemsRead,
			auth.ScopeItemsWrite,
		},
		OAuthClientID: "odysseus-client",
		OAuthResource: testMCPResource,
	})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	for _, tool := range []string{"get_item", "create_item"} {
		t.Run(tool, func(t *testing.T) {
			var body string
			next := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				raw, readErr := io.ReadAll(request.Body)
				if readErr != nil {
					t.Fatalf("read restored MCP body: %v", readErr)
				}
				body = string(raw)
				w.WriteHeader(http.StatusNoContent)
			})
			middleware := bearerAuthMiddlewareWithConfig(
				tokenManager,
				AuthConfig{
					ResourceURI:         testMCPResource,
					ResourceMetadataURI: testMCPMetadata,
				},
				next,
			)
			requestBody := `{"method":"tools/call","params":{"name":"` + tool + `"}}`
			request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(requestBody))
			request.Header.Set("Authorization", "Bearer "+token.Token)
			recorder := httptest.NewRecorder()

			middleware.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204; body=%q", recorder.Code, recorder.Body.String())
			}
			if body != requestBody {
				t.Fatalf("restored MCP body = %q, want %q", body, requestBody)
			}
		})
	}
}

func TestMCPPATFallbackDoesNotRequireOAuthAudience(t *testing.T) {
	tokenManager, _, userID := newMCPTestEnv(t)
	token, err := tokenManager.CreateToken(userID, models.APITokenCreate{
		Name:        "user PAT",
		Permissions: []string{auth.ScopeMCPAccess},
	})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	middleware := bearerAuthMiddlewareWithConfig(
		tokenManager,
		AuthConfig{
			ResourceURI:         testMCPResource,
			ResourceMetadataURI: testMCPMetadata,
		},
		next,
	)
	request := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	request.Header.Set("Authorization", "Bearer "+token.Token)
	recorder := httptest.NewRecorder()

	middleware.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%q", recorder.Code, recorder.Body.String())
	}
}
