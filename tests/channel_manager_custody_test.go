package tests

import (
	"fmt"
	"io"
	"net/http"
	"testing"
)

func TestChannelManagerCustodialOperationsRequireSystemAdmin(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	managerID, managerUsername, managerPassword := CreateTestUserWithCredentials(
		t,
		server,
		"channel_custody_manager",
		"channel-custody-manager@test.com",
	)
	_, otherUsername, otherPassword := CreateTestUserWithCredentials(
		t,
		server,
		"channel_custody_other",
		"channel-custody-other@test.com",
	)

	createResp := MakeAuthRequest(t, server, http.MethodPost, "/channels", map[string]interface{}{
		"name":      "Custody boundary",
		"type":      "smtp",
		"direction": "outbound",
		"status":    "disabled",
		"config":    "{}",
	})
	if createResp.StatusCode != http.StatusCreated && createResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(createResp.Body)
		createResp.Body.Close()
		t.Fatalf("create channel: status=%d body=%s", createResp.StatusCode, body)
	}
	var created map[string]interface{}
	DecodeJSON(t, createResp, &created)
	createResp.Body.Close()
	channelID := ExtractIDFromResponse(t, created)

	addResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/managers", channelID), map[string]interface{}{
		"manager_type": "user",
		"manager_ids":  []int{managerID},
	})
	if addResp.StatusCode != http.StatusCreated && addResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(addResp.Body)
		addResp.Body.Close()
		t.Fatalf("assign channel manager: status=%d body=%s", addResp.StatusCode, body)
	}
	addResp.Body.Close()

	unownedResp := MakeAuthRequest(t, server, http.MethodPost, "/channels", map[string]interface{}{
		"name":      "Unowned custody boundary",
		"type":      "smtp",
		"direction": "outbound",
		"status":    "disabled",
		"config":    "{}",
	})
	if unownedResp.StatusCode != http.StatusCreated && unownedResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(unownedResp.Body)
		unownedResp.Body.Close()
		t.Fatalf("create unowned channel: status=%d body=%s", unownedResp.StatusCode, body)
	}
	var unowned map[string]interface{}
	DecodeJSON(t, unownedResp, &unowned)
	unownedResp.Body.Close()
	unownedChannelID := ExtractIDFromResponse(t, unowned)

	managerCookie := CreateBearerTokenForUser(t, server, managerUsername, managerPassword)
	otherCookie := CreateBearerTokenForUser(t, server, otherUsername, otherPassword)

	t.Run("assigned manager can read managers", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(
			t,
			server,
			managerCookie,
			http.MethodGet,
			fmt.Sprintf("/channels/%d/managers", channelID),
			nil,
		)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	t.Run("unassigned user cannot enumerate managers", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(
			t,
			server,
			otherCookie,
			http.MethodGet,
			fmt.Sprintf("/channels/%d/managers", channelID),
			nil,
		)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
	})

	t.Run("assigned manager gets forbidden for an unowned channel", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(
			t,
			server,
			managerCookie,
			http.MethodGet,
			fmt.Sprintf("/channels/%d", unownedChannelID),
			nil,
		)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
	})

	denied := []struct {
		name   string
		method string
		path   string
		body   interface{}
	}{
		{
			name:   "appoint manager",
			method: http.MethodPost,
			path:   fmt.Sprintf("/channels/%d/managers", channelID),
			body: map[string]interface{}{
				"manager_type": "user",
				"manager_ids":  []int{managerID},
			},
		},
		{
			name:   "remove manager",
			method: http.MethodDelete,
			path:   fmt.Sprintf("/channels/%d/managers/1", channelID),
		},
		{
			name:   "delete channel",
			method: http.MethodDelete,
			path:   fmt.Sprintf("/channels/%d", channelID),
		},
		{
			name:   "test SMTP delivery",
			method: http.MethodPost,
			path:   fmt.Sprintf("/channels/%d/test", channelID),
			body:   map[string]interface{}{"test_email": "recipient@test.com"},
		},
		{
			name:   "start inline OAuth",
			method: http.MethodPost,
			path:   fmt.Sprintf("/channels/%d/inline-oauth/start", channelID),
			body:   map[string]interface{}{"restore_channel_enabled": false},
		},
	}

	for _, test := range denied {
		t.Run("assigned manager cannot "+test.name, func(t *testing.T) {
			resp := MakeAuthRequestWithToken(
				t,
				server,
				managerCookie,
				test.method,
				test.path,
				test.body,
			)
			defer resp.Body.Close()
			AssertStatusCode(t, resp, http.StatusForbidden)
		})
	}
}

