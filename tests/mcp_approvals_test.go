// mcp_approvals_test covers get_item_approvals. The tool is read-only and
// returns an empty `requests` array when an item has no approval history,
// so it can be exercised against any seeded item without a separate
// approvals fixture.
package tests

import "testing"

func TestMCP_GetItemApprovals_NoHistory(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)
	session := dialMCP(t, ts)

	target := w.Items[0]

	var out struct {
		Requests []map[string]interface{} `json:"requests"`
	}
	callTool(t, session, "get_item_approvals", map[string]interface{}{"item_id": target.ID}, &out)
	if out.Requests == nil {
		// JSON-decode of `null` would also produce nil; the contract is an
		// (empty) array so callers don't have to special-case nil.
		t.Fatalf("get_item_approvals: requests is nil; expected empty array")
	}
	if len(out.Requests) != 0 {
		t.Fatalf("expected 0 approval requests on fresh item, got %d", len(out.Requests))
	}
}
