//go:build test

package services

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"windshift/internal/models"
)

// seedCustomerErasureFixture seeds the full customer data matrix the erasure
// flow touches: authentication state, drafts, channel grants, roles, ticket
// content, retained email rows, and an approval-routing assignment. Direct
// inserts are the norm for white-box service tests in this package.
func seedCustomerErasureFixture(t *testing.T, env *approvalTestEnv, customerID int) (channelID int, stepInstanceID int) {
	t.Helper()

	res, err := env.db.Exec(`INSERT INTO channels (name, type, direction, status) VALUES ('erasure-portal', 'portal', 'inbound', 'enabled')`)
	if err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	cid, _ := res.LastInsertId()
	channelID = int(cid)

	res, err = env.db.Exec(`INSERT INTO request_types (channel_id, name, item_type_id, workspace_id) VALUES (?, 'Erasure Request', ?, ?)`, channelID, env.itemTypeID, env.workspaceID)
	if err != nil {
		t.Fatalf("insert request type: %v", err)
	}
	requestTypeID, _ := res.LastInsertId()

	for _, stmt := range []struct {
		query string
		args  []any
		desc  string
	}{
		{`INSERT INTO portal_customer_sessions (portal_customer_id, session_token, channel_id, expires_at, ip_address, user_agent, is_active) VALUES (?, 'tok-expired', ?, ?, '10.0.0.1', 'ua', 1)`,
			[]any{customerID, channelID, time.Now().Add(-time.Hour)}, "expired session"},
		{`INSERT INTO portal_customer_sessions (portal_customer_id, session_token, channel_id, expires_at, ip_address, user_agent, is_active) VALUES (?, 'tok-live', ?, ?, '10.0.0.2', 'ua', 1)`,
			[]any{customerID, channelID, time.Now().Add(24 * time.Hour)}, "live session"},
		{`INSERT INTO portal_customer_magic_links (portal_customer_id, token, channel_id, expires_at) VALUES (?, 'ml-tok', ?, ?)`,
			[]any{customerID, channelID, time.Now().Add(time.Hour)}, "magic link"},
		{`INSERT INTO portal_webauthn_credentials (id, portal_customer_id, credential_name, public_key) VALUES ('cred-1', ?, 'passkey', X'00')`,
			[]any{customerID}, "passkey credential"},
		{`INSERT INTO portal_webauthn_sessions (id, portal_customer_id, challenge, session_data, session_type, expires_at) VALUES ('ws-1', ?, 'chal', '{}', 'authentication', ?)`,
			[]any{customerID, time.Now().Add(time.Hour)}, "passkey session"},
		{`INSERT INTO portal_request_drafts (channel_id, request_type_id, portal_customer_id, title, description) VALUES (?, ?, ?, 'draft', 'd')`,
			[]any{channelID, requestTypeID, customerID}, "request draft"},
		{`INSERT INTO portal_customer_channels (portal_customer_id, channel_id) VALUES (?, ?)`,
			[]any{customerID, channelID}, "channel grant"},
		{`INSERT INTO portal_customer_roles (portal_customer_id, contact_role_id) VALUES (?, (SELECT id FROM contact_roles LIMIT 1))`,
			[]any{customerID}, "contact role"},
		{`INSERT INTO comments (item_id, portal_customer_id, content) VALUES (?, ?, 'customer-authored context that outlives the account')`,
			[]any{env.itemID, customerID}, "customer comment"},
		{`INSERT INTO email_reply_outbox (comment_id, channel_id, item_id, to_email, to_name, subject, html_body, text_body, message_id, from_email) VALUES ((SELECT id FROM comments WHERE portal_customer_id = ?), ?, ?, 'subject@test.local', 'Erasure Subject', 're: ticket', '<p>re</p>', 're', 'msg-1', 'agent@test.local')`,
			[]any{customerID, channelID, env.itemID}, "reply outbox"},
		{`INSERT INTO email_message_tracking (channel_id, message_id, from_email, from_name, subject) VALUES (?, 'track-1', 'subject@test.local', 'Erasure Subject', 'ticket subject')`,
			[]any{channelID}, "email tracking"},
	} {
		if _, err := env.db.Exec(stmt.query, stmt.args...); err != nil {
			t.Fatalf("seed %s: %v", stmt.desc, err)
		}
	}

	// Identity attributes that erasure must scrub.
	if _, err := env.db.Exec(`UPDATE portal_customers SET phone = '+1 555 0100', custom_field_values = '{"tier":"gold"}' WHERE id = ?`, customerID); err != nil {
		t.Fatalf("seed customer attributes: %v", err)
	}

	// Approval routing: an item the customer created gets an approval request
	// whose resolved pool contains the customer.
	if _, err := env.db.Exec(`UPDATE items SET creator_id = NULL, creator_portal_customer_id = ? WHERE id = ?`, customerID, env.itemID); err != nil {
		t.Fatalf("flip item to customer-created: %v", err)
	}
	env.createApprovalSet(approvalSetSpec{
		steps: []approvalStepSpec{
			{displayOrder: 0, name: "Customer sign-off", quorumMode: models.ApprovalQuorumModeAny, source: models.ApprovalSourceCreator, allowSelf: true},
		},
	})
	req, err := env.approvalService.RequestApproval(context.Background(), env.itemID, env.statusReviewID, env.statusOpenID, env.requestor)
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	stepInstanceID = req.StepInstances[0].ID
	if got := len(req.StepInstances[0].Approvers); got != 1 {
		t.Fatalf("approver pool size = %d, want 1", got)
	}
	if req.StepInstances[0].Approvers[0].PortalCustomerID == nil {
		t.Fatalf("approver pool does not reference the portal customer")
	}
	return channelID, stepInstanceID
}

