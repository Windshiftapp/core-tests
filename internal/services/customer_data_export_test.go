//go:build test

package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"windshift/internal/models"
)

// TestCustomerDataExport_CoversAllCategories pins the export payload against
// a fully seeded customer on the white-box layer, covering the categories the
// production HTTP API cannot easily produce (approval decisions, email
// tracking) and the determinism contract: identical inputs marshal to
// identical JSON (modulo the export timestamp), sections id-ordered, empty
// sections as [].
func TestCustomerDataExport_CoversAllCategories(t *testing.T) {
	env := newApprovalTestEnv(t)
	customerID := approvalPortalCustomer(t, env, "Export Subject", "subject@test.local")
	_, stepInstanceID := seedCustomerErasureFixture(t, env, customerID)

	// Submitted request bodies and custom-field values are personal data the
	// export must disclose (WI-1581).
	if _, err := env.db.Exec(`
		UPDATE items SET description = 'submitted request body', custom_field_values = '{"7":"secret detail"}' WHERE id = ?
	`, env.itemID); err != nil {
		t.Fatalf("seed item body: %v", err)
	}

	// Produce an approval decision through the production approval service
	// path against the request the fixture already created (one open request
	// per item), so the export sees a real decision row.
	var requestID int
	if err := env.db.QueryRow(
		`SELECT approval_request_id FROM approval_step_instances WHERE id = ?`, stepInstanceID,
	).Scan(&requestID); err != nil {
		t.Fatalf("resolve approval request: %v", err)
	}
	if _, out, err := env.approvalService.DecideAsCustomer(context.Background(), requestID, customerID, models.ApprovalDecisionApprove, "looks good to me", DecideOptions{}); err != nil {
		t.Fatalf("DecideAsCustomer: %v", err)
	} else if out.Status != models.ApprovalRequestStatusApproved {
		t.Fatalf("approval status = %s, want approved", out.Status)
	}
	actor := AuditActor{UserID: env.approver1, Username: "approver1"}
	export, err := ExportCustomerData(env.db, customerID, actor, []int{env.workspaceID})
	if err != nil {
		t.Fatalf("ExportCustomerData: %v", err)
	}

	if export.SchemaVersion != CustomerDataExportSchemaVersion {
		t.Errorf("schema_version = %d, want %d", export.SchemaVersion, CustomerDataExportSchemaVersion)
	}

	// Profile: identity attributes, roles, and custom fields.
	if export.Customer.ID != customerID || export.Customer.Name != "Export Subject" || export.Customer.Email != "subject@test.local" {
		t.Errorf("profile = %#v", export.Customer)
	}
	if export.Customer.Phone == nil || *export.Customer.Phone != "+1 555 0100" {
		t.Errorf("profile phone = %v, want +1 555 0100", export.Customer.Phone)
	}
	if len(export.Customer.Roles) != 1 || export.Customer.Roles[0] != "Portal Customer" {
		t.Errorf("roles = %v, want [Portal Customer]", export.Customer.Roles)
	}
	if string(export.Customer.CustomFieldValues) != `{"tier":"gold"}` {
		t.Errorf("custom_field_values = %s", export.Customer.CustomFieldValues)
	}

	// The requested item carries the workspace display key.
	if len(export.RequestedItems) != 1 {
		t.Fatalf("requested_items = %d, want 1", len(export.RequestedItems))
	}
	item := export.RequestedItems[0]
	if item.ID != env.itemID || item.WorkspaceKey != "TWS" || item.ItemNumber == 0 {
		t.Errorf("requested item = %#v", item)
	}
	if item.ItemKey != fmt.Sprintf("%s-%d", item.WorkspaceKey, item.ItemNumber) {
		t.Errorf("item_key = %q, want display key", item.ItemKey)
	}
	if item.Description != "submitted request body" {
		t.Errorf("item description = %q, want the submitted body", item.Description)
	}
	if string(item.CustomFieldValues) != `{"7":"secret detail"}` {
		t.Errorf("item custom_field_values = %s, want the submitted values", item.CustomFieldValues)
	}

	// Unfinished drafts are the customer's personal data too.
	if len(export.Drafts) != 1 {
		t.Fatalf("drafts = %d, want 1", len(export.Drafts))
	}
	if export.Drafts[0].Title != "draft" || export.Drafts[0].Description != "d" {
		t.Errorf("draft = %#v, want the seeded draft", export.Drafts[0])
	}

	// Authored comments.
	if len(export.Comments) != 1 || export.Comments[0].Content != "customer-authored context that outlives the account" {
		t.Fatalf("comments = %#v", export.Comments)
	}

	// Approval decisions authored by the customer.
	if len(export.ApprovalDecisions) != 1 {
		t.Fatalf("approval_decisions = %d, want 1", len(export.ApprovalDecisions))
	}
	decision := export.ApprovalDecisions[0]
	if decision.Decision != string(models.ApprovalDecisionApprove) || decision.Comment == nil || *decision.Comment != "looks good to me" {
		t.Errorf("approval decision = %#v", decision)
	}

	// Attachments and kb_events are empty sections, not null.
	if export.Attachments == nil || len(export.Attachments) != 0 {
		t.Errorf("attachments = %#v, want empty slice", export.Attachments)
	}
	if export.KBEvents == nil || len(export.KBEvents) != 0 {
		t.Errorf("kb_events = %#v, want empty slice", export.KBEvents)
	}

	// Email tracking rows referencing the subject's address.
	if len(export.EmailTracking) != 1 {
		t.Fatalf("email_tracking = %d, want 1", len(export.EmailTracking))
	}
	tracking := export.EmailTracking[0]
	if tracking.FromEmail != "subject@test.local" || tracking.Subject == nil || *tracking.Subject != "ticket subject" {
		t.Errorf("email tracking = %#v", tracking)
	}

	// Session metadata in id order, tokens never present.
	if len(export.Sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(export.Sessions))
	}
	if export.Sessions[0].ID >= export.Sessions[1].ID {
		t.Errorf("sessions not id-ordered: %d then %d", export.Sessions[0].ID, export.Sessions[1].ID)
	}
	if export.Sessions[0].IPAddress == nil || *export.Sessions[0].IPAddress != "10.0.0.1" {
		t.Errorf("session ip = %v, want 10.0.0.1", export.Sessions[0].IPAddress)
	}
	raw, err := json.Marshal(export)
	if err != nil {
		t.Fatalf("marshal export: %v", err)
	}
	for _, forbidden := range []string{"session_token", "token", "file_path"} {
		if containsJSONKey(string(raw), forbidden) {
			t.Errorf("export leaks %q: %s", forbidden, raw)
		}
	}

	// Determinism: a second export differs only in the timestamp.
	second, err := ExportCustomerData(env.db, customerID, actor, []int{env.workspaceID})
	if err != nil {
		t.Fatalf("second ExportCustomerData: %v", err)
	}
	export.ExportedAt, second.ExportedAt = "", ""
	first, err := json.Marshal(export)
	if err != nil {
		t.Fatalf("marshal first export: %v", err)
	}
	secondBytes, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second export: %v", err)
	}
	if string(first) != string(secondBytes) {
		t.Errorf("repeated export is not deterministic:\nfirst  %s\nsecond %s", first, secondBytes)
	}

	// The disclosure is audited with section sizes.
	var auditDetails string
	if err := env.db.QueryRow(
		`SELECT details FROM audit_logs WHERE action_type = 'portal_customer.data_export' AND resource_id = ?`, customerID,
	).Scan(&auditDetails); err != nil {
		t.Fatalf("data export audit row missing: %v", err)
	}
	for _, want := range []string{"subject@test.local", `"sessions":2`, `"comments":1`} {
		if !strings.Contains(auditDetails, want) {
			t.Errorf("audit details missing %q: %s", want, auditDetails)
		}
	}
}

