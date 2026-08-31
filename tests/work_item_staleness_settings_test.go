package tests

import (
	"net/http"
	"testing"
)

func TestWorkItemStalenessSettingsRoundTripAndAuthorization(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	read := func() int {
		t.Helper()
		resp := MakeAuthRequest(t, server, http.MethodGet, "/admin/work-item-staleness", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var body struct {
			StaleAfterDays int `json:"stale_after_days"`
		}
		DecodeJSON(t, resp, &body)
		return body.StaleAfterDays
	}

	if got := read(); got != 30 {
		t.Fatalf("default stale_after_days = %d, want 30", got)
	}

	update := MakeAuthRequest(t, server, http.MethodPut, "/admin/work-item-staleness", map[string]any{
		"stale_after_days": 45,
	})
	defer update.Body.Close()
	AssertStatusCode(t, update, http.StatusOK)
	if got := read(); got != 45 {
		t.Fatalf("persisted stale_after_days = %d, want 45", got)
	}

	invalid := MakeAuthRequest(t, server, http.MethodPut, "/admin/work-item-staleness", map[string]any{
		"stale_after_days": 0,
	})
	defer invalid.Body.Close()
	AssertStatusCode(t, invalid, http.StatusBadRequest)

	_, username, password := CreateTestUserWithCredentials(
		t, server, "staleness_regular_user", "staleness-regular@test.com",
	)
	regularCookie := CreateBearerTokenForUser(t, server, username, password)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		resp := MakeAuthRequestWithToken(
			t, server, regularCookie, method, "/admin/work-item-staleness",
			map[string]any{"stale_after_days": 60},
		)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
	}
}
