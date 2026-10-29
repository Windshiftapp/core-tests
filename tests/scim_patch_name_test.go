package tests

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// scimCreateUser creates a SCIM-managed user and returns its resource ID.
func scimCreateUser(t *testing.T, server *TestServer, scimToken, userName, given, family string) string {
	t.Helper()
	body := map[string]interface{}{
		"schemas":  []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		"userName": userName,
		"name":     map[string]string{"givenName": given, "familyName": family},
		"emails":   []map[string]interface{}{{"value": userName, "primary": true}},
		"active":   true,
	}
	resp := MakeSCIMRequest(t, server, scimToken, http.MethodPost, "/scim/v2/Users", body)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)

	var created map[string]interface{}
	DecodeJSON(t, resp, &created)
	id, ok := created["id"].(string)
	if !ok || id == "" {
		t.Fatalf("created user missing id: %#v", created)
	}
	return id
}

func scimGetUser(t *testing.T, server *TestServer, scimToken, id string) map[string]interface{} {
	t.Helper()
	resp := MakeSCIMRequest(t, server, scimToken, http.MethodGet, "/scim/v2/Users/"+id, nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	return got
}

func scimName(t *testing.T, user map[string]interface{}) map[string]interface{} {
	t.Helper()
	name, ok := user["name"].(map[string]interface{})
	if !ok {
		t.Fatalf("response missing name object: %#v", user)
	}
	return name
}

// TestSCIM_PatchPathlessNameObject covers the Okta-style PATCH body that omits
// "path" and nests the name object. It must update first and last name, not
// silently no-op (RFC 7644 §3.5.2.1/.3).
func TestSCIM_PatchPathlessNameObject(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Pathless Name Token")

	userID := scimCreateUser(t, server, scimToken, "okta.name@example.com", "Old", "Name")

	patch := map[string]interface{}{
		"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
		"Operations": []map[string]interface{}{
			{
				"op": "replace",
				"value": map[string]interface{}{
					"name": map[string]interface{}{"givenName": "Jane", "familyName": "Doe"},
				},
			},
		},
	}
	resp := MakeSCIMRequest(t, server, scimToken, http.MethodPatch, "/scim/v2/Users/"+userID, patch)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var patched map[string]interface{}
	DecodeJSON(t, resp, &patched)
	name := scimName(t, patched)
	if name["givenName"] != "Jane" || name["familyName"] != "Doe" {
		t.Fatalf("PATCH response name = %#v want Jane/Doe", name)
	}
	if patched["displayName"] != "Jane Doe" {
		t.Fatalf("displayName = %#v want Jane Doe", patched["displayName"])
	}

	// Re-fetch to prove the change persisted.
	got := scimGetUser(t, server, scimToken, userID)
	name = scimName(t, got)
	if name["givenName"] != "Jane" || name["familyName"] != "Doe" {
		t.Fatalf("persisted name = %#v want Jane/Doe", name)
	}
}

// TestSCIM_PatchCaseInsensitivePath verifies attribute paths are matched
// case-insensitively (RFC 7643 §2.1).
func TestSCIM_PatchCaseInsensitivePath(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Case Path Token")

	userID := scimCreateUser(t, server, scimToken, "case.path@example.com", "Old", "Name")

	patch := map[string]interface{}{
		"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
		"Operations": []map[string]interface{}{
			{"op": "Replace", "path": "Name.GivenName", "value": "Casey"},
		},
	}
	resp := MakeSCIMRequest(t, server, scimToken, http.MethodPatch, "/scim/v2/Users/"+userID, patch)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	got := scimGetUser(t, server, scimToken, userID)
	if name := scimName(t, got); name["givenName"] != "Casey" {
		t.Fatalf("givenName = %#v want Casey", name["givenName"])
	}
}

// TestSCIM_PatchDisplayNameDoesNotClobberFamily verifies a single-word
// displayName updates the given name but leaves an existing family name.
func TestSCIM_PatchDisplayNameDoesNotClobberFamily(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Display Name Token")

	userID := scimCreateUser(t, server, scimToken, "cher@example.com", "Cherilyn", "Doe")

	patch := map[string]interface{}{
		"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
		"Operations": []map[string]interface{}{
			{"op": "replace", "path": "displayName", "value": "Cher"},
		},
	}
	resp := MakeSCIMRequest(t, server, scimToken, http.MethodPatch, "/scim/v2/Users/"+userID, patch)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	got := scimGetUser(t, server, scimToken, userID)
	name := scimName(t, got)
	if name["givenName"] != "Cher" {
		t.Fatalf("givenName = %#v want Cher", name["givenName"])
	}
	if name["familyName"] != "Doe" {
		t.Fatalf("familyName = %#v want Doe (must not be clobbered)", name["familyName"])
	}
}

// TestSCIM_PatchRemoveGivenName verifies remove with a name sub-attribute path
// clears only that component (RFC 7644 §3.5.2.2).
func TestSCIM_PatchRemoveGivenName(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Remove Name Token")

	userID := scimCreateUser(t, server, scimToken, "remove.name@example.com", "Jane", "Doe")

	patch := map[string]interface{}{
		"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
		"Operations": []map[string]interface{}{
			{"op": "remove", "path": "name.givenName"},
		},
	}
	resp := MakeSCIMRequest(t, server, scimToken, http.MethodPatch, "/scim/v2/Users/"+userID, patch)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	got := scimGetUser(t, server, scimToken, userID)
	name := scimName(t, got)
	if v, ok := name["givenName"]; ok && v != "" {
		t.Fatalf("givenName = %#v want empty/absent", v)
	}
	if name["familyName"] != "Doe" {
		t.Fatalf("familyName = %#v want Doe", name["familyName"])
	}
}

// TestSCIM_PatchRemoveWithoutPathRejected verifies remove requires a path.
func TestSCIM_PatchRemoveWithoutPathRejected(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Remove No Path Token")

	userID := scimCreateUser(t, server, scimToken, "remove.nopath@example.com", "Jane", "Doe")

	patch := map[string]interface{}{
		"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
		"Operations": []map[string]interface{}{
			{"op": "remove", "value": "name.givenName"},
		},
	}
	resp := MakeSCIMRequest(t, server, scimToken, http.MethodPatch, "/scim/v2/Users/"+userID, patch)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusBadRequest)
}

