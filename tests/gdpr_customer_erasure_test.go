package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestGBPDCustomerErasure pins the Article 17 erasure flow for portal
// customers (WI-1550): erasure replaces the historical hard delete,
// pseudonymizes the customer row instead of destroying it, records DSAR
// evidence, hard-deletes authentication state and channel grants, retains
// customer-authored ticket content under the pseudonym, and is irreversible.
// The comment-retention assertion is the regression for the old CASCADE
// behavior, which destroyed customer-authored comments with the row.
func TestGBPDCustomerErasure(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "GDPR Customer Erasure WS", "")
	portalSlug, channelID := SetupPortalChannel(t, server, wsID)

	customerEmail := "gdprcustomer@test.local"
	customerID, portalCookie := CreatePortalCustomerWithSession(t, server, channelID, "Erasure Subject", customerEmail)

	// Customer-authored ticket content that must outlive the erasure.
	itemID := SubmitPortalRequest(t, server, portalSlug, portalCookie, "Customer request retained after erasure")
	commentResp := MakePortalRequest(t, server, portalCookie, http.MethodPost,
		fmt.Sprintf("/portal/%s/requests/%d/comments", portalSlug, itemID),
		map[string]interface{}{"content": "customer-authored context that outlives the account"})
	defer commentResp.Body.Close()
	AssertStatusCode(t, commentResp, http.StatusCreated)

	// A live magic link minted through the production auth flow must be
	// destroyed by erasure.
	magicResp := MakeUnauthenticatedRequest(t, server, http.MethodPost,
		fmt.Sprintf("/portal/%s/auth/request", portalSlug),
		map[string]interface{}{"email": customerEmail})
	defer magicResp.Body.Close()
	AssertStatusCode(t, magicResp, http.StatusOK)

	requestedAt := time.Now().Add(-24 * time.Hour)

	eraseResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/portal-customers/%d/erase", customerID), map[string]interface{}{
		"requested_by": "gdprcustomer@test.local via privacy@ channel",
		"requested_at": requestedAt.UTC().Format(time.RFC3339),
		"notes":        "Art. 17 request approved after identity verification",
	})
	defer eraseResp.Body.Close()
	AssertStatusCode(t, eraseResp, http.StatusCreated)

	var evidence struct {
		CustomerID    int    `json:"customer_id"`
		RequestedBy   string `json:"requested_by"`
		RequestedAt   string `json:"requested_at"`
		ApprovedBy    int    `json:"approved_by"`
		ExecutedAt    string `json:"executed_at"`
		PolicyVersion string `json:"policy_version"`
	}
	DecodeJSON(t, eraseResp, &evidence)
	if evidence.CustomerID != customerID || evidence.PolicyVersion == "" || evidence.ApprovedBy == 0 {
		t.Fatalf("evidence = %#v", evidence)
	}
	if _, err := time.Parse(time.RFC3339, evidence.RequestedAt); err != nil {
		t.Errorf("requested_at = %q is not RFC3339", evidence.RequestedAt)
	}

	// The customer row is pseudonymized, not deleted, and its direct
	// identifiers and links are gone.
	var name, email string
	var phone, customFields, orgID, userID *string
	var erasedAt *time.Time
	if err := server.DB().QueryRow(`
		SELECT name, email, phone, custom_field_values, customer_organisation_id, user_id, erased_at
		FROM portal_customers WHERE id = ?`, customerID).
		Scan(&name, &email, &phone, &customFields, &orgID, &userID, &erasedAt); err != nil {
		t.Fatalf("read customer row: %v", err)
	}
	if erasedAt == nil {
		t.Errorf("erased_at not set")
	}
	if name != fmt.Sprintf("deleted-customer-%d", customerID) {
		t.Errorf("customer name = %q, want pseudonym", name)
	}
	if email != fmt.Sprintf("deleted-customer-%d@erased.invalid", customerID) {
		t.Errorf("customer email = %q, want pseudonym", email)
	}
	if phone != nil || customFields != nil || orgID != nil || userID != nil {
		t.Errorf("customer row retained personal data or links: phone=%v custom_fields=%v org=%v user=%v", phone, customFields, orgID, userID)
	}

	// Authentication state and channel grants are hard-deleted, expired or
	// live alike.
	for _, check := range []struct {
		table string
		desc  string
	}{
		{"portal_customer_sessions", "sessions"},
		{"portal_customer_magic_links", "magic links"},
		{"portal_webauthn_credentials", "passkey credentials"},
		{"portal_webauthn_sessions", "passkey sessions"},
		{"portal_request_drafts", "request drafts"},
		{"portal_customer_channels", "channel grants"},
		{"portal_customer_roles", "contact roles"},
	} {
		var count int
		if err := server.DB().QueryRow(
			fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE portal_customer_id = ?`, check.table), customerID,
		).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", check.table, err)
		}
		if count != 0 {
			t.Errorf("%s rows = %d, want 0 (erasure must hard-delete %s)", check.table, count, check.desc)
		}
	}

	// Customer-authored comments survive erasure under the pseudonymized
	// identity — regression for the old CASCADE destruction.
	var commentContent string
	var commentCustomerID int
	if err := server.DB().QueryRow(
		`SELECT content, portal_customer_id FROM comments WHERE item_id = ? ORDER BY id DESC LIMIT 1`, itemID,
	).Scan(&commentContent, &commentCustomerID); err != nil {
		t.Fatalf("customer comment lost during erasure: %v", err)
	}
	if commentContent != "customer-authored context that outlives the account" {
		t.Errorf("comment content changed: %q", commentContent)
	}
	if commentCustomerID != customerID {
		t.Errorf("comment portal_customer_id = %d, want %d (must keep pointing at the pseudonym)", commentCustomerID, customerID)
	}

	// The requester attribution on the item survives under the pseudonym.
	var itemCreatorID *int
	if err := server.DB().QueryRow(
		`SELECT creator_portal_customer_id FROM items WHERE id = ?`, itemID,
	).Scan(&itemCreatorID); err != nil {
		t.Fatalf("read item row: %v", err)
	}
	if itemCreatorID == nil || *itemCreatorID != customerID {
		t.Errorf("item creator_portal_customer_id = %v, want %d", itemCreatorID, customerID)
	}

	// DSAR evidence row persisted.
	var evidenceCount int
	var evidencePolicy string
	if err := server.DB().QueryRow(
		`SELECT COUNT(*), COALESCE(MAX(policy_version), '') FROM customer_erasure_records WHERE portal_customer_id = ?`, customerID,
	).Scan(&evidenceCount, &evidencePolicy); err != nil {
		t.Fatalf("query erasure evidence: %v", err)
	}
	if evidenceCount != 1 || evidencePolicy == "" {
		t.Errorf("customer_erasure_records rows = %d policy = %q, want 1 row with a policy version", evidenceCount, evidencePolicy)
	}

	// The erasure audit event carries the pre-erasure identity snapshot.
	var eraseDetails string
	if err := server.DB().QueryRow(
		`SELECT details FROM audit_logs WHERE action_type = 'portal_customer.erase' AND resource_id = ?`, customerID,
	).Scan(&eraseDetails); err != nil {
		t.Fatalf("erasure audit row for customer missing: %v", err)
	}
	if !strings.Contains(eraseDetails, customerEmail) {
		t.Errorf("erasure audit details lost the pre-erasure identity: %s", eraseDetails)
	}

	// Second erasure is refused — the decision is recorded once.
	secondResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/portal-customers/%d/erase", customerID), map[string]interface{}{
		"requested_by": "again",
	})
	defer secondResp.Body.Close()
	AssertStatusCode(t, secondResp, http.StatusConflict)

	// The deleted portal session no longer authenticates.
	orphanResp := MakePortalRequest(t, server, portalCookie, http.MethodGet,
		fmt.Sprintf("/portal/%s/requests/%d/comments", portalSlug, itemID), nil)
	defer orphanResp.Body.Close()
	AssertStatusCode(t, orphanResp, http.StatusUnauthorized)
}

// TestGBPDCustomerErasureTombstoneIsImmutable verifies WI-1580: an erased
// customer cannot be re-identified by an ordinary update or resurrected by an
// activation, and its deactivation marker stays set.
func TestGBPDCustomerErasureTombstoneIsImmutable(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "GDPR Tombstone WS", "")
	_, channelID := SetupPortalChannel(t, server, wsID)
	customerID, _ := CreatePortalCustomerWithSession(t, server, channelID, "Tombstone Subject", "tombstone@test.local")

	eraseResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/portal-customers/%d/erase", customerID), map[string]interface{}{
		"requested_by": "privacy@test.local",
		"requested_at": time.Now().UTC().Format(time.RFC3339),
		"notes":        "Art. 17 request approved",
	})
	AssertStatusCode(t, eraseResp, http.StatusCreated)
	_ = eraseResp.Body.Close()

	// A re-identifying write must not resurrect the tombstone.
	updateResp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/portal-customers/%d", customerID), map[string]interface{}{
		"name":  "Resurrected Subject",
		"email": "resurrected@test.local",
	})
	defer updateResp.Body.Close()
	AssertStatusCode(t, updateResp, http.StatusNotFound)

	var name, email string
	if err := server.DB().QueryRow(`SELECT name, email FROM portal_customers WHERE id = ?`, customerID).Scan(&name, &email); err != nil {
		t.Fatalf("read customer: %v", err)
	}
	if name != fmt.Sprintf("deleted-customer-%d", customerID) || email != fmt.Sprintf("deleted-customer-%d@erased.invalid", customerID) {
		t.Fatalf("erased customer was re-identified: name=%q email=%q", name, email)
	}

	// Activation cannot clear the erased state either.
	activateResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/portal-customers/%d/activate", customerID), nil)
	defer activateResp.Body.Close()
	AssertStatusCode(t, activateResp, http.StatusConflict)

	var erasedAt *time.Time
	var postActivationName, postActivationEmail string
	if err := server.DB().QueryRow(
		`SELECT erased_at, name, email FROM portal_customers WHERE id = ?`, customerID,
	).Scan(&erasedAt, &postActivationName, &postActivationEmail); err != nil {
		t.Fatalf("read tombstone: %v", err)
	}
	if erasedAt == nil {
		t.Fatal("activation cleared erased_at on an erased customer")
	}
	if postActivationName != fmt.Sprintf("deleted-customer-%d", customerID) || postActivationEmail != fmt.Sprintf("deleted-customer-%d@erased.invalid", customerID) {
		t.Fatalf("activation re-identified the tombstone: name=%q email=%q", postActivationName, postActivationEmail)
	}
}

// TestGBPDCustomerErasureDenials pins the access and validation contract of
// the erasure endpoint: only callers with customers.manage may execute a
// DSAR, the intake reference is required, and unknown customers 404.
func TestGBPDCustomerErasureDenials(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "GDPR Customer Erasure Denials WS", "")
	_, channelID := SetupPortalChannel(t, server, wsID)
	customerID, _ := CreatePortalCustomerWithSession(t, server, channelID, "Denial Subject", "gdprdenialcustomer@test.local")

	t.Run("non_privileged_user_cannot_erase", func(t *testing.T) {
		_, _, plainPass := CreateTestUserWithCredentials(t, server, "gdprplain", "gdprplain@test.local")
		plainSession, _ := CreateAuthCredentialsForUser(t, server, "gdprplain", plainPass)

		resp := MakeAuthRequestWithToken(t, server, plainSession, http.MethodPost, fmt.Sprintf("/portal-customers/%d/erase", customerID), map[string]interface{}{
			"requested_by": "someone",
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
	})

	t.Run("missing_intake_reference_is_rejected", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/portal-customers/%d/erase", customerID), map[string]interface{}{
			"requested_by": "",
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	t.Run("unknown_customer_is_not_found", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, "/portal-customers/999999999/erase", map[string]interface{}{
			"requested_by": "someone",
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNotFound)
	})
}

// TestGBPDCustomerDeleteAliasErases pins the legacy DELETE endpoint contract:
// it now executes the same erasure flow with a derived intake reference, so
// existing admin UI delete flows can no longer destroy ticket content.
func TestGBPDCustomerDeleteAliasErases(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "GDPR Customer Delete Alias WS", "")
	portalSlug, channelID := SetupPortalChannel(t, server, wsID)

	customerID, portalCookie := CreatePortalCustomerWithSession(t, server, channelID, "Alias Subject", "aliascustomer@test.local")
	itemID := SubmitPortalRequest(t, server, portalSlug, portalCookie, "Alias request survives delete")

	delResp := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/portal-customers/%d", customerID), nil)
	defer delResp.Body.Close()
	AssertStatusCode(t, delResp, http.StatusCreated)

	var name, email string
	if err := server.DB().QueryRow(`SELECT name, email FROM portal_customers WHERE id = ?`, customerID).
		Scan(&name, &email); err != nil {
		t.Fatalf("customer row must survive the delete alias: %v", err)
	}
	if name != fmt.Sprintf("deleted-customer-%d", customerID) || email != fmt.Sprintf("deleted-customer-%d@erased.invalid", customerID) {
		t.Errorf("delete alias did not pseudonymize: name=%q email=%q", name, email)
	}

	var itemCount int
	if err := server.DB().QueryRow(
		`SELECT COUNT(*) FROM items WHERE id = ?`, itemID,
	).Scan(&itemCount); err != nil {
		t.Fatalf("read item row: %v", err)
	}
	if itemCount != 1 {
		t.Errorf("items = %d, want 1 (delete alias must not destroy tickets)", itemCount)
	}

	// The derived intake reference records the acting administrator.
	var intakeRef string
	if err := server.DB().QueryRow(
		`SELECT requested_by FROM customer_erasure_records WHERE portal_customer_id = ?`, customerID,
	).Scan(&intakeRef); err != nil {
		t.Fatalf("erasure evidence missing after delete alias: %v", err)
	}
	if !strings.Contains(intakeRef, "admin:") {
		t.Errorf("requested_by = %q, want an admin-derived intake reference", intakeRef)
	}
}
