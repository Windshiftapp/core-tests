package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestIterationProgressBatch exercises GET /api/iterations/progress?ids=... —
// the bulk progress fetch that backs the dashboard iteration-timeline widget,
// replacing one GET /iterations/{id}/progress per iteration. It verifies the
// requested iteration is present (keyed by id), a non-existent id is omitted,
// and the batched report matches the per-iteration endpoint.
func TestIterationProgressBatch(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Iter Progress WS", shortKey("IPWS"))

	iterationData := map[string]interface{}{
		"workspace_id": wsID,
		"name":         "Sprint Batch",
		"description":  "batch progress test",
		"start_date":   "2024-01-01",
		"end_date":     "2024-01-31",
		"status":       "active",
		"is_global":    false,
	}
	iterResp := MakeAuthRequest(t, server, http.MethodPost, "/iterations", iterationData)
	AssertStatusCode(t, iterResp, http.StatusCreated)
	var iter map[string]interface{}
	DecodeJSON(t, iterResp, &iter)
	iterResp.Body.Close()
	iterationID := ExtractIDFromResponse(t, iter)
	const missing = 999999999

	resp := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/iterations/progress?ids=%d,%d", iterationID, missing), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var batch map[string]map[string]interface{}
	DecodeJSON(t, resp, &batch)

	key := fmt.Sprintf("%d", iterationID)
	if _, ok := batch[key]; !ok {
		t.Fatalf("expected iteration %d in batch progress, got keys %v", iterationID, keysOfAnyMap(batch))
	}
	if _, ok := batch[fmt.Sprintf("%d", missing)]; ok {
		t.Fatalf("non-existent iteration %d should be omitted", missing)
	}

	// Cross-check a stable field against the per-iteration endpoint.
	perResp := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/iterations/%d/progress", iterationID), nil)
	defer perResp.Body.Close()
	AssertStatusCode(t, perResp, http.StatusOK)
	var per map[string]interface{}
	DecodeJSON(t, perResp, &per)

	if intField(batch[key], "total_items") != intField(per, "total_items") {
		t.Fatalf("batch total_items %v != per-iteration total_items %v",
			batch[key]["total_items"], per["total_items"])
	}
}

// TestIterationProgressBatch_NoIDs verifies an empty ids list returns an empty
// object, not an error.
func TestIterationProgressBatch_NoIDs(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	resp := MakeAuthRequest(t, server, http.MethodGet, "/iterations/progress?ids=", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var batch map[string]interface{}
	DecodeJSON(t, resp, &batch)
	if len(batch) != 0 {
		t.Fatalf("want empty object, got %v", batch)
	}
}

func keysOfAnyMap(m map[string]map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
