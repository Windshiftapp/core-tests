//go:build test

package repository

import (
	"context"
	"encoding/json"
	"testing"
)

// A stored email OAuth client secret is scrubbed from channel reads, so the
// response must instead advertise that one exists. Without this flag the UI
// cannot tell "never configured" from "saved but hidden" and blocks Connect.
func TestFindByIDReportsStoredEmailOAuthClientSecret(t *testing.T) {
	db := newChannelRepositoryTestDB(t)

	encrypted := insertTestChannel(t, db, "email", "inbound", "disabled",
		`{"email_oauth_client_id":"client","email_oauth_client_secret":"enc:v1:ZmFrZS1jaXBoZXJ0ZXh0"}`, false)
	legacyPlaintext := insertTestChannel(t, db, "email", "inbound", "disabled",
		`{"email_oauth_client_id":"client","email_oauth_client_secret":"legacy-plaintext-secret"}`, false)
	missing := insertTestChannel(t, db, "email", "inbound", "disabled",
		`{"email_oauth_client_id":"client"}`, false)
	blank := insertTestChannel(t, db, "email", "inbound", "disabled",
		`{"email_oauth_client_id":"client","email_oauth_client_secret":""}`, false)

	repo := NewChannelRepository(db)
	cases := []struct {
		name     string
		channel  int
		wantFlag bool
	}{
		{name: "encrypted secret", channel: encrypted, wantFlag: true},
		{name: "legacy plaintext secret", channel: legacyPlaintext, wantFlag: true},
		{name: "no secret key", channel: missing, wantFlag: false},
		{name: "blank secret", channel: blank, wantFlag: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			channel, err := repo.FindByID(context.Background(), tc.channel)
			if err != nil {
				t.Fatalf("FindByID: %v", err)
			}
			if channel.HasEmailOAuthClientSecret != tc.wantFlag {
				t.Fatalf("HasEmailOAuthClientSecret = %v, want %v", channel.HasEmailOAuthClientSecret, tc.wantFlag)
			}

			var config map[string]any
			if err := json.Unmarshal([]byte(channel.Config), &config); err != nil {
				t.Fatalf("returned config is not a JSON object: %v", err)
			}
			if _, present := config["email_oauth_client_secret"]; present {
				t.Fatalf("returned config still exposes the client secret: %s", channel.Config)
			}
		})
	}
}
