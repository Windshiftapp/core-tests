// cookie_workspace_config_xss_test pins the WI-185 slice-8 sweep on
// the workspace-config CRUD family — workflows, screens, board
// configurations, condition sets, approval sets, permission sets,
// configuration sets. All are admin-only and all surface Name +
// Description in the admin directory + various picker/editor UIs.
//
// Tests target the easy create surfaces. condition_sets and
// approval_sets need a real workflow id; board_configuration needs
// a workspace + collection; the other four are standalone admin
// POSTs.
package tests

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCookieAuth_WorkflowXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)

	resp := MakeAuthRequest(t, ts, http.MethodPost, "/workflows",
		map[string]interface{}{
			"name":        "<script>alert(1)</script>BugWorkflow",
			"description": "Triage<img src=x onerror=evil()>flow",
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create workflow: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	if name, _ := got["name"].(string); strings.Contains(name, "<script") || name != "BugWorkflow" {
		t.Fatalf("workflow name unsanitized: %q", name)
	}
	if desc, _ := got["description"].(string); strings.Contains(desc, "<img") || strings.Contains(desc, "onerror") {
		t.Fatalf("workflow description unsanitized: %q", desc)
	}
	warnings, _ := got["warnings"].([]interface{})
	if len(warnings) == 0 {
		t.Fatalf("expected WI-186 warnings, got %v", got["warnings"])
	}
}

func TestCookieAuth_ScreenXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)

	resp := MakeAuthRequest(t, ts, http.MethodPost, "/screens",
		map[string]interface{}{
			"name":        "<script>alert(1)</script>BugScreen",
			"description": "Form<img src=x onerror=evil()>layout",
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create screen: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	if name, _ := got["name"].(string); strings.Contains(name, "<script") || name != "BugScreen" {
		t.Fatalf("screen name unsanitized: %q", name)
	}
	if desc, _ := got["description"].(string); strings.Contains(desc, "<img") || strings.Contains(desc, "onerror") {
		t.Fatalf("screen description unsanitized: %q", desc)
	}
}

func TestCookieAuth_PermissionSetXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)

	resp := MakeAuthRequest(t, ts, http.MethodPost, "/permission-sets",
		map[string]interface{}{
			"name":           "<script>alert(1)</script>Readonly",
			"description":    "View-only<img src=x onerror=evil()>permissions",
			"permission_ids": []int{},
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create permission set: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	if name, _ := got["name"].(string); strings.Contains(name, "<script") || name != "Readonly" {
		t.Fatalf("permission set name unsanitized: %q", name)
	}
	if desc, _ := got["description"].(string); strings.Contains(desc, "<img") || strings.Contains(desc, "onerror") {
		t.Fatalf("permission set description unsanitized: %q", desc)
	}
}

func TestCookieAuth_ConfigurationSetXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)

	resp := MakeAuthRequest(t, ts, http.MethodPost, "/configuration-sets",
		map[string]interface{}{
			"name":        "<script>alert(1)</script>BugConfig",
			"description": "Bug<img src=x onerror=evil()>set",
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create configuration set: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	// configuration_sets_crud responds via respondJSONWithWarnings —
	// the body shape is either the bare model or {data: ..., warnings:
	// [APIWarning]}. Either is fine for asserting scrub, but we need
	// to handle both shapes.
	DecodeJSON(t, resp, &got)
	model := got
	if d, ok := got["data"].(map[string]interface{}); ok {
		model = d
	}
	if name, _ := model["name"].(string); strings.Contains(name, "<script") || name != "BugConfig" {
		t.Fatalf("config set name unsanitized: %q", name)
	}
	if desc, _ := model["description"].(string); strings.Contains(desc, "<img") || strings.Contains(desc, "onerror") {
		t.Fatalf("config set description unsanitized: %q", desc)
	}
}
