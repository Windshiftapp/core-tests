package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestPortalCustomerDeactivation pins the security deactivation lifecycle
// (WI-1554): deactivation cuts portal access without exercising the erasure
// right — it invalidates every live session, all portal auth paths refuse the
// customer (existing session validation, magic-link issuance), the decision
// and its actor are audited, and reactivation is an explicit admin action
// that never resurrects an erased customer.
func TestPortalCustomerDeactivation(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Customer Deactivation WS", shortKey("DEA"))
	portalSlug, channelID := SetupPortalChannel(t, server, wsID)

	customerEmail := fmt.Sprintf("deactivate-subject-%d@test.local", wsID)
	customerID, portalCookie := CreatePortalCustomerWithSession(t, server, channelID, "Deactivation Subject", customerEmail)
	itemID := SubmitPortalRequest(t, server, portalSlug, portalCookie, "Deactivation request")

	// Baseline: the live session authenticates.
	before := MakePortalRequest(t, server, portalCookie, http.MethodGet,
		fmt.Sprintf("/portal/%s/requests/%d/comments", portalSlug, itemID), nil)
	defer before.Body.Close()
	AssertStatusCode(t, before, http.StatusOK)

	// Deactivate.
	deactivateResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/portal-customers/%d/deactivate", customerID), nil)
	defer deactivateResp.Body.Close()
	AssertStatusCode(t, deactivateResp, http.StatusOK)

	var updated struct {
		ID            int      `json:"id"`
		DeactivatedAt *string  `json:"deactivated_at"`
	}
	DecodeJSON(t, deactivateResp, &updated)
	if updated.ID != customerID || updated.DeactivatedAt == nil || *updated.DeactivatedAt == "" {
		t.Fatalf("deactivate response = %#v, want deactivated_at set", updated)
	}

	// Session invalidation: rows gone and the old cookie no longer authenticates.
	var sessionCount int
	if err := server.DB().QueryRow(
		`SELECT COUNT(*) FROM portal_customer_sessions WHERE portal_customer_id = ?`, customerID,
	).Scan(&sessionCount); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessionCount != 0 {
		t.Errorf("sessions after deactivation = %d, want 0 (must be invalidated)", sessionCount)
	}
	after := MakePortalRequest(t, server, portalCookie, http.MethodGet,
		fmt.Sprintf("/portal/%s/requests/%d/comments", portalSlug, itemID), nil)
	defer after.Body.Close()
	AssertStatusCode(t, after, http.StatusUnauthorized)

	// Magic-link issuance is refused, but the response stays a generic
	// success so deactivation state is not disclosed to arbitrary callers.
	magicResp := MakeUnauthenticatedRequest(t, server, http.MethodPost,
		fmt.Sprintf("/portal/%s/auth/request", portalSlug),
		map[string]interface{}{"email": customerEmail})
	defer magicResp.Body.Close()
	AssertStatusCode(t, magicResp, http.StatusOK)
	var magicRows int
	if err := server.DB().QueryRow(
		`SELECT COUNT(*) FROM portal_customer_magic_links WHERE portal_customer_id = ?`, customerID,
	).Scan(&magicRows); err != nil {
		t.Fatalf("count magic links: %v", err)
	}
	if magicRows != 0 {
		t.Errorf("magic links issued for deactivated customer = %d, want 0", magicRows)
	}

	// The deactivation is audited with the acting identity and subject.
	var auditDetails string
	if err := server.DB().QueryRow(
		`SELECT details FROM audit_logs WHERE action_type = 'portal_customer.deactivate' AND resource_id = ?`, customerID,
	).Scan(&auditDetails); err != nil {
		t.Fatalf("deactivation audit row missing: %v", err)
	}
	if !strings.Contains(auditDetails, customerEmail) {
		t.Errorf("deactivation audit details lost the subject: %s", auditDetails)
	}

	// Double deactivation is refused.
	second := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/portal-customers/%d/deactivate", customerID), nil)
	defer second.Body.Close()
	AssertStatusCode(t, second, http.StatusConflict)

	// Activating a customer that is not deactivated is a validation error.
	bogusActivate := MakeAuthRequest(t, server, http.MethodPost, "/portal-customers/999999999/activate", nil)
	defer bogusActivate.Body.Close()
	AssertStatusCode(t, bogusActivate, http.StatusNotFound)

	// Reactivation restores access: magic-link issuance works again.
	activateResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/portal-customers/%d/activate", customerID), nil)
	defer activateResp.Body.Close()
	AssertStatusCode(t, activateResp, http.StatusOK)

	var reactivated struct {
		DeactivatedAt *string `json:"deactivated_at"`
	}
	DecodeJSON(t, activateResp, &reactivated)
	if reactivated.DeactivatedAt != nil {
		t.Errorf("reactivated customer still carries deactivated_at = %v", *reactivated.DeactivatedAt)
	}

	reactivateMagic := MakeUnauthenticatedRequest(t, server, http.MethodPost,
		fmt.Sprintf("/portal/%s/auth/request", portalSlug),
		map[string]interface{}{"email": customerEmail})
	defer reactivateMagic.Body.Close()
	AssertStatusCode(t, reactivateMagic, http.StatusOK)
	if err := server.DB().QueryRow(
		`SELECT COUNT(*) FROM portal_customer_magic_links WHERE portal_customer_id = ?`, customerID,
	).Scan(&magicRows); err != nil {
		t.Fatalf("count magic links after reactivation: %v", err)
	}
	if magicRows != 1 {
		t.Errorf("magic links after reactivation = %d, want 1", magicRows)
	}

	// The reactivation is audited too.
	var activateAudit int
	if err := server.DB().QueryRow(
		`SELECT COUNT(*) FROM audit_logs WHERE action_type = 'portal_customer.activate' AND resource_id = ?`, customerID,
	).Scan(&activateAudit); err != nil {
		t.Fatalf("count activation audit rows: %v", err)
	}
	if activateAudit != 1 {
		t.Errorf("activation audit rows = %d, want 1", activateAudit)
	}

	// Activating a non-deactivated customer is a validation error.
	activateAgain := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/portal-customers/%d/activate", customerID), nil)
	defer activateAgain.Body.Close()
	AssertStatusCode(t, activateAgain, http.StatusBadRequest)
}