// TestCustomerDataExport_UnknownCustomer pins the 404 contract at the service
// layer.
func TestCustomerDataExport_UnknownCustomer(t *testing.T) {
	env := newApprovalTestEnv(t)
	if _, err := ExportCustomerData(env.db, 999999999, AuditActor{UserID: env.approver1}, nil); err == nil {
		t.Fatal("unknown customer must be rejected")
	} else if se, ok := err.(*ServiceError); !ok || se.StatusCode != 404 {
		t.Fatalf("unknown customer error = %v, want 404 ServiceError", err)
	}
}

// containsJSONKey reports whether the marshaled payload contains the JSON key.
func containsJSONKey(payload, key string) bool {
	return strings.Contains(payload, `"`+key+`"`)
}

// TestCustomerDataExport_IncludesTrackingRowsRegardlessOfEmailCase is the
// regression for the Article 15 gap where the intake stored the sender address
// with the case from the From header while the profile email was lowercased,
// so the case-sensitive export query omitted the subject's own email rows.
func TestCustomerDataExport_IncludesTrackingRowsRegardlessOfEmailCase(t *testing.T) {
	env := newApprovalTestEnv(t)
	customerID := approvalPortalCustomer(t, env, "Case Export Subject", "case.export@test.local")

	var channelID int
	if err := env.db.QueryRow(
		`INSERT INTO channels (name, type, direction, status) VALUES ('export-case', 'email', 'inbound', 'enabled') RETURNING id`,
	).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if _, err := env.db.Exec(`
		INSERT INTO email_message_tracking (channel_id, message_id, from_email, from_name, subject)
		VALUES (?, 'export-track-mixed-case', 'Case.Export@TEST.Local', 'Case Export Subject', 'exported subject')
	`, channelID); err != nil {
		t.Fatalf("seed tracking row: %v", err)
	}

	export, err := ExportCustomerData(env.db, customerID, AuditActor{UserID: env.approver1, Username: "approver1"}, []int{env.workspaceID})
	if err != nil {
		t.Fatalf("ExportCustomerData: %v", err)
	}

	if len(export.EmailTracking) != 1 {
		t.Fatalf("email_tracking = %d, want 1 (the subject's own message)", len(export.EmailTracking))
	}
	tracking := export.EmailTracking[0]
	if tracking.MessageID != "export-track-mixed-case" || tracking.FromEmail != "Case.Export@TEST.Local" {
		t.Errorf("email tracking = %#v, want the mixed-case sender row", tracking)
	}
	if tracking.Subject == nil || *tracking.Subject != "exported subject" {
		t.Errorf("email tracking subject = %v, want exported subject", tracking.Subject)
	}
}

