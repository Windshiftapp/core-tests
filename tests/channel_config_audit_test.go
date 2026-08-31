package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/logger"
)

func TestChannelConfigUpdateCreatesAuditEvent(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	createResponse := MakeAuthRequest(t, server, http.MethodPost, "/channels", map[string]interface{}{
		"name":      "Audited widget channel",
		"type":      "widget",
		"direction": "inbound",
		"status":    "disabled",
		"config":    "{}",
	})
	defer createResponse.Body.Close()
	AssertStatusCode(t, createResponse, http.StatusCreated)
	var created map[string]interface{}
	DecodeJSON(t, createResponse, &created)
	channelID := ExtractIDFromResponse(t, created)

	updateResponse := MakeAuthRequest(t, server, http.MethodPut,
		fmt.Sprintf("/channels/%d/config", channelID), map[string]interface{}{
			"config": map[string]interface{}{"widget_theme": "dark"},
		})
	defer updateResponse.Body.Close()
	AssertStatusCode(t, updateResponse, http.StatusOK)

	var resourceType, resourceName, detailsJSON string
	var success bool
	if err := server.server.DB().QueryRow(`
		SELECT resource_type, resource_name, details, success
		FROM audit_logs
		WHERE action_type = ? AND resource_id = ?
		ORDER BY id DESC
		LIMIT 1
	`, logger.ActionChannelUpdate, channelID).Scan(&resourceType, &resourceName, &detailsJSON, &success); err != nil {
		t.Fatalf("load channel configuration audit: %v", err)
	}
	if resourceType != logger.ResourceChannel || resourceName != "Audited widget channel" || !success {
		t.Fatalf("channel configuration audit = resource %q name %q success %v", resourceType, resourceName, success)
	}
	var details map[string]interface{}
	if err := json.Unmarshal([]byte(detailsJSON), &details); err != nil {
		t.Fatalf("decode channel configuration audit details: %v", err)
	}
	if len(details) != 1 || details["change_type"] != "configuration" {
		t.Fatalf("channel configuration audit details = %#v", details)
	}
}