// TestCustomerErasure_ReleasesApprovalAssignmentsAndHardDeletesAuth pins the
// WI-1550 erasure matrix: approval assignments are released (the old hard
// delete failed with a raw 500 on the RESTRICT FK), authentication state is
// hard-deleted live or expired alike, retained ticket content and email rows
// survive de-identified, and the flow is irreversible with evidence.
func TestCustomerErasure_ReleasesApprovalAssignmentsAndHardDeletesAuth(t *testing.T) {
	env := newApprovalTestEnv(t)
	customerID := approvalPortalCustomer(t, env, "Erasure Subject", "subject@test.local")
	_, stepInstanceID := seedCustomerErasureFixture(t, env, customerID)

	pseudonymName := fmt.Sprintf("deleted-customer-%d", customerID)
	pseudonymEmail := fmt.Sprintf("deleted-customer-%d@erased.invalid", customerID)

	actor := AuditActor{UserID: env.approver1, Username: "approver1"}
	evidence, err := EraseCustomer(env.db, customerID, actor, CustomerErasureInput{
		RequestedBy: "subject@test.local via privacy@ channel",
		RequestedAt: time.Now().Add(-24 * time.Hour),
		Notes:       "Art. 17 request approved",
	})
	if err != nil {
		t.Fatalf("EraseCustomer: %v", err)
	}
	if evidence.CustomerID != customerID || evidence.PolicyVersion != CustomerErasurePolicyVersion || evidence.ApprovedBy != env.approver1 {
		t.Fatalf("evidence = %#v", evidence)
	}

	// The approval assignment is released, not destroyed: history stays
	// interpretable but the erased identity can never approve.
	var activeApprovers int
	if err := env.db.QueryRow(
		`SELECT COUNT(*) FROM approval_step_approvers WHERE approval_step_instance_id = ? AND is_active = 1`, stepInstanceID,
	).Scan(&activeApprovers); err != nil {
		t.Fatalf("count active approvers: %v", err)
	}
	if activeApprovers != 0 {
		t.Errorf("active approvers = %d, want 0 (erasure must release assignments)", activeApprovers)
	}
	var approverRows, retainedCustomerRef int
	if err := env.db.QueryRow(
		`SELECT COUNT(*), COALESCE(MAX(portal_customer_id), 0) FROM approval_step_approvers WHERE approval_step_instance_id = ?`, stepInstanceID,
	).Scan(&approverRows, &retainedCustomerRef); err != nil {
		t.Fatalf("count approver rows: %v", err)
	}
	if approverRows != 1 || retainedCustomerRef != customerID {
		t.Errorf("approver rows = %d referencing %d, want the row retained under customer %d", approverRows, retainedCustomerRef, customerID)
	}

	// Authentication state is gone, live and expired alike.
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
		if err := env.db.QueryRow(`SELECT COUNT(*) FROM `+check.table+` WHERE portal_customer_id = ?`, customerID).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", check.table, err)
		}
		if count != 0 {
			t.Errorf("%s rows = %d, want 0 (%s must be hard-deleted)", check.table, count, check.desc)
		}
	}

	// The row is pseudonymized with links severed.
	var name, email string
	var phone, customFields, orgID, userID sql.NullString
	var erasedAt sql.NullTime
	if err := env.db.QueryRow(`
		SELECT name, email, phone, custom_field_values, customer_organisation_id, user_id, erased_at
		FROM portal_customers WHERE id = ?`, customerID,
	).Scan(&name, &email, &phone, &customFields, &orgID, &userID, &erasedAt); err != nil {
		t.Fatalf("read customer row: %v", err)
	}
	if !erasedAt.Valid {
		t.Errorf("erased_at not set")
	}
	if name != pseudonymName || email != pseudonymEmail {
		t.Errorf("pseudonym mismatch: name=%q email=%q, want %q / %q", name, email, pseudonymName, pseudonymEmail)
	}
	if phone.Valid || customFields.Valid || orgID.Valid || userID.Valid {
		t.Errorf("personal attributes or links survived: phone=%v custom=%v org=%v user=%v", phone, customFields, orgID, userID)
	}

	// Retained content: comment intact under the pseudonym.
	var commentContent string
	if err := env.db.QueryRow(
		`SELECT content FROM comments WHERE portal_customer_id = ?`, customerID,
	).Scan(&commentContent); err != nil {
		t.Fatalf("customer comment lost during erasure: %v", err)
	}
	if commentContent != "customer-authored context that outlives the account" {
		t.Errorf("comment content changed: %q", commentContent)
	}

	// The reply queue row is retained but no longer addressed to a real
	// mailbox.
	var toEmail, toName string
	if err := env.db.QueryRow(
		`SELECT to_email, to_name FROM email_reply_outbox WHERE message_id = 'msg-1'`,
	).Scan(&toEmail, &toName); err != nil {
		t.Fatalf("reply outbox row lost during erasure: %v", err)
	}
	if toEmail != pseudonymEmail || toName != pseudonymName {
		t.Errorf("outbox recipient not de-identified: to_email=%q to_name=%q", toEmail, toName)
	}

	// The intake-tracking row loses the erased sender identity but keeps the
	// ticket subject.
	var fromEmail string
	var subject sql.NullString
	if err := env.db.QueryRow(
		`SELECT from_email, subject FROM email_message_tracking WHERE message_id = 'track-1'`,
	).Scan(&fromEmail, &subject); err != nil {
		t.Fatalf("email tracking row lost during erasure: %v", err)
	}
	if fromEmail != pseudonymEmail {
		t.Errorf("tracking sender not de-identified: %q", fromEmail)
	}
	if !subject.Valid || subject.String != "ticket subject" {
		t.Errorf("tracking subject must be retained, got %v", subject)
	}

	// Evidence and audit snapshot.
	var evidenceRows int
	if err := env.db.QueryRow(
		`SELECT COUNT(*) FROM customer_erasure_records WHERE portal_customer_id = ? AND requested_by = ?`, customerID, "subject@test.local via privacy@ channel",
	).Scan(&evidenceRows); err != nil {
		t.Fatalf("query erasure evidence: %v", err)
	}
	if evidenceRows != 1 {
		t.Errorf("customer_erasure_records rows = %d, want 1", evidenceRows)
	}
	var auditDetails string
	if err := env.db.QueryRow(
		`SELECT details FROM audit_logs WHERE action_type = 'portal_customer.erase' AND resource_id = ?`, customerID,
	).Scan(&auditDetails); err != nil {
		t.Fatalf("erasure audit row missing: %v", err)
	}
	for _, want := range []string{"subject@test.local", "Erasure Subject", "+1 555 0100"} {
		if !strings.Contains(auditDetails, want) {
			t.Errorf("audit snapshot missing %q: %s", want, auditDetails)
		}
	}

	// Repeat erasure is refused.
	if _, err := EraseCustomer(env.db, customerID, actor, CustomerErasureInput{RequestedBy: "again"}); err != ErrCustomerAlreadyErased {
		t.Fatalf("repeat erasure error = %v, want ErrCustomerAlreadyErased", err)
	}
}

