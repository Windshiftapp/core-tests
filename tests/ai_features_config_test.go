package tests

import (
	"net/http"
	"testing"
)

func TestAIFeaturesConfigRoundTrip(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	put := func(mode, schedule string) {
		t.Helper()
		feature := map[string]any{"mode": mode, "connection_id": 0}
		if schedule != "" {
			feature["schedule"] = schedule
		}
		resp := MakeAuthRequest(
			t,
			server,
			http.MethodPut,
			"/admin/ai-features",
			map[string]any{"daily_briefing": feature},
		)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	}

	read := func() map[string]any {
		t.Helper()
		resp := MakeAuthRequest(t, server, http.MethodGet, "/admin/ai-features", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var body struct {
			Config map[string]map[string]any `json:"config"`
		}
		DecodeJSON(t, resp, &body)
		return body.Config["daily_briefing"]
	}

	put("default", "daily")
	AssertJSONField(t, read(), "mode", "default")

	put("disabled", "")
	AssertJSONField(t, read(), "mode", "disabled")

	put("default", "daily")
	config := read()
	AssertJSONField(t, config, "mode", "default")
	AssertJSONField(t, config, "schedule", "daily")
}