// TestSCIM_CreateUserReturnsLocation verifies the Location header on create
// (RFC 7644 §3.1).
func TestSCIM_CreateUserReturnsLocation(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Location Token")

	body := map[string]interface{}{
		"schemas":  []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		"userName": "location.user@example.com",
		"name":     map[string]string{"givenName": "Loc", "familyName": "User"},
		"emails":   []map[string]interface{}{{"value": "location.user@example.com", "primary": true}},
		"active":   true,
	}
	resp := MakeSCIMRequest(t, server, scimToken, http.MethodPost, "/scim/v2/Users", body)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)

	var created map[string]interface{}
	DecodeJSON(t, resp, &created)
	userID := created["id"].(string)

	location := resp.Header.Get("Location")
	wantSuffix := "/scim/v2/Users/" + userID
	if !strings.HasSuffix(location, wantSuffix) {
		t.Fatalf("Location = %q want suffix %q", location, wantSuffix)
	}
}

// TestSCIM_CreateGroupReturnsLocation verifies the Location header on group
// create.
func TestSCIM_CreateGroupReturnsLocation(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Group Location Token")

	body := map[string]interface{}{
		"schemas":     []string{"urn:ietf:params:scim:schemas:core:2.0:Group"},
		"displayName": "Location Group",
	}
	resp := MakeSCIMRequest(t, server, scimToken, http.MethodPost, "/scim/v2/Groups", body)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)

	var created map[string]interface{}
	DecodeJSON(t, resp, &created)
	groupID := created["id"].(string)

	location := resp.Header.Get("Location")
	wantSuffix := "/scim/v2/Groups/" + groupID
	if !strings.HasSuffix(location, wantSuffix) {
		t.Fatalf("Location = %q want suffix %q", location, wantSuffix)
	}
}

// TestSCIM_PatchGroupPathlessDisplayName covers the path-less object form for
// groups.
func TestSCIM_PatchGroupPathlessDisplayName(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Group Pathless Token")

	create := map[string]interface{}{
		"schemas":     []string{"urn:ietf:params:scim:schemas:core:2.0:Group"},
		"displayName": "Original Group",
	}
	resp := MakeSCIMRequest(t, server, scimToken, http.MethodPost, "/scim/v2/Groups", create)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var created map[string]interface{}
	DecodeJSON(t, resp, &created)
	groupID := created["id"].(string)

	patch := map[string]interface{}{
		"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
		"Operations": []map[string]interface{}{
			{
				"op":    "replace",
				"value": map[string]interface{}{"displayName": "Renamed Group"},
			},
		},
	}
	patchResp := MakeSCIMRequest(t, server, scimToken, http.MethodPatch, "/scim/v2/Groups/"+groupID, patch)
	defer patchResp.Body.Close()
	AssertStatusCode(t, patchResp, http.StatusOK)

	getResp := MakeSCIMRequest(t, server, scimToken, http.MethodGet, "/scim/v2/Groups/"+groupID, nil)
	defer getResp.Body.Close()
	AssertStatusCode(t, getResp, http.StatusOK)
	var got map[string]interface{}
	DecodeJSON(t, getResp, &got)
	if got["displayName"] != "Renamed Group" {
		t.Fatalf("displayName = %#v want Renamed Group", got["displayName"])
	}
}