// TestCustomerErasure_Validations pins the input contract: missing intake
// reference is a 400-class ServiceError and unknown customers are 404.
func TestCustomerErasure_Validations(t *testing.T) {
	env := newApprovalTestEnv(t)
	customerID := approvalPortalCustomer(t, env, "Validation Subject", "validation@test.local")

	if _, err := EraseCustomer(env.db, customerID, AuditActor{UserID: env.approver1}, CustomerErasureInput{}); err == nil {
		t.Fatal("missing requested_by must be rejected")
	} else if se, ok := err.(*ServiceError); !ok || se.StatusCode != 400 {
		t.Fatalf("missing requested_by error = %v, want 400 ServiceError", err)
	}

	if _, err := EraseCustomer(env.db, 999999999, AuditActor{UserID: env.approver1}, CustomerErasureInput{RequestedBy: "someone"}); err == nil {
		t.Fatal("unknown customer must be rejected")
	} else if se, ok := err.(*ServiceError); !ok || se.StatusCode != 404 {
		t.Fatalf("unknown customer error = %v, want 404 ServiceError", err)
	}

	// Nothing was written by the rejected calls.
	var rowCount int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM customer_erasure_records`).Scan(&rowCount); err != nil {
		t.Fatalf("count evidence rows: %v", err)
	}
	if rowCount != 0 {
		t.Errorf("customer_erasure_records rows = %d, want 0 after rejected calls", rowCount)
	}
}

// TestCustomerErasure_DeidentifiesOutboxRecipientsAndCancelsPending proves
// erasure matches retained outbound-email rows by RECIPIENT address — the
// production shape, where staff-authored replies carry the customer in
// to_email — cancels queued deliveries to the erased identity, and
// pseudonymizes delivered rows while their ticket history is retained
// (WI-1579).
func TestCustomerErasure_DeidentifiesOutboxRecipientsAndCancelsPending(t *testing.T) {
	env := newApprovalTestEnv(t)
	customerID := approvalPortalCustomer(t, env, "Outbox Subject", "outbox-subject@test.local")

	// Staff-authored replies on a shared ticket: production outbox rows are
	// staff-authored, so the comment author is not the customer.
	var staffID int
	if err := env.db.QueryRow(`SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&staffID); err != nil {
		t.Fatalf("load staff user: %v", err)
	}
	var channelID int
	if err := env.db.QueryRow(`INSERT INTO channels (name, type, direction, status) VALUES ('erasure-email', 'email', 'inbound', 'enabled') RETURNING id`).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	seed := []struct {
		desc  string
		query string
		args  []any
	}{
		{"staff comment one", `INSERT INTO comments (item_id, author_id, content) VALUES (?, ?, 'agent reply one')`,
			[]any{env.itemID, staffID}},
		{"staff comment two", `INSERT INTO comments (item_id, author_id, content) VALUES (?, ?, 'agent reply two')`,
			[]any{env.itemID, staffID}},
		{"pending outbox row", `INSERT INTO email_reply_outbox (comment_id, channel_id, item_id, to_email, to_name, subject, html_body, text_body, message_id, from_email, next_attempt_at)
			VALUES ((SELECT id FROM comments WHERE author_id = ? AND item_id = ? AND content = 'agent reply one'), ?, ?, 'outbox-subject@test.local', 'Outbox Subject', 're: ticket', '<p>re</p>', 're', 'msg-pending', 'agent@test.local', CURRENT_TIMESTAMP)`,
			[]any{staffID, env.itemID, channelID, env.itemID}},
		{"delivered outbox row", `INSERT INTO email_reply_outbox (comment_id, channel_id, item_id, to_email, to_name, subject, html_body, text_body, message_id, from_email, delivered_at)
			VALUES ((SELECT id FROM comments WHERE author_id = ? AND item_id = ? AND content = 'agent reply two'), ?, ?, 'outbox-subject@test.local', 'Outbox Subject', 're: ticket', '<p>re</p>', 're', 'msg-delivered', 'agent@test.local', CURRENT_TIMESTAMP)`,
			[]any{staffID, env.itemID, channelID, env.itemID}},
	}
	for _, stmt := range seed {
		if _, err := env.db.Exec(stmt.query, stmt.args...); err != nil {
			t.Fatalf("seed %s: %v", stmt.desc, err)
		}
	}
	actor := AuditActor{UserID: env.approver1, Username: "approver1"}
	if _, err := EraseCustomer(env.db, customerID, actor, CustomerErasureInput{
		RequestedBy: "outbox-subject@test.local via privacy@ channel",
		RequestedAt: time.Now().Add(-24 * time.Hour),
	}); err != nil {
		t.Fatalf("EraseCustomer: %v", err)
	}

	pseudonymEmail := fmt.Sprintf("deleted-customer-%d@erased.invalid", customerID)
	type row struct {
		toEmail    string
		toName     string
		discarded  *time.Time
		delivered  *time.Time
		lastError  *string
		staffEmail string
	}
	rows := map[string]row{}
	ROS, err := env.db.Query(`
		SELECT message_id, to_email, to_name, discarded_at, delivered_at, last_error, from_email
		FROM email_reply_outbox WHERE message_id IN ('msg-pending', 'msg-delivered')
	`)
	if err != nil {
		t.Fatalf("read outbox rows: %v", err)
	}
	defer ROS.Close()
	for ROS.Next() {
		var r row
		var msgID string
		if err := ROS.Scan(&msgID, &r.toEmail, &r.toName, &r.discarded, &r.delivered, &r.lastError, &r.staffEmail); err != nil {
			t.Fatalf("scan outbox row: %v", err)
		}
		rows[msgID] = r
	}
	if err := ROS.Err(); err != nil {
		t.Fatalf("iterate outbox rows: %v", err)
	}

	pending, ok := rows["msg-pending"]
	if !ok {
		t.Fatal("pending row missing")
	}
	if pending.discarded == nil {
		t.Fatal("queued delivery to the erased customer was not canceled; mail could still go out")
	}
	if pending.toEmail != pseudonymEmail {
		t.Fatalf("pending row to_email = %q, want pseudonym %q", pending.toEmail, pseudonymEmail)
	}
	if pending.lastError == nil || !strings.Contains(*pending.lastError, "erased") {
		t.Fatalf("pending row last_error = %v, want a cancellation note", pending.lastError)
	}

	delivered, ok := rows["msg-delivered"]
	if !ok {
		t.Fatal("delivered row missing")
	}
	if delivered.discarded != nil {
		t.Fatal("delivered history row must not be discarded")
	}
	if delivered.toEmail != pseudonymEmail {
		t.Fatalf("delivered row to_email = %q, want pseudonym %q", delivered.toEmail, pseudonymEmail)
	}
	// The staff author keeps their identity; only the erased recipient and
	// erased senders are de-identified.
	if delivered.staffEmail != "agent@test.local" {
		t.Fatalf("staff sender email = %q, want agent@test.local", delivered.staffEmail)
	}
}

