package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// The stored email OAuth client secret is scrubbed from channel reads, so the
// response must instead advertise that one exists. Without the flag the config
// UI cannot tell "never configured" from "saved but hidden" and blocks the
// Connect Mailbox action.
func TestChannelReadReportsStoredEmailOAuthClientSecret(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	newEmailChannel := func(t *testing.T, name string) int {
		t.Helper()
		createResponse := MakeAuthRequest(t, server, http.MethodPost, "/channels", map[string]interface{}{
			"name":      name,
			"type":      "email",
			"direction": "inbound",
			"status":    "disabled",
			"config":    "{}",
		})
		defer createResponse.Body.Close()
		AssertStatusCode(t, createResponse, http.StatusCreated)
		var created map[string]interface{}
		DecodeJSON(t, createResponse, &created)
		return ExtractIDFromResponse(t, created)
	}

	t.Run("secret saved", func(t *testing.T) {
		channelID := newEmailChannel(t, "OAuth mailbox")

		updateResponse := MakeAuthRequest(t, server, http.MethodPut,
			fmt.Sprintf("/channels/%d/config", channelID), map[string]interface{}{
				"config": map[string]interface{}{
					"email_auth_method":         "oauth",
					"email_oauth_provider_type": "microsoft",
					"email_oauth_client_id":     "client-id",
					"email_oauth_client_secret": "super-secret",
					"email_oauth_tenant_id":     "tenant-id",
					"email_oauth_mailbox":       "support@example.com",
				},
			})
		defer updateResponse.Body.Close()
		AssertStatusCode(t, updateResponse, http.StatusOK)

		getResponse := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/channels/%d", channelID), nil)
		defer getResponse.Body.Close()
		AssertStatusCode(t, getResponse, http.StatusOK)
		var channel map[string]interface{}
		DecodeJSON(t, getResponse, &channel)

		if channel["has_email_oauth_client_secret"] != true {
			t.Fatalf("has_email_oauth_client_secret = %v, want true", channel["has_email_oauth_client_secret"])
		}

		configRaw, _ := channel["config"].(string)
		var config map[string]interface{}
		if err := json.Unmarshal([]byte(configRaw), &config); err != nil {
			t.Fatalf("decode returned channel config: %v", err)
		}
		if _, present := config["email_oauth_client_secret"]; present {
			t.Fatalf("channel read exposed the stored client secret: %s", configRaw)
		}
	})

	t.Run("no secret saved", func(t *testing.T) {
		channelID := newEmailChannel(t, "No OAuth mailbox")

		getResponse := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/channels/%d", channelID), nil)
		defer getResponse.Body.Close()
		AssertStatusCode(t, getResponse, http.StatusOK)
		var channel map[string]interface{}
		DecodeJSON(t, getResponse, &channel)

		if channel["has_email_oauth_client_secret"] != nil {
			t.Fatalf("has_email_oauth_client_secret = %v, want absent", channel["has_email_oauth_client_secret"])
		}
	})
}
