// cookie_actions_xss_test pins the WI-185 slice-2 sweep on the
// cookie-auth automation handlers (actions + integration
// capabilities). action Name / Description and capability Name are
// the user-facing free-form fields decoded into stores; covering
// them here closes the same class of gap WI-183 closed for the v1
// actions handler.
package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCookieAuth_ActionXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	wsID, _ := CreateTestWorkspace(t, ts, "Action XSS WS", shortKey("AXC"))

	body := map[string]interface{}{
		"name":         "<script>alert(1)</script>Auto-assign",
		"description":  "Triggers<img src=x onerror=evil()>on status change",
		"trigger_type": "status_transition",
		"nodes":        []interface{}{},
		"edges":        []interface{}{},
	}
	bodyJSON, _ := json.Marshal(body)
	resp := MakeAuthRequestRaw(t, ts, http.MethodPost,
		fmt.Sprintf("/workspaces/%d/actions", wsID), string(bodyJSON))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create action: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	if name, _ := got["name"].(string); strings.Contains(name, "<script") {
		t.Fatalf("action name unsanitized: %q", name)
	}
	if got["name"] != "Auto-assign" {
		t.Fatalf("action name = %v, want 'Auto-assign'", got["name"])
	}
	if desc, _ := got["description"].(string); strings.Contains(desc, "<img") || strings.Contains(desc, "onerror") {
		t.Fatalf("action description unsanitized: %q", desc)
	}
}
