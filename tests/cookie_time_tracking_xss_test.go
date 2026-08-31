// cookie_time_tracking_xss_test pins the WI-185 slice-9 sweep on the
// time-tracking handler family: customer organizations, project
// categories, projects, worklogs. All carry user-facing Name +
// Description / Color (categories + projects) or free-form description
// (worklogs).
package tests

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCookieAuth_TimeCustomerXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)

	resp := MakeAuthRequest(t, ts, http.MethodPost, "/customer-organisations",
		map[string]interface{}{
			"name":        "<script>alert(1)</script>AcmeCo",
			"email":       "billing@acme.example",
			"description": "Long-time<img src=x onerror=evil()>customer",
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create customer: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	if name, _ := got["name"].(string); strings.Contains(name, "<script") || name != "AcmeCo" {
		t.Fatalf("customer name unsanitized: %q", name)
	}
	if desc, _ := got["description"].(string); strings.Contains(desc, "<img") || strings.Contains(desc, "onerror") {
		t.Fatalf("customer description unsanitized: %q", desc)
	}
	warnings, _ := got["warnings"].([]interface{})
	if len(warnings) == 0 {
		t.Fatalf("expected WI-186 warnings, got %v", got["warnings"])
	}
}

func TestCookieAuth_TimeProjectCategoryXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)

	resp := MakeAuthRequest(t, ts, http.MethodPost, "/time/project-categories",
		map[string]interface{}{
			"name":        "<script>alert(1)</script>Engineering",
			"description": "Eng<img src=x onerror=evil()>work",
			"color":       "#1f6feb<script>bad()</script>",
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create category: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	for _, field := range []string{"name", "description", "color"} {
		val, _ := got[field].(string)
		if strings.Contains(val, "<script") || strings.Contains(val, "onerror") {
			t.Fatalf("category %s unsanitized: %q", field, val)
		}
	}
}

func TestCookieAuth_TimeProjectXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)

	// Create a customer to attach.
	custResp := MakeAuthRequest(t, ts, http.MethodPost, "/customer-organisations",
		map[string]interface{}{"name": "TestCustomer", "email": "test@example.com"})
	defer custResp.Body.Close()
	AssertStatusCode(t, custResp, http.StatusCreated)
	var custGot map[string]interface{}
	DecodeJSON(t, custResp, &custGot)
	customerID := ExtractIDFromResponse(t, custGot)

	resp := MakeAuthRequest(t, ts, http.MethodPost, "/time/projects",
		map[string]interface{}{
			"customer_id": customerID,
			"name":        "<script>alert(1)</script>WebsiteRedesign",
			"description": "Q1<img src=x onerror=evil()>2026",
			"color":       "#ff0000<script>bad()</script>",
			"hourly_rate": 150,
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create project: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	for _, field := range []string{"name", "description", "color"} {
		val, _ := got[field].(string)
		if strings.Contains(val, "<script") || strings.Contains(val, "onerror") {
			t.Fatalf("project %s unsanitized: %q", field, val)
		}
	}
}

func TestCookieAuth_TimeWorklogXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)

	// Seed customer + project so the worklog has a valid project_id.
	custResp := MakeAuthRequest(t, ts, http.MethodPost, "/customer-organisations",
		map[string]interface{}{"name": "WLCust", "email": "wl@example.com"})
	defer custResp.Body.Close()
	AssertStatusCode(t, custResp, http.StatusCreated)
	var custGot map[string]interface{}
	DecodeJSON(t, custResp, &custGot)
	customerID := ExtractIDFromResponse(t, custGot)

	projResp := MakeAuthRequest(t, ts, http.MethodPost, "/time/projects",
		map[string]interface{}{
			"customer_id": customerID,
			"name":        "WLProject",
			"hourly_rate": 100,
		})
	defer projResp.Body.Close()
	AssertStatusCode(t, projResp, http.StatusCreated)
	var projGot map[string]interface{}
	DecodeJSON(t, projResp, &projGot)
	projectID := ExtractIDFromResponse(t, projGot)

	resp := MakeAuthRequest(t, ts, http.MethodPost, "/time/worklogs",
		map[string]interface{}{
			"project_id":  projectID,
			"description": "Pair<script>alert(1)</script>session with Bob",
			"date":        "2026-06-04",
			"duration":    "1h",
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create worklog: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	desc, _ := got["description"].(string)
	if strings.Contains(desc, "<script") {
		t.Fatalf("worklog description unsanitized: %q", desc)
	}
	if !strings.Contains(desc, "Pair") || !strings.Contains(desc, "session with Bob") {
		t.Fatalf("worklog description lost legitimate copy: %q", desc)
	}
	// Print captured shape to help debug if needed
	if got["description"] == "" {
		_ = fmt.Sprint(got)
	}
}
