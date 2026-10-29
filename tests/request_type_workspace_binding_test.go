//go:build test

package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// WI-1695: a request type's workspace and item type are fixed at creation.
// These are the black-box HTTP contracts the portal/form UIs and the admin
// API rely on after the implicit first-served-workspace fallback was removed.

func requestTypeItemTypes(t *testing.T, server *TestServer) (taskID, storyID int) {
	t.Helper()
	configSetID := GetDefaultConfigurationSet(t, server)
	itemTypes := GetItemTypes(t, server, configSetID)
	return RequireItemTypeID(t, itemTypes, "Task"), RequireItemTypeID(t, itemTypes, "Story")
}

func TestCreateRequestTypeRequiresServedWorkspace(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "RT Binding WS", shortKey("RTBW"))
	_, channelID := SetupPortalChannel(t, server, wsID)
	taskID, _ := requestTypeItemTypes(t, server)

	resp := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/channels/%d/request-types", channelID),
		map[string]interface{}{"name": "Unpinned", "item_type_id": taskID, "is_active": true})
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "Workspace is required for this request type") {
		t.Fatalf("create without workspace: status=%d body=%s, want 400 workspace-required", resp.StatusCode, body)
	}

	otherWS, _ := CreateTestWorkspace(t, server, "RT Other WS", shortKey("RTOW"))
	resp = MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/channels/%d/request-types", channelID),
		map[string]interface{}{"name": "Foreign", "item_type_id": taskID, "workspace_id": otherWS, "is_active": true})
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "Workspace is not served by this channel") {
		t.Fatalf("create with unserved workspace: status=%d body=%s, want 400 not-served", resp.StatusCode, body)
	}
}

func TestUpdateRequestTypeBindingIsImmutable(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "RT Immutable WS", shortKey("RTIW"))
	_, channelID := SetupPortalChannel(t, server, wsID)
	taskID, storyID := requestTypeItemTypes(t, server)

	listResp := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/channels/%d/request-types", channelID), nil)
	AssertStatusCode(t, listResp, http.StatusOK)
	var requestTypes []map[string]interface{}
	DecodeJSON(t, listResp, &requestTypes)
	listResp.Body.Close()

	var seeded map[string]interface{}
	for _, candidate := range requestTypes {
		if candidate["name"] == "General Request" {
			seeded = candidate
			break
		}
	}
	if seeded == nil {
		t.Fatalf("seeded General Request not found in %v", requestTypes)
	}
	requestTypeID := intField(seeded, "id")
	path := fmt.Sprintf("/channels/%d/request-types/%d", channelID, requestTypeID)

	otherWS, _ := CreateTestWorkspace(t, server, "RT Retarget WS", shortKey("RTRW"))
	resp := MakeAuthRequest(t, server, http.MethodPut, path, map[string]interface{}{
		"name": "General Request", "item_type_id": taskID, "workspace_id": otherWS, "is_active": true,
	})
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "Request type workspace cannot be changed after creation") {
		t.Fatalf("retarget workspace: status=%d body=%s, want 400 workspace-immutable", resp.StatusCode, body)
	}

	resp = MakeAuthRequest(t, server, http.MethodPut, path, map[string]interface{}{
		"name": "General Request", "item_type_id": storyID, "workspace_id": wsID, "is_active": true,
	})
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "Request type item type cannot be changed after creation") {
		t.Fatalf("change item type: status=%d body=%s, want 400 item-type-immutable", resp.StatusCode, body)
	}

	// An unchanged binding still saves, and the rejected attempts left the
	// persisted routing untouched.
	resp = MakeAuthRequest(t, server, http.MethodPut, path, map[string]interface{}{
		"name": "Renamed Request", "item_type_id": taskID, "workspace_id": wsID, "is_active": true,
	})
	AssertStatusCode(t, resp, http.StatusOK)
	resp.Body.Close()

	verifyResp := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/channels/%d/request-types", channelID), nil)
	defer verifyResp.Body.Close()
	AssertStatusCode(t, verifyResp, http.StatusOK)
	var after []map[string]interface{}
	DecodeJSON(t, verifyResp, &after)
	for _, candidate := range after {
		if intField(candidate, "id") != requestTypeID {
			continue
		}
		if got := intField(candidate, "workspace_id"); got != wsID {
			t.Fatalf("persisted workspace_id = %d, want %d after rejected retargets", got, wsID)
		}
		if got := intField(candidate, "item_type_id"); got != taskID {
			t.Fatalf("persisted item_type_id = %d, want %d after rejected change", got, taskID)
		}
		return
	}
	t.Fatalf("request type %d missing after update", requestTypeID)
}
