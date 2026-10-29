//go:build test

package services

import (
	"fmt"
	"testing"
)

// A partial config update must not treat a blank secret input as a deletion:
// forms echo an unedited credential back as "", and API callers may send an
// empty string for a secret they are not editing. Omitted and blank both
// preserve the stored ciphertext; only a new value rotates it, and JSON null
// clears it.
func TestMergeChannelConfig_SecretFieldUpdateSemantics(t *testing.T) {
	tests := []struct {
		name        string
		incoming    func(field string) map[string]interface{}
		wantPresent bool
		wantValue   interface{}
	}{
		{
			name:        "omitted preserves stored ciphertext",
			incoming:    func(string) map[string]interface{} { return map[string]interface{}{} },
			wantPresent: true,
			wantValue:   "enc:v1:stored",
		},
		{
			name: "blank preserves stored ciphertext",
			incoming: func(field string) map[string]interface{} {
				return map[string]interface{}{field: ""}
			},
			wantPresent: true,
			wantValue:   "enc:v1:stored",
		},
		{
			name: "new value rotates ciphertext",
			incoming: func(field string) map[string]interface{} {
				return map[string]interface{}{field: "enc:v1:rotated"}
			},
			wantPresent: true,
			wantValue:   "enc:v1:rotated",
		},
		{
			name: "null clears stored ciphertext",
			incoming: func(field string) map[string]interface{} {
				return map[string]interface{}{field: nil}
			},
			wantPresent: false,
		},
	}

	for _, field := range []string{"smtp_password", "imap_password", "webhook_secret", "email_oauth_client_secret"} {
		existing := fmt.Sprintf(`{"%s":"enc:v1:stored"}`, field)
		for _, tt := range tests {
			t.Run(field+"/"+tt.name, func(t *testing.T) {
				merged, _, err := mergeChannelConfig(existing, tt.incoming(field))
				if err != nil {
					t.Fatalf("mergeChannelConfig() error = %v", err)
				}
				got, present := merged[field]
				if present != tt.wantPresent {
					t.Fatalf("%s present = %v, want %v", field, present, tt.wantPresent)
				}
				if tt.wantPresent && got != tt.wantValue {
					t.Fatalf("%s = %#v, want %#v", field, got, tt.wantValue)
				}
			})
		}
	}
}

// Changing the OAuth client identity still invalidates a secret that belonged
// to the old client, even when the client echoes the secret back blank.
func TestMergeChannelConfig_BlankOAuthSecretOnIdentityChange(t *testing.T) {
	const existing = `{
		"email_oauth_provider_type":"google",
		"email_oauth_client_id":"old-client",
		"email_oauth_client_secret":"enc:v1:old-secret"
	}`
	tests := []struct {
		name        string
		incoming    map[string]interface{}
		wantPresent bool
		wantValue   interface{}
	}{
		{
			name:        "blank secret with client change drops old ciphertext",
			incoming:    map[string]interface{}{"email_oauth_client_id": "new-client", "email_oauth_client_secret": ""},
			wantPresent: false,
		},
		{
			name:        "new secret with client change rotates ciphertext",
			incoming:    map[string]interface{}{"email_oauth_client_id": "new-client", "email_oauth_client_secret": "enc:v1:new-secret"},
			wantPresent: true,
			wantValue:   "enc:v1:new-secret",
		},
		{
			name:        "blank secret without identity change keeps ciphertext",
			incoming:    map[string]interface{}{"email_oauth_client_secret": ""},
			wantPresent: true,
			wantValue:   "enc:v1:old-secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged, _, err := mergeChannelConfig(existing, tt.incoming)
			if err != nil {
				t.Fatalf("mergeChannelConfig() error = %v", err)
			}
			got, present := merged["email_oauth_client_secret"]
			if present != tt.wantPresent {
				t.Fatalf("email_oauth_client_secret present = %v, want %v", present, tt.wantPresent)
			}
			if tt.wantPresent && got != tt.wantValue {
				t.Fatalf("email_oauth_client_secret = %#v, want %#v", got, tt.wantValue)
			}
		})
	}
}

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
		"email_oauth_mailbox":       "support@example.com",
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
	if _, ok := config["email_oauth_mailbox"]; ok {
		t.Fatal("OAuth mailbox remained in basic auth config")
	}
}

func TestMergeChannelConfig_MailboxChangeClearsVerifiedConnection(t *testing.T) {
	merged, _, err := mergeChannelConfig(`{
		"email_oauth_provider_type":"microsoft",
		"email_oauth_client_id":"client",
		"email_oauth_mailbox":"old@example.com",
		"email_oauth_email":"old@example.com",
		"email_oauth_refresh_token":"refresh",
		"email_oauth_expires_at":"2030-01-01T00:00:00Z"
	}`, map[string]interface{}{
		"email_oauth_mailbox": "new@example.com",
	})
	if err != nil {
		t.Fatalf("mergeChannelConfig() error = %v", err)
	}
	if merged["email_oauth_mailbox"] != "new@example.com" {
		t.Fatalf("mailbox = %#v, want new@example.com", merged["email_oauth_mailbox"])
	}
	for _, field := range []string{"email_oauth_refresh_token", "email_oauth_email", "email_oauth_expires_at"} {
		if _, ok := merged[field]; ok {
			t.Fatalf("%s survived a mailbox change; the connection must be invalidated", field)
		}
	}
}

func TestMergeChannelConfig_LegacyMailboxBackfillKeepsConnection(t *testing.T) {
	// Channels connected before email_oauth_mailbox existed carry only the
	// verified email. Re-saving the same address into the new field must not
	// look like an identity change and disconnect them.
	merged, _, err := mergeChannelConfig(`{
		"email_oauth_provider_type":"microsoft",
		"email_oauth_client_id":"client",
		"email_oauth_email":"support@example.com",
		"email_oauth_refresh_token":"refresh"
	}`, map[string]interface{}{
		"email_oauth_mailbox": "support@example.com",
	})
	if err != nil {
		t.Fatalf("mergeChannelConfig() error = %v", err)
	}
	if merged["email_oauth_email"] != "support@example.com" || merged["email_oauth_refresh_token"] != "refresh" {
		t.Fatalf("legacy connection dropped on mailbox backfill: %#v", merged)
	}
}

func TestMergeChannelConfig_UnrelatedUpdateKeepsLegacyConnection(t *testing.T) {
	merged, _, err := mergeChannelConfig(`{
		"email_oauth_provider_type":"microsoft",
		"email_oauth_client_id":"client",
		"email_oauth_email":"support@example.com",
		"email_oauth_refresh_token":"refresh"
	}`, map[string]interface{}{
		"email_processing_disposition": "mark_read",
	})
	if err != nil {
		t.Fatalf("mergeChannelConfig() error = %v", err)
	}
	if merged["email_oauth_refresh_token"] != "refresh" {
		t.Fatalf("unrelated update dropped the legacy connection: %#v", merged)
	}
}
