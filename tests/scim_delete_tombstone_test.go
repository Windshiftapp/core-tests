package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// WI-1310: RFC 7644 §3.6 compliance for SCIM DELETE. A deleted user becomes a
// hidden tombstone: 404 on every subsequent SCIM operation, omitted from list
// and filter queries, with SCIM-managed group memberships removed. PATCH
// active=false remains the supported deactivation path and keeps the resource
// visible.

func newSCIMUserPayload(userName string) map[string]interface{} {
	return map[string]interface{}{
		"schemas":    []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		"userName":   userName,
		"externalId": "ext-" + userName,
		"name": map[string]string{
			"givenName":  "Tomb",
			"familyName": "Stone",
		},
		"emails": []map[string]interface{}{
			{"value": userName, "primary": true},
		},
		"active": true,
	}
}

func createSCIMUser(t *testing.T, server *TestServer, scimToken, userName string) string {
	t.Helper()

	resp := MakeSCIMRequest(t, server, scimToken, http.MethodPost, "/scim/v2/Users", newSCIMUserPayload(userName))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body := readBody(t, resp)
		t.Fatalf("Failed to create SCIM user %s: %d - %s", userName, resp.StatusCode, body)
	}
	var result map[string]interface{}
	DecodeJSON(t, resp, &result)
	id, ok := result["id"].(string)
	if !ok || id == "" {
		t.Fatalf("SCIM user creation returned no id: %v", result)
	}
	return id
}

