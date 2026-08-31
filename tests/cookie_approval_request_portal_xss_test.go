// cookie_approval_request_portal_xss_test pins the WI-185 slice-5
// sweep of the approval / request-type / portal medium-priority
// handlers.
//
// e2e coverage:
//
//   - request_types.Create — covered below.
//   - portal_drafts.SaveDraft — sanitize is applied at decode + a
//     warnings field is wrapped into the existing draftResponse map.
//     End-to-end coverage from the portal-customer session is
//     deferred to a follow-up; in this slice the change is small
//     (Apply → map insert) and the sanitize policy is already pinned
//     by internal/sanitize/sanitize_test.go.
//   - portal_approvals.DecideAsPortalCustomer + approvals.Decide /
//     Cancel / Delegate / RefreshApprovers — sanitize.Apply on
//     Comment before the service call. Building a live in-flight
//     approval request to drive end-to-end coverage requires a
//     multi-step approval-set + status-transition setup that's out
//     of scope for this slice; tracked as a follow-up.
package tests

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestCookieAuth_RequestTypeXSS — portal admin POSTs a new request type
// with payload in every free-form field on the model. All five fields
// (Name, Description, Icon, Color, TitleTemplate) must come back clean
// and the response carries the WI-186 warnings list.
func TestCookieAuth_RequestTypeXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	workspaceID, _ := CreateTestWorkspace(t, ts, "RT XSS Workspace", shortKey("RTX"))

	channelData := map[string]interface{}{
		"name":      "RT XSS Portal",
		"type":      "portal",
		"direction": "inbound",
		"status":    "enabled",
	}
	chResp := MakeAuthRequest(t, ts, http.MethodPost, "/channels", channelData)
	defer chResp.Body.Close()
	AssertStatusCode(t, chResp, http.StatusCreated)
	var chGot map[string]interface{}
	DecodeJSON(t, chResp, &chGot)
	channelID := ExtractIDFromResponse(t, chGot)
	configResp := MakeAuthRequest(t, ts, http.MethodPut,
		fmt.Sprintf("/channels/%d/config", channelID),
		map[string]interface{}{
			"config": map[string]interface{}{
				"portal_workspace_ids": []int{workspaceID},
			},
		})
	defer configResp.Body.Close()
	AssertStatusCode(t, configResp, http.StatusOK)

	configSetID := GetDefaultConfigurationSet(t, ts)
	itemTypes := GetItemTypes(t, ts, configSetID)
	itemTypeID := RequireItemTypeID(t, itemTypes, "Task")

	resp := MakeAuthRequest(t, ts, http.MethodPost,
		fmt.Sprintf("/channels/%d/request-types", channelID),
		map[string]interface{}{
			"name":           "<script>alert(1)</script>BugReport",
			"description":    "Use this<img src=x onerror=evil()>form",
			"item_type_id":   itemTypeID,
			"icon":           "<script>icon()</script>Bug",
			"color":          "#ef4444<script>bad()</script>",
			"title_template": "<script>tpl()</script>Bug: {{title}}",
			"is_active":      true,
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create request type: %d %s", resp.StatusCode, string(b))
	}

	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	for _, field := range []string{"name", "description", "icon", "color", "title_template"} {
		val, _ := got[field].(string)
		if strings.Contains(val, "<script") || strings.Contains(val, "onerror") {
			t.Fatalf("request type %s unsanitized: %q", field, val)
		}
	}
	if got["name"] != "BugReport" {
		t.Fatalf("request type name = %v, want 'BugReport'", got["name"])
	}
	// {{title}} placeholder stays — only the HTML markup gets stripped
	// from the surrounding template literal.
	if tpl, _ := got["title_template"].(string); !strings.Contains(tpl, "{{title}}") {
		t.Fatalf("title_template lost its placeholder: %q", tpl)
	}
	// WI-186 — warnings on the response naming the mutated fields.
	warnings, _ := got["warnings"].([]interface{})
	if len(warnings) == 0 {
		t.Fatalf("expected warnings on the sanitized response, got %v", got["warnings"])
	}
	var matchedName bool
	for _, w := range warnings {
		s, _ := w.(string)
		if strings.Contains(s, "Name") && strings.Contains(s, "HTML") {
			matchedName = true
		}
	}
	if !matchedName {
		t.Fatalf("expected a warning naming 'Name' + HTML, got %v", warnings)
	}
}
