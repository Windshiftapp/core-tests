package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// WI-1423: manual-registration portals must resolve an admin-provisioned
// customer whose stored email has uppercase characters. The sign-in request
// lowercases the submitted address, so the channel-access lookup must compare
// case-insensitively or the customer can never receive a magic link.
func TestManualPortalMagicLinkMatchesStoredEmailCaseInsensitively(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Manual Magic Link WS", shortKey("MML"))
	portalSlug, channelID := SetupPortalChannel(t, server, wsID)

	const storedEmail = "User@Example.com"
	customerID, _ := CreatePortalCustomerWithSession(t, server, channelID, "Mixed Case Customer", storedEmail)

	// Customer management exposes channel grants read-only; create the grant
	// row the admin flow owns so manual-mode access is in place.
	if _, err := server.DB().ExecWrite(`
		INSERT INTO portal_customer_channels (portal_customer_id, channel_id, created_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
	`, customerID, channelID); err != nil {
		t.Fatalf("grant portal channel access: %v", err)
	}

	configResp := MakeAuthRequest(t, server, http.MethodPut,
		fmt.Sprintf("/channels/%d/config", channelID), map[string]interface{}{
			"config": map[string]interface{}{
				"portal_slug":              portalSlug,
				"portal_enabled":           true,
				"portal_title":             "Manual magic link portal",
				"portal_registration_mode": "manual",
				"portal_workspace_ids":     []int{wsID},
			},
		})
	defer configResp.Body.Close()
	AssertStatusCode(t, configResp, http.StatusOK)

	resp := MakeUnauthenticatedRequest(t, server, http.MethodPost,
		fmt.Sprintf("/portal/%s/auth/request", portalSlug),
		map[string]interface{}{"email": "user@example.com"})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var magicLinks int
	if err := server.DB().QueryRow(
		`SELECT COUNT(*) FROM portal_customer_magic_links WHERE portal_customer_id = ?`, customerID,
	).Scan(&magicLinks); err != nil {
		t.Fatalf("count magic links: %v", err)
	}
	if magicLinks != 1 {
		t.Fatalf("magic links issued = %d, want 1 for case-insensitive email match", magicLinks)
	}
}
