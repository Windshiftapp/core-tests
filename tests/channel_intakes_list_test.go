package tests

import (
	"fmt"
	"io"
	"net/http"
	"testing"
)

// TestChannelListIncludeIntakes covers the opt-in intake attachment on the
// channel listing that lets the portal settings page resolve connected
// mailboxes in one request instead of one per channel (WI-1921).
func TestChannelListIncludeIntakes(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Intake List Workspace", "ILST")
	_, portalID := SetupPortalChannel(t, server, workspaceID)

	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)
	itemTypeID := RequireItemTypeID(t, itemTypes, "Task")

	linkedMailbox := createMailboxChannel(t, server, "Linked mailbox")
	unlinkedMailbox := createMailboxChannel(t, server, "Unlinked mailbox")

	create := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/intakes", linkedMailbox), map[string]interface{}{
		"folder":            "INBOX",
		"portal_channel_id": portalID,
		"workspace_id":      workspaceID,
		"item_type_id":      itemTypeID,
	})
	if create.StatusCode != http.StatusCreated && create.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(create.Body)
		create.Body.Close()
		t.Fatalf("create intake: status=%d body=%s", create.StatusCode, body)
	}
	create.Body.Close()

	listChannels := func(t *testing.T, token, query string) []map[string]any {
		t.Helper()
		resp := MakeAuthRequestWithToken(t, server, token, http.MethodGet, "/channels"+query, nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var channels []map[string]any
		DecodeJSON(t, resp, &channels)
		return channels
	}
	indexByID := func(channels []map[string]any) map[int]map[string]any {
		byID := make(map[int]map[string]any, len(channels))
		for _, channel := range channels {
			byID[int(channel["id"].(float64))] = channel
		}
		return byID
	}

	t.Run("include_intakes attaches intakes and omits empty ones", func(t *testing.T) {
		channels := indexByID(listChannels(t, server.SessionCookie,
			"?type=email&direction=inbound&include_disabled=true&include_intakes=true"))

		linked := channels[linkedMailbox]
		if linked == nil {
			t.Fatalf("linked mailbox %d missing from channel list", linkedMailbox)
		}
		intakes, ok := linked["intakes"].([]any)
		if !ok || len(intakes) != 1 {
			t.Fatalf("linked mailbox intakes = %v, want 1", linked["intakes"])
		}
		first := intakes[0].(map[string]any)
		if int(first["portal_channel_id"].(float64)) != portalID || int(first["mailbox_id"].(float64)) != linkedMailbox {
			t.Fatalf("intake = %v, want portal %d on mailbox %d", first, portalID, linkedMailbox)
		}

		unlinked := channels[unlinkedMailbox]
		if unlinked == nil {
			t.Fatalf("unlinked mailbox %d missing from channel list", unlinkedMailbox)
		}
		if _, present := unlinked["intakes"]; present {
			t.Fatalf("unlinked mailbox intakes = %v, want the field omitted", unlinked["intakes"])
		}
	})

	t.Run("without the flag the channel list omits intakes", func(t *testing.T) {
		channels := listChannels(t, server.SessionCookie,
			"?type=email&direction=inbound&include_disabled=true")
		for _, channel := range channels {
			if _, present := channel["intakes"]; present {
				t.Fatalf("channel %v carries intakes without include_intakes", channel["id"])
			}
		}
	})

	t.Run("a channel manager sees only managed mailboxes with their intakes", func(t *testing.T) {
		managerID, managerUsername, managerPassword := CreateTestUserWithCredentials(
			t, server, "intake_list_manager", "intake_list_manager@test.com")
		addManager := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/channels/%d/managers", linkedMailbox), map[string]interface{}{
			"manager_type": "user",
			"manager_ids":  []int{managerID},
		})
		addManager.Body.Close()
		managerToken := CreateBearerTokenForUser(t, server, managerUsername, managerPassword)

		channels := listChannels(t, managerToken,
			"?type=email&direction=inbound&include_disabled=true&include_intakes=true")
		if len(channels) != 1 || int(channels[0]["id"].(float64)) != linkedMailbox {
			t.Fatalf("manager channels = %v, want only mailbox %d", channels, linkedMailbox)
		}
		if intakes, ok := channels[0]["intakes"].([]any); !ok || len(intakes) != 1 {
			t.Fatalf("manager intakes = %v, want 1", channels[0]["intakes"])
		}
	})
}
