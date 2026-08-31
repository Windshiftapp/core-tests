package tests

import (
	"fmt"
	"io"
	"net/http"
	"testing"
)

// TestPortalCustomer_CannotAccessInternalEndpoints verifies that a portal session token
// cannot be used to access internal API endpoints. Portal tokens are stored in
// portal_customer_sessions (not api_tokens), so RequireAuth rejects them.
func TestPortalCustomer_CannotAccessInternalEndpoints(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	// Create workspace and item for testing
	workspaceID, _ := CreateTestWorkspace(t, server, "Portal Boundary Test", shortKey("PBT"))
	itemID := CreateTestItem(t, server, workspaceID, "Test Item")

	// Portal channel is needed so the portal session can bind to a channel.
	// The test never uses the session against the portal — it only verifies
	// portal tokens are rejected on internal endpoints.
	_, channelID := SetupPortalChannel(t, server, workspaceID)
	_, portalToken := CreatePortalCustomerWithSession(t, server, channelID, "Portal User", "portal@test.com")

	// All internal endpoints should reject portal tokens with 401
	tests := []struct {
		name     string
		method   string
		endpoint string
	}{
		{"GET /items", http.MethodGet, "/items"},
		{"GET /items/{id}", http.MethodGet, fmt.Sprintf("/items/%d", itemID)},
		{"POST /items", http.MethodPost, "/items"},
		{"PUT /items/{id}", http.MethodPut, fmt.Sprintf("/items/%d", itemID)},
		{"DELETE /items/{id}", http.MethodDelete, fmt.Sprintf("/items/%d", itemID)},
		{"GET /workspaces", http.MethodGet, "/workspaces"},
		{"GET /workspaces/{id}", http.MethodGet, fmt.Sprintf("/workspaces/%d", workspaceID)},
		{"GET /users", http.MethodGet, "/users"},
		{"GET /permissions", http.MethodGet, "/permissions"},
		{"GET /channels", http.MethodGet, "/channels"},
		{"GET /configuration-sets", http.MethodGet, "/configuration-sets"},
		{"GET /custom-fields", http.MethodGet, "/custom-fields"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := MakePortalRequest(t, server, portalToken, tc.method, tc.endpoint, nil)
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("Expected 401 Unauthorized for portal token on %s %s, got %d",
					tc.method, tc.endpoint, resp.StatusCode)
			}
		})
	}
}

// TestUnauthenticated_CannotAccessInternalEndpoints verifies that requests with no
// authentication are rejected from internal endpoints. The portal bootstrap remains
// available only as a minimal sign-in shell.
func TestUnauthenticated_CannotAccessInternalEndpoints(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Unauth Boundary Test", shortKey("UBT"))
	portalSlug, _ := SetupPortalChannel(t, server, workspaceID)

	// Internal endpoints should reject unauthenticated requests
	internalTests := []struct {
		name     string
		method   string
		endpoint string
	}{
		{"GET /items", http.MethodGet, "/items"},
		{"POST /items", http.MethodPost, "/items"},
		{"GET /workspaces", http.MethodGet, "/workspaces"},
		{"GET /users", http.MethodGet, "/users"},
		{"GET /permissions", http.MethodGet, "/permissions"},
		{"GET /channels", http.MethodGet, "/channels"},
		{"GET /configuration-sets", http.MethodGet, "/configuration-sets"},
	}

	for _, tc := range internalTests {
		t.Run("Rejected/"+tc.name, func(t *testing.T) {
			resp := MakeUnauthenticatedRequest(t, server, tc.method, tc.endpoint, nil)
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("Expected 401 Unauthorized for unauthenticated request on %s %s, got %d",
					tc.method, tc.endpoint, resp.StatusCode)
			}
		})
	}

	// The anonymous portal bootstrap contains only the branded sign-in shell.
	t.Run("Allowed/GET /portal/{slug}/bootstrap", func(t *testing.T) {
		resp := MakeUnauthenticatedRequest(t, server, http.MethodGet, fmt.Sprintf("/portal/%s/bootstrap", portalSlug), nil)
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 OK for portal sign-in bootstrap, got %d", resp.StatusCode)
		}
		var bootstrap struct {
			Portal       map[string]interface{}   `json:"portal"`
			RequestTypes []map[string]interface{} `json:"request_types"`
			AssetReports []map[string]interface{} `json:"asset_reports"`
		}
		DecodeJSON(t, resp, &bootstrap)
		if len(bootstrap.RequestTypes) != 0 || len(bootstrap.AssetReports) != 0 {
			t.Fatalf("anonymous bootstrap exposed catalogs: %+v", bootstrap)
		}
		for _, sensitiveField := range []string{
			"channel_id", "workspace", "workspace_id", "workspace_ids", "sections",
			"footer_columns", "knowledge_base_share_link", "knowledge_base_url", "knowledge_base_share_id",
		} {
			if _, exposed := bootstrap.Portal[sensitiveField]; exposed {
				t.Fatalf("anonymous bootstrap exposed portal field %q: %+v", sensitiveField, bootstrap.Portal)
			}
		}
	})
}

