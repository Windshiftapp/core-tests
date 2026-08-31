package handlers

import (
	"database/sql"
	"encoding/json"
	"testing"

	"windshift/internal/models"
)

func TestSCMProviderResponseOmitsProviderOAuthState(t *testing.T) {
	row := providerRowScanResult{
		Provider: models.SCMProvider{
			ID:           42,
			Slug:         "github-main",
			Name:         "GitHub Main",
			ProviderType: models.SCMProviderTypeGitHub,
			AuthMethod:   models.SCMAuthMethodOAuth,
		},
		OAuthClientID:        sql.NullString{String: "client-id", Valid: true},
		OAuthClientSecretEnc: sql.NullString{String: "encrypted-secret", Valid: true},
	}

	payload, err := json.Marshal(row.toResponse())
	if err != nil {
		t.Fatalf("marshal provider response: %v", err)
	}

	var response map[string]any
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatalf("decode provider response: %v", err)
	}

	for _, key := range []string{"has_oauth_token", "oauth_token_expires_at"} {
		if _, exists := response[key]; exists {
			t.Errorf("provider response unexpectedly contains %q", key)
		}
	}
	if configured, ok := response["has_oauth_client_secret"].(bool); !ok || !configured {
		t.Errorf("has_oauth_client_secret = %#v, want true", response["has_oauth_client_secret"])
	}
}