// TestPortalCustomerDeactivationErasureInterplay pins the state machine
// boundary: erasure implies deactivation, and an erased customer can never be
// reactivated or deactivated again.
func TestPortalCustomerDeactivationErasureInterplay(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Deactivation Erasure WS", shortKey("DEE"))
	_, channelID := SetupPortalChannel(t, server, wsID)
	customerID, _ := CreatePortalCustomerWithSession(t, server, channelID, "Erased Subject", fmt.Sprintf("deact-erased-%d@test.local", wsID))

	eraseResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/portal-customers/%d/erase", customerID), map[string]interface{}{
		"requested_by": "dsar intake",
	})
	defer eraseResp.Body.Close()
	AssertStatusCode(t, eraseResp, http.StatusCreated)

	// Erasure implies deactivation.
	var deactivatedAt *string
	var erasedAt *string
	if err := server.DB().QueryRow(
		`SELECT deactivated_at, erased_at FROM portal_customers WHERE id = ?`, customerID,
	).Scan(&deactivatedAt, &erasedAt); err != nil {
		t.Fatalf("read customer row: %v", err)
	}
	if deactivatedAt == nil || erasedAt == nil {
		t.Fatalf("erased customer state: deactivated_at=%v erased_at=%v, want both set", deactivatedAt, erasedAt)
	}

	// Reactivation of an erased customer is refused with 409.
	reactivate := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/portal-customers/%d/activate", customerID), nil)
	defer reactivate.Body.Close()
	AssertStatusCode(t, reactivate, http.StatusConflict)

	// Deactivation of an erased customer is refused too — the identity is
	// closed.
	deactivate := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/portal-customers/%d/deactivate", customerID), nil)
	defer deactivate.Body.Close()
	AssertStatusCode(t, deactivate, http.StatusConflict)
}

// TestPortalCustomerDeactivationDenials pins the permission and lookup
// contract: customers.manage is required on both lifecycle endpoints and
// unknown customers 404.
func TestPortalCustomerDeactivationDenials(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Deactivation Denials WS", shortKey("DED"))
	_, channelID := SetupPortalChannel(t, server, wsID)
	customerID, _ := CreatePortalCustomerWithSession(t, server, channelID, "Denial Subject", fmt.Sprintf("deact-denial-%d@test.local", wsID))

	t.Run("non_privileged_user_cannot_deactivate", func(t *testing.T) {
		_, _, plainPass := CreateTestUserWithCredentials(t, server, fmt.Sprintf("deactplain%d", wsID), fmt.Sprintf("deactplain%d@test.local", wsID))
		plainSession, _ := CreateAuthCredentialsForUser(t, server, fmt.Sprintf("deactplain%d", wsID), plainPass)

		resp := MakeAuthRequestWithToken(t, server, plainSession, http.MethodPost, fmt.Sprintf("/portal-customers/%d/deactivate", customerID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
	})

	t.Run("non_privileged_user_cannot_activate", func(t *testing.T) {
		_, _, plainPass := CreateTestUserWithCredentials(t, server, fmt.Sprintf("deactplain2%d", wsID), fmt.Sprintf("deactplain2%d@test.local", wsID))
		plainSession, _ := CreateAuthCredentialsForUser(t, server, fmt.Sprintf("deactplain2%d", wsID), plainPass)

		resp := MakeAuthRequestWithToken(t, server, plainSession, http.MethodPost, fmt.Sprintf("/portal-customers/%d/activate", customerID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
	})

	t.Run("unknown_customer_is_not_found", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, "/portal-customers/999999999/deactivate", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNotFound)
	})
}
