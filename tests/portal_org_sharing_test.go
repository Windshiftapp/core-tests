package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestPortalOrganisationRequestSharingAPI exercises WI-1139 over HTTP: the
// customer.manage-only settings endpoint validates and persists the policy,
// the portal submit path honours the creator's share flag only in
// requester_choice mode, and a colleague in the same organisation sees shared
// requests but not private ones.
func TestPortalOrganisationRequestSharingAPI(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Org Share WS", shortKey("OSW"))
	portalSlug, portalChannelID := SetupPortalChannel(t, server, wsID)

	creatorID, creatorCookie := CreatePortalCustomerWithSession(t, server, portalChannelID, "Creator", "org-creator@example.test")
	colleagueID, colleagueCookie := CreatePortalCustomerWithSession(t, server, portalChannelID, "Colleague", "org-colleague@example.test")

	orgResp := MakeAuthRequest(t, server, http.MethodPost, "/customer-organisations", map[string]interface{}{"name": "Acme"})
	defer orgResp.Body.Close()
	AssertStatusCode(t, orgResp, http.StatusCreated)
	var org map[string]interface{}
	DecodeJSON(t, orgResp, &org)
	orgID := int(org["id"].(float64))

	assignOrg := func(customerID int) {
		t.Helper()
		resp := MakeAuthRequest(t, server, http.MethodPut,
			fmt.Sprintf("/portal-customers/%d/organisation", customerID),
			map[string]interface{}{"customer_organisation_id": orgID})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	}
	assignOrg(creatorID)
	assignOrg(colleagueID)

	setSharing := func(mode, audience string, roleIDs []int) *http.Response {
		body := map[string]interface{}{
			"request_sharing":          mode,
			"request_sharing_audience": audience,
		}
		if roleIDs != nil {
			body["visible_role_ids"] = roleIDs
		}
		return MakeAuthRequest(t, server, http.MethodPut,
			fmt.Sprintf("/customer-organisations/%d/request-sharing", orgID), body)
	}

	submit := func(cookie, title string, share bool) int {
		t.Helper()
		resp := MakePortalRequest(t, server, cookie, http.MethodPost,
			fmt.Sprintf("/portal/%s/submit", portalSlug),
			map[string]interface{}{"title": title, "description": "d", "share_with_organisation": share})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
		var result map[string]interface{}
		DecodeJSON(t, resp, &result)
		return int(result["item_id"].(float64))
	}

	itemSharedFlag := func(itemID int) bool {
		t.Helper()
		var shared bool
		if err := server.DB().QueryRow(`SELECT portal_org_shared FROM items WHERE id = ?`, itemID).Scan(&shared); err != nil {
			t.Fatalf("read portal_org_shared for %d: %v", itemID, err)
		}
		return shared
	}

	myRequests := func(cookie string) map[int]map[string]interface{} {
		t.Helper()
		resp := MakePortalRequest(t, server, cookie, http.MethodGet,
			fmt.Sprintf("/portal/%s/my-requests", portalSlug), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var requests []map[string]interface{}
		DecodeJSON(t, resp, &requests)
		got := map[int]map[string]interface{}{}
		for _, r := range requests {
			got[int(r["id"].(float64))] = r
		}
		return got
	}
	myRequestIDs := func(cookie string) map[int]bool {
		t.Helper()
		got := map[int]bool{}
		for id := range myRequests(cookie) {
			got[id] = true
		}
		return got
	}

	t.Run("requester_choice honours the creator flag", func(t *testing.T) {
		resp := setSharing("requester_choice", "organisation", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		shared := submit(creatorCookie, "Shared ticket", true)
		private := submit(creatorCookie, "Private ticket", false)
		if !itemSharedFlag(shared) {
			t.Fatalf("shared submission must set portal_org_shared")
		}
		if itemSharedFlag(private) {
			t.Fatalf("private submission must not set portal_org_shared")
		}
		got := myRequests(colleagueCookie)
		if _, ok := got[shared]; !ok {
			t.Fatalf("colleague must see the shared ticket; got %v", got)
		}
		if _, ok := got[private]; ok {
			t.Fatalf("colleague must not see the private ticket; got %v", got)
		}
		if sharedRow := got[shared]; sharedRow["shared"] != true {
			t.Fatalf("shared ticket must carry shared=true; got %v", sharedRow["shared"])
		}
		if ownRow := myRequests(creatorCookie)[shared]; ownRow["shared"] == true {
			t.Fatalf("creator's own ticket must not be labelled shared")
		}
	})

	t.Run("bootstrap exposes the sharing mode", func(t *testing.T) {
		resp := setSharing("requester_choice", "organisation", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		boot := MakePortalRequest(t, server, creatorCookie, http.MethodGet,
			fmt.Sprintf("/portal/%s/user-bootstrap", portalSlug), nil)
		defer boot.Body.Close()
		AssertStatusCode(t, boot, http.StatusOK)
		var body map[string]interface{}
		DecodeJSON(t, boot, &body)
		if body["request_sharing"] != "requester_choice" {
			t.Fatalf("bootstrap request_sharing = %v, want requester_choice", body["request_sharing"])
		}
	})

	t.Run("automatic shares regardless of the flag", func(t *testing.T) {
		resp := setSharing("automatic", "organisation", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		item := submit(creatorCookie, "Auto ticket", false)
		if got := myRequestIDs(colleagueCookie); !got[item] {
			t.Fatalf("automatic mode must share the ticket; got %v", got)
		}
	})

	t.Run("disabled ignores the flag", func(t *testing.T) {
		resp := setSharing("disabled", "organisation", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		item := submit(creatorCookie, "Disabled ticket", true)
		if got := myRequestIDs(colleagueCookie); got[item] {
			t.Fatalf("disabled mode must not share the ticket; got %v", got)
		}
	})

	t.Run("validation rejects bad policy values", func(t *testing.T) {
		bad := setSharing("bogus", "organisation", nil)
		defer bad.Body.Close()
		AssertStatusCode(t, bad, http.StatusBadRequest)

		emptyRoles := setSharing("requester_choice", "roles", nil)
		defer emptyRoles.Body.Close()
		AssertStatusCode(t, emptyRoles, http.StatusBadRequest)

		unknownRole := setSharing("requester_choice", "roles", []int{999999})
		defer unknownRole.Body.Close()
		AssertStatusCode(t, unknownRole, http.StatusBadRequest)
	})

	t.Run("requires customers.manage", func(t *testing.T) {
		uname := "limited-" + shortKey("lim")
		_, username, password := CreateTestUserWithCredentials(t, server, uname, uname+"@example.test")
		token := CreateBearerTokenForUser(t, server, username, password)
		resp := MakeAuthRequestWithToken(t, server, token, http.MethodPut,
			fmt.Sprintf("/customer-organisations/%d/request-sharing", orgID),
			map[string]interface{}{"request_sharing": "automatic", "request_sharing_audience": "organisation"})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
	})
}
