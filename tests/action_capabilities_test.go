package tests

import (
	"fmt"
	"net/http"
	"testing"
)

func TestActionCapabilityValidationAndWorkspacePicker(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceA, _ := CreateTestWorkspace(t, server, "Capability Scope A", shortKey("CSA"))
	workspaceB, _ := CreateTestWorkspace(t, server, "Capability Scope B", shortKey("CSB"))

	t.Run("rejects unusable HTTP capability", func(t *testing.T) {
		payload := map[string]interface{}{
			"name":            "No Allowlist",
			"capability_type": "http_client",
			"config":          `{"allowed_url_patterns":[],"timeout_secs":30}`,
		}
		resp := MakeAuthRequest(t, server, http.MethodPost, "/admin/action-capabilities", payload)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	globalPayload := map[string]interface{}{
		"name":                      "Global HTTP Picker",
		"capability_type":           "http_client",
		"config":                    `{"allowed_url_patterns":["https://global.example.com/**"],"timeout_secs":30}`,
		"is_enabled":                false,
		"applies_to_all_workspaces": true,
	}
	resp := MakeAuthRequest(t, server, http.MethodPost, "/admin/action-capabilities", globalPayload)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var disabledGlobal map[string]interface{}
	DecodeJSON(t, resp, &disabledGlobal)
	AssertJSONField(t, disabledGlobal, "is_enabled", false)

	scopedPayload := map[string]interface{}{
		"name":                      "Scoped HTTP Picker",
		"capability_type":           "http_client",
		"config":                    `{"allowed_url_patterns":["https://scoped.example.com/**"],"timeout_secs":30}`,
		"applies_to_all_workspaces": false,
		"workspace_ids":             []int{workspaceA},
	}
	resp = MakeAuthRequest(t, server, http.MethodPost, "/admin/action-capabilities", scopedPayload)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)

	enabledGlobalPayload := map[string]interface{}{
		"name":                      "Enabled Global HTTP Picker",
		"capability_type":           "http_client",
		"config":                    `{"allowed_url_patterns":["https://enabled.example.com/**"],"timeout_secs":30}`,
		"applies_to_all_workspaces": true,
	}
	resp = MakeAuthRequest(t, server, http.MethodPost, "/admin/action-capabilities", enabledGlobalPayload)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)

	listPickerNames := func(workspaceID int) map[string]bool {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/workspaces/%d/action-capabilities?type=http_client", workspaceID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var caps []map[string]interface{}
		DecodeJSON(t, resp, &caps)
		names := map[string]bool{}
		for _, cap := range caps {
			names[cap["name"].(string)] = true
			if cap["capability_type"] != "http_client" {
				t.Fatalf("picker returned wrong type: %#v", cap)
			}
		}
		return names
	}

	namesA := listPickerNames(workspaceA)
	if !namesA["Scoped HTTP Picker"] || !namesA["Enabled Global HTTP Picker"] {
		t.Fatalf("workspace A picker missing scoped/global capabilities: %#v", namesA)
	}
	if namesA["Global HTTP Picker"] {
		t.Fatalf("workspace A picker included disabled capability: %#v", namesA)
	}

	namesB := listPickerNames(workspaceB)
	if namesB["Scoped HTTP Picker"] {
		t.Fatalf("workspace B picker included out-of-scope capability: %#v", namesB)
	}
	if !namesB["Enabled Global HTTP Picker"] {
		t.Fatalf("workspace B picker missing global capability: %#v", namesB)
	}
}

func TestLLMCapabilityHiddenWhenConnectionDisabled(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "LLM Capability Scope", shortKey("LCS"))

	connectionPayload := map[string]interface{}{
		"name":          "Action LLM",
		"provider_type": "local",
		"model":         "test-model",
		"base_url":      "https://llm.example.com",
		"is_enabled":    true,
	}
	resp := MakeAuthRequest(t, server, http.MethodPost, "/admin/llm-connections", connectionPayload)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var connection map[string]interface{}
	DecodeJSON(t, resp, &connection)
	connectionID := ExtractIDFromResponse(t, connection)

	capabilityPayload := map[string]interface{}{
		"name":                      "Action LLM Capability",
		"capability_type":           "llm_connection",
		"config":                    fmt.Sprintf(`{"connection_id":%d}`, connectionID),
		"applies_to_all_workspaces": true,
	}
	resp = MakeAuthRequest(t, server, http.MethodPost, "/admin/action-capabilities", capabilityPayload)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)

	listLLMCapabilities := func() []map[string]interface{} {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/workspaces/%d/action-capabilities?type=llm_connection", workspaceID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var caps []map[string]interface{}
		DecodeJSON(t, resp, &caps)
		return caps
	}

	if caps := listLLMCapabilities(); len(caps) != 1 || caps[0]["name"] != "Action LLM Capability" {
		t.Fatalf("expected enabled LLM capability in picker, got %#v", caps)
	}

	connectionPayload["is_enabled"] = false
	resp = MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/admin/llm-connections/%d", connectionID), connectionPayload)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	if caps := listLLMCapabilities(); len(caps) != 0 {
		t.Fatalf("expected disabled connection to hide LLM capability from picker, got %#v", caps)
	}
}

