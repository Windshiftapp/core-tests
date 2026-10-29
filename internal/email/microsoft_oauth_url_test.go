//go:build test

package email

import (
	"net/url"
	"strings"
	"testing"
)

// TestMicrosoftGetOAuthURLForcesAccountSelection pins the fix for the silent
// SSO reuse that made a shared mailbox impossible to connect: without
// prompt=select_account Entra hands back a token for whichever account the
// browser already holds.
func TestMicrosoftGetOAuthURLForcesAccountSelection(t *testing.T) {
	p := NewMicrosoftProvider("client-abc", "secret", "common", nil)

	raw := p.GetOAuthURL("state-123", "https://windshift.test/api/channels/inline-oauth/callback")
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	if got := parsed.Query().Get("prompt"); got != "select_account" {
		t.Fatalf("prompt = %q, want select_account", got)
	}
	if got := parsed.Query().Get("state"); got != "state-123" {
		t.Fatalf("state = %q, want state-123", got)
	}
}

// TestMicrosoftOAuthScopesRequestNoGraphOrIdentityResource pins the root cause
// of GH #363: the Graph /me identity call needed a Graph-audience token the
// flow never requested. The mailbox is now typed, so no identity scope belongs
// in the request.
func TestMicrosoftOAuthScopesRequestNoGraphOrIdentityResource(t *testing.T) {
	p := NewMicrosoftProvider("client-abc", "secret", "common", nil)
	raw := p.GetOAuthURL("state", "https://windshift.test/callback")
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	scope := parsed.Query().Get("scope")

	for _, want := range []string{
		"https://outlook.office365.com/IMAP.AccessAsUser.All",
		"offline_access",
	} {
		if !strings.Contains(scope, want) {
			t.Errorf("scope %q missing required %q", scope, want)
		}
	}
	for _, unwanted := range []string{"openid", "profile", "graph.microsoft.com", "SMTP.Send"} {
		if strings.Contains(scope, unwanted) {
			t.Errorf("scope %q still requests %q", scope, unwanted)
		}
	}
}

func TestProviderDefaultScopesRequestNoIdentityLookup(t *testing.T) {
	for _, scope := range MicrosoftDefaultScopes {
		if strings.Contains(scope, "graph.microsoft.com") || scope == "openid" || scope == "profile" || scope == "email" || strings.Contains(scope, "SMTP.Send") {
			t.Errorf("MicrosoftDefaultScopes still requests identity/Graph scope %q", scope)
		}
	}
	for _, scope := range GoogleDefaultScopes {
		if strings.Contains(scope, "userinfo") || scope == "openid" || scope == "email" || scope == "profile" {
			t.Errorf("GoogleDefaultScopes still requests identity scope %q", scope)
		}
	}
}