func TestChannelManagerCanPreserveButNotExpandPortalWorkspaceBindings(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	managerID, managerUsername, managerPassword := CreateTestUserWithCredentials(
		t,
		server,
		"channel_appearance_manager",
		"channel-appearance-manager@test.com",
	)

	createWorkspace := func(name, key string) int {
		t.Helper()
		resp := MakeAuthRequest(t, server, http.MethodPost, "/workspaces", map[string]interface{}{
			"name":        name,
			"key":         key,
			"description": "Channel manager workspace binding test",
		})
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatalf("create workspace %s: status=%d body=%s", key, resp.StatusCode, body)
		}
		var created map[string]interface{}
		DecodeJSON(t, resp, &created)
		resp.Body.Close()
		return ExtractIDFromResponse(t, created)
	}
	connectedWorkspaceID := createWorkspace("Connected workspace", shortKey("CMCW"))
	unrelatedWorkspaceID := createWorkspace("Unrelated workspace", shortKey("CMUW"))

	createResp := MakeAuthRequest(t, server, http.MethodPost, "/channels", map[string]interface{}{
		"name":      "Managed portal appearance",
		"type":      "portal",
		"direction": "inbound",
		"status":    "disabled",
		"config":    "{}",
	})
	if createResp.StatusCode != http.StatusCreated && createResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(createResp.Body)
		createResp.Body.Close()
		t.Fatalf("create portal channel: status=%d body=%s", createResp.StatusCode, body)
	}
	var createdChannel map[string]interface{}
	DecodeJSON(t, createResp, &createdChannel)
	createResp.Body.Close()
	channelID := ExtractIDFromResponse(t, createdChannel)

	initialConfig := map[string]interface{}{
		"config": map[string]interface{}{
			"portal_slug":              "managed-portal-appearance",
			"portal_workspace_ids":     []int{connectedWorkspaceID},
			"portal_title":             "Original title",
			"portal_registration_mode": "open",
		},
	}
	initialResp := MakeAuthRequest(
		t,
		server,
		http.MethodPut,
		fmt.Sprintf("/channels/%d/config", channelID),
		initialConfig,
	)
	defer initialResp.Body.Close()
	AssertStatusCode(t, initialResp, http.StatusOK)

	addResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/managers", channelID), map[string]interface{}{
		"manager_type": "user",
		"manager_ids":  []int{managerID},
	})
	if addResp.StatusCode != http.StatusCreated && addResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(addResp.Body)
		addResp.Body.Close()
		t.Fatalf("assign channel manager: status=%d body=%s", addResp.StatusCode, body)
	}
	addResp.Body.Close()

	managerCookie := CreateBearerTokenForUser(t, server, managerUsername, managerPassword)

	t.Run("appearance change preserving workspace succeeds", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(
			t,
			server,
			managerCookie,
			http.MethodPut,
			fmt.Sprintf("/channels/%d/config", channelID),
			map[string]interface{}{
				"config": map[string]interface{}{
					"portal_slug":              "managed-portal-appearance",
					"portal_workspace_ids":     []int{connectedWorkspaceID},
					"portal_title":             "Manager-updated title",
					"portal_registration_mode": "open",
				},
			},
		)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	t.Run("adding an unadministered workspace is denied", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(
			t,
			server,
			managerCookie,
			http.MethodPut,
			fmt.Sprintf("/channels/%d/config", channelID),
			map[string]interface{}{
				"config": map[string]interface{}{
					"portal_workspace_ids": []int{connectedWorkspaceID, unrelatedWorkspaceID},
				},
			},
		)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
	})
}
