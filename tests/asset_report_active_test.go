package tests

import (
	"fmt"
	"io"
	"net/http"
	"testing"
)

func TestAssetReportCreateActiveState(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)

	channelResp := MakeAuthRequest(t, server, http.MethodPost, "/channels", map[string]interface{}{
		"name":      "Asset report active state portal",
		"type":      "portal",
		"direction": "inbound",
		"status":    "disabled",
	})
	defer channelResp.Body.Close()
	if channelResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(channelResp.Body)
		t.Fatalf("create channel: %d %s", channelResp.StatusCode, string(body))
	}
	var channel map[string]interface{}
	DecodeJSON(t, channelResp, &channel)
	channelID := ExtractIDFromResponse(t, channel)
	assetSetID := createTestAssetSet(t, server, "Asset report active state set")

	tests := []struct {
		name       string
		isActive   *bool
		wantActive bool
	}{
		{name: "omitted defaults active", wantActive: true},
		{name: "explicit true stays active", isActive: boolPointer(true), wantActive: true},
		{name: "explicit false stays inactive", isActive: boolPointer(false), wantActive: false},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := map[string]interface{}{
				"name":         fmt.Sprintf("Active state report %d", index),
				"asset_set_id": assetSetID,
				"cql_query":    "name != null",
				"run_mode":     "direct",
			}
			if test.isActive != nil {
				body["is_active"] = *test.isActive
			}

			resp := MakeAuthRequest(t, server, http.MethodPost,
				fmt.Sprintf("/channels/%d/asset-reports", channelID), body)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusCreated {
				responseBody, _ := io.ReadAll(resp.Body)
				t.Fatalf("create asset report: %d %s", resp.StatusCode, string(responseBody))
			}

			var report map[string]interface{}
			DecodeJSON(t, resp, &report)
			active, ok := report["is_active"].(bool)
			if !ok {
				t.Fatalf("is_active response = %#v, want boolean", report["is_active"])
			}
			if active != test.wantActive {
				t.Fatalf("is_active = %t, want %t", active, test.wantActive)
			}
		})
	}
}

func boolPointer(value bool) *bool {
	return &value
}
