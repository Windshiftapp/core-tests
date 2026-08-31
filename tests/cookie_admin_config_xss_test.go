// cookie_admin_config_xss_test pins the WI-185 slice-4 sweep on the
// remaining high-priority cookie-auth admin surfaces: channels (incl.
// the webhook config that originally got its own entry — the webhook
// handler itself decodes only an item id, so the audit collapsed),
// workspace roles, and themes. Each handler now decodes free-form text
// (Name + Description, plus the four nav-color values on themes) and
// the sanitize sweep + WI-186 warnings surfacing land here.
package tests

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCookieAuth_ChannelXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)

	body := map[string]interface{}{
		"name":        "<script>alert(1)</script>OpsHook",
		"description": "ships<img src=x onerror=evil()>events",
		"type":        "webhook",
		"direction":   "outbound",
	}
	resp := MakeAuthRequest(t, ts, http.MethodPost, "/channels", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create channel: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	if name, _ := got["name"].(string); strings.Contains(name, "<script") {
		t.Fatalf("channel name unsanitized: %q", name)
	}
	if got["name"] != "OpsHook" {
		t.Fatalf("channel name = %v, want 'OpsHook'", got["name"])
	}
	if desc, _ := got["description"].(string); strings.Contains(desc, "<img") || strings.Contains(desc, "onerror") {
		t.Fatalf("channel description unsanitized: %q", desc)
	}

	// PUT path mirrors POST — pin it too. UpdateChannel decodes the full
	// model so any text field has to survive the same scrub.
	id := ExtractIDFromResponse(t, got)
	updateBody := map[string]interface{}{
		"name":        "<script>alert(2)</script>OpsHookV2",
		"description": "updated<img src=x onerror=evil()>events",
		"type":        "webhook",
		"direction":   "outbound",
		"status":      "disabled",
	}
	updResp := MakeAuthRequest(t, ts, http.MethodPut, fmt.Sprintf("/channels/%d", id), updateBody)
	defer updResp.Body.Close()
	if updResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(updResp.Body)
		t.Fatalf("update channel: %d %s", updResp.StatusCode, string(b))
	}
	var updGot map[string]interface{}
	DecodeJSON(t, updResp, &updGot)
	if name, _ := updGot["name"].(string); strings.Contains(name, "<script") || name != "OpsHookV2" {
		t.Fatalf("channel update name unsanitized: %q", name)
	}
	if desc, _ := updGot["description"].(string); strings.Contains(desc, "<img") || strings.Contains(desc, "onerror") {
		t.Fatalf("channel update description unsanitized: %q", desc)
	}
}

func TestCookieAuth_WorkspaceRoleXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)

	resp := MakeAuthRequest(t, ts, http.MethodPost, "/workspace-roles",
		map[string]interface{}{
			"name":        "<script>alert(1)</script>QA Lead",
			"description": "Owns<img src=x onerror=evil()>QA sign-off",
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create role: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	if name, _ := got["name"].(string); strings.Contains(name, "<script") {
		t.Fatalf("role name unsanitized: %q", name)
	}
	if got["name"] != "QA Lead" {
		t.Fatalf("role name = %v, want 'QA Lead'", got["name"])
	}
	if desc, _ := got["description"].(string); strings.Contains(desc, "<img") || strings.Contains(desc, "onerror") {
		t.Fatalf("role description unsanitized: %q", desc)
	}

	// WI-186 warnings surface — the response must carry a labeled
	// warning naming the Name field so the frontend toast machinery
	// has something to show.
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

func TestCookieAuth_ThemeXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)

	resp := MakeAuthRequest(t, ts, http.MethodPost, "/themes",
		map[string]interface{}{
			"name":                       "<script>alert(1)</script>Midnight",
			"description":                "dark<img src=x onerror=evil()>theme",
			"nav_background_color_light": "#ffffff<script>bad()</script>",
			"nav_text_color_light":       "#111111",
			"nav_background_color_dark":  "#0d1117<script>bad()</script>",
			"nav_text_color_dark":        "#f6f8fa",
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create theme: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	for _, field := range []string{"name", "description", "nav_background_color_light", "nav_background_color_dark"} {
		val, _ := got[field].(string)
		if strings.Contains(val, "<script") || strings.Contains(val, "onerror") {
			t.Fatalf("theme %s unsanitized: %q", field, val)
		}
	}
	if got["name"] != "Midnight" {
		t.Fatalf("theme name = %v, want 'Midnight'", got["name"])
	}
}
