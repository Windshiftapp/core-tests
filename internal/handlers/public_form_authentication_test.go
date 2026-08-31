//go:build test

package handlers

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/auth"
	"windshift/internal/middleware"
)

func authenticatedFormRequest(requestTypeID int) *http.Request {
	body := fmt.Sprintf(`{
		"request_type_id":%d,
		"title":"Authenticated request",
		"description":"Identity is required",
		"custom_fields":{}
	}`, requestTypeID)
	request := httptest.NewRequest(http.MethodPost, "/api/forms/attachment-form/submit", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "attachment-form")
	return request
}

func TestAuthenticatedPublicFormRejectsAnonymousSubmission(t *testing.T) {
	fixture := newPublicFormAttachmentFixture(t, t.TempDir(), false)
	if _, err := fixture.db.ExecWrite(`UPDATE request_types SET config = '{"require_auth":true}' WHERE id = ?`, fixture.requestTypeID); err != nil {
		t.Fatalf("enable require_auth: %v", err)
	}
	recorder := httptest.NewRecorder()

	fixture.handler.SubmitForm(recorder, authenticatedFormRequest(fixture.requestTypeID))

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", recorder.Code, recorder.Body.String())
	}
	if count := fixture.rowCount(t, "items"); count != 0 {
		t.Fatalf("item count = %d, want 0", count)
	}
}

func TestAuthenticatedPublicFormAcceptsInternalAndPortalIdentities(t *testing.T) {
	fixture := newPublicFormAttachmentFixture(t, t.TempDir(), false)
	if _, err := fixture.db.ExecWrite(`UPDATE request_types SET config = '{"require_auth":true}' WHERE id = ?`, fixture.requestTypeID); err != nil {
		t.Fatalf("enable require_auth: %v", err)
	}

	var userID int
	if err := fixture.db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('form-user@example.test', 'form-user', 'Form', 'User', true) RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	internalRequest := authenticatedFormRequest(fixture.requestTypeID)
	internalRequest = internalRequest.WithContext(context.WithValue(
		internalRequest.Context(),
		middleware.ContextKeySession,
		&auth.Session{UserID: userID},
	))
	internalRecorder := httptest.NewRecorder()
	fixture.handler.SubmitForm(internalRecorder, internalRequest)
	if internalRecorder.Code != http.StatusCreated {
		t.Fatalf("internal status = %d, want 201; body=%s", internalRecorder.Code, internalRecorder.Body.String())
	}

	var customerID int
	if err := fixture.db.QueryRow(`
		INSERT INTO portal_customers (email, name)
		VALUES ('form-customer@example.test', 'Form Customer') RETURNING id
	`).Scan(&customerID); err != nil {
		t.Fatalf("insert portal customer: %v", err)
	}
	customerRequest := authenticatedFormRequest(fixture.requestTypeID)
	customerRequest = customerRequest.WithContext(context.WithValue(
		customerRequest.Context(),
		middleware.ContextKeyPortalCustomerID,
		customerID,
	))
	customerRecorder := httptest.NewRecorder()
	fixture.handler.SubmitForm(customerRecorder, customerRequest)
	if customerRecorder.Code != http.StatusCreated {
		t.Fatalf("portal customer status = %d, want 201; body=%s", customerRecorder.Code, customerRecorder.Body.String())
	}

	if count := fixture.rowCount(t, "items"); count != 2 {
		t.Fatalf("item count = %d, want 2", count)
	}
	var auditCount int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action_type = 'form.submit'`).Scan(&auditCount); err != nil {
		t.Fatalf("count form submission audits: %v", err)
	}
	if auditCount != 0 {
		t.Fatalf("public form submissions emitted %d administrative audit rows, want 0", auditCount)
	}
}
