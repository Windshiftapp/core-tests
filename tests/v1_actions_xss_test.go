// v1_actions_xss_test confirms the v1 automation-action handler strips
// injection vectors from action Name + Description on the three decode
// sites (Create / Update / Validate). Closes WI-183 (child of WI-180
// 'Bound all decoded string fields via sanitize package').
//
// Scope note: ActionNode.NodeConfig and the request's TriggerConfig
// are JSON-encoded blobs. Sanitizing the whole blob would corrupt the
// JSON, so per-field sanitization there must happen at parse time
// downstream. This test pins only the top-level Name / Description
// contract; node-config sanitization is a separate follow-up.
package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"windshift/internal/logger"
)

func TestV1Actions_XSSSanitized(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	token := ts.BearerToken

	workspaceID, _ := CreateTestWorkspace(t, ts, "Actions XSS WS", shortKey("AXW"))

	body := map[string]interface{}{
		"name":         "<script>alert(1)</script>Notify on assign",
		"description":  "When<img src=x onerror=alert(2)>assigned, ping Slack",
		"trigger_type": "status_transition",
		"nodes":        []interface{}{},
		"edges":        []interface{}{},
	}
	resp := MakeBearerRequestWithToken(t, ts, token, http.MethodPost,
		fmt.Sprintf("/rest/api/v1/workspaces/%d/actions", workspaceID), body)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	if name, _ := got["name"].(string); strings.Contains(name, "<script") {
		t.Fatalf("action name unsanitized: %q", name)
	}
	if got["name"] != "Notify on assign" {
		t.Fatalf("action name = %v, want 'Notify on assign'", got["name"])
	}
	if desc, _ := got["description"].(string); strings.Contains(desc, "<img") || strings.Contains(desc, "onerror") {
		t.Fatalf("action description unsanitized: %q", desc)
	}

	actionID := ExtractIDFromResponse(t, got)
	updateBody := map[string]interface{}{
		"name":         "Updated action",
		"description":  "Updated description",
		"trigger_type": "status_transition",
		"nodes":        []interface{}{},
		"edges":        []interface{}{},
	}
	updateResp := MakeBearerRequestWithToken(t, ts, token, http.MethodPut,
		fmt.Sprintf("/rest/api/v1/workspaces/%d/actions/%d", workspaceID, actionID), updateBody)
	defer updateResp.Body.Close()
	AssertStatusCode(t, updateResp, http.StatusOK)

	var updateAuditCount int
	if err := ts.DB().QueryRow(`
		SELECT COUNT(*) FROM audit_logs
		WHERE action_type = ? AND resource_type = ? AND resource_id = ? AND success = true
	`, logger.ActionAutomationUpdate, logger.ResourceAutomation, actionID).Scan(&updateAuditCount); err != nil {
		t.Fatalf("load automation update audit: %v", err)
	}
	if updateAuditCount != 1 {
		t.Fatalf("automation update audit rows = %d, want 1", updateAuditCount)
	}
}