func TestUnauthenticated_CannotReadPortalCatalogsOrAssetInventory(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Private portal reads", shortKey("PPR"))
	portalSlug, _ := SetupPortalChannel(t, server, workspaceID)

	tests := []struct {
		name     string
		method   string
		endpoint string
		body     interface{}
	}{
		{name: "portal metadata", method: http.MethodGet, endpoint: fmt.Sprintf("/portal/%s", portalSlug)},
		{name: "request types", method: http.MethodGet, endpoint: fmt.Sprintf("/portal/%s/request-types", portalSlug)},
		{name: "asset reports", method: http.MethodGet, endpoint: fmt.Sprintf("/portal/%s/asset-reports", portalSlug)},
		{name: "execute asset report", method: http.MethodGet, endpoint: fmt.Sprintf("/portal/%s/asset-reports/999999/execute", portalSlug)},
		{name: "submit asset report", method: http.MethodPost, endpoint: fmt.Sprintf("/portal/%s/asset-reports/999999/execute", portalSlug), body: map[string]interface{}{"params": map[string]string{}}},
		{name: "asset report fields", method: http.MethodGet, endpoint: fmt.Sprintf("/portal/%s/asset-reports/999999/fields", portalSlug)},
		{name: "knowledge base search", method: http.MethodPost, endpoint: fmt.Sprintf("/portal/%s/knowledge-base/search", portalSlug), body: map[string]string{"query": "vpn"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := MakeUnauthenticatedRequest(t, server, tc.method, tc.endpoint, tc.body)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d, want 401; body=%s", resp.StatusCode, body)
			}
			var denial struct {
				Code string `json:"code"`
			}
			DecodeJSON(t, resp, &denial)
			if denial.Code != "AUTHENTICATION_REQUIRED" {
				t.Fatalf("denial code = %q, want AUTHENTICATION_REQUIRED", denial.Code)
			}
		})
	}
}

func TestManualPortalReadRequiresCurrentChannelGrant(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Manual portal reads", shortKey("MPR"))
	portalSlug, channelID := SetupPortalChannel(t, server, workspaceID)
	customerID, portalCookie := CreatePortalCustomerWithSession(
		t, server, channelID, "Uninvited portal customer", "uninvited-portal-reader@test.com",
	)

	configResp := MakeAuthRequest(t, server, http.MethodPut,
		fmt.Sprintf("/channels/%d/config", channelID), map[string]interface{}{
			"config": map[string]interface{}{
				"portal_slug":              portalSlug,
				"portal_enabled":           true,
				"portal_title":             "Manual portal",
				"portal_registration_mode": "manual",
				"portal_workspace_ids":     []int{workspaceID},
			},
		})
	defer configResp.Body.Close()
	AssertStatusCode(t, configResp, http.StatusOK)

	requestTypesEndpoint := fmt.Sprintf("/portal/%s/request-types", portalSlug)
	assertDenied := func(stage string) {
		t.Helper()
		resp := MakePortalRequest(t, server, portalCookie, http.MethodGet, requestTypesEndpoint, nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("%s status = %d, want 401; body=%s", stage, resp.StatusCode, body)
		}
	}

	assertDenied("without grant")

	// The customer-management API currently exposes channel grants read-only,
	// so this authorization fixture creates the same row the admin flow owns.
	if _, err := server.DB().ExecWrite(`
		INSERT INTO portal_customer_channels (portal_customer_id, channel_id, created_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
	`, customerID, channelID); err != nil {
		t.Fatalf("grant portal channel access: %v", err)
	}

	allowedResp := MakePortalRequest(t, server, portalCookie, http.MethodGet, requestTypesEndpoint, nil)
	defer allowedResp.Body.Close()
	if allowedResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(allowedResp.Body)
		t.Fatalf("granted status = %d, want 200; body=%s", allowedResp.StatusCode, body)
	}
	var requestTypes []map[string]interface{}
	DecodeJSON(t, allowedResp, &requestTypes)
	if len(requestTypes) == 0 || requestTypes[0]["name"] != "General Request" {
		t.Fatalf("granted request types = %+v, want General Request", requestTypes)
	}

	if _, err := server.DB().ExecWrite(`
		DELETE FROM portal_customer_channels
		WHERE portal_customer_id = ? AND channel_id = ?
	`, customerID, channelID); err != nil {
		t.Fatalf("revoke portal channel access: %v", err)
	}
	assertDenied("after revocation")
}

