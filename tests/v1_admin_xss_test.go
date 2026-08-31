// v1_admin_xss_test confirms the v1 admin handlers strip injection
// vectors from their decoded JSON bodies before persisting. Closes
// WI-181 (children of WI-180: "Bound all decoded string fields via
// sanitize package"). The admin surface is highly sensitive — a
// stored payload in a group name or user display name renders on
// every page that surfaces the user/group, so the sanitize sweep
// must cover it.
package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"windshift/internal/logger"
)

// TestV1AdminGroups_Warnings — WI-186 pilot: when sanitize mutates a
// decoded field, the create response carries a user-facing warning
// the frontend toast machinery surfaces at info severity. Clean
// input produces no warnings field at all (omitempty).
func TestV1AdminGroups_Warnings(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	token := ts.BearerToken

	t.Run("html_in_name_yields_warning", func(t *testing.T) {
		body := map[string]interface{}{
			"name":        "<script>alert(1)</script>EngTeam",
			"description": "plain description",
		}
		resp := MakeBearerRequestWithToken(t, ts, token, http.MethodPost, "/rest/api/v1/admin/groups", body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
		var got map[string]interface{}
		DecodeJSON(t, resp, &got)
		warnings, ok := got["warnings"].([]interface{})
		if !ok || len(warnings) == 0 {
			t.Fatalf("expected warnings on the response, got %v", got["warnings"])
		}
		var matched bool
		for _, w := range warnings {
			s, _ := w.(string)
			if strings.Contains(s, "Group name") && strings.Contains(s, "HTML") {
				matched = true
			}
		}
		if !matched {
			t.Fatalf("expected a warning naming 'Group name' + HTML, got %v", warnings)
		}
	})

	t.Run("clean_input_omits_warnings_field", func(t *testing.T) {
		body := map[string]interface{}{
			"name":        "Clean Group",
			"description": "A plain description",
		}
		resp := MakeBearerRequestWithToken(t, ts, token, http.MethodPost, "/rest/api/v1/admin/groups", body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
		var got map[string]interface{}
		DecodeJSON(t, resp, &got)
		if _, present := got["warnings"]; present {
			t.Fatalf("clean input must omit warnings field entirely (omitempty); got %v", got["warnings"])
		}
	})

	t.Run("overlength_name_yields_truncation_warning", func(t *testing.T) {
		body := map[string]interface{}{
			"name":        strings.Repeat("a", 300),
			"description": "ok",
		}
		resp := MakeBearerRequestWithToken(t, ts, token, http.MethodPost, "/rest/api/v1/admin/groups", body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
		var got map[string]interface{}
		DecodeJSON(t, resp, &got)
		warnings, _ := got["warnings"].([]interface{})
		if len(warnings) == 0 {
			t.Fatalf("expected truncation warning, got none")
		}
		s, _ := warnings[0].(string)
		if !strings.Contains(s, "shortened to 256") {
			t.Fatalf("warning didn't mention 256-char cap: %q", s)
		}
	})
}

func TestV1AdminGroups_XSSSanitized(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	token := ts.BearerToken

	t.Run("create_strips_script", func(t *testing.T) {
		body := map[string]interface{}{
			"name":        "<script>alert(1)</script>EngTeam",
			"description": "before<script>steal()</script>after",
		}
		resp := MakeBearerRequestWithToken(t, ts, token, http.MethodPost, "/rest/api/v1/admin/groups", body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
		var got map[string]interface{}
		DecodeJSON(t, resp, &got)
		if name, _ := got["name"].(string); strings.Contains(name, "<script") {
			t.Fatalf("create response carries unsanitized name: %q", name)
		}
		if name, _ := got["name"].(string); name != "EngTeam" {
			t.Fatalf("expected name 'EngTeam' after sanitize, got %q", name)
		}
		if desc, _ := got["description"].(string); strings.Contains(desc, "<script") {
			t.Fatalf("create response carries unsanitized description: %q", desc)
		}
	})

	t.Run("update_strips_script_and_persists_clean", func(t *testing.T) {
		// Seed a group to update.
		createResp := MakeBearerRequestWithToken(t, ts, token, http.MethodPost,
			"/rest/api/v1/admin/groups",
			map[string]interface{}{"name": "Seed", "description": ""},
		)
		defer createResp.Body.Close()
		AssertStatusCode(t, createResp, http.StatusCreated)
		var created map[string]interface{}
		DecodeJSON(t, createResp, &created)
		id := ExtractIDFromResponse(t, created)

		updateBody := map[string]interface{}{
			"name":        "<img src=x onerror=alert(1)>QA",
			"description": "Quality\n<script>x</script>Assurance",
		}
		upResp := MakeBearerRequestWithToken(t, ts, token, http.MethodPut,
			fmt.Sprintf("/rest/api/v1/admin/groups/%d", id), updateBody)
		defer upResp.Body.Close()
		AssertStatusCode(t, upResp, http.StatusNoContent)

		// Confirm via list — fetched-back row must be clean.
		listResp := MakeBearerRequestWithToken(t, ts, token, http.MethodGet, "/rest/api/v1/admin/groups", nil)
		defer listResp.Body.Close()
		AssertStatusCode(t, listResp, http.StatusOK)
		var listOut struct {
			Data []map[string]interface{} `json:"data"`
		}
		if err := json.NewDecoder(listResp.Body).Decode(&listOut); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		var row map[string]interface{}
		for _, g := range listOut.Data {
			if int(g["id"].(float64)) == id {
				row = g
				break
			}
		}
		if row == nil {
			b, _ := io.ReadAll(listResp.Body)
			t.Fatalf("group %d missing from list: %s", id, string(b))
		}
		if name, _ := row["name"].(string); strings.Contains(name, "<img") || strings.Contains(name, "onerror") {
			t.Fatalf("persisted name carries img/onerror: %q", name)
		}
		if desc, _ := row["description"].(string); strings.Contains(desc, "<script") {
			t.Fatalf("persisted description carries <script>: %q", desc)
		}
	})
}

func TestV1AdminUsers_XSSSanitized_DisplayName(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	token := ts.BearerToken

	// Seed a non-admin user so we have an id to update.
	targetID, _, _ := CreateTestUserWithCredentials(t, ts, "xss_target", "xss_target@test.com")

	body := map[string]interface{}{
		"first_name": "<script>alert(1)</script>Alice",
		"last_name":  "<img src=x onerror=evil()>Smith",
	}
	resp := MakeBearerRequestWithToken(t, ts, token, http.MethodPut,
		fmt.Sprintf("/rest/api/v1/admin/users/%d", targetID), body)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	for _, field := range []string{"first_name", "last_name"} {
		val, _ := got[field].(string)
		if strings.Contains(val, "<script") || strings.Contains(val, "onerror") {
			t.Fatalf("admin user %s carries unsanitized payload: %q", field, val)
		}
	}
	if got["first_name"] != "Alice" {
		t.Fatalf("first_name = %v, want 'Alice'", got["first_name"])
	}
	if got["last_name"] != "Smith" {
		t.Fatalf("last_name = %v, want 'Smith'", got["last_name"])
	}

	var auditCount int
	if err := ts.DB().QueryRow(`
		SELECT COUNT(*) FROM audit_logs
		WHERE action_type = ? AND resource_type = ? AND resource_id = ? AND success = true
	`, logger.ActionUserUpdate, logger.ResourceUser, targetID).Scan(&auditCount); err != nil {
		t.Fatalf("load user update audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("user update audit rows = %d, want 1", auditCount)
	}
}
