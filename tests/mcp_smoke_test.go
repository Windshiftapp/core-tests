// mcp_smoke_test verifies the MCP client wiring against /mcp before the
// per-tool rigorous suite is layered on top. A failure here means the
// transport / auth / tool registry need triage before any deeper assertion
// is meaningful.
package tests

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCP_Smoke_ListTools(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)

	session := dialMCP(t, ts)
	res, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) == 0 {
		t.Fatalf("MCP server returned 0 tools — registry not wired or ts.BearerToken lacks mcp:access scope")
	}
	t.Logf("MCP server exposes %d tools", len(res.Tools))

	// Spot check: list_workspaces must be present (it is the only tool
	// every Windshift install ships unconditionally) and list_diagrams
	// should be present too (it's the new untracked one).
	have := map[string]bool{}
	for _, tool := range res.Tools {
		have[tool.Name] = true
	}
	for _, must := range []string{"list_workspaces", "list_diagrams", "list_items", "list_milestones", "list_iterations"} {
		if !have[must] {
			t.Fatalf("expected tool %q in registry, got %v", must, mapKeys(have))
		}
	}
}

func mapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
