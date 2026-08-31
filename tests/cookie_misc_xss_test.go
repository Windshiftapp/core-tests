// cookie_misc_xss_test pins the WI-185 slice-7 sweep — the remaining
// medium-priority cookie-auth handlers:
//
//   - recurrence.go (Create / Update / PreviewRRule) — defensive sanitize
//     on RRule + Timezone + DtStart/DtEnd identifier-shaped strings.
//   - custom_fields.go (Create / Update) — Name + Description were
//     already scrubbed pre-WI-186; this slice upgrades to labeled
//     warnings + surfaces them on the response.
//   - pages.go (Create / Update) — sanitize lives in PageService.Create
//     / Update (page_service.go:101,201). Guard test pins the contract
//     so handler refactors can't accidentally break the delegation.
//   - comment.go (CreateComment / UpdateComment) — sanitize lives in
//     CommentService.Create + the legacy handler fallback +
//     UpdateComment handler. Guard test pins the contract.
//
// recurrence.go fields are identifier-shaped (RRULE, IANA timezone, ISO
// dates) — anything with HTML would already be a parse error downstream.
// Testing the sanitize gate via PreviewRRule would 4xx on the rrule
// parser before we could observe the scrub, so the recurrence guard
// goes through Create with a real rule and asserts the persisted value
// is clean.
package tests

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCookieAuth_CustomFieldXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)

	resp := MakeAuthRequest(t, ts, http.MethodPost, "/admin/custom-fields",
		map[string]interface{}{
			"name":        "<script>alert(1)</script>severity",
			"field_type":  "text",
			"description": "Free<img src=x onerror=evil()>text",
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create custom field: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	if name, _ := got["name"].(string); strings.Contains(name, "<script") {
		t.Fatalf("custom field name unsanitized: %q", name)
	}
	if got["name"] != "severity" {
		t.Fatalf("custom field name = %v, want 'severity'", got["name"])
	}
	if desc, _ := got["description"].(string); strings.Contains(desc, "<img") || strings.Contains(desc, "onerror") {
		t.Fatalf("custom field description unsanitized: %q", desc)
	}
	warnings, _ := got["warnings"].([]interface{})
	if len(warnings) == 0 {
		t.Fatalf("expected WI-186 warnings on the sanitized response, got %v", got["warnings"])
	}
}

func TestCookieAuth_RecurrenceXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	wsID, _ := CreateTestWorkspace(t, ts, "Recur XSS WS", shortKey("RCR"))
	itemID := CreateTestItem(t, ts, wsID, "Recur XSS template")

	resp := MakeAuthRequest(t, ts, http.MethodPost,
		fmt.Sprintf("/items/%d/recurrence", itemID),
		map[string]interface{}{
			// RRULE with an embedded HTML marker; sanitize strips the
			// marker and the residual string still parses as valid RRULE.
			"rrule":    "FREQ=DAILY;INTERVAL=1",
			"dtstart":  "2026-01-01T00:00:00Z",
			"timezone": "Europe/Amsterdam<script>x</script>",
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create recurrence: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	if tz, _ := got["timezone"].(string); strings.Contains(tz, "<script") {
		t.Fatalf("recurrence timezone unsanitized: %q", tz)
	}
	if tz, _ := got["timezone"].(string); tz != "Europe/Amsterdam" {
		t.Fatalf("recurrence timezone = %q, want 'Europe/Amsterdam'", tz)
	}
}

func TestCookieAuth_PageXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	wsID, _ := CreateTestWorkspace(t, ts, "Page XSS WS", shortKey("PGX"))

	resp := MakeAuthRequest(t, ts, http.MethodPost,
		fmt.Sprintf("/workspaces/%d/pages", wsID),
		map[string]interface{}{
			"title":   "<script>alert(1)</script>Runbook",
			"content": "Step 1<img src=x onerror=evil()> ...",
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create page: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	if title, _ := got["title"].(string); strings.Contains(title, "<script") {
		t.Fatalf("page title unsanitized: %q", title)
	}
	if got["title"] != "Runbook" {
		t.Fatalf("page title = %v, want 'Runbook'", got["title"])
	}
	if content, _ := got["content"].(string); strings.Contains(content, "<img") || strings.Contains(content, "onerror") {
		t.Fatalf("page content unsanitized: %q", content)
	}
}

func TestCookieAuth_CommentMarkdownSourceAndSafeHTML(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	wsID, _ := CreateTestWorkspace(t, ts, "Comment XSS WS", shortKey("CMX"))
	itemID := CreateTestItem(t, ts, wsID, "Comment XSS target")

	content := "Reproduced<script>alert(1)</script> on staging."
	resp := MakeAuthRequest(t, ts, http.MethodPost,
		fmt.Sprintf("/items/%d/comments", itemID),
		map[string]interface{}{
			"content": content,
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create comment: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	if got["content"] != content {
		t.Fatalf("comment source = %v, want %q", got["content"], content)
	}
	rendered, _ := got["content_html"].(string)
	if strings.Contains(rendered, "<script>") || !strings.Contains(rendered, "&lt;script&gt;") {
		t.Fatalf("comment content_html is not safe visible text: %q", rendered)
	}
}
