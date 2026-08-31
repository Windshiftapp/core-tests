// Page source remains on the legacy sanitizer until its diagram renderer can
// participate in the two-layer output boundary. Comments use the new exact
// source plus sanitized HTML contract.
package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestV1Pages_XSSSanitized_ViaService(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	token := ts.BearerToken
	wsID, _ := CreateTestWorkspace(t, ts, "Pages XSS WS", shortKey("PXW"))

	body := map[string]interface{}{
		"title":   "<script>alert(1)</script>Runbook",
		"content": "Step 1<br />Step 2<script>steal()</script>Step 3",
	}
	resp := MakeBearerRequestWithToken(t, ts, token, http.MethodPost,
		fmt.Sprintf("/rest/api/v1/workspaces/%d/pages", wsID), body)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	if title, _ := got["title"].(string); strings.Contains(title, "<script") {
		t.Fatalf("page title unsanitized: %q", title)
	}
	if got["title"] != "Runbook" {
		t.Fatalf("page title = %v, want 'Runbook'", got["title"])
	}
	content, _ := got["content"].(string)
	if strings.Contains(content, "<script") {
		t.Fatalf("page content unsanitized: %q", content)
	}
	if !strings.Contains(content, "<br />") {
		t.Fatalf("page content lost <br />: %q", content)
	}
}

func TestV1Comments_MarkdownSourceRoundTripsAndHTMLIsSanitized(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	token := ts.BearerToken

	wsID, _ := CreateTestWorkspace(t, ts, "Comment XSS WS", shortKey("CXW"))
	itemID := CreateTestItem(t, ts, wsID, "CommentTarget")

	content := "Looks good<script>alert(1)</script> — see [docs](javascript:alert(2))"
	body := map[string]interface{}{
		"content": content,
	}
	resp := MakeBearerRequestWithToken(t, ts, token, http.MethodPost,
		fmt.Sprintf("/rest/api/v1/items/%d/comments", itemID), body)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	if got["content"] != content {
		t.Fatalf("comment content = %v, want exact source %q", got["content"], content)
	}
	rendered, _ := got["content_html"].(string)
	if strings.Contains(rendered, "<script>") || strings.Contains(rendered, "javascript:") {
		t.Fatalf("comment content_html contains executable content: %q", rendered)
	}
	if !strings.Contains(rendered, "&lt;script&gt;") {
		t.Fatalf("comment content_html does not show raw HTML literally: %q", rendered)
	}
}

func TestV1ItemMarkdownRoundTripAndRenderedHTML(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	wsID, _ := CreateTestWorkspace(t, ts, "Markdown round trip", shortKey("MRT"))

	title := "Promise<Anything>"
	description := "Use `Promise<Anything>` <script>alert(1)</script>"
	resp := MakeBearerRequestWithToken(t, ts, ts.BearerToken, http.MethodPost,
		"/rest/api/v1/items", map[string]any{
			"workspace_id": wsID,
			"title":        title,
			"description":  description,
		})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var got struct {
		ID              int    `json:"id"`
		Title           string `json:"title"`
		Description     string `json:"description"`
		DescriptionHTML string `json:"description_html"`
	}
	DecodeJSON(t, resp, &got)
	if got.Title != title || got.Description != description {
		t.Fatalf("source round trip = title %q description %q, want %q and %q", got.Title, got.Description, title, description)
	}
	if !strings.Contains(got.DescriptionHTML, "<code>Promise&lt;Anything&gt;</code>") {
		t.Fatalf("description_html = %q, missing rendered inline code", got.DescriptionHTML)
	}
	if strings.Contains(got.DescriptionHTML, "<script>") {
		t.Fatalf("description_html executes raw HTML: %q", got.DescriptionHTML)
	}
}
