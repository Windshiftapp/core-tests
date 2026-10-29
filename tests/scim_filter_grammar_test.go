package tests

import (
	"net/http"
	"net/url"
	"testing"
)

func scimCountUsers(t *testing.T, server *TestServer, scimToken, filter string) int {
	t.Helper()
	endpoint := "/scim/v2/Users?filter=" + url.QueryEscape(filter)
	resp := MakeSCIMRequest(t, server, scimToken, http.MethodGet, endpoint, nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var result map[string]interface{}
	DecodeJSON(t, resp, &result)
	total, _ := result["totalResults"].(float64)
	return int(total)
}

// TestSCIM_ListUsersFilterOr exercises the "or" operator end-to-end.
func TestSCIM_ListUsersFilterOr(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Filter Or Token")

	scimCreateUser(t, server, scimToken, "or.one@example.com", "Or", "One")
	scimCreateUser(t, server, scimToken, "or.two@example.com", "Or", "Two")
	scimCreateUser(t, server, scimToken, "or.three@example.com", "Or", "Three")

	filter := `userName eq "or.one@example.com" or userName eq "or.two@example.com"`
	if got := scimCountUsers(t, server, scimToken, filter); got != 2 {
		t.Fatalf("totalResults = %d want 2", got)
	}
}

// TestSCIM_ListUsersFilterNot exercises "not" with grouping end-to-end.
func TestSCIM_ListUsersFilterNot(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Filter Not Token")

	scimCreateUser(t, server, scimToken, "not.one@example.com", "Not", "One")
	scimCreateUser(t, server, scimToken, "not.two@example.com", "Not", "Two")
	scimCreateUser(t, server, scimToken, "not.three@example.com", "Not", "Three")

	filter := `not (userName eq "not.one@example.com")`
	if got := scimCountUsers(t, server, scimToken, filter); got != 2 {
		t.Fatalf("totalResults = %d want 2", got)
	}
}

// TestSCIM_ListUsersFilterPresent exercises the "pr" operator end-to-end.
func TestSCIM_ListUsersFilterPresent(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Filter Pr Token")

	scimCreateUser(t, server, scimToken, "pr.one@example.com", "Pr", "One")
	scimCreateUser(t, server, scimToken, "pr.two@example.com", "Pr", "Two")

	if got := scimCountUsers(t, server, scimToken, `userName pr`); got != 2 {
		t.Fatalf("totalResults = %d want 2", got)
	}
}

// TestSCIM_ListUsersFilterValuePath exercises the Okta-style multi-valued
// selector end-to-end.
func TestSCIM_ListUsersFilterValuePath(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Filter ValuePath Token")

	scimCreateUser(t, server, scimToken, "vp.one@example.com", "Vp", "One")
	scimCreateUser(t, server, scimToken, "vp.two@example.com", "Vp", "Two")

	filter := `emails[type eq "work"].value eq "vp.one@example.com"`
	if got := scimCountUsers(t, server, scimToken, filter); got != 1 {
		t.Fatalf("totalResults = %d want 1", got)
	}
}

// TestSCIM_ListUsersFilterMalformedReturns400 verifies a syntax error maps to
// the SCIM invalidFilter error.
func TestSCIM_ListUsersFilterMalformedReturns400(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	scimToken := CreateSCIMToken(t, server, "Filter Malformed Token")

	endpoint := "/scim/v2/Users?filter=" + url.QueryEscape(`userName eq "x" and`)
	resp := MakeSCIMRequest(t, server, scimToken, http.MethodGet, endpoint, nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusBadRequest)

	var result map[string]interface{}
	DecodeJSON(t, resp, &result)
	if result["scimType"] != "invalidFilter" {
		t.Fatalf("scimType = %#v want invalidFilter", result["scimType"])
	}
}