func scimUserListContains(t *testing.T, server *TestServer, scimToken, userID string, query string) bool {
	t.Helper()

	endpoint := "/scim/v2/Users"
	if query != "" {
		endpoint += "?" + query
	}
	resp := MakeSCIMRequest(t, server, scimToken, http.MethodGet, endpoint, nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var list struct {
		TotalResults int              `json:"totalResults"`
		Resources    []map[string]any `json:"Resources"`
	}
	DecodeJSON(t, resp, &list)
	for _, resource := range list.Resources {
		if resource["id"] == userID {
			return true
		}
	}
	return false
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	return string(responseBody(t, resp))
}

func TestSCIM_PatchDeactivateRemainsVisible(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Tombstone Deactivate Token")

	userID := createSCIMUser(t, server, scimToken, "scim.deactivate@example.com")

	patchData := map[string]interface{}{
		"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
		"Operations": []map[string]interface{}{
			{"op": "replace", "path": "active", "value": false},
		},
	}
	patch := MakeSCIMRequest(t, server, scimToken, http.MethodPatch, fmt.Sprintf("/scim/v2/Users/%s", userID), patchData)
	defer patch.Body.Close()
	AssertStatusCode(t, patch, http.StatusOK)

	get := MakeSCIMRequest(t, server, scimToken, http.MethodGet, fmt.Sprintf("/scim/v2/Users/%s", userID), nil)
	defer get.Body.Close()
	AssertStatusCode(t, get, http.StatusOK)
	var user map[string]interface{}
	DecodeJSON(t, get, &user)
	AssertJSONField(t, user, "active", false)

	if !scimUserListContains(t, server, scimToken, userID, "") {
		t.Fatal("deactivated user must remain visible in /Users; PATCH active=false is the deactivation path")
	}
}

func TestSCIM_DeleteTombstonesUser(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Tombstone Delete Token")

	userName := "scim.tombstone@example.com"
	userID := createSCIMUser(t, server, scimToken, userName)
	userPath := fmt.Sprintf("/scim/v2/Users/%s", userID)

	del := MakeSCIMRequest(t, server, scimToken, http.MethodDelete, userPath, nil)
	defer del.Body.Close()
	AssertStatusCode(t, del, http.StatusNoContent)

	// Every subsequent SCIM operation must report 404 (RFC 7644 §3.6).
	get := MakeSCIMRequest(t, server, scimToken, http.MethodGet, userPath, nil)
	defer get.Body.Close()
	AssertStatusCode(t, get, http.StatusNotFound)

	put := MakeSCIMRequest(t, server, scimToken, http.MethodPut, userPath, newSCIMUserPayload(userName))
	defer put.Body.Close()
	AssertStatusCode(t, put, http.StatusNotFound)

	patch := MakeSCIMRequest(t, server, scimToken, http.MethodPatch, userPath, map[string]interface{}{
		"schemas":    []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
		"Operations": []map[string]interface{}{{"op": "replace", "path": "active", "value": true}},
	})
	defer patch.Body.Close()
	AssertStatusCode(t, patch, http.StatusNotFound)

	deleteAgain := MakeSCIMRequest(t, server, scimToken, http.MethodDelete, userPath, nil)
	defer deleteAgain.Body.Close()
	AssertStatusCode(t, deleteAgain, http.StatusNotFound)

	// Omitted from the plain list and from filter queries.
	if scimUserListContains(t, server, scimToken, userID, "") {
		t.Fatal("deleted user must be omitted from /Users")
	}
	if scimUserListContains(t, server, scimToken, userID, fmt.Sprintf("filter=userName+eq+%%22%s%%22", userName)) {
		t.Fatal("deleted user must be omitted from filtered /Users queries")
	}

	// Re-provisioning the same email must conflict, not resurrect the account.
	reprovision := MakeSCIMRequest(t, server, scimToken, http.MethodPost, "/scim/v2/Users", newSCIMUserPayload(userName))
	defer reprovision.Body.Close()
	AssertStatusCode(t, reprovision, http.StatusConflict)
}

func TestSCIM_DeleteRemovesSCIMGroupMemberships(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Tombstone Group Token")

	userID := createSCIMUser(t, server, scimToken, "scim.groupmember@example.com")

	groupData := map[string]interface{}{
		"schemas":     []string{"urn:ietf:params:scim:schemas:core:2.0:Group"},
		"displayName": "Tombstone Group",
		"members": []map[string]interface{}{
			{"value": userID},
		},
	}
	groupResp := MakeSCIMRequest(t, server, scimToken, http.MethodPost, "/scim/v2/Groups", groupData)
	defer groupResp.Body.Close()
	AssertStatusCode(t, groupResp, http.StatusCreated)
	var group map[string]interface{}
	DecodeJSON(t, groupResp, &group)
	groupID, _ := group["id"].(string)
	if groupID == "" {
		t.Fatal("group creation returned no id")
	}

	getBefore := MakeSCIMRequest(t, server, scimToken, http.MethodGet, fmt.Sprintf("/scim/v2/Groups/%s", groupID), nil)
	defer getBefore.Body.Close()
	AssertStatusCode(t, getBefore, http.StatusOK)
	var groupBefore map[string]interface{}
	DecodeJSON(t, getBefore, &groupBefore)
	membersBefore, _ := groupBefore["members"].([]interface{})
	if len(membersBefore) != 1 {
		t.Fatalf("expected 1 group member before delete, got %v", groupBefore["members"])
	}

	del := MakeSCIMRequest(t, server, scimToken, http.MethodDelete, fmt.Sprintf("/scim/v2/Users/%s", userID), nil)
	defer del.Body.Close()
	AssertStatusCode(t, del, http.StatusNoContent)

	getAfter := MakeSCIMRequest(t, server, scimToken, http.MethodGet, fmt.Sprintf("/scim/v2/Groups/%s", groupID), nil)
	defer getAfter.Body.Close()
	AssertStatusCode(t, getAfter, http.StatusOK)
	var groupAfter map[string]interface{}
	DecodeJSON(t, getAfter, &groupAfter)
	if members, present := groupAfter["members"]; present && len(members.([]interface{})) > 0 {
		t.Fatalf("deleted user still exposed in group members: %v", groupAfter["members"])
	}

	// The tombstoned user can no longer be wired into new groups either.
	otherGroup := map[string]interface{}{
		"schemas":     []string{"urn:ietf:params:scim:schemas:core:2.0:Group"},
		"displayName": "Tombstone Group Two",
		"members": []map[string]interface{}{
			{"value": userID},
		},
	}
	second := MakeSCIMRequest(t, server, scimToken, http.MethodPost, "/scim/v2/Groups", otherGroup)
	defer second.Body.Close()
	AssertStatusCode(t, second, http.StatusCreated)
	var secondGroup map[string]interface{}
	DecodeJSON(t, second, &secondGroup)
	secondGroupID, _ := secondGroup["id"].(string)

	secondGet := MakeSCIMRequest(t, server, scimToken, http.MethodGet, fmt.Sprintf("/scim/v2/Groups/%s", secondGroupID), nil)
	defer secondGet.Body.Close()
	AssertStatusCode(t, secondGet, http.StatusOK)
	var secondAfter map[string]interface{}
	DecodeJSON(t, secondGet, &secondAfter)
	if members, present := secondAfter["members"]; present && len(members.([]interface{})) > 0 {
		t.Fatalf("tombstoned user must not be addable as group member: %v", secondAfter["members"])
	}
}
