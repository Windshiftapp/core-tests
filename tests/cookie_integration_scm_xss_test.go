// cookie_integration_scm_xss_test pins the WI-185 slice-6 sweep on the
// integration/SCM/links handler family. Coverage focuses on the
// provider-create surfaces — the easiest end-to-end entry points and
// the handlers most likely to render sanitize warnings to an admin.
//
// Out of scope for this slice's tests but covered by handler-side
// sanitize.Apply: integration_item_links.CreateItemLink (needs an item
// + integration provider linked to a real OAuth flow), scm_item_links
// CreateBranchForItem / CreatePRFromBranch (need a live SCM
// connection), scm_workspace LinkRepository / UpdateRepository (need
// a workspace SCM connection seeded from a real provider response).
// All of those land sanitize.Apply at decode in this slice's handler
// commit and the policy is pinned by internal/sanitize/sanitize_test.go.
// item_links.go is documented as a no-op for this sweep — its
// CreateLink decoder has no user-supplied free-form text fields.
package tests

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCookieAuth_IntegrationProviderXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)

	resp := MakeAuthRequest(t, ts, http.MethodPost, "/admin/integration-providers",
		map[string]interface{}{
			"name":          "<script>alert(1)</script>NotionDocs",
			"slug":          "notion-docs<script>x</script>",
			"provider_type": "notion",
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create integration provider: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	if name, _ := got["name"].(string); strings.Contains(name, "<script") {
		t.Fatalf("provider name unsanitized: %q", name)
	}
	if got["name"] != "NotionDocs" {
		t.Fatalf("provider name = %v, want 'NotionDocs'", got["name"])
	}
	if slug, _ := got["slug"].(string); strings.Contains(slug, "<script") {
		t.Fatalf("provider slug unsanitized: %q", slug)
	}
	warnings, _ := got["warnings"].([]interface{})
	if len(warnings) == 0 {
		t.Fatalf("expected WI-186 warnings on the sanitized response, got %v", got["warnings"])
	}
}

func TestCookieAuth_SCMProviderXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)

	resp := MakeAuthRequest(t, ts, http.MethodPost, "/admin/scm-providers",
		map[string]interface{}{
			"name":          "<script>alert(1)</script>GitHubCorp",
			"slug":          "gh-corp<script>x</script>",
			"provider_type": "github",
			"auth_method":   "pat",
			"enabled":       true,
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create scm provider: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	if name, _ := got["name"].(string); strings.Contains(name, "<script") {
		t.Fatalf("scm provider name unsanitized: %q", name)
	}
	if got["name"] != "GitHubCorp" {
		t.Fatalf("scm provider name = %v, want 'GitHubCorp'", got["name"])
	}
	if slug, _ := got["slug"].(string); strings.Contains(slug, "<script") {
		t.Fatalf("scm provider slug unsanitized: %q", slug)
	}
}

func TestCookieAuth_EmailProviderXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)

	resp := MakeAuthRequest(t, ts, http.MethodPost, "/admin/email-providers",
		map[string]interface{}{
			"name":       "<script>alert(1)</script>GenericIMAP",
			"slug":       "imap-1<script>x</script>",
			"type":       "generic",
			"is_enabled": true,
			"imap_host":  "mail.example.com",
			"imap_port":  993,
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create email provider: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	// CreateEmailProvider responds with {id, slug, warnings?} — name not
	// echoed. Sanitize hits at decode, slug is round-tripped clean.
	if slug, _ := got["slug"].(string); strings.Contains(slug, "<script") {
		t.Fatalf("email provider slug unsanitized: %q", slug)
	}
	warnings, _ := got["warnings"].([]interface{})
	if len(warnings) == 0 {
		t.Fatalf("expected WI-186 warnings on the sanitized response, got %v", got["warnings"])
	}
}
