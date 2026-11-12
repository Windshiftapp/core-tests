package tests

import (
	"net/http"
	"testing"
)

// TestPortalRegionalSettingsPreservesOmittedTimezone pins the partial-update
// contract for PUT /portal/{slug}/auth/me: the profile language picker sends
// only {language}, so the customer's stored timezone must survive.
func TestPortalRegionalSettingsPreservesOmittedTimezone(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Portal Regional WS", shortKey("PRS"))
	portalSlug, channelID := SetupPortalChannel(t, server, wsID)
	_, sessionCookie := CreatePortalCustomerWithSession(t, server, channelID, "Regional User", "regional@test.local")

	// Seed a stored timezone.
	setResp := MakePortalRequest(t, server, sessionCookie, http.MethodPut, "/portal/"+portalSlug+"/auth/me", map[string]any{
		"timezone": "America/New_York",
	})
	defer setResp.Body.Close()
	AssertStatusCode(t, setResp, http.StatusOK)
	var set map[string]any
	DecodeJSON(t, setResp, &set)
	if set["timezone"] != "America/New_York" {
		t.Fatalf("timezone = %v, want America/New_York", set["timezone"])
	}

	// A language-only save must keep it.
	langResp := MakePortalRequest(t, server, sessionCookie, http.MethodPut, "/portal/"+portalSlug+"/auth/me", map[string]any{
		"language": "de",
	})
	defer langResp.Body.Close()
	AssertStatusCode(t, langResp, http.StatusOK)
	var lang map[string]any
	DecodeJSON(t, langResp, &lang)
	if lang["language"] != "de" {
		t.Errorf("language = %v, want de", lang["language"])
	}
	if lang["timezone"] != "America/New_York" {
		t.Errorf("timezone = %v, want America/New_York after a language-only save", lang["timezone"])
	}

	// Persistence: the stored row still carries the timezone.
	var stored string
	if err := server.DB().QueryRow(
		`SELECT COALESCE(timezone, '') FROM portal_customers WHERE email = ?`, "regional@test.local",
	).Scan(&stored); err != nil {
		t.Fatalf("read stored timezone: %v", err)
	}
	if stored != "America/New_York" {
		t.Errorf("stored timezone = %q, want America/New_York", stored)
	}
}
