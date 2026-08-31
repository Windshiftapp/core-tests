// mcp_auth_test verifies the bearer-token gating on /mcp implemented in
// internal/mcp/auth.go: no token → 401, token without mcp:access scope →
// 403, valid token → 200. We hit the endpoint with a hand-rolled HTTP POST
// (a minimal MCP initialize envelope) rather than the SDK so we can
// observe the raw status codes the middleware returns.
package tests

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

const initEnvelope = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"auth-probe","version":"1.0"}}}`

func TestMCP_Auth_NoToken(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)

	req, _ := http.NewRequest(http.MethodPost, ts.BaseURL+"/mcp", bytes.NewReader([]byte(initEnvelope)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := testHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("post /mcp: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-token POST /mcp: status=%d want 401", resp.StatusCode)
	}
}

func TestMCP_Auth_TokenWithoutScope(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)

	// Mint a fresh token with a single scope that does NOT include mcp:access.
	// "items:read" alone is enough to authenticate against /rest/api/v1/* but
	// not /mcp.
	body := map[string]interface{}{
		"name":        "scoped-down",
		"permissions": []string{"items:read"},
	}
	resp := MakeAuthRequest(t, ts, http.MethodPost, "/api-tokens", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("api-tokens create: %d", resp.StatusCode)
	}
	var minted struct {
		Token string `json:"token"`
	}
	DecodeJSON(t, resp, &minted)
	if minted.Token == "" {
		t.Fatalf("no token returned")
	}

	req, _ := http.NewRequest(http.MethodPost, ts.BaseURL+"/mcp", bytes.NewReader([]byte(initEnvelope)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+minted.Token)
	req.Header.Set("Accept", "application/json, text/event-stream")
	scopedResp, err := testHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("post /mcp: %v", err)
	}
	defer scopedResp.Body.Close()
	if scopedResp.StatusCode != http.StatusForbidden {
		t.Fatalf("scoped-down POST /mcp: status=%d want 403", scopedResp.StatusCode)
	}
	// Body should mention the missing scope.
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(scopedResp.Body)
	if !strings.Contains(buf.String(), "mcp:access") {
		t.Fatalf("scoped-down body missing 'mcp:access' hint: %s", buf.String())
	}
}

func TestMCP_Auth_ValidToken(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)

	// dialMCP uses ts.BearerToken (admin scope, expands to mcp:access).
	// If this returns without fatal-ing, auth is good.
	_ = dialMCP(t, ts)
}
