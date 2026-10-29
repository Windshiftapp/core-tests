package tests

import (
	"net/http"
	"testing"
)

func scimCreateGroup(t *testing.T, server *TestServer, scimToken, name, externalID string) string {
	t.Helper()
	body := map[string]interface{}{
		"schemas":     []string{"urn:ietf:params:scim:schemas:core:2.0:Group"},
		"displayName": name,
		"externalId":  externalID,
	}
	resp := MakeSCIMRequest(t, server, scimToken, http.MethodPost, "/scim/v2/Groups", body)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)

	var created map[string]interface{}
	DecodeJSON(t, resp, &created)
	id, ok := created["id"].(string)
	if !ok || id == "" {
		t.Fatalf("created group missing id: %#v", created)
	}
	return id
}

func scimGetGroup(t *testing.T, server *TestServer, scimToken, id string) map[string]interface{} {
	t.Helper()
	resp := MakeSCIMRequest(t, server, scimToken, http.MethodGet, "/scim/v2/Groups/"+id, nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	return got
}

func scimGroupMemberIDs(t *testing.T, group map[string]interface{}) map[string]bool {
	t.Helper()
	ids := map[string]bool{}
	members, ok := group["members"].([]interface{})
	if !ok {
		return ids
	}
	for _, m := range members {
		mm, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		if v, ok := mm["value"].(string); ok {
			ids[v] = true
		}
	}
	return ids
}

// TestSCIM_PatchGroupAtomicity verifies that a failing operation leaves the
// group and its memberships untouched (RFC 7644 §3.5.2 all-or-none). The
// externalId collision fails the transaction after the member add and
// displayName change have been applied inside it.
func TestSCIM_PatchGroupAtomicity(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Group Atomicity Token")

	// Occupy the externalId that the victim's patch will collide with.
	scimCreateGroup(t, server, scimToken, "Collider Group", "ext-collider")
	victimID := scimCreateGroup(t, server, scimToken, "Victim Group", "ext-victim")
	memberID := scimCreateUser(t, server, scimToken, "group.atomic.member@example.com", "Atomic", "Member")

	patch := map[string]interface{}{
		"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
		"Operations": []map[string]interface{}{
			{"op": "replace", "path": "displayName", "value": "Renamed Victim"},
			{"op": "add", "path": "members", "value": []map[string]interface{}{{"value": memberID}}},
			{"op": "replace", "path": "externalId", "value": "ext-collider"},
		},
	}
	resp := MakeSCIMRequest(t, server, scimToken, http.MethodPatch, "/scim/v2/Groups/"+victimID, patch)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusInternalServerError)

	got := scimGetGroup(t, server, scimToken, victimID)
	if got["displayName"] != "Victim Group" {
		t.Fatalf("displayName survived a failed patch: %#v", got["displayName"])
	}
	if got["externalId"] != "ext-victim" {
		t.Fatalf("externalId changed on a failed patch: %#v", got["externalId"])
	}
	if ids := scimGroupMemberIDs(t, got); ids[memberID] {
		t.Fatalf("member add survived a failed patch: %#v", got["members"])
	}
}

// TestSCIM_PatchGroupReplaceMembersRewritesSet verifies RFC 7644 §3.5.2.3
// replace semantics: the member list is rewritten, not appended to.
func TestSCIM_PatchGroupReplaceMembersRewritesSet(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Group Replace Token")

	groupID := scimCreateGroup(t, server, scimToken, "Replace Members Group", "ext-replace")
	u1 := scimCreateUser(t, server, scimToken, "replace.u1@example.com", "R1", "User")
	u2 := scimCreateUser(t, server, scimToken, "replace.u2@example.com", "R2", "User")
	u3 := scimCreateUser(t, server, scimToken, "replace.u3@example.com", "R3", "User")

	seed := map[string]interface{}{
		"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
		"Operations": []map[string]interface{}{
			{"op": "add", "path": "members", "value": []map[string]interface{}{
				{"value": u1}, {"value": u2},
			}},
		},
	}
	resp := MakeSCIMRequest(t, server, scimToken, http.MethodPatch, "/scim/v2/Groups/"+groupID, seed)
	resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	patch := map[string]interface{}{
		"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
		"Operations": []map[string]interface{}{
			{"op": "replace", "path": "members", "value": []map[string]interface{}{
				{"value": u2}, {"value": u3},
			}},
		},
	}
	replaceResp := MakeSCIMRequest(t, server, scimToken, http.MethodPatch, "/scim/v2/Groups/"+groupID, patch)
	defer replaceResp.Body.Close()
	AssertStatusCode(t, replaceResp, http.StatusOK)

	got := scimGetGroup(t, server, scimToken, groupID)
	ids := scimGroupMemberIDs(t, got)
	if len(ids) != 2 || !ids[u2] || !ids[u3] {
		t.Fatalf("members = %#v want {%s, %s}", ids, u2, u3)
	}
	if ids[u1] {
		t.Fatalf("replaced member %s survived: %#v", u1, got["members"])
	}
}