// TestCustomerDataExport_ScopesWorkspaceSections pins that the export only
// discloses workspace-scoped personal data (requested items, comments) from
// workspaces the caller can view, while still returning the customer profile.
func TestCustomerDataExport_ScopesWorkspaceSections(t *testing.T) {
	env := newApprovalTestEnv(t)
	customerID := approvalPortalCustomer(t, env, "Scoped Subject", "scoped@test.local")

	// Visible workspace content: an item and comment in env.workspaceID.
	visibleItemID, err := CreateItem(env.db, ItemCreationParams{
		WorkspaceID:             env.workspaceID,
		Title:                   "Visible export item",
		Description:             "visible body",
		CreatorPortalCustomerID: &customerID,
	})
	if err != nil {
		t.Fatalf("create visible item: %v", err)
	}
	if _, err := env.db.Exec(
		`INSERT INTO comments (item_id, portal_customer_id, content) VALUES (?, ?, 'visible comment')`,
		visibleItemID, customerID,
	); err != nil {
		t.Fatalf("seed visible comment: %v", err)
	}

	// Hidden workspace content: a second workspace the caller cannot read.
	var hiddenWorkspaceID int
	if err := env.db.QueryRow(
		`INSERT INTO workspaces (name, key) VALUES ('Hidden export workspace', 'HEX') RETURNING id`,
	).Scan(&hiddenWorkspaceID); err != nil {
		t.Fatalf("insert hidden workspace: %v", err)
	}
	hiddenItemID, err := CreateItem(env.db, ItemCreationParams{
		WorkspaceID:             hiddenWorkspaceID,
		Title:                   "Hidden export item",
		Description:             "hidden body",
		CreatorPortalCustomerID: &customerID,
	})
	if err != nil {
		t.Fatalf("create hidden item: %v", err)
	}
	if _, err := env.db.Exec(
		`INSERT INTO comments (item_id, portal_customer_id, content) VALUES (?, ?, 'hidden comment')`,
		hiddenItemID, customerID,
	); err != nil {
		t.Fatalf("seed hidden comment: %v", err)
	}

	export, err := ExportCustomerData(env.db, customerID, AuditActor{UserID: env.approver1, Username: "approver1"}, []int{env.workspaceID})
	if err != nil {
		t.Fatalf("ExportCustomerData: %v", err)
	}

	if len(export.RequestedItems) != 1 || export.RequestedItems[0].ID != int(visibleItemID) {
		t.Fatalf("requested_items = %#v, want only the visible item", export.RequestedItems)
	}
	if len(export.Comments) != 1 || export.Comments[0].Content != "visible comment" {
		t.Fatalf("comments = %#v, want only the visible comment", export.Comments)
	}

	// An empty scope discloses nothing workspace-scoped but keeps the profile.
	empty, err := ExportCustomerData(env.db, customerID, AuditActor{UserID: env.approver1, Username: "approver1"}, []int{})
	if err != nil {
		t.Fatalf("ExportCustomerData (empty scope): %v", err)
	}
	if len(empty.RequestedItems) != 0 || len(empty.Comments) != 0 {
		t.Fatalf("empty scope disclosed workspace content: items=%d comments=%d", len(empty.RequestedItems), len(empty.Comments))
	}
	if empty.Customer.ID != customerID {
		t.Fatalf("empty scope dropped the profile: %#v", empty.Customer)
	}
}
