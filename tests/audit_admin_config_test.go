//go:build test

package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/logger"
	"windshift/internal/models"
)

// TestV2SLACalendarCreateWritesRecorderAudit covers WI-1900: an SLA
// configuration mutation emits a central audit row through the v2 recorder,
// with the authenticated actor populated.
func TestV2SLACalendarCreateWritesRecorderAudit(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	bearer := server.BearerToken
	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Audit Workspace", shortKey("SLA"))

	payload := map[string]any{
		"name":     "Audited support hours",
		"timezone": "UTC",
		"weekly_intervals": map[string]any{
			"monday": []map[string]string{{"start": "09:00", "end": "17:00"}},
		},
	}
	response := MakeBearerRequestWithToken(t, server, bearer, http.MethodPost,
		fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/calendars", workspaceID), payload)
	AssertStatusCode(t, response, http.StatusCreated)
	var calendar models.WorkingCalendar
	DecodeJSON(t, response, &calendar)
	_ = response.Body.Close()
	if calendar.ID == 0 {
		t.Fatal("calendar was not created")
	}

	var resourceType, username, ipAddress string
	var success bool
	if err := server.server.DB().QueryRow(`
		SELECT resource_type, username, ip_address, success
		FROM audit_logs
		WHERE action_type = ? AND resource_id = ?
		ORDER BY id DESC
		LIMIT 1
	`, logger.ActionSLACalendarCreate, calendar.ID).Scan(&resourceType, &username, &ipAddress, &success); err != nil {
		t.Fatalf("load SLA calendar audit: %v", err)
	}
	if resourceType != logger.ResourceSLACalendar || !success {
		t.Fatalf("SLA calendar audit = resource %q success %v", resourceType, success)
	}
	if username == "" || ipAddress == "" {
		t.Fatalf("SLA calendar audit actor not populated: username=%q ip=%q", username, ipAddress)
	}
}

// TestV1ObjectTranslationUpsertWritesAudit covers WI-1900: an object
// translation upsert emits an audit row naming the object, field, and locale.
func TestV1ObjectTranslationUpsertWritesAudit(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	token := CreateBearerToken(t, server)

	createResponse := MakeAuthRequestWithToken(t, server, token, http.MethodPost, "/priorities", map[string]any{
		"name": "Audited translation priority", "description": "", "icon": "AlertCircle",
		"color": "#123456", "sort_order": 90, "is_default": false,
	})
	AssertStatusCode(t, createResponse, http.StatusCreated)
	var priority struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, createResponse, &priority)
	_ = createResponse.Body.Close()

	upsertPath := fmt.Sprintf("/admin/object-translations/priority/%d/name/pt-br", priority.ID)
	upsertResponse := MakeAuthRequestWithToken(t, server, token, http.MethodPut, upsertPath, map[string]any{
		"value": "Prioridade auditada",
	})
	AssertStatusCode(t, upsertResponse, http.StatusOK)
	_ = upsertResponse.Body.Close()

	var resourceType, detailsJSON string
	var success bool
	if err := server.server.DB().QueryRow(`
		SELECT resource_type, details, success
		FROM audit_logs
		WHERE action_type = ? AND resource_id = ?
		ORDER BY id DESC
		LIMIT 1
	`, logger.ActionObjectTranslationUpdate, priority.ID).Scan(&resourceType, &detailsJSON, &success); err != nil {
		t.Fatalf("load object translation audit: %v", err)
	}
	if resourceType != logger.ResourceObjectTranslation || !success {
		t.Fatalf("object translation audit = resource %q success %v", resourceType, success)
	}
	var details map[string]any
	if err := json.Unmarshal([]byte(detailsJSON), &details); err != nil {
		t.Fatalf("decode translation audit details: %v", err)
	}
	if details["object_type"] != "priority" || details["field"] != "name" || details["locale"] != "pt-br" {
		t.Fatalf("translation audit details = %#v", details)
	}
}

// TestSecuritySettingsUpdateAuditsOldAndNew covers WI-1900: a settings update
// records the previous and new value, not just the final state.
func TestSecuritySettingsUpdateAuditsOldAndNew(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	response := MakeAuthRequest(t, server, http.MethodPut, "/admin/security-settings", map[string]any{
		"calendar_feed_enabled":     true,
		"plugin_cli_exec_enabled":   false,
		"allow_external_images":     true,
		"api_key_creation_policy":   "all_users",
		"api_key_allowed_group_ids": []int{},
		"allow_user_managed_agents": false,
		"max_agents_per_user":       5,
		"workspace_managed_agents":  true,
	})
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusOK)

	var detailsJSON string
	if err := server.server.DB().QueryRow(`
		SELECT details FROM audit_logs WHERE action_type = ? ORDER BY id DESC LIMIT 1
	`, logger.ActionSecuritySettingsUpdate).Scan(&detailsJSON); err != nil {
		t.Fatalf("load security settings audit: %v", err)
	}
	var details map[string]any
	if err := json.Unmarshal([]byte(detailsJSON), &details); err != nil {
		t.Fatalf("decode security settings audit details: %v", err)
	}
	change, ok := details["allow_external_images"].(map[string]any)
	if !ok {
		t.Fatalf("allow_external_images change missing: %#v", details)
	}
	if change["old"] != false || change["new"] != true {
		t.Fatalf("allow_external_images change = %#v, want old=false new=true", change)
	}
	if _, present := details["calendar_feed_enabled"]; present {
		t.Errorf("unchanged calendar_feed_enabled should not be in details: %#v", details)
	}
}

// TestWorkItemStalenessUpdateAuditsOldAndNew covers WI-1900: the staleness
// threshold update records the previous and new value.
func TestWorkItemStalenessUpdateAuditsOldAndNew(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	response := MakeAuthRequest(t, server, http.MethodPut, "/admin/work-item-staleness", map[string]any{
		"stale_after_days": 45,
	})
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusOK)

	var detailsJSON string
	if err := server.server.DB().QueryRow(`
		SELECT details FROM audit_logs WHERE action_type = ? ORDER BY id DESC LIMIT 1
	`, logger.ActionWorkItemStalenessUpdate).Scan(&detailsJSON); err != nil {
		t.Fatalf("load staleness audit: %v", err)
	}
	var details map[string]any
	if err := json.Unmarshal([]byte(detailsJSON), &details); err != nil {
		t.Fatalf("decode staleness audit details: %v", err)
	}
	change, ok := details["stale_after_days"].(map[string]any)
	if !ok {
		t.Fatalf("stale_after_days change missing: %#v", details)
	}
	if change["old"] != float64(30) || change["new"] != float64(45) {
		t.Fatalf("stale_after_days change = %#v, want old=30 new=45", change)
	}
}