// TestPortalCustomer_IDOR_Isolation verifies that portal customer A cannot access
// portal customer B's requests. The portal returns 404 (not 403) to prevent enumeration.
func TestPortalCustomer_IDOR_Isolation(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "IDOR Test Workspace", shortKey("IDOR"))
	portalSlug, channelID := SetupPortalChannel(t, server, workspaceID)

	// Create two portal customers with sessions
	_, tokenA := CreatePortalCustomerWithSession(t, server, channelID, "Customer A", "customerA@test.com")
	_, tokenB := CreatePortalCustomerWithSession(t, server, channelID, "Customer B", "customerB@test.com")

	// Each customer submits a request (using their token for authentication)
	itemIDA := SubmitPortalRequest(t, server, portalSlug, tokenA, "Request from Customer A")
	itemIDB := SubmitPortalRequest(t, server, portalSlug, tokenB, "Request from Customer B")

	t.Logf("Customer A item: %d, Customer B item: %d", itemIDA, itemIDB)

	// Customer A can see own requests
	t.Run("CustomerA_CanSeeOwnRequests", func(t *testing.T) {
		resp := MakePortalRequest(t, server, tokenA, http.MethodGet,
			fmt.Sprintf("/portal/%s/my-requests", portalSlug), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	// Customer A can see own request detail
	t.Run("CustomerA_CanSeeOwnRequestDetail", func(t *testing.T) {
		resp := MakePortalRequest(t, server, tokenA, http.MethodGet,
			fmt.Sprintf("/portal/%s/requests/%d", portalSlug, itemIDA), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
	})

	// Customer A CANNOT see Customer B's request detail (expects 404 to prevent enumeration)
	t.Run("CustomerA_CannotSeeCustomerB_RequestDetail", func(t *testing.T) {
		resp := MakePortalRequest(t, server, tokenA, http.MethodGet,
			fmt.Sprintf("/portal/%s/requests/%d", portalSlug, itemIDB), nil)
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Expected 404 Not Found (IDOR protection), got %d", resp.StatusCode)
		}
	})

	// Customer A CANNOT see Customer B's comments (expects 404)
	t.Run("CustomerA_CannotSeeCustomerB_Comments", func(t *testing.T) {
		resp := MakePortalRequest(t, server, tokenA, http.MethodGet,
			fmt.Sprintf("/portal/%s/requests/%d/comments", portalSlug, itemIDB), nil)
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Expected 404 Not Found (IDOR protection), got %d", resp.StatusCode)
		}
	})

	// Bidirectional: Customer B CANNOT see Customer A's request detail
	t.Run("CustomerB_CannotSeeCustomerA_RequestDetail", func(t *testing.T) {
		resp := MakePortalRequest(t, server, tokenB, http.MethodGet,
			fmt.Sprintf("/portal/%s/requests/%d", portalSlug, itemIDA), nil)
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Expected 404 Not Found (IDOR protection), got %d", resp.StatusCode)
		}
	})

	// Bidirectional: Customer B CANNOT see Customer A's comments
	t.Run("CustomerB_CannotSeeCustomerA_Comments", func(t *testing.T) {
		resp := MakePortalRequest(t, server, tokenB, http.MethodGet,
			fmt.Sprintf("/portal/%s/requests/%d/comments", portalSlug, itemIDA), nil)
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Expected 404 Not Found (IDOR protection), got %d", resp.StatusCode)
		}
	})
}

