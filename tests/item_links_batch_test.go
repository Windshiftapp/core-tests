package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestItemLinksBatchEndpoint exercises GET /api/links/batch — the batched
// links fetch that backs the board/roadmap dependency badges, replacing the
// per-card GET /items/{id}/links burst that could exhaust the DB pool. It
// verifies grouping by item id, that incoming/outgoing are split correctly,
// that duplicate ids are tolerated, and that every requested id appears in the
// response (so the client can cache misses without re-fetching).
func TestItemLinksBatchEndpoint(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server) // completes setup + admin login; sets SessionCookie

	wsID, _ := CreateTestWorkspace(t, server, "Links Batch WS", shortKey("LBWS"))
	itemA := CreateTestItem(t, server, wsID, "Item A")
	itemB := CreateTestItem(t, server, wsID, "Item B")
	itemC := CreateTestItem(t, server, wsID, "Item C")

	// Link A -> B (link_type_id 2 = "Implements", which permits item<->item).
	linkBody := map[string]interface{}{
		"link_type_id": 2,
		"source_type":  "item",
		"source_id":    itemA,
		"target_type":  "item",
		"target_id":    itemB,
	}
	createResp := MakeAuthRequest(t, server, http.MethodPost, "/links", linkBody)
	AssertStatusCode(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	type entityLinks struct {
		Outgoing []map[string]interface{} `json:"outgoing"`
		Incoming []map[string]interface{} `json:"incoming"`
	}

	// Duplicate ids included intentionally — the endpoint must dedupe.
	endpoint := fmt.Sprintf("/links/batch?ids=%d,%d,%d,%d", itemA, itemB, itemC, itemA)
	resp := MakeAuthRequest(t, server, http.MethodGet, endpoint, nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var got map[string]entityLinks
	DecodeJSON(t, resp, &got)

	// Every requested id present, even the one with no links (caching contract).
	for _, id := range []int{itemA, itemB, itemC} {
		if _, ok := got[fmt.Sprintf("%d", id)]; !ok {
			t.Fatalf("expected item %d in batch response, got keys %v", id, got)
		}
	}

	a := got[fmt.Sprintf("%d", itemA)]
	if len(a.Outgoing) != 1 {
		t.Fatalf("item A outgoing: want 1, got %d", len(a.Outgoing))
	}
	if tid := intField(a.Outgoing[0], "target_id"); tid != itemB {
		t.Fatalf("item A outgoing target_id: want %d, got %d", itemB, tid)
	}
	if len(a.Incoming) != 0 {
		t.Fatalf("item A incoming: want 0, got %d", len(a.Incoming))
	}

	b := got[fmt.Sprintf("%d", itemB)]
	if len(b.Incoming) != 1 {
		t.Fatalf("item B incoming: want 1, got %d", len(b.Incoming))
	}
	if sid := intField(b.Incoming[0], "source_id"); sid != itemA {
		t.Fatalf("item B incoming source_id: want %d, got %d", itemA, sid)
	}

	c := got[fmt.Sprintf("%d", itemC)]
	if len(c.Outgoing) != 0 || len(c.Incoming) != 0 {
		t.Fatalf("item C should have no links, got outgoing=%d incoming=%d", len(c.Outgoing), len(c.Incoming))
	}
}

// TestItemLinksBatchEndpoint_NoIDs verifies the endpoint returns an empty
// object (not an error) when no usable ids are supplied.
func TestItemLinksBatchEndpoint_NoIDs(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server) // completes setup + admin login; sets SessionCookie

	resp := MakeAuthRequest(t, server, http.MethodGet, "/links/batch?ids=", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	if len(got) != 0 {
		t.Fatalf("want empty object, got %v", got)
	}
}

func intField(m map[string]interface{}, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}
