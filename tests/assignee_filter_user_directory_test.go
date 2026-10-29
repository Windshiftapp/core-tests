package tests

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
)

type qlUserValue struct {
	Value any    `json:"value"`
	Label string `json:"label"`
}

func qlUserValueIDs(values []qlUserValue) []int {
	ids := make([]int, 0, len(values))
	for _, value := range values {
		if id, ok := value.Value.(float64); ok {
			ids = append(ids, int(id))
		}
	}
	slices.Sort(ids)
	return ids
}

func qlUserValueIDSet(values []qlUserValue) map[int]bool {
	set := make(map[int]bool, len(values))
	for _, value := range values {
		if id, ok := value.Value.(float64); ok {
			set[int(id)] = true
		}
	}
	return set
}

// GH #299: a workspace member without user.list must still get user options
// from the query-language value help, and the roster must be exactly the
// assignable-users roster the item assignee picker uses.
func TestQueryLanguageUsersValueHelpMatchesAssignableUsersForNonAdmin(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Assignee filter roster", shortKey("AFR"))

	memberID, memberName, memberPassword := CreateTestUserWithCredentials(t, server, "afr_member", "afr_member@test.com")
	teammateID, _, _ := CreateTestUserWithCredentials(t, server, "afr_teammate", "afr_teammate@test.com")
	// No explicit role: the workspace's default everyone grant decides whether
	// this user is on the roster, and both surfaces must agree either way.
	CreateTestUserWithCredentials(t, server, "afr_outsider", "afr_outsider@test.com")
	AssignWorkspaceRole(t, server, memberID, workspaceID, "Editor")
	AssignWorkspaceRole(t, server, teammateID, workspaceID, "Editor")

	sessionCookie, bearerToken := CreateAuthCredentialsForUser(t, server, memberName, memberPassword)

	filterResp := MakeAuthRequestWithToken(t, server, sessionCookie, http.MethodGet,
		"/v2/query-language/values?source=users&value_field=id", nil)
	defer filterResp.Body.Close()
	AssertStatusCode(t, filterResp, http.StatusOK)
	var filterBody struct {
		Data []qlUserValue `json:"data"`
	}
	DecodeJSON(t, filterResp, &filterBody)

	assignableResp := MakeAuthRequestWithToken(t, server, sessionCookie, http.MethodGet,
		fmt.Sprintf("/v2/workspaces/%d/assignable-users", workspaceID), nil)
	defer assignableResp.Body.Close()
	AssertStatusCode(t, assignableResp, http.StatusOK)
	var assignable []surfaceDirectoryUser
	DecodeJSON(t, assignableResp, &assignable)
	assignableIDs := make([]int, 0, len(assignable))
	for _, user := range assignable {
		assignableIDs = append(assignableIDs, user.ID)
	}
	slices.Sort(assignableIDs)

	if len(filterBody.Data) == 0 {
		t.Fatal("query-language user values were empty for a workspace member")
	}
	for _, value := range filterBody.Data {
		if value.Label == "" {
			t.Fatalf("user value %#v has an empty label", value)
		}
	}

	filterIDs := qlUserValueIDs(filterBody.Data)
	if !slices.Equal(filterIDs, assignableIDs) {
		t.Fatalf("query-language user ids = %v, want assignable-user ids %v", filterIDs, assignableIDs)
	}
	roster := qlUserValueIDSet(filterBody.Data)
	if !roster[memberID] || !roster[teammateID] {
		t.Fatalf("workspace members missing from user values: ids=%v member=%d teammate=%d", filterIDs, memberID, teammateID)
	}

	bearerResp := MakeBearerRequestWithToken(t, server, bearerToken, http.MethodGet,
		"/rest/api/v2/query-language/values?source=users&value_field=id", nil)
	defer bearerResp.Body.Close()
	AssertStatusCode(t, bearerResp, http.StatusOK)
}

func TestQueryLanguageUsersValueHelpIncludesWorkspaceRosterForAdmin(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Assignee filter admin", shortKey("AFA"))

	memberID, _, _ := CreateTestUserWithCredentials(t, server, "afa_member", "afa_member@test.com")
	AssignWorkspaceRole(t, server, memberID, workspaceID, "Editor")

	resp := MakeAuthRequest(t, server, http.MethodGet,
		"/v2/query-language/values?source=users&value_field=id", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var body struct {
		Data []qlUserValue `json:"data"`
	}
	DecodeJSON(t, resp, &body)

	roster := qlUserValueIDSet(body.Data)
	if !roster[memberID] {
		t.Fatalf("workspace member %d missing from admin user values: ids=%v", memberID, qlUserValueIDs(body.Data))
	}
}