func TestPortalCustomer_CannotExecuteAssetReportFromDifferentPortal(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Asset report boundary", shortKey("ARB"))
	_, publicChannelID := SetupPortalChannel(t, server, workspaceID)
	restrictedSlug, restrictedChannelID := SetupPortalChannel(t, server, workspaceID)

	configResp := MakeAuthRequest(t, server, http.MethodPut,
		fmt.Sprintf("/channels/%d/config", restrictedChannelID), map[string]interface{}{
			"config": map[string]interface{}{
				"portal_slug":              restrictedSlug,
				"portal_enabled":           true,
				"portal_title":             "Restricted portal",
				"portal_registration_mode": "manual",
				"portal_workspace_ids":     []int{workspaceID},
			},
		})
	defer configResp.Body.Close()
	AssertStatusCode(t, configResp, http.StatusOK)

	assetSetID := createTestAssetSet(t, server, "Restricted portal assets")
	reportResp := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/channels/%d/asset-reports", restrictedChannelID), map[string]interface{}{
			"name":          "Internal",
			"asset_set_id":  assetSetID,
			"cql_query":     "title != null",
			"column_config": []string{"title"},
			"run_mode":      "direct",
		})
	defer reportResp.Body.Close()
	if reportResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(reportResp.Body)
		t.Fatalf("create restricted asset report: status = %d, want 201; body=%s", reportResp.StatusCode, body)
	}
	var report map[string]interface{}
	DecodeJSON(t, reportResp, &report)
	reportID := ExtractIDFromResponse(t, report)

	_, publicPortalCookie := CreatePortalCustomerWithSession(
		t, server, publicChannelID, "Public portal customer", "public-portal-customer@test.com",
	)
	executeResp := MakePortalRequest(t, server, publicPortalCookie, http.MethodPost,
		fmt.Sprintf("/portal/%s/asset-reports/%d/execute?page=1&per_page=100", restrictedSlug, reportID),
		map[string]interface{}{"params": ""})
	defer executeResp.Body.Close()

	if executeResp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(executeResp.Body)
		t.Fatalf("cross-portal asset report execution status = %d, want 401; body=%s", executeResp.StatusCode, body)
	}
	var denial struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	DecodeJSON(t, executeResp, &denial)
	if denial.Error != "Authentication required" || denial.Code != "UNAUTHORIZED" {
		t.Fatalf("cross-portal asset report execution denial = %+v, want authentication required", denial)
	}
}

// TestInternalUser_CannotImpersonatePortalCustomer verifies that an internal user's
// bearer token is rejected by portal request-tracking endpoints when the user has
// no linked portal customer.
//
// The premise — "internal bearer token reaches portal handlers" — is no longer
// possible: the cookie-auth surface (which all /api/portal/* routes live on)
// stopped accepting bearer tokens entirely, so an internal API token cannot
// even authenticate against these routes. The bearer-rejection guarantee is
// covered explicitly by the
// TestAPITokenScopes_DoNotEscalateBeyondUserRole/bearer_rejected_on_cookie_auth_surface
// subtest in api_token_scope_test.go. Keeping this test would require driving
// a real bearer through MakeBearerRequestWithToken and re-asserting 401 — but
// that's already what the scope test checks. Skip rather than duplicate.
func TestInternalUser_CannotImpersonatePortalCustomer(t *testing.T) {
	t.Skip("subsumed by TestAPITokenScopes_DoNotEscalateBeyondUserRole/bearer_rejected_on_cookie_auth_surface")

	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Impersonation Test", shortKey("IMP"))
	portalSlug, channelID := SetupPortalChannel(t, server, workspaceID)

	// Create portal customer and submit a request
	_, portalToken := CreatePortalCustomerWithSession(t, server, channelID, "Real Customer", "real@test.com")
	itemID := SubmitPortalRequest(t, server, portalSlug, portalToken, "Real customer request")

	// Create an internal user (no linked portal customer)
	_, username, password := CreateTestUserWithCredentials(t, server, "internaluser", "internal@test.com")
	internalToken := CreateBearerTokenForUser(t, server, username, password)

	// Internal bearer token should fail on portal request-tracking endpoints
	// because the internal user has no linked portal customer
	portalEndpoints := []struct {
		name     string
		method   string
		endpoint string
	}{
		{"GET /portal/{slug}/my-requests", http.MethodGet,
			fmt.Sprintf("/portal/%s/my-requests", portalSlug)},
		{"GET /portal/{slug}/requests/{itemId}", http.MethodGet,
			fmt.Sprintf("/portal/%s/requests/%d", portalSlug, itemID)},
		{"GET /portal/{slug}/requests/{itemId}/comments", http.MethodGet,
			fmt.Sprintf("/portal/%s/requests/%d/comments", portalSlug, itemID)},
		{"POST /portal/{slug}/requests/{itemId}/comments", http.MethodPost,
			fmt.Sprintf("/portal/%s/requests/%d/comments", portalSlug, itemID)},
	}

	for _, tc := range portalEndpoints {
		t.Run(tc.name, func(t *testing.T) {
			var body interface{}
			if tc.method == http.MethodPost {
				body = map[string]string{"content": "test comment"}
			}

			resp := MakeAuthRequestWithToken(t, server, internalToken, tc.method, tc.endpoint, body)
			defer resp.Body.Close()

			// The internal user's bearer token passes OptionalAuth (sets user context),
			// but getPortalCustomerID falls back to internal session lookup which requires
			// a session cookie (not bearer token). The bearer token path through
			// GetPortalSessionFromRequest will extract the token, but ValidatePortalSession
			// will fail because the token is an API token, not a portal session token.
			// Expected: 401 Unauthorized
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("Expected 401 Unauthorized for internal token on portal endpoint %s, got %d",
					tc.endpoint, resp.StatusCode)
			}
		})
	}
}
