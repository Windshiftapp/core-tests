//go:build test

package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// WI-1678: a partial request-type update must preserve fields the caller did
// not send (title template, display_order, is_active), while explicit edits
// still apply.
func TestUpdateRequestTypePreservesOmittedFields(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "RT Preserve WS", shortKey("RTPW"))
	_, channelID := SetupPortalChannel(t, server, wsID)

	listResp := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/channels/%d/request-types", channelID), nil)
	AssertStatusCode(t, listResp, http.StatusOK)
	var requestTypes []map[string]interface{}
	DecodeJSON(t, listResp, &requestTypes)
	listResp.Body.Close()

	var seeded map[string]interface{}
	for _, candidate := range requestTypes {
		if candidate["name"] == "General Request" {
			seeded = candidate
			break
		}
	}
	if seeded == nil {
		t.Fatalf("seeded General Request not found in %v", requestTypes)
	}
	requestTypeID := intField(seeded, "id")
	path := fmt.Sprintf("/channels/%d/request-types/%d", channelID, requestTypeID)

	// Seed a nonempty title template, explicit ordering, and an inactive form.
	resp := MakeAuthRequest(t, server, http.MethodPut, path, map[string]interface{}{
		"title_template": "Ticket {{id}}",
		"display_order":  7,
		"is_active":      false,
	})
	AssertStatusCode(t, resp, http.StatusOK)
	resp.Body.Close()

	// A rename that omits the other fields must not clear them.
	resp = MakeAuthRequest(t, server, http.MethodPut, path, map[string]interface{}{
		"name": "Renamed Request",
	})
	AssertStatusCode(t, resp, http.StatusOK)
	var updated map[string]interface{}
	DecodeJSON(t, resp, &updated)
	resp.Body.Close()

	if got := updated["title_template"]; got != "Ticket {{id}}" {
		t.Fatalf("title_template after partial update = %v, want preserved", got)
	}
	if got := intField(updated, "display_order"); got != 7 {
		t.Fatalf("display_order after partial update = %d, want 7", got)
	}
	if active, _ := updated["is_active"].(bool); active {
		t.Fatal("is_active after partial update = true, want preserved false")
	}

	// Explicit edits still apply, including clearing the template and ordering.
	resp = MakeAuthRequest(t, server, http.MethodPut, path, map[string]interface{}{
		"title_template": "",
		"display_order":  0,
	})
	AssertStatusCode(t, resp, http.StatusOK)
	DecodeJSON(t, resp, &updated)
	resp.Body.Close()

	if got := updated["title_template"]; got != "" {
		t.Fatalf("title_template after explicit clear = %v, want empty", got)
	}
	if got := intField(updated, "display_order"); got != 0 {
		t.Fatalf("display_order after explicit clear = %d, want 0", got)
	}
}
