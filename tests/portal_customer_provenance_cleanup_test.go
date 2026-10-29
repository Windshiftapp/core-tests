package tests

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// attachPortalSession inserts a portal session for an existing customer and
// returns the raw session cookie for MakePortalRequest.
func attachPortalSession(t *testing.T, testServer *TestServer, channelID, customerID int) (string, string) {
	t.Helper()

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		t.Fatalf("generate session token: %v", err)
	}
	rawToken := fmt.Sprintf("%x", tokenBytes)
	if _, err := testServer.DB().ExecWrite(
		`INSERT INTO portal_customer_sessions (portal_customer_id, session_token, channel_id, expires_at, ip_address, user_agent, is_active, created_at)
		 VALUES (?, ?, ?, ?, '', 'test-agent', true, ?)`,
		customerID, rawToken, channelID, time.Now().Add(7*24*time.Hour), time.Now(),
	); err != nil {
		t.Fatalf("insert portal session: %v", err)
	}
	return rawToken, encodePortalSessionCookie(t, rawToken)
}

// TestPortalCustomerBulkCleanup pins the admin bulk cleanup of stale
// auto-created customers (WI-1553): the run erases matching customers through
// the standard erasure flow (pseudonymization + DSAR evidence), skips
// candidates that hold ticket content, records one batch audit event listing
// the erased set, and leaves non-matching customers untouched.
func TestPortalCustomerBulkCleanup(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Bulk Cleanup WS", shortKey("BCL"))
	portalSlug, channelID := SetupPortalChannel(t, server, wsID)

	mintMagicLinkCustomer := func(local string) int {
		resp := MakeUnauthenticatedRequest(t, server, http.MethodPost,
			fmt.Sprintf("/portal/%s/auth/request", portalSlug),
			map[string]interface{}{"email": fmt.Sprintf("%s-%d@test.local", local, wsID)})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var customerID int
		if err := server.DB().QueryRow(
			`SELECT id FROM portal_customers WHERE email = ?`, fmt.Sprintf("%s-%d@test.local", local, wsID),
		).Scan(&customerID); err != nil {
			t.Fatalf("auto-created customer row missing: %v", err)
		}
		return customerID
	}

	emptyID := mintMagicLinkCustomer("bulkempty")
	ticketedID := mintMagicLinkCustomer("bulkticketed")

	// The ticketed customer authored a request — the exclusion boundary. The
	// customer already exists (minted above), so attach a session directly.
	_, existingSession := attachPortalSession(t, server, channelID, ticketedID)
	SubmitPortalRequest(t, server, portalSlug, existingSession, "Cleanup boundary ticket")

	// Provenance is captured on the intake path.
	var createdVia string
	if err := server.DB().QueryRow(
		`SELECT created_via FROM portal_customers WHERE id = ?`, emptyID,
	).Scan(&createdVia); err != nil {
		t.Fatalf("read created_via: %v", err)
	}
	if createdVia != "magic-link" {
		t.Fatalf("created_via = %q, want magic-link", createdVia)
	}

	// Admin-created customers carry agent provenance.
	adminResp := MakeAuthRequest(t, server, http.MethodPost, "/portal-customers", map[string]interface{}{
		"name":  "Admin Created",
		"email": fmt.Sprintf("bulkadmin-%d@test.local", wsID),
	})
	defer adminResp.Body.Close()
	AssertStatusCode(t, adminResp, http.StatusCreated)
	var created map[string]interface{}
	DecodeJSON(t, adminResp, &created)
	if created["created_via"] != "agent" {
		t.Fatalf("admin-created created_via = %v, want agent", created["created_via"])
	}

	// Run the cleanup limited to magic-link customers.
	cleanupResp := MakeAuthRequest(t, server, http.MethodPost, "/portal-customers/cleanup", map[string]interface{}{
		"created_via": "magic-link",
	})
	defer cleanupResp.Body.Close()
	AssertStatusCode(t, cleanupResp, http.StatusOK)

	var result struct {
		Erased []struct {
			ID    int    `json:"id"`
			Email string `json:"email"`
		} `json:"erased"`
		Skipped []struct {
			ID     int    `json:"id"`
			Email  string `json:"email"`
			Reason string `json:"reason"`
		} `json:"skipped"`
		Failed       []struct {
			ID int `json:"id"`
		} `json:"failed"`
		ScannedCount int `json:"scanned_count"`
	}
	DecodeJSON(t, cleanupResp, &result)

	if len(result.Erased) != 1 || result.Erased[0].ID != emptyID {
		t.Fatalf("erased = %#v, want exactly the ticketless customer %d", result.Erased, emptyID)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].ID != ticketedID {
		t.Fatalf("skipped = %#v, want exactly the ticketed customer %d", result.Skipped, ticketedID)
	}
	if !strings.Contains(result.Skipped[0].Reason, "item") {
		t.Errorf("skip reason = %q, want an item-related exclusion", result.Skipped[0].Reason)
	}
	if len(result.Failed) != 0 {
		t.Fatalf("failed = %#v, want none", result.Failed)
	}

	// The erased customer is pseudonymized with DSAR evidence.
	var name, email string
	if err := server.DB().QueryRow(
		`SELECT name, email FROM portal_customers WHERE id = ?`, emptyID,
	).Scan(&name, &email); err != nil {
		t.Fatalf("erased customer row missing: %v", err)
	}
	if name != fmt.Sprintf("deleted-customer-%d", emptyID) {
		t.Errorf("erased customer name = %q, want pseudonym", name)
	}
	var evidenceRows int
	if err := server.DB().QueryRow(
		`SELECT COUNT(*) FROM customer_erasure_records WHERE portal_customer_id = ?`, emptyID,
	).Scan(&evidenceRows); err != nil {
		t.Fatalf("count erasure records: %v", err)
	}
	if evidenceRows != 1 {
		t.Errorf("erasure records = %d, want 1", evidenceRows)
	}

	// The ticketed customer is untouched.
	var ticketedName string
	if err := server.DB().QueryRow(
		`SELECT name FROM portal_customers WHERE id = ?`, ticketedID,
	).Scan(&ticketedName); err != nil {
		t.Fatalf("ticketed customer row missing: %v", err)
	}
	if ticketedName == fmt.Sprintf("deleted-customer-%d", ticketedID) {
		t.Errorf("ticketed customer was erased; want skipped")
	}

	// One batch audit event lists the erased set.
	var auditDetails string
	if err := server.DB().QueryRow(
		`SELECT details FROM audit_logs WHERE action_type = 'portal_customer.bulk_cleanup'`,
	).Scan(&auditDetails); err != nil {
		t.Fatalf("bulk cleanup audit row missing: %v", err)
	}
	if !strings.Contains(auditDetails, fmt.Sprintf(`"id":%d`, emptyID)) || !strings.Contains(auditDetails, "magic-link") {
		t.Errorf("audit details missing the erased customer or filter: %s", auditDetails)
	}

	// A repeat run finds nothing left to erase.
	repeatResp := MakeAuthRequest(t, server, http.MethodPost, "/portal-customers/cleanup", map[string]interface{}{
		"created_via": "magic-link",
	})
	defer repeatResp.Body.Close()
	AssertStatusCode(t, repeatResp, http.StatusOK)
	var repeat struct {
		Erased []struct {
			ID int `json:"id"`
		} `json:"erased"`
	}
	DecodeJSON(t, repeatResp, &repeat)
	if len(repeat.Erased) != 0 {
		t.Errorf("repeat run erased = %#v, want none", repeat.Erased)
	}
}

// TestPortalCustomerBulkCleanupContract pins validation and access: invalid
// filters 400, non-privileged callers 403.
func TestPortalCustomerBulkCleanupContract(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	CreateTestWorkspace(t, server, "Bulk Cleanup Contract WS", shortKey("BCC"))

	t.Run("invalid_created_via_is_rejected", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, "/portal-customers/cleanup", map[string]interface{}{
			"created_via": "spam",
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	t.Run("negative_age_is_rejected", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, "/portal-customers/cleanup", map[string]interface{}{
			"older_than_days": -5,
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	t.Run("non_privileged_user_cannot_cleanup", func(t *testing.T) {
		_, _, plainPass := CreateTestUserWithCredentials(t, server, "bulkplain", "bulkplain@test.local")
		plainSession, _ := CreateAuthCredentialsForUser(t, server, "bulkplain", plainPass)

		resp := MakeAuthRequestWithToken(t, server, plainSession, http.MethodPost, "/portal-customers/cleanup", map[string]interface{}{})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
	})
}
