//go:build test

package sso

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"windshift/internal/utils"
)

// TestCreateRelyingParty_SmokeWithSignAlgsAllowlist verifies that
// CreateRelyingParty wires up successfully with the signing-algorithm
// allowlist option in place. The point of this test is to catch
// accidental removal of rp.WithSupportedSigningAlgorithms in the
// verifier options — if zitadel renames or drops the option, this
// test will fail at compile time alongside the production code; if
// someone deletes the line, this still smoke-tests the wiring.
func TestCreateRelyingParty_SmokeWithSignAlgsAllowlist(t *testing.T) {
	srv := newStubIDPServer(t)
	defer srv.Close()

	svc := NewOIDCService(make([]byte, 32))
	svc.httpClient = srv.Client() // permissive; the stub IdP runs on 127.0.0.1, which SafeNetDialer would otherwise reject
	provider := &SSOProvider{
		ProviderType: ProviderTypeOIDC,
		IssuerURL:    srv.URL,
		ClientID:     "test-client",
		Scopes:       "openid email profile",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rp, err := svc.CreateRelyingParty(ctx, provider, "https://example.test/callback", "test-secret")
	if err != nil {
		t.Fatalf("CreateRelyingParty: %v", err)
	}
	if rp == nil {
		t.Fatalf("expected non-nil relying party")
	}

	got := rp.IDTokenVerifier().SupportedSignAlgs
	wantSet := map[string]struct{}{
		"RS256": {}, "RS384": {}, "RS512": {},
		"ES256": {}, "ES384": {}, "ES512": {},
		"PS256": {}, "PS384": {}, "PS512": {},
	}
	if len(got) != len(wantSet) {
		t.Fatalf("SupportedSignAlgs length = %d, want %d (got %v)", len(got), len(wantSet), got)
	}
	for _, alg := range got {
		if _, ok := wantSet[alg]; !ok {
			t.Errorf("unexpected algorithm %q in allowlist", alg)
		}
	}
	for _, banned := range []string{"none", "HS256", "HS384", "HS512"} {
		for _, alg := range got {
			if alg == banned {
				t.Errorf("banned algorithm %q present in allowlist", banned)
			}
		}
	}
}

// TestCreateRelyingParty_BlocksLoopbackDial confirms the SSRF-safe dialer is
// actually wired into the relying-party constructor: when local connections
// are explicitly disabled, an issuer URL that resolves to loopback gets
// rejected at dial time, before any discovery request completes.
func TestCreateRelyingParty_BlocksLoopbackDial(t *testing.T) {
	utils.SetAllowLocalConnections(false)
	defer utils.SetAllowLocalConnections(true)

	srv := newStubIDPServer(t)
	defer srv.Close()

	svc := NewOIDCService(make([]byte, 32)) // default SSRF-safe client
	provider := &SSOProvider{
		ProviderType: ProviderTypeOIDC,
		IssuerURL:    srv.URL, // httptest.Server binds 127.0.0.1
		ClientID:     "test-client",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := svc.CreateRelyingParty(ctx, provider, "https://example.test/callback", "test-secret")
	if err == nil {
		t.Fatalf("expected CreateRelyingParty to reject a loopback issuer URL via the SSRF-safe dialer")
	}
}

// newStubIDPServer returns an httptest.Server that serves a minimal
// OIDC discovery document at /.well-known/openid-configuration. The
// discovery document advertises HS256 as a supported alg specifically
// to confirm that our explicit allowlist takes precedence over what an
// IdP claims to support.
func newStubIDPServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var baseURL string
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                baseURL,
			"authorization_endpoint":                baseURL + "/authorize",
			"token_endpoint":                        baseURL + "/token",
			"jwks_uri":                              baseURL + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"HS256", "RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keys":[]}`))
	})
	srv := httptest.NewServer(mux)
	baseURL = srv.URL
	return srv
}
