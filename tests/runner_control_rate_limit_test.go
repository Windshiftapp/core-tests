package tests

import (
	"fmt"
	"net/http"
	"testing"
)

func TestRunnerClaimSustainsProductionPollingRate(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	resp := MakeAuthRequest(t, server, http.MethodPost, "/admin/action-capabilities", map[string]interface{}{
		"name":                      "Polling Pool",
		"capability_type":           "runner_pool",
		"config":                    `{"max_concurrent_runs":1}`,
		"applies_to_all_workspaces": true,
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var pool map[string]interface{}
	DecodeJSON(t, resp, &pool)
	poolID := ExtractIDFromResponse(t, pool)

	resp = MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/admin/action-capabilities/%d/runner-tokens", poolID),
		map[string]interface{}{"description": "polling runner", "ttl_hours": 1})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var token map[string]interface{}
	DecodeJSON(t, resp, &token)

	resp = MakeAuthRequest(t, server, http.MethodPost, "/runner/register", map[string]interface{}{
		"registration_token": token["token"],
		"name":               "polling-runner",
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var registration map[string]interface{}
	DecodeJSON(t, resp, &registration)
	credential, _ := registration["credential"].(string)
	if credential == "" {
		t.Fatalf("registration returned no runner credential: %#v", registration)
	}

	// The production runner polls every two seconds, or 30 times per minute.
	// A short burst beyond the shared auth limiter's capacity reproduces the old
	// failure without sleeps: valid claim polls must remain runner traffic.
	for attempt := 1; attempt <= 40; attempt++ {
		resp = MakeBearerRequestWithToken(t, server, credential, http.MethodPost, "/api/runner/claim", nil)
		if resp.StatusCode != http.StatusOK {
			defer resp.Body.Close()
			AssertStatusCode(t, resp, http.StatusOK)
			return
		}
		resp.Body.Close()
	}
}