// TestCustomerErasure_DeidentifiesTrackingRowsRegardlessOfEmailCase is the
// regression for the GDPR leak where the intake stored the sender address with
// the case from the From header while the customer profile was lowercased, so
// the case-sensitive erasure match left the address behind.
func TestCustomerErasure_DeidentifiesTrackingRowsRegardlessOfEmailCase(t *testing.T) {
	env := newApprovalTestEnv(t)
	customerID := approvalPortalCustomer(t, env, "Case Subject", "case.subject@test.local")

	var channelID int
	if err := env.db.QueryRow(
		`INSERT INTO channels (name, type, direction, status) VALUES ('erasure-case', 'email', 'inbound', 'enabled') RETURNING id`,
	).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	// The From header preserved uppercase letters; the profile email above is
	// lowercase, exactly the mismatch that used to survive erasure.
	if _, err := env.db.Exec(`
		INSERT INTO email_message_tracking (channel_id, message_id, from_email, from_name, subject)
		VALUES (?, 'track-mixed-case', 'Case.Subject@TEST.Local', 'Case Subject', 'ticket subject')
	`, channelID); err != nil {
		t.Fatalf("seed tracking row: %v", err)
	}

	pseudonymEmail := fmt.Sprintf("deleted-customer-%d@erased.invalid", customerID)
	pseudonymName := fmt.Sprintf("deleted-customer-%d", customerID)
	if _, err := EraseCustomer(env.db, customerID, AuditActor{UserID: env.approver1, Username: "approver1"}, CustomerErasureInput{
		RequestedBy: "case.subject@test.local via privacy@ channel",
	}); err != nil {
		t.Fatalf("EraseCustomer: %v", err)
	}

	var fromEmail, fromName, subject string
	if err := env.db.QueryRow(
		`SELECT from_email, COALESCE(from_name, ''), COALESCE(subject, '') FROM email_message_tracking WHERE message_id = 'track-mixed-case'`,
	).Scan(&fromEmail, &fromName, &subject); err != nil {
		t.Fatalf("read tracking row: %v", err)
	}
	if fromEmail != pseudonymEmail {
		t.Errorf("tracking sender survived erasure: from_email = %q, want %q", fromEmail, pseudonymEmail)
	}
	if fromName != pseudonymName {
		t.Errorf("tracking sender name survived erasure: from_name = %q, want %q", fromName, pseudonymName)
	}
	// The ticket subject is retained as part of the controller's record.
	if subject != "ticket subject" {
		t.Errorf("tracking subject = %q, want it retained", subject)
	}
}
