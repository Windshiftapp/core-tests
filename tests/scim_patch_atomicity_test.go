package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// WI-1309: SCIM PATCH is all-or-none (RFC 7644 §3.5.2). When any operation in
// the set fails, the resource must be left exactly as it was — no partial
// application of earlier operations.
func TestSCIM_PatchFailureAppliesNothing(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Patch Atomicity Token")

	victimID := createSCIMUser(t, server, scimToken, "scim.victim@example.com")
	// A second user whose userName the victim's PATCH will collide with.
	createSCIMUser(t, server, scimToken, "scim.collider@example.com")

	patchData := map[string]interface{}{
		"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
		"Operations": []map[string]interface{}{
			// Op 1 would succeed on its own…
			{"op": "replace", "path": "name.givenName", "value": "Partial"},
			// …op 2 violates the username uniqueness constraint.
			{"op": "replace", "path": "userName", "value": "scim.collider@example.com"},
		},
	}

	patch := MakeSCIMRequest(t, server, scimToken, http.MethodPatch, fmt.Sprintf("/scim/v2/Users/%s", victimID), patchData)
	defer patch.Body.Close()
	if patch.StatusCode < 400 {
		t.Fatalf("expected the failing patch to be rejected, got %d", patch.StatusCode)
	}

	// The resource must be untouched: neither op may survive.
	get := MakeSCIMRequest(t, server, scimToken, http.MethodGet, fmt.Sprintf("/scim/v2/Users/%s", victimID), nil)
	defer get.Body.Close()
	AssertStatusCode(t, get, http.StatusOK)

	var user struct {
		UserName string `json:"userName"`
		Name     struct {
			GivenName string `json:"givenName"`
		} `json:"name"`
	}
	DecodeJSON(t, get, &user)
	if user.UserName != "scim.victim@example.com" {
		t.Fatalf("userName mutated by a failed patch: %+v", user)
	}
	if user.Name.GivenName == "Partial" {
		t.Fatalf("partial patch state survived: op 1 (givenName) was written before op 2 failed: %+v", user)
	}
}
