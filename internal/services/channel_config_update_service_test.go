//go:build test

package services

import "testing"

func TestMergeChannelConfig_PreservesPartialFieldsAndClearsOAuthState(t *testing.T) {
	merged, stored, err := mergeChannelConfig(`{
		"smtp_host":"mail.example.com",
		"email_oauth_provider_type":"google",
		"email_oauth_client_id":"old-client",
		"email_oauth_client_secret":"old-secret",
		"email_oauth_refresh_token":"refresh",
		"email_oauth_email":"user@example.com"
	}`, map[string]interface{}{
		"email_oauth_client_id": "new-client",
	})
	if err != nil {
		t.Fatalf("mergeChannelConfig() error = %v", err)
	}
	if merged["smtp_host"] != "mail.example.com" {
		t.Fatalf("smtp_host = %#v, want preserved value", merged["smtp_host"])
	}
	if merged["email_oauth_client_id"] != "new-client" {
		t.Fatalf("email_oauth_client_id = %#v, want new-client", merged["email_oauth_client_id"])
	}
	for _, field := range []string{"email_oauth_refresh_token", "email_oauth_email"} {
		if _, ok := merged[field]; ok {
			t.Fatalf("%s was not cleared after OAuth identity change", field)
		}
	}
	if stored.EmailOAuthProviderType != "google" || stored.EmailOAuthClientID != "old-client" {
		t.Fatalf("stored config = %+v, want original OAuth identity", stored)
	}
}

func TestNormalizeEmailAuthConfig_RemovesInactiveCredentials(t *testing.T) {
	config := map[string]interface{}{
		"email_auth_method":         "basic",
		"imap_password":             "password",
		"email_oauth_client_secret": "oauth-secret",
		"email_oauth_refresh_token": "refresh",
	}
	normalizeEmailAuthConfig(config)
	if config["imap_password"] != "password" {
		t.Fatalf("imap_password = %#v, want retained basic credential", config["imap_password"])
	}
	if _, ok := config["email_oauth_client_secret"]; ok {
		t.Fatal("OAuth client secret remained in basic auth config")
	}
	if _, ok := config["email_oauth_refresh_token"]; ok {
		t.Fatal("OAuth refresh token remained in basic auth config")
	}
}
