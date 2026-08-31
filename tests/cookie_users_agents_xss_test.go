// cookie_users_agents_xss_test pins the WI-185 slice-10 sweep on the
// user + agent CRUD handlers. Sanitize at decode is silent on these
// endpoints — the existing responses bypass the WI-186 anonymous-
// struct mechanism because both users.go Create + Update already
// have established response shapes feeding the admin UI. The XSS
// contract is pinned here at the handler boundary.
package tests

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCookieAuth_UserCreateXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)

	timestamp := time.Now().UnixNano()
	resp := MakeAuthRequest(t, ts, http.MethodPost, "/users",
		map[string]interface{}{
			"email":      fmt.Sprintf("user-%d@example.com", timestamp),
			"username":   fmt.Sprintf("xss%d", timestamp%1000000),
			"first_name": "<script>alert(1)</script>Alice",
			"last_name":  "Smith<img src=x onerror=evil()>",
			"password":   "TempPass123!",
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create user: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	if fn, _ := got["first_name"].(string); strings.Contains(fn, "<script") || fn != "Alice" {
		t.Fatalf("user first_name unsanitized: %q", fn)
	}
	if ln, _ := got["last_name"].(string); strings.Contains(ln, "<img") || strings.Contains(ln, "onerror") || ln != "Smith" {
		t.Fatalf("user last_name unsanitized: %q", ln)
	}
}

func TestCookieAuth_AgentCreateXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)

	timestamp := time.Now().UnixNano()
	resp := MakeAuthRequest(t, ts, http.MethodPost, "/me/agents",
		map[string]interface{}{
			"username":   fmt.Sprintf("agt%d", timestamp%1000000),
			"first_name": "<script>alert(1)</script>BotAgent",
			"last_name":  "Worker<img src=x onerror=evil()>",
			"email":      fmt.Sprintf("agent-%d@example.com", timestamp),
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create agent: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	if fn, _ := got["first_name"].(string); strings.Contains(fn, "<script") || fn != "BotAgent" {
		t.Fatalf("agent first_name unsanitized: %q", fn)
	}
	if ln, _ := got["last_name"].(string); strings.Contains(ln, "<img") || strings.Contains(ln, "onerror") || ln != "Worker" {
		t.Fatalf("agent last_name unsanitized: %q", ln)
	}
}