// TestRunnerPoolCapability covers the WI-177 admin surface: runner_pool is a
// first-class capability type — creatable, config-validated, and visible in
// the workspace capability picker (so a container_run node can target it).
func TestRunnerPoolCapability(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Runner Pool WS", shortKey("RPW"))

	t.Run("creates a runner_pool capability", func(t *testing.T) {
		payload := map[string]interface{}{
			"name":                      "CI Pool",
			"capability_type":           "runner_pool",
			"config":                    `{"max_concurrent_runs":4,"ephemeral":true}`,
			"applies_to_all_workspaces": true,
		}
		resp := MakeAuthRequest(t, server, http.MethodPost, "/admin/action-capabilities", payload)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
		var result map[string]interface{}
		DecodeJSON(t, resp, &result)
		AssertJSONField(t, result, "capability_type", "runner_pool")
	})

	t.Run("rejects negative max_concurrent_runs", func(t *testing.T) {
		payload := map[string]interface{}{
			"name":            "Bad Pool",
			"capability_type": "runner_pool",
			"config":          `{"max_concurrent_runs":-1}`,
		}
		resp := MakeAuthRequest(t, server, http.MethodPost, "/admin/action-capabilities", payload)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	t.Run("appears in the workspace picker", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/workspaces/%d/action-capabilities?type=runner_pool", workspaceID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var caps []map[string]interface{}
		DecodeJSON(t, resp, &caps)
		found := false
		for _, cap := range caps {
			if cap["capability_type"] != "runner_pool" {
				t.Fatalf("picker returned wrong type: %#v", cap)
			}
			if cap["name"] == "CI Pool" {
				found = true
			}
		}
		if !found {
			t.Fatalf("runner_pool capability not surfaced in picker: %#v", caps)
		}
	})
}

// TestRunnerPoolLifecycle covers the WI-177 admin lifecycle: minting,
// listing, and revoking registration tokens and runner instances as child
// resources of a runner_pool capability, including pool scoping.
func TestRunnerPoolLifecycle(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	// A runner_pool capability to manage.
	poolPayload := map[string]interface{}{
		"name":                      "Lifecycle Pool",
		"capability_type":           "runner_pool",
		"config":                    `{"max_concurrent_runs":2}`,
		"applies_to_all_workspaces": true,
	}
	resp := MakeAuthRequest(t, server, http.MethodPost, "/admin/action-capabilities", poolPayload)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var pool map[string]interface{}
	DecodeJSON(t, resp, &pool)
	poolID := ExtractIDFromResponse(t, pool)

	// A non-runner_pool capability — lifecycle endpoints must 404 for it.
	httpPayload := map[string]interface{}{
		"name":            "Not A Pool",
		"capability_type": "http_client",
		"config":          `{"allowed_url_patterns":["https://x.example.com/**"],"timeout_secs":30}`,
	}
	resp = MakeAuthRequest(t, server, http.MethodPost, "/admin/action-capabilities", httpPayload)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var httpCap map[string]interface{}
	DecodeJSON(t, resp, &httpCap)
	httpCapID := ExtractIDFromResponse(t, httpCap)

	tokensURL := fmt.Sprintf("/admin/action-capabilities/%d/runner-tokens", poolID)
	instancesURL := fmt.Sprintf("/admin/action-capabilities/%d/runner-instances", poolID)

	var registrationToken string
	var tokenID int

	t.Run("mints a registration token", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, tokensURL, map[string]interface{}{
			"description": "ci runners",
			"ttl_hours":   24,
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
		var out map[string]interface{}
		DecodeJSON(t, resp, &out)
		registrationToken, _ = out["token"].(string)
		if registrationToken == "" {
			t.Fatalf("expected plaintext token in response: %#v", out)
		}
		tokenID = int(out["id"].(float64))
	})

	t.Run("rejects negative ttl", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, tokensURL, map[string]interface{}{"ttl_hours": -5})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	t.Run("404 for a non-runner_pool capability", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/admin/action-capabilities/%d/runner-tokens", httpCapID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNotFound)
	})

	t.Run("lists the token", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, tokensURL, nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var tokens []map[string]interface{}
		DecodeJSON(t, resp, &tokens)
		if len(tokens) != 1 || int(tokens[0]["id"].(float64)) != tokenID {
			t.Fatalf("expected the minted token in list, got: %#v", tokens)
		}
		if _, leaked := tokens[0]["token"]; leaked {
			t.Fatalf("list must not expose plaintext token: %#v", tokens[0])
		}
	})

	t.Run("registers a runner, then lists and revokes it", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, "/runner/register", map[string]interface{}{
			"registration_token": registrationToken,
			"name":               "runner-1",
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
		var reg map[string]interface{}
		DecodeJSON(t, resp, &reg)
		instanceID := int(reg["instance_id"].(float64))

		resp = MakeAuthRequest(t, server, http.MethodGet, instancesURL, nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var instances []map[string]interface{}
		DecodeJSON(t, resp, &instances)
		if len(instances) != 1 || int(instances[0]["id"].(float64)) != instanceID {
			t.Fatalf("expected the registered runner in list, got: %#v", instances)
		}
		AssertJSONField(t, instances[0], "status", "active")

		resp = MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("%s/%d", instancesURL, instanceID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	t.Run("revokes the token", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("%s/%d", tokensURL, tokenID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		// A revoked token can no longer register a runner.
		resp = MakeAuthRequest(t, server, http.MethodPost, "/runner/register", map[string]interface{}{
			"registration_token": registrationToken,
			"name":               "runner-2",
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusUnauthorized)
	})

	t.Run("revoking a cross-pool token id 404s", func(t *testing.T) {
		// tokenID belongs to poolID; revoking it through the http capability's
		// URL must not succeed (it isn't even a runner pool).
		resp := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/admin/action-capabilities/%d/runner-tokens/%d", httpCapID, tokenID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNotFound)
	})
}

func TestActionCapabilityCRUD(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	var capabilityID int

	t.Run("Create", func(t *testing.T) {
		payload := map[string]interface{}{
			"name":            "Test HTTP Client",
			"capability_type": "http_client",
			"config":          `{"allowed_url_patterns":["https://api.example.com/*"],"timeout_secs":30}`,
		}
		resp := MakeAuthRequest(t, server, http.MethodPost, "/admin/action-capabilities", payload)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)

		var result map[string]interface{}
		DecodeJSON(t, resp, &result)
		capabilityID = ExtractIDFromResponse(t, result)

		AssertJSONField(t, result, "name", "Test HTTP Client")
		AssertJSONField(t, result, "capability_type", "http_client")
		AssertJSONField(t, result, "is_enabled", true)
		if result["config"] == nil || result["config"] == "" {
			t.Error("Expected config to be set")
		}
	})

	t.Run("List", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, "/admin/action-capabilities", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var capabilities []map[string]interface{}
		DecodeJSON(t, resp, &capabilities)
		if len(capabilities) < 1 {
			t.Fatal("Expected at least 1 capability")
		}

		found := false
		for _, cap := range capabilities {
			if int(cap["id"].(float64)) == capabilityID {
				found = true
				break
			}
		}
		if !found {
			t.Error("Created capability not found in list")
		}
	})

	t.Run("Get", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/admin/action-capabilities/%d", capabilityID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var result map[string]interface{}
		DecodeJSON(t, resp, &result)
		AssertJSONField(t, result, "name", "Test HTTP Client")
	})

	t.Run("Update", func(t *testing.T) {
		updatePayload := map[string]interface{}{
			"name":       "Updated HTTP Client",
			"config":     `{"allowed_url_patterns":["https://api.example.com/*","https://webhooks.example.com/*"],"timeout_secs":60}`,
			"is_enabled": false,
		}
		resp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/admin/action-capabilities/%d", capabilityID), updatePayload)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var result map[string]interface{}
		DecodeJSON(t, resp, &result)
		AssertJSONField(t, result, "name", "Updated HTTP Client")
		AssertJSONField(t, result, "is_enabled", false)
	})

	t.Run("Delete", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/admin/action-capabilities/%d", capabilityID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNoContent)

		// Verify it's gone
		resp2 := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/admin/action-capabilities/%d", capabilityID), nil)
		defer resp2.Body.Close()
		AssertStatusCode(t, resp2, http.StatusNotFound)
	})
}
