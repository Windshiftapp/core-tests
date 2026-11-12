package tests

import (
	"net/http"
	"testing"
)

// TestPortalCustomerLanguageDetectionAndUpdate covers the portal language
// lifecycle: first contact stores the browser language once, the explicit
// toggle wins, and a later login never overwrites the chosen value.
func TestPortalCustomerLanguageDetectionAndUpdate(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Portal Language WS", shortKey("PLG"))
	portalSlug, channelID := SetupPortalChannel(t, server, wsID)

	const email = "portal-lang@example.test"
	customerID, portalCookie := CreatePortalCustomerWithSession(t, server, channelID, "Language Customer", email)

	languageOf := func() string {
		t.Helper()
		var language string
		if err := server.DB().QueryRow(
			`SELECT COALESCE(language, '') FROM portal_customers WHERE id = ?`, customerID,
		).Scan(&language); err != nil {
			t.Fatalf("read portal customer language: %v", err)
		}
		return language
	}

	requestLink := func(acceptLanguage string) *http.Response {
		t.Helper()
		return makeRequest(t, http.MethodPost,
			server.APIBase+"/portal/"+portalSlug+"/auth/request", "",
			map[string]any{"email": email},
			map[string]string{"Accept-Language": acceptLanguage})
	}

	// First contact: the browser language is stored before the email renders.
	first := requestLink("de-DE,de;q=0.9,en;q=0.8")
	defer first.Body.Close()
	AssertStatusCode(t, first, http.StatusOK)
	if got := languageOf(); got != "de" {
		t.Fatalf("language after first request = %q, want de", got)
	}

	// The explicit toggle wins and is persisted.
	update := MakePortalRequest(t, server, portalCookie, http.MethodPut,
		"/portal/"+portalSlug+"/auth/me", map[string]any{"language": "es"})
	defer update.Body.Close()
	AssertStatusCode(t, update, http.StatusOK)
	if got := languageOf(); got != "es" {
		t.Fatalf("language after update = %q, want es", got)
	}

	// A later login must not overwrite the chosen language.
	second := requestLink("fr")
	defer second.Body.Close()
	AssertStatusCode(t, second, http.StatusOK)
	if got := languageOf(); got != "es" {
		t.Fatalf("language after second request = %q, want es (unchanged)", got)
	}
}

// TestPortalCustomerLanguageUpdateValidation covers the PUT contract: only
// shipped locales and real timezones are accepted, and an internal or absent
// session cannot write a portal customer's preference.
func TestPortalCustomerLanguageUpdateValidation(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Portal Language Validation WS", shortKey("PLV"))
	portalSlug, channelID := SetupPortalChannel(t, server, wsID)
	_, portalCookie := CreatePortalCustomerWithSession(t, server, channelID, "Validation Customer", "portal-lang-validation@example.test")

	endpoint := "/portal/" + portalSlug + "/auth/me"

	unsupported := MakePortalRequest(t, server, portalCookie, http.MethodPut, endpoint,
		map[string]any{"language": "xx"})
	defer unsupported.Body.Close()
	AssertStatusCode(t, unsupported, http.StatusBadRequest)

	badTimezone := MakePortalRequest(t, server, portalCookie, http.MethodPut, endpoint,
		map[string]any{"language": "de", "timezone": "Not/AZone"})
	defer badTimezone.Body.Close()
	AssertStatusCode(t, badTimezone, http.StatusBadRequest)

	// No portal session: the endpoint must not fall back to internal auth.
	noSession := makeRequest(t, http.MethodPut, server.APIBase+endpoint, "",
		map[string]any{"language": "de"}, nil)
	defer noSession.Body.Close()
	AssertStatusCode(t, noSession, http.StatusUnauthorized)
}
