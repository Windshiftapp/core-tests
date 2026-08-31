package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// bearerRoundTripper injects Authorization: Bearer onto every request the
// MCP SDK's StreamableClientTransport makes. The SDK doesn't have a header
// hook on the transport, but it does honor a custom http.Client.
type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (b *bearerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	base := b.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(r)
}

// dialMCP opens a session against ts.BaseURL/mcp authenticated with the
// admin bearer token. The cleanup runs on test exit.
func dialMCP(t *testing.T, ts *TestServer) *mcp.ClientSession {
	t.Helper()
	httpClient := &http.Client{
		Transport: &bearerRoundTripper{token: ts.BearerToken},
	}
	transport := &mcp.StreamableClientTransport{
		Endpoint:   ts.BaseURL + "/mcp",
		HTTPClient: httpClient,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "windshift-tests", Version: "1.0"}, nil)
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("MCP connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// callTool invokes the named MCP tool with args and returns the parsed JSON
// payload from the first text content block. Tools that return JSON-encoded
// bodies (every Windshift tool does) get decoded into out.
func callTool(t *testing.T, session *mcp.ClientSession, name string, args any, out any) {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("CallTool %s returned IsError=true: %s", name, joinTextContent(res.Content))
	}
	if out == nil {
		return
	}
	if len(res.Content) == 0 {
		t.Fatalf("CallTool %s: no content blocks", name)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("CallTool %s: first content block is %T, want *mcp.TextContent", name, res.Content[0])
	}
	if err := json.Unmarshal([]byte(tc.Text), out); err != nil {
		t.Fatalf("CallTool %s: unmarshal: %v\nraw=%s", name, err, tc.Text)
	}
}

func joinTextContent(content []mcp.Content) string {
	var b []byte
	for i, c := range content {
		if i > 0 {
			b = append(b, '\n')
		}
		if tc, ok := c.(*mcp.TextContent); ok {
			b = append(b, tc.Text...)
		}
	}
	return string(b)
}

// callToolRaw is callTool without unmarshaling — returns the raw text payload
// of the first content block. Useful when the response shape is dynamic.
func callToolRaw(t *testing.T, session *mcp.ClientSession, name string, args any) string {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("CallTool %s returned IsError=true content=%v", name, res.Content)
	}
	if len(res.Content) == 0 {
		return ""
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("CallTool %s: first content block is %T, want *mcp.TextContent", name, res.Content[0])
	}
	return tc.Text
}
