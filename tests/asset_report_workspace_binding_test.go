//go:build test

package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// WI-1697: a form-mode asset report pins its workspace and item type at
// creation, matching request types. These are the HTTP contracts the admin UI
// and the portal rely on.

func TestAssetReportFormBindingIsRequiredAndImmutable(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "AR Binding WS", shortKey("ARBW"))
	_, channelID := SetupPortalChannel(t, server, wsID)
	taskID, storyID := requestTypeItemTypes(t, server)
	assetSetID := createTestAssetSet(t, server, "AR binding set")
	collectionURL := fmt.Sprintf("/channels/%d/asset-reports", channelID)

	// Form mode without a workspace is rejected.
	resp := MakeAuthRequest(t, server, http.MethodPost, collectionURL, map[string]interface{}{
		"name": "Unpinned report", "asset_set_id": assetSetID, "cql_query": "name != null",
		"run_mode": "form", "item_type_id": taskID,
	})
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "Workspace ID is required for form-mode asset reports") {
		t.Fatalf("create without workspace: status=%d body=%s, want 400 workspace-required", resp.StatusCode, body)
	}

	resp = MakeAuthRequest(t, server, http.MethodPost, collectionURL, map[string]interface{}{
		"name": "Pinned report", "asset_set_id": assetSetID, "cql_query": "name != null",
		"run_mode": "form", "item_type_id": taskID, "workspace_id": wsID,
	})
	AssertStatusCode(t, resp, http.StatusCreated)
	var report map[string]interface{}
	DecodeJSON(t, resp, &report)
	resp.Body.Close()
	reportID := intField(report, "id")
	itemURL := fmt.Sprintf("/channels/%d/asset-reports/%d", channelID, reportID)

	otherWS, _ := CreateTestWorkspace(t, server, "AR Retarget WS", shortKey("AROW"))

	resp = MakeAuthRequest(t, server, http.MethodPut, itemURL, map[string]interface{}{
		"name": "Pinned report", "asset_set_id": assetSetID, "cql_query": "name != null",
		"run_mode": "form", "item_type_id": taskID, "workspace_id": otherWS, "is_active": true,
	})
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "cannot be changed after creation") {
		t.Fatalf("retarget workspace: status=%d body=%s, want 400 binding-immutable", resp.StatusCode, body)
	}

	resp = MakeAuthRequest(t, server, http.MethodPut, itemURL, map[string]interface{}{
		"name": "Pinned report", "asset_set_id": assetSetID, "cql_query": "name != null",
		"run_mode": "form", "item_type_id": storyID, "workspace_id": wsID, "is_active": true,
	})
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "cannot be changed after creation") {
		t.Fatalf("change item type: status=%d body=%s, want 400 binding-immutable", resp.StatusCode, body)
	}

	resp = MakeAuthRequest(t, server, http.MethodPut, itemURL, map[string]interface{}{
		"name": "Pinned report", "asset_set_id": assetSetID, "cql_query": "name != null",
		"run_mode": "direct", "is_active": true,
	})
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "cannot be changed to direct mode") {
		t.Fatalf("downgrade to direct: status=%d body=%s, want 400 mode-immutable", resp.StatusCode, body)
	}

	// An unchanged binding still saves, and the rejected attempts left the
	// persisted binding untouched.
	resp = MakeAuthRequest(t, server, http.MethodPut, itemURL, map[string]interface{}{
		"name": "Renamed report", "asset_set_id": assetSetID, "cql_query": "name != null",
		"run_mode": "form", "item_type_id": taskID, "workspace_id": wsID, "is_active": true,
	})
	AssertStatusCode(t, resp, http.StatusOK)
	resp.Body.Close()

	verifyResp := MakeAuthRequest(t, server, http.MethodGet, collectionURL, nil)
	defer verifyResp.Body.Close()
	AssertStatusCode(t, verifyResp, http.StatusOK)
	var reports []map[string]interface{}
	DecodeJSON(t, verifyResp, &reports)
	for _, candidate := range reports {
		if intField(candidate, "id") != reportID {
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
	t.Fatalf("asset report %d missing after update", reportID)
}

// A direct report may be upgraded to form mode once; the established binding is
// then fixed and the report cannot return to direct mode.
func TestAssetReportDirectToFormUpgradeLocksBinding(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "AR Upgrade WS", shortKey("ARUW"))
	_, channelID := SetupPortalChannel(t, server, wsID)
	taskID, _ := requestTypeItemTypes(t, server)
	assetSetID := createTestAssetSet(t, server, "AR upgrade set")
	collectionURL := fmt.Sprintf("/channels/%d/asset-reports", channelID)

	resp := MakeAuthRequest(t, server, http.MethodPost, collectionURL, map[string]interface{}{
		"name": "Upgradable report", "asset_set_id": assetSetID, "cql_query": "name != null", "run_mode": "direct",
	})
	AssertStatusCode(t, resp, http.StatusCreated)
	var report map[string]interface{}
	DecodeJSON(t, resp, &report)
	resp.Body.Close()
	reportID := intField(report, "id")
	itemURL := fmt.Sprintf("/channels/%d/asset-reports/%d", channelID, reportID)

	resp = MakeAuthRequest(t, server, http.MethodPut, itemURL, map[string]interface{}{
		"name": "Upgraded report", "asset_set_id": assetSetID, "cql_query": "name != null",
		"run_mode": "form", "item_type_id": taskID, "workspace_id": wsID, "is_active": true,
	})
	AssertStatusCode(t, resp, http.StatusOK)
	resp.Body.Close()

	otherWS, _ := CreateTestWorkspace(t, server, "AR Upgrade Other", shortKey("ARUO"))
	resp = MakeAuthRequest(t, server, http.MethodPut, itemURL, map[string]interface{}{
		"name": "Upgraded report", "asset_set_id": assetSetID, "cql_query": "name != null",
		"run_mode": "form", "item_type_id": taskID, "workspace_id": otherWS, "is_active": true,
	})
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "cannot be changed after creation") {
		t.Fatalf("retarget after upgrade: status=%d body=%s, want 400 binding-immutable", resp.StatusCode, body)
	}

	resp = MakeAuthRequest(t, server, http.MethodPut, itemURL, map[string]interface{}{
		"name": "Upgraded report", "asset_set_id": assetSetID, "cql_query": "name != null",
		"run_mode": "direct", "is_active": true,
	})
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "cannot be changed to direct mode") {
		t.Fatalf("downgrade after upgrade: status=%d body=%s, want 400 mode-immutable", resp.StatusCode, body)
	}
}
