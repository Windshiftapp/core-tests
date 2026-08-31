package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestPortalRequestTypesFieldCount verifies GET /api/portal/{slug}/request-types
// returns field_count inline, so the portal UI no longer fans out one
// GET /request-types/{id}/fields per request type just to show counts. It
// checks the count is 0 with no fields and reflects the actual number after
// fields are attached.
func TestPortalRequestTypesFieldCount(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Portal FC WS", shortKey("PFCW"))
	slug, channelID := SetupPortalChannel(t, server, wsID)

	// Fetch request types — expect the seeded one with field_count 0.
	rts := getPortalRequestTypes(t, server, slug)
	if len(rts) == 0 {
		t.Fatalf("expected at least one request type")
	}
	rt := rts[0]
	if _, present := rt["field_count"]; !present {
		t.Fatalf("response missing field_count key: %v", rt)
	}
	if fc := intField(rt, "field_count"); fc != 0 {
		t.Fatalf("expected field_count 0 for a fieldless request type, got %d", fc)
	}
	rtID := intField(rt, "id")

	// Attach two fields.
	fields := []map[string]interface{}{
		{"field_identifier": "title", "field_type": "default", "display_order": 0, "is_required": true},
		{"field_identifier": "description", "field_type": "default", "display_order": 1, "is_required": false},
	}
	updateResp := MakeAuthRequest(t, server, http.MethodPut,
		fmt.Sprintf("/channels/%d/request-types/%d/fields", channelID, rtID), fields)
	AssertStatusCode(t, updateResp, http.StatusOK)
	updateResp.Body.Close()

	// field_count should now reflect the two fields.
	rts = getPortalRequestTypes(t, server, slug)
	var updated map[string]interface{}
	for _, candidate := range rts {
		if intField(candidate, "id") == rtID {
			updated = candidate
			break
		}
	}
	if updated == nil {
		t.Fatalf("request type %d not found after field update", rtID)
	}
	if fc := intField(updated, "field_count"); fc != 2 {
		t.Fatalf("expected field_count 2 after attaching 2 fields, got %d", fc)
	}
}

func TestManagedRequestTypesFieldCount(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Managed FC WS", shortKey("MFCW"))
	_, channelID := SetupPortalChannel(t, server, wsID)

	resp := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/channels/%d/request-types", channelID), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var requestTypes []map[string]interface{}
	DecodeJSON(t, resp, &requestTypes)
	if len(requestTypes) == 0 {
		t.Fatal("expected at least one managed request type")
	}
	rtID := intField(requestTypes[0], "id")

	fields := []map[string]interface{}{
		{"field_identifier": "title", "field_type": "default", "display_order": 0, "is_required": true},
		{"field_identifier": "description", "field_type": "default", "display_order": 1, "is_required": false},
	}
	updateResp := MakeAuthRequest(t, server, http.MethodPut,
		fmt.Sprintf("/channels/%d/request-types/%d/fields", channelID, rtID), fields)
	AssertStatusCode(t, updateResp, http.StatusOK)
	updateResp.Body.Close()

	resp = MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/channels/%d/request-types", channelID), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	requestTypes = nil
	DecodeJSON(t, resp, &requestTypes)
	if got := intField(requestTypes[0], "field_count"); got != 2 {
		t.Fatalf("managed field_count = %d, want 2", got)
	}
}

func getPortalRequestTypes(t *testing.T, server *TestServer, slug string) []map[string]interface{} {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/portal/%s/request-types", slug), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var rts []map[string]interface{}
	DecodeJSON(t, resp, &rts)
	return rts
}