// TestSCIM_PatchEmails verifies a PATCH to the emails array updates the
// persisted email address (previously a silent no-op).
func TestSCIM_PatchEmails(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Email Patch Token")

	userID := scimCreateUser(t, server, scimToken, "old.email@example.com", "Email", "Patch")

	patch := map[string]interface{}{
		"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
		"Operations": []map[string]interface{}{
			{
				"op":   "replace",
				"path": "emails",
				"value": []map[string]interface{}{
					{"value": "new.email@example.com", "primary": true},
				},
			},
		},
	}
	resp := MakeSCIMRequest(t, server, scimToken, http.MethodPatch, "/scim/v2/Users/"+userID, patch)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	got := scimGetUser(t, server, scimToken, userID)
	emails, ok := got["emails"].([]interface{})
	if !ok || len(emails) == 0 {
		t.Fatalf("response missing emails: %#v", got)
	}
	first := emails[0].(map[string]interface{})
	if first["value"] != "new.email@example.com" {
		t.Fatalf("email = %#v want new.email@example.com", first["value"])
	}
}

// TestSCIM_PatchEmailConflict verifies an email already owned by another user
// is rejected with a SCIM uniqueness conflict.
func TestSCIM_PatchEmailConflict(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Email Conflict Token")

	scimCreateUser(t, server, scimToken, "owner.email@example.com", "Owner", "Email")
	victimID := scimCreateUser(t, server, scimToken, "victim.email@example.com", "Victim", "Email")

	patch := map[string]interface{}{
		"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
		"Operations": []map[string]interface{}{
			{"op": "replace", "path": "emails.value", "value": "owner.email@example.com"},
		},
	}
	resp := MakeSCIMRequest(t, server, scimToken, http.MethodPatch, "/scim/v2/Users/"+victimID, patch)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusConflict)

	// The victim's email must be unchanged.
	got := scimGetUser(t, server, scimToken, victimID)
	emails := got["emails"].([]interface{})
	if emails[0].(map[string]interface{})["value"] != "victim.email@example.com" {
		t.Fatalf("email mutated on conflict: %#v", got["emails"])
	}
}

// TestSCIM_ListUsersFilterCaseInsensitiveAttribute verifies filter attribute
// names are matched case-insensitively (RFC 7643 §2.1).
func TestSCIM_ListUsersFilterCaseInsensitiveAttribute(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Filter Case Token")

	scimCreateUser(t, server, scimToken, "filter.case@example.com", "Filter", "Case")

	filter := `UserName eq "filter.case@example.com"`
	endpoint := "/scim/v2/Users?filter=" + url.QueryEscape(filter)
	resp := MakeSCIMRequest(t, server, scimToken, http.MethodGet, endpoint, nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var result map[string]interface{}
	DecodeJSON(t, resp, &result)
	if total, _ := result["totalResults"].(float64); total != 1 {
		t.Fatalf("totalResults = %v want 1", result["totalResults"])
	}
}

// TestSCIM_ListUsersFilterNameSubAttributeCaseInsensitive covers the dotted
// name sub-attribute spelling.
func TestSCIM_ListUsersFilterNameSubAttributeCaseInsensitive(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Filter Sub Token")

	scimCreateUser(t, server, scimToken, "filter.sub@example.com", "Sub", "Attr")

	filter := `Name.FamilyName eq "Attr"`
	endpoint := "/scim/v2/Users?filter=" + url.QueryEscape(filter)
	resp := MakeSCIMRequest(t, server, scimToken, http.MethodGet, endpoint, nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var result map[string]interface{}
	DecodeJSON(t, resp, &result)
	if total, _ := result["totalResults"].(float64); total != 1 {
		t.Fatalf("totalResults = %v want 1", result["totalResults"])
	}
}
